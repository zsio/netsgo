//go:build !linux

package installmethod

import (
	"netsgo/internal/svcmgr"
)

func systemdUsable() bool {
	return false
}

func systemdMainPID(role svcmgr.Role) (int, error) {
	return 0, svcmgr.ErrUnsupportedPlatform
}
