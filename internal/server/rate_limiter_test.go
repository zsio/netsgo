package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowWithinWindow(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 5,
		MaxFailures: 3,
		LockoutPeriod: 15 * time.Minute,
	})
	defer rl.Stop()

	for i := 0; i < 5; i++ {
		allowed, _ := rl.Allow("1.2.3.4")
		if !allowed {
			t.Fatalf("第 %d 次请求应被允许", i+1)
		}
	}
}

func TestRateLimiter_BlockAfterWindowExceeded(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:  time.Minute,
		MaxRequests: 3,
		MaxFailures: 10,
		LockoutPeriod: 15 * time.Minute,
	})
	defer rl.Stop()

	// 用完 3 次配额
	for i := 0; i < 3; i++ {
		allowed, _ := rl.Allow("1.2.3.4")
		if !allowed {
			t.Fatalf("第 %d 次请求应被允许", i+1)
		}
	}

	// 第 4 次应被拒绝
	allowed, retryAfter := rl.Allow("1.2.3.4")
	if allowed {
		t.Fatal("超过窗口限制后应被拒绝")
	}
	if retryAfter <= 0 {
		t.Fatal("retryAfter 应 > 0")
	}

	// 不同 IP 不受影响
	allowed, _ = rl.Allow("5.6.7.8")
	if !allowed {
		t.Fatal("不同 IP 不应受影响")
	}
}

func TestRateLimiter_LockoutAfterFailures(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100, // 不受窗口限速影响
		MaxFailures:   3,
		LockoutPeriod: 500 * time.Millisecond, // 短锁定方便测试
	})
	defer rl.Stop()

	ip := "10.0.0.1"

	// 记录 3 次失败
	for i := 0; i < 3; i++ {
		rl.RecordFailure(ip)
	}

	// 应被锁定
	allowed, retryAfter := rl.Allow(ip)
	if allowed {
		t.Fatal("连续失败后应被锁定")
	}
	if retryAfter <= 0 {
		t.Fatal("锁定时 retryAfter 应 > 0")
	}
}

func TestRateLimiter_LockoutExpiry(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   2,
		LockoutPeriod: 100 * time.Millisecond, // 极短锁定
	})
	defer rl.Stop()

	ip := "10.0.0.2"

	// 触发锁定
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	allowed, _ := rl.Allow(ip)
	if allowed {
		t.Fatal("锁定期间应被拒绝")
	}

	// 等待锁定过期
	time.Sleep(150 * time.Millisecond)

	allowed, _ = rl.Allow(ip)
	if !allowed {
		t.Fatal("锁定过期后应恢复放行")
	}
}

func TestRateLimiter_ResetOnSuccess(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   100,
		MaxFailures:   3,
		LockoutPeriod: time.Hour,
	})
	defer rl.Stop()

	ip := "10.0.0.3"

	// 记录 2 次失败（未达阈值）
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	// 成功后重置
	rl.ResetFailures(ip)

	// 再记录 2 次失败（从 0 开始，未达阈值 3）
	rl.RecordFailure(ip)
	rl.RecordFailure(ip)

	// 不应被锁定（因为之前被重置了，现在只有 2 次）
	allowed, _ := rl.Allow(ip)
	if !allowed {
		t.Fatal("重置后再次失败未达阈值，不应被锁定")
	}
}

func TestRateLimiter_AutoCleanup(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:    time.Minute,
		MaxRequests:   10,
		MaxFailures:   5,
		LockoutPeriod: time.Minute,
	})
	defer rl.Stop()

	// 创建一个条目
	rl.Allow("cleanup-test-ip")

	// 手动将 lastActivity 设为很久以前
	if val, ok := rl.entries.Load("cleanup-test-ip"); ok {
		entry := val.(*rateLimitEntry)
		entry.mu.Lock()
		entry.lastActivity = time.Now().Add(-10 * time.Minute)
		entry.mu.Unlock()
	}

	// 手动触发清理
	rl.cleanup()

	// 条目应已被清理
	_, loaded := rl.entries.Load("cleanup-test-ip")
	if loaded {
		t.Fatal("过期条目应被自动清理")
	}
}

func TestRateLimiter_WindowSliding(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		WindowSize:    200 * time.Millisecond,
		MaxRequests:   2,
		MaxFailures:   100,
		LockoutPeriod: time.Hour,
	})
	defer rl.Stop()

	ip := "sliding-test"

	// 用完 2 次配额
	rl.Allow(ip)
	rl.Allow(ip)

	// 第 3 次被拒
	allowed, _ := rl.Allow(ip)
	if allowed {
		t.Fatal("超过窗口限制应被拒绝")
	}

	// 等待窗口滑过
	time.Sleep(250 * time.Millisecond)

	// 旧条目滑出窗口，应恢复
	allowed, _ = rl.Allow(ip)
	if !allowed {
		t.Fatal("窗口滑过后应恢复放行")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		want       string
	}{
		{
			name:       "从 RemoteAddr 提取",
			remoteAddr: "192.168.1.1:12345",
			want:       "192.168.1.1",
		},
		{
			name:       "X-Forwarded-For 单个 IP",
			remoteAddr: "10.0.0.1:80",
			xff:        "203.0.113.50",
			want:       "203.0.113.50",
		},
		{
			name:       "X-Forwarded-For 多个 IP 取第一个",
			remoteAddr: "10.0.0.1:80",
			xff:        "203.0.113.50, 70.41.3.18, 150.172.238.178",
			want:       "203.0.113.50",
		},
		{
			name:       "X-Real-IP 优先于 RemoteAddr",
			remoteAddr: "10.0.0.1:80",
			xri:        "198.51.100.1",
			want:       "198.51.100.1",
		},
		{
			name:       "X-Forwarded-For 优先于 X-Real-IP",
			remoteAddr: "10.0.0.1:80",
			xff:        "203.0.113.50",
			xri:        "198.51.100.1",
			want:       "203.0.113.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				r.Header.Set("X-Real-IP", tt.xri)
			}

			got := clientIP(r)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteRateLimitResponse(t *testing.T) {
	w := httptest.NewRecorder()
	writeRateLimitResponse(w, 30*time.Second)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("期望 429，得到 %d", w.Code)
	}

	retryAfter := w.Header().Get("Retry-After")
	if retryAfter != "30" {
		t.Errorf("Retry-After 期望 30，得到 %q", retryAfter)
	}
}
