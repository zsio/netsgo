//go:build linux

package installmethod

import (
	"os/exec"
	"strconv"
	"strings"

	"netsgo/internal/svcmgr"
)

func systemdUsable() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "show", "--property=Version", "--value").Run() == nil
}

func systemdMainPID(role svcmgr.Role) (int, error) {
	out, err := exec.Command("systemctl", "show", svcmgr.UnitName(role), "--property=MainPID", "--value").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
