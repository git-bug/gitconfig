//go:build !windows

package gitconfig

import (
	"os"
	"strconv"
	"syscall"
)

// openRegular opens a file for reading without blocking on a FIFO; the
// caller checks that it is a regular file.
func openRegular(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// retryTransient calls fn once: only Windows refuses to open or replace a
// file another process holds.
func retryTransient(fn func() error) error {
	return fn()
}

// ownedByCurrentUser reports whether path belongs to the effective user or,
// running as root, to the user sudo recorded in SUDO_UID (sudoUID).
func ownedByCurrentUser(path, sudoUID string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	owner := fi.Sys().(*syscall.Stat_t).Uid
	euid := os.Geteuid()
	if uint64(owner) == uint64(euid) {
		return true, nil
	}
	uid, err := strconv.ParseUint(sudoUID, 10, 32)
	return euid == 0 && err == nil && uint64(owner) == uid, nil
}
