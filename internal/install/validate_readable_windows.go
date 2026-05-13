//go:build windows

package install

import "os"

func isReadableByUser(info os.FileInfo, username string) (bool, error) {
	return true, nil
}
