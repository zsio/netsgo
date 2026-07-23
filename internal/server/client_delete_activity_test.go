package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"netsgo/pkg/protocol"
)

func TestClientOfflineCompletesBeforeDeleteActivity(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	record, err := s.auth.adminStore.GetOrCreateClient("offline-before-delete", protocol.ClientInfo{Hostname: "ordered-client"}, "192.0.2.50:1234")
	if err != nil {
		t.Fatalf("GetOrCreateClient() error = %v", err)
	}
	client := &ClientConn{ID: record.ID, InstallID: record.InstallID, Info: record.Info, generation: 1, state: clientStateLive, proxies: make(map[string]*ProxyTunnel)}
	s.clients.Store(client.ID, client)

	invalidationLocked := make(chan struct{})
	continueInvalidation := make(chan struct{})
	client.lifecycleMu.Lock()
	go func() {
		close(invalidationLocked)
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "normal_closure")
		close(continueInvalidation)
	}()
	<-invalidationLocked

	deleteDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/clients/"+record.ID, nil)
		req.Host = defaultTestRequestHost()
		req.Header.Set("Authorization", "Bearer "+token)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		deleteDone <- resp.Code
	}()
	select {
	case status := <-deleteDone:
		t.Fatalf("delete crossed active invalidation barrier with status %d", status)
	default:
	}
	client.lifecycleMu.Unlock()
	<-continueInvalidation
	if status := <-deleteDone; status != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", status)
	}

	page, err := s.activityStore.Query(ActivityQuery{Scope: ActivityScopeClient, ScopeID: record.ID, Limit: 20})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(page.Items) != 2 || page.Items[1].Action != "offline" || page.Items[0].Action != "deleted" {
		t.Fatalf("client lifecycle activities = %+v", page.Items)
	}
	if page.Items[1].ID >= page.Items[0].ID {
		t.Fatalf("offline ID %d must precede deleted ID %d", page.Items[1].ID, page.Items[0].ID)
	}
}
