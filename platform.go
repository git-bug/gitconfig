package gitconfig

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

// openRegular opens a file for reading without blocking on a FIFO; the
// caller checks that it is a regular file. Windows has no FIFO to open by
// name.
func openRegular(path string) (*os.File, error) {
	flag := os.O_RDONLY
	if runtime.GOOS != "windows" {
		flag |= syscall.O_NONBLOCK
	}
	return os.OpenFile(path, flag, 0)
}

// Another process holding a file for a moment, as a concurrent writer or a
// reader (an editor, an antivirus) does, makes opening or replacing it fail
// on Windows. Such failures are retried for up to a second.
const retryFor = time.Second

// Windows error codes for a file another process holds.
const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// transient reports whether err is a Windows failure to retry.
func transient(err error) bool {
	return runtime.GOOS == "windows" &&
		(errors.Is(err, errorAccessDenied) || errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation))
}

// retryTransient calls fn until it succeeds, fails for another reason, or
// a second has passed. Only Windows refuses to open or replace a file
// another process holds: elsewhere fn is called once.
func retryTransient(fn func() error) error {
	deadline := time.Now().Add(retryFor)
	delay := time.Millisecond
	for {
		err := fn()
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 50*time.Millisecond)
	}
}
