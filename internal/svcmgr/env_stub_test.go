//go:build !linux

package svcmgr

import "testing"

func assertEnvFileGroup(t *testing.T, path string, wantGID int) {
	t.Helper()
}
