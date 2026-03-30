package server

import "time"

// AuthService 持有认证与访问控制相关的全部状态：
//   - AdminStore（系统初始化、管理员、会话、客户端 Token、API Key）
//   - 三类速率限制器（登录、客户端接入、系统初始化）
//   - setupToken（首次初始化一次性令牌）
//   - authTimeout（WebSocket 认证阶段读超时）
//
// 同包内的其他文件通过 s.auth.* 直接访问；不对外暴露接口。
type AuthService struct {
	adminStore    *AdminStore
	loginLimiter  *RateLimiter
	clientLimiter *RateLimiter
	setupLimiter  *RateLimiter
	setupToken    string
	authTimeout   time.Duration
}

// newAuthService 创建空的 AuthService（字段在 Start() 阶段填充）。
func newAuthService() *AuthService {
	return &AuthService{}
}

// initRateLimiters 初始化服务端的三个速率限制器。
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

	a.setupLimiter = NewRateLimiter(RateLimiterConfig{
		WindowSize:      time.Minute,
		MaxRequests:     5,
		MaxFailures:     3,
		LockoutPeriod:   30 * time.Minute,
		CleanupInterval: 10 * time.Minute,
	})
}
