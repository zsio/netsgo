package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret = []byte("netsgo-super-secret-key-change-me") // 在生产环境应通过配置或环境变量加载

// AdminClaims JWT 的载荷定义
type AdminClaims struct {
	UserID   string `json:"uid"`
	Username string `json:"name"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateAdminToken 生成一个新的 JWT Token
func GenerateAdminToken(user *AdminUser) (string, error) {
	claims := AdminClaims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), // 24 个小时过期
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// RequireAuth 验证请求头中的 JWT 令牌并提取用户信息放入 Context
func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
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

		tokenString := parts[1]
		claims := &AdminClaims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		// 将用户信息存入 Context
		ctx := context.WithValue(r.Context(), "admin_user", claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// GetAdminFromContext 从 Context 中提取当前请求的管理员信息
func GetAdminFromContext(ctx context.Context) *AdminClaims {
	claims, ok := ctx.Value("admin_user").(*AdminClaims)
	if !ok {
		return nil
	}
	return claims
}
