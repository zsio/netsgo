package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimiterConfig 速率限制器配置
type RateLimiterConfig struct {
	WindowSize      time.Duration // 滑动窗口大小（如 1 分钟）
	MaxRequests     int           // 窗口内最大请求数
	MaxFailures     int           // 最大连续失败次数（触发锁定）
	LockoutPeriod   time.Duration // 锁定时长
	CleanupInterval time.Duration // 自动清理间隔（0 表示不启动后台清理）
}

// rateLimitEntry 单个 IP 的限速状态
type rateLimitEntry struct {
	mu           sync.Mutex
	timestamps   []time.Time // 请求时间戳（滑动窗口）
	failures     int         // 连续失败次数
	lockedUntil  time.Time   // 锁定截止时间
	lastActivity time.Time   // 最后一次活动时间
}

// RateLimiter 基于 IP 的速率限制器
type RateLimiter struct {
	config  RateLimiterConfig
	entries sync.Map // IP string -> *rateLimitEntry
	stopCh  chan struct{}
}

// NewRateLimiter 创建一个新的速率限制器
// 如果 config.CleanupInterval > 0，会启动后台清理 goroutine
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

// Allow 检查给定 IP 是否允许发起请求
// 返回 (是否允许, 需要等待的时间)
func (rl *RateLimiter) Allow(ip string) (bool, time.Duration) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	now := time.Now()
	entry.lastActivity = now

	// 检查是否被锁定
	if now.Before(entry.lockedUntil) {
		retryAfter := entry.lockedUntil.Sub(now)
		return false, retryAfter
	}

	// 清理窗口外的时间戳
	windowStart := now.Add(-rl.config.WindowSize)
	validIdx := 0
	for _, t := range entry.timestamps {
		if t.After(windowStart) {
			entry.timestamps[validIdx] = t
			validIdx++
		}
	}
	entry.timestamps = entry.timestamps[:validIdx]

	// 检查窗口内请求数
	if len(entry.timestamps) >= rl.config.MaxRequests {
		// 计算最早条目过期的时间
		retryAfter := entry.timestamps[0].Add(rl.config.WindowSize).Sub(now)
		if retryAfter < 0 {
			retryAfter = time.Second
		}
		return false, retryAfter
	}

	// 允许请求，记录时间戳
	entry.timestamps = append(entry.timestamps, now)
	return true, 0
}

// RecordFailure 记录一次认证失败
// 如果连续失败次数达到阈值，触发锁定
func (rl *RateLimiter) RecordFailure(ip string) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.failures++
	entry.lastActivity = time.Now()

	if rl.config.MaxFailures > 0 && entry.failures >= rl.config.MaxFailures {
		entry.lockedUntil = time.Now().Add(rl.config.LockoutPeriod)
		entry.failures = 0 // 重置计数，锁定期满后重新开始计数
	}
}

// ResetFailures 认证成功后重置失败计数
func (rl *RateLimiter) ResetFailures(ip string) {
	entry := rl.getOrCreate(ip)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.failures = 0
}

// Stop 停止后台清理 goroutine
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopCh:
		// 已经停止
	default:
		close(rl.stopCh)
	}
}

// getOrCreate 获取或创建 IP 对应的限速条目
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

// cleanupLoop 后台定期清理过期条目
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

// cleanup 清理不活跃的条目
func (rl *RateLimiter) cleanup() {
	now := time.Now()
	// 条目在窗口大小 + 锁定时长之后才被认为不活跃
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

// --- 辅助函数 ---

// clientIP 从 HTTP 请求中提取客户端 IP 地址。
// 信任代理头（X-Forwarded-For / X-Real-IP）的条件：
//  1. 来源是本地回环地址（127.0.0.1 / ::1）— 同机 nginx/Caddy，默认信任
//  2. TLS 模式为 off 且来源 IP 在 TrustedProxies 列表中 — 用户显式配置
//
// 其他情况一律使用 RemoteAddr，防止攻击者伪造代理头绕过速率限制。
func (s *Server) clientIP(r *http.Request) string {
	// 先提取直连 IP
	directIP := remoteIPFromAddr(r.RemoteAddr)

	// 判断是否信任代理头
	trustProxy := isLoopback(directIP) || // 本地回环默认信任
		(s.TLS != nil && s.TLS.isTrustedProxy(directIP)) // 显式配置的受信代理

	if trustProxy {
		// 优先使用 X-Forwarded-For（取第一个 IP）
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.SplitN(xff, ",", 2)
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}

		// 再尝试 X-Real-IP
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	return directIP
}

// isLoopback 判断 IP 是否是本地回环地址
func isLoopback(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1"
}

// remoteIPFromAddr 从 host:port 格式的地址中提取 IP
func remoteIPFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// writeRateLimitResponse 返回 429 Too Many Requests 响应
func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfterString(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(`{"error":"too many requests, please try again later"}`))
}

// retryAfterString 将 Duration 转为秒数字符串
func retryAfterString(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// initRateLimiters 初始化服务端的三个速率限制器
func (s *Server) initRateLimiters() {
	s.loginLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     10,
		MaxFailures:     5,
		LockoutPeriod:   15 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})

	s.clientLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     20,
		MaxFailures:     10,
		LockoutPeriod:   15 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})

	s.setupLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     5,
		MaxFailures:     3,
		LockoutPeriod:   30 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})
}
