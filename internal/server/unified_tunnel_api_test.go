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
	capabilities := protocol.DefaultClientCapabilities()
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

	source := createUnifiedAPITestClient(t, s, "install-unified-source", "unified-source")
	ingress := createUnifiedAPITestClient(t, s, "install-unified-ingress", "unified-ingress")
	clientRelayBody := []byte(`{
		"name":"client-relay-direct",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":22003}},
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
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":23001}},
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
		"ingress":{"location":"client","client_id":"` + record.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":23002}},
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
