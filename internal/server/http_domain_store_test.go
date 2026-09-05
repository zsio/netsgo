package server

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"netsgo/pkg/protocol"
)

func httpDomainTestTunnel(id, owner, domain string) StoredTunnel {
	clientID := "client-" + id
	return StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			ID: id, Name: id, Type: protocol.ProxyTypeHTTP,
			LocalIP: "127.0.0.1", LocalPort: 3000,
			Domain: "legacy-" + id + ".invalid",
		},
		ClientID: clientID, OwnerClientID: clientID, OwnerUserID: owner,
		Topology: TunnelTopologyServerExpose, Revision: 1,
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		TransportPolicy: protocol.TransportPolicyServerRelayOnly,
		Ingress: EndpointSpec{
			Location: protocol.EndpointLocationServer, Type: TunnelIngressTypeHTTPHost,
			Config: mustRawJSON(httpHostConfigAPI{Domain: domain, AllowedSourceCIDRs: allowAllSourceCIDRs(), Auth: protocol.HTTPAuthConfig{Type: protocol.HTTPAuthTypeNone}}),
		},
		Target: EndpointSpec{
			Location: protocol.EndpointLocationClient, ClientID: clientID, Type: TunnelTargetTypeTCPService,
			Config: mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 3000}),
		},
	}
}

func assertHTTPDomainClaim(t *testing.T, store *TunnelStore, host, wantID string) {
	t.Helper()
	claim, ok, err := store.findHTTPDomainClaim(host)
	if err != nil {
		t.Fatalf("claim %q: %v", host, err)
	}
	if ok != (wantID != "") || claim.ID != wantID {
		t.Fatalf("claim %q = %q, found=%v; want %q", host, claim.ID, ok, wantID)
	}
}

func TestHTTPDomainStoreOwnershipAndDuplicateRules(t *testing.T) {
	cases := []struct {
		name, left, right string
		sameOwner, reject bool
	}{
		{"duplicate exact same owner", "app.x.com", "APP.X.COM.", true, true},
		{"duplicate exact cross owner", "app.x.com", "app.x.com", false, true},
		{"duplicate wildcard same owner", "*.x.com", "*.X.COM.", true, true},
		{"duplicate wildcard cross owner", "*.*.*.x.com", "*.*.*.x.com", false, true},
		{"exact override across clients of same owner", "*.x.com", "app.x.com", true, false},
		{"specific wildcard override", "*.*.x.com", "*.dev.x.com", true, false},
		{"cross owner exact covered", "*.x.com", "app.x.com", false, true},
		{"cross owner wildcard covers exact", "app.x.com", "*.x.com", false, true},
		{"cross owner wildcard covered", "*.*.x.com", "*.dev.x.com", false, true},
		{"cross owner wildcard covers wildcard", "*.dev.x.com", "*.*.x.com", false, true},
		{"three level cross owner overlap", "*.*.*.x.com", "a.b.c.x.com", false, true},
		{"different depth is disjoint", "*.x.com", "*.*.x.com", false, false},
		{"apex is separate", "*.x.com", "x.com", false, false},
		{"different suffix", "*.dev.x.com", "*.prod.x.com", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestTunnelStore(t)
			left := httpDomainTestTunnel("left", "alice", tc.left)
			owner := "bob"
			if tc.sameOwner {
				owner = "alice"
			}
			right := httpDomainTestTunnel("right", owner, tc.right)
			mustAddStableTunnel(t, store, left)
			mustPrepareTestTunnelOwnership(t, store, right)
			_, conflict, err := store.findIngressResourceConflict(right, "")
			if err != nil || conflict != tc.reject {
				t.Fatalf("preflight: conflict=%v err=%v, want reject=%v", conflict, err, tc.reject)
			}
			err = store.AddTunnel(right)
			if (err != nil) != tc.reject {
				t.Fatalf("transaction: err=%v, want reject=%v", err, tc.reject)
			}
			if err != nil {
				status, body := tunnelMutationErrorStatusAndBody(err)
				if status != http.StatusConflict || body.Field != "domain" || body.Code != protocol.TunnelMutationErrorCodeIngressResourceConflict {
					t.Fatalf("conflict must be a structured 409 domain error: %d %+v", status, body)
				}
				if strings.Contains(body.Message, "alice") || strings.Contains(body.Message, "left") {
					t.Fatalf("error disclosed another owner's resource: %+v", body)
				}
			}
			wantCount := 2
			if tc.reject {
				wantCount = 1
			}
			for _, table := range []string{"tunnels", "tunnel_resource_locks"} {
				var count int
				if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != wantCount {
					t.Fatalf("%s count=%d want=%d err=%v", table, count, wantCount, err)
				}
			}
		})
	}
}

