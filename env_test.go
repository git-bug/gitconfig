package gitconfig_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

// sep is the separator the platform's paths use. An include's relative
// path is joined to the including file's directory with the separator that
// ends it, and is not cleaned: the include's own '/' are kept.
const sep = string(filepath.Separator)

// tempDir returns a temporary directory with symbolic links resolved, so
// that paths built from it are the real paths includeIf "gitdir:" compares.
func tempDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

// writeFile writes content to path, creating parent directories.
func writeFile(t testing.TB, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func readFile(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// initRepo creates a minimal git directory at gitDir, on branch main.
func initRepo(t testing.TB, gitDir, config string) {
	t.Helper()
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(gitDir, "config"), config)
}

// addWorktree registers a linked worktree named name in the repository at
// gitDir, as `git worktree add` does, and returns its git directory.
func addWorktree(t testing.TB, gitDir, name, branch string) string {
	t.Helper()
	wtGitDir := filepath.Join(gitDir, "worktrees", name)
	writeFile(t, filepath.Join(wtGitDir, "HEAD"), "ref: refs/heads/"+branch+"\n")
	writeFile(t, filepath.Join(wtGitDir, "commondir"), "../..\n")
	writeFile(t, filepath.Join(wtGitDir, "gitdir"), filepath.Join(filepath.Dir(gitDir), name, ".git")+"\n")
	return wtGitDir
}

type scoped struct {
	Scope gitconfig.Scope
	Value string
}

func scopedValues(entries []gitconfig.Entry) []scoped {
	var out []scoped
	for _, e := range entries {
		out = append(out, scoped{e.Origin.Scope, e.Value})
	}
	return out
}

// fullEnv is an Env with a file in every scope, each setting a.k to the
// scope's name.
func fullEnv(t testing.TB) gitconfig.Env {
	t.Helper()
	dir := tempDir(t)
	home := filepath.Join(dir, "home")
	gitDir := filepath.Join(dir, "repo", ".git")

	writeFile(t, filepath.Join(dir, "etc", "gitconfig"), "[a]\n\tk = system\n")
	writeFile(t, filepath.Join(home, ".config", "git", "config"), "[a]\n\tk = xdg\n\txdgonly = x\n")
	writeFile(t, filepath.Join(home, ".gitconfig"), "[a]\n\tk = home\n")
	initRepo(t, gitDir, "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig = true\n[a]\n\tk = local\n")
	writeFile(t, filepath.Join(gitDir, "config.worktree"), "[a]\n\tk = worktree\n")

	return gitconfig.Env{
		Home:         home,
		GitDir:       gitDir,
		SystemConfig: filepath.Join(dir, "etc", "gitconfig"),
		GlobalConfig: []string{filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig")},
		Command:      []gitconfig.CommandEntry{{Key: "a.k", Value: "command"}},
	}
}

func TestEnvLoad(t *testing.T) {
	t.Run("every scope in order of precedence", func(t *testing.T) {
		env := fullEnv(t)
		c, err := env.Load()
		require.NoError(t, err)

		assert.Equal(t, []scoped{
			{gitconfig.ScopeSystem, "system"},
			{gitconfig.ScopeGlobal, "xdg"},
			{gitconfig.ScopeGlobal, "home"},
			{gitconfig.ScopeLocal, "local"},
			{gitconfig.ScopeWorktree, "worktree"},
			{gitconfig.ScopeCommand, "command"},
		}, scopedValues(c.GetAll("a.k")))

		e, ok := c.Get("a.k")
		require.True(t, ok)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeCommand}, e.Origin)

		e, ok = c.Get("a.xdgonly")
		require.True(t, ok)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeGlobal, File: env.GlobalConfig[0], Line: 3}, e.Origin)

		local := c.GetAll("a.k")[3]
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeLocal, File: filepath.Join(env.GitDir, "config"), Line: 6}, local.Origin)
	})

	t.Run("worktree file ignored without extensions.worktreeConfig", func(t *testing.T) {
		env := fullEnv(t)
		writeFile(t, filepath.Join(env.GitDir, "config"), "[a]\n\tk = local\n")
		c, err := env.Load()
		require.NoError(t, err)
		assert.NotContains(t, values(c.GetAll("a.k")), "worktree")
	})

	t.Run("empty Env reads nothing", func(t *testing.T) {
		c, err := gitconfig.Env{}.Load()
		require.NoError(t, err)
		assert.Empty(t, slices.Collect(c.All()))
	})

	t.Run("missing files are empty", func(t *testing.T) {
		dir := tempDir(t)
		env := gitconfig.Env{
			Home:         dir,
			GitDir:       filepath.Join(dir, ".git"),
			SystemConfig: filepath.Join(dir, "nosystem"),
			GlobalConfig: []string{filepath.Join(dir, "a"), filepath.Join(dir, "b")},
		}
		require.NoError(t, os.Mkdir(env.GitDir, 0o755))
		c, err := env.Load()
		require.NoError(t, err)
		assert.Empty(t, slices.Collect(c.All()))
	})

	t.Run("file in place of a directory is missing", func(t *testing.T) {
		// A file ~/.config/git hides the XDG file, as for git.
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "git"), "")
		env := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "git", "config")}}
		c, err := env.Load()
		require.NoError(t, err)
		assert.Empty(t, slices.Collect(c.All()))
	})

	t.Run("outside a repository", func(t *testing.T) {
		env := fullEnv(t)
		env.GitDir = ""
		c, err := env.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"system", "xdg", "home", "command"}, values(c.GetAll("a.k")))
	})

	t.Run("command entries", func(t *testing.T) {
		c, err := gitconfig.Env{Command: []gitconfig.CommandEntry{
			{Key: "A.K", Value: "1"},
			{Key: "a.flag", Implicit: true},
			{Key: "remote.Origin.url", Value: "u"},
		}}.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"a.k=1", "a.flag", "remote.Origin.url=u"}, list(slices.Collect(c.All())))
		b, err := c.Bool("a.flag", false)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("invalid command key", func(t *testing.T) {
		_, err := gitconfig.Env{Command: []gitconfig.CommandEntry{{Key: "a.k_y", Value: "1"}}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrInvalidKey)
		var ke *gitconfig.KeyError
		require.ErrorAs(t, err, &ke)
		assert.Equal(t, "a.k_y", ke.Key)
	})

	t.Run("relative GitDir", func(t *testing.T) {
		_, err := gitconfig.Env{GitDir: ".git"}.Load()
		require.ErrorIs(t, err, gitconfig.ErrNotAbsolute)
	})

	t.Run("syntax error", func(t *testing.T) {
		env := fullEnv(t)
		local := filepath.Join(env.GitDir, "config")
		writeFile(t, local, "[a]\n\tk = v\n[broken\n")
		_, err := env.Load()

		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
		assert.Equal(t, local, fe.Path)
		var se *gitconfig.SyntaxError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, local, se.File)
		assert.Equal(t, 3, se.Line)
	})

	t.Run("configuration file is a directory", func(t *testing.T) {
		dir := tempDir(t)
		_, err := gitconfig.Env{GlobalConfig: []string{dir}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
		assert.Equal(t, dir, fe.Path)
	})
}

