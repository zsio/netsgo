package server

import (
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

func readControlMessageOfType(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
}, wantType string) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		var msg protocol.Message
		if err := conn.ReadJSON(&msg); err != nil {
			continue
		}
		if msg.Type == wantType {
			return msg
		}
	}
	t.Fatalf("did not receive control message %s", wantType)
	return protocol.Message{}
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
		msg := readControlMessageOfType(t, conn, protocol.MsgTypeTunnelProvision)
		var req protocol.TunnelProvisionRequest
		if err := msg.ParsePayload(&req); err != nil {
			t.Fatalf("parse provision payload: %v", err)
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
	}
}

func ackLegacyProxyProvision(t *testing.T, conn interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
	WriteJSON(any) error
}) protocol.ProxyProvisionRequest {
	t.Helper()
	msg := readControlMessageOfType(t, conn, protocol.MsgTypeProxyProvision)
	var req protocol.ProxyProvisionRequest
	if err := msg.ParsePayload(&req); err != nil {
		t.Fatalf("parse legacy proxy provision payload: %v", err)
	}
	ack, err := protocol.NewMessage(protocol.MsgTypeProxyProvisionAck, protocol.ProxyProvisionAck{
		Name:              req.Name,
		ProvisionRevision: req.ProvisionRevision,
		Accepted:          true,
		Message:           "ok",
	})
	if err != nil {
		t.Fatalf("build legacy proxy provision ack: %v", err)
	}
	if err := conn.WriteJSON(ack); err != nil {
		t.Fatalf("write legacy proxy provision ack: %v", err)
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
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d}},
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
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d}},
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
			}
			if projected.Topology != tunnelTopologyServerExpose || projected.Ingress == nil || projected.Target == nil {
				t.Fatalf("/api/clients tunnel should keep unified metadata: %+v", projected)
			}
			if projected.Issues == nil || len(*projected.Issues) != 1 || (*projected.Issues)[0].Code != protocol.TunnelIssueCodeProvisionAckTimeout {
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":%d}},
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
	provisionReq := ackLegacyProxyProvision(t, targetConn)
	if provisionReq.Name != "server-expose-listen-race" || provisionReq.ProvisionRevision == 0 {
		t.Fatalf("legacy provision payload mismatch: %+v", provisionReq)
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
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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
		"ingress":{"location":"client","client_id":"%s","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":%d}},
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

func TestAPI_UnifiedTunnelProjectsRuntimeReportIssuesFromMemory(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-issue-target", "issue-target")
	ingress := createUnifiedAPITestClient(t, s, "install-issue-ingress", "issue-ingress")
	body := []byte(`{
		"name":"issue-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":24001}},
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

	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	s.clients.Store(target.ID, &ClientConn{ID: target.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: targetSession})
	s.clients.Store(ingress.ID, &ClientConn{ID: ingress.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: ingressSession})
	s.unifiedRuntime.recordReport(ingress.ID, protocol.TunnelRuntimeReport{
		TunnelID: created.ID,
		Revision: created.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "ingress listener failed",
	}, time.Date(2026, 5, 24, 1, 0, 0, 0, time.UTC))

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	if len(got.Issues) != 1 {
		t.Fatalf("expected one runtime issue, got %+v", got.Issues)
	}
	if got.Issues[0].Scope != "ingress_client" || got.Issues[0].ClientID != ingress.ID || got.Issues[0].Message != "ingress listener failed" {
		t.Fatalf("unexpected runtime issue: %+v", got.Issues[0])
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
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":24002}},
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
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":25001}},
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
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":25001}},
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `}},
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
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `}},
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
		"ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(port) + `}},
		"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
		"transport_policy":"server_relay_only"
	}`)
	resp = doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, udpBody)
	if resp.Code != http.StatusCreated {
		t.Fatalf("server UDP same-port create: want 201, got %d body=%s", resp.Code, resp.Body.String())
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
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":26001}},
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
			Config:   mustRawJSON(tcpListenConfigAPI{BindIP: "127.0.0.1", Port: 27001}),
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
		body       []byte
		topology   string
		ingress    string
		targetType string
	}{
		{
			name: "server tcp",
			body: []byte(`{
				"name":"shape-server-tcp",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
				"transport_policy":"server_relay_only"
			}`),
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeTCPListen, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "server udp",
			body: []byte(`{
				"name":"shape-server-udp",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"udp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveUDPPort(t)) + `}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
				"transport_policy":"server_relay_only"
			}`),
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeUDPListen, targetType: tunnelTargetTypeUDPService,
		},
		{
			name: "server http",
			body: []byte(`{
				"name":"shape-server-http",
				"topology":"server_expose",
				"ingress":{"location":"server","type":"http_host","config":{"domain":"shape-http.example.com"}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":8080}},
				"transport_policy":"server_relay_only"
			}`),
			topology: tunnelTopologyServerExpose, ingress: tunnelIngressTypeHTTPHost, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "c2c tcp",
			body: []byte(`{
				"name":"shape-c2c-tcp",
				"topology":"client_to_client",
				"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":` + strconv.Itoa(reserveTCPPort(t)) + `}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"tcp_service","config":{"ip":"127.0.0.1","port":22}},
				"transport_policy":"server_relay_only"
			}`),
			topology: tunnelTopologyClientToClient, ingress: tunnelIngressTypeTCPListen, targetType: tunnelTargetTypeTCPService,
		},
		{
			name: "c2c udp",
			body: []byte(`{
				"name":"shape-c2c-udp",
				"topology":"client_to_client",
				"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"udp_listen","config":{"bind_ip":"127.0.0.1","port":` + strconv.Itoa(reserveUDPPort(t)) + `}},
				"target":{"location":"client","client_id":"` + target.ID + `","type":"udp_service","config":{"ip":"127.0.0.1","port":5353}},
				"transport_policy":"server_relay_only"
			}`),
			topology: tunnelTopologyClientToClient, ingress: tunnelIngressTypeUDPListen, targetType: tunnelTargetTypeUDPService,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doMuxRequest(t, handler, http.MethodPost, "/api/tunnels", token, tc.body)
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

	body := []byte(`{
		"name":"server-expose-unsupported-target",
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{"bind_ip":"0.0.0.0","port":` + strconv.Itoa(reserveTCPPort(t)) + `}},
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
}

