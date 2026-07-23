package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setupActivityAPIAuthTest(t *testing.T) (*Server, http.Handler, func()) {
	t.Helper()
	s := New(0)
	path := filepath.Join(t.TempDir(), serverDBFileName)
	db, err := openServerDB(path)
	if err != nil {
		t.Fatal(err)
	}
	s.serverDB = db
	s.activityStore = newActivityStoreWithDB(path, db, false)
	store, err := newAdminStoreWithDB(path, db, false)
	if err != nil {
		t.Fatal(err)
	}
	s.auth.adminStore = store
	if err := store.Initialize("admin", "password123", "https://example.com", []PortRange{}); err != nil {
		t.Fatal(err)
	}
	return s, s.newHTTPHandler(), func() { _ = db.Close() }
}

func issueRoleToken(t *testing.T, s *Server, role string) string {
	t.Helper()
	session := mustCreateSession(t, s.auth.adminStore, "user-"+role, role, role, "127.0.0.1", "Go-http-client/1.1")
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestActivityAPIRequiresAdminButViewerKeepsSSE(t *testing.T) {
	s, handler, cleanup := setupActivityAPIAuthTest(t)
	defer cleanup()
	viewer := issueRoleToken(t, s, "viewer")

	activityReq := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	activityReq.Header.Set("Authorization", "Bearer "+viewer)
	activityReq.Header.Set("User-Agent", "Go-http-client/1.1")
	activityResp := httptest.NewRecorder()
	handler.ServeHTTP(activityResp, activityReq)
	if activityResp.Code != http.StatusForbidden {
		t.Fatalf("viewer activity status = %d, body=%s", activityResp.Code, activityResp.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventsReq := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	eventsReq.Header.Set("Authorization", "Bearer "+viewer)
	eventsReq.Header.Set("User-Agent", "Go-http-client/1.1")
	eventsResp := newLockedRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(eventsResp, eventsReq)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(eventsResp.BodyString(), `event: ready`) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if body := eventsResp.BodyString(); !strings.Contains(body, `event: ready`) || !strings.Contains(body, `"activity_cursor":0`) {
		t.Fatalf("viewer SSE handshake = %q", body)
	}
	if id, err := s.activityStore.Append(testActivitySpec("created", time.Now())); err != nil {
		t.Fatal(err)
	} else {
		s.publishActivityID(id)
	}
	time.Sleep(25 * time.Millisecond)
	if body := eventsResp.BodyString(); strings.Contains(body, `event: activity_event`) {
		t.Fatalf("viewer SSE leaked activity event: %q", body)
	}
	s.events.PublishJSON("client_online", map[string]any{"client_id": "viewer-visible"})
	deadline = time.Now().Add(time.Second)
	for !strings.Contains(eventsResp.BodyString(), `viewer-visible`) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if body := eventsResp.BodyString(); !strings.Contains(body, `event: client_online`) {
		t.Fatalf("viewer SSE missed console event: %q", body)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("viewer SSE did not stop")
	}
}

func TestActivityAPIAdminCanRead(t *testing.T) {
	s, handler, cleanup := setupActivityAPIAuthTest(t)
	defer cleanup()
	if _, err := s.activityStore.Append(testActivitySpec("created", time.Now())); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	req.Header.Set("Authorization", "Bearer "+issueRoleToken(t, s, "admin"))
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestParseActivityQueryFiltersAndDefaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/activity?scope=client&client_id=client-1&severity=debug&severity=error&category=p2p&category=tunnel&limit=17&from=2026-07-01T00%3A00%3A00Z&to=2026-07-02T00%3A00%3A00Z", nil)
	query, err := parseActivityQuery(req)
	if err != nil {
		t.Fatal(err)
	}
	if query.Scope != ActivityScopeClient || query.ScopeID != "client-1" || query.Limit != 17 || len(query.Severities) != 2 || len(query.Categories) != 2 || query.From == nil || query.To == nil {
		t.Fatalf("parsed query = %+v", query)
	}
	defaults, err := parseActivityQuery(httptest.NewRequest(http.MethodGet, "/api/activity", nil))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Scope != ActivityScopeGlobal || defaults.Limit != 50 || len(defaults.Severities) != 3 {
		t.Fatalf("default query = %+v", defaults)
	}
}

func TestParseActivityQueryRejectsInvalidCombinations(t *testing.T) {
	paths := []string{
		"/api/activity?scope=client",
		"/api/activity?scope=tunnel&tunnel_id=t1&client_id=c1",
		"/api/activity?before=1&after=2",
		"/api/activity?limit=201",
		"/api/activity?from=bad",
		"/api/activity?from=2026-07-02T00%3A00%3A00Z&to=2026-07-01T00%3A00%3A00Z",
	}
	for _, path := range paths {
		if _, err := parseActivityQuery(httptest.NewRequest(http.MethodGet, path, nil)); err == nil {
			t.Fatalf("invalid query accepted: %s", path)
		}
	}
}

func TestActivityAPICursorDirections(t *testing.T) {
	s, handler, cleanup := setupActivityAPIAuthTest(t)
	defer cleanup()
	var ids []int64
	for _, action := range []string{"created", "updated", "stopped", "resumed"} {
		id, err := s.activityStore.Append(testActivitySpec(action, time.Now()))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	token := issueRoleToken(t, s, "admin")
	requestPage := func(path string) ActivityPage {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "Go-http-client/1.1")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, resp.Code, resp.Body.String())
		}
		var page ActivityPage
		if err := mustDecodeJSON(t, resp.Body, &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	before := requestPage("/api/activity?limit=2")
	if got := activityIDs(before.Items); !reflect.DeepEqual(got, []int64{ids[3], ids[2]}) || before.Direction != ActivityDirectionBefore || before.NextCursor != ids[2] || !before.HasMore {
		t.Fatalf("before page = %+v", before)
	}
	after := requestPage(fmt.Sprintf("/api/activity?after=%d&limit=2", ids[0]))
	if got := activityIDs(after.Items); !reflect.DeepEqual(got, []int64{ids[2], ids[1]}) || after.Direction != ActivityDirectionAfter || after.NextCursor != ids[2] || !after.HasMore {
		t.Fatalf("after page = %+v", after)
	}
}