func TestEnvIncludes(t *testing.T) {
	// load writes the given files under a fresh directory and loads
	// <dir>/main as the global configuration, with <dir> as home.
	load := func(t *testing.T, files map[string]string) (string, *gitconfig.Config, error) {
		t.Helper()
		dir := tempDir(t)
		for name, content := range files {
			writeFile(t, filepath.Join(dir, name), strings.ReplaceAll(content, "$DIR", filepath.ToSlash(dir)))
		}
		c, err := gitconfig.Env{Home: dir, GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
		return dir, c, err
	}

	t.Run("spliced at the position of the directive", func(t *testing.T) {
		dir, c, err := load(t, map[string]string{
			"main":    "[a]\n\tk = before\n[include]\n\tpath = sub/inc\n[a]\n\tk = after\n",
			"sub/inc": "[a]\n\tk = inc\n",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"a.k=before", "include.path=sub/inc", "a.k=inc", "a.k=after"}, list(slices.Collect(c.All())))

		inc := c.GetAll("a.k")[1]
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeGlobal, File: dir + sep + "sub/inc", Line: 2}, inc.Origin)
	})

	t.Run("included value wins when last", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[user]\n\tname = home\n[include]\n\tpath = inc\n",
			"inc":  "[user]\n\tname = inc\n",
		})
		require.NoError(t, err)
		assert.Equal(t, "inc", c.Value("user.name", ""))
	})

	t.Run("origin path not cleaned", func(t *testing.T) {
		dir, c, err := load(t, map[string]string{
			"main":  "[include]\n\tpath = ./d/../d/inc\n",
			"d/inc": "[a]\n\tk = v\n",
		})
		require.NoError(t, err)
		e, ok := c.Get("a.k")
		require.True(t, ok)
		assert.Equal(t, dir+sep+"./d/../d/inc", e.Origin.File)
	})

	t.Run("nested includes relative to the including file", func(t *testing.T) {
		dir, c, err := load(t, map[string]string{
			"main":      "[include]\n\tpath = d1/inc\n",
			"d1/inc":    "[include]\n\tpath = d2/inc\n",
			"d1/d2/inc": "[a]\n\tk = deep\n",
		})
		require.NoError(t, err)
		e, ok := c.Get("a.k")
		require.True(t, ok)
		assert.Equal(t, "deep", e.Value)
		assert.Equal(t, dir+sep+"d1/d2/inc", e.Origin.File)
	})

	t.Run("home expanded", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main":  "[include]\n\tpath = ~/tinc\n[user]\n\tname = after\n[include]\n\tpath = ~/tinc2\n",
			"tinc":  "[user]\n\tname = tinc\n",
			"tinc2": "[user]\n\temail = e\n",
		})
		require.NoError(t, err)
		assert.Equal(t, "after", c.Value("user.name", ""))
		assert.Equal(t, "e", c.Value("user.email", ""))
	})

	t.Run("absolute path", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[include]\n\tpath = $DIR/abs\n",
			"abs":  "[a]\n\tk = v\n",
		})
		require.NoError(t, err)
		assert.Equal(t, "v", c.Value("a.k", ""))
	})

	t.Run("missing file ignored", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[include]\n\tpath = missing\n\tpath = ~/missing\n\tpath = main/missing\n[a]\n\tk = v\n",
		})
		require.NoError(t, err)
		assert.Equal(t, "v", c.Value("a.k", ""))
	})

	t.Run("several paths in one section", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[include]\n\tpath = one\n\tpath = two\n",
			"one":  "[a]\n\tk = 1\n",
			"two":  "[a]\n\tk = 2\n",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"1", "2"}, values(c.GetAll("a.k")))
	})

	t.Run("case-insensitive directive", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[Include]\n\tPath = inc\n",
			"inc":  "[a]\n\tk = v\n",
		})
		require.NoError(t, err)
		assert.Equal(t, "v", c.Value("a.k", ""))
	})

	t.Run("include subsection is not a directive", func(t *testing.T) {
		_, c, err := load(t, map[string]string{
			"main": "[include \"x\"]\n\tpath = inc\n",
			"inc":  "[a]\n\tk = v\n",
		})
		require.NoError(t, err)
		_, ok := c.Get("a.k")
		assert.False(t, ok)
	})

	t.Run("each scope's includes have its scope", func(t *testing.T) {
		env := fullEnv(t)
		writeFile(t, filepath.Join(env.GitDir, "config"), "[include]\n\tpath = inc\n")
		writeFile(t, filepath.Join(env.GitDir, "inc"), "[a]\n\tk = local-inc\n")
		c, err := env.Load()
		require.NoError(t, err)
		assert.Contains(t, scopedValues(c.GetAll("a.k")), scoped{gitconfig.ScopeLocal, "local-inc"})
	})

	t.Run("depth limit", func(t *testing.T) {
		chain := func(n int) map[string]string {
			files := map[string]string{"main": "[include]\n\tpath = c1\n"}
			for i := 1; i < n; i++ {
				files[fmt.Sprintf("c%d", i)] = fmt.Sprintf("[include]\n\tpath = c%d\n", i+1)
			}
			files[fmt.Sprintf("c%d", n)] = "[a]\n\tk = deep\n"
			return files
		}

		_, c, err := load(t, chain(10))
		require.NoError(t, err)
		assert.Equal(t, "deep", c.Value("a.k", ""))

		_, _, err = load(t, chain(11))
		require.ErrorIs(t, err, gitconfig.ErrIncludeDepth)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
	})

	t.Run("cycle", func(t *testing.T) {
		_, _, err := load(t, map[string]string{"main": "[include]\n\tpath = main\n"})
		require.ErrorIs(t, err, gitconfig.ErrIncludeDepth)
	})

	t.Run("syntax error in an included file", func(t *testing.T) {
		dir, _, err := load(t, map[string]string{
			"main": "[include]\n\tpath = ./inc\n",
			"inc":  "[a]\n\tk = \"unterminated\n",
		})
		var se *gitconfig.SyntaxError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, dir+sep+"./inc", se.File)
		assert.Equal(t, 2, se.Line)
	})

	t.Run("directory", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "main"), "[include]\n\tpath = d\n")
		require.NoError(t, os.Mkdir(filepath.Join(dir, "d"), 0o755))
		_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
	})

	t.Run("home unknown", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "main"), "[include]\n\tpath = ~/inc\n")
		_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
		require.Error(t, err)
	})

	t.Run("from the command line", func(t *testing.T) {
		dir := tempDir(t)
		inc := filepath.Join(dir, "inc")
		writeFile(t, inc, "[a]\n\tk = inc\n")

		c, err := gitconfig.Env{Command: []gitconfig.CommandEntry{{Key: "include.path", Value: inc}}}.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"include.path=" + inc, "a.k=inc"}, list(slices.Collect(c.All())))
		e, _ := c.Get("a.k")
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeCommand, File: inc, Line: 2}, e.Origin)

		_, err = gitconfig.Env{Command: []gitconfig.CommandEntry{{Key: "include.path", Value: "inc"}}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrNotAbsolute)
	})
}

func TestEnvLoadLimits(t *testing.T) {
	t.Run("file count", func(t *testing.T) {
		// Each level includes the next one 10 times: 10⁴ reads from a few
		// small files.
		dir := tempDir(t)
		for i := range 4 {
			writeFile(t, filepath.Join(dir, fmt.Sprintf("l%d", i)), strings.Repeat(fmt.Sprintf("[include]\n\tpath = l%d\n", i+1), 10))
		}
		writeFile(t, filepath.Join(dir, "l4"), "[a]\n\tk = v\n")

		_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "l0")}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrLoadLimit)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
	})

	t.Run("total size", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "big"), strings.Repeat("# padding padding padding padding\n", (8<<20)/34))
		writeFile(t, filepath.Join(dir, "main"), strings.Repeat("[include]\n\tpath = big\n", 9))

		_, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
		require.ErrorIs(t, err, gitconfig.ErrLoadLimit)
	})

	t.Run("under the limits", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "inc"), "[a]\n\tk = v\n")
		writeFile(t, filepath.Join(dir, "main"), strings.Repeat("[include]\n\tpath = inc\n", 100))

		c, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}.Load()
		require.NoError(t, err)
		assert.Len(t, c.GetAll("a.k"), 100)
	})

	t.Run("limits are per load", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "inc"), "[a]\n\tk = v\n")
		writeFile(t, filepath.Join(dir, "main"), strings.Repeat("[include]\n\tpath = inc\n", 600))
		env := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "main")}}

		for range 3 {
			_, err := env.Load()
			require.NoError(t, err)
		}
	})
}

