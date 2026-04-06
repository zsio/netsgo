//go:build !linux

package svcmgr

import "strconv"

func DaemonReload() error {
	return ErrUnsupportedPlatform
}

func EnableAndStart(unitName string) error {
	return ErrUnsupportedPlatform
}

func DisableAndStop(unitName string) error {
	return ErrUnsupportedPlatform
}

func IsActive(unitName string) (bool, error) {
	return false, ErrUnsupportedPlatform
}

func IsEnabled(unitName string) (bool, error) {
	return false, ErrUnsupportedPlatform
}

func Status(unitName string) (string, error) {
	return "", ErrUnsupportedPlatform
}

func JournalArgs(unitName string, tail int) []string {
	return []string{"journalctl", "-u", unitName, "-n", strconv.Itoa(tail), "-f"}
}
