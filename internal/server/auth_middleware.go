package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
	secret, err := s.adminStore.GetJWTSecret()
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

// RequireAuth 验证请求头中的 JWT 令牌 + 服务端 session 是否有效
// JWT 只作为 session 载体，真正的权限判定来自 session 状态
func (s *Server) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}

		// 🔑 核心：检查 adminStore 是否已初始化
		if s.adminStore == nil {
			http.Error(w, `{"error":"admin store not initialized"}`, http.StatusInternalServerError)
			return
		}

		tokenString := parts[1]
		claims := &AdminClaims{}
		secret, err := s.adminStore.GetJWTSecret()
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

		session := s.adminStore.GetSession(claims.SessionID)
		if session == nil {
			// session 被删除（登出/踢出/过期）→ 401
			http.Error(w, `{"error":"session expired or revoked"}`, http.StatusUnauthorized)
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

// RequireAuthIfInitialized 条件鉴权中间件：
// - 如果服务尚未初始化（adminStore 为 nil 或未走 setup），直接放行以保持向后兼容
// - 如果已初始化，则走完整的 JWT + Session 鉴权
func (s *Server) RequireAuthIfInitialized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminStore == nil || !s.adminStore.IsInitialized() {
			next.ServeHTTP(w, r)
			return
		}
		s.RequireAuth(next).ServeHTTP(w, r)
	}
}
