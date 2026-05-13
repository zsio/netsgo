//go:build !unix

package manage

import "errors"

func execAsRoot(argv0 string, argv []string, envv []string) error {
	return errors.New("exec is not supported on this platform")
}
