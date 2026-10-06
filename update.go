package gitconfig

import (
	"io"
	"io/fs"
)

// lockAndUpdate implements Env.UpdateFile.
func lockAndUpdate(fsys FS, path string, fn func(*File) error) error {
	panic("not implemented")
}

// FS is the filesystem an Env reads and writes. Paths are native paths, as
// git uses them: absolute, or relative to the working directory.
//
// A nil FS in an Env means the operating system's filesystem. Implement FS
// only to plug in another one, such as go-billy or an in-memory filesystem.
type FS interface {
	// ReadFile fails with an error satisfying errors.Is(err, fs.ErrNotExist)
	// if name does not exist.
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)

	// Lstat is like Stat but does not follow a final symbolic link.
	Lstat(name string) (fs.FileInfo, error)

	// ReadDir lists the directory name. It is used to count the
	// repository's worktrees.
	ReadDir(name string) ([]fs.DirEntry, error)

	// Readlink returns the target of the symbolic link name, as stored.
	Readlink(name string) (string, error)

	// CreateExclusive creates name with perm, subject to the umask, and
	// fails with an error satisfying errors.Is(err, fs.ErrExist) if it
	// already exists.
	CreateExclusive(name string, perm fs.FileMode) (io.WriteCloser, error)

	// Chmod sets the permission bits of name exactly, ignoring the umask.
	Chmod(name string, mode fs.FileMode) error

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

func (osFS) Lstat(name string) (fs.FileInfo, error) {
	panic("not implemented")
}

func (osFS) ReadDir(name string) ([]fs.DirEntry, error) {
	panic("not implemented")
}

func (osFS) Readlink(name string) (string, error) {
	panic("not implemented")
}

func (osFS) Chmod(name string, mode fs.FileMode) error {
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
