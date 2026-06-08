package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	adminAuthChallengeKindMFA             = "mfa_login"
	adminAuthChallengeKindTOTPSetup       = "totp_setup"
	adminAuthChallengeKindPasskeyRegister = "passkey_register"
	adminAuthChallengeKindPasskeyLogin    = "passkey_login"
	adminAuthChallengeDefaultTTL          = 5 * time.Minute
	adminRecoveryCodeCount                = 10
	adminRecoveryCodeRandomBytes          = 10
	adminRecoveryCodePrefix               = "ng"
)

var (
	errMFARequired      = errors.New("mfa verification required")
	errMFAInvalid       = errors.New("invalid mfa code")
	errCurrentPassword  = errors.New("current password is incorrect")
	errPasskeyNotFound  = errors.New("passkey not found")
	errPasskeyRPInvalid = errors.New("passkey relying party is unavailable")
)

type adminSecurityCredentialVerification struct {
	User             AdminUser
	RecoveryCodeUsed bool
}

func (s *AdminStore) GetAdminUserByID(userID string) (AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, err := scanAdminUser(s.db.QueryRow(`SELECT `+adminUserSelectColumns()+` FROM admin_users WHERE id = ?`, userID))
	if err != nil {
		return AdminUser{}, err
	}
	return user, nil
}

func (s *AdminStore) GetSingleAdminUser() (AdminUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, err := scanAdminUser(s.db.QueryRow(`SELECT ` + adminUserSelectColumns() + ` FROM admin_users ORDER BY created_at, id LIMIT 1`))
	if err != nil {
		return AdminUser{}, err
	}
	return user, nil
}

func (s *AdminStore) VerifyAdminSecurityCredentials(userID, currentPassword, mfaCode string) (adminSecurityCredentialVerification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return adminSecurityCredentialVerification{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	user, err := scanAdminUser(tx.QueryRow(`SELECT `+adminUserSelectColumns()+` FROM admin_users WHERE id = ?`, userID))
	if err != nil {
		if err == sql.ErrNoRows {
			_ = bcrypt.CompareHashAndPassword(s.getDummyHash(), []byte(currentPassword))
			return adminSecurityCredentialVerification{}, errCurrentPassword
		}
		return adminSecurityCredentialVerification{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
		return adminSecurityCredentialVerification{}, errCurrentPassword
	}

	result := adminSecurityCredentialVerification{User: user}
	if user.TOTPEnabled {
		ok, recoveryUsed, err := verifyMFAInTx(tx, user, mfaCode)
		if err != nil {
			return adminSecurityCredentialVerification{}, err
		}
		if !ok {
			return adminSecurityCredentialVerification{}, errMFAInvalid
		}
		result.RecoveryCodeUsed = recoveryUsed
	}
	if err := s.maybeFailSave(); err != nil {
		return adminSecurityCredentialVerification{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return adminSecurityCredentialVerification{}, err
	}
	return result, nil
}

func verifyMFAInTx(tx *sql.Tx, user AdminUser, code string) (ok bool, recoveryUsed bool, err error) {
	code = normalizeMFACode(code)
	if code == "" {
		return false, false, nil
	}
	if user.TOTPSecret != "" && totp.Validate(code, user.TOTPSecret) {
		return true, false, nil
	}
	if used, err := consumeRecoveryCodeInTx(tx, user.ID, code); err != nil {
		return false, false, err
	} else if used {
		return true, true, nil
	}
	return false, false, nil
}

func normalizeMFACode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
}

func (s *AdminStore) UpdateAdminUsername(userID, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("admin username is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE admin_users SET username = ? WHERE id = ?`, username, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) UpdateAdminPassword(userID, currentPassword, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return fmt.Errorf("password does not meet requirements: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	user, err := scanAdminUser(tx.QueryRow(`SELECT `+adminUserSelectColumns()+` FROM admin_users WHERE id = ?`, userID))
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(newPassword)); err == nil {
		return fmt.Errorf("new password must be different from the current password")
	}
	if currentPassword != "" {
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
			return errCurrentPassword
		}
	}
	if _, err := tx.Exec(`UPDATE admin_users SET password_hash = ? WHERE id = ?`, string(hash), userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) BeginTOTPSetup(user AdminUser, issuer string) (challengeID, secret, otpURL, qrDataURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: user.Username,
	})
	if err != nil {
		return "", "", "", "", err
	}

	img, err := key.Image(192, 192)
	if err != nil {
		return "", "", "", "", err
	}
	qrDataURL, err = encodePNGDataURL(img)
	if err != nil {
		return "", "", "", "", err
	}

	raw, err := json.Marshal(map[string]string{
		"secret": key.Secret(),
		"url":    key.URL(),
	})
	if err != nil {
		return "", "", "", "", err
	}
	challenge, err := s.StoreAuthChallenge(user.ID, adminAuthChallengeKindTOTPSetup, string(raw), nil, adminAuthChallengeDefaultTTL)
	if err != nil {
		return "", "", "", "", err
	}
	return challenge.ID, key.Secret(), key.URL(), qrDataURL, nil
}

