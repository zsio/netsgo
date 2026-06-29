package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

func responseHasTunnelErrorCode(t testing.TB, resp *httptest.ResponseRecorder, code string) bool {
	t.Helper()
	var body tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, bytes.NewReader(resp.Body.Bytes()), &body); err != nil {
		return false
	}
	return body.ErrorCode == code || body.Code == code
}

func TestDecodeStrictEndpointConfigRejectsComplexConfig(t *testing.T) {
	var serviceCfg serviceConfigAPI
	if err := decodeStrictEndpointConfig(json.RawMessage(`{"host":"127.0.0.1","port":8080}`), &serviceCfg); err != nil {
		t.Fatalf("valid config should decode: %v", err)
	}

	oversized := json.RawMessage(`{"host":"` + strings.Repeat("a", unifiedEndpointConfigMaxBytes) + `","port":8080}`)
	if err := decodeStrictEndpointConfig(oversized, &serviceCfg); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized config should be rejected, got %v", err)
	}

	deep := strings.Repeat(`{"nested":`, unifiedEndpointConfigMaxDepth+1) + `0` + strings.Repeat(`}`, unifiedEndpointConfigMaxDepth+1)
	var target map[string]any
	if err := decodeStrictEndpointConfig(json.RawMessage(deep), &target); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("deep config should be rejected, got %v", err)
	}
}

func readControlMessageOfType(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
}, wantType string) protocol.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()
	for {
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read control message %s: %v", wantType, err)
		}
		if msg.Type == wantType {
			return msg
		}
	}
}

func readTunnelUnprovision(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
}) protocol.TunnelUnprovisionRequest {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelUnprovision)
	var req protocol.TunnelUnprovisionRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse unprovision payload: %v", err)
	}
	return req
}

func ackProvisionMessages(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		ackTunnelProvision(t, conn)
	}
}

func ackTunnelProvision(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}) protocol.TunnelProvisionRequest {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelProvision)
	var req protocol.TunnelProvisionRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse provision payload: %v", err)
	}
	if req.TunnelID == "" {
		t.Fatalf("expected unified tunnel provision payload, got empty tunnel_id: %+v", req)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeTunnelProvisionAck, protocol.TunnelProvisionAck{
		TunnelID: req.TunnelID,
		Revision: req.Revision,
		Role:     req.Role,
		Accepted: true,
		Message:  "ok",
	})
	if err != nil {
		t.Fatalf("build provision ack: %v", err)
	}
	if err := conn.WriteJSON(ack); err != nil {
		t.Fatalf("write provision ack: %v", err)
	}
	return req
}

func setLiveClientDefaultCapabilities(t *testing.T, s *Server, clientID string) {
	t.Helper()
	value, ok := s.clients.Load(clientID)
	if !ok {
		t.Fatalf("client %s is not live", clientID)
	}
	client := value.(*ClientConn)
	caps := protocol.DefaultClientCapabilities()
	info := client.GetInfo()
	info.Capabilities = &caps
	client.SetInfo(info)
}

func respondPreflight(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}) {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelPreflight)
	var req protocol.TunnelPreflightRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse preflight payload: %v", err)
	}
	resp, err := protocol.NewMessage(protocol.MsgTypeTunnelPreflightResp, protocol.TunnelPreflightResponse{
		RequestID: req.RequestID,
		TunnelID:  req.TunnelID,
		Revision:  req.Revision,
		Role:      req.Role,
		Accepted:  true,
		Message:   "ok",
	})
	if err != nil {
		t.Fatalf("build preflight response: %v", err)
	}
	if err := conn.WriteJSON(resp); err != nil {
		t.Fatalf("write preflight response: %v", err)
	}
}

func acceptPreflight(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}) protocol.TunnelPreflightRequest {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelPreflight)
	var req protocol.TunnelPreflightRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse preflight payload: %v", err)
	}
	resp, err := protocol.NewMessage(protocol.MsgTypeTunnelPreflightResp, protocol.TunnelPreflightResponse{
		RequestID: req.RequestID,
		TunnelID:  req.TunnelID,
		Revision:  req.Revision,
		Role:      req.Role,
		Accepted:  true,
		Message:   "ok",
	})
	if err != nil {
		t.Fatalf("build preflight response: %v", err)
	}
	if err := conn.WriteJSON(resp); err != nil {
		t.Fatalf("write preflight response: %v", err)
	}
	return req
}

func rejectPreflight(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}, code, message string) protocol.TunnelPreflightRequest {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelPreflight)
	var req protocol.TunnelPreflightRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse preflight payload: %v", err)
	}
	resp, err := protocol.NewMessage(protocol.MsgTypeTunnelPreflightResp, protocol.TunnelPreflightResponse{
		RequestID: req.RequestID,
		TunnelID:  req.TunnelID,
		Revision:  req.Revision,
		Role:      req.Role,
		Accepted:  false,
		Code:      code,
		Message:   message,
	})
	if err != nil {
		t.Fatalf("build rejected preflight response: %v", err)
	}
	if err := conn.WriteJSON(resp); err != nil {
		t.Fatalf("write rejected preflight response: %v", err)
	}
	return req
}

func doMuxRequestAsync(t *testing.T, handler http.Handler, method, path, token string, body []byte) <-chan *httptest.ResponseRecorder {
	t.Helper()
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		ch <- doMuxRequest(t, handler, method, path, token, body)
	}()
	return ch
}

func newTestWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConnCh := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))
	t.Cleanup(ts.Close)

	clientURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(clientURL, nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case serverConn := <-serverConnCh:
		return clientConn, serverConn
	case <-time.After(time.Second):
		_ = clientConn.Close()
		t.Fatal("timed out waiting for server websocket")
		return nil, nil
	}
}

func awaitMuxResponse(t *testing.T, ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for HTTP response")
		return nil
	}
}

func createUnifiedAPITestClient(t *testing.T, s *Server, installID, hostname string) RegisteredClient {
	t.Helper()
	capabilities := protocol.DefaultClientCapabilities()
	return createUnifiedAPITestClientWithCapabilities(t, s, installID, hostname, capabilities)
}

func createUnifiedAPITestClientWithCapabilities(t *testing.T, s *Server, installID, hostname string, capabilities protocol.ClientCapabilities) RegisteredClient {
	t.Helper()
	record, err := s.auth.adminStore.GetOrCreateClient(installID, protocol.ClientInfo{
		Hostname:     hostname,
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &capabilities,
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("failed to create client record: %v", err)
	}
	return *record
}

func unifiedCapabilityTestConfig(t *testing.T, endpointType string) string {
	t.Helper()
	switch endpointType {
	case protocol.IngressTypeTCPListen:
		return `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}`
	case protocol.IngressTypeUDPListen:
		return `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveUDPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}`
	case protocol.IngressTypeSOCKS5Listen:
		return `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"],"auth":{"type":"none"}}`
	case protocol.TargetTypeTCPService:
		return `{"host":"127.0.0.1","port":22}`
	case protocol.TargetTypeUDPService:
		return `{"host":"127.0.0.1","port":53}`
	case protocol.TargetTypeSOCKS5ConnectHandler:
		return `{"allowed_target_cidrs":["0.0.0.0/0","::/0"],"allowed_target_hosts":["example.com"],"allowed_target_ports":[80,443],"dial_timeout_seconds":5}`
	default:
		t.Fatalf("unsupported endpoint type %q", endpointType)
		return `{}`
	}
}

func unifiedCreatePayload(name, clientID string, port int) []byte {
	return []byte(`{
		"name":"` + name + `",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + clientID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only",
		"bandwidth_settings":{"ingress_bps":0,"egress_bps":0}
	}`)
}

func TestAPI_UnifiedTunnelCreateDerivesOwnerAndListsByClientRole(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-unified-owner", "unified-owner")

	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, unifiedCreatePayload("ssh", record.ID, 22001))
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created tunnel should include server-owned id")
	}
	if created.Revision != 1 {
		t.Fatalf("created revision: want 1, got %d", created.Revision)
	}
	if created.OwnerClientID != record.ID {
		t.Fatalf("owner_client_id: want target client %q, got %q", record.ID, created.OwnerClientID)
	}
	if created.Ingress.Type != tunnelIngressTypeTCPListen || created.Target.Type != tunnelTargetTypeTCPService {
		t.Fatalf("unexpected endpoint shape: %+v -> %+v", created.Ingress, created.Target)
	}

	stored, err := s.store.GetTunnelByIDE(record.ID, created.ID)
	if err != nil {
		t.Fatalf("created tunnel should persist through existing store by id: %v", err)
	}
	if stored.Name != "ssh" || stored.ClientID != record.ID {
		t.Fatalf("stored tunnel mismatch: %+v", stored)
	}

	listResp := doMuxRequest(t, handler, http.MethodGet, "/api/clients/"+record.ID+"/tunnels?role=owner", token, nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET owner tunnels: want 200, got %d body=%s", listResp.Code, listResp.Body.String())
	}
	var ownerList []protocol.ProxyConfig
	if err := mustDecodeJSON(t, listResp.Body, &ownerList); err != nil {
		t.Fatalf("failed to decode owner list: %v", err)
	}
	if len(ownerList) != 1 || ownerList[0].ID != created.ID {
		t.Fatalf("owner list mismatch: %+v", ownerList)
	}
	ownerView := ownerList[0]
	if ownerView.Type != protocol.ProxyTypeTCP || ownerView.LocalIP != "127.0.0.1" || ownerView.LocalPort != 22 || ownerView.RemotePort != 22001 {
		t.Fatalf("owner list should return ProxyConfig shape, got %+v", ownerView)
	}
	if ownerView.Capabilities == nil {
		t.Fatalf("owner list should include action capabilities")
	}

	ingressResp := doMuxRequest(t, handler, http.MethodGet, "/api/clients/"+record.ID+"/tunnels?role=ingress", token, nil)
	if ingressResp.Code != http.StatusOK {
		t.Fatalf("GET ingress tunnels: want 200, got %d body=%s", ingressResp.Code, ingressResp.Body.String())
	}
	var ingressList []tunnelSpecAPI
	if err := mustDecodeJSON(t, ingressResp.Body, &ingressList); err != nil {
		t.Fatalf("failed to decode ingress list: %v", err)
	}
	if len(ingressList) != 0 {
		t.Fatalf("server ingress should not match client ingress role, got %+v", ingressList)
	}

	targetResp := doMuxRequest(t, handler, http.MethodGet, "/api/clients/"+record.ID+"/tunnels?role=target", token, nil)
	if targetResp.Code != http.StatusOK {
		t.Fatalf("GET target tunnels: want 200, got %d body=%s", targetResp.Code, targetResp.Body.String())
	}
	var targetList []tunnelSpecAPI
	if err := mustDecodeJSON(t, targetResp.Body, &targetList); err != nil {
		t.Fatalf("failed to decode target list: %v", err)
	}
	if len(targetList) != 1 || targetList[0].ID != created.ID {
		t.Fatalf("target list mismatch: %+v", targetList)
	}
}

func TestAPI_UnifiedTunnelDefaultsMissingSourceCIDRsAndRejectsEmptyList(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-source-policy-default", "source-policy-default")
	base := func(ingressType, config string) []byte {
		targetType := tunnelTargetTypeTCPService
		if ingressType == tunnelIngressTypeUDPListen {
			targetType = tunnelTargetTypeUDPService
		}
		targetConfig := `{"ip":"127.0.0.1","port":22}`
		if targetType == tunnelTargetTypeUDPService {
			targetConfig = `{"ip":"127.0.0.1","port":53}`
		}
		return []byte(`{
			"name":"missing-source-policy-` + ingressType + `",
			"topology":"server_expose",
			"ingress":{"location":"server","type":"` + ingressType + `","config":` + config + `},
			"target":{"location":"client","client_id":"` + target.ID + `","type":"` + targetType + `","config":` + targetConfig + `},
			"transport_policy":"server_relay_only"
		}`)
	}

	for _, tc := range []struct {
		name         string
		ingressType  string
		config       string
		wantStatus   int
		wantAllowAll bool
	}{
		{name: "tcp missing", ingressType: tunnelIngressTypeTCPListen, config: `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `}`, wantStatus: http.StatusCreated, wantAllowAll: true},
		{name: "tcp empty", ingressType: tunnelIngressTypeTCPListen, config: `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":[]}`, wantStatus: http.StatusBadRequest},
		{name: "udp missing", ingressType: tunnelIngressTypeUDPListen, config: `{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveUDPPort(t)) + `}`, wantStatus: http.StatusCreated, wantAllowAll: true},
		{name: "http missing", ingressType: tunnelIngressTypeHTTPHost, config: `{"domain":"missing-source-policy.example.com"}`, wantStatus: http.StatusCreated, wantAllowAll: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, base(tc.ingressType, tc.config))
			if resp.Code != tc.wantStatus {
				t.Fatalf("source CIDR validation: want %d, got %d body=%s", tc.wantStatus, resp.Code, resp.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				var body tunnelMutationErrorResponse
				if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
					t.Fatalf("decode source CIDR error: %v", err)
				}
				if body.Field != "ingress.config.allowed_source_cidrs" {
					t.Fatalf("source CIDR error field mismatch: %+v", body)
				}
				return
			}
			var created tunnelSpecAPI
			if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
				t.Fatalf("decode created tunnel: %v", err)
			}
			if tc.wantAllowAll {
				policy, err := decodeIngressAccessPolicy(created.Ingress.Config, false)
				if err != nil {
					t.Fatalf("decode response source policy: %v", err)
				}
				if got, want := strings.Join(policy.allowedSourceCIDRs, ","), "0.0.0.0/0,::/0"; got != want {
					t.Fatalf("default response source CIDRs: got %q want %q", got, want)
				}
			}
		})
	}
}

func TestAPI_UnifiedTunnelPreservesSourceCIDRs(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-source-policy-preserve", "source-policy-preserve")
	body := []byte(`{
		"name":"source-policy-preserve",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":["127.0.0.0/8","10.0.0.0/8"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("source CIDR create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode source CIDR tunnel: %v", err)
	}
	var cfg tcpListenConfigAPI
	if err := json.Unmarshal(created.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode response ingress config: %v", err)
	}
	if got, want := strings.Join(cfg.AllowedSourceCIDRs, ","), "127.0.0.0/8,10.0.0.0/8"; got != want {
		t.Fatalf("response source CIDRs: got %q want %q", got, want)
	}
	stored, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load stored source CIDR tunnel: %v", err)
	}
	if err := json.Unmarshal(stored.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode stored ingress config: %v", err)
	}
	if got, want := strings.Join(cfg.AllowedSourceCIDRs, ","), "127.0.0.0/8,10.0.0.0/8"; got != want {
		t.Fatalf("stored source CIDRs: got %q want %q", got, want)
	}
}

