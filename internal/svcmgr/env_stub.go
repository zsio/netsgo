//go:build !linux

package svcmgr

func repairEnvFileOwnership(string) error {
	return nil
}
