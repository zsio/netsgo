package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

func TestAdminLoginFailureActivityUsesAllowlistedPayload(t *testing.T) {
	s, handler, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	baseline, err := s.activityStore.MaxID()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"username":"private-admin","password":"secret-password"}`)
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/auth/login", "", body)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d body=%s", resp.Code, resp.Body.String())
	}
	page, err := s.activityStore.Query(ActivityQuery{AfterID: baseline, Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("login failure activity = %+v, %v", page.Items, err)
	}
	item := page.Items[0]
	if item.Action != "admin_login_failed" || !bytes.Contains(item.Payload, []byte(`"reason_code":"bad_credentials"`)) {
		t.Fatalf("login failure item = %+v payload=%s", item, item.Payload)
	}
	payload := string(item.Payload)
	if strings.Contains(payload, "private-admin") || strings.Contains(payload, "secret-password") {
		t.Fatalf("credentials leaked into activity payload: %s", payload)
	}
}

func TestMalformedAdminLoginDoesNotCreateSecurityActivity(t *testing.T) {
	s, handler, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()
	baseline, err := s.activityStore.MaxID()
	if err != nil {
		t.Fatal(err)
	}
	resp := doMuxRequest(t, handler, http.MethodPost, "/api/auth/login", "", []byte(`{"username":`))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("malformed login status = %d", resp.Code)
	}
	maxID, err := s.activityStore.MaxID()
	if err != nil || maxID != baseline {
		t.Fatalf("malformed login changed activity max ID from %d to %d, err=%v", baseline, maxID, err)
	}
}

func TestClientAuthFailureReasonMappings(t *testing.T) {
	tests := []struct {
		err        error
		wantCode   string
		wantReason string
		token      bool
	}{
		{ErrClientTokenInvalid, protocol.AuthCodeInvalidToken, "invalid_token", true},
		{ErrClientTokenRevoked, protocol.AuthCodeRevokedToken, "revoked_token", true},
		{ErrClientTokenExpired, protocol.AuthCodeExpiredToken, "expired_token", true},
		{ErrClientTokenInstallMismatch, protocol.AuthCodeInstallMismatch, "install_mismatch", true},
		{ErrClientKeyInvalid, protocol.AuthCodeInvalidKey, "invalid_key", false},
		{ErrClientKeyDisabled, protocol.AuthCodeDisabledKey, "disabled_key", false},
		{ErrClientKeyExpired, protocol.AuthCodeExpiredKey, "expired_key", false},
		{ErrClientKeyMaxUsesExceeded, protocol.AuthCodeMaxUsesExceeded, "max_uses_exceeded", false},
	}
	for _, tt := range tests {
		var code, reason string
		if tt.token {
			code, reason = clientTokenFailureReason(tt.err)
		} else {
			code, reason = clientKeyFailureReason(tt.err)
		}
		if code != tt.wantCode || reason != tt.wantReason {
			t.Fatalf("mapping %v = %q/%q, want %q/%q", tt.err, code, reason, tt.wantCode, tt.wantReason)
		}
	}
}

func TestClientInvalidKeyProducesSanitizedSecurityActivity(t *testing.T) {
	s, ts, cleanup := setupWSTestNoConn(t)
	defer cleanup()
	s.ensureSharedStoreReferences()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial control websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	req, _ := protocol.NewMessage(protocol.MsgTypeAuth, protocol.AuthRequest{InstallID: "secret-install", Key: "secret-invalid-key", Client: protocol.ClientInfo{Hostname: "self-claimed-host"}})
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write auth request: %v", err)
	}
	var response protocol.Message
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	var auth protocol.AuthResponse
	if err := response.ParsePayload(&auth); err != nil || auth.Code != protocol.AuthCodeInvalidKey {
		t.Fatalf("auth response = %+v, parseErr=%v", auth, err)
	}
	page, err := s.activityStore.Query(ActivityQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("client auth activity = %+v, %v", page.Items, err)
	}
	raw, _ := json.Marshal(page.Items[0])
	for _, secret := range []string{"secret-install", "secret-invalid-key", "self-claimed-host"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("client auth activity leaked %q: %s", secret, raw)
		}
	}
}
