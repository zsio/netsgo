//go:build !unix

package flock

func TryLock(path string) (func(), error) {
	return func() {}, nil
}