func TestHTTPDomainStorePriorityPersistsAcrossStatesAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	store := newTestTunnelStoreAt(t, path)
	for _, item := range []struct{ id, domain string }{
		{"broad", "*.*.*.x.com"}, {"middle", "*.*.eu.x.com"},
		{"specific", "*.dev.eu.x.com"}, {"exact", "shop.dev.eu.x.com"},
	} {
		mustAddStableTunnel(t, store, httpDomainTestTunnel(item.id, "alice", item.domain))
	}
	for _, state := range []struct{ desired, runtime, message string }{
		{protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStatePending, ""},
		{protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateOffline, ""},
		{protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""},
		{protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "failed"},
	} {
		if err := store.UpdateStates("client-exact", "exact", state.desired, state.runtime, state.message); err != nil {
			t.Fatal(err)
		}
		assertHTTPDomainClaim(t, store, "SHOP.DEV.EU.X.COM.:8443", "exact")
	}
	assertHTTPDomainClaim(t, store, "other.dev.eu.x.com", "specific")
	assertHTTPDomainClaim(t, store, "other.prod.eu.x.com", "middle")
	assertHTTPDomainClaim(t, store, "other.prod.us.x.com", "broad")
	assertHTTPDomainClaim(t, store, "x.com", "")
	assertHTTPDomainClaim(t, store, "a.b.x.com", "")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newTestTunnelStoreAt(t, path)
	assertHTTPDomainClaim(t, store, "shop.dev.eu.x.com", "exact")
	other := httpDomainTestTunnel("other-user", "bob", "shop.dev.eu.x.com")
	mustPrepareTestTunnelOwnership(t, store, other)
	if err := store.AddTunnel(other); err == nil {
		t.Fatal("unavailable claim was released on restart")
	}
	for _, id := range []string{"exact", "specific", "middle", "broad"} {
		if err := store.RemoveTunnelByID("client-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	assertHTTPDomainClaim(t, store, "shop.dev.eu.x.com", "")
	if err := store.AddTunnel(other); err != nil {
		t.Fatalf("deleted rules should release ownership: %v", err)
	}
}

func TestHTTPDomainStoreMutationRollbackAndRelease(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, httpDomainTestTunnel("alice", "alice", "*.x.com"))
	mustAddStableTunnel(t, store, httpDomainTestTunnel("bob", "bob", "old.y.com"))
	current, err := store.GetTunnelByID("bob")
	if err != nil {
		t.Fatal(err)
	}
	replacement := current
	replacement.Revision++
	replacement.Ingress.Config = mustRawJSON(httpHostConfigAPI{Domain: "shop.x.com", AllowedSourceCIDRs: allowAllSourceCIDRs()})
	if err := store.ReplaceTunnelByID(current.ClientID, current.ID, current.Revision, replacement); err == nil {
		t.Fatal("cross-owner update should fail")
	}
	assertHTTPDomainClaim(t, store, "old.y.com", "bob")
	assertHTTPDomainClaim(t, store, "shop.x.com", "alice")
	after, err := store.GetTunnelByID(current.ID)
	if err != nil || after.Revision != current.Revision || tunnelIngressDomain(after) != "old.y.com" {
		t.Fatalf("failed update changed stored configuration: %+v err=%v", after, err)
	}
	replacement.Ingress.Config = mustRawJSON(httpHostConfigAPI{Domain: "new.y.com", AllowedSourceCIDRs: allowAllSourceCIDRs()})
	if err := store.ReplaceTunnelByID(current.ClientID, current.ID, current.Revision, replacement); err != nil {
		t.Fatal(err)
	}
	assertHTTPDomainClaim(t, store, "old.y.com", "")
	assertHTTPDomainClaim(t, store, "new.y.com", "bob")
	// A failed persistence operation must also retain the previous claim.
	store.failSaveErr = fmt.Errorf("injected write failure")
	store.failSaveCount = 1
	if err := store.RemoveTunnelByID(current.ClientID, current.ID); err == nil {
		t.Fatal("expected injected failure")
	}
	assertHTTPDomainClaim(t, store, "new.y.com", "bob")
}

