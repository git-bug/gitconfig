//go:build unix

package gitconfig_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

// withTimeout fails the test if fn does not return within a few seconds:
// git hangs reading a FIFO, this library must not.
func withTimeout(t *testing.T, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("blocked on a FIFO")
		return nil
	}
}

func TestEnvFIFO(t *testing.T) {
	dir := tempDir(t)
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))

	t.Run("include", func(t *testing.T) {
		main := filepath.Join(dir, "main")
		writeFile(t, main, "[include]\n\tpath = fifo\n")
		err := withTimeout(t, func() error {
			_, err := gitconfig.Env{GlobalConfig: []string{main}}.Load()
			return err
		})
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
	})

	t.Run("configuration file", func(t *testing.T) {
		err := withTimeout(t, func() error {
			_, err := gitconfig.Env{GlobalConfig: []string{fifo}}.Load()
			return err
		})
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
	})

	t.Run("update", func(t *testing.T) {
		err := withTimeout(t, func() error {
			return gitconfig.Env{}.UpdateFile(fifo, func(f *gitconfig.File) error { return nil })
		})
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
		assert.NoFileExists(t, fifo+".lock")
	})
}

func TestEnvUpdateFilePermissions(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	for _, mode := range []os.FileMode{0o600, 0o640, 0o664, 0o666, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(tempDir(t), "config")
			writeFile(t, path, "[a]\n\tk = v\n")
			require.NoError(t, os.Chmod(path, mode))

			err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
				return f.Set("a.k", "w")
			})
			require.NoError(t, err)
			fi, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, mode, fi.Mode().Perm())
		})
	}

	t.Run("new file", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
			return f.Set("a.k", "v")
		})
		require.NoError(t, err)
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	})
}
