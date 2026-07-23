package server

import (
	"sync"
	"time"
)

// AuthService holds all state related to authentication and access control:
//   - AdminStore (system initialization, admin users, sessions, client tokens, API keys)
//   - Two rate limiters (login, client connection)
//   - authTimeout (read deadline during the WebSocket authentication phase)
//
// Other files in the same package access it via s.auth.*; no interface is exposed externally.
type AuthService struct {
	adminStore                   *AdminStore
	loginLimiter                 *RateLimiter
	clientLimiterMu              sync.RWMutex
	clientLimiter                *RateLimiter
	clientRateLimits             ClientAuthRateLimitSettings
	clientRateLimitUpdateMu      sync.Mutex
	clientRateLimitAfterSaveHook func()
	mfaLimiter                   *mfaAttemptLimiter
	authTimeout                  time.Duration
}

// newAuthService creates an empty AuthService (fields are populated during Start()).
func newAuthService() *AuthService {
	return &AuthService{
		clientRateLimits: ClientAuthRateLimitSettings{RequestsPerMinute: defaultClientAuthRateLimitPerMinute},
	}
}

// initRateLimiters initializes the server's rate limiters.
func (a *AuthService) initRateLimiters(clientSettings ClientAuthRateLimitSettings) {
	a.loginLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     10,
		MaxFailures:     5,
		LockoutPeriod:   15 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})

	a.replaceClientRateLimiter(clientSettings)
	a.mfaLimiter = newMFAAttemptLimiter(time.Minute, 10, 5*time.Minute)
}

func newClientAuthRateLimiter(settings ClientAuthRateLimitSettings) *RateLimiter {
	if !settings.Enabled {
		return nil
	}
	return NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     settings.RequestsPerMinute,
		CleanupInterval: 10 * time.Minute,
	})
}

func (a *AuthService) replaceClientRateLimiter(settings ClientAuthRateLimitSettings) {
	next := newClientAuthRateLimiter(settings)
	a.clientLimiterMu.Lock()
	previous := a.clientLimiter
	a.clientLimiter = next
	a.clientRateLimits = settings
	a.clientLimiterMu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

func (a *AuthService) updateClientRateLimitSettings(settings ClientAuthRateLimitSettings) error {
	a.clientRateLimitUpdateMu.Lock()
	defer a.clientRateLimitUpdateMu.Unlock()

	if err := a.adminStore.UpdateClientAuthRateLimitSettings(settings); err != nil {
		return err
	}
	if a.clientRateLimitAfterSaveHook != nil {
		a.clientRateLimitAfterSaveHook()
	}
	a.replaceClientRateLimiter(settings)
	return nil
}
func (a *AuthService) updateClientRateLimitSettingsWithActivity(settings ClientAuthRateLimitSettings, actor ActivityActor) (int64, error) {
	a.clientRateLimitUpdateMu.Lock()
	defer a.clientRateLimitUpdateMu.Unlock()

	activityID, err := a.adminStore.UpdateClientAuthRateLimitSettingsWithActivity(settings, actor)
	if err != nil {
		return 0, err
	}
	if a.clientRateLimitAfterSaveHook != nil {
		a.clientRateLimitAfterSaveHook()
	}
	a.replaceClientRateLimiter(settings)
	return activityID, nil
}

func (a *AuthService) clientRateLimitSnapshot(now time.Time) (ClientAuthRateLimitSettings, []RateLimitSnapshot) {
	a.clientLimiterMu.RLock()
	defer a.clientLimiterMu.RUnlock()
	settings := a.clientRateLimits
	if a.clientLimiter == nil {
		return settings, []RateLimitSnapshot{}
	}
	return settings, a.clientLimiter.Snapshot(now)
}

func (a *AuthService) allowClientAuthentication(ip string) (bool, time.Duration) {
	a.clientLimiterMu.RLock()
	defer a.clientLimiterMu.RUnlock()
	if a.clientLimiter == nil {
		return true, 0
	}
	return a.clientLimiter.Allow(ip)
}

func (a *AuthService) deleteClientRateLimit(ip string) bool {
	a.clientLimiterMu.RLock()
	defer a.clientLimiterMu.RUnlock()
	return a.clientLimiter != nil && a.clientLimiter.Delete(ip)
}

func (a *AuthService) stopRateLimiters() {
	if a.loginLimiter != nil {
		a.loginLimiter.Stop()
	}
	a.clientLimiterMu.Lock()
	clientLimiter := a.clientLimiter
	a.clientLimiter = nil
	a.clientLimiterMu.Unlock()
	if clientLimiter != nil {
		clientLimiter.Stop()
	}
}

type mfaAttemptLimiter struct {
	mu              sync.Mutex
	window          time.Duration
	maxFailures     int
	lockoutDuration time.Duration
	entries         map[string]mfaAttemptEntry
}

type mfaAttemptEntry struct {
	windowStart time.Time
	failures    int
	lockedUntil time.Time
	expiresAt   time.Time
}

func newMFAAttemptLimiter(window time.Duration, maxFailures int, lockoutDuration time.Duration) *mfaAttemptLimiter {
	return &mfaAttemptLimiter{
		window:          window,
		maxFailures:     maxFailures,
		lockoutDuration: lockoutDuration,
		entries:         make(map[string]mfaAttemptEntry),
	}
}

func (l *mfaAttemptLimiter) Allow(key string, challengeExpiresAt time.Time) (bool, time.Duration) {
	if l == nil || key == "" {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	entry := l.entries[key]
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(l.entries, key)
		return true, 0
	}
	if now.Before(entry.lockedUntil) {
		return false, entry.lockedUntil.Sub(now)
	}
	if challengeExpiresAt.IsZero() || now.Before(challengeExpiresAt) {
		entry.expiresAt = challengeExpiresAt
		l.entries[key] = entry
	}
	return true, 0
}

func (l *mfaAttemptLimiter) RecordFailure(key string, challengeExpiresAt time.Time) (locked bool, retryAfter time.Duration) {
	if l == nil || key == "" {
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	entry := l.entries[key]
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		entry = mfaAttemptEntry{}
	}
	if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
		entry.windowStart = now
		entry.failures = 0
		entry.lockedUntil = time.Time{}
	}
	entry.failures++
	entry.expiresAt = challengeExpiresAt
	if l.maxFailures > 0 && entry.failures > l.maxFailures {
		lockedUntil := now.Add(l.lockoutDuration)
		if !challengeExpiresAt.IsZero() && challengeExpiresAt.Before(lockedUntil) {
			lockedUntil = challengeExpiresAt
		}
		entry.lockedUntil = lockedUntil
		l.entries[key] = entry
		return true, lockedUntil.Sub(now)
	}
	l.entries[key] = entry
	return false, 0
}

func (l *mfaAttemptLimiter) Reset(key string) {
	if l == nil || key == "" {
		return
	}
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}
