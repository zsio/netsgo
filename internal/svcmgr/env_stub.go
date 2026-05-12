//go:build !linux

package svcmgr

func chownEnvFileForServiceUser(string) error {
	return nil
}
