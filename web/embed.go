//go:build !dev

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS 返回嵌入的前端构建产物（dist/ 子目录）
// 生产模式下，dist/ 目录会在编译时嵌入到二进制中
func DistFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// IsDevMode 返回当前是否为开发模式
func IsDevMode() bool {
	return false
}
