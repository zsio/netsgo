package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func adminUserTOTPState(t *testing.T, store *AdminStore, userID string) (bool, string) {
	t.Helper()
	var enabled int
	var secret string
	if err := store.db.QueryRow(`SELECT totp_enabled, totp_secret FROM admin_users WHERE id = ?`, userID).Scan(&enabled, &secret); err != nil {
		t.Fatalf("load admin totp state: %v", err)
	}
	return intToBool(enabled), secret
}

func countAdminPasskeys(t *testing.T, store *AdminStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM admin_passkeys`).Scan(&count); err != nil {
		t.Fatalf("count admin passkeys: %v", err)
	}
	return count
}

func countAdminAuthChallenges(t *testing.T, store *AdminStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM admin_auth_challenges`).Scan(&count); err != nil {
		t.Fatalf("count admin auth challenges: %v", err)
	}
	return count
}

func TestAdminStore_TOTPRecoveryCodesAndReset(t *testing.T) {
	store := newInitializedAdminStore(t)
	user, err := store.ValidateAdminPassword("admin", "Admin1234")
	if err != nil {
		t.Fatalf("ValidateAdminPassword failed: %v", err)
	}

	setupToken, _, _, _, err := store.BeginTOTPSetup(*user, "NetsGo")
	if err != nil {
		t.Fatalf("BeginTOTPSetup failed: %v", err)
	}
	challenge, err := store.GetAuthChallenge(setupToken, adminAuthChallengeKindTOTPSetup)
	if err != nil {
		t.Fatalf("GetAuthChallenge failed: %v", err)
	}
	var metadata struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(challenge.SessionJSON), &metadata); err != nil {
		t.Fatalf("decode setup metadata: %v", err)
	}
	code, err := totp.GenerateCode(metadata.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode failed: %v", err)
	}
	if _, err := store.ConfirmTOTPSetup(user.ID, setupToken, "000000"); err == nil {
		t.Fatal("wrong TOTP setup code should be rejected")
	}
	if _, err := store.GetAuthChallenge(setupToken, adminAuthChallengeKindTOTPSetup); err != nil {
		t.Fatalf("wrong TOTP setup code should not consume setup token: %v", err)
	}
	recoveryCodes, err := store.ConfirmTOTPSetup(user.ID, setupToken, code)
	if err != nil {
		t.Fatalf("ConfirmTOTPSetup failed: %v", err)
	}
	if len(recoveryCodes) != adminRecoveryCodeCount {
		t.Fatalf("expected %d recovery codes, got %d", adminRecoveryCodeCount, len(recoveryCodes))
	}
	enabled, secret := adminUserTOTPState(t, store, user.ID)
	if !enabled || secret == "" {
		t.Fatalf("TOTP should be enabled with a secret, enabled=%v secret=%q", enabled, secret)
	}

	refreshed, err := store.GetAdminUserByID(user.ID)
	if err != nil {
		t.Fatalf("GetAdminUserByID failed: %v", err)
	}
	verified, err := store.VerifyAdminSecurityCredentials(refreshed.ID, "Admin1234", recoveryCodes[0])
	if err != nil {
		t.Fatalf("recovery code should verify once: %v", err)
	}
	if !verified.RecoveryCodeUsed {
		t.Fatal("expected recovery code usage to be reported")
	}
	if _, err := store.VerifyAdminSecurityCredentials(refreshed.ID, "Admin1234", recoveryCodes[0]); err == nil {
		t.Fatal("recovery code should be single-use")
	}

	if _, err := store.db.Exec(`INSERT INTO admin_passkeys (id, user_id, name, credential_id, credential_json, rp_id, origin, created_at) VALUES (?, ?, 'key', 'cred', '{}', 'example.com', 'https://example.com', ?)`,
		generateUUID(), user.ID, formatTime(time.Now())); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	if _, err := store.StoreAuthChallenge(user.ID, adminAuthChallengeKindMFA, "{}", nil, time.Minute); err != nil {
		t.Fatalf("StoreAuthChallenge failed: %v", err)
	}
	session := mustCreateSession(t, store, user.ID, user.Username, user.Role, "127.0.0.1", "ua")
	if store.GetSession(session.ID) == nil {
		t.Fatal("expected seeded session")
	}

	if err := store.ResetAdminUser("root", "NewPass123"); err != nil {
		t.Fatalf("ResetAdminUser failed: %v", err)
	}
	newUser, err := store.ValidateAdminPassword("root", "NewPass123")
	if err != nil {
		t.Fatalf("new admin user should validate: %v", err)
	}
	enabled, secret = adminUserTOTPState(t, store, newUser.ID)
	if enabled || secret != "" {
		t.Fatalf("reset admin user should clear TOTP, enabled=%v secret=%q", enabled, secret)
	}
	if countAdminPasskeys(t, store) != 0 {
		t.Fatal("reset admin user should clear passkeys")
	}
	if countAdminAuthChallenges(t, store) != 0 {
		t.Fatal("reset admin user should clear auth challenges")
	}
	if countAdminSessions(t, store) != 0 {
		t.Fatal("reset admin user should clear sessions")
	}
}

