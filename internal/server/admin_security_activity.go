package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func (s *AdminStore) UpdateAdminUsernameWithActivity(userID, username string, actor ActivityActor) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, fmt.Errorf("admin username is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var before string
	if err := tx.QueryRow(`SELECT username FROM admin_users WHERE id = ?`, userID).Scan(&before); err != nil {
		return 0, err
	}
	if before == username {
		if err := commitTx(tx, &committed); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if _, err := tx.Exec(`UPDATE admin_users SET username = ? WHERE id = ?`, username, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec("username_changed", actor, ActivitySummaryArgs{}))
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
}

func (s *AdminStore) UpdateAdminPasswordWithActivity(userID, currentPassword, newPassword string, actor ActivityActor) (int64, error) {
	if err := validatePassword(newPassword); err != nil {
		return 0, fmt.Errorf("password does not meet requirements: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return 0, fmt.Errorf("failed to hash password: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	user, err := scanAdminUser(tx.QueryRow(`SELECT `+adminUserSelectColumns()+` FROM admin_users WHERE id = ?`, userID))
	if err != nil {
		return 0, err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(newPassword)) == nil {
		return 0, fmt.Errorf("new password must be different from the current password")
	}
	if currentPassword != "" && bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)) != nil {
		return 0, errCurrentPassword
	}
	if _, err := tx.Exec(`UPDATE admin_users SET password_hash = ? WHERE id = ?`, string(hash), userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec("password_changed", actor, ActivitySummaryArgs{}))
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
}

func (s *AdminStore) ConfirmTOTPSetupWithActivity(userID, challengeID, code string, actor ActivityActor) ([]string, int64, error) {
	challenge, err := s.GetAuthChallenge(challengeID, adminAuthChallengeKindTOTPSetup)
	if err != nil {
		return nil, 0, err
	}
	if challenge.UserID != userID {
		return nil, 0, sql.ErrNoRows
	}
	var metadata struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(challenge.SessionJSON), &metadata); err != nil {
		return nil, 0, err
	}
	if metadata.Secret == "" || !totp.Validate(normalizeMFACode(code), metadata.Secret) {
		return nil, 0, errMFAInvalid
	}
	codes, err := generateRecoveryCodes(adminRecoveryCodeCount)
	if err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE id = ? AND user_id = ? AND kind = ? AND expires_at > ?`, challengeID, userID, adminAuthChallengeKindTOTPSetup, formatTime(time.Now()))
	if err != nil {
		return nil, 0, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		if err != nil {
			return nil, 0, err
		}
		return nil, 0, sql.ErrNoRows
	}
	if _, err := tx.Exec(`UPDATE admin_users SET totp_enabled = 1, totp_secret = ? WHERE id = ?`, metadata.Secret, userID); err != nil {
		return nil, 0, err
	}
	if err := replaceRecoveryCodesInTx(tx, userID, codes); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec("totp_enabled", actor, ActivitySummaryArgs{}))
	if err != nil {
		return nil, 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, 0, err
	}
	return codes, activityID, nil
}

func (s *AdminStore) DisableTOTPWithActivity(userID string, actor ActivityActor) (int64, error) {
	return s.securitySimpleActivity(userID, "totp_disabled", actor, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE admin_users SET totp_enabled = 0, totp_secret = '' WHERE id = ?`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM admin_totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return err
		}
		return nil
	}, true)
}

func (s *AdminStore) RegenerateRecoveryCodesWithActivity(userID string, actor ActivityActor) ([]string, int64, error) {
	codes, err := generateRecoveryCodes(adminRecoveryCodeCount)
	if err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := replaceRecoveryCodesInTx(tx, userID, codes); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec("recovery_codes_regenerated", actor, ActivitySummaryArgs{Count: len(codes)}))
	if err != nil {
		return nil, 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, 0, err
	}
	return codes, activityID, nil
}

func (s *AdminStore) AddPasskeyWithActivity(userID, name, credentialID string, credential webauthn.Credential, rpID, origin string, actor ActivityActor) (*AdminPasskey, int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return nil, 0, err
	}
	passkeyID, err := generateUUIDE()
	if err != nil {
		return nil, 0, err
	}
	passkey := AdminPasskey{ID: passkeyID, UserID: userID, Name: name, CredentialID: credentialID, CredentialJSON: string(raw), RPID: rpID, Origin: origin, CreatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := tx.Exec(`INSERT INTO admin_passkeys (id, user_id, name, credential_id, credential_json, rp_id, origin, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`, passkey.ID, passkey.UserID, passkey.Name, passkey.CredentialID, passkey.CredentialJSON, passkey.RPID, passkey.Origin, formatTime(passkey.CreatedAt)); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
		return nil, 0, err
	}
	if err := s.maybeFailSave(); err != nil {
		return nil, 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec("passkey_added", actor, ActivitySummaryArgs{ResourceName: passkey.Name}))
	if err != nil {
		return nil, 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return nil, 0, err
	}
	return &passkey, activityID, nil
}

func (s *AdminStore) UpdatePasskeyNameWithActivity(userID, passkeyID, name string, actor ActivityActor) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("passkey name is required")
	}
	return s.securitySimpleActivity(userID, "passkey_renamed", actor, func(tx *sql.Tx) error {
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
		return nil
	}, false)
}

func (s *AdminStore) DeletePasskeyWithActivity(userID, passkeyID string, actor ActivityActor) (int64, error) {
	return s.securitySimpleActivity(userID, "passkey_deleted", actor, func(tx *sql.Tx) error {
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
		return nil
	}, true)
}

func (s *AdminStore) securitySimpleActivity(userID, action string, actor ActivityActor, mutate func(*sql.Tx) error, revokeSessions bool) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := mutate(tx); err != nil {
		return 0, err
	}
	if revokeSessions {
		if _, err := tx.Exec(`DELETE FROM admin_sessions WHERE user_id = ?`, userID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM admin_auth_challenges WHERE user_id = ?`, userID); err != nil {
			return 0, err
		}
	}
	if err := s.maybeFailSave(); err != nil {
		return 0, err
	}
	activityID, err := s.appendActivityTx(tx, adminActivitySpec(action, actor, ActivitySummaryArgs{}))
	if err != nil {
		return 0, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return activityID, nil
}
