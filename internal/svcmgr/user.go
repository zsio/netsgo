package svcmgr

import (
	"errors"
	"os/user"
)

var ErrUnsupportedPlatform = errors.New("svcmgr: only supported on Linux")

func UserExists(username string) (bool, error) {
	_, err := user.Lookup(username)
	if err == nil {
		return true, nil
	}
	if _, ok := err.(user.UnknownUserError); ok {
		return false, nil
	}
	return false, err
}
