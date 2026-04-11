//go:build linux

package svcmgr

import (
	"fmt"
	"os/exec"
)

func EnsureUser(username string) error {
	exists, err := UserExists(username)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if err := runUserCommand("groupadd", "--system", username); err != nil {
		return err
	}
	return runUserCommand("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "-g", username, username)
}

func runUserCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w: %s", name, args, err, string(output))
	}
	return nil
}
