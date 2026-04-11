//go:build !linux

package flock

func TryLock(path string) (func(), error) {
	return func() {}, nil
}
