package server

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

func TestHTTPDomainDispatchPriorityAndOriginalAuthority(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
			rules := []struct{ name, domain string }{
				{"broad", "*.*.*.x.com"}, {"middle", "*.*.eu.x.com"},
				{"specific", "*.dev.eu.x.com"}, {"exact", "shop.dev.eu.x.com"},
			}
			for n := range rules {
				i := n
				if reverse {
					i = len(rules) - 1 - n
				}
				rule := rules[i]
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Route", rule.name)
					w.Header().Set("X-Seen-Host", r.Host)
					w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
					w.Header().Set("X-Seen-URI", r.RequestURI)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(backend.Close)
				t.Cleanup(addLiveHTTPDispatchTunnel(t, s, "client-"+rule.name, rule.name, rule.domain, backend.Listener.Addr()))
			}
			handler := s.StartHTTPOnly()
			for _, tc := range []struct{ host, route string }{
				{"SHOP.DEV.EU.X.COM.:8443", "exact"},
				{"other.dev.eu.x.com:443", "specific"},
				{"other.prod.eu.x.com", "middle"},
				{"other.prod.us.x.com", "broad"},
				{"x.com", ""}, {"a.b.x.com", ""}, {"a.b.c.d.x.com", ""},
				{"shop.dev.eu.x.com.evil.com", ""},
			} {
				req := newManagementRequest(http.MethodPost, "/api/orders?tenant=1", tc.host, strings.NewReader("payload"))
				req.Header.Set("X-Forwarded-Host", "spoofed.example.com")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if tc.route == "" {
					if w.Code != http.StatusNotFound {
						t.Fatalf("unmatched %q: got %d", tc.host, w.Code)
					}
					continue
				}
				if w.Code != http.StatusNoContent || w.Header().Get("X-Route") != tc.route {
					t.Fatalf("%q: status=%d route=%q, want %q; %s", tc.host, w.Code, w.Header().Get("X-Route"), tc.route, w.Body.String())
				}
				for _, header := range []string{"X-Seen-Host", "X-Seen-Forwarded-Host"} {
					if got := w.Header().Get(header); got != tc.host {
						t.Fatalf("%s=%q want original authority %q", header, got, tc.host)
					}
				}
				if got := w.Header().Get("X-Seen-URI"); got != "/api/orders?tenant=1" {
					t.Fatalf("request URI changed: %q", got)
				}
			}
		})
	}
}

func TestHTTPDomainDispatchUnavailableSpecificNeverFallsBack(t *testing.T) {
	for _, specific := range []string{"shop.dev.x.com", "*.dev.x.com"} {
		for _, failure := range []string{"offline", "no data", "pending", "error", "stopped", "closed activation", "stale revision", "different owner", "auth", "acl", "backend"} {
			t.Run(specific+"/"+failure, func(t *testing.T) {
				s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
				var broadHits atomic.Int32
				broad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					broadHits.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(broad.Close)
				t.Cleanup(addLiveHTTPDispatchTunnel(t, s, "broad-client", "broad", "*.*.x.com", broad.Listener.Addr()))
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(backend.Close)
				t.Cleanup(addLiveHTTPDispatchTunnel(t, s, "specific-client", "specific", specific, backend.Listener.Addr()))
				value, _ := s.clients.Load("specific-client")
				client := value.(*ClientConn)
				want := http.StatusServiceUnavailable
				switch failure {
				case "offline":
					s.clients.Delete(client.ID)
				case "no data":
					client.dataMu.Lock()
					client.dataSession = nil
					client.dataMu.Unlock()
				case "pending", "error", "stale revision", "closed activation":
					client.proxyMu.Lock()
					tunnel := client.proxies["specific"]
					switch failure {
					case "pending":
						tunnel.Config.RuntimeState = protocol.ProxyRuntimeStatePending
					case "error":
						tunnel.Config.RuntimeState = protocol.ProxyRuntimeStateError
					case "stale revision":
						tunnel.Config.Revision = 999
					case "closed activation":
						close(tunnel.done)
					}
					client.proxyMu.Unlock()
				case "stopped":
					if err := s.store.UpdateStates(client.ID, "specific", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
						t.Fatal(err)
					}
				case "different owner":
					client.OwnerUserID = "another-owner"
				case "auth":
					setHTTPDispatchBasicAuth(t, s, client.ID, "specific", "alice", "secret")
					want = http.StatusUnauthorized
				case "acl":
					setHTTPDispatchSourceCIDRs(t, s, client.ID, "specific", []string{"203.0.113.0/24"})
					want = http.StatusForbidden
				case "backend":
					backend.Close()
					want = http.StatusBadGateway
				}
				req := newManagementRequest(http.MethodGet, "/", "shop.dev.x.com", nil)
				req.RemoteAddr = "198.51.100.2:12345"
				w := httptest.NewRecorder()
				s.StartHTTPOnly().ServeHTTP(w, req)
				if w.Code != want || broadHits.Load() != 0 {
					t.Fatalf("status=%d want=%d broad hits=%d body=%s", w.Code, want, broadHits.Load(), w.Body.String())
				}
				// Only deleting the more specific claim permits the broader route.
				if err := s.store.RemoveTunnel(client.ID, "specific"); err != nil {
					t.Fatal(err)
				}
				w = httptest.NewRecorder()
				s.StartHTTPOnly().ServeHTTP(w, req)
				if w.Code != http.StatusNoContent || broadHits.Load() != 1 {
					t.Fatalf("delete did not release claim: status=%d hits=%d", w.Code, broadHits.Load())
				}
			})
		}
	}
}

