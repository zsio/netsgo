package server

import "time"

// AuthService holds all state related to authentication and access control:
//   - AdminStore (system initialization, admin users, sessions, client tokens, API keys)
//   - Two rate limiters (login, client connection)
//   - authTimeout (read deadline during the WebSocket authentication phase)
//
// Other files in the same package access it via s.auth.*; no interface is exposed externally.
type AuthService struct {
	adminStore    *AdminStore
	loginLimiter  *RateLimiter
	clientLimiter *RateLimiter
	authTimeout   time.Duration
}

// newAuthService creates an empty AuthService (fields are populated during Start()).
func newAuthService() *AuthService {
	return &AuthService{}
}

// initRateLimiters initializes the server's rate limiters.
func (a *AuthService) initRateLimiters() {
	a.loginLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     10,
		MaxFailures:     5,
		LockoutPeriod:   15 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})

	a.clientLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     20,
		MaxFailures:     10,
		LockoutPeriod:   15 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})

}