func TestEnvIncludeIf(t *testing.T) {
	dir := tempDir(t)
	s := filepath.ToSlash(dir)
	home := filepath.Join(dir, "home")
	gitDir := filepath.Join(dir, "work", "proj", ".git")
	initRepo(t, gitDir, "")
	writeFile(t, filepath.Join(dir, "inc"), "[a]\n\tk = inc\n")

	// included loads a global file holding only includeIf <cond>.
	included := func(t *testing.T, env gitconfig.Env, cond string) bool {
		t.Helper()
		global := filepath.Join(t.TempDir(), "global")
		writeFile(t, global, fmt.Sprintf("[includeIf %q]\n\tpath = %s/inc\n", cond, s))
		env.GlobalConfig = []string{global}
		c, err := env.Load()
		require.NoError(t, err)
		_, ok := c.Get("a.k")
		return ok
	}

	env := gitconfig.Env{Home: home, GitDir: gitDir}

	t.Run("gitdir", func(t *testing.T) {
		for _, tc := range []struct {
			cond string
			want bool
		}{
			{"gitdir:" + s + "/work/", true},
			{"gitdir:" + s + "/work", false},
			{"gitdir:" + s + "/work/**", true},
			{"gitdir:" + s + "/w*/", true},
			{"gitdir:" + s + "/work/proj/.git", true},
			{"gitdir:" + s + "/work/proj/.git/", false},
			{"gitdir:" + s + "/work/proj", false},
			{"gitdir:" + s + "/other/", false},
			{"gitdir:work/", true},
			{"gitdir:proj/.git", true},
			{"gitdir:ork/", false},
			{"gitdir:" + strings.ToUpper(s) + "/WORK/", strings.ToUpper(s) == s},
			{"gitdir/i:" + strings.ToUpper(s) + "/WORK/", true},
			{"gitdir:~/../work/", false},
			{"gitdir:" + s + "/home/../work/", false},
			{"gitdir:" + s + "/./work/", false},
		} {
			t.Run(tc.cond, func(t *testing.T) {
				assert.Equal(t, tc.want, included(t, env, tc.cond))
			})
		}
	})

	t.Run("gitdir with home", func(t *testing.T) {
		env := gitconfig.Env{Home: dir, GitDir: gitDir}
		assert.True(t, included(t, env, "gitdir:~/work/"))
	})

	t.Run("gitdir matches the real path too", func(t *testing.T) {
		link := filepath.Join(dir, "link")
		symlink(t, filepath.Join(dir, "work"), link)
		env := gitconfig.Env{GitDir: filepath.Join(link, "proj", ".git")}
		assert.True(t, included(t, env, "gitdir:"+s+"/work/"), "real path")
		assert.True(t, included(t, env, "gitdir:"+s+"/link/"), "path as given")
	})

	t.Run("gitdir classes", func(t *testing.T) {
		// Checked against git 2.56. A malformed class does not fall back
		// to literal characters: the pattern matches nothing.
		for _, tc := range []struct {
			name string // of the directory holding .git
			cond string
			want bool
		}{
			{"x1", "gitdir:" + s + "/x[[:digit:]]/", true},
			{"x1", "gitdir:" + s + "/[[:lower:]][[:digit:]]/", true},
			{"x1", "gitdir:" + s + "/[[:upper:]]1/", false},
			{"x1", "gitdir/i:" + s + "/[[:upper:]]1/", true},
			{"x1", "gitdir:" + s + "/x[[:alpha:][:digit:]]/", true},
			{"x1", "gitdir:" + s + "/x[![:alpha:]]/", true},
			{"x1", "gitdir:" + s + "/x[[:punct:]]/", false},
			{"x1", "gitdir:" + s + "/x[[:digit:]a-]/", true},
			{"x1", "gitdir:" + s + "/x[[:bogus:]]/", false},
			{"x1", "gitdir:" + s + "/x[![:bogus:]]/", false},
			{"x1", "gitdir:" + s + "/x[1[:bogus:]]/", false},
			{"x1", "gitdir:" + s + "/x[[:DIGIT:]]/", false},
			{"x1", "gitdir:" + s + "/x[[::]1]/", false},
			{"x1", "gitdir:" + s + "/x[[:digit:]/", false},
			{"x1", "gitdir:" + s + "/x1/[a", false},
			{"x-", "gitdir:" + s + "/x[[:digit:]-z]/", true},
			{"xd", "gitdir:" + s + "/x[[:digit]/", true}, // no ":]": '[', ':', 'd', ...
			{"a[b", "gitdir:" + s + "/a[b/", false},
			{"a[b", "gitdir:" + s + "/a[[]b/", true},
			{"a[b", "gitdir:" + s + "/[[:alpha:]][[]b/", true},
		} {
			t.Run(tc.name+" "+tc.cond, func(t *testing.T) {
				gitDir := filepath.Join(dir, tc.name, ".git")
				initRepo(t, gitDir, "")
				assert.Equal(t, tc.want, included(t, gitconfig.Env{GitDir: gitDir}, tc.cond))
			})
		}
	})

	t.Run("malformed patterns take linear time", func(t *testing.T) {
		// Each '[' would otherwise scan the rest of the pattern. A final
		// ":]" makes each "[:" look for the end of a class name.
		for _, p := range []string{"[", "[\\\\]", "[:", "[[:"} {
			cond := "gitdir:" + strings.Repeat(p, 1<<18/len(p))
			assert.False(t, included(t, env, cond), "%q", p)
			assert.False(t, included(t, env, cond+":]"), "%q, then \":]\"", p)
		}
	})

	t.Run("onbranch", func(t *testing.T) {
		for _, tc := range []struct {
			head string
			cond string
			want bool
		}{
			{"ref: refs/heads/main\n", "onbranch:main", true},
			{"ref: refs/heads/main\n", "onbranch:ma*", true},
			{"ref: refs/heads/main\n", "onbranch:other", false},
			{"ref: refs/heads/main\n", "onbranch:mai", false},
			{"ref: refs/heads/feature/x\n", "onbranch:feature/", true},
			{"ref: refs/heads/feature/x\n", "onbranch:feature/*", true},
			{"ref: refs/heads/feature/x\n", "onbranch:x", false},
			{"0123456789012345678901234567890123456789\n", "onbranch:main", false},
			{"ref:refs/heads/main\n", "onbranch:main", true},
			{"ref:\trefs/heads/main\n", "onbranch:main", true},
		} {
			t.Run(tc.head+tc.cond, func(t *testing.T) {
				gitDir := filepath.Join(tempDir(t), ".git")
				initRepo(t, gitDir, "")
				writeFile(t, filepath.Join(gitDir, "HEAD"), tc.head)
				assert.Equal(t, tc.want, included(t, gitconfig.Env{GitDir: gitDir}, tc.cond))
			})
		}
	})

	t.Run("outside a repository", func(t *testing.T) {
		env := gitconfig.Env{Home: home}
		assert.False(t, included(t, env, "gitdir:/"))
		assert.False(t, included(t, env, "gitdir:**"))
		assert.False(t, included(t, env, "onbranch:main"))
		assert.False(t, included(t, env, "onbranch:*"))
	})

	t.Run("unknown condition", func(t *testing.T) {
		assert.False(t, included(t, env, "nosuch:x"))
		assert.False(t, included(t, env, "gitdir"))
	})

	t.Run("relative gitdir pattern from the command line", func(t *testing.T) {
		// git prints an error and goes on without the include.
		env := gitconfig.Env{GitDir: gitDir, Command: []gitconfig.CommandEntry{{Key: "includeIf.gitdir:./.path", Value: s + "/inc"}}}
		_, err := env.Load()
		require.ErrorIs(t, err, gitconfig.ErrNotAbsolute)
	})

	t.Run("in the repository's config, relative path", func(t *testing.T) {
		gitDir := filepath.Join(tempDir(t), ".git")
		initRepo(t, gitDir, "[includeIf \"onbranch:main\"]\n\tpath = inc\n")
		writeFile(t, filepath.Join(gitDir, "inc"), "[a]\n\tk = v\n")
		c, err := gitconfig.Env{GitDir: gitDir}.Load()
		require.NoError(t, err)
		e, ok := c.Get("a.k")
		require.True(t, ok)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeLocal, File: gitDir + sep + "inc", Line: 2}, e.Origin)
	})

	t.Run("linked worktree", func(t *testing.T) {
		gitDir := filepath.Join(tempDir(t), "main", ".git")
		initRepo(t, gitDir, "[includeIf \"onbranch:wt-branch\"]\n\tpath = inc\n")
		writeFile(t, filepath.Join(gitDir, "inc"), "[a]\n\tk = v\n")
		wt := addWorktree(t, gitDir, "wt", "wt-branch")

		c, err := gitconfig.Env{GitDir: wt}.Load()
		require.NoError(t, err)
		assert.Equal(t, "v", c.Value("a.k", ""), "HEAD of the worktree, config of the common directory")

		c, err = gitconfig.Env{GitDir: gitDir}.Load()
		require.NoError(t, err)
		_, ok := c.Get("a.k")
		assert.False(t, ok)
	})
}

