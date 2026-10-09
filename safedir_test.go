package gitconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSafeDirectory tests git's safe.directory matching, which Discover
// applies to a repository the user does not own. The directories exist: a
// listed path that cannot be resolved is skipped. Values are written with
// '/', as a backslash is an escape in a configuration file.
func TestSafeDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, d := range []string{"a/b", "ab"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	s := filepath.ToSlash(root)
	a := filepath.Join(root, "a")

	check := func(t *testing.T, config, dir string, want bool) {
		t.Helper()
		f, err := Parse("global", []byte("[safe]\n"+config))
		require.NoError(t, err)
		assert.Equal(t, want, safeDirectory(f.Config(), dir, ""))
	}

	for _, tc := range []struct {
		name   string
		config string
		dir    string
		want   bool
	}{
		{"not listed", "", a, false},
		{"listed", "directory = " + s + "/a\n", a, true},
		{"other directory listed", "directory = " + s + "/a\n", filepath.Join(a, "b"), false},
		{"no prefix match", "directory = " + s + "/a\n", filepath.Join(root, "ab"), false},
		{"star", "directory = *\n", a, true},
		{"trailing star", "directory = " + s + "/*\n", filepath.Join(a, "b"), true},
		{"trailing star, the directory itself", "directory = " + s + "/*\n", root, false},
		{"empty value resets", "directory = *\ndirectory =\n", a, false},
		{"listed after a reset", "directory =\ndirectory = " + s + "/a\n", a, true},
	} {
		t.Run(tc.name, func(t *testing.T) { check(t, tc.config, tc.dir, tc.want) })
	}

	t.Run("relative directory", func(t *testing.T) {
		// Checked against git 2.56: "../*" trusts nothing, as "../repo"
		// does not. A relative dir is taken from the working directory.
		t.Chdir(a)
		for _, tc := range []struct {
			entry string
			dir   string
			want  bool
		}{
			{s + "/a", ".", true},
			{s + "/a/b", "b", true},
			{s + "/ab", "../ab", true},
			{s + "/*", "../ab", true},
			{"../*", "../ab", false},
			{"../ab", "../ab", false},
		} {
			t.Run(tc.entry+" "+tc.dir, func(t *testing.T) { check(t, "directory = "+tc.entry+"\n", tc.dir, tc.want) })
		}
	})
}