func TestAPI_UnifiedTunnelCreateSOCKS5ServerExpose(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-socks5-target", "socks5-target")
	port := reserveTCPPort(t)
	body := []byte(`{
		"name":"socks5",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(port) + `,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"allowed_target_hosts":["example.com"],
			"allowed_target_ports":[443],
			"dial_timeout_seconds":9
		}},
		"transport_policy":"server_relay_only",
		"confirm_no_auth_risk":true,
		"bandwidth_settings":{"ingress_bps":0,"egress_bps":0}
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("SOCKS5 create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode SOCKS5 create response: %v", err)
	}
	if created.Ingress.Type != protocol.IngressTypeSOCKS5Listen || created.Target.Type != protocol.TargetTypeSOCKS5ConnectHandler {
		t.Fatalf("SOCKS5 endpoint types mismatch: %+v -> %+v", created.Ingress, created.Target)
	}
	if bytes.Contains(resp.Body.Bytes(), []byte(`"local_ip"`)) || bytes.Contains(resp.Body.Bytes(), []byte(`"local_port"`)) {
		t.Fatalf("unified SOCKS5 response should be endpoint-spec based, got %s", resp.Body.String())
	}

	stored, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load stored SOCKS5 tunnel: %v", err)
	}
	if stored.LocalIP != "" || stored.LocalPort != 0 {
		t.Fatalf("SOCKS5 must not store dynamic target in LocalIP/LocalPort: %+v", stored.ProxyNewRequest)
	}
	if stored.Target.Type != protocol.TargetTypeSOCKS5ConnectHandler {
		t.Fatalf("stored target endpoint mismatch: %+v", stored.Target)
	}
}

