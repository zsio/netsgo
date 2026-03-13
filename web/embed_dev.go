//go:build dev

package web

import "io/fs"

// DistFS 在开发模式下返回 nil，表示前端资源不可用
// 此时前端应通过 Vite Dev Server (bun run dev) 独立运行
func DistFS() (fs.FS, error) {
	return nil, nil
}

// IsDevMode 返回当前是否为开发模式
func IsDevMode() bool {
	return true
}
