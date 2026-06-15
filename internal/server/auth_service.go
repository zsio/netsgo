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
	adminStore    *AdminStore
	loginLimiter  *RateLimiter
	clientLimiter *RateLimiter
	mfaLimiter    *mfaAttemptLimiter
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

	a.mfaLimiter = newMFAAttemptLimiter(time.Minute, 10, 5*time.Minute)
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
