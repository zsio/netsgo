//go:build linux

package svcmgr

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
)

func DaemonReload() error {
	return runSystemctl("daemon-reload")
}

func EnableAndStart(unitName string) error {
	return runSystemctl("enable", "--now", unitName)
}

func DisableAndStop(unitName string) error {
	return runSystemctl("disable", "--now", unitName)
}

func IsActive(unitName string) (bool, error) {
	err := exec.Command("systemctl", "is-active", unitName).Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}

func IsEnabled(unitName string) (bool, error) {
	err := exec.Command("systemctl", "is-enabled", unitName).Run()
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, err
}

func Status(unitName string) (string, error) {
	cmd := exec.Command("systemctl", "status", unitName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return string(output), nil
		}
		return "", err
	}
	return string(output), nil
}

func JournalArgs(unitName string, tail int) []string {
	return []string{"journalctl", "-u", unitName, "-n", strconv.Itoa(tail), "-f"}
}

func runSystemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %v failed: %w: %s", args, err, stderr.String())
	}
	return nil
}
