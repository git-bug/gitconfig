package gitconfig_test

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

// Error messages must not leak the content of a file reached through an
// include, nor let a value write control sequences to a terminal (X4).

func TestSyntaxErrorDoesNotQuoteContent(t *testing.T) {
	for _, data := range []string{
		"[a]\n\tTOPSECRET = \"unterminated\n",
		"[TOPSECRET\n",
		"[a]\n\tk = TOPSECRET\\q\n",
		"[a]\nTOPSECRET\x1b]0;title\x07\n",
	} {
		_, err := gitconfig.Parse("f", []byte(data))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "TOPSECRET")
		assert.NotContains(t, err.Error(), "\x1b")
	}
}

func TestSyntaxErrorThroughInclude(t *testing.T) {
	dir := tempDir(t)
	secret := filepath.Join(dir, "secret")
	writeFile(t, secret, "token: TOPSECRET\n")
	writeFile(t, filepath.Join(dir, "main"), "[include]\n\tpath = secret\n")

	_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "TOPSECRET")
	assert.Contains(t, err.Error(), secret)
}

func TestErrorMessagesEscapeControlCharacters(t *testing.T) {
	const evil = "\x1b[31mred\x1b]0;title\x07\r\n"

	t.Run("ValueError", func(t *testing.T) {
		_, err := gitconfig.Entry{Key: gitconfig.Key{Section: "a", Name: "k"}, Value: evil}.Bool()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "\x1b")
		assert.NotContains(t, err.Error(), "\r")
		assert.Contains(t, err.Error(), `\x1b[31mred`)
		assert.Contains(t, err.Error(), "a.k")
	})

	t.Run("KeyError", func(t *testing.T) {
		_, err := gitconfig.ParseKey("a." + evil + ".k_y")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "\x1b")
		assert.NotContains(t, err.Error(), "\r")
	})

	// Paths are not quoted: as in git, their control characters become '?',
	// and also newline and tab, C1 and bidirectional controls, and bytes that
	// are not UTF-8.
	const evilPath = "p\x1b]52;c;cHduZWQ=\x07\x1b[2J\x7f\t\n\u009b\u202e\x9b é"
	const shown = "p?]52;c;cHduZWQ=??[2J?????? é"

	t.Run("SyntaxError", func(t *testing.T) {
		_, err := gitconfig.Parse(evilPath, []byte("[a\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "in file "+shown)
	})

	t.Run("ValueError file", func(t *testing.T) {
		e := gitconfig.Entry{Key: gitconfig.Key{Section: "a", Name: "k"}, Value: "x", Origin: gitconfig.Origin{File: evilPath}}
		_, err := e.Bool()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "in file "+shown)
	})

	t.Run("FileError", func(t *testing.T) {
		err := error(&gitconfig.FileError{Path: evilPath, Err: gitconfig.ErrLocked})
		assert.Equal(t, "gitconfig: "+shown+": "+gitconfig.ErrLocked.Error(), err.Error())

		// A wrapped *fs.PathError names the path again. It is made by hand:
		// opening such a name fails differently on each system.
		err = &gitconfig.FileError{Path: "f", Err: &fs.PathError{Op: "open", Path: evilPath, Err: fs.ErrNotExist}}
		require.ErrorIs(t, err, fs.ErrNotExist)
		assert.NotContains(t, err.Error(), "\x1b")
		assert.Contains(t, err.Error(), shown)
	})

	t.Run("include path", func(t *testing.T) {
		// Only the deepest file includes the path, and the depth limit
		// stops it there: it is never opened, which Windows would refuse.
		dir := tempDir(t)
		for i := range 10 {
			writeFile(t, filepath.Join(dir, fmt.Sprintf("f%d", i)), fmt.Sprintf("[include]\n\tpath = f%d\n", i+1))
		}
		writeFile(t, filepath.Join(dir, "f10"), "[include]\n\tpath = \"x\x1b]52;c;cHduZWQ=\x07\"\n")
		_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "f0")}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrIncludeDepth)
		assert.NotContains(t, err.Error(), "\x1b")
		assert.NotContains(t, err.Error(), "\x07")
		assert.Contains(t, err.Error(), "x?]52;c;cHduZWQ=?")
	})

	t.Run("EnvError", func(t *testing.T) {
		for _, name := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_NOSYSTEM"} {
			cleanGitEnv(t)
			t.Setenv(name, evil)
			_, err := gitconfig.Discover("")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "\x1b")
			assert.NotContains(t, err.Error(), "\r")
			assert.Contains(t, err.Error(), name)
		}
	})
}

func TestErrorMessagesHaveContext(t *testing.T) {
	t.Run("SyntaxError", func(t *testing.T) {
		_, err := gitconfig.Parse("some/file", []byte("[a]\n\tk = v\n[broken\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "some/file")
		assert.Contains(t, err.Error(), "3")
	})

	t.Run("FileError for a held lock", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path+".lock", "")
		err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error { return nil })
		require.Error(t, err)
		assert.Contains(t, err.Error(), path+".lock")
	})

	t.Run("KeyError", func(t *testing.T) {
		var f gitconfig.File
		err := f.Set("a.k_y", "v")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a.k_y")
	})
}
