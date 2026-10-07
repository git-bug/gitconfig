package gitconfig_test

import (
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
