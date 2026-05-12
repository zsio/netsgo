package svcmgr

import (
	"errors"
	"os/user"
)

var ErrUnsupportedPlatform = errors.New("svcmgr: only supported on Linux")

func UserExists(username string) (bool, error) {
	_, err := lookupSystemUser(username)
	if err == nil {
		return true, nil
	}
	if isUnknownUser(err) {
		return false, nil
	}
	return false, err
}

var lookupSystemUser = user.Lookup

func isUnknownUser(err error) bool {
	_, ok := err.(user.UnknownUserError)
	return ok
}
