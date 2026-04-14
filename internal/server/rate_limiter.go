package server

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiterConfig holds the rate limiter configuration.
type RateLimiterConfig struct {
	WindowSize      time.Duration // Sliding window size (e.g. 1 minute)
	MaxRequests     int           // Maximum requests within the window
	MaxFailures     int           // Maximum consecutive failures before lockout
	LockoutPeriod   time.Duration // Lockout duration
	CleanupInterval time.Duration // Background cleanup interval (0 disables background cleanup)
}

// rateLimitEntry holds the rate-limit state for a single IP.
type rateLimitEntry struct {
	mu           sync.Mutex
	timestamps   []time.Time // Request timestamps (sliding window)
	failures     int         // Consecutive failure count
	lockedUntil  time.Time   // Lockout expiry time
	lastActivity time.Time   // Last activity time
}

// RateLimiter is an IP-based rate limiter.
type RateLimiter struct {
	config  RateLimiterConfig
	entries sync.Map // IP string -> *rateLimitEntry
	stopCh  chan struct{}
}

// NewRateLimiter creates a new rate limiter.
// If config.CleanupInterval > 0, a background cleanup goroutine is started.
func NewRateLimiter(config RateLimiterConfig) *RateLimiter {
	rl := &RateLimiter{
		config: config,
		stopCh: make(chan struct{}),
	}

	if config.CleanupInterval > 0 {
		go rl.cleanupLoop()
	}

	return rl
}

// Allow checks whether the given IP is allowed to make a request.
// Returns (allowed, retry-after duration).
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	entry.lastActivity = now

	// Check whether the IP is locked out.
	if now.Before(entry.lockedUntil) {
		retryAfter := entry.lockedUntil.Sub(now)
		return false, retryAfter
	}

	// Prune timestamps outside the window.
	windowStart := now.Add(-rl.config.WindowSize)
	validIdx := 0
	for _, t := range entry.timestamps {
		if t.After(windowStart) {
			entry.timestamps[validIdx] = t
			validIdx++
		}
	}
	entry.timestamps = entry.timestamps[:validIdx]

	// Check request count within the window.
	if len(entry.timestamps) >= rl.config.MaxRequests {
		// Calculate when the earliest entry expires.
		retryAfter := entry.timestamps[0].Add(rl.config.WindowSize).Sub(now)
		if retryAfter < 0 {
			retryAfter = time.Second
		}
		return false, retryAfter
	}

	// Allow the request and record the timestamp.
	entry.timestamps = append(entry.timestamps, now)
	return true, 0
}

// RecordFailure records one authentication failure.
// If the consecutive failure count reaches the threshold, the IP is locked out.
func (rl *RateLimiter) RecordFailure(ip string) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.failures++
	entry.lastActivity = time.Now()

	if rl.config.MaxFailures > 0 && entry.failures >= rl.config.MaxFailures {
		entry.lockedUntil = time.Now().Add(rl.config.LockoutPeriod)
		entry.failures = 0 // Reset counter; start fresh after lockout expires.
	}
}

// ResetFailures resets the failure counter after a successful authentication.
func (rl *RateLimiter) ResetFailures(ip string) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.failures = 0
}

// Stop stops the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopCh:
		// Already stopped.
	default:
		close(rl.stopCh)
	}
}

// getOrCreate retrieves or creates the rate-limit entry for the given IP.
func (rl *RateLimiter) getOrCreate(ip string) *rateLimitEntry {
	if val, ok := rl.entries.Load(ip); ok {
		return val.(*rateLimitEntry)
	}
	entry := &rateLimitEntry{
		lastActivity: time.Now(),
	}
	actual, _ := rl.entries.LoadOrStore(ip, entry)
	return actual.(*rateLimitEntry)
}

// cleanupLoop periodically removes expired entries in the background.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCh:
			return
		}
	}
}

// cleanup removes inactive entries.
func (rl *RateLimiter) cleanup() {
	now := time.Now()
	// An entry is considered inactive after window size + lockout period.
	maxIdle := rl.config.WindowSize + rl.config.LockoutPeriod
	if maxIdle < time.Minute {
		maxIdle = time.Minute
	}

	rl.entries.Range(func(key, value any) bool {
		entry := value.(*rateLimitEntry)
		entry.mu.Lock()
		idle := now.Sub(entry.lastActivity) > maxIdle
		entry.mu.Unlock()
		if idle {
			rl.entries.Delete(key)
		}
		return true
	})
}

// --- helper functions ---

// clientIP extracts the client IP address from an HTTP request.
// Proxy headers (X-Forwarded-For / X-Real-IP) are trusted when:
//  1. The source is a loopback address (127.0.0.1 / ::1) — same-host nginx/Caddy, trusted by default.
//  2. TLS mode is "off" and the source IP is in the TrustedProxies list — explicitly configured by the user.
//
// In all other cases RemoteAddr is used to prevent attackers from spoofing proxy headers to bypass rate limiting.
func (s *Server) clientIP(r *http.Request) string {
	// Extract the direct connection IP first.
	directIP := remoteIPFromAddr(r.RemoteAddr)

	// Determine whether to trust proxy headers.
	if s.trustProxyHeaders(r) {
		// Prefer X-Forwarded-For (take the first IP).
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}

		// Fall back to X-Real-IP.
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	return directIP
}

// trustProxyHeaders reports whether proxy headers can be trusted for the current request.
// The rule matches clientIP: loopback is trusted by default, or when the source is an explicitly configured trusted proxy.
func (s *Server) trustProxyHeaders(r *http.Request) bool {
	directIP := remoteIPFromAddr(r.RemoteAddr)
	return isLoopback(directIP) || (s.TLS != nil && s.TLS.isTrustedProxy(directIP))
}

// isHTTPSRequest reports whether the browser-side request was made over HTTPS.
// 1. When the server terminates TLS itself, check r.TLS.
// 2. In reverse-proxy mode, only inspect protocol headers from trusted proxies.
func (s *Server) isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !s.trustProxyHeaders(r) {
		return false
	}

	if proto := firstHeaderToken(r.Header.Get("X-Forwarded-Proto")); strings.EqualFold(proto, "https") {
		return true
	}

	return strings.EqualFold(forwardedProto(r.Header.Get("Forwarded")), "https")
}

func firstHeaderToken(v string) string {
	if v == "" {
		return ""
	}
	parts := strings.SplitN(v, ",", 2)
	return strings.TrimSpace(parts[0])
}

// forwardedProto extracts the first proto value from an RFC 7239 Forwarded header.
func forwardedProto(v string) string {
	if v == "" {
		return ""
	}

	for _, entry := range strings.Split(v, ",") {
		for _, param := range strings.Split(entry, ";") {
			param = strings.TrimSpace(param)
			if len(param) < 6 || !strings.EqualFold(param[:6], "proto=") {
				continue
			}
			value := strings.TrimSpace(param[6:])
			return strings.Trim(value, `"`)
		}
	}

	return ""
}

// isLoopback reports whether the IP is a loopback address.
func isLoopback(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1"
}

// remoteIPFromAddr extracts the IP from a host:port formatted address.
func remoteIPFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// writeRateLimitResponse writes a 429 Too Many Requests response.
func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfterString(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	if _, err := w.Write([]byte(`{"error":"too many requests, please try again later"}`)); err != nil {
		log.Printf("⚠️ Failed to write rate limit response: %v", err)
	}
}

// retryAfterString converts a Duration to a seconds string.
func retryAfterString(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