func TestEnvLoadScope(t *testing.T) {
	t.Run("each scope alone", func(t *testing.T) {
		env := fullEnv(t)
		for _, tc := range []struct {
			scope gitconfig.Scope
			want  []scoped
		}{
			{gitconfig.ScopeSystem, []scoped{{gitconfig.ScopeSystem, "system"}}},
			{gitconfig.ScopeGlobal, []scoped{{gitconfig.ScopeGlobal, "xdg"}, {gitconfig.ScopeGlobal, "home"}}},
			{gitconfig.ScopeLocal, []scoped{{gitconfig.ScopeLocal, "local"}}},
			{gitconfig.ScopeWorktree, []scoped{{gitconfig.ScopeWorktree, "worktree"}}},
			{gitconfig.ScopeCommand, []scoped{{gitconfig.ScopeCommand, "command"}}},
		} {
			t.Run(tc.scope.String(), func(t *testing.T) {
				c, err := env.LoadScope(tc.scope)
				require.NoError(t, err)
				assert.Equal(t, tc.want, scopedValues(c.GetAll("a.k")))
			})
		}
	})

	t.Run("includes followed", func(t *testing.T) {
		env := fullEnv(t)
		writeFile(t, filepath.Join(env.GitDir, "config"), "[include]\n\tpath = inc\n")
		writeFile(t, filepath.Join(env.GitDir, "inc"), "[a]\n\tk = inc\n")
		c, err := env.LoadScope(gitconfig.ScopeLocal)
		require.NoError(t, err)
		assert.Equal(t, []scoped{{gitconfig.ScopeLocal, "inc"}}, scopedValues(c.GetAll("a.k")))
	})

	t.Run("absent files are empty", func(t *testing.T) {
		dir := tempDir(t)
		gitDir := filepath.Join(dir, ".git")
		initRepo(t, gitDir, "[extensions]\n\tworktreeConfig = true\n")
		env := gitconfig.Env{GitDir: gitDir, GlobalConfig: []string{filepath.Join(dir, "missing")}}
		for _, s := range []gitconfig.Scope{gitconfig.ScopeSystem, gitconfig.ScopeGlobal, gitconfig.ScopeWorktree, gitconfig.ScopeCommand} {
			c, err := env.LoadScope(s)
			require.NoError(t, err, s)
			assert.Empty(t, slices.Collect(c.All()), s)
		}
	})

	t.Run("worktree without the extension reads the local config", func(t *testing.T) {
		env := fullEnv(t)
		writeFile(t, filepath.Join(env.GitDir, "config"), "[a]\n\tk = local\n")
		c, err := env.LoadScope(gitconfig.ScopeWorktree)
		require.NoError(t, err)
		assert.Equal(t, []scoped{{gitconfig.ScopeLocal, "local"}}, scopedValues(c.GetAll("a.k")))
	})

	t.Run("unavailable scopes", func(t *testing.T) {
		env := fullEnv(t)
		_, err := env.LoadScope(gitconfig.ScopeFile)
		assert.Equal(t, gitconfig.ErrNoScope, err)

		env.GitDir = ""
		_, err = env.LoadScope(gitconfig.ScopeLocal)
		assert.Equal(t, gitconfig.ErrNoScope, err)
		_, err = env.LoadScope(gitconfig.ScopeWorktree)
		assert.Equal(t, gitconfig.ErrNoScope, err)
	})

	t.Run("worktree without the extension, several worktrees", func(t *testing.T) {
		env := fullEnv(t)
		writeFile(t, filepath.Join(env.GitDir, "config"), "[a]\n\tk = local\n")
		addWorktree(t, env.GitDir, "wt", "other")
		_, err := env.LoadScope(gitconfig.ScopeWorktree)
		assert.Equal(t, gitconfig.ErrNoScope, err)

		c, err := env.LoadScope(gitconfig.ScopeLocal)
		require.NoError(t, err)
		assert.Equal(t, "local", c.Value("a.k", ""))
	})

	t.Run("linked worktree", func(t *testing.T) {
		env := fullEnv(t)
		wt := addWorktree(t, env.GitDir, "wt", "other")
		writeFile(t, filepath.Join(wt, "config.worktree"), "[a]\n\tk = linked\n")
		env.GitDir = wt

		c, err := env.LoadScope(gitconfig.ScopeLocal)
		require.NoError(t, err)
		assert.Equal(t, "local", c.Value("a.k", ""))

		c, err = env.LoadScope(gitconfig.ScopeWorktree)
		require.NoError(t, err)
		assert.Equal(t, []scoped{{gitconfig.ScopeWorktree, "linked"}}, scopedValues(c.GetAll("a.k")))
	})
}

func TestEnvLoadFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "f")
	writeFile(t, path, "[a]\n\tk = file\n[include]\n\tpath = inc\n")
	writeFile(t, filepath.Join(dir, "inc"), "[a]\n\tk = inc\n")

	env := fullEnv(t)
	c, err := env.LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []scoped{{gitconfig.ScopeFile, "file"}, {gitconfig.ScopeFile, "inc"}}, scopedValues(c.GetAll("a.k")))
	e, _ := c.Get("a.k")
	assert.Equal(t, dir+sep+"inc", e.Origin.File)

	c, err = env.LoadFile(filepath.Join(dir, "missing"))
	require.NoError(t, err)
	assert.Empty(t, slices.Collect(c.All()))
}

func TestEnvLoadProtected(t *testing.T) {
	env := fullEnv(t)

	t.Run("system, global and command only", func(t *testing.T) {
		c, err := env.LoadProtected()
		require.NoError(t, err)
		assert.Equal(t, []string{"system", "xdg", "home", "command"}, values(c.GetAll("a.k")))
	})

	t.Run("the repository is not read", func(t *testing.T) {
		writeFile(t, filepath.Join(env.GitDir, "config"), "[broken\n")
		writeFile(t, filepath.Join(env.GitDir, "HEAD"), "garbage")
		_, err := env.LoadProtected()
		require.NoError(t, err)

		_, err = env.Load()
		require.Error(t, err)
	})
}

