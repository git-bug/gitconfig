package gitconfig

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// maxSymlinks is how many symbolic links an update follows to the file it
// edits.
const maxSymlinks = 5

var errTooManyLinks = errors.New("too many levels of symbolic links")

// updateFile edits the file at path under git's lock: see Env.UpdateFile.
func updateFile(path string, scope Scope, fn func(*File) error) (err error) {
	target, err := followLinks(path)
	if err != nil {
		return err
	}

	lockPath := target + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &FileError{Path: lockPath, Err: ErrLocked}
		}
		return &FileError{Path: lockPath, Err: err}
	}
	committed := false
	defer func() {
		if !committed {
			lock.Close()
			os.Remove(lockPath)
		}
	}()

	// Read under the lock: no update made since can be lost.
	data, err := readConfigFile(target, maxLoadBytes)
	exists := err == nil
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data = nil
	case err != nil:
		return &FileError{Path: path, Err: err}
	}
	p, err := parse(path, data)
	if err != nil {
		return &FileError{Path: path, Err: err}
	}
	f := &File{name: path, scope: scope, data: data, parsed: p}

	if err := callClosing(f, fn); err != nil {
		return err
	}
	if bytes.Equal(f.data, data) {
		return nil
	}

	if _, err := lock.Write(f.data); err != nil {
		return &FileError{Path: lockPath, Err: err}
	}
	if exists && runtime.GOOS != "windows" {
		fi, err := os.Stat(target)
		if err != nil {
			return &FileError{Path: target, Err: err}
		}
		// Exactly the target's bits: chmod is not subject to the umask.
		if err := lock.Chmod(fi.Mode().Perm()); err != nil {
			return &FileError{Path: lockPath, Err: err}
		}
	}
	if err := lock.Close(); err != nil {
		return &FileError{Path: lockPath, Err: err}
	}
	if err := retryTransient(func() error { return os.Rename(lockPath, target) }); err != nil {
		return &FileError{Path: target, Err: err}
	}
	committed = true
	return nil
}

// callClosing calls fn and closes f when fn returns or panics.
func callClosing(f *File, fn func(*File) error) error {
	defer func() { f.closed = true }()
	return fn(f)
}

// followLinks returns the file path names once symbolic links are followed,
// as git does: up to maxSymlinks of them, a relative target resolved
// against the directory of the link. The file may not exist; if it does, it
// must be a regular file.
func followLinks(path string) (string, error) {
	p := path
	for n := 0; ; n++ {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return p, nil
		}
		if err != nil {
			return "", &FileError{Path: p, Err: err}
		}
		switch {
		case fi.Mode().IsRegular():
			return p, nil
		case fi.Mode()&fs.ModeSymlink == 0:
			return "", &FileError{Path: p, Err: ErrNotRegular}
		case n == maxSymlinks:
			return "", &FileError{Path: path, Err: errTooManyLinks}
		}
		link, err := os.Readlink(p)
		if err != nil {
			return "", &FileError{Path: p, Err: err}
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(p), link)
		}
		p = link
	}
}
