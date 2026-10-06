package gitconfig

import (
	"io"
	"io/fs"
)

// lockAndUpdate edits the file at path with git's lock protocol:
//
//  1. create <path>.lock exclusively, or fail with ErrLocked;
//  2. read and parse path under the lock (a missing file is empty);
//  3. call fn;
//  4. if the content changed, write it to the lock file and rename it over
//     path, keeping the file's permission bits; otherwise leave path alone.
//
// If fn returns an error or panics, the lock is released and nothing is
// written. Readers see either the old or the new file, never a partial one.
func lockAndUpdate(fsys FS, path string, fn func(*File) error) error {
	panic("not implemented")
}

// FS is the filesystem an Env reads and writes. Paths are native paths, as
// git uses them: absolute, or relative to the working directory.
//
// Discover uses the operating system's filesystem. Implement FS only to plug in
// another one, such as go-billy or an in-memory filesystem.
type FS interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)

	// CreateExclusive creates name with perm, and fails with an error
	// satisfying errors.Is(err, fs.ErrExist) if it already exists.
	CreateExclusive(name string, perm fs.FileMode) (io.WriteCloser, error)

	// Rename replaces newpath with oldpath atomically.
	Rename(oldpath, newpath string) error
	Remove(name string) error
}

// osFS is the operating system's filesystem: names are passed to the os
// package unchanged.
type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) {
	panic("not implemented")
}

func (osFS) Stat(name string) (fs.FileInfo, error) {
	panic("not implemented")
}

func (osFS) CreateExclusive(name string, perm fs.FileMode) (io.WriteCloser, error) {
	panic("not implemented")
}

func (osFS) Rename(oldpath, newpath string) error {
	panic("not implemented")
}

func (osFS) Remove(name string) error {
	panic("not implemented")
}