func TestEnvSafeDirectory(t *testing.T) {
	// The directories exist: SafeDirectory skips a listed path it cannot
	// resolve, and /srv only exists on some systems. Values are written
	// with '/', as a backslash is an escape in a configuration file.
	root := tempDir(t)
	for _, d := range []string{"a/b", "ab"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	s := filepath.ToSlash(root)
	a := filepath.Join(root, "a")

	for _, tc := range []struct {
		name   string
		global string
		local  string
		cmd    []gitconfig.CommandEntry
		dir    string
		want   bool
	}{
		{"not listed", "", "", nil, a, false},
		{"listed", "[safe]\n\tdirectory = " + s + "/a\n", "", nil, a, true},
		{"other directory listed", "[safe]\n\tdirectory = " + s + "/a\n", "", nil, filepath.Join(root, "a", "b"), false},
		{"no prefix match", "[safe]\n\tdirectory = " + s + "/a\n", "", nil, filepath.Join(root, "ab"), false},
		{"star", "[safe]\n\tdirectory = *\n", "", nil, a, true},
		{"trailing star", "[safe]\n\tdirectory = " + s + "/*\n", "", nil, filepath.Join(root, "a", "b"), true},
		{"trailing star, the directory itself", "[safe]\n\tdirectory = " + s + "/*\n", "", nil, root, false},
		{"empty value resets", "[safe]\n\tdirectory = *\n\tdirectory =\n", "", nil, a, false},
		{"listed after a reset", "[safe]\n\tdirectory =\n\tdirectory = " + s + "/a\n", "", nil, a, true},
		{"repository config ignored", "", "[safe]\n\tdirectory = *\n", nil, a, false},
		{"command line", "", "", []gitconfig.CommandEntry{{Key: "safe.directory", Value: s + "/a"}}, a, true},
		{"command line resets the global list", "[safe]\n\tdirectory = *\n", "", []gitconfig.CommandEntry{{Key: "safe.directory", Value: ""}}, a, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			gitDir := filepath.Join(dir, ".git")
			initRepo(t, gitDir, tc.local)
			global := filepath.Join(dir, "global")
			writeFile(t, global, tc.global)

			got, err := gitconfig.Env{GitDir: gitDir, GlobalConfig: []string{global}, Command: tc.cmd}.SafeDirectory(tc.dir)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
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
			t.Run(tc.entry+" "+tc.dir, func(t *testing.T) {
				global := filepath.Join(tempDir(t), "global")
				writeFile(t, global, "[safe]\n\tdirectory = "+tc.entry+"\n")
				got, err := gitconfig.Env{GlobalConfig: []string{global}}.SafeDirectory(tc.dir)
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("listed through an include", func(t *testing.T) {
		dir := tempDir(t)
		writeFile(t, filepath.Join(dir, "global"), "[include]\n\tpath = inc\n")
		writeFile(t, filepath.Join(dir, "inc"), "[safe]\n\tdirectory = "+s+"/a\n")
		got, err := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "global")}}.SafeDirectory(a)
		require.NoError(t, err)
		assert.True(t, got)
	})
}

func TestEnvWritePath(t *testing.T) {
	dir := tempDir(t)
	xdg := filepath.Join(dir, "xdg")
	home := filepath.Join(dir, "home")
	gitDir := filepath.Join(dir, "repo", ".git")
	initRepo(t, gitDir, "")
	extGitDir := filepath.Join(dir, "ext", ".git")
	initRepo(t, extGitDir, "[extensions]\n\tworktreeConfig = true\n")
	multiGitDir := filepath.Join(dir, "multi", ".git")
	initRepo(t, multiGitDir, "")
	addWorktree(t, multiGitDir, "wt", "other")
	linked := addWorktree(t, extGitDir, "wt", "other")

	exists := filepath.Join(dir, "exists")
	writeFile(t, exists, "")

	for _, tc := range []struct {
		name  string
		env   gitconfig.Env
		scope gitconfig.Scope
		want  string // "" for ErrNoScope
	}{
		{"file", gitconfig.Env{GitDir: gitDir}, gitconfig.ScopeFile, ""},
		{"command", gitconfig.Env{GitDir: gitDir}, gitconfig.ScopeCommand, ""},
		{"system", gitconfig.Env{SystemConfig: "/etc/gitconfig"}, gitconfig.ScopeSystem, "/etc/gitconfig"},
		{"no system", gitconfig.Env{}, gitconfig.ScopeSystem, ""},
		{"no global", gitconfig.Env{}, gitconfig.ScopeGlobal, ""},
		{"global, none exists: the last", gitconfig.Env{GlobalConfig: []string{xdg, home}}, gitconfig.ScopeGlobal, home},
		{"global, only the first exists", gitconfig.Env{GlobalConfig: []string{exists, home}}, gitconfig.ScopeGlobal, exists},
		{"global, only the last exists", gitconfig.Env{GlobalConfig: []string{xdg, exists}}, gitconfig.ScopeGlobal, exists},
		{"global, single", gitconfig.Env{GlobalConfig: []string{xdg}}, gitconfig.ScopeGlobal, xdg},
		{"local", gitconfig.Env{GitDir: gitDir}, gitconfig.ScopeLocal, filepath.Join(gitDir, "config")},
		{"local outside a repository", gitconfig.Env{}, gitconfig.ScopeLocal, ""},
		{"worktree with the extension", gitconfig.Env{GitDir: extGitDir}, gitconfig.ScopeWorktree, filepath.Join(extGitDir, "config.worktree")},
		{"worktree without the extension", gitconfig.Env{GitDir: gitDir}, gitconfig.ScopeWorktree, filepath.Join(gitDir, "config")},
		{"worktree without the extension, several worktrees", gitconfig.Env{GitDir: multiGitDir}, gitconfig.ScopeWorktree, ""},
		{"worktree outside a repository", gitconfig.Env{}, gitconfig.ScopeWorktree, ""},
		{"linked worktree, local", gitconfig.Env{GitDir: linked}, gitconfig.ScopeLocal, filepath.Join(extGitDir, "config")},
		{"linked worktree, worktree", gitconfig.Env{GitDir: linked}, gitconfig.ScopeWorktree, filepath.Join(linked, "config.worktree")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.env.WritePath(tc.scope)
			if tc.want == "" {
				assert.Equal(t, gitconfig.ErrNoScope, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, filepath.Clean(tc.want), filepath.Clean(got))
		})
	}
}

// unsetenv unsets an environment variable for the duration of the test.
func unsetenv(t *testing.T, key string) {
	t.Setenv(key, "")
	require.NoError(t, os.Unsetenv(key))
}

// cleanGitEnv removes every variable Discover reads and sets HOME to a new
// directory, which it returns.
func cleanGitEnv(t *testing.T) string {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			unsetenv(t, k)
		}
	}
	unsetenv(t, "XDG_CONFIG_HOME")
	home := tempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestDiscover(t *testing.T) {
	t.Run("default global files", func(t *testing.T) {
		home := cleanGitEnv(t)
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, home, env.Home)
		assert.Equal(t, []string{filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig")}, env.GlobalConfig)
		assert.Empty(t, env.GitDir)
		assert.Empty(t, env.Command)
	})

	t.Run("XDG_CONFIG_HOME", func(t *testing.T) {
		home := cleanGitEnv(t)
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(xdg, "git", "config"), filepath.Join(home, ".gitconfig")}, env.GlobalConfig)
	})

	t.Run("no home directory", func(t *testing.T) {
		// As git: no ~/.gitconfig, the XDG file only if XDG_CONFIG_HOME
		// says where it is.
		cleanGitEnv(t)
		unsetenv(t, "HOME")
		unsetenv(t, "USERPROFILE")
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Empty(t, env.Home)
		assert.Empty(t, env.GlobalConfig)

		xdg := tempDir(t)
		t.Setenv("XDG_CONFIG_HOME", xdg)
		env, err = gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(xdg, "git", "config")}, env.GlobalConfig)
	})

	t.Run("GIT_CONFIG_GLOBAL", func(t *testing.T) {
		home := cleanGitEnv(t)
		t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "g"))
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(home, "g")}, env.GlobalConfig)
	})

	t.Run("GIT_CONFIG_SYSTEM", func(t *testing.T) {
		home := cleanGitEnv(t)
		t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "s"))
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, "s"), env.SystemConfig)
	})

	t.Run("GIT_CONFIG_NOSYSTEM", func(t *testing.T) {
		home := cleanGitEnv(t)
		t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "s"))
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Empty(t, env.SystemConfig)
	})

	t.Run("command line, GIT_CONFIG_COUNT before GIT_CONFIG_PARAMETERS", func(t *testing.T) {
		cleanGitEnv(t)
		t.Setenv("GIT_CONFIG_COUNT", "2")
		t.Setenv("GIT_CONFIG_KEY_0", "a.k")
		t.Setenv("GIT_CONFIG_VALUE_0", "count0")
		t.Setenv("GIT_CONFIG_KEY_1", "a.j")
		t.Setenv("GIT_CONFIG_VALUE_1", "")
		t.Setenv("GIT_CONFIG_PARAMETERS", "'a.k'='param' 'a.flag'")
		env, err := gitconfig.Discover("")
		require.NoError(t, err)
		assert.Equal(t, []gitconfig.CommandEntry{
			{Key: "a.k", Value: "count0"},
			{Key: "a.j", Value: ""},
			{Key: "a.k", Value: "param"},
			{Key: "a.flag", Implicit: true},
		}, env.Command)
	})

	t.Run("GIT_CONFIG_COUNT", func(t *testing.T) {
		for _, tc := range []struct {
			count string
			want  int
		}{
			{"", 0},
			{"0", 0},
			{"1", 1},
		} {
			t.Run(strconv.Quote(tc.count), func(t *testing.T) {
				cleanGitEnv(t)
				t.Setenv("GIT_CONFIG_COUNT", tc.count)
				t.Setenv("GIT_CONFIG_KEY_0", "a.k")
				t.Setenv("GIT_CONFIG_VALUE_0", "v")
				env, err := gitconfig.Discover("")
				require.NoError(t, err)
				assert.Len(t, env.Command, tc.want)
			})
		}
	})

	t.Run("malformed command line", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			vars map[string]string
		}{
			{"bogus count", map[string]string{"GIT_CONFIG_COUNT": "x"}},
			{"negative count", map[string]string{"GIT_CONFIG_COUNT": "-1"}},
			{"sign only", map[string]string{"GIT_CONFIG_COUNT": "+"}},
			{"space only", map[string]string{"GIT_CONFIG_COUNT": " "}},
			// Accepted by git.
			{"plus sign", map[string]string{"GIT_CONFIG_COUNT": "+1", "GIT_CONFIG_KEY_0": "a.k", "GIT_CONFIG_VALUE_0": "v"}},
			{"leading space", map[string]string{"GIT_CONFIG_COUNT": " 1", "GIT_CONFIG_KEY_0": "a.k", "GIT_CONFIG_VALUE_0": "v"}},
			{"negative zero", map[string]string{"GIT_CONFIG_COUNT": "-0"}},
			{"too many", map[string]string{"GIT_CONFIG_COUNT": "2147483648"}},
			{"missing key", map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_VALUE_0": "v"}},
			{"missing value", map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "a.k"}},
			{"bad parameters", map[string]string{"GIT_CONFIG_PARAMETERS": "'a.k"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cleanGitEnv(t)
				for k, v := range tc.vars {
					t.Setenv(k, v)
				}
				_, err := gitconfig.Discover("")
				require.Error(t, err)
			})
		}
	})

	t.Run("relative gitDir made absolute", func(t *testing.T) {
		home := cleanGitEnv(t)
		initRepo(t, filepath.Join(home, "repo", ".git"), "")
		t.Chdir(filepath.Join(home, "repo"))
		want, err := filepath.Abs(".git")
		require.NoError(t, err)

		env, err := gitconfig.Discover(".git")
		require.NoError(t, err)
		assert.Equal(t, want, env.GitDir)
	})

	t.Run("gitdir file", func(t *testing.T) {
		home := cleanGitEnv(t)
		gitDir := filepath.Join(home, "main", ".git")
		initRepo(t, gitDir, "")
		wt := addWorktree(t, gitDir, "wt", "other")

		writeFile(t, filepath.Join(home, "wt", ".git"), "gitdir: "+wt+"\n")
		env, err := gitconfig.Discover(filepath.Join(home, "wt", ".git"))
		require.NoError(t, err)
		assert.Equal(t, wt, env.GitDir)

		writeFile(t, filepath.Join(home, "wt2", ".git"), "gitdir: ../main/.git/worktrees/wt\n")
		env, err = gitconfig.Discover(filepath.Join(home, "wt2", ".git"))
		require.NoError(t, err)
		assert.Equal(t, wt, filepath.Clean(env.GitDir))
	})

	t.Run("malformed gitdir file", func(t *testing.T) {
		home := cleanGitEnv(t)
		writeFile(t, filepath.Join(home, "wt", ".git"), "garbage\n")
		_, err := gitconfig.Discover(filepath.Join(home, "wt", ".git"))
		require.Error(t, err)
	})

	t.Run("loads like git", func(t *testing.T) {
		home := cleanGitEnv(t)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		writeFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = global\n")
		gitDir := filepath.Join(home, "repo", ".git")
		initRepo(t, gitDir, "[user]\n\temail = local\n")
		t.Setenv("GIT_CONFIG_PARAMETERS", "'core.editor'='vi'")

		env, err := gitconfig.Discover(gitDir)
		require.NoError(t, err)
		c, err := env.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"user.name=global", "user.email=local", "core.editor=vi"}, list(slices.Collect(c.All())))
	})
}

