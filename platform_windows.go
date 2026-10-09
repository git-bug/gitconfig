package gitconfig

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// openRegular opens a file for reading; Windows has no FIFO to open by name.
func openRegular(path string) (*os.File, error) {
	return os.Open(path)
}

// Another process holding a file for a moment, as a concurrent writer or a
// reader (an editor, an antivirus) does, makes opening or replacing it fail.
// Such failures are retried for up to a second.
const retryFor = time.Second

// Windows error codes for a file another process holds.
const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// retryTransient calls fn until it succeeds, fails for another reason, or
// a second has passed.
func retryTransient(fn func() error) error {
	deadline := time.Now().Add(retryFor)
	delay := time.Millisecond
	for {
		err := fn()
		transient := errors.Is(err, errorAccessDenied) || errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
		if err == nil || !transient || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		delay = min(2*delay, 50*time.Millisecond)
	}
}

// ownedByCurrentUser reports whether path belongs to the current user. The
// Administrators group counts as the user if the user is an administrator,
// elevated or not: files an elevated process creates belong to that group.
// sudoUID is not used.
func ownedByCurrentUser(path, sudoUID string) (bool, error) {
	// The owner checked is the link target's, as with os.Stat elsewhere.
	// GetNamedSecurityInfo follows links itself; resolving them first
	// keeps that from resting on its behaviour.
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false, nil // no owner recorded, as on FAT32
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return false, err
	}
	if owner.Equals(user.User.Sid) {
		return true, nil
	}
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return false, nil
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, err
	}
	// Token 0 is the calling thread's: elevated. Not elevated, an
	// administrator's token is linked to an elevated one.
	if ok, err := windows.Token(0).IsMember(admins); err == nil && ok {
		return true, nil
	}
	linked, err := token.GetLinkedToken()
	if err != nil {
		return false, nil
	}
	defer linked.Close()
	// IsMember needs an impersonation token. Without SeTcbPrivilege the
	// linked token already is one, but a caller holding it gets a primary
	// token: duplicating covers both. Should duplicating fail, the linked
	// token is tried as is.
	var imp windows.Token
	if err := windows.DuplicateTokenEx(linked, windows.TOKEN_QUERY, nil, windows.SecurityIdentification, windows.TokenImpersonation, &imp); err == nil {
		defer imp.Close()
		linked = imp
	}
	ok, err := linked.IsMember(admins)
	return err == nil && ok, nil
}
