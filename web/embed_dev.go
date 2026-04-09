//go:build dev

package web

import "io/fs"

// DistFS returns nil in dev mode; the frontend should run via the Vite dev server (bun run dev).
func DistFS() (fs.FS, error) {
	return nil, nil
}

// IsDevMode reports whether the current build is in development mode.
func IsDevMode() bool {
	return true
}