func TestEnvUpdateFile(t *testing.T) {
	var env gitconfig.Env // UpdateFile uses nothing from the Env

	t.Run("edit", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "# keep\n[a]\n\tk = v\n")
		before := statNow(t, path)

		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			e, ok := f.Config().Get("a.k")
			require.True(t, ok)
			assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeFile, File: path, Line: 3}, e.Origin)
			return f.Set("a.j", "w")
		})
		require.NoError(t, err)
		assert.Equal(t, "# keep\n[a]\n\tk = v\n\tj = w\n", readFile(t, path))
		assert.NoFileExists(t, path+".lock")

		after, err := os.Stat(path)
		require.NoError(t, err)
		assert.False(t, os.SameFile(before, after), "the file is replaced through a rename")
	})

	t.Run("missing file created", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			assert.Empty(t, f.Bytes())
			return f.Set("a.k", "v")
		})
		require.NoError(t, err)
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
	})

	t.Run("unchanged content not written", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n    k=v\n")
		before := statNow(t, path)

		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			return f.Set("a.k", "v")
		})
		require.NoError(t, err)
		after, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, os.SameFile(before, after))
		assert.Equal(t, before.ModTime(), after.ModTime())
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("edits undone are not written", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		before := statNow(t, path)

		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			if err := f.Set("a.j", "w"); err != nil {
				return err
			}
			_, err := f.Unset("a.j")
			return err
		})
		require.NoError(t, err)
		after, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, os.SameFile(before, after))
	})

	t.Run("missing file not created without edit", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		require.NoError(t, env.UpdateFile(path, func(f *gitconfig.File) error { return nil }))
		assert.NoFileExists(t, path)
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("callback error returned as is", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		sentinel := errors.New("sentinel")

		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			require.NoError(t, f.Set("a.k", "w"))
			return sentinel
		})
		assert.True(t, err == sentinel, "got %v", err)
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("callback panic", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")

		assert.PanicsWithValue(t, "boom", func() {
			_ = env.UpdateFile(path, func(f *gitconfig.File) error {
				require.NoError(t, f.Set("a.k", "w"))
				panic("boom")
			})
		})
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("locked", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		writeFile(t, path+".lock", "someone else")

		called := false
		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			called = true
			return nil
		})
		require.ErrorIs(t, err, gitconfig.ErrLocked)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
		assert.Equal(t, path+".lock", fe.Path)
		assert.False(t, called)
		assert.Equal(t, "someone else", readFile(t, path+".lock"), "a held lock is never removed")
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
	})

	t.Run("syntax error", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[broken\n")

		called := false
		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			called = true
			return nil
		})
		var se *gitconfig.SyntaxError
		require.ErrorAs(t, err, &se)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
		assert.Equal(t, path, fe.Path)
		assert.False(t, called)
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("not a regular file", func(t *testing.T) {
		path := tempDir(t)
		err := env.UpdateFile(path, func(f *gitconfig.File) error { return nil })
		require.ErrorIs(t, err, gitconfig.ErrNotRegular)
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("closed after the callback", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		var kept *gitconfig.File
		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			kept = f
			return f.Set("a.k", "v")
		})
		require.NoError(t, err)

		require.ErrorIs(t, kept.Set("a.k", "w"), gitconfig.ErrClosed)
		require.ErrorIs(t, kept.Add("a.k", "w"), gitconfig.ErrClosed)
		require.ErrorIs(t, kept.ReplaceAll("a.k", "w"), gitconfig.ErrClosed)
		_, err = kept.Unset("a.k")
		require.ErrorIs(t, err, gitconfig.ErrClosed)
		_, err = kept.UnsetAll("a.k")
		require.ErrorIs(t, err, gitconfig.ErrClosed)
		_, err = kept.RemoveSection("a")
		require.ErrorIs(t, err, gitconfig.ErrClosed)
		_, err = kept.RemoveSubsection("a", "b")
		require.ErrorIs(t, err, gitconfig.ErrClosed)
		require.ErrorIs(t, kept.RenameSection("a", "b"), gitconfig.ErrClosed)
		require.ErrorIs(t, kept.RenameSubsection("a", "b", "c"), gitconfig.ErrClosed)

		assert.Equal(t, "[a]\n\tk = v\n", string(kept.Bytes()))
		assert.Equal(t, "v", kept.Config().Value("a.k", ""))
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
	})

	t.Run("closed after an error too", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		var kept *gitconfig.File
		_ = env.UpdateFile(path, func(f *gitconfig.File) error {
			kept = f
			return errors.New("fail")
		})
		require.ErrorIs(t, kept.Set("a.k", "w"), gitconfig.ErrClosed)
	})

	t.Run("includes not followed", func(t *testing.T) {
		dir := tempDir(t)
		path := filepath.Join(dir, "config")
		writeFile(t, path, "[include]\n\tpath = inc\n")
		writeFile(t, filepath.Join(dir, "inc"), "[a]\n\tk = v\n")
		err := env.UpdateFile(path, func(f *gitconfig.File) error {
			_, ok := f.Config().Get("a.k")
			assert.False(t, ok)
			return nil
		})
		require.NoError(t, err)
	})

	t.Run("concurrent updates lose nothing", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		const writers, adds = 8, 10

		var wg sync.WaitGroup
		for w := range writers {
			wg.Go(func() {
				for i := range adds {
					for {
						err := env.UpdateFile(path, func(f *gitconfig.File) error {
							return f.Add("a.k", fmt.Sprintf("%d-%d", w, i))
						})
						if errors.Is(err, gitconfig.ErrLocked) {
							continue
						}
						assert.NoError(t, err)
						break
					}
				}
			})
		}
		wg.Wait()

		c := mustConfig(t, readFile(t, path))
		got := values(c.GetAll("a.k"))
		assert.Len(t, got, writers*adds)
		for w := range writers {
			for i := range adds {
				assert.Contains(t, got, fmt.Sprintf("%d-%d", w, i))
			}
		}
		assert.NoFileExists(t, path+".lock")
	})
}

