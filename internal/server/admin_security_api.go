package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type authSuccessPayload struct {
	Token string `json:"token"`
	User  struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
}

func newAuthSuccessPayload(token string, user AdminUser) authSuccessPayload {
	var payload authSuccessPayload
	payload.Token = token
	payload.User.ID = user.ID
	payload.User.Username = user.Username
	payload.User.Role = user.Role
	return payload
}

func (s *Server) createAdminLoginSession(w http.ResponseWriter, r *http.Request, user AdminUser) {
	session, err := s.auth.adminStore.CreateSession(user.ID, user.Username, user.Role, r.RemoteAddr, r.UserAgent())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_persist_failed", "failed to persist session")
		return
	}
	token, err := s.GenerateAdminToken(session)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "token_generate_failed", "failed to generate token")
		return
	}
	s.setSessionCookie(w, r, token, int(sessionDefaultTTL.Seconds()))
	encodeJSON(w, http.StatusOK, newAuthSuccessPayload(token, user))
}

func (s *Server) handleAPIMFAVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	challenge, err := s.auth.adminStore.GetAuthChallenge(req.MFAToken, adminAuthChallengeKindMFA)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_mfa_token", "invalid or expired mfa token")
		return
	}
	user, err := s.auth.adminStore.GetAdminUserByID(challenge.UserID)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_mfa_token", "invalid or expired mfa token")
		return
	}
	ok, err := s.verifyLoginMFA(user, req.Code)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "mfa_verify_failed", "failed to verify mfa code")
		return
	}
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid mfa code")
		return
	}
	if _, err := s.auth.adminStore.ConsumeAuthChallenge(req.MFAToken, user.ID, adminAuthChallengeKindMFA); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_mfa_token", "invalid or expired mfa token")
		return
	}
	s.createAdminLoginSession(w, r, user)
}

func (s *Server) verifyLoginMFA(user AdminUser, code string) (bool, error) {
	s.auth.adminStore.mu.Lock()
	defer s.auth.adminStore.mu.Unlock()

	tx, err := s.auth.adminStore.db.Begin()
	if err != nil {
		return false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	ok, _, err := verifyMFAInTx(tx, user, code)
	if err != nil || !ok {
		return false, err
	}
	if err := s.auth.adminStore.maybeFailSave(); err != nil {
		return false, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) handleAPIPasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	ctx, err := s.webAuthnContextForRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	passkeys, err := s.auth.adminStore.ListPasskeysByRP(ctx.RPID, ctx.Origin)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	if len(passkeys) == 0 {
		writeAPIError(w, http.StatusNotFound, "passkey_not_registered", "no passkey is registered for this server address")
		return
	}
	user, err := s.auth.adminStore.GetAdminUserByID(passkeys[0].UserID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "admin_user_load_failed", "failed to load admin user")
		return
	}
	waUser, err := webAuthnUserFromPasskeys(user, passkeys)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	wa, err := newWebAuthn(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	assertion, session, err := wa.BeginLogin(waUser)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_begin_failed", err.Error())
		return
	}
	sessionJSON, err := marshalWebAuthnSession(session)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_begin_failed", "failed to persist passkey challenge")
		return
	}
	challenge, err := s.auth.adminStore.StoreAuthChallenge(user.ID, adminAuthChallengeKindPasskeyLogin, sessionJSON, ctx, webAuthnChallengeTTL(session))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_begin_failed", "failed to persist passkey challenge")
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": challenge.ID,
		"public_key":   assertion,
	})
}