func TestAPI_LoginRequiresMFAWhenEnabled(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	user, err := s.auth.adminStore.ValidateAdminPassword("admin", "password123")
	if err != nil {
		t.Fatalf("ValidateAdminPassword failed: %v", err)
	}
	if _, err := s.auth.adminStore.db.Exec(`UPDATE admin_users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, "JBSWY3DPEHPK3PXP", user.ID); err != nil {
		t.Fatalf("enable totp: %v", err)
	}

	body := []byte(`{"username":"admin","password":"password123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAPILogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login with TOTP enabled should return 200 mfa_required, got %d: %s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("mfa_required response should not set a session cookie")
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if payload["mfa_required"] != true || payload["mfa_token"] == "" {
		t.Fatalf("expected mfa_required payload, got %#v", payload)
	}
}

func TestAPI_MFAVerifyRateLimitsAfterTenInvalidCodes(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()
	s.auth.mfaLimiter = newMFAAttemptLimiter(time.Minute, 10, 5*time.Minute)

	user, err := s.auth.adminStore.ValidateAdminPassword("admin", "password123")
	if err != nil {
		t.Fatalf("ValidateAdminPassword failed: %v", err)
	}
	if _, err := s.auth.adminStore.db.Exec(`UPDATE admin_users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, "JBSWY3DPEHPK3PXP", user.ID); err != nil {
		t.Fatalf("enable totp: %v", err)
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"password123"}`)))
	loginReq.RemoteAddr = "203.0.113.10:1000"
	loginResp := httptest.NewRecorder()
	s.handleAPILogin(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login should begin MFA challenge, got %d: %s", loginResp.Code, loginResp.Body.String())
	}
	var loginBody struct {
		MFAToken string `json:"mfa_token"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode login body: %v", err)
	}
	if loginBody.MFAToken == "" {
		t.Fatal("mfa_token should be present")
	}

	body := []byte(`{"mfa_token":"` + loginBody.MFAToken + `","code":"000000"}`)
	for i := 1; i <= 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1000"
		w := httptest.NewRecorder()
		s.handleAPIMFAVerify(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d body=%s", i, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.10:1000"
	w := httptest.NewRecorder()
	s.handleAPIMFAVerify(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 11: want 429, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("rate limited MFA response should include Retry-After")
	}
	var payload apiErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if payload.Code != "mfa_attempts_exceeded" {
		t.Fatalf("want mfa_attempts_exceeded, got %#v", payload)
	}
}

func TestAPI_MFAVerifyRateLimitSurvivesChallengeRotation(t *testing.T) {
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()
	s.auth.mfaLimiter = newMFAAttemptLimiter(time.Minute, 10, 5*time.Minute)

	user, err := s.auth.adminStore.ValidateAdminPassword("admin", "password123")
	if err != nil {
		t.Fatalf("ValidateAdminPassword failed: %v", err)
	}
	if _, err := s.auth.adminStore.db.Exec(`UPDATE admin_users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, "JBSWY3DPEHPK3PXP", user.ID); err != nil {
		t.Fatalf("enable totp: %v", err)
	}

	loginFromIP := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"password123"}`)))
		req.RemoteAddr = "203.0.113.10:1000"
		w := httptest.NewRecorder()
		s.handleAPILogin(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("login should begin MFA challenge, got %d: %s", w.Code, w.Body.String())
		}
		var body struct {
			MFAToken string `json:"mfa_token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode login body: %v", err)
		}
		if body.MFAToken == "" {
			t.Fatal("mfa_token should be present")
		}
		return body.MFAToken
	}

	firstToken := loginFromIP()
	for i := 1; i <= 10; i++ {
		body := []byte(`{"mfa_token":"` + firstToken + `","code":"000000"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1000"
		w := httptest.NewRecorder()
		s.handleAPIMFAVerify(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d body=%s", i, w.Code, w.Body.String())
		}
	}

	secondToken := loginFromIP()
	if secondToken == firstToken {
		t.Fatal("rotated MFA challenge should issue a new token")
	}
	body := []byte(`{"mfa_token":"` + secondToken + `","code":"000000"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.10:1000"
	w := httptest.NewRecorder()
	s.handleAPIMFAVerify(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("rotated challenge attempt: want 429, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAPI_AdminSecurityResponse(t *testing.T) {
	_, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	resp := doMuxRequest(t, handler, http.MethodGet, "/api/admin/security", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/security: want 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var payload adminSecurityResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode security response: %v", err)
	}
	if payload.User.Username != "admin" {
		t.Fatalf("security user: want admin, got %q", payload.User.Username)
	}
	if payload.TOTPEnabled {
		t.Fatal("TOTP should be disabled by default")
	}
	if payload.Passkeys == nil {
		t.Fatal("passkeys should be an empty array, not null")
	}
}

func TestAPI_PasskeyBeginRejectsHTTPNonLocalhost(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "http://example.com")
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/begin", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	s.handleAPIPasskeyLoginBegin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("passkey begin on insecure origin should be rejected with 400, got %d", w.Code)
	}
}

func TestAPI_PasskeyBeginRequiresRegisteredCredential(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "http://localhost")
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/begin", nil)
	req.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	s.handleAPIPasskeyLoginBegin(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("passkey begin without credentials should return 404, got %d: %s", w.Code, w.Body.String())
	}
	var payload apiErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Code != "passkey_not_registered" {
		t.Fatalf("expected passkey_not_registered, got %#v", payload)
	}
}

func TestAPI_PasskeyBeginRejectsOriginMismatch(t *testing.T) {
	t.Setenv("NETSGO_SERVER_ADDR", "https://admin.example.com")
	s, cleanup := setupTestServerWithDB(t, true)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/begin", nil)
	req.Header.Set("Origin", "https://other.example.com")
	w := httptest.NewRecorder()
	s.handleAPIPasskeyLoginBegin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("passkey begin with mismatched origin should return 400, got %d: %s", w.Code, w.Body.String())
	}
	var payload apiErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Code != "passkey_unavailable" {
		t.Fatalf("expected passkey_unavailable, got %#v", payload)
	}
}