// statNow returns the FileInfo of path with the file's identity read now.
// On Windows, os.Stat leaves it to be read by os.SameFile, through the path,
// which by then may name another file.
func statNow(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	os.SameFile(fi, fi)
	return fi
}

// symlink creates a symbolic link, or skips the test where the platform or
// the user cannot (Windows without the privilege).
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symbolic links not supported:", err)
	}
}

func TestEnvUpdateFileSymlinks(t *testing.T) {
	set := func(f *gitconfig.File) error { return f.Set("a.k", "w") }

	assertLink := func(t *testing.T, link, target string) {
		t.Helper()
		got, err := os.Readlink(link)
		require.NoError(t, err)
		// os.Symlink writes the target with the platform's separators.
		assert.Equal(t, filepath.FromSlash(target), got)
	}

	t.Run("link followed and kept", func(t *testing.T) {
		dir := tempDir(t)
		target := filepath.Join(dir, "real")
		link := filepath.Join(dir, "link")
		writeFile(t, target, "[a]\n\tk = v\n")
		symlink(t, "real", link)

		require.NoError(t, gitconfig.Env{}.UpdateFile(link, set))
		assert.Equal(t, "[a]\n\tk = w\n", readFile(t, target))
		assertLink(t, link, "real")
		assert.NoFileExists(t, target+".lock")
		assert.NoFileExists(t, link+".lock")
	})

	t.Run("chain with relative targets", func(t *testing.T) {
		dir := tempDir(t)
		target := filepath.Join(dir, "real")
		writeFile(t, target, "[a]\n\tk = v\n")
		require.NoError(t, os.Mkdir(filepath.Join(dir, "d"), 0o755))
		symlink(t, "../real", filepath.Join(dir, "d", "l2"))
		symlink(t, "d/l2", filepath.Join(dir, "l1"))

		require.NoError(t, gitconfig.Env{}.UpdateFile(filepath.Join(dir, "l1"), set))
		assert.Equal(t, "[a]\n\tk = w\n", readFile(t, target))
		assertLink(t, filepath.Join(dir, "l1"), "d/l2")
		assertLink(t, filepath.Join(dir, "d", "l2"), "../real")
	})

	t.Run("relative target through a directory link", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows removes \"..\" from a path before following links")
		}
		// cfg/link -> d/../real, with cfg/d -> ../store/sub: the ".." is
		// that of store/sub, so the file is store/real, not cfg/real.
		dir := tempDir(t)
		target := filepath.Join(dir, "store", "real")
		decoy := filepath.Join(dir, "cfg", "real")
		writeFile(t, target, "[a]\n\tk = v\n")
		writeFile(t, decoy, "[a]\n\tk = v\n")
		require.NoError(t, os.Mkdir(filepath.Join(dir, "store", "sub"), 0o755))
		symlink(t, "../store/sub", filepath.Join(dir, "cfg", "d"))
		symlink(t, "d/../real", filepath.Join(dir, "cfg", "link"))

		require.NoError(t, gitconfig.Env{}.UpdateFile(filepath.Join(dir, "cfg", "link"), set))
		assert.Equal(t, "[a]\n\tk = w\n", readFile(t, target))
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, decoy))
	})

	t.Run("dangling link creates the target", func(t *testing.T) {
		dir := tempDir(t)
		link := filepath.Join(dir, "link")
		symlink(t, "new", link)

		require.NoError(t, gitconfig.Env{}.UpdateFile(link, set))
		assert.Equal(t, "[a]\n\tk = w\n", readFile(t, filepath.Join(dir, "new")))
		assertLink(t, link, "new")
	})

	t.Run("loop", func(t *testing.T) {
		dir := tempDir(t)
		symlink(t, "l2", filepath.Join(dir, "l1"))
		symlink(t, "l1", filepath.Join(dir, "l2"))

		called := false
		err := gitconfig.Env{}.UpdateFile(filepath.Join(dir, "l1"), func(f *gitconfig.File) error {
			called = true
			return nil
		})
		require.Error(t, err)
		assert.False(t, called)
	})

	t.Run("lock file is a symbolic link", func(t *testing.T) {
		for _, existing := range []bool{false, true} {
			t.Run(map[bool]string{false: "to nothing", true: "to a file"}[existing], func(t *testing.T) {
				dir := tempDir(t)
				path := filepath.Join(dir, "config")
				victim := filepath.Join(dir, "victim")
				writeFile(t, path, "[a]\n\tk = v\n")
				if existing {
					writeFile(t, victim, "precious")
				}
				symlink(t, victim, path+".lock")

				err := gitconfig.Env{}.UpdateFile(path, set)
				require.ErrorIs(t, err, gitconfig.ErrLocked)
				if existing {
					assert.Equal(t, "precious", readFile(t, victim))
				} else {
					assert.NoFileExists(t, victim)
				}
				assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
			})
		}
	})
}

func TestEnvUpdateScope(t *testing.T) {
	t.Run("writes to the scope's file, not into includes", func(t *testing.T) {
		env := fullEnv(t)
		home := env.GlobalConfig[1]
		inc := filepath.Join(filepath.Dir(home), "inc")
		writeFile(t, home, "[include]\n\tpath = inc\n")
		writeFile(t, inc, "[user]\n\tname = inc\n")

		err := env.UpdateScope(gitconfig.ScopeGlobal, func(f *gitconfig.File) error {
			return f.Set("user.name", "x")
		})
		require.NoError(t, err)
		assert.Equal(t, "[include]\n\tpath = inc\n[user]\n\tname = x\n", readFile(t, home))
		assert.Equal(t, "[user]\n\tname = inc\n", readFile(t, inc))
		assert.Equal(t, "[a]\n\tk = xdg\n\txdgonly = x\n", readFile(t, env.GlobalConfig[0]))
	})

	t.Run("entries have the scope", func(t *testing.T) {
		env := fullEnv(t)
		for _, tc := range []struct {
			scope gitconfig.Scope
			want  string
		}{
			{gitconfig.ScopeSystem, "system"},
			{gitconfig.ScopeGlobal, "home"},
			{gitconfig.ScopeLocal, "local"},
			{gitconfig.ScopeWorktree, "worktree"},
		} {
			t.Run(tc.scope.String(), func(t *testing.T) {
				err := env.UpdateScope(tc.scope, func(f *gitconfig.File) error {
					e, ok := f.Config().Get("a.k")
					require.True(t, ok)
					assert.Equal(t, tc.want, e.Value)
					assert.Equal(t, tc.scope, e.Origin.Scope)
					return nil
				})
				require.NoError(t, err)
			})
		}
	})

	t.Run("worktree without the extension", func(t *testing.T) {
		env := fullEnv(t)
		local := filepath.Join(env.GitDir, "config")
		writeFile(t, local, "[a]\n\tk = local\n")

		err := env.UpdateScope(gitconfig.ScopeWorktree, func(f *gitconfig.File) error {
			e, _ := f.Config().Get("a.k")
			assert.Equal(t, gitconfig.ScopeLocal, e.Origin.Scope)
			return f.Set("a.j", "w")
		})
		require.NoError(t, err)
		assert.Equal(t, "[a]\n\tk = local\n\tj = w\n", readFile(t, local))
	})

	t.Run("global file created", func(t *testing.T) {
		dir := tempDir(t)
		env := gitconfig.Env{GlobalConfig: []string{filepath.Join(dir, "xdg", "git", "config"), filepath.Join(dir, ".gitconfig")}}
		err := env.UpdateScope(gitconfig.ScopeGlobal, func(f *gitconfig.File) error {
			return f.Set("user.name", "x")
		})
		require.NoError(t, err)
		assert.Equal(t, "[user]\n\tname = x\n", readFile(t, filepath.Join(dir, ".gitconfig")))
		assert.NoFileExists(t, env.GlobalConfig[0])
	})

	t.Run("unavailable scopes", func(t *testing.T) {
		env := fullEnv(t)
		for _, s := range []gitconfig.Scope{gitconfig.ScopeFile, gitconfig.ScopeCommand} {
			called := false
			err := env.UpdateScope(s, func(f *gitconfig.File) error {
				called = true
				return nil
			})
			assert.Equal(t, gitconfig.ErrNoScope, err)
			assert.False(t, called)
		}
	})
}

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