func TestHTTPDomainStoreConcurrentClaims(t *testing.T) {
	for _, domains := range [][2]string{{"*.x.com", "shop.x.com"}, {"*.*.x.com", "*.dev.x.com"}, {"app.x.com", "APP.X.COM."}} {
		t.Run(domains[0]+"/"+domains[1], func(t *testing.T) {
			store := newTestTunnelStore(t)
			candidates := []StoredTunnel{
				httpDomainTestTunnel("alice", "alice", domains[0]),
				httpDomainTestTunnel("bob", "bob", domains[1]),
			}
			for _, candidate := range candidates {
				mustPrepareTestTunnelOwnership(t, store, candidate)
			}
			start := make(chan struct{})
			results := make(chan error, len(candidates))
			var wg sync.WaitGroup
			for _, candidate := range candidates {
				wg.Add(1)
				go func(candidate StoredTunnel) {
					defer wg.Done()
					<-start
					results <- store.AddTunnel(candidate)
				}(candidate)
			}
			close(start)
			wg.Wait()
			close(results)
			successes, conflicts := 0, 0
			for err := range results {
				if err == nil {
					successes++
				} else if status, _ := tunnelMutationErrorStatusAndBody(err); status == http.StatusConflict {
					conflicts++
				} else {
					t.Fatalf("unexpected concurrent result: %v", err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("concurrent results: successes=%d conflicts=%d", successes, conflicts)
			}
			all, err := store.GetAllTunnels()
			if err != nil || len(all) != 1 {
				t.Fatalf("losing transaction left a tunnel: len=%d err=%v", len(all), err)
			}
		})
	}
}

func TestHTTPDomainStoreLegacyTerminalDotAndInvalidWildcard(t *testing.T) {
	store := newTestTunnelStore(t)
	mustAddStableTunnel(t, store, httpDomainTestTunnel("old", "alice", "SHOP.X.COM."))
	if _, err := store.db.Exec(`UPDATE tunnel_resource_locks SET resource_key = ? WHERE tunnel_id = ?`, "ingress:server:http_host:shop.x.com.", "old"); err != nil {
		t.Fatal(err)
	}
	assertHTTPDomainClaim(t, store, "shop.x.com", "old")
	assertHTTPDomainClaim(t, store, "SHOP.X.COM.:8080", "old")
	for i, domain := range []string{"*.*.*.*.x.com", "shop.*.x.com", "*.com"} {
		candidate := httpDomainTestTunnel(fmt.Sprintf("invalid-%d", i), "alice", domain)
		mustPrepareTestTunnelOwnership(t, store, candidate)
		if err := store.AddTunnel(candidate); err == nil {
			t.Errorf("storage accepted invalid wildcard %q", domain)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.findHTTPDomainClaim("shop.x.com"); err == nil {
		t.Fatal("storage failure must not become a route miss")
	}
}
