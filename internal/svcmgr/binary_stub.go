//go:build !linux

package svcmgr

func InstallBinary(srcPath string) error {
	return ErrUnsupportedPlatform
}

func RemoveBinary() error {
	return ErrUnsupportedPlatform
}
