package server

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"netsgo/pkg/protocol"
)

func createUnifiedAPITestClient(t *testing.T, s *Server, installID, hostname string) RegisteredClient {
	t.Helper()
	record, err := s.auth.adminStore.GetOrCreateClient(installID, protocol.ClientInfo{
		Hostname: hostname,
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("failed to create client record: %v", err)
	}
	return *record
}

func unifiedCreatePayload(name, clientID string, port int) []byte {
	return []byte(`{
		"name":"` + name + `",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `}},
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
	var ownerList []tunnelSpecAPI
	if err := mustDecodeJSON(t, listResp.Body, &ownerList); err != nil {
		t.Fatalf("failed to decode owner list: %v", err)
	}
	if len(ownerList) != 1 || ownerList[0].ID != created.ID {
		t.Fatalf("owner list mismatch: %+v", ownerList)
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

func TestAPI_UnifiedTunnelRejectsFutureTargetsAndDirectPolicies(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record := createUnifiedAPITestClient(t, s, "install-unified-reject", "unified-reject")

	futureBody := []byte(`{
		"name":"future-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22002}},
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
	if body.ErrorCode != "future_target_type" || body.Field != "target.type" {
		t.Fatalf("future target error mismatch: %+v", body)
	}
	if _, ok := s.store.GetTunnel(record.ID, "future-target"); ok {
		t.Fatal("future target payload must not be persisted")
	}

	directBody := []byte(`{
		"name":"direct-policy",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + record.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":22003}},
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

	clientRelayBody := []byte(`{
		"name":"client-relay",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + record.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":22003}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
		"transport_policy":"server_relay_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, clientRelayBody)
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("client_to_client relay create: want 501 for unavailable runtime slice, got %d body=%s", resp.Code, resp.Body.String())
	}
	body = tunnelMutationErrorResponse{}
	if err := mustDecodeJSON(t, resp.Body, &body); err != nil {
		t.Fatalf("failed to decode client_to_client error: %v", err)
	}
	if body.ErrorCode != "client_to_client_unavailable" || body.Field != "topology" {
		t.Fatalf("client_to_client error mismatch: %+v", body)
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22004}},
		"target":{"location":"client","client_id":"` + record.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":2222}},
		"transport_policy":"server_relay_only"
	}}`)
	staleResp := doMuxRequest(t, handler, http.MethodPut, "/api/tunnels/"+created.ID, token, staleUpdate)
	if staleResp.Code != http.StatusConflict {
		t.Fatalf("stale update: want 409, got %d body=%s", staleResp.Code, staleResp.Body.String())
	}

	validUpdate := []byte(`{"expected_revision":1,"spec":{
		"name":"revise-me",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":22005}},
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
	stored, err := s.store.GetTunnelByIDE(record.ID, created.ID)
	if err != nil {
		t.Fatalf("updated tunnel should remain persisted: %v", err)
	}
	if stored.LocalPort != 2222 || stored.RemotePort != 22005 || stored.IngressBPS != 128 || stored.EgressBPS != 256 {
		t.Fatalf("stored update mismatch: %+v", stored)
	}

	deleteResp := doMuxRequest(t, handler, http.MethodDelete, "/api/tunnels/"+created.ID, token, nil)
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/tunnels/{id}: want 204, got %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	if _, err := s.store.GetTunnelByIDE(record.ID, created.ID); !errors.Is(err, ErrTunnelNotFound) {
		t.Fatalf("deleted tunnel should be hard-deleted, got err=%v", err)
	}
}
