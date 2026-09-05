package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"netsgo/pkg/protocol"
)

func httpDomainAPIRequest(name, clientID, domain string) tunnelCreateRequestAPI {
	return tunnelCreateRequestAPI{
		Name: name, Topology: tunnelTopologyServerExpose,
		Ingress: endpointSpecAPI{
			Location: tunnelEndpointLocationServer, Type: tunnelIngressTypeHTTPHost,
			Config: mustRawJSON(httpHostConfigAPI{Domain: domain, AllowedSourceCIDRs: allowAllSourceCIDRs()}),
		},
		Target: endpointSpecAPI{
			Location: tunnelEndpointLocationClient, ClientID: clientID, Type: tunnelTargetTypeTCPService,
			Config: mustRawJSON(serviceConfigAPI{IP: "127.0.0.1", Port: 3000}),
		},
		TransportPolicy: tunnelTransportPolicyServerRelayOnly,
	}
}

func TestHTTPDomainAPIValidationAndEdit(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	target := createUnifiedAPITestClient(t, s, "install-wildcard", "wildcard")
	for i, domain := range []string{"APP.X.COM.", "*.x.com", "*.*.x.com", "*.*.*.x.com", "*.a.b.com"} {
		name := fmt.Sprintf("valid-%d", i)
		req := httpDomainAPIRequest(name, target.ID, domain)
		resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, mustRawJSON(req))
		if resp.Code != http.StatusCreated {
			t.Fatalf("create %q: %d %s", domain, resp.Code, resp.Body.String())
		}
		var created tunnelSpecAPI
		if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		stored, err := s.store.GetTunnelByID(created.ID)
		if err != nil || tunnelIngressDomain(stored) != strings.TrimSuffix(strings.ToLower(domain), ".") {
			t.Fatalf("canonical domain not stored: %+v err=%v", stored, err)
		}
		// Updating the same rule excludes itself from global duplicate checks.
		resp = doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token,
			mustRawJSON(tunnelUpdateRequestAPI{ExpectedRevision: created.Revision, Spec: req}))
		if resp.Code != http.StatusOK {
			t.Fatalf("self update %q: %d %s", domain, resp.Code, resp.Body.String())
		}
		before, err := s.store.GetTunnelByID(created.ID)
		if err != nil {
			t.Fatal(err)
		}
		req.Ingress.Config = mustRawJSON(httpHostConfigAPI{Domain: "*.*.*.*.x.com"})
		resp = doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token,
			mustRawJSON(tunnelUpdateRequestAPI{ExpectedRevision: before.Revision, Spec: req}))
		if resp.Code != http.StatusBadRequest || !responseHasTunnelErrorCode(t, resp, protocol.TunnelMutationErrorCodeDomainInvalid) {
			t.Fatalf("invalid update %q: %d %s", domain, resp.Code, resp.Body.String())
		}
		after, err := s.store.GetTunnelByID(created.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertStoredTunnelUnchangedAfterRejectedUpdate(t, before, after)
	}
	before, err := s.store.GetAllTunnels()
	if err != nil {
		t.Fatal(err)
	}
	for i, domain := range []string{"*.*.*.*.x.com", "app.*.x.com", "*.app.*.x.com", "foo*.x.com", "*.com", "*.x.com:80", "*.x.com/path", "*.x..com"} {
		resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token,
			mustRawJSON(httpDomainAPIRequest(fmt.Sprintf("invalid-%d", i), target.ID, domain)))
		var body tunnelMutationErrorResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if resp.Code != http.StatusBadRequest || body.Field != "domain" || body.Code != protocol.TunnelMutationErrorCodeDomainInvalid {
			t.Errorf("invalid %q: %d %+v", domain, resp.Code, body)
		}
	}
	after, err := s.store.GetAllTunnels()
	if err != nil || len(before) != len(after) {
		t.Fatalf("invalid creates changed storage: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

func TestHTTPDomainAPICrossUserConflictAndRelease(t *testing.T) {
	s, handler, tokenA, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	targetA := createUnifiedAPITestClient(t, s, "install-owner-a", "owner-a")
	ownerB, err := s.auth.adminStore.CreateUser("wildcard-owner-b", "Password123")
	if err != nil {
		t.Fatal(err)
	}
	targetB := createUnifiedAPITestClientForUser(t, s, ownerB.ID, "install-owner-b", "owner-b")
	tokenB := loginAdminTokenLocal(t, handler, "wildcard-owner-b", "Password123")
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", tokenA,
		mustRawJSON(httpDomainAPIRequest("private-tunnel-name", targetA.ID, "*.*.x.com")))
	if resp.Code != http.StatusCreated {
		t.Fatalf("owner A create: %d %s", resp.Code, resp.Body.String())
	}
	var created tunnelSpecAPI
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"*.*.X.COM.", "*.dev.x.com", "app.dev.x.com"} {
		resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", tokenB,
			mustRawJSON(httpDomainAPIRequest("owner-b-tunnel", targetB.ID, domain)))
		var body tunnelMutationErrorResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if resp.Code != http.StatusConflict || body.Field != "domain" || body.Code != protocol.TunnelMutationErrorCodeIngressResourceConflict {
			t.Fatalf("cross-user %q: %d %+v", domain, resp.Code, body)
		}
		for _, secret := range []string{"private-tunnel-name", targetA.ID, created.ID} {
			if strings.Contains(resp.Body.String(), secret) {
				t.Fatalf("conflict exposed other user's resource: %s", resp.Body.String())
			}
		}
	}
	resp = doMuxRequest(t, handler, http.MethodDelete, "/api/tunnels/"+created.ID, tokenA, nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", resp.Code, resp.Body.String())
	}
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", tokenB,
		mustRawJSON(httpDomainAPIRequest("owner-b-tunnel", targetB.ID, "*.dev.x.com")))
	if resp.Code != http.StatusCreated {
		t.Fatalf("claim after delete: %d %s", resp.Code, resp.Body.String())
	}
}