func encodePNGDataURL(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func (s *AdminStore) ConfirmTOTPSetup(userID, challengeID, code string) ([]string, error) {
	challenge, err := s.GetAuthChallenge(challengeID, adminAuthChallengeKindTOTPSetup)
	if err != nil {
		return nil, err
	}
	if challenge.UserID != userID {
		return nil, sql.ErrNoRows
	}
	var metadata struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(challenge.SessionJSON), &metadata); err != nil {
		return nil, err
	}
	if metadata.Secret == "" || !totp.Validate(normalizeMFACode(code), metadata.Secret) {
		return nil, errMFAInvalid
	}
	if _, err := s.ConsumeAuthChallenge(challengeID, userID, adminAuthChallengeKindTOTPSetup); err != nil {
		return nil, err
	}
	codes, err := generateRecoveryCodes(adminRecoveryCodeCount)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`UPDATE admin_users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, metadata.Secret, userID); err != nil {
		return nil, err
	}
	if err := replaceRecoveryCodesInTx(tx, userID, codes); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *AdminStore) DisableTOTP(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`UPDATE admin_users SET totp_enabled = 0, totp_secret = '' WHERE id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) RegenerateRecoveryCodes(userID string) ([]string, error) {
	codes, err := generateRecoveryCodes(adminRecoveryCodeCount)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if err := replaceRecoveryCodesInTx(tx, userID, codes); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *AdminStore) CountUnusedRecoveryCodes(userID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_totp_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func generateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := range codes {
		raw := make([]byte, adminRecoveryCodeRandomBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		encoded := strings.ToLower(base64.RawStdEncoding.EncodeToString(raw))
		encoded = strings.NewReplacer("+", "a", "/", "b").Replace(encoded)
		codes[i] = adminRecoveryCodePrefix + "-" + encoded[:4] + "-" + encoded[4:8] + "-" + encoded[8:12]
	}
	return codes, nil
}

func replaceRecoveryCodesInTx(tx *sql.Tx, userID string, codes []string) error {
	if _, err := tx.Exec(`DELETE FROM admin_totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	now := formatTime(time.Now())
	for _, code := range codes {
		hash, err := bcrypt.GenerateFromPassword([]byte(normalizeMFACode(code)), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO admin_totp_recovery_codes (id, user_id, code_hash, created_at, used_at) VALUES (?, ?, ?, ?, NULL)`,
			generateUUID(), userID, string(hash), now); err != nil {
			return err
		}
	}
	return nil
}

