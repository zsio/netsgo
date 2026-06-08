package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type adminWebAuthnUser struct {
	user        AdminUser
	credentials []webauthn.Credential
}

func (u adminWebAuthnUser) WebAuthnID() []byte {
	return []byte(u.user.ID)
}

func (u adminWebAuthnUser) WebAuthnName() string {
	return u.user.Username
}

func (u adminWebAuthnUser) WebAuthnDisplayName() string {
	return u.user.Username
}

func (u adminWebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

type webAuthnContext struct {
	RPID   string `json:"rp_id"`
	Origin string `json:"origin"`
}

type passkeyRegisterMetadata struct {
	Name   string `json:"name"`
	RPID   string `json:"rp_id"`
	Origin string `json:"origin"`
}

func (s *Server) webAuthnContextForRequest(r *http.Request) (webAuthnContext, error) {
	if s.auth.adminStore == nil {
		return webAuthnContext{}, fmt.Errorf("admin store unavailable")
	}
	config, err := s.auth.adminStore.GetServerConfigE()
	if err != nil {
		return webAuthnContext{}, err
	}
	base, ok := configuredPublicServerAddr(&config)
	if !ok {
		return webAuthnContext{}, errPasskeyRPInvalid
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return webAuthnContext{}, err
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" {
		return webAuthnContext{}, fmt.Errorf("passkeys require https or localhost")
	}
	rpID := strings.ToLower(parsed.Hostname())
	if rpID == "" {
		return webAuthnContext{}, errPasskeyRPInvalid
	}
	if ip := net.ParseIP(rpID); ip != nil && rpID != "127.0.0.1" && rpID != "::1" {
		return webAuthnContext{}, fmt.Errorf("passkeys require a domain name or localhost")
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if requestOrigin := strings.TrimSpace(r.Header.Get("Origin")); requestOrigin != "" && !sameOrigin(requestOrigin, origin) {
		return webAuthnContext{}, fmt.Errorf("origin does not match configured server address")
	}
	return webAuthnContext{RPID: rpID, Origin: origin}, nil
}

func sameOrigin(a, b string) bool {
	pa, errA := url.Parse(a)
	pb, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return constantTimeStringEqual(a, b)
	}
	return strings.EqualFold(pa.Scheme, pb.Scheme) && strings.EqualFold(pa.Host, pb.Host)
}

func newWebAuthn(ctx webAuthnContext) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:          ctx.RPID,
		RPDisplayName: "NetsGo",
		RPOrigins:     []string{ctx.Origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login: webauthn.TimeoutConfig{
				Enforce: true,
				Timeout: adminAuthChallengeDefaultTTL,
			},
			Registration: webauthn.TimeoutConfig{
				Enforce: true,
				Timeout: adminAuthChallengeDefaultTTL,
			},
		},
	})
}

func webAuthnUserFromPasskeys(user AdminUser, passkeys []AdminPasskey) (adminWebAuthnUser, error) {
	credentials := make([]webauthn.Credential, 0, len(passkeys))
	for _, passkey := range passkeys {
		credential, err := passkey.WebAuthnCredential()
		if err != nil {
			return adminWebAuthnUser{}, err
		}
		credentials = append(credentials, credential)
	}
	return adminWebAuthnUser{user: user, credentials: credentials}, nil
}

func marshalWebAuthnSession(session *webauthn.SessionData) (string, error) {
	raw, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalWebAuthnSession(raw string) (webauthn.SessionData, error) {
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return webauthn.SessionData{}, err
	}
	return session, nil
}

func webAuthnRequestFromJSON(r *http.Request, raw json.RawMessage) (*http.Request, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("credential response is required")
	}
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(strings.NewReader(string(raw)))
	clone.ContentLength = int64(len(raw))
	clone.Header = r.Header.Clone()
	clone.Header.Set("Content-Type", "application/json")
	return clone, nil
}

func webAuthnChallengeTTL(session *webauthn.SessionData) time.Duration {
	if session == nil || session.Expires.IsZero() {
		return adminAuthChallengeDefaultTTL
	}
	ttl := time.Until(session.Expires)
	if ttl <= 0 {
		return adminAuthChallengeDefaultTTL
	}
	return ttl
}