func TestUnifiedRuntimeReportIgnoresNonServerRelayTransport(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	target := createUnifiedAPITestClient(t, s, "install-report-transport-target", "report-transport-target")
	ingress := createUnifiedAPITestClient(t, s, "install-report-transport-ingress", "report-transport-ingress")
	body := []byte(`{
		"name":"report-transport-c2c",
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":"` + ingress.ID + `","type":"tcp_listen","config":{"bind_ip":"127.0.0.1","port":24011}},
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
	_, targetSession := newTestClientRelayDataSession(t)
	_, ingressSession := newTestClientRelayDataSession(t)
	caps := protocol.DefaultClientCapabilities()
	s.clients.Store(target.ID, &ClientConn{ID: target.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: targetSession})
	s.clients.Store(ingress.ID, &ClientConn{ID: ingress.ID, Info: protocol.ClientInfo{Capabilities: &caps}, state: clientStateLive, dataSession: ingressSession})
	s.unifiedRuntime.recordReport(ingress.ID, protocol.TunnelRuntimeReport{
		TunnelID: created.ID,
		Revision: created.Revision,
		Role:     protocol.DataStreamRoleIngress,
		Message:  "peer-direct failure should not project",
		Transport: protocol.TransportRuntime{
			Actual: protocol.ActualTransportPeerDirect,
		},
	}, time.Now())

	getResp := doMuxRequest(t, handler, http.MethodGet, "/api/tunnels/"+created.ID, token, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET tunnel: want 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	var got tunnelSpecAPI
	if err := mustDecodeJSON(t, getResp.Body, &got); err != nil {
		t.Fatalf("decode tunnel: %v", err)
	}
	if len(got.Issues) != 0 {
		t.Fatalf("non-server-relay runtime report must not project issues, got %+v", got.Issues)
	}
}
