package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const sessionCookieName = "netsgo_session"

// extractToken extracts the JWT token from the request.
// Priority: Authorization header > Cookie
func extractToken(r *http.Request) string {
	// 1. Authorization: Bearer <token>
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return parts[1]
		}
	}
	// 2. Cookie fallback (browser)
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return ""
}

// AdminClaims JWT 载荷 — 仅存 sessionID，业务信息从 session 中查找
type AdminClaims struct {
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// SessionInfo 从 Context 中提取的 session 信息（替代旧的 AdminClaims）
type SessionInfo struct {
	SessionID string
	UserID    string
	Username  string
	Role      string
}

// GenerateAdminToken 生成一个新的 JWT Token（绑定 session）
func (s *Server) GenerateAdminToken(session *AdminSession) (string, error) {
	secret, err := s.auth.adminStore.GetJWTSecret()
	if err != nil {
		return "", fmt.Errorf("get jwt secret: %w", err)
	}

	claims := AdminClaims{
		SessionID: session.ID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(session.ExpiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// RequireAuth 验证 JWT 令牌 + 服务端 session 是否有效
// 支持两种认证方式（优先级从高到低）:
//  1. Authorization: Bearer <token> — API 编程调用
//  2. Cookie netsgo_session — 浏览器 Web 面板
//
// JWT 只作为 session 载体，真正的权限判定来自 session 状态
func (s *Server) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		tokenString := extractToken(r)
		if tokenString == "" {
			http.Error(w, `{"error":"missing credentials"}`, http.StatusUnauthorized)
			return
		}

		// 🔑 核心：检查 adminStore 是否已初始化
		if s.auth.adminStore == nil {
			http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
			return
		}
		claims := &AdminClaims{}
		secret, err := s.auth.adminStore.GetJWTSecret()
		if err != nil {
			http.Error(w, `{"error":"jwt secret unavailable"}`, http.StatusInternalServerError)
			return
		}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return secret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		session := s.auth.adminStore.GetSession(claims.SessionID)
		if session == nil {
			// session 被删除（登出/踢出/过期）→ 401
			http.Error(w, `{"error":"session expired or revoked"}`, http.StatusUnauthorized)
			return
		}

		// 同一浏览器 session 内 UA 不会改变，变化说明 token 可能被盗用
		if r.UserAgent() != session.UserAgent {
			slog.Warn("session UA mismatch, possible token theft",
				"session_id", session.ID, "user", session.Username, "module", "security")
			http.Error(w, `{"error":"session environment mismatch"}`, http.StatusUnauthorized)
			return
		}

		// session 有效 → 注入用户信息到 Context
		info := &SessionInfo{
			SessionID: session.ID,
			UserID:    session.UserID,
			Username:  session.Username,
			Role:      session.Role,
		}
		ctx := context.WithValue(r.Context(), sessionContextKey, info)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// sessionContextKey context key 类型（避免碰撞）
type contextKey string

const sessionContextKey contextKey = "session_info"

// GetSessionFromContext 从 Context 中提取当前请求的 session 信息
func GetSessionFromContext(ctx context.Context) *SessionInfo {
	info, ok := ctx.Value(sessionContextKey).(*SessionInfo)
	if !ok {
		return nil
	}
	return info
}

// GetAdminFromContext 向后兼容的别名
func GetAdminFromContext(ctx context.Context) *SessionInfo {
	return GetSessionFromContext(ctx)
}

func (s *Server) RequireAuthIfInitialized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth.adminStore == nil || !s.auth.adminStore.IsInitialized() {
			next.ServeHTTP(w, r)
			return
		}
		s.RequireAuth(next).ServeHTTP(w, r)
	}
}

// setSessionCookie 设置 httpOnly session cookie

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/api",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.isHTTPSRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie 清除 session cookie
func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/api",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.isHTTPSRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
}