func TestAPI_UnifiedTunnelSOCKS5NoAuthRequiresSubmitConfirmation(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-socks5-no-auth-target", "socks5-no-auth-target")
	body := []byte(`{
		"name":"socks5-no-confirm",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(reserveTCPPort(t)) + `,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":10
		}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("SOCKS5 no-auth without confirmation: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	if _, err := s.store.GetTunnelE(target.ID, "socks5-no-confirm"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("missing no-auth confirmation must not persist config, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelSOCKS5PasswordIsWriteOnly(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-socks5-password-target", "socks5-password-target")
	secret := "super-secret-password"
	body := []byte(`{
		"name":"socks5-password",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(reserveTCPPort(t)) + `,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"username_password","username":"alice","password":"` + secret + `"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":10
		}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("SOCKS5 password create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	if bytes.Contains(resp.Body.Bytes(), []byte(secret)) || bytes.Contains(resp.Body.Bytes(), []byte(`"password"`)) {
		t.Fatalf("SOCKS5 create response must not echo password, got %s", resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	stored, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load stored SOCKS5 tunnel: %v", err)
	}
	if bytes.Contains(stored.Ingress.Config, []byte(secret)) || bytes.Contains(stored.Ingress.Config, []byte(`"password"`)) {
		t.Fatalf("stored ingress config must not contain plaintext password: %s", string(stored.Ingress.Config))
	}
	if !bytes.Contains(stored.Ingress.Config, []byte(`"password_hash"`)) {
		t.Fatalf("stored ingress config should contain password hash: %s", string(stored.Ingress.Config))
	}
	var storedIngress protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(stored.Ingress.Config, &storedIngress); err != nil {
		t.Fatalf("decode stored ingress config: %v", err)
	}
	originalHash := storedIngress.Auth.PasswordHash
	if originalHash == "" {
		t.Fatalf("stored ingress config should contain password hash: %s", string(stored.Ingress.Config))
	}

	update := []byte(`{"expected_revision":` + strconv.FormatInt(created.Revision, 10) + `,"spec":{
		"name":"socks5-password",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(stored.RemotePort) + `,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"username_password","username":"alice"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":20
		}},
		"transport_policy":"server_relay_only"
	}}`)
	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("SOCKS5 password update without new password: want 200, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	if bytes.Contains(updateResp.Body.Bytes(), []byte(secret)) ||
		bytes.Contains(updateResp.Body.Bytes(), []byte(`"password"`)) ||
		bytes.Contains(updateResp.Body.Bytes(), []byte(`"password_hash"`)) {
		t.Fatalf("SOCKS5 update response must not echo password material, got %s", updateResp.Body.String())
	}
	updated, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load updated SOCKS5 tunnel: %v", err)
	}
	var updatedIngress protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(updated.Ingress.Config, &updatedIngress); err != nil {
		t.Fatalf("decode updated ingress config: %v", err)
	}
	if updatedIngress.Auth.PasswordHash != originalHash {
		t.Fatalf("SOCKS5 update without new password should preserve password hash")
	}
}

func TestAPI_UnifiedTunnelSOCKS5PasswordPreserveRejectsUnknownIngressField(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-socks5-password-unknown-target", "socks5-password-unknown-target")
	secret := "super-secret-password"
	create := []byte(`{
		"name":"socks5-password-unknown",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(reserveTCPPort(t)) + `,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"username_password","username":"alice","password":"` + secret + `"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":10
		}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("SOCKS5 password create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	stored, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load stored SOCKS5 tunnel: %v", err)
	}
	var before protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(stored.Ingress.Config, &before); err != nil {
		t.Fatalf("decode stored ingress config: %v", err)
	}

	update := []byte(`{"expected_revision":` + strconv.FormatInt(created.Revision, 10) + `,"spec":{
		"name":"socks5-password-unknown",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(stored.RemotePort) + `,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"unexpected":true,
			"auth":{"type":"username_password","username":"alice"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":20
		}},
		"transport_policy":"server_relay_only"
	}}`)
	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if updateResp.Code != http.StatusBadRequest {
		t.Fatalf("SOCKS5 password preserve with unknown field: want 400, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	after, err := s.store.GetTunnelByIDE(target.ID, created.ID)
	if err != nil {
		t.Fatalf("load tunnel after rejected update: %v", err)
	}
	if after.Revision != stored.Revision {
		t.Fatalf("rejected update should keep revision %d, got %d", stored.Revision, after.Revision)
	}
	var afterCfg protocol.SOCKS5ListenConfig
	if err := json.Unmarshal(after.Ingress.Config, &afterCfg); err != nil {
		t.Fatalf("decode post-reject ingress config: %v", err)
	}
	if afterCfg.Auth.PasswordHash != before.Auth.PasswordHash {
		t.Fatal("rejected update should keep existing password hash")
	}
}

func TestStoredTunnelViewConfigRedactsSOCKS5PasswordHash(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "socks5-view-redact-id",
			Name:       "socks5-view-redact",
			Type:       protocol.ProxyTypeTCP,
			RemotePort: 1080,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        1,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeSOCKS5Listen,
			Config: mustRawJSON(protocol.SOCKS5ListenConfig{
				BindIP:             "0.0.0.0",
				Port:               1080,
				AllowedSourceCIDRs: []string{"0.0.0.0/0", "::/0"},
				Auth: protocol.SOCKS5AuthConfig{
					Type:         protocol.SOCKS5AuthTypeUsernamePassword,
					Username:     "alice",
					PasswordHash: "$argon2id$v=19$m=65536,t=3,p=1$c2FsdA$Ynl0ZXM",
				},
			}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeSOCKS5ConnectHandler,
			Config: mustRawJSON(protocol.SOCKS5ConnectHandlerConfig{
				AllowedTargetCIDRs: []string{"0.0.0.0/0", "::/0"},
				DialTimeoutSeconds: 10,
			}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	view := s.storedTunnelViewConfig(stored)
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	if bytes.Contains(raw, []byte("password_hash")) || bytes.Contains(raw, []byte("$argon2id")) {
		t.Fatalf("stored tunnel view should redact password hash: %s", string(raw))
	}
}

func TestStoredTunnelViewConfigBackfillsMissingSourceCIDRs(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "legacy-view-id",
			Name:       "legacy-view",
			Type:       protocol.ProxyTypeTCP,
			RemotePort: 18080,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        1,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: 18080}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}

	view := s.storedTunnelViewConfig(stored)
	if view.Ingress == nil {
		t.Fatal("view should include ingress config")
	}
	var cfg tcpListenConfigAPI
	if err := json.Unmarshal(view.Ingress.Config, &cfg); err != nil {
		t.Fatalf("decode view ingress config: %v", err)
	}
	if got, want := strings.Join(cfg.AllowedSourceCIDRs, ","), "0.0.0.0/0,::/0"; got != want {
		t.Fatalf("view source CIDRs: got %q want %q", got, want)
	}
}

func TestSpecFromStoredTunnelPrefersEndpointConfigOverLegacyFlatFields(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "endpoint-priority",
			Name:       "endpoint-priority",
			Type:       protocol.ProxyTypeHTTP,
			Domain:     "flat.example.com",
			LocalIP:    "127.0.0.10",
			LocalPort:  10010,
			RemotePort: 18080,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        7,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeHTTPHost,
			Config: mustRawJSON(httpHostConfigAPI{
				Domain:             "endpoint.example.com",
				AllowedSourceCIDRs: allowAllSourceCIDRs(),
				Auth:               protocol.HTTPAuthConfig{Type: protocol.HTTPAuthTypeNone},
			}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: "target-client",
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{Host: "10.20.30.40", Port: 2040}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}

	spec := specFromStoredTunnel(stored, s)
	var ingress httpHostConfigAPI
	if err := json.Unmarshal(spec.Ingress.Config, &ingress); err != nil {
		t.Fatalf("decode spec ingress config: %v", err)
	}
	if ingress.Domain != "endpoint.example.com" {
		t.Fatalf("spec ingress domain should come from endpoint config, got %q", ingress.Domain)
	}
	var target serviceConfigAPI
	if err := json.Unmarshal(spec.Target.Config, &target); err != nil {
		t.Fatalf("decode spec target config: %v", err)
	}
	if target.Host != "10.20.30.40" || target.Port != 2040 {
		t.Fatalf("spec target should come from endpoint config, got %+v", target)
	}

	view := s.storedTunnelViewConfig(stored)
	if view.Ingress == nil || view.Target == nil {
		t.Fatalf("view should include endpoint specs: %+v", view)
	}
	var viewIngress httpHostConfigAPI
	if err := json.Unmarshal(view.Ingress.Config, &viewIngress); err != nil {
		t.Fatalf("decode view ingress config: %v", err)
	}
	if viewIngress.Domain != "endpoint.example.com" {
		t.Fatalf("view ingress domain should come from endpoint config, got %q", viewIngress.Domain)
	}
	var viewTarget serviceConfigAPI
	if err := json.Unmarshal(view.Target.Config, &viewTarget); err != nil {
		t.Fatalf("decode view target config: %v", err)
	}
	if viewTarget.Host != "10.20.30.40" || viewTarget.Port != 2040 {
		t.Fatalf("view target should come from endpoint config, got %+v", viewTarget)
	}
}

func TestSpecFromStoredTunnelBackfillsMissingEndpointsFromLegacyFlatFields(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:        "flat-backfill",
			Name:      "flat-backfill",
			Type:      protocol.ProxyTypeHTTP,
			Domain:    "flat.example.com",
			LocalIP:   "127.0.0.44",
			LocalPort: 18044,
		},
		ClientID:        "target-client",
		OwnerClientID:   "target-client",
		Binding:         TunnelBindingClientID,
		Revision:        3,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateOffline,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportUnknown,
		P2P:             P2PState{State: TunnelP2PStateIdle},
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}

	spec := specFromStoredTunnel(stored, s)
	if spec.Ingress.Type != protocol.IngressTypeHTTPHost {
		t.Fatalf("backfilled ingress type: got %q", spec.Ingress.Type)
	}
	var ingress httpHostConfigAPI
	if err := json.Unmarshal(spec.Ingress.Config, &ingress); err != nil {
		t.Fatalf("decode backfilled ingress config: %v", err)
	}
	if ingress.Domain != "flat.example.com" {
		t.Fatalf("backfilled ingress domain: got %q", ingress.Domain)
	}
	if spec.Target.Type != protocol.TargetTypeTCPService {
		t.Fatalf("backfilled target type: got %q", spec.Target.Type)
	}
	var target serviceConfigAPI
	if err := json.Unmarshal(spec.Target.Config, &target); err != nil {
		t.Fatalf("decode backfilled target config: %v", err)
	}
	if target.Host != "127.0.0.44" && target.IP != "127.0.0.44" {
		t.Fatalf("backfilled target host/ip: got %+v", target)
	}
	if target.Port != 18044 {
		t.Fatalf("backfilled target port: got %d", target.Port)
	}
}

func TestAPI_UnifiedTunnelHTTPBasicAuthHashesAndRedacts(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-http-basic-auth-target", "http-basic-auth-target")
	secret := "http-secret-password"
	body := []byte(`{
		"name":"http-basic-auth",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"http_host","config":{
			"domain":"basic-auth.example.com",
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"basic","username":"alice","password":"` + secret + `"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
		"transport_policy":"server_relay_only"
	}`)

	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("HTTP Basic create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	if bytes.Contains(resp.Body.Bytes(), []byte(secret)) || bytes.Contains(resp.Body.Bytes(), []byte("password_hash")) {
		t.Fatalf("HTTP Basic create response must not echo password material: %s", resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode HTTP Basic create response: %v", err)
	}

	stored, err := s.store.GetTunnelByIDE(created.OwnerClientID, created.ID)
	if err != nil {
		t.Fatalf("load stored HTTP Basic tunnel: %v", err)
	}
	if bytes.Contains(stored.Ingress.Config, []byte(secret)) || bytes.Contains(stored.Ingress.Config, []byte(`"password"`)) {
		t.Fatalf("stored HTTP ingress config must not contain plaintext password: %s", string(stored.Ingress.Config))
	}
	if !bytes.Contains(stored.Ingress.Config, []byte(`"password_hash"`)) {
		t.Fatalf("stored HTTP ingress config should contain password hash: %s", string(stored.Ingress.Config))
	}

	updateBody := []byte(fmt.Sprintf(`{
		"expected_revision":%d,
		"spec":{
			"name":"http-basic-auth",
			"topology":"server_expose",
			"ingress":{"location":"server","type":"http_host","config":{
				"domain":"basic-auth.example.com",
				"allowed_source_cidrs":["0.0.0.0/0","::/0"],
				"auth":{"type":"basic","username":"alice"}
			}},
			"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
			"transport_policy":"server_relay_only"
		}
	}`, created.Revision, target.ID))
	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, updateBody)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("HTTP Basic update without new password: want 200, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	updated, err := s.store.GetTunnelByIDE(created.OwnerClientID, created.ID)
	if err != nil {
		t.Fatalf("load updated HTTP Basic tunnel: %v", err)
	}
	if !bytes.Contains(updated.Ingress.Config, []byte(`"password_hash"`)) {
		t.Fatalf("HTTP Basic update should preserve password hash: %s", string(updated.Ingress.Config))
	}
}

func TestAPI_UnifiedTunnelHTTPBasicPasswordPreserveRejectsUnknownIngressField(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-http-basic-unknown-target", "http-basic-unknown-target")
	secret := "http-secret-password"
	create := []byte(`{
		"name":"http-basic-unknown",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"http_host","config":{
			"domain":"basic-unknown.example.com",
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"basic","username":"alice","password":"` + secret + `"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("HTTP Basic create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode HTTP Basic create response: %v", err)
	}
	stored, err := s.store.GetTunnelByIDE(created.OwnerClientID, created.ID)
	if err != nil {
		t.Fatalf("load stored HTTP Basic tunnel: %v", err)
	}
	var before httpHostConfigAPI
	if err := json.Unmarshal(stored.Ingress.Config, &before); err != nil {
		t.Fatalf("decode stored HTTP ingress config: %v", err)
	}
	if before.Auth.PasswordHash == "" {
		t.Fatalf("stored HTTP ingress config should contain password hash: %s", string(stored.Ingress.Config))
	}

	update := []byte(fmt.Sprintf(`{
		"expected_revision":%d,
		"spec":{
			"name":"http-basic-unknown",
			"topology":"server_expose",
			"ingress":{"location":"server","type":"http_host","config":{
				"domain":"basic-unknown.example.com",
				"allowed_source_cidrs":["0.0.0.0/0","::/0"],
				"unexpected":true,
				"auth":{"type":"basic","username":"alice"}
			}},
			"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
			"transport_policy":"server_relay_only"
		}
	}`, created.Revision, target.ID))
	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if updateResp.Code != http.StatusBadRequest {
		t.Fatalf("HTTP Basic preserve with unknown field: want 400, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	after, err := s.store.GetTunnelByIDE(created.OwnerClientID, created.ID)
	if err != nil {
		t.Fatalf("load tunnel after rejected update: %v", err)
	}
	if after.Revision != stored.Revision {
		t.Fatalf("rejected update should keep revision %d, got %d", stored.Revision, after.Revision)
	}
	var afterCfg httpHostConfigAPI
	if err := json.Unmarshal(after.Ingress.Config, &afterCfg); err != nil {
		t.Fatalf("decode post-reject HTTP ingress config: %v", err)
	}
	if afterCfg.Auth.PasswordHash != before.Auth.PasswordHash {
		t.Fatal("rejected update should keep existing HTTP Basic password hash")
	}
}

func TestAPI_UnifiedTunnelRejectsFutureTargetsAndDirectPolicies(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-unified-reject", "unified-reject")

	futureBody := []byte(`{
		"name":"future-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22002,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"static_file","config":{"root":"/tmp"}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, futureBody)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("future target create: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var body tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to decode future target error: %v", err)
	}
	if body.ErrorCode != protocol.TunnelMutationErrorCodeUnsupportedEndpointType || body.Field != "target.type" {
		t.Fatalf("future target error mismatch: %+v", body)
	}
	if _, ok := s.store.GetTunnel(record.ID, "future-target"); ok {
		t.Fatal("future target payload must not be persisted")
	}

	directBody := []byte(`{
		"name":"direct-policy",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + record.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":22003,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"direct_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, directBody)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("direct policy create: want 400 while direct transport unavailable, got %d body=%s", resp.Code, resp.Body.String())
	}
	body = tunnelMutationErrorResponse{}
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to decode direct policy error: %v", err)
	}
	if body.ErrorCode != "direct_transport_unavailable" || body.Field != "transport_policy" {
		t.Fatalf("direct policy error mismatch: %+v", body)
	}
	if _, ok := s.store.GetTunnel(record.ID, "direct-policy"); ok {
		t.Fatal("unsupported direct policy payload must not be persisted")
	}

	source := createUnifiedAPITestClient(t, s, "install-unified-source", "unified-source")
	ingress := createUnifiedAPITestClient(t, s, "install-unified-ingress", "unified-ingress")
	clientRelayBody := []byte(`{
		"name":"client-relay-direct",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":22003,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + source.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"direct_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, clientRelayBody)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("client_to_client direct create: want 400 for unavailable direct transport, got %d body=%s", resp.Code, resp.Body.String())
	}
	body = tunnelMutationErrorResponse{}
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to decode client_to_client direct error: %v", err)
	}
	if body.ErrorCode != "direct_transport_unavailable" || body.Field != "transport_policy" {
		t.Fatalf("client_to_client direct error mismatch: %+v", body)
	}
}

func TestAPI_UnifiedTunnelCreateClientToClientPersistsOwnerAndRoles(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	source := createUnifiedAPITestClient(t, s, "install-c2c-source", "c2c-source")
	ingress := createUnifiedAPITestClient(t, s, "install-c2c-ingress", "c2c-ingress")

	body := []byte(`{
		"name":"source-to-ingress",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":23001,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + source.ID + `","type":"tcp_service","config":{"host":"a2","port":8080}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}
	if created.Topology != tunnelTopologyClientToClient {
		t.Fatalf("topology: want client_to_client, got %q", created.Topology)
	}
	if created.OwnerClientID != source.ID {
		t.Fatalf("owner_client_id: want source %q, got %q", source.ID, created.OwnerClientID)
	}
	if created.Ingress.ClientID != ingress.ID || created.Target.ClientID != source.ID {
		t.Fatalf("participants mismatch: ingress=%+v target=%+v", created.Ingress, created.Target)
	}
	if created.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("offline clients should create as offline, got %s", created.RuntimeState)
	}

	stored, err := s.store.GetTunnelByIDE(source.ID, created.ID)
	if err != nil {
		t.Fatalf("client_to_client tunnel should persist under source owner: %v", err)
	}
	if stored.Topology != TunnelTopologyClientToClient || stored.OwnerClientID != source.ID {
		t.Fatalf("stored topology/owner mismatch: %+v", stored)
	}
	if stored.Ingress.ClientID != ingress.ID || stored.Target.ClientID != source.ID {
		t.Fatalf("stored participants mismatch: ingress=%+v target=%+v", stored.Ingress, stored.Target)
	}

	for _, tc := range []struct {
		role     string
		clientID string
	}{
		{role: "owner", clientID: source.ID},
		{role: "target", clientID: source.ID},
		{role: "ingress", clientID: ingress.ID},
		{role: "related", clientID: ingress.ID},
	} {
		listResp := doMuxRequest(t, handler, http.MethodGet, "/api/clients/"+tc.clientID+"/tunnels?role="+tc.role, token, nil)
		if listResp.Code != http.StatusOK {
			t.Fatalf("GET role %s for %s: want 200, got %d body=%s", tc.role, tc.clientID, listResp.Code, listResp.Body.String())
		}
		var list []tunnelSpecAPI
		if err := mustDecodeJSON(t, listResp.Body, &list); err != nil {
			t.Fatalf("failed to decode role %s list: %v", tc.role, err)
		}
		if len(list) != 1 || list[0].ID != created.ID {
			t.Fatalf("role %s list mismatch: %+v", tc.role, list)
		}
	}
}

func TestAPI_UnifiedTunnelRejectsSameClientToClientParticipants(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-c2c-same", "c2c-same")
	body := []byte(`{
		"name":"bad-same-client",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + record.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":23002,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("same participant create: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("failed to decode same participant error: %v", err)
	}
	if bodyResp.ErrorCode != "same_ingress_and_target_client" || bodyResp.Field != "ingress.client_id" {
		t.Fatalf("same participant error mismatch: %+v", bodyResp)
	}
}

func TestAPI_UnifiedTunnelUpdateRequiresExpectedRevisionAndHardDelete(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-unified-update", "unified-update")

	createResp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, unifiedCreatePayload("revise-me", record.ID, 22004))
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/tunnels: want 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, createResp.Body, &created); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}

	staleUpdate := []byte(`{"expected_revision":99,"spec":{
		"name":"revise-me",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22004,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`)
	staleResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, staleUpdate)
	if staleResp.Code != http.StatusConflict {
		t.Fatalf("stale update: want 409, got %d body=%s", staleResp.Code, staleResp.Body.String())
	}
	var staleBody struct {
		ErrorCode       string `json:"error_code"`
		Code            string `json:"code"`
		Field           string `json:"field"`
		CurrentRevision int64  `json:"current_revision"`
	}
	if err := mustDecodeJSON(t, staleResp.Body, &staleBody); err != nil {
		t.Fatalf("failed to decode stale revision error: %v", err)
	}
	if staleBody.ErrorCode != protocol.TunnelMutationErrorCodeRevisionConflict ||
		staleBody.Code != protocol.TunnelMutationErrorCodeRevisionConflict ||
		staleBody.Field != "expected_revision" ||
		staleBody.CurrentRevision != created.Revision {
		t.Fatalf("stale revision error mismatch: %+v", staleBody)
	}

	missingRevisionResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, []byte(`{"spec":{
		"name":"revise-me",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22004,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"`+record.ID+`","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`))
	if missingRevisionResp.Code != http.StatusBadRequest {
		t.Fatalf("missing expected revision: want 400, got %d body=%s", missingRevisionResp.Code, missingRevisionResp.Body.String())
	}
	staleBody = struct {
		ErrorCode       string `json:"error_code"`
		Code            string `json:"code"`
		Field           string `json:"field"`
		CurrentRevision int64  `json:"current_revision"`
	}{}
	if err := mustDecodeJSON(t, missingRevisionResp.Body, &staleBody); err != nil {
		t.Fatalf("failed to decode missing revision error: %v", err)
	}
	if staleBody.ErrorCode != protocol.TunnelMutationErrorCodeRevisionConflict ||
		staleBody.Code != protocol.TunnelMutationErrorCodeRevisionConflict ||
		staleBody.Field != "expected_revision" ||
		staleBody.CurrentRevision != created.Revision {
		t.Fatalf("missing revision error mismatch: %+v", staleBody)
	}

	validUpdate := []byte(`{"expected_revision":1,"spec":{
		"name":"revise-me",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22005,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only",
		"bandwidth_settings":{"ingress_bps":128,"egress_bps":256}
	}}`)
	updateResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, validUpdate)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("valid update: want 200, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	var updatePayload struct {
		Tunnel tunnelSpecAPI `json:"tunnel"`
	}
	if err := mustDecodeJSON(t, updateResp.Body, &updatePayload); err != nil {
		t.Fatalf("failed to decode update response: %v", err)
	}
	if updatePayload.Tunnel.ID != created.ID {
		t.Fatalf("updated tunnel id mismatch: want %q, got %q", created.ID, updatePayload.Tunnel.ID)
	}
	if updatePayload.Tunnel.Revision != 2 {
		t.Fatalf("updated revision: want 2, got %d", updatePayload.Tunnel.Revision)
	}
	stored, err := s.store.GetTunnelByIDE(record.ID, created.ID)
	if err != nil {
		t.Fatalf("updated tunnel should remain persisted: %v", err)
	}
	if stored.Revision != 2 {
		t.Fatalf("stored revision: want 2, got %d", stored.Revision)
	}
	if stored.LocalPort != 2222 || stored.RemotePort != 22005 || stored.IngressBPS != 128 || stored.EgressBPS != 256 {
		t.Fatalf("stored update mismatch: %+v", stored)
	}

	staleSecondResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, validUpdate)
	if staleSecondResp.Code != http.StatusConflict {
		t.Fatalf("second update with stale revision: want 409, got %d body=%s", staleSecondResp.Code, staleSecondResp.Body.String())
	}

	deleteResp := doMuxRequest(t, handler, http.MethodDelete, "/api/tunnels/"+created.ID, token, nil)
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/tunnels/{id}: want 204, got %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	if _, err := s.store.GetTunnelByIDE(record.ID, created.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("deleted tunnel should be hard-deleted, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelUpdateUnprovisionsOldServerExposeTarget(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "server-expose-update-target", "install-server-expose-update-target")
	defer mustClose(t, targetConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"server-expose-update",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, reserveTCPPort(t), targetAuth.ClientID))
	createResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("server_expose create: want 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, createResp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	initialProvision := ackTunnelProvision(t, targetConn)
	if initialProvision.TunnelID != created.ID || initialProvision.Revision != created.Revision || initialProvision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("initial provision mismatch: %+v", initialProvision)
	}

	update := []byte(fmt.Sprintf(`{"expected_revision":%d,"spec":{
		"name":"server-expose-update",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`, created.Revision, reserveTCPPort(t), targetAuth.ClientID))
	updateResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("server_expose update: want 200, got %d body=%s", updateResp.Code, updateResp.Body.String())
	}

	unprovision := readTunnelUnprovision(t, targetConn)
	if unprovision.TunnelID != created.ID || unprovision.Revision != created.Revision || unprovision.Role != protocol.DataStreamRoleTarget || unprovision.Reason != "updated" {
		t.Fatalf("old target unprovision mismatch: %+v", unprovision)
	}
}

func TestAPI_UnifiedTunnelDeleteUnprovisionsServerExposeTarget(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "server-expose-delete-target", "install-server-expose-delete-target")
	defer mustClose(t, targetConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"server-expose-delete",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, reserveTCPPort(t), targetAuth.ClientID))
	createResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("server_expose create: want 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, createResp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	initialProvision := ackTunnelProvision(t, targetConn)
	if initialProvision.TunnelID != created.ID || initialProvision.Revision != created.Revision || initialProvision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("initial provision mismatch: %+v", initialProvision)
	}

	deleteResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodDelete, "/api/tunnels/"+created.ID, token, nil)
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("server_expose delete: want 204, got %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}

	unprovision := readTunnelUnprovision(t, targetConn)
	if unprovision.TunnelID != created.ID || unprovision.Revision != created.Revision || unprovision.Role != protocol.DataStreamRoleTarget || unprovision.Reason != "deleted" {
		t.Fatalf("delete target unprovision mismatch: %+v", unprovision)
	}
	live, ok := s.loadLiveClient(targetAuth.ClientID)
	if !ok {
		t.Fatal("target client should remain live")
	}
	if _, _, exists := findTunnelBySelector(live, created.ID); exists {
		t.Fatal("delete should remove server-expose runtime from live client")
	}
	if _, err := s.store.GetTunnelByIDE(targetAuth.ClientID, created.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("deleted tunnel should be hard-deleted, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelDeleteUnprovisionsClientToClientParticipants(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-delete-target", "install-c2c-delete-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-delete-ingress", "install-c2c-delete-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-delete",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	deleteResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodDelete, "/api/tunnels/"+created.ID, token, nil)
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("client_to_client delete: want 204, got %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}

	targetUnprovision := readTunnelUnprovision(t, targetConn)
	if targetUnprovision.TunnelID != created.ID || targetUnprovision.Revision != created.Revision || targetUnprovision.Role != protocol.DataStreamRoleTarget || targetUnprovision.Reason != "deleted" {
		t.Fatalf("target delete unprovision mismatch: %+v", targetUnprovision)
	}
	ingressUnprovision := readTunnelUnprovision(t, ingressConn)
	if ingressUnprovision.TunnelID != created.ID || ingressUnprovision.Revision != created.Revision || ingressUnprovision.Role != protocol.DataStreamRoleIngress || ingressUnprovision.Reason != "deleted" {
		t.Fatalf("ingress delete unprovision mismatch: %+v", ingressUnprovision)
	}
	if _, ok := s.c2c.get(created.ID); ok {
		t.Fatal("delete should remove client relay runtime")
	}
	if _, err := s.store.GetTunnelByIDE(targetAuth.ClientID, created.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("deleted tunnel should be hard-deleted, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelUpdateUnprovisionsOldClientToClientParticipants(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-update-target", "install-c2c-update-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-update-ingress", "install-c2c-update-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-update",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	update := []byte(fmt.Sprintf(`{"expected_revision":%d,"spec":{
		"name":"c2c-update",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`, created.Revision, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	updateRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	respondPreflight(t, ingressConn)

	targetUnprovision := readTunnelUnprovision(t, targetConn)
	if targetUnprovision.TunnelID != created.ID || targetUnprovision.Revision != created.Revision || targetUnprovision.Role != protocol.DataStreamRoleTarget || targetUnprovision.Reason != "updated" {
		t.Fatalf("target old unprovision mismatch: %+v", targetUnprovision)
	}
	ingressUnprovision := readTunnelUnprovision(t, ingressConn)
	if ingressUnprovision.TunnelID != created.ID || ingressUnprovision.Revision != created.Revision || ingressUnprovision.Role != protocol.DataStreamRoleIngress || ingressUnprovision.Reason != "updated" {
		t.Fatalf("ingress old unprovision mismatch: %+v", ingressUnprovision)
	}
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp = awaitMuxResponse(t, updateRespCh)
	if resp.Code != http.StatusOK {
		t.Fatalf("client_to_client update: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPI_UnifiedTunnelStopUnprovisionsClientToClientParticipants(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-stop-target", "install-c2c-stop-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-stop-ingress", "install-c2c-stop-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-stop",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	stopResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID+"/stop", token, nil)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("client_to_client stop: want 200, got %d body=%s", stopResp.Code, stopResp.Body.String())
	}

	targetUnprovision := readTunnelUnprovision(t, targetConn)
	if targetUnprovision.TunnelID != created.ID || targetUnprovision.Revision != created.Revision || targetUnprovision.Role != protocol.DataStreamRoleTarget || targetUnprovision.Reason != "stopped" {
		t.Fatalf("target stop unprovision mismatch: %+v", targetUnprovision)
	}
	ingressUnprovision := readTunnelUnprovision(t, ingressConn)
	if ingressUnprovision.TunnelID != created.ID || ingressUnprovision.Revision != created.Revision || ingressUnprovision.Role != protocol.DataStreamRoleIngress || ingressUnprovision.Reason != "stopped" {
		t.Fatalf("ingress stop unprovision mismatch: %+v", ingressUnprovision)
	}
	stored, err := s.store.GetTunnelByIDE(targetAuth.ClientID, created.ID)
	if err != nil {
		t.Fatalf("stopped tunnel should remain persisted: %v", err)
	}
	if stored.DesiredState != protocol.ProxyDesiredStateStopped || stored.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("stop should persist stopped/idle, got %s/%s", stored.DesiredState, stored.RuntimeState)
	}
}

func TestAPI_UnifiedTunnelCreateDoesNotWaitForServerExposeProvisionAck(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	s.tunnels.tunnelReadyTimeout = time.Second
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "server-expose-async-target", "install-server-expose-async-target")
	defer mustClose(t, targetConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"server-expose-async",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, reserveTCPPort(t), targetAuth.ClientID))

	started := time.Now()
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	elapsed := time.Since(started)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	if elapsed >= s.tunnels.tunnelReadyTimeout/2 {
		t.Fatalf("server_expose create should not wait for provisioning timeout, elapsed=%s timeout=%s", elapsed, s.tunnels.tunnelReadyTimeout)
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	if created.RuntimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("online server_expose create should project pending before async ACK, got %q", created.RuntimeState)
	}
}

func TestAPI_UnifiedTunnelCreatePersistsProvisionRuntimeFailure(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	s.tunnels.tunnelReadyTimeout = 20 * time.Millisecond
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-timeout-target", "install-c2c-timeout-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-timeout-ingress", "install-c2c-timeout-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-provision-timeout",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create should persist despite runtime timeout: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	if _, err := s.store.GetTunnelByIDE(targetAuth.ClientID, created.ID); err != nil {
		t.Fatalf("tunnel should remain persisted after runtime failure: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		getResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
		if getResp.Code != http.StatusOK {
			t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
		}
		var got tunnelSpecAPI
		if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
			t.Fatalf("decode tunnel: %v", err)
		}
		if got.RuntimeState == protocol.ProxyRuntimeStateError {
			if len(got.Issues) != 1 || got.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckTimeout || got.Issues[0].ClientID != targetAuth.ClientID {
				t.Fatalf("created issue mismatch: %+v", got.Issues)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime state should eventually project provisioning error, last state=%q issues=%+v", got.RuntimeState, got.Issues)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPI_UnifiedTunnelServerExposeProvisionTimeoutProjectsIssue(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	s.tunnels.tunnelReadyTimeout = 20 * time.Millisecond
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "server-expose-timeout-target", "install-server-expose-timeout-target")
	defer mustClose(t, targetConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)

	create := []byte(fmt.Sprintf(`{
		"name":"server-expose-provision-timeout",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, reserveTCPPort(t), targetAuth.ClientID))

	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose create should persist despite runtime timeout: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		getResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
		if getResp.Code != http.StatusOK {
			t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
		}
		var got tunnelSpecAPI
		if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
			t.Fatalf("decode tunnel: %v", err)
		}
		if got.RuntimeState == protocol.ProxyRuntimeStateError {
			if len(got.Issues) != 1 || got.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckTimeout || got.Issues[0].Scope != "target_client" || got.Issues[0].ClientID != targetAuth.ClientID {
				t.Fatalf("server-expose timeout issue mismatch: %+v", got.Issues)
			}
			clientsResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodGet, "/api/clients", token, nil)
			if clientsResp.Code != http.StatusOK {
				t.Fatalf("GET clients: want 200, got %d body=%s", clientsResp.Code, clientsResp.Body.String())
			}
			var clients []clientView
			if err := mustDecodeJSON(t, clientsResp.Body, &clients); err != nil {
				t.Fatalf("decode clients: %v", err)
			}
			var projected *protocol.ProxyConfig
			for i := range clients {
				if clients[i].ID != targetAuth.ClientID {
					continue
				}
				for j := range clients[i].Proxies {
					if clients[i].Proxies[j].ID == created.ID {
						projected = &clients[i].Proxies[j]
						break
					}
				}
			}
			if projected == nil {
				t.Fatalf("unified tunnel should appear in /api/clients projection")
				return
			}
			if projected.Topology != tunnelTopologyServerExpose || projected.Ingress == nil || projected.Target == nil {
				t.Fatalf("/api/clients tunnel should keep unified metadata: %+v", projected)
			}
			if len(projected.Issues) != 1 || projected.Issues[0].Code != protocol.TunnelIssueCodeProvisionAckTimeout {
				t.Fatalf("/api/clients tunnel should keep unified issues: %+v", projected.Issues)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server_expose should eventually project provisioning error, last state=%q issues=%+v", got.RuntimeState, got.Issues)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPI_UnifiedTunnelServerExposeListenFailureProjectsIssue(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	target := createUnifiedAPITestClient(t, s, "install-server-expose-listen-target", "server-expose-listen-target")
	port := reserveTCPPort(t)
	create := []byte(fmt.Sprintf(`{
		"name":"server-expose-listen-race",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, port, target.ID))
	resp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("offline server_expose create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("occupy server-expose port after create: %v", err)
	}
	defer mustClose(t, ln)

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "server-expose-listen-target", "install-server-expose-listen-target")
	defer mustClose(t, targetConn)
	if targetAuth.ClientID != target.ID {
		t.Fatalf("target client id mismatch after reconnect: want %s got %s", target.ID, targetAuth.ClientID)
	}
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	provisionReq := ackTunnelProvision(t, targetConn)
	if provisionReq.TunnelID != created.ID || provisionReq.Revision != created.Revision || provisionReq.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("server-expose provision identity mismatch: %+v", provisionReq)
	}
	if provisionReq.Spec.Topology != tunnelTopologyServerExpose || provisionReq.Spec.Target.ClientID != target.ID || provisionReq.Spec.Target.Type != tunnelTargetTypeTCPService {
		t.Fatalf("server-expose provision spec mismatch: %+v", provisionReq.Spec)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		getResp := doMuxRequest(t, s.StartHTTPOnly(), http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
		if getResp.Code != http.StatusOK {
			t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
		}
		var got tunnelSpecAPI
		if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
			t.Fatalf("decode tunnel: %v", err)
		}
		if got.RuntimeState == protocol.ProxyRuntimeStateError {
			if len(got.Issues) != 1 || got.Issues[0].Code != protocol.TunnelIssueCodeIngressPortInUse || got.Issues[0].Scope != "server" {
				t.Fatalf("server-expose listen issue mismatch: %+v", got.Issues)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server_expose should eventually project listen failure, last state=%q issues=%+v", got.RuntimeState, got.Issues)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPI_UnifiedTunnelUpdateSameIngressPortSkipsSelfPreflightConflict(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-same-port-target", "install-c2c-same-port-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-same-port-ingress", "install-c2c-same-port-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-same-port",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, ingressPort, targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	update := []byte(fmt.Sprintf(`{"expected_revision":%d,"spec":{
		"name":"c2c-same-port",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`, created.Revision, ingressAuth.ClientID, ingressPort, targetAuth.ClientID))
	updateRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	// No preflight response is sent here. Same ingress bind/port updates must not
	// ask the client to re-bind a port that the current revision already owns.
	targetUnprovision := readTunnelUnprovision(t, targetConn)
	if targetUnprovision.Revision != created.Revision || targetUnprovision.Role != protocol.DataStreamRoleTarget {
		t.Fatalf("target old unprovision mismatch: %+v", targetUnprovision)
	}
	ingressUnprovision := readTunnelUnprovision(t, ingressConn)
	if ingressUnprovision.Revision != created.Revision || ingressUnprovision.Role != protocol.DataStreamRoleIngress {
		t.Fatalf("ingress old unprovision mismatch: %+v", ingressUnprovision)
	}
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp = awaitMuxResponse(t, updateRespCh)
	if resp.Code != http.StatusOK {
		t.Fatalf("same-port client_to_client update: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPI_UnifiedTunnelUpdatePreflightFailureKeepsOldClientToClientConfig(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "c2c-preflight-update-target", "install-c2c-preflight-update-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "c2c-preflight-update-ingress", "install-c2c-preflight-update-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)
	ingressPort := reserveTCPPort(t)

	create := []byte(fmt.Sprintf(`{
		"name":"c2c-preflight-update",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, ingressPort, targetAuth.ClientID))
	createRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	respondPreflight(t, ingressConn)
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, createRespCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	update := []byte(fmt.Sprintf(`{"expected_revision":%d,"spec":{
		"name":"c2c-preflight-update",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"%s","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`, created.Revision, ingressAuth.ClientID, reserveTCPPort(t), targetAuth.ClientID))
	updateRespCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	preflightReq := rejectPreflight(t, ingressConn, protocol.TunnelMutationErrorCodeIngressPortInUse, "port already in use")
	if preflightReq.TunnelID != created.ID || preflightReq.Revision != created.Revision+1 || preflightReq.Role != protocol.DataStreamRoleIngress {
		t.Fatalf("update preflight identity mismatch: %+v", preflightReq)
	}
	resp = awaitMuxResponse(t, updateRespCh)
	if resp.Code != http.StatusConflict {
		t.Fatalf("preflight-rejected update: want 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	var mutationErr tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &mutationErr); err != nil {
		t.Fatalf("decode mutation error: %v", err)
	}
	if mutationErr.ErrorCode != protocol.TunnelMutationErrorCodeIngressPortInUse || mutationErr.Field != "ingress.config.port" {
		t.Fatalf("mutation error mismatch: %+v", mutationErr)
	}

	stored, err := s.store.GetTunnelByIDE(targetAuth.ClientID, created.ID)
	if err != nil {
		t.Fatalf("stored tunnel should remain after rejected update: %v", err)
	}
	if stored.Revision != created.Revision || stored.LocalPort != 22 {
		t.Fatalf("rejected update should keep old revision/target, got revision=%d local_port=%d", stored.Revision, stored.LocalPort)
	}
	cfg, err := decodeListenEndpointConfig(endpointSpecAPI{
		Location: stored.Ingress.Location,
		ClientID: stored.Ingress.ClientID,
		Type:     stored.Ingress.Type,
		Config:   stored.Ingress.Config,
	}, stored.Topology)
	if err != nil {
		t.Fatalf("decode stored ingress: %v", err)
	}
	if cfg.Port != ingressPort {
		t.Fatalf("rejected update should keep old ingress port: want %d, got %d", ingressPort, cfg.Port)
	}
}

func TestReleaseUnifiedRuntimeForClientUnprovisionsRemainingClientRelayParticipant(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	targetWS, targetServerWS := newTestWebSocketPair(t)
	defer mustClose(t, targetWS)
	defer mustClose(t, targetServerWS)
	s.clients.Store(stored.Target.ClientID, &ClientConn{
		ID:         stored.Target.ClientID,
		conn:       targetServerWS,
		generation: 1,
		state:      clientStateLive,
	})

	s.releaseUnifiedRuntimeForClient(stored.Ingress.ClientID)

	if _, ok := s.c2c.get(stored.ID); ok {
		t.Fatal("release should remove server C2C registry entry")
	}
	req := readTunnelUnprovision(t, targetWS)
	if req.TunnelID != stored.ID || req.Revision != stored.Revision || req.Role != protocol.DataStreamRoleTarget || req.Reason != "participant_session_released" {
		t.Fatalf("remaining target unprovision mismatch: %+v", req)
	}
}

func TestAPI_UnifiedTunnelProjectionRequiresExposedClientRelayRuntime(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	stored.RuntimeState = protocol.ProxyRuntimeStatePending
	mustAddStableTunnel(t, s.store, stored)
	caps := protocol.DefaultClientCapabilities()
	_, ingressSession := newTestClientRelayDataSession(t)
	_, targetSession := newTestClientRelayDataSession(t)
	s.clients.Store(stored.Ingress.ClientID, &ClientConn{ID: stored.Ingress.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: ingressSession})
	s.clients.Store(stored.Target.ClientID, &ClientConn{ID: stored.Target.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: targetSession})
	s.c2c.set(stored)

	spec := specFromStoredTunnel(stored, s)
	if spec.RuntimeState != protocol.ProxyRuntimeStatePending {
		t.Fatalf("pending C2C route should not project active before exposed runtime, got %q", spec.RuntimeState)
	}
	if spec.ActualTransport != tunnelActualTransportUnknown {
		t.Fatalf("pending C2C route should not project server relay transport, got %q", spec.ActualTransport)
	}

	stored.RuntimeState = protocol.ProxyRuntimeStateExposed
	spec = specFromStoredTunnel(stored, s)
	if spec.RuntimeState != tunnelRuntimeStateActive || spec.ActualTransport != protocol.ActualTransportServerRelay {
		t.Fatalf("exposed C2C route should project active server relay, got state=%q transport=%q", spec.RuntimeState, spec.ActualTransport)
	}
}

func TestAPI_UnifiedTunnelListKeepsSameNameLiveTunnelsWithoutIDs(t *testing.T) {
	s := New(0)
	now := time.Now().UTC()
	s.clients.Store("client-a", &ClientConn{
		ID:    "client-a",
		state: clientStateLive,
		proxies: map[string]*ProxyTunnel{"web": {
			Config: protocol.ProxyConfig{
				Name:         "web",
				Type:         protocol.ProxyTypeTCP,
				LocalIP:      "127.0.0.1",
				LocalPort:    8080,
				RemotePort:   18080,
				ClientID:     "client-a",
				CreatedAt:    now,
				DesiredState: protocol.ProxyDesiredStateRunning,
				RuntimeState: protocol.ProxyRuntimeStateExposed,
			},
			done: make(chan struct{}),
		}},
	})
	s.clients.Store("client-b", &ClientConn{
		ID:    "client-b",
		state: clientStateLive,
		proxies: map[string]*ProxyTunnel{"web": {
			Config: protocol.ProxyConfig{
				Name:         "web",
				Type:         protocol.ProxyTypeTCP,
				LocalIP:      "127.0.0.1",
				LocalPort:    8081,
				RemotePort:   18081,
				ClientID:     "client-b",
				CreatedAt:    now.Add(time.Second),
				DesiredState: protocol.ProxyDesiredStateRunning,
				RuntimeState: protocol.ProxyRuntimeStateExposed,
			},
			done: make(chan struct{}),
		}},
	})

	specs, err := s.allUnifiedTunnelSpecs()
	if err != nil {
		t.Fatalf("list unified tunnels: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("same-name live tunnels without ids should not be collapsed, got %d: %+v", len(specs), specs)
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		seen[spec.OwnerClientID] = true
	}
	if !seen["client-a"] || !seen["client-b"] {
		t.Fatalf("same-name live tunnels should include both clients, got %+v", specs)
	}
}

func addUnifiedC2CTestTunnel(t *testing.T, s *Server, name, ingressClientID, targetClientID string, ingressPort int) StoredTunnel {
	t.Helper()
	req := tunnelCreateRequestAPI{
		Name:            name,
		Topology:        tunnelTopologyClientToClient,
		TransportPolicy: tunnelTransportPolicyServerRelayOnly,
		Ingress: endpointSpecAPI{
			Location: tunnelEndpointLocationClient,
			ClientID: ingressClientID,
			Type:     tunnelIngressTypeTCPListen,
			Config: mustRawJSON(tcpListenConfigAPI{
				BindIP:             "127.0.0.1",
				Port:               ingressPort,
				AllowedSourceCIDRs: allowAllSourceCIDRs(),
			}),
		},
		Target: endpointSpecAPI{
			Location: tunnelEndpointLocationClient,
			ClientID: targetClientID,
			Type:     tunnelTargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
	}
	stored, err := s.storedTunnelFromUnifiedRequest(req, "")
	if err != nil {
		t.Fatalf("build stored tunnel: %v", err)
	}
	if err := s.store.AddTunnel(stored); err != nil {
		t.Fatalf("add stored tunnel: %v", err)
	}
	return stored
}

func TestAPI_UnifiedTunnelProjectsRuntimeReportIssuesFromMemory(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-issue-target", "issue-target")
	ingress := createUnifiedAPITestClient(t, s, "install-issue-ingress", "issue-ingress")
	stored := addUnifiedC2CTestTunnel(t, s, "issue-c2c", ingress.ID, target.ID, 24001)

	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	s.clients.Store(target.ID, &ClientConn{ID: target.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: targetSession})
	s.clients.Store(ingress.ID, &ClientConn{ID: ingress.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: ingressSession})
	s.unifiedRuntime.clearServerIssues(stored.ID)
	s.unifiedRuntime.recordReport(ingress.ID, protocol.TunnelRuntimeReport{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "ingress listener failed",
	}, time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC))

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+stored.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	foundRuntimeIssue := false
	for _, issue := range got.Issues {
		if issue.Scope == "ingress_client" && issue.ClientID == ingress.ID && issue.Message == "ingress listener failed" {
			foundRuntimeIssue = true
			break
		}
	}
	if !foundRuntimeIssue {
		t.Fatalf("expected projected runtime report issue, got %+v", got.Issues)
	}
}

func TestServer_TunnelRuntimeReportIgnoresStaleRevision(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	s.clients.Store(stored.Target.ClientID, &ClientConn{ID: stored.Target.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, generation: 1, state: clientStateLive, dataSession: targetSession})
	ingressClient := &ClientConn{ID: stored.Ingress.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, generation: 1, state: clientStateLive, dataSession: ingressSession}
	s.clients.Store(stored.Ingress.ClientID, ingressClient)

	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelRuntimeReport, protocol.TunnelRuntimeReport{
		TunnelID: stored.ID,
		Revision: stored.Revision - 1,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "stale listener failure",
	})
	if err != nil {
		t.Fatalf("build runtime report: %v", err)
	}

	s.handleTunnelRuntimeReportMessage(ingressClient, *msg)

	spec := specFromStoredTunnel(stored, s)
	if len(spec.Issues) != 0 {
		t.Fatalf("stale runtime report should not project issues, got %+v", spec.Issues)
	}
}

func TestServer_TunnelRuntimeReportIgnoresWrongRoleClient(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)

	caps := protocol.DefaultClientCapabilities()
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	targetClient := &ClientConn{ID: stored.Target.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, generation: 1, state: clientStateLive, dataSession: targetSession}
	s.clients.Store(stored.Target.ClientID, targetClient)
	s.clients.Store(stored.Ingress.ClientID, &ClientConn{ID: stored.Ingress.ClientID, Info: protocol.ClientInfo{Capabilities: &caps}, generation: 1, state: clientStateLive, dataSession: ingressSession})

	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelRuntimeReport, protocol.TunnelRuntimeReport{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "wrong client listener failure",
	})
	if err != nil {
		t.Fatalf("build runtime report: %v", err)
	}

	s.handleTunnelRuntimeReportMessage(targetClient, *msg)

	spec := specFromStoredTunnel(stored, s)
	if len(spec.Issues) != 0 {
		t.Fatalf("wrong-role runtime report should not project issues, got %+v", spec.Issues)
	}
}

func TestServer_TunnelRuntimeReportSchedulesReconcile(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)
	stored := testClientRelayStoredTunnel(t)
	mustAddStableTunnel(t, s.store, stored)
	s.c2c.set(stored)

	caps := protocol.DefaultClientCapabilities()
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	targetClient := &ClientConn{
		ID:          stored.Target.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		generation:  1,
		state:       clientStateLive,
		dataSession: targetSession,
	}
	ingressClient := &ClientConn{
		ID:          stored.Ingress.ClientID,
		Info:        protocol.ClientInfo{Capabilities: &caps},
		generation:  1,
		state:       clientStateLive,
		dataSession: ingressSession,
	}
	s.clients.Store(stored.Target.ClientID, targetClient)
	s.clients.Store(stored.Ingress.ClientID, ingressClient)

	msg, err := protocol.NewMessage(protocol.MsgTypeTunnelRuntimeReport, protocol.TunnelRuntimeReport{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "ingress listener failed",
	})
	if err != nil {
		t.Fatalf("build runtime report: %v", err)
	}

	s.handleTunnelRuntimeReportMessage(ingressClient, *msg)

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
		if err != nil {
			t.Fatalf("load tunnel: %v", err)
		}
		spec := specFromStoredTunnel(got, s)
		if got.RuntimeState == protocol.ProxyRuntimeStateError &&
			len(spec.Issues) > 0 &&
			spec.Issues[0].Code == protocol.TunnelIssueCodeProvisionAckRejected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime report should trigger reconcile and provisioning issue, state=%q issues=%+v", got.RuntimeState, spec.Issues)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPI_UnifiedTunnelCapabilityLossProjectsError(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-capability-loss-target", "capability-loss-target")
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, unifiedCreatePayload("capability-loss", target.ID, reserveTCPPort(t)))
	if resp.Code != http.StatusCreated {
		t.Fatalf("server_expose create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	noCaps := protocol.ClientCapabilities{}
	_, targetSession := newTestClientRelayDataSession(t)
	s.clients.Store(target.ID, &ClientConn{
		ID:          target.ID,
		Info:        protocol.ClientInfo{Capabilities: &noCaps},
		dataSession: targetSession,
		generation:  1,
		state:       clientStateLive,
	})

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("capability loss should project error, got %q", got.RuntimeState)
	}
	if len(got.Issues) != 1 || got.Issues[0].Code != protocol.TunnelIssueCodeCapabilityNotSupported || got.Issues[0].ClientID != target.ID {
		t.Fatalf("capability issue mismatch: %+v", got.Issues)
	}
}

func TestAPI_UnifiedTunnelSuppressesRuntimeReportIssuesWhenClientOffline(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-issue-offline-target", "issue-offline-target")
	ingress := createUnifiedAPITestClient(t, s, "install-issue-offline-ingress", "issue-offline-ingress")
	body := []byte(`{
		"name":"issue-offline-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":24002,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusCreated {
		t.Fatalf("client_to_client create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}

	s.clients.Store(target.ID, &ClientConn{ID: target.ID, state: clientStateLive})
	s.unifiedRuntime.recordReport(ingress.ID, protocol.TunnelRuntimeReport{
		TunnelID: created.ID,
		Revision: created.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "old online listener failure",
	}, time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC))

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	if len(got.Issues) != 0 {
		t.Fatalf("offline ingress should suppress old runtime issues, got %+v", got.Issues)
	}
}

func TestAPI_UnifiedTunnelRejectsClientIngressResourceConflictBeforePersist(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	targetA := createUnifiedAPITestClient(t, s, "install-conflict-target-a", "conflict-target-a")
	targetB := createUnifiedAPITestClient(t, s, "install-conflict-target-b", "conflict-target-b")
	ingress := createUnifiedAPITestClient(t, s, "install-conflict-ingress", "conflict-ingress")
	first := []byte(`{
		"name":"first-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":25001,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + targetA.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, first)
	if resp.Code != http.StatusCreated {
		t.Fatalf("first c2c create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	conflict := []byte(`{
		"name":"conflict-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":25001,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + targetB.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, conflict)
	if resp.Code != http.StatusConflict {
		t.Fatalf("conflicting c2c create: want 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode conflict error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeIngressResourceConflict || bodyResp.Code != protocol.TunnelMutationErrorCodeIngressResourceConflict || bodyResp.Field != "ingress.config.port" {
		t.Fatalf("conflict error mismatch: %+v", bodyResp)
	}
	if _, err := s.store.GetTunnelByIDE(targetB.ID, "conflict-c2c"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("conflicting create must not persist, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelRejectsOccupiedServerExposePortBeforePersist(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-server-port-busy-target", "server-port-busy-target")
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("occupy tcp port: %v", err)
	}
	defer mustClose(t, ln)
	port := ln.Addr().(*net.TCPAddr).Port

	body := []byte(`{
		"name":"server-port-busy",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusConflict {
		t.Fatalf("occupied server port create: want 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode port error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeIngressPortInUse || bodyResp.Field != "ingress.config.port" {
		t.Fatalf("occupied port error mismatch: %+v", bodyResp)
	}
	if _, err := s.store.GetTunnelByIDE(target.ID, "server-port-busy"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("failed server port preflight must not persist config, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelAllowsServerExposeTCPAndUDPSamePort(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-server-shared-port-target", "server-shared-port-target")
	port := reserveTCPPort(t)
	tcpBody := []byte(`{
		"name":"server-shared-tcp",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, tcpBody)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server TCP create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	udpBody := []byte(`{
		"name":"server-shared-udp",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
		"transport_policy":"server_relay_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, udpBody)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server UDP same-port create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPI_UnifiedTunnelRejectsServerExposeTCPAndSOCKS5SamePort(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-server-socks5-conflict-target", "server-socks5-conflict-target")
	port := reserveTCPPort(t)
	tcpBody := []byte(`{
		"name":"server-conflict-tcp",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, tcpBody)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server TCP create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	socksBody := []byte(`{
		"name":"server-conflict-socks5",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":` + strconv.Itoa(port) + `,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":10
		}},
		"transport_policy":"server_relay_only",
		"confirm_no_auth_risk":true
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, socksBody)
	if resp.Code != http.StatusConflict {
		t.Fatalf("server SOCKS5 same TCP port create: want 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode conflict error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeIngressResourceConflict || bodyResp.Field != "ingress.config.port" {
		t.Fatalf("conflict error mismatch: %+v", bodyResp)
	}
}

func TestAPI_UnifiedTunnelOnlineIngressPreflightFailureDoesNotPersist(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-preflight-target", "preflight-target")
	ingress := createUnifiedAPITestClient(t, s, "install-preflight-ingress", "preflight-ingress")
	caps := protocol.DefaultClientCapabilities()
	s.clients.Store(ingress.ID, &ClientConn{
		ID:    ingress.ID,
		Info:  protocol.ClientInfo{Hostname: "preflight-ingress", Capabilities: &caps},
		state: clientStateLive,
	})

	body := []byte(`{
		"name":"preflight-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":26001,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("online ingress without control conn should fail preflight before persist: want 502, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode preflight error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeIngressPreflightRejected || bodyResp.Field != "ingress.config.port" {
		t.Fatalf("preflight error mismatch: %+v", bodyResp)
	}
	if _, err := s.store.GetTunnelByIDE(target.ID, "preflight-c2c"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("failed preflight must not persist config, got err=%v", err)
	}
}

func TestAPI_UnifiedTunnelSOCKS5ClientIngressPreflightUsesMinimalConfig(t *testing.T) {
	s := New(0)
	initTestAdminStore(t, s)
	s.store = newTestTunnelStore(t)
	ts := httptest.NewServer(s.newHTTPMux())
	defer ts.Close()
	token := loginAdminTokenLocal(t, s.StartHTTPOnly(), "admin", "password123")

	targetConn, targetAuth := connectAndAuthWithInstallID(t, ts, "socks5-preflight-target", "install-socks5-preflight-target")
	defer mustClose(t, targetConn)
	ingressConn, ingressAuth := connectAndAuthWithInstallID(t, ts, "socks5-preflight-ingress", "install-socks5-preflight-ingress")
	defer mustClose(t, ingressConn)
	setLiveClientDefaultCapabilities(t, s, targetAuth.ClientID)
	setLiveClientDefaultCapabilities(t, s, ingressAuth.ClientID)

	secret := "preflight-secret"
	port := reserveTCPPort(t)
	create := []byte(fmt.Sprintf(`{
		"name":"socks5-preflight-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"%s","type":"socks5_listen","config":{
			"bind_ip":"127.0.0.1",
			"port":%d,
			"allowed_source_cidrs":["127.0.0.0/8"],
			"auth":{"type":"username_password","username":"alice","password":"%s"}
		}},
		"target":{"location":"client","client_id":"%s","type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"dial_timeout_seconds":10
		}},
		"transport_policy":"server_relay_only"
	}`, ingressAuth.ClientID, port, secret, targetAuth.ClientID))
	respCh := doMuxRequestAsync(t, s.StartHTTPOnly(), http.MethodPost, "/api/tunnels", token, create)
	preflightReq := acceptPreflight(t, ingressConn)
	if preflightReq.Ingress.Type != protocol.IngressTypeSOCKS5Listen {
		t.Fatalf("SOCKS5 preflight type mismatch: %+v", preflightReq.Ingress)
	}
	if bytes.Contains(preflightReq.Ingress.Config, []byte(secret)) ||
		bytes.Contains(preflightReq.Ingress.Config, []byte(`"auth"`)) ||
		bytes.Contains(preflightReq.Ingress.Config, []byte(`"password_hash"`)) ||
		bytes.Contains(preflightReq.Ingress.Config, []byte(`"password"`)) {
		t.Fatalf("SOCKS5 preflight must not carry auth material, got %s", string(preflightReq.Ingress.Config))
	}
	var bind tcpListenConfigAPI
	if err := json.Unmarshal(preflightReq.Ingress.Config, &bind); err != nil {
		t.Fatalf("decode preflight bind config: %v", err)
	}
	if bind.BindIP != "127.0.0.1" || bind.Port != port {
		t.Fatalf("preflight bind config mismatch: %+v", bind)
	}
	ackProvisionMessages(t, targetConn, 1)
	ackProvisionMessages(t, ingressConn, 1)
	resp := awaitMuxResponse(t, respCh)
	if resp.Code != http.StatusCreated {
		t.Fatalf("SOCKS5 c2c create: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestReconcileUnifiedTunnelRoutesClientToClientThroughSingleEntry(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-reconcile-target", "reconcile-target")
	ingress := createUnifiedAPITestClient(t, s, "install-reconcile-ingress", "reconcile-ingress")
	stored, err := s.storedTunnelFromUnifiedRequest(tunnelCreateRequestAPI{
		Name:            "reconcile-c2c",
		Topology:        tunnelTopologyClientToClient,
		TransportPolicy: tunnelTransportPolicyServerRelayOnly,
		Ingress: endpointSpecAPI{
			Location: tunnelEndpointLocationClient,
			ClientID: ingress.ID,
			Type:     tunnelIngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: 27001, AllowedSourceCIDRs: allowAllSourceCIDRs()}),
		},
		Target: endpointSpecAPI{
			Location: tunnelEndpointLocationClient,
			ClientID: target.ID,
			Type:     tunnelTargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
	}, "")
	if err != nil {
		t.Fatalf("build stored tunnel: %v", err)
	}
	if err := s.store.AddTunnel(stored); err != nil {
		t.Fatalf("add tunnel: %v", err)
	}

	if err := s.reconcileUnifiedTunnel(stored.ID, "test"); err != nil {
		t.Fatalf("reconcile unified tunnel: %v", err)
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	if reloaded.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("offline c2c reconcile should project offline, got %q", reloaded.RuntimeState)
	}
}

func TestReconcileUnifiedTunnelRoutesServerExposeThroughSingleEntry(t *testing.T) {
	s, _, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-reconcile-server-expose", "reconcile-server-expose")
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "server-expose-reconcile-id",
			Name:       "server-expose-reconcile",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
			RemotePort: reserveTCPPort(t),
		},
		ClientID:        record.ID,
		OwnerClientID:   record.ID,
		Binding:         TunnelBindingClientID,
		Revision:        3,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportServerRelay,
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: 22099}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: record.ID,
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := stored.normalize(); err != nil {
		t.Fatalf("normalize stored tunnel: %v", err)
	}
	mustAddStableTunnel(t, s.store, stored)

	client := &ClientConn{
		ID:         record.ID,
		Info:       protocol.ClientInfo{Hostname: "reconcile-server-expose"},
		proxies:    make(map[string]*ProxyTunnel),
		generation: 1,
		state:      clientStateLive,
	}
	s.clients.Store(record.ID, client)

	if err := s.reconcileUnifiedTunnel(stored.ID, "test"); err != nil {
		t.Fatalf("reconcile unified server-expose tunnel: %v", err)
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	if reloaded.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("server-expose reconcile without data session should project offline, got %q", reloaded.RuntimeState)
	}
}

func TestAPI_UnifiedTunnelAcceptsAllServerRelayCloseoutShapes(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-shapes-target", "shapes-target")
	ingress := createUnifiedAPITestClient(t, s, "install-shapes-ingress", "shapes-ingress")

	cases := []struct {
		name       string
		body       func() []byte
		topology   string
		ingress    string
		targetType string
	}{
		{
			name: "server tcp",
			body: func() []byte {
				return []byte(`{
				"name":"shape-server-tcp",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
				"transport_policy":"server_relay_only"
			}`)
			},
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeTCPListen, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "server udp",
			body: func() []byte {
				return []byte(`{
				"name":"shape-server-udp",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveUDPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
				"transport_policy":"server_relay_only"
			}`)
			},
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeUDPListen, targetType: tunnelTargetTypeUDPService,
		},
		{
			name: "server http",
			body: func() []byte {
				return []byte(`{
				"name":"shape-server-http",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"http_host","config":{"domain":"shape-http.example.com","allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
				"transport_policy":"server_relay_only"
			}`)
			},
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeHTTPHost, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "c2c tcp",
			body: func() []byte {
				return []byte(`{
				"name":"shape-c2c-tcp",
				"topology":"client_to_client",
				"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":` + strconv.Itoa(reserveTCPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
				"transport_policy":"server_relay_only"
			}`)
			},
			topology: tunnelTopologyClientToClient, ingress: tunnelIngressTypeTCPListen, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "c2c udp",
			body: func() []byte {
				return []byte(`{
				"name":"shape-c2c-udp",
				"topology":"client_to_client",
				"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"udp_listen","config":{"bind_ip":"127.0.0.1","port":` + strconv.Itoa(reserveUDPPort(t)) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
				"transport_policy":"server_relay_only"
			}`)
			},
			topology: tunnelTopologyClientToClient, ingress: tunnelIngressTypeUDPListen, targetType: tunnelTargetTypeUDPService,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp *httptest.ResponseRecorder
			for attempt := 0; attempt < 3; attempt++ {
				resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, tc.body())
				if resp.Code != http.StatusConflict || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeIngressPortInUse) {
					break
				}
			}
			if resp.Code != http.StatusCreated {
				t.Fatalf("POST /api/tunnels: want 201, got %d body=%s", resp.Code, resp.Body.String())
			}
			var created tunnelSpecAPI
			if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
				t.Fatalf("decode create response: %v", err)
			}
			if created.Topology != tc.topology || created.Ingress.Type != tc.ingress || created.Target.Type != tc.targetType {
				t.Fatalf("unexpected created shape: %+v", created)
			}
			if created.TransportPolicy != tunnelTransportPolicyServerRelayOnly {
				t.Fatalf("transport policy: want server relay only, got %q", created.TransportPolicy)
			}
			if created.ActualTransport != tunnelActualTransportUnknown {
				t.Fatalf("offline create should not claim active transport, got %q", created.ActualTransport)
			}
		})
	}
}

func TestAPI_UnifiedTunnelProjectionIgnoresPersistedRuntimeTruth(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	client := createUnifiedAPITestClient(t, s, "install-projection-ignore", "projection-ignore")
	stored := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID:         "projection-ignore-id",
			Name:       "projection-ignore",
			Type:       protocol.ProxyTypeTCP,
			LocalIP:    "127.0.0.1",
			LocalPort:  22,
			RemotePort: reserveTCPPort(t),
		},
		ClientID:        client.ID,
		OwnerClientID:   client.ID,
		Binding:         TunnelBindingClientID,
		Revision:        4,
		Topology:        TunnelTopologyServerExpose,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		ActualTransport: protocol.ActualTransportServerRelay,
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer,
			Type:     protocol.IngressTypeTCPListen,
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "0.0.0.0", Port: reserveTCPPort(t)}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient,
			ClientID: client.ID,
			Type:     protocol.TargetTypeTCPService,
			Config:   mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 22}),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	mustAddStableTunnel(t, s.store, stored)

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+stored.ID, token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	if got.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("computed runtime should ignore stored exposed when client offline, got %q", got.RuntimeState)
	}
	if got.ActualTransport != tunnelActualTransportUnknown {
		t.Fatalf("computed transport should ignore stored server_relay when inactive, got %q", got.ActualTransport)
	}
	if got.Error != "" || len(got.Issues) != 0 {
		t.Fatalf("stored runtime cache must not project errors/issues as truth, error=%q issues=%+v", got.Error, got.Issues)
	}
}

func TestUnifiedRestoreRoutesClientToClientThroughUnifiedReconcile(t *testing.T) {
	s := New(0)
	s.store = newTestTunnelStore(t)

	stored := testClientRelayStoredTunnel(t)
	stored.RuntimeState = protocol.ProxyRuntimeStateOffline
	mustAddStableTunnel(t, s.store, stored)

	client := &ClientConn{ID: stored.OwnerClientID, generation: 1, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(client.ID, client)

	s.restoreTunnels(client)

	if _, ok := client.proxies[stored.Name]; ok {
		t.Fatal("C2C restore must not use legacy server-expose proxy map")
	}
	reloaded, err := s.store.GetTunnelByIDE(stored.OwnerClientID, stored.ID)
	if err != nil {
		t.Fatalf("reload tunnel: %v", err)
	}
	if reloaded.RuntimeState != protocol.ProxyRuntimeStateOffline {
		t.Fatalf("C2C restore should reconcile to offline without ingress/target sessions, got %q", reloaded.RuntimeState)
	}
}

func TestAPI_UnifiedTunnelRejectsServerExposeUnsupportedTargetCapability(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	caps := protocol.DefaultClientCapabilities()
	caps.TargetTypes = []string{protocol.TargetTypeUDPService}
	record, err := s.auth.adminStore.GetOrCreateClient("install-server-expose-no-tcp", protocol.ClientInfo{
		Hostname:     "server-expose-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	client := &ClientConn{ID: record.ID, generation: 1, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)}
	client.SetInfo(protocol.ClientInfo{
		Hostname:     "server-expose-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	})
	s.clients.Store(record.ID, client)

	port := reserveTCPPort(t)
	body := []byte(`{
		"name":"server-expose-unsupported-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unsupported server-expose target capability: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeCapabilityNotSupported || bodyResp.Field != "target.type" {
		t.Fatalf("capability error mismatch: %+v", bodyResp)
	}
	if _, err := s.store.GetTunnelE(record.ID, "server-expose-unsupported-target"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("unsupported capability reject must not persist config, got err=%v", err)
	}
	if _, exists := client.proxies["server-expose-unsupported-target"]; exists {
		t.Fatal("unsupported capability reject must not create server-expose runtime")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("unsupported capability reject must not leave tcp listener on port %d: %v", port, err)
	}
	mustClose(t, ln)
}

func TestAPI_UnifiedTunnelRejectsUnsupportedEndpointCapabilitiesBeforePersist(t *testing.T) {
	for _, tc := range []struct {
		name              string
		topology          string
		ingressType       string
		targetType        string
		ingressCaps       protocol.ClientCapabilities
		targetCaps        protocol.ClientCapabilities
		wantField         string
		expectIngressPeer bool
	}{
		{
			name:        "server expose target missing udp",
			topology:    tunnelTopologyServerExpose,
			ingressType: protocol.IngressTypeUDPListen,
			targetType:  protocol.TargetTypeUDPService,
			ingressCaps: protocol.ClientCapabilities{},
			targetCaps: protocol.ClientCapabilities{
				IngressTypes: protocol.DefaultClientCapabilities().IngressTypes,
				TargetTypes:  []string{protocol.TargetTypeTCPService, protocol.TargetTypeSOCKS5ConnectHandler},
			},
			wantField: "target.type",
		},
		{
			name:        "server expose target missing socks5",
			topology:    tunnelTopologyServerExpose,
			ingressType: protocol.IngressTypeSOCKS5Listen,
			targetType:  protocol.TargetTypeSOCKS5ConnectHandler,
			ingressCaps: protocol.ClientCapabilities{},
			targetCaps: protocol.ClientCapabilities{
				IngressTypes: protocol.DefaultClientCapabilities().IngressTypes,
				TargetTypes:  []string{protocol.TargetTypeTCPService, protocol.TargetTypeUDPService},
			},
			wantField: "target.type",
		},
		{
			name:              "client relay target missing tcp",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeTCPListen,
			targetType:        protocol.TargetTypeTCPService,
			ingressCaps:       protocol.DefaultClientCapabilities(),
			targetCaps:        protocol.ClientCapabilities{IngressTypes: protocol.DefaultClientCapabilities().IngressTypes, TargetTypes: []string{protocol.TargetTypeUDPService, protocol.TargetTypeSOCKS5ConnectHandler}},
			wantField:         "target.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay target missing udp",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeUDPListen,
			targetType:        protocol.TargetTypeUDPService,
			ingressCaps:       protocol.DefaultClientCapabilities(),
			targetCaps:        protocol.ClientCapabilities{IngressTypes: protocol.DefaultClientCapabilities().IngressTypes, TargetTypes: []string{protocol.TargetTypeTCPService, protocol.TargetTypeSOCKS5ConnectHandler}},
			wantField:         "target.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay target missing socks5",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeSOCKS5Listen,
			targetType:        protocol.TargetTypeSOCKS5ConnectHandler,
			ingressCaps:       protocol.DefaultClientCapabilities(),
			targetCaps:        protocol.ClientCapabilities{IngressTypes: protocol.DefaultClientCapabilities().IngressTypes, TargetTypes: []string{protocol.TargetTypeTCPService, protocol.TargetTypeUDPService}},
			wantField:         "target.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay reports target before ingress when both missing",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeTCPListen,
			targetType:        protocol.TargetTypeTCPService,
			ingressCaps:       protocol.ClientCapabilities{},
			targetCaps:        protocol.ClientCapabilities{},
			wantField:         "target.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay ingress missing tcp",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeTCPListen,
			targetType:        protocol.TargetTypeTCPService,
			ingressCaps:       protocol.ClientCapabilities{IngressTypes: []string{protocol.IngressTypeUDPListen, protocol.IngressTypeSOCKS5Listen}, TargetTypes: protocol.DefaultClientCapabilities().TargetTypes},
			targetCaps:        protocol.DefaultClientCapabilities(),
			wantField:         "ingress.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay ingress missing udp",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeUDPListen,
			targetType:        protocol.TargetTypeUDPService,
			ingressCaps:       protocol.ClientCapabilities{IngressTypes: []string{protocol.IngressTypeTCPListen, protocol.IngressTypeSOCKS5Listen}, TargetTypes: protocol.DefaultClientCapabilities().TargetTypes},
			targetCaps:        protocol.DefaultClientCapabilities(),
			wantField:         "ingress.type",
			expectIngressPeer: true,
		},
		{
			name:              "client relay ingress missing socks5",
			topology:          tunnelTopologyClientToClient,
			ingressType:       protocol.IngressTypeSOCKS5Listen,
			targetType:        protocol.TargetTypeSOCKS5ConnectHandler,
			ingressCaps:       protocol.ClientCapabilities{IngressTypes: []string{protocol.IngressTypeTCPListen, protocol.IngressTypeUDPListen}, TargetTypes: protocol.DefaultClientCapabilities().TargetTypes},
			targetCaps:        protocol.DefaultClientCapabilities(),
			wantField:         "ingress.type",
			expectIngressPeer: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, token, cleanup := setupTestServerWithStores(t, true)
			defer cleanup()

			target := createUnifiedAPITestClientWithCapabilities(t, s, "install-"+strings.ReplaceAll(tc.name, " ", "-")+"-target", tc.name+"-target", tc.targetCaps)
			ingressClientID := ""
			ingressLocation := protocol.EndpointLocationServer
			if tc.expectIngressPeer {
				ingress := createUnifiedAPITestClientWithCapabilities(t, s, "install-"+strings.ReplaceAll(tc.name, " ", "-")+"-ingress", tc.name+"-ingress", tc.ingressCaps)
				ingressClientID = ingress.ID
				ingressLocation = protocol.EndpointLocationClient
			}

			name := "unsupported-cap-" + strings.ReplaceAll(strings.ReplaceAll(tc.name, " ", "-"), "_", "-")
			body := []byte(`{
				"name":"` + name + `",
				"topology":"` + tc.topology + `",
				"ingress":{"location":"` + ingressLocation + `","client_id":"` + ingressClientID + `","type":"` + tc.ingressType + `","config":` + unifiedCapabilityTestConfig(t, tc.ingressType) + `},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"` + tc.targetType + `","config":` + unifiedCapabilityTestConfig(t, tc.targetType) + `},
				"transport_policy":"server_relay_only",
				"confirm_no_auth_risk":true
			}`)

			resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("unsupported capability: want 400, got %d body=%s", resp.Code, resp.Body.String())
			}
			var bodyResp tunnelMutationErrorResponse
			if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeCapabilityNotSupported || bodyResp.Field != tc.wantField {
				t.Fatalf("capability error mismatch: %+v", bodyResp)
			}
			assertUnsupportedCapabilityRejectDidNotPersist(t, s, tc.topology, name, target.ID, ingressClientID)
		})
	}
}

func assertUnsupportedCapabilityRejectDidNotPersist(t *testing.T, s *Server, topology, name, targetClientID, ingressClientID string) {
	t.Helper()
	if _, err := s.store.GetTunnelE(targetClientID, name); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("unsupported capability reject must not persist target-owned config, got err=%v", err)
	}
	switch topology {
	case tunnelTopologyServerExpose:
		if ingressClientID != "" {
			t.Fatalf("server-expose capability reject fixture should not have a client ingress owner, got %q", ingressClientID)
		}
	case tunnelTopologyClientToClient:
		if ingressClientID == "" {
			t.Fatal("client-to-client capability reject fixture must have an ingress client owner")
		}
		if _, err := s.store.GetTunnelE(ingressClientID, name); !errors.Is(err, ErrTunnelNotFound) {
			t.Fatalf("unsupported capability reject must not persist ingress-owned config, got err=%v", err)
		}
	default:
		t.Fatalf("unsupported topology fixture %q", topology)
	}
}

func TestAPI_UnifiedTunnelRejectsServerExposeUnsupportedIngressTypeWithoutResidualState(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record, err := s.auth.adminStore.GetOrCreateClient("install-server-expose-unknown-ingress", protocol.ClientInfo{
		Hostname: "server-expose-unknown-ingress",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	client := &ClientConn{ID: record.ID, generation: 1, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)}
	client.SetInfo(protocol.ClientInfo{
		Hostname: "server-expose-unknown-ingress",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	})
	s.clients.Store(record.ID, client)

	port := reserveTCPPort(t)
	body := []byte(`{
		"name":"server-expose-unsupported-ingress",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"future_ingress","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unsupported server-expose ingress type: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeUnsupportedEndpointType || bodyResp.Field != "ingress.type" {
		t.Fatalf("unsupported ingress error mismatch: %+v", bodyResp)
	}
	if _, err := s.store.GetTunnelE(record.ID, "server-expose-unsupported-ingress"); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("unsupported ingress reject must not persist config, got err=%v", err)
	}
	if _, exists := client.proxies["server-expose-unsupported-ingress"]; exists {
		t.Fatal("unsupported ingress reject must not create server-expose runtime")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		t.Fatalf("unsupported ingress reject must not leave tcp listener on port %d: %v", port, err)
	}
	mustClose(t, ln)
}

func TestAPI_UnifiedTunnelUpdateRejectsServerExposeUnsupportedTargetCapabilityWithoutResidualState(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	caps := protocol.DefaultClientCapabilities()
	record, err := s.auth.adminStore.GetOrCreateClient("install-server-expose-update-no-tcp", protocol.ClientInfo{
		Hostname:     "server-expose-update-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	oldPort := reserveTCPPort(t)
	create := []byte(`{
		"name":"server-expose-update-unsupported-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(oldPort) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, create)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create server-expose tunnel: want 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := mustDecodeJSON(t, resp.Body, &created); err != nil {
		t.Fatalf("decode created tunnel: %v", err)
	}
	before, err := s.store.GetTunnelByIDE(record.ID, created.ID)
	if err != nil {
		t.Fatalf("load created tunnel: %v", err)
	}
	client := &ClientConn{ID: record.ID, generation: 1, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)}
	client.SetInfo(protocol.ClientInfo{
		Hostname:     "server-expose-update-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	})
	s.clients.Store(record.ID, client)
	client.proxies[before.Name] = &ProxyTunnel{Config: storedTunnelToProxyConfig(before)}

	caps.TargetTypes = []string{protocol.TargetTypeUDPService}
	client.SetInfo(protocol.ClientInfo{
		Hostname:     "server-expose-update-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	})
	_, err = s.auth.adminStore.GetOrCreateClient("install-server-expose-update-no-tcp", protocol.ClientInfo{
		Hostname:     "server-expose-update-no-tcp",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &caps,
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("update client capabilities: %v", err)
	}

	newPort := reserveTCPPort(t)
	update := []byte(`{"expected_revision":` + strconv.FormatInt(created.Revision, 10) + `,"spec":{
		"name":"server-expose-update-unsupported-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(newPort) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`)
	resp = doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, update)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unsupported capability update: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode update error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeCapabilityNotSupported || bodyResp.Field != "target.type" {
		t.Fatalf("capability update error mismatch: %+v", bodyResp)
	}

	after, err := s.store.GetTunnelByIDE(record.ID, created.ID)
	if err != nil {
		t.Fatalf("reload tunnel after rejected update: %v", err)
	}
	if after.Revision != before.Revision || after.LocalPort != before.LocalPort || after.RemotePort != before.RemotePort || after.Name != before.Name {
		t.Fatalf("rejected update mutated stored tunnel:\n before=%+v\n after=%+v", before, after)
	}
	if !bytes.Equal(after.Ingress.Config, before.Ingress.Config) || !bytes.Equal(after.Target.Config, before.Target.Config) {
		t.Fatalf("rejected update mutated endpoint config:\n before=%+v\n after=%+v", before, after)
	}
	if got := len(client.proxies); got != 1 {
		t.Fatalf("rejected update must not replace or add server-expose runtime, got %d runtime(s)", got)
	}
	if runtime, ok := client.proxies[before.Name]; !ok || runtime.Config.RemotePort != oldPort {
		t.Fatalf("old runtime should remain unchanged after rejected update: %+v", runtime)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", newPort))
	if err != nil {
		t.Fatalf("rejected update must not leave tcp listener on new port %d: %v", newPort, err)
	}
	mustClose(t, ln)
}

func TestAPI_UnifiedTunnelUpdateRejectsClientRelayUnsupportedIngressCapabilityBeforePersist(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-c2c-update-target", "c2c-update-target")
	ingress := createUnifiedAPITestClient(t, s, "install-c2c-update-ingress", "c2c-update-ingress")
	oldPort := reserveTCPPort(t)
	stored := addUnifiedC2CTestTunnel(t, s, "c2c-update-unsupported-ingress", ingress.ID, target.ID, oldPort)
	beforeTarget, err := s.store.GetTunnelByIDE(target.ID, stored.ID)
	if err != nil {
		t.Fatalf("load target-owned tunnel: %v", err)
	}

	ingressCaps := protocol.DefaultClientCapabilities()
	ingressCaps.IngressTypes = []string{protocol.IngressTypeUDPListen, protocol.IngressTypeSOCKS5Listen}
	_, ingressSession := newTestClientRelayDataSession(t)
	s.clients.Store(ingress.ID, &ClientConn{
		ID:          ingress.ID,
		Info:        protocol.ClientInfo{Capabilities: &ingressCaps},
		dataSession: ingressSession,
		generation:  1,
		state:       clientStateLive,
	})
	_, err = s.auth.adminStore.GetOrCreateClient("install-c2c-update-ingress", protocol.ClientInfo{
		Hostname:     "c2c-update-ingress",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "0.1.0",
		Capabilities: &ingressCaps,
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("update ingress capabilities: %v", err)
	}

	newPort := reserveTCPPort(t)
	update := []byte(`{"expected_revision":` + strconv.FormatInt(stored.Revision, 10) + `,"spec":{
		"name":"c2c-update-unsupported-ingress",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":` + strconv.Itoa(newPort) + `,"allowed_source_cidrs":["0.0.0.0/0","::/0"]}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`)
	resp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+stored.ID, token, update)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unsupported ingress capability update: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	var bodyResp tunnelMutationErrorResponse
	if err := mustDecodeJSON(t, resp.Body, &bodyResp); err != nil {
		t.Fatalf("decode update error: %v", err)
	}
	if bodyResp.ErrorCode != protocol.TunnelMutationErrorCodeCapabilityNotSupported || bodyResp.Field != "ingress.type" {
		t.Fatalf("capability update error mismatch: %+v", bodyResp)
	}

	afterTarget, err := s.store.GetTunnelByIDE(target.ID, stored.ID)
	if err != nil {
		t.Fatalf("reload target-owned tunnel after rejected update: %v", err)
	}
	assertStoredTunnelUnchangedAfterRejectedUpdate(t, beforeTarget, afterTarget)
	if _, err := s.store.GetTunnelByIDE(ingress.ID, stored.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("rejected update must not create ingress-owned config, got err=%v", err)
	}
}

func assertStoredTunnelUnchangedAfterRejectedUpdate(t *testing.T, before, after StoredTunnel) {
	t.Helper()
	if after.Revision != before.Revision ||
		after.LocalIP != before.LocalIP ||
		after.LocalPort != before.LocalPort ||
		after.RemotePort != before.RemotePort ||
		after.Name != before.Name {
		t.Fatalf("rejected update mutated stored tunnel:\n before=%+v\n after=%+v", before, after)
	}
	if !bytes.Equal(after.Ingress.Config, before.Ingress.Config) || !bytes.Equal(after.Target.Config, before.Target.Config) {
		t.Fatalf("rejected update mutated endpoint config:\n before=%+v\n after=%+v", before, after)
	}
}

func TestUnifiedRuntimeReportIgnoresNonServerRelayTransport(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-report-transport-target", "report-transport-target")
	ingress := createUnifiedAPITestClient(t, s, "install-report-transport-ingress", "report-transport-ingress")
	stored := addUnifiedC2CTestTunnel(t, s, "report-transport-c2c", ingress.ID, target.ID, 24011)
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	s.clients.Store(target.ID, &ClientConn{ID: target.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: targetSession})
	s.clients.Store(ingress.ID, &ClientConn{ID: ingress.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: ingressSession})
	s.unifiedRuntime.clearServerIssues(stored.ID)
	s.unifiedRuntime.recordReport(ingress.ID, protocol.TunnelRuntimeReport{
		TunnelID: stored.ID,
		Revision: stored.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "peer-direct failure should not project",
		Transport: protocol.TransportRuntime{
			Actual: protocol.ActualTransportPeerDirect,
		},
	}, time.Now())

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+stored.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	for _, issue := range got.Issues {
		if issue.Code == protocol.TunnelIssueCodeRuntimeReport {
			t.Fatalf("non-server-relay runtime report must not project runtime report issues, got %+v", got.Issues)
		}
	}
}

func TestDecodeServiceEndpointConfigRejectsConflictingHostAndIP(t *testing.T) {
	_, err := decodeServiceEndpointConfig(endpointSpecAPI{
		Type:   tunnelTargetTypeTCPService,
		Config: mustRawJSON(map[string]any{"host": "service.internal", "ip": "127.0.0.1", "port": 8080}),
	})
	if err == nil {
		t.Fatal("expected host/ip conflict to be rejected")
	}
	if !strings.Contains(err.Error(), "host and ip must match") {
		t.Fatalf("unexpected error: %v", err)
	}
}