// TestEnvUnreadable checks that a global file that cannot be
// read for lack of permission is skipped, any other one is an error.
func TestEnvUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 does not make a file unreadable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	dir := tempDir(t)
	unreadable := filepath.Join(dir, "unreadable")
	writeFile(t, unreadable, "[a]\n\tk = v\n")
	require.NoError(t, os.Chmod(unreadable, 0))
	readable := filepath.Join(dir, "readable")
	writeFile(t, readable, "[a]\n\tj = w\n")

	t.Run("global file skipped", func(t *testing.T) {
		env := gitconfig.Env{GlobalConfig: []string{unreadable, readable}}
		c, err := env.Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"a.j=w"}, list(slices.Collect(c.All())))

		c, err = env.LoadProtected()
		require.NoError(t, err)
		assert.Equal(t, []string{"a.j=w"}, list(slices.Collect(c.All())))

		// As `git config --global --get`, which reads it as empty.
		c, err = env.LoadScope(gitconfig.ScopeGlobal)
		require.NoError(t, err)
		assert.Equal(t, []string{"a.j=w"}, list(slices.Collect(c.All())))
	})

	t.Run("system file skipped by LoadProtected only", func(t *testing.T) {
		env := gitconfig.Env{SystemConfig: unreadable}
		_, err := env.Load()
		require.ErrorIs(t, err, fs.ErrPermission)

		c, err := env.LoadProtected()
		require.NoError(t, err)
		assert.Empty(t, slices.Collect(c.All()))
	})

	t.Run("include is an error", func(t *testing.T) {
		global := filepath.Join(dir, "global")
		writeFile(t, global, "[include]\n\tpath = unreadable\n")
		_, err := gitconfig.Env{GlobalConfig: []string{global}}.Load()
		require.ErrorIs(t, err, fs.ErrPermission)
	})

	t.Run("file is an error", func(t *testing.T) {
		_, err := gitconfig.Env{}.LoadFile(unreadable)
		require.ErrorIs(t, err, fs.ErrPermission)
	})
}

func TestEnvFIFO(t *testing.T) {
	// Windows has no FIFO to open by name. The mkfifo of MSYS2, which Git
	// for Windows puts on PATH, makes a file only MSYS programs take for a
	// FIFO: Go reads it as a regular file.
	if runtime.GOOS == "windows" {
		t.Skip("no FIFO to open by name on Windows")
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("no mkfifo: no FIFO to open by name")
	}
	dir := tempDir(t)
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, exec.Command("mkfifo", fifo).Run())

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

// TestEnvUpdateFilePermissions checks that the permissions of the file are
// kept exactly, whatever the umask: with any umask, the lock file is created
// with fewer bits than 0666.
func TestEnvUpdateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions on Windows")
	}
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

	t.Run("lock private until written", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		require.NoError(t, os.Chmod(path, 0o644))

		err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
			fi, err := os.Stat(path + ".lock")
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
			return f.Set("a.k", "w")
		})
		require.NoError(t, err)
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	})

	t.Run("new file", func(t *testing.T) {
		// As any new file: 0666 minus the umask.
		dir := tempDir(t)
		ref, err := os.OpenFile(filepath.Join(dir, "ref"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		require.NoError(t, err)
		require.NoError(t, ref.Close())
		want, err := os.Stat(filepath.Join(dir, "ref"))
		require.NoError(t, err)

		path := filepath.Join(dir, "config")
		err = gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
			return f.Set("a.k", "v")
		})
		require.NoError(t, err)
		fi, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, want.Mode().Perm(), fi.Mode().Perm())
	})
}

// TestEnvUpdateFileHeld checks that UpdateFile waits for a file another
// process holds for a moment, and gives up if it holds it for long. On
// Windows, a file open without FILE_SHARE_DELETE, as os.Open opens it,
// cannot be replaced. A file opened without sharing at all cannot be read
// either; that error is checked by TestRetryTransient.
func TestEnvUpdateFileHeld(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to replace an open file")
	}
	set := func(f *gitconfig.File) error { return f.Set("a.k", "w") }
	hold := func(t *testing.T, path string) *os.File {
		f, err := os.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() { f.Close() })
		return f
	}

	t.Run("released", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		f := hold(t, path)
		time.AfterFunc(200*time.Millisecond, func() { f.Close() })

		require.NoError(t, gitconfig.Env{}.UpdateFile(path, set))
		assert.Equal(t, "[a]\n\tk = w\n", readFile(t, path))
		assert.NoFileExists(t, path+".lock")
	})

	t.Run("kept", func(t *testing.T) {
		path := filepath.Join(tempDir(t), "config")
		writeFile(t, path, "[a]\n\tk = v\n")
		hold(t, path)

		start := time.Now()
		err := gitconfig.Env{}.UpdateFile(path, set)
		var fe *gitconfig.FileError
		require.ErrorAs(t, err, &fe)
		assert.NotErrorIs(t, err, gitconfig.ErrLocked)
		assert.GreaterOrEqual(t, time.Since(start), time.Second, "given up without retrying")
		assert.Equal(t, "[a]\n\tk = v\n", readFile(t, path))
		assert.NoFileExists(t, path+".lock")
	})
}

// benchEnv is a realistic Env: a system file, global files with an include
// and an includeIf, a local config with many remotes and command entries.
func benchEnv(b *testing.B) gitconfig.Env {
	dir := tempDir(b)
	home := filepath.Join(dir, "home")
	gitDir := filepath.Join(dir, "work", "proj", ".git")
	writeFile(b, filepath.Join(dir, "etc", "gitconfig"), "[core]\n\tautocrlf = input\n")
	writeFile(b, filepath.Join(home, ".gitconfig"), fmt.Sprintf(
		"[user]\n\tname = Someone\n[include]\n\tpath = ~/.gitconfig.d/aliases\n[includeIf \"gitdir:%s/work/\"]\n\tpath = ~/.gitconfig.d/work\n",
		filepath.ToSlash(dir)))
	writeFile(b, filepath.Join(home, ".gitconfig.d", "aliases"), strings.Repeat("[alias]\n\tco = checkout\n\tst = status\n", 20))
	writeFile(b, filepath.Join(home, ".gitconfig.d", "work"), "[user]\n\temail = someone@work\n")
	initRepo(b, gitDir, bigConfig(50))
	return gitconfig.Env{
		Home:         home,
		GitDir:       gitDir,
		SystemConfig: filepath.Join(dir, "etc", "gitconfig"),
		GlobalConfig: []string{filepath.Join(home, ".config", "git", "config"), filepath.Join(home, ".gitconfig")},
		Command:      []gitconfig.CommandEntry{{Key: "core.pager", Value: "less"}},
	}
}

func BenchmarkEnv(b *testing.B) {
	env := benchEnv(b)

	b.Run("Load", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = env.Load()
		}
	})
	b.Run("LoadProtected", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = env.LoadProtected()
		}
	})
	b.Run("LoadFileManyGitdirConditions", func(b *testing.B) {
		// The git directory's real path is resolved once per load, not
		// once per condition.
		path := filepath.Join(tempDir(b), "config")
		writeFile(b, path, strings.Repeat("[includeIf \"gitdir:/nope/\"]\n\tpath = x\n", 1000))
		b.ReportAllocs()
		for b.Loop() {
			_, _ = env.LoadFile(path)
		}
	})
	b.Run("UpdateFile", func(b *testing.B) {
		path := filepath.Join(env.GitDir, "config")
		i := 0
		for b.Loop() {
			i++
			_ = env.UpdateFile(path, func(f *gitconfig.File) error {
				return f.Set("core.editor", strconv.Itoa(i))
			})
		}
	})
	b.Run("UpdateFileUnchanged", func(b *testing.B) {
		path := filepath.Join(env.GitDir, "config")
		for b.Loop() {
			_ = env.UpdateFile(path, func(f *gitconfig.File) error {
				return f.Set("user.name", "Someone")
			})
		}
	})
}
