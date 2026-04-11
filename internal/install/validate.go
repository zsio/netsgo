package install

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func validateInstallClientServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("server address must be a valid ws:// or wss:// URL")
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("server address must start with ws:// or wss://")
	}
	if u.Host == "" {
		return fmt.Errorf("server address must include a host")
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("server address must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("server address must not include a query or fragment")
	}
	return nil
}

func validateReadableCustomTLSFiles(certPath, keyPath, runUser string) error {
	if err := validateReadableCustomTLSFile(certPath, "certificate", runUser); err != nil {
		return err
	}
	return validateReadableCustomTLSFile(keyPath, "private key", runUser)
}

func validateReadableCustomTLSFile(path, label, runUser string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("TLS %s file is invalid: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("TLS %s path must be a regular file", label)
	}
	readable, err := isReadableByUser(info, runUser)
	if err != nil {
		return fmt.Errorf("failed to verify TLS %s readability: %w", label, err)
	}
	if !readable {
		return fmt.Errorf("TLS %s file must be readable by %s", label, runUser)
	}
	return nil
}

func isReadableByUser(info os.FileInfo, username string) (bool, error) {
	account, err := user.Lookup(username)
	if err != nil {
		return false, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return false, err
	}
	if uid == 0 {
		return true, nil
	}
	gids, err := account.GroupIds()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("unsupported file stat type")
	}
	mode := info.Mode().Perm()
	if int(stat.Uid) == uid && mode&0o400 != 0 {
		return true, nil
	}
	for _, gid := range gids {
		parsed, err := strconv.Atoi(gid)
		if err != nil {
			continue
		}
		if int(stat.Gid) == parsed && mode&0o040 != 0 {
			return true, nil
		}
	}
	return mode&0o004 != 0, nil
}
