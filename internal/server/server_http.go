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
	mux.HandleFunc("GET /api/status", s.RequireAuth(s.handleAPIStatus))
	mux.HandleFunc("GET /api/clients", s.RequireAuth(s.handleAPIClients))
	mux.HandleFunc("GET /api/console/snapshot", s.RequireAuth(s.handleAPIConsoleSnapshot))
	mux.HandleFunc("PUT /api/clients/{id}/display-name", s.RequireAuth(s.handleUpdateDisplayName))
	mux.HandleFunc("PUT /api/clients/{id}/bandwidth-settings", s.RequireAuth(s.handleUpdateBandwidthSettings))
	mux.HandleFunc("POST /api/clients/{id}/tunnels", s.RequireAuth(s.handleCreateTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}/resume", s.RequireAuth(s.handleResumeTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}/stop", s.RequireAuth(s.handleStopTunnel))
	mux.HandleFunc("PUT /api/clients/{id}/tunnels/{name}", s.RequireAuth(s.handleUpdateTunnel))
	mux.HandleFunc("DELETE /api/clients/{id}/tunnels/{name}", s.RequireAuth(s.handleDeleteTunnel))
	mux.HandleFunc("GET /api/clients/{id}/traffic", s.RequireAuth(s.handleGetClientTraffic))

	mux.HandleFunc("POST /api/auth/login", s.handleAPILogin)
	mux.HandleFunc("POST /api/auth/logout", s.RequireAuth(s.handleAPILogout))
	mux.HandleFunc("GET /api/admin/keys", s.RequireAuth(s.handleAPIAdminKeys))
	mux.HandleFunc("POST /api/admin/keys", s.RequireAuth(s.handleAPIAdminKeys))
	mux.HandleFunc("PUT /api/admin/keys/{id}/{action}", s.RequireAuth(s.handleAPIAdminKeyItem))
	mux.HandleFunc("DELETE /api/admin/keys/{id}", s.RequireAuth(s.handleAPIAdminKeyItem))
	mux.HandleFunc("GET /api/admin/config", s.RequireAuth(s.handleAPIAdminConfig))
	mux.HandleFunc("PUT /api/admin/config", s.RequireAuth(s.handleAPIAdminConfig))

	mux.HandleFunc("GET /api/events", s.RequireAuth(s.handleSSE))
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