func (s *Server) handleAPIPasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	challenge, err := s.auth.adminStore.GetAuthChallenge(req.ChallengeID, adminAuthChallengeKindPasskeyLogin)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_passkey_challenge", "invalid or expired passkey challenge")
		return
	}
	var ctx webAuthnContext
	if err := json.Unmarshal([]byte(challenge.MetadataJSON), &ctx); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_login_failed", "failed to load passkey challenge")
		return
	}
	if requestCtx, err := s.webAuthnContextForRequest(r); err != nil || requestCtx != ctx {
		writeAPIError(w, http.StatusBadRequest, "passkey_origin_mismatch", "passkey origin does not match configured server address")
		return
	}
	user, err := s.auth.adminStore.GetAdminUserByID(challenge.UserID)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_passkey_challenge", "invalid or expired passkey challenge")
		return
	}
	passkeys, err := s.auth.adminStore.ListPasskeysByRP(ctx.RPID, ctx.Origin)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	waUser, err := webAuthnUserFromPasskeys(user, passkeys)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	session, err := unmarshalWebAuthnSession(challenge.SessionJSON)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_login_failed", "failed to load passkey challenge")
		return
	}
	wa, err := newWebAuthn(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	credentialRequest, err := webAuthnRequestFromJSON(r, req.Credential)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_passkey_response", err.Error())
		return
	}
	credential, err := wa.FinishLogin(waUser, session, credentialRequest)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "passkey_login_failed", err.Error())
		return
	}
	if _, err := s.auth.adminStore.ConsumeAuthChallenge(req.ChallengeID, challenge.UserID, adminAuthChallengeKindPasskeyLogin); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_passkey_challenge", "invalid or expired passkey challenge")
		return
	}
	if err := s.auth.adminStore.TouchPasskey(user.ID, credentialIDString(credential.ID), *credential); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_update_failed", "failed to update passkey")
		return
	}
	s.createAdminLoginSession(w, r, user)
}

func (s *Server) handleAPIAdminSecurity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	passkeys, err := s.auth.adminStore.ListPasskeys(user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	recoveryCount, err := s.auth.adminStore.CountUnusedRecoveryCodes(user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "recovery_codes_load_failed", "failed to load recovery codes")
		return
	}
	webAuthn := adminSecurityWebAuthnResponse{}
	if ctx, err := s.webAuthnContextForRequest(r); err == nil {
		webAuthn.RPID = ctx.RPID
		webAuthn.Origin = ctx.Origin
	}
	encodeJSON(w, http.StatusOK, adminSecurityResponse{
		User: adminSecurityUserResponse{
			ID:        user.ID,
			Username:  user.Username,
			Role:      user.Role,
			CreatedAt: user.CreatedAt,
			LastLogin: user.LastLogin,
		},
		TOTPEnabled:            user.TOTPEnabled,
		RecoveryCodesRemaining: recoveryCount,
		Passkeys:               sanitizePasskeys(passkeys),
		WebAuthn:               webAuthn,
	})
}

func (s *Server) handleAPIAdminSecurityUsername(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	if err := s.auth.adminStore.UpdateAdminUsername(user.ID, req.NewUsername); err != nil {
		writeAPIError(w, http.StatusBadRequest, "username_update_failed", err.Error())
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true})
}

func (s *Server) handleAPIAdminSecurityPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	if err := s.auth.adminStore.UpdateAdminPassword(user.ID, req.CurrentPassword, req.NewPassword); err != nil {
		writeAPIError(w, http.StatusBadRequest, "password_update_failed", err.Error())
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true})
}

func (s *Server) handleAPIAdminSecurityTOTPBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	challengeID, secret, otpURL, qrDataURL, err := s.auth.adminStore.BeginTOTPSetup(user, "NetsGo")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "totp_begin_failed", "failed to begin totp setup")
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{
		"setup_token": challengeID,
		"secret":      secret,
		"otpauth_url": otpURL,
		"qr_data_url": qrDataURL,
	})
}

func (s *Server) handleAPIAdminSecurityTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		SetupToken string `json:"setup_token"`
		Code       string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	codes, err := s.auth.adminStore.ConfirmTOTPSetup(user.ID, req.SetupToken, req.Code)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "totp_confirm_failed", err.Error())
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"requires_relogin": true,
		"recovery_codes":   codes,
	})
}