func TestHTTPDomainDispatchManagementReservation(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "hit")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(addLiveHTTPDispatchTunnel(t, s, "wildcard-client", "wildcard", "*.example.com", backend.Listener.Addr()))
	setHTTPDispatchBasicAuth(t, s, "wildcard-client", "wildcard", "alice", "secret")
	req := newAuthenticatedManagementRequest(t, s, http.MethodGet, "/api/admin/config", "PANEL.EXAMPLE.COM.:443", nil)
	w := httptest.NewRecorder()
	s.StartHTTPOnly().ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("X-Backend") != "" {
		t.Fatalf("wildcard captured management host: %d %s", w.Code, w.Body.String())
	}
	// Management reservation must not depend on the wildcard being serviceable.
	if err := s.store.UpdateStates("wildcard-client", "wildcard", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.StartHTTPOnly().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("inactive wildcard blocked management host: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTPDomainDispatchWebSocketAndSSE(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: wildcard\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		kind, payload, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, payload)
		}
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(addLiveHTTPDispatchTunnel(t, s, "stream-client", "stream", "*.*.*.x.com", backend.Listener.Addr()))
	ts := httptest.NewServer(s.StartHTTPOnly())
	t.Cleanup(ts.Close)
	conn, _ := dialWSWithHost(t, ts, "shop.dev.eu.x.com", "/ws/chat", nil)
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "ping" {
		t.Fatalf("wildcard WebSocket echo=%q err=%v", payload, err)
	}
	client := ts.Client()
	client.Timeout = 3 * time.Second
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "shop.dev.eu.x.com"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: wildcard\n" || resp.StatusCode != http.StatusOK {
		t.Fatalf("wildcard SSE status=%d line=%q err=%v", resp.StatusCode, line, err)
	}
}

func TestHTTPDomainDispatchInternalChannelsPrecedeWildcard(t *testing.T) {
	s, _ := newDispatchTestServer(t, true, "https://panel.example.com")
	seedStoredTunnel(t, s, "offline-client", protocol.ProxyNewRequest{
		Name: "offline-wildcard", Type: protocol.ProxyTypeHTTP, Domain: "*.example.com",
	}, protocol.ProxyStatusStopped)
	ts := httptest.NewServer(s.StartHTTPOnly())
	t.Cleanup(ts.Close)
	for _, tc := range []struct{ path, subprotocol string }{
		{"/ws/control", protocol.WSSubProtocolControl}, {"/ws/data", protocol.WSSubProtocolData},
	} {
		conn, _ := dialWSWithHost(t, ts, "app.example.com", tc.path, []string{tc.subprotocol})
		if got := conn.Subprotocol(); got != tc.subprotocol {
			t.Errorf("%s subprotocol=%q want=%q", tc.path, got, tc.subprotocol)
		}
		_ = conn.Close()
	}
}
