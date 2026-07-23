package server

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"netsgo/pkg/protocol"
)

const securityActivityDedupeWindow = time.Minute

func (s *Server) appendSecurityActivity(r *http.Request, action, reason string, related *AdminSession) int64 {
	s.ensureSharedStoreReferences()
	if s.activityStore == nil {
		return 0
	}
	secret := ""
	if s.auth != nil && s.auth.adminStore != nil {
		if raw, err := s.auth.adminStore.GetJWTSecret(); err == nil {
			secret = string(raw)
		}
	}
	actor := NewActivityActor("unknown", "", "", s.clientIP(r), secret)
	payload := newActivitySecurityPayload(action, reason)
	bucket := time.Now().UTC().UnixNano() / securityActivityDedupeWindow.Nanoseconds()
	dedupeSubject := actor.IPHash
	if related != nil {
		dedupeSubject = related.UserID
	}
	spec := ActivityEventSpec{
		OccurredAt: time.Now().UTC(), Category: ActivityCategorySecurity, Action: action, Source: "server", Actor: actor,
		DedupeKey: fmt.Sprintf("security:%s:%s:%s:%d", action, normalizeActivityReason(action, reason), dedupeSubject, bucket), Payload: payload,
	}
	id, err := s.activityStore.Append(spec)
	if err != nil {
		log.Printf("⚠️ Failed to persist security activity [%s/%s]: %v", action, reason, err)
		return 0
	}
	s.publishActivityID(id)
	return id
}

func (s *Server) recordSessionEnvironmentMismatch(r *http.Request, session *AdminSession) {
	s.appendSecurityActivity(r, "session_environment_mismatch", "environment_mismatch", session)
}

func (s *Server) recordAuthFailure(r *http.Request, action, reason string) {
	s.appendSecurityActivity(r, action, reason, nil)
}

func clientTokenFailureReason(err error) (string, string) {
	switch {
	case errors.Is(err, ErrClientTokenRevoked):
		return protocol.AuthCodeRevokedToken, "revoked_token"
	case errors.Is(err, ErrClientTokenExpired):
		return protocol.AuthCodeExpiredToken, "expired_token"
	case errors.Is(err, ErrClientTokenInstallMismatch):
		return protocol.AuthCodeInstallMismatch, "install_mismatch"
	default:
		return protocol.AuthCodeInvalidToken, "invalid_token"
	}
}

func clientKeyFailureReason(err error) (string, string) {
	switch {
	case errors.Is(err, ErrClientKeyDisabled):
		return protocol.AuthCodeDisabledKey, "disabled_key"
	case errors.Is(err, ErrClientKeyExpired):
		return protocol.AuthCodeExpiredKey, "expired_key"
	case errors.Is(err, ErrClientKeyMaxUsesExceeded):
		return protocol.AuthCodeMaxUsesExceeded, "max_uses_exceeded"
	default:
		return protocol.AuthCodeInvalidKey, "invalid_key"
	}
}
