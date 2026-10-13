package main

import (
	"errors"
	"os"
	"syscall"
)

func socketIdentity(path string) (string, error) {
	i, e := os.Lstat(path)
	if e != nil || i.Mode()&os.ModeSocket == 0 {
		return "", errors.New("Herdr socket missing or replaced")
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("socket identity unavailable")
	}
	return identityText(uint64(s.Dev), s.Ino, s.Ctimespec.Sec, s.Ctimespec.Nsec), nil
}