func (s *Server) handleAPIAdminSecurityTOTPDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.Body != http.NoBody {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	if err := s.auth.adminStore.DisableTOTP(user.ID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "totp_disable_failed", "failed to disable totp")
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true})
}

func (s *Server) handleAPIAdminSecurityRecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	codes, err := s.auth.adminStore.RegenerateRecoveryCodes(user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "recovery_codes_regenerate_failed", "failed to regenerate recovery codes")
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true, "recovery_codes": codes})
}

func (s *Server) handleAPIAdminSecurityPasskeys(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		passkeys, err := s.auth.adminStore.ListPasskeys(user.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
			return
		}
		encodeJSON(w, http.StatusOK, sanitizePasskeys(passkeys))
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleAPIAdminSecurityPasskeyItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	passkeyID := r.PathValue("id")
	switch r.Method {
	case http.MethodPut:
		var req struct {
			CurrentPassword string `json:"current_password"`
			MFACode         string `json:"mfa_code"`
			Name            string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
			return
		}
		if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
			writeCredentialVerificationError(w, err)
			return
		}
		if err := s.auth.adminStore.UpdatePasskeyName(user.ID, passkeyID, req.Name); err != nil {
			writeAPIError(w, http.StatusBadRequest, "passkey_update_failed", err.Error())
			return
		}
		encodeJSON(w, http.StatusOK, map[string]any{"success": true})
	case http.MethodDelete:
		var req struct {
			CurrentPassword string `json:"current_password"`
			MFACode         string `json:"mfa_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
			return
		}
		if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
			writeCredentialVerificationError(w, err)
			return
		}
		if err := s.auth.adminStore.DeletePasskey(user.ID, passkeyID); err != nil {
			writeAPIError(w, http.StatusBadRequest, "passkey_delete_failed", err.Error())
			return
		}
		s.clearSessionCookie(w, r)
		encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Server) handleAPIAdminSecurityPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		MFACode         string `json:"mfa_code"`
		Name            string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := s.auth.adminStore.VerifyAdminSecurityCredentials(user.ID, req.CurrentPassword, req.MFACode); err != nil {
		writeCredentialVerificationError(w, err)
		return
	}
	ctx, err := s.webAuthnContextForRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	passkeys, err := s.auth.adminStore.ListPasskeys(user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	waUser, err := webAuthnUserFromPasskeys(user, passkeys)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	wa, err := newWebAuthn(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	creation, session, err := wa.BeginRegistration(waUser)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_begin_failed", err.Error())
		return
	}
	sessionJSON, err := marshalWebAuthnSession(session)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_begin_failed", "failed to persist passkey challenge")
		return
	}
	metadata := passkeyRegisterMetadata{Name: strings.TrimSpace(req.Name), RPID: ctx.RPID, Origin: ctx.Origin}
	challenge, err := s.auth.adminStore.StoreAuthChallenge(user.ID, adminAuthChallengeKindPasskeyRegister, sessionJSON, metadata, webAuthnChallengeTTL(session))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_begin_failed", "failed to persist passkey challenge")
		return
	}
	encodeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": challenge.ID,
		"public_key":   creation,
	})
}

func (s *Server) handleAPIAdminSecurityPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	user, ok := s.currentAdminUser(w, r)
	if !ok {
		return
	}
	var req struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	challenge, err := s.auth.adminStore.GetAuthChallenge(req.ChallengeID, adminAuthChallengeKindPasskeyRegister)
	if err != nil || challenge.UserID != user.ID {
		writeAPIError(w, http.StatusUnauthorized, "invalid_passkey_challenge", "invalid or expired passkey challenge")
		return
	}
	var metadata passkeyRegisterMetadata
	if err := json.Unmarshal([]byte(challenge.MetadataJSON), &metadata); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_register_failed", "failed to load passkey challenge")
		return
	}
	ctx := webAuthnContext{RPID: metadata.RPID, Origin: metadata.Origin}
	if requestCtx, err := s.webAuthnContextForRequest(r); err != nil || requestCtx != ctx {
		writeAPIError(w, http.StatusBadRequest, "passkey_origin_mismatch", "passkey origin does not match configured server address")
		return
	}
	passkeys, err := s.auth.adminStore.ListPasskeys(user.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	waUser, err := webAuthnUserFromPasskeys(user, passkeys)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_load_failed", "failed to load passkeys")
		return
	}
	session, err := unmarshalWebAuthnSession(challenge.SessionJSON)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_register_failed", "failed to load passkey challenge")
		return
	}
	wa, err := newWebAuthn(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "passkey_unavailable", err.Error())
		return
	}
	credentialRequest, err := webAuthnRequestFromJSON(r, req.Credential)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_passkey_response", err.Error())
		return
	}
	credential, err := wa.FinishRegistration(waUser, session, credentialRequest)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "passkey_register_failed", err.Error())
		return
	}
	if _, err := s.auth.adminStore.ConsumeAuthChallenge(req.ChallengeID, user.ID, adminAuthChallengeKindPasskeyRegister); err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid_passkey_challenge", "invalid or expired passkey challenge")
		return
	}
	passkey, err := s.auth.adminStore.AddPasskey(user.ID, metadata.Name, credentialIDString(credential.ID), *credential, metadata.RPID, metadata.Origin)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "passkey_register_failed", "failed to save passkey")
		return
	}
	s.clearSessionCookie(w, r)
	encodeJSON(w, http.StatusOK, map[string]any{"success": true, "requires_relogin": true, "passkey": sanitizePasskey(*passkey)})
}

func (s *Server) currentAdminUser(w http.ResponseWriter, r *http.Request) (AdminUser, bool) {
	info := GetSessionFromContext(r.Context())
	if info == nil {
		writeAPIError(w, http.StatusUnauthorized, "session_not_found", "session not found")
		return AdminUser{}, false
	}
	user, err := s.auth.adminStore.GetAdminUserByID(info.UserID)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "admin_user_not_found", "admin user not found")
		return AdminUser{}, false
	}
	return user, true
}

func writeCredentialVerificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCurrentPassword):
		writeAPIError(w, http.StatusUnauthorized, "current_password_incorrect", "current password is incorrect")
	case errors.Is(err, errMFAInvalid), errors.Is(err, errMFARequired):
		writeAPIError(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid mfa code")
	default:
		writeAPIError(w, http.StatusBadRequest, "security_verification_failed", err.Error())
	}
}

func sanitizePasskeys(passkeys []AdminPasskey) []adminPasskeyResponse {
	if len(passkeys) == 0 {
		return []adminPasskeyResponse{}
	}
	result := make([]adminPasskeyResponse, 0, len(passkeys))
	for _, passkey := range passkeys {
		result = append(result, sanitizePasskey(passkey))
	}
	return result
}

func sanitizePasskey(passkey AdminPasskey) adminPasskeyResponse {
	return adminPasskeyResponse{
		ID:         passkey.ID,
		Name:       passkey.Name,
		RPID:       passkey.RPID,
		Origin:     passkey.Origin,
		CreatedAt:  passkey.CreatedAt,
		LastUsedAt: passkey.LastUsedAt,
	}
}

func (s *Server) maybeBeginMFALogin(w http.ResponseWriter, r *http.Request, user *AdminUser) bool {
	if user == nil || !user.TOTPEnabled {
		return false
	}
	challenge, err := s.auth.adminStore.StoreAuthChallenge(user.ID, adminAuthChallengeKindMFA, "{}", nil, adminAuthChallengeDefaultTTL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "mfa_challenge_failed", "failed to create mfa challenge")
		return true
	}
	encodeJSON(w, http.StatusOK, map[string]any{
		"mfa_required": true,
		"mfa_token":    challenge.ID,
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"role":     user.Role,
		},
	})
	return true
}