func consumeRecoveryCodeInTx(tx *sql.Tx, userID, code string) (bool, error) {
	rows, err := tx.Query(`SELECT id, code_hash FROM admin_totp_recovery_codes WHERE user_id = ? AND used_at IS NULL ORDER BY created_at, id`, userID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	var matchedID string
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return false, err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil {
			matchedID = id
			break
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if matchedID == "" {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE admin_totp_recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`, formatTime(time.Now()), matchedID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *AdminStore) StoreAuthChallenge(userID, kind, sessionJSON string, metadata any, ttl time.Duration) (AdminAuthChallenge, error) {
	if ttl <= 0 {
		ttl = adminAuthChallengeDefaultTTL
	}
	metadataJSON := "{}"
	if metadata != nil {
		raw, err := json.Marshal(metadata)
		if err != nil {
			return AdminAuthChallenge{}, err
		}
		metadataJSON = string(raw)
	}
	now := time.Now()
	challenge := AdminAuthChallenge{
		ID:           generateUUID(),
		UserID:       userID,
		Kind:         kind,
		SessionJSON:  sessionJSON,
		MetadataJSON: metadataJSON,
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ? AND kind = ?`, userID, kind); err != nil {
		return AdminAuthChallenge{}, err
	}
	if _, err := tx.Exec(`INSERT INTO admin_auth_challenges (id, user_id, kind, session_json, metadata_json, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		challenge.ID, challenge.UserID, challenge.Kind, challenge.SessionJSON, challenge.MetadataJSON, formatTime(challenge.CreatedAt), formatTime(challenge.ExpiresAt)); err != nil {
		return AdminAuthChallenge{}, err
	}
	if err := s.maybeFailSave(); err != nil {
		return AdminAuthChallenge{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return AdminAuthChallenge{}, err
	}
	return challenge, nil
}

func (s *AdminStore) ConsumeAuthChallenge(id, userID, kind string) (AdminAuthChallenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	var row *sql.Row
	if userID == "" {
		row = tx.QueryRow(`SELECT id, user_id, kind, session_json, metadata_json, created_at, expires_at FROM admin_auth_challenges WHERE id = ? AND kind = ?`, id, kind)
	} else {
		row = tx.QueryRow(`SELECT id, user_id, kind, session_json, metadata_json, created_at, expires_at FROM admin_auth_challenges WHERE id = ? AND user_id = ? AND kind = ?`, id, userID, kind)
	}
	challenge, err := scanAdminAuthChallenge(row)
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	if time.Now().After(challenge.ExpiresAt) {
		_, _ = tx.Exec(`DELETE FROM admin_auth_challenges WHERE id = ?`, id)
		_ = commitTx(tx, &committed)
		return AdminAuthChallenge{}, fmt.Errorf("challenge expired")
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE id = ?`, id); err != nil {
		return AdminAuthChallenge{}, err
	}
	if err := s.maybeFailSave(); err != nil {
		return AdminAuthChallenge{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return AdminAuthChallenge{}, err
	}
	return challenge, nil
}

func (s *AdminStore) GetAuthChallenge(id, kind string) (AdminAuthChallenge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	challenge, err := scanAdminAuthChallenge(s.db.QueryRow(`SELECT id, user_id, kind, session_json, metadata_json, created_at, expires_at FROM admin_auth_challenges WHERE id = ? AND kind = ?`, id, kind))
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	if time.Now().After(challenge.ExpiresAt) {
		return AdminAuthChallenge{}, fmt.Errorf("challenge expired")
	}
	return challenge, nil
}

func scanAdminAuthChallenge(row dbScanner) (AdminAuthChallenge, error) {
	var challenge AdminAuthChallenge
	var createdAt, expiresAt string
	if err := row.Scan(&challenge.ID, &challenge.UserID, &challenge.Kind, &challenge.SessionJSON, &challenge.MetadataJSON, &createdAt, &expiresAt); err != nil {
		return AdminAuthChallenge{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	parsedExpiresAt, err := parseTime(expiresAt)
	if err != nil {
		return AdminAuthChallenge{}, err
	}
	challenge.CreatedAt = parsedCreatedAt
	challenge.ExpiresAt = parsedExpiresAt
	return challenge, nil
}

func (s *AdminStore) AddPasskey(userID, name, credentialID string, credential webauthn.Credential, rpID, origin string) (*AdminPasskey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	passkey := AdminPasskey{
		ID:             generateUUID(),
		UserID:         userID,
		Name:           name,
		CredentialID:   credentialID,
		CredentialJSON: string(raw),
		RPID:           rpID,
		Origin:         origin,
		CreatedAt:      now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	if _, err := tx.Exec(`INSERT INTO admin_passkeys (id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		passkey.ID, passkey.UserID, passkey.Name, passkey.CredentialID, passkey.CredentialJSON, passkey.RPID, passkey.Origin, formatTime(passkey.CreatedAt)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, err
	}
	return &passkey, nil
}

func (s *AdminStore) ListPasskeys(userID string) ([]AdminPasskey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at
		FROM admin_passkeys WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var passkeys []AdminPasskey
	for rows.Next() {
		passkey, err := scanAdminPasskey(rows)
		if err != nil {
			return nil, err
		}
		passkeys = append(passkeys, passkey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return passkeys, nil
}

func (s *AdminStore) ListPasskeysByRP(rpID, origin string) ([]AdminPasskey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at
		FROM admin_passkeys WHERE rp_id = ? AND origin = ? ORDER BY created_at, id`, rpID, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var passkeys []AdminPasskey
	for rows.Next() {
		passkey, err := scanAdminPasskey(rows)
		if err != nil {
			return nil, err
		}
		passkeys = append(passkeys, passkey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return passkeys, nil
}

func (s *AdminStore) UpdatePasskeyName(userID, passkeyID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("passkey name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE admin_passkeys SET name = ? WHERE id = ? AND user_id = ?`, name, passkeyID, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errPasskeyNotFound
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) DeletePasskey(userID, passkeyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`DELETE FROM admin_passkeys WHERE id = ? AND user_id = ?`, passkeyID, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errPasskeyNotFound
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func (s *AdminStore) TouchPasskey(userID, credentialID string, credential webauthn.Credential) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	result, err := tx.Exec(`UPDATE admin_passkeys SET credential_json = ?, last_used_at = ? WHERE user_id = ? AND credential_id = ?`,
		string(raw), formatTime(time.Now()), userID, credentialID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errPasskeyNotFound
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}
	return commitTx(tx, &committed)
}

func scanAdminPasskey(row dbScanner) (AdminPasskey, error) {
	var passkey AdminPasskey
	var createdAt string
	var lastUsedAt sql.NullString
	if err := row.Scan(&passkey.ID, &passkey.UserID, &passkey.Name, &passkey.CredentialID, &passkey.CredentialJSON, &passkey.RPID, &passkey.Origin, &createdAt, &lastUsedAt); err != nil {
		return AdminPasskey{}, err
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return AdminPasskey{}, err
	}
	parsedLastUsedAt, err := parseOptionalTime(lastUsedAt)
	if err != nil {
		return AdminPasskey{}, err
	}
	passkey.CreatedAt = parsedCreatedAt
	passkey.LastUsedAt = parsedLastUsedAt
	return passkey, nil
}

func (p AdminPasskey) WebAuthnCredential() (webauthn.Credential, error) {
	var credential webauthn.Credential
	if err := json.Unmarshal([]byte(p.CredentialJSON), &credential); err != nil {
		return webauthn.Credential{}, err
	}
	return credential, nil
}

func credentialIDString(id []byte) string {
	return base64.RawURLEncoding.EncodeToString(id)
}

func constantTimeStringEqual(a, b string) bool {
	sumA := sha256.Sum256([]byte(a))
	sumB := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(sumA[:], sumB[:]) == 1
}
