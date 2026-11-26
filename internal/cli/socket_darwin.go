package cli

import (
	"errors"

	"syscall"
)

func socketIdentity(path string) (string, error) {
	i, e := targetSocketInfo(path)
	if e != nil {
		return "", e
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("socket identity unavailable")
	}
	return identityText(uint64(s.Dev), s.Ino, s.Ctimespec.Sec, s.Ctimespec.Nsec), nil
}
