//go:build unix

package main

import "syscall"

func execAsRoot(argv0 string, argv []string, envv []string) error {
	return syscall.Exec(argv0, argv, envv)
}
