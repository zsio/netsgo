//go:build !dev

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the embedded frontend build artifacts (the dist/ subdirectory).
// In production mode, the dist/ directory is embedded into the binary at compile time.
func DistFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// IsDevMode reports whether the current build is in development mode.
func IsDevMode() bool {
	return false
}
