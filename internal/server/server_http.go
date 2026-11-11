package server

import "net/http"

func (s *Server) StartHTTPOnly() http.Handler {
	return s.newHTTPHandler()
}

func (s *Server) newHTTPHandler() http.Handler {
	return s.hostDispatchHandler(s.securityHeadersHandler(s.newManagementMux()))
}

func (s *Server) newManagementMux() *http.ServeMux {
	mux := http.NewServeMux()
	s.registerManagementRoutes(mux)
	return mux
}

func (s *Server) newHTTPMux() *http.ServeMux {
	mux := s.newManagementMux()
	s.registerInternalWSRoutes(mux)
	return mux
}

func (s *Server) registerManagementRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleWeb)
	// Server-wide state remains administrator-only. Resource-bearing routes are
	// always given an explicit owner scope below.
	mux.HandleFunc("GET /api/status", s.RequireAdmin(s.handleAPIStatus))
	mux.HandleFunc("GET /api/version/check", s.RequireAdmin(s.handleAPIVersionCheck))

	// Self resource scope.
	mux.HandleFunc("GET /api/clients", s.requireSelfResourceScope(s.handleAPIClients))
	mux.HandleFunc("GET /api/clients/{id}/tunnels", s.requireSelfResourceScope(s.handleClientTunnels))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireSelfResourceScope(s.handleDeleteClient))
	mux.HandleFunc("GET /api/clients/{id}/version/check", s.requireSelfResourceScope(s.handleAPIClientVersionCheck))
	mux.HandleFunc("PUT /api/clients/{id}/display-name", s.requireSelfResourceScope(s.handleUpdateDisplayName))
	mux.HandleFunc("PUT /api/clients/{id}/bandwidth-settings", s.requireSelfResourceScope(s.handleUpdateBandwidthSettings))
	mux.HandleFunc("GET /api/clients/{id}/traffic", s.requireSelfResourceScope(s.handleGetClientTraffic))
	mux.HandleFunc("GET /api/traffic/realtime", s.requireSelfResourceScope(s.handleGetRealtimeTraffic))
	mux.HandleFunc("GET /api/console/snapshot", s.requireSelfResourceScope(s.handleAPIConsoleSnapshot))
	mux.HandleFunc("GET /api/tunnels", s.requireSelfResourceScope(s.handleUnifiedTunnelCollection))
	mux.HandleFunc("POST /api/tunnels", s.requireSelfResourceScope(s.handleUnifiedTunnelCollection))
	mux.HandleFunc("GET /api/tunnels/{tunnel_id}", s.requireSelfResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("PUT /api/tunnels/{tunnel_id}", s.requireSelfResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("DELETE /api/tunnels/{tunnel_id}", s.requireSelfResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("POST /api/tunnels/{tunnel_id}/migrate", s.requireSelfResourceScope(s.handleUnifiedTunnelMigrate))
	mux.HandleFunc("PUT /api/tunnels/{tunnel_id}/{action}", s.requireSelfResourceScope(s.handleUnifiedTunnelAction))
	mux.HandleFunc("GET /api/keys", s.requireSelfResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("POST /api/keys", s.requireSelfResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("PUT /api/keys/{id}/{action}", s.requireSelfResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("DELETE /api/keys/{id}", s.requireSelfResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("GET /api/activity", s.requireSelfResourceScope(s.handleAPIActivity))
	mux.HandleFunc("GET /api/webhooks/catalog", s.requireSelfResourceScope(s.handleAPIWebhookCatalog))
	mux.HandleFunc("GET /api/webhooks", s.requireSelfResourceScope(s.handleAPIWebhooks))
	mux.HandleFunc("POST /api/webhooks", s.requireSelfResourceScope(s.handleAPIWebhooks))
	mux.HandleFunc("GET /api/webhooks/{id}", s.requireSelfResourceScope(s.handleAPIWebhookItem))
	mux.HandleFunc("PUT /api/webhooks/{id}", s.requireSelfResourceScope(s.handleAPIWebhookItem))
	mux.HandleFunc("PUT /api/webhooks/{id}/enabled", s.requireSelfResourceScope(s.handleAPIWebhookEnabledToggle))
	mux.HandleFunc("DELETE /api/webhooks/{id}", s.requireSelfResourceScope(s.handleAPIWebhookItem))
	mux.HandleFunc("POST /api/webhooks/preview", s.requireSelfResourceScope(s.handleAPIWebhookPreview))
	mux.HandleFunc("POST /api/webhooks/test", s.requireSelfResourceScope(s.handleAPIWebhookTest))
	mux.HandleFunc("GET /api/webhooks/{id}/deliveries", s.requireSelfResourceScope(s.handleAPIWebhookDeliveries))
	mux.HandleFunc("GET /api/webhook-deliveries/{id}", s.requireSelfResourceScope(s.handleAPIWebhookDelivery))
	mux.HandleFunc("POST /api/webhook-deliveries/{id}/replay", s.requireSelfResourceScope(s.handleAPIWebhookReplay))
	mux.HandleFunc("GET /api/events", s.requireSelfResourceScope(s.handleSSE))

	mux.HandleFunc("POST /api/auth/login", s.handleAPILogin)
	mux.HandleFunc("POST /api/auth/mfa/verify", s.handleAPIMFAVerify)
	mux.HandleFunc("POST /api/auth/passkey/begin", s.handleAPIPasskeyLoginBegin)
	mux.HandleFunc("POST /api/auth/passkey/finish", s.handleAPIPasskeyLoginFinish)
	mux.HandleFunc("POST /api/auth/logout", s.RequireAuth(s.handleAPILogout))
	mux.HandleFunc("GET /api/auth/me", s.RequirePrincipal(s.handleAPIAuthMe))
	mux.HandleFunc("GET /api/admin/rate-limits/client-auth", s.RequireAdmin(s.handleAPIAdminClientAuthRateLimits))
	mux.HandleFunc("PUT /api/admin/rate-limits/client-auth", s.RequireAdmin(s.handleAPIAdminClientAuthRateLimits))
	mux.HandleFunc("DELETE /api/admin/rate-limits/client-auth", s.RequireAdmin(s.handleAPIAdminClientAuthRateLimits))
	mux.HandleFunc("GET /api/admin/settings/webhooks", s.RequireAdmin(s.handleAPIAdminWebhookSettings))
	mux.HandleFunc("PUT /api/admin/settings/webhooks", s.RequireAdmin(s.handleAPIAdminWebhookSettings))
	// Backward-compatible administrator key routes retain an explicit self
	// scope. New UI calls the /api/keys form above.
	mux.HandleFunc("GET /api/admin/keys", s.requireAdminSelfResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("POST /api/admin/keys", s.requireAdminSelfResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("PUT /api/admin/keys/{id}/{action}", s.requireAdminSelfResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("DELETE /api/admin/keys/{id}", s.requireAdminSelfResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("GET /api/admin/config", s.RequireAdmin(s.handleAPIAdminConfig))
	mux.HandleFunc("PUT /api/admin/config", s.RequireAdmin(s.handleAPIAdminConfig))
	mux.HandleFunc("GET /api/admin/security", s.RequireAdmin(s.handleAPIAdminSecurity))
	mux.HandleFunc("PUT /api/admin/security/username", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityUsername))
	mux.HandleFunc("PUT /api/admin/security/password", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityPassword))
	mux.HandleFunc("POST /api/admin/security/totp/begin", s.RequireAdmin(s.handleAPIAdminSecurityTOTPBegin))
	mux.HandleFunc("POST /api/admin/security/totp/confirm", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityTOTPConfirm))
	mux.HandleFunc("DELETE /api/admin/security/totp", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityTOTPDisable))
	mux.HandleFunc("POST /api/admin/security/recovery-codes/regenerate", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityRecoveryRegenerate))
	mux.HandleFunc("GET /api/admin/security/passkeys", s.RequireAdmin(s.handleAPIAdminSecurityPasskeys))
	mux.HandleFunc("POST /api/admin/security/passkeys/begin", s.RequireAdmin(s.handleAPIAdminSecurityPasskeyBegin))
	mux.HandleFunc("POST /api/admin/security/passkeys/finish", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityPasskeyFinish))
	mux.HandleFunc("PUT /api/admin/security/passkeys/{id}", s.RequireAdmin(s.handleAPIAdminSecurityPasskeyItem))
	mux.HandleFunc("DELETE /api/admin/security/passkeys/{id}", s.requireAdminSelfSessionMutation(s.handleAPIAdminSecurityPasskeyItem))
	mux.HandleFunc("GET /api/admin/users", s.RequireAdmin(s.handleAPIAdminUsers))
	mux.HandleFunc("POST /api/admin/users", s.RequireAdmin(s.handleAPIAdminUsers))
	mux.HandleFunc("GET /api/admin/users/{user_id}", s.RequireAdmin(s.handleAPIAdminUser))
	mux.HandleFunc("GET /api/admin/users/{user_id}/deletion-impact", s.RequireAdmin(s.handleAPIAdminUserDeletionImpact))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/username", s.RequireAdmin(s.handleAPIAdminUserUsername))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/password", s.RequireAdmin(s.handleAPIAdminUserPassword))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/admin", s.RequireAdmin(s.handleAPIAdminUserAdmin))
	mux.HandleFunc("POST /api/admin/users/{user_id}/disable", s.RequireAdmin(s.handleAPIAdminUserDisable))
	mux.HandleFunc("POST /api/admin/users/{user_id}/enable", s.RequireAdmin(s.handleAPIAdminUserEnable))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}", s.RequireAdmin(s.handleAPIAdminUserDelete))
	mux.HandleFunc("POST /api/admin/users/{user_id}/sessions/revoke", s.RequireAdmin(s.handleAPIAdminUserSessionsRevoke))

	// Administrator target-user resource scope. A target is resolved by the
	// route middleware, never supplied in a request body or query parameter.
	mux.HandleFunc("GET /api/admin/users/{user_id}/clients", s.requireAdminUserResourceScope(s.handleAPIClients))
	mux.HandleFunc("GET /api/admin/users/{user_id}/clients/{id}/tunnels", s.requireAdminUserResourceScope(s.handleClientTunnels))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}/clients/{id}", s.requireAdminUserResourceScope(s.handleDeleteClient))
	mux.HandleFunc("GET /api/admin/users/{user_id}/clients/{id}/version/check", s.requireAdminUserResourceScope(s.handleAPIClientVersionCheck))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/clients/{id}/display-name", s.requireAdminUserResourceScope(s.handleUpdateDisplayName))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/clients/{id}/bandwidth-settings", s.requireAdminUserResourceScope(s.handleUpdateBandwidthSettings))
	mux.HandleFunc("GET /api/admin/users/{user_id}/clients/{id}/traffic", s.requireAdminUserResourceScope(s.handleGetClientTraffic))
	mux.HandleFunc("GET /api/admin/users/{user_id}/traffic/realtime", s.requireAdminUserResourceScope(s.handleGetRealtimeTraffic))
	mux.HandleFunc("GET /api/admin/users/{user_id}/console/snapshot", s.requireAdminUserResourceScope(s.handleAPIConsoleSnapshot))
	mux.HandleFunc("GET /api/admin/users/{user_id}/tunnels", s.requireAdminUserResourceScope(s.handleUnifiedTunnelCollection))
	mux.HandleFunc("POST /api/admin/users/{user_id}/tunnels", s.requireAdminUserResourceScope(s.handleUnifiedTunnelCollection))
	mux.HandleFunc("GET /api/admin/users/{user_id}/tunnels/{tunnel_id}", s.requireAdminUserResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/tunnels/{tunnel_id}", s.requireAdminUserResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}/tunnels/{tunnel_id}", s.requireAdminUserResourceScope(s.handleUnifiedTunnelItem))
	mux.HandleFunc("POST /api/admin/users/{user_id}/tunnels/{tunnel_id}/migrate", s.requireAdminUserResourceScope(s.handleUnifiedTunnelMigrate))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/tunnels/{tunnel_id}/{action}", s.requireAdminUserResourceScope(s.handleUnifiedTunnelAction))
	mux.HandleFunc("GET /api/admin/users/{user_id}/keys", s.requireAdminUserResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("POST /api/admin/users/{user_id}/keys", s.requireAdminUserResourceScope(s.handleAPIAdminKeys))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/keys/{id}/{action}", s.requireAdminUserResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}/keys/{id}", s.requireAdminUserResourceScope(s.handleAPIAdminKeyItem))
	mux.HandleFunc("GET /api/admin/users/{user_id}/activity", s.requireAdminUserResourceScope(s.handleAPIActivity))
	mux.HandleFunc("GET /api/admin/users/{user_id}/events", s.requireAdminUserResourceScope(s.handleSSE))

	// Administrator-global activity/event views are deliberately distinct from
	// self and target-user paths.
	mux.HandleFunc("GET /api/admin/activity", s.RequireAdmin(s.handleAPIActivity))
	mux.HandleFunc("GET /api/admin/events", s.RequireAdmin(s.handleSSE))
}

func (s *Server) registerInternalWSRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/control", s.handleControlWS)
	mux.HandleFunc("/ws/data", s.handleDataWS)
}

func (s *Server) securityHeadersHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; "+
				"frame-ancestors 'none'; form-action 'self'")
		if s.isHTTPSRequest(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
