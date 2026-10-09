package gitconfig_test

// Interoperability with git: the same input given to `git config` and to
// this package must give the same entries, the same bytes after an edit, and
// the same failures. Skipped with -short or when git is not installed.

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

var gitVersion = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("git", "--version").Output()
	return strings.TrimSpace(string(out)), err
})

func requireGit(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("git interop skipped with -short")
	}
	v, err := gitVersion()
	if err != nil {
		t.Skip("git not available:", err)
	}
	t.Log(v)
}

type gitResult struct {
	stdout, stderr string
	code           int
}

func runGit(t testing.TB, dir string, env []string, args ...string) gitResult {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee, "running git %q", args)
		code = ee.ExitCode()
	}
	return gitResult{stdout.String(), stderr.String(), code}
}

// processEnv is the environment of this process, with git's messages in
// English.
func processEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
}

// isolatedGit runs git in a directory of its own, reading no configuration
// but the files it is given: no GIT_* variable, an empty home, no system
// file.
type isolatedGit struct {
	t   testing.TB
	dir string
	env []string
}

func newIsolatedGit(t testing.TB) *isolatedGit {
	t.Helper()
	requireGit(t)
	dir := tempDir(t)
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "GIT_"), strings.HasPrefix(k, "LC_"),
			k == "HOME", k == "XDG_CONFIG_HOME", k == "LANG", k == "LANGUAGE":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+dir,
		"XDG_CONFIG_HOME="+filepath.Join(dir, ".config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir),
		"LC_ALL=C",
		"LANGUAGE=C",
	)
	return &isolatedGit{t: t, dir: dir, env: env}
}

func (g *isolatedGit) run(args ...string) gitResult {
	g.t.Helper()
	return runGit(g.t, g.dir, g.env, args...)
}

// write writes content to a fresh file and returns its path.
func (g *isolatedGit) write(content string) string {
	g.t.Helper()
	f, err := os.CreateTemp(g.dir, "config")
	require.NoError(g.t, err)
	_, err = f.WriteString(content)
	require.NoError(g.t, err)
	require.NoError(g.t, f.Close())
	return f.Name()
}

// splitZ splits the output of `git config -z --list` into records of n
// fields each.
func splitZ(t testing.TB, out string, n int) [][]string {
	t.Helper()
	if out == "" {
		return nil
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	require.Zero(t, len(fields)%n, "malformed git output %q", out)
	var records [][]string
	for i := 0; i < len(fields); i += n {
		records = append(records, fields[i:i+n])
	}
	return records
}

// gitKeyValue renders "key\nvalue" as list does: "key=value", or "key" for
// an implicit value.
func gitKeyValue(kv string) string {
	k, v, ok := strings.Cut(kv, "\n")
	if !ok {
		return k
	}
	return k + "=" + v
}

var badLine = regexp.MustCompile(`bad config line (\d+)`)

// gitList returns the entries git reads from content, without includes, or
// the line of the syntax error git reports.
func (g *isolatedGit) list(content string) (entries []string, errLine int) {
	g.t.Helper()
	res := g.run("config", "-f", g.write(content), "--list", "-z")
	if res.code != 0 {
		m := badLine.FindStringSubmatch(res.stderr)
		require.NotNil(g.t, m, "git failed without a line number: %s", res.stderr)
		line, err := strconv.Atoi(m[1])
		require.NoError(g.t, err)
		return nil, line
	}
	for _, r := range splitZ(g.t, res.stdout, 1) {
		entries = append(entries, gitKeyValue(r[0]))
	}
	return entries, 0
}

// assertSamePath compares paths up to their separators: git for Windows
// writes some paths with '/' (it converts HOME, for one) where this package
// keeps the caller's spelling. A no-op elsewhere.
func assertSamePath(t testing.TB, want, got string, msgAndArgs ...any) {
	t.Helper()
	assert.Equal(t, filepath.ToSlash(want), filepath.ToSlash(got), msgAndArgs...)
}

// checkParse checks that Parse reads content as git does.
func checkParse(t *testing.T, g *isolatedGit, content string) {
	t.Helper()
	want, errLine := g.list(content)
	f, err := gitconfig.Parse("f", []byte(content))
	if errLine != 0 {
		var se *gitconfig.SyntaxError
		require.ErrorAs(t, err, &se, "git rejects %q at line %d", content, errLine)
		assert.Equal(t, errLine, se.Line, "line of the syntax error in %q", content)
		return
	}
	require.NoError(t, err, "git accepts %q", content)
	// An entry before any header has a key with no section, which no git
	// command can ask for; Config leaves it out.
	want = slices.DeleteFunc(want, func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return !strings.Contains(k, ".")
	})
	if len(want) == 0 {
		want = nil
	}
	assert.Equal(t, want, list(slices.Collect(f.Config().All())), "entries of %q", content)
}

func TestGitParse(t *testing.T) {
	g := newIsolatedGit(t)

	corpus := slices.Clone(roundTripCases)
	corpus = append(corpus, parseSeeds...)
	for _, tc := range parseValueCases {
		corpus = append(corpus, tc.data)
	}
	for _, tc := range syntaxErrorCases {
		corpus = append(corpus, tc.data)
	}
	for _, tc := range slices.Concat(allEditCases(), gitDifferenceCases) {
		corpus = append(corpus, tc.before, tc.after)
	}
	corpus = append(corpus, editErrorData)

	t.Run("corpus", func(t *testing.T) {
		for _, content := range corpus {
			t.Run(strconv.Quote(content), func(t *testing.T) {
				checkParse(t, g, content)
			})
		}
	})

	t.Run("random", func(t *testing.T) {
		r := rand.New(rand.NewPCG(1, 2))
		for i := range 300 {
			content := randomFile(r, true, true)
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				checkParse(t, g, content)
			})
		}
	})
}

// gitQuote writes value so that git reads it back unchanged.
func gitQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(value) + `"`
}

func TestGitTypes(t *testing.T) {
	g := newIsolatedGit(t)

	// get returns what `git config --type=<typ>` gives for a.k in content,
	// and whether it succeeded.
	get := func(typ, content string) (string, bool) {
		res := g.run("config", "-f", g.write(content), "--type="+typ, "--get", "a.k")
		return strings.TrimSuffix(res.stdout, "\n"), res.code == 0
	}

	type input struct {
		name    string
		content string
		entry   gitconfig.Entry
	}
	inputs := func(values []string) []input {
		in := []input{{"implicit", "[a]\n\tk\n", gitconfig.Entry{Implicit: true}}}
		for _, v := range values {
			in = append(in, input{strconv.Quote(v), "[a]\n\tk = " + gitQuote(v) + "\n", gitconfig.Entry{Value: v}})
		}
		return in
	}

	t.Run("bool", func(t *testing.T) {
		values := slices.Clone(boolErrorCases)
		for _, tc := range boolCases {
			values = append(values, tc.value)
		}
		for _, in := range inputs(values) {
			t.Run(in.name, func(t *testing.T) {
				want, ok := get("bool", in.content)
				got, err := in.entry.Bool()
				if !ok {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, want, strconv.FormatBool(got))
			})
		}
	})

	t.Run("int", func(t *testing.T) {
		var values []string
		for _, tc := range intCases {
			values = append(values, tc.value)
		}
		for _, tc := range intErrorCases {
			if !tc.entry.Implicit {
				values = append(values, tc.entry.Value)
			}
		}
		for _, in := range inputs(values) {
			t.Run(in.name, func(t *testing.T) {
				want, ok := get("int", in.content)
				got, err := in.entry.Int()
				if !ok {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, want, strconv.FormatInt(got, 10))
			})
		}
	})

	t.Run("path", func(t *testing.T) {
		var values []string
		for _, tc := range pathCases {
			values = append(values, tc.value)
		}
		// "%(prefix)/" is left out: git expands it to its installation
		// directory, which this package refuses to guess (C9).
		for _, in := range inputs(values) {
			t.Run(in.name, func(t *testing.T) {
				want, ok := get("path", in.content)
				got, err := in.entry.Path(g.dir)
				if !ok {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				assertSamePath(t, want, got)
			})
		}
	})
}

// checkEdits applies edits to before with File and with `git config -f`,
// one at a time, and checks that both succeed or fail alike and produce the
// same bytes. The one allowed difference is E7: setting a value that is
// already set leaves our file unchanged where git rewrites the line; the
// entries must then be the same, and git's file is reset to ours.
func checkEdits(t *testing.T, g *isolatedGit, before string, edits []edit) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("before: %q\nedits: %v", before, edits)
		}
	})

	path := g.write(before)
	f, err := gitconfig.Parse("f", []byte(before))
	require.NoError(t, err)

	for _, e := range edits {
		prev := slices.Clone(f.Bytes())
		ours := e.apply(f)
		res := g.run(append([]string{"config", "-f", path}, e.gitArgs()...)...)
		theirs := readFile(t, path)

		if res.code != 0 {
			require.Error(t, ours, "%v: git fails (%d): %s", e, res.code, res.stderr)
			require.Equal(t, theirs, string(f.Bytes()), "%v: a failed edit changed the file", e)
			return
		}
		require.NoError(t, ours, "%v: git succeeds", e)

		if theirs != string(f.Bytes()) {
			require.True(t, e.op == "" && bytes.Equal(prev, f.Bytes()),
				"%v:\n git: %q\nours: %q", e, theirs, f.Bytes())
			assert.Equal(t, list(slices.Collect(mustConfig(t, theirs).All())), list(slices.Collect(f.Config().All())),
				"%v: git rewrote the line to a different value", e)
			writeFile(t, path, string(f.Bytes()))
		}
	}
}

func TestGitEdit(t *testing.T) {
	g := newIsolatedGit(t)

	t.Run("cases", func(t *testing.T) {
		for _, tc := range allEditCases() {
			t.Run(tc.name, func(t *testing.T) {
				checkEdits(t, g, tc.before, tc.edits)
			})
		}
	})

	t.Run("quoting", func(t *testing.T) {
		for _, tc := range quotingCases {
			t.Run(strconv.Quote(tc.value), func(t *testing.T) {
				checkEdits(t, g, "[a]\n", edits(set("a.k", tc.value)))
			})
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, tc := range editErrorCases {
			if !gitRejects(tc.edit) {
				continue
			}
			t.Run(tc.name, func(t *testing.T) {
				checkEdits(t, g, editErrorData, edits(tc.edit))
			})
		}
	})

	t.Run("random", func(t *testing.T) {
		r := rand.New(rand.NewPCG(3, 4))
		for i := range 400 {
			before, es := randomEditCase(r)
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				if _, errLine := g.list(before); errLine != 0 {
					t.Skip("git rejects the file")
				}
				checkEdits(t, g, before, es)
			})
		}
	})
}

// gitRejects reports whether git fails on an edit of editErrorCases as this
// package does. A NUL cannot be passed to git, and git renames a section to
// a subsection containing a newline, writing a header it cannot parse back
// (X1). git also writes a key with an empty section, under [ "sub"], which
// this package refuses (see COMPATIBILITY.md).
func gitRejects(e edit) bool {
	has := func(r rune) bool {
		return slices.ContainsFunc(e.args, func(s string) bool { return strings.ContainsRune(s, r) })
	}
	return !has(0) && !(e.op == "--rename-section" && has('\n')) && !strings.HasPrefix(e.args[0], ".")
}

// TestGitVectors checks the expectations of the unit tests against git
// alone, without this package, so that a wrong expectation is never taken
// for a bug of the implementation.
func TestGitVectors(t *testing.T) {
	g := newIsolatedGit(t)

	// get returns what `git config --get` gives for key in content, and
	// whether it succeeded.
	get := func(content, key string, opts ...string) (string, gitResult) {
		args := slices.Concat([]string{"config", "-f", g.write(content)}, opts, []string{"--get", key})
		res := g.run(args...)
		return strings.TrimSuffix(res.stdout, "\n"), res
	}

	t.Run("values", func(t *testing.T) {
		for _, tc := range parseValueCases {
			got, res := get(tc.data, tc.key)
			assert.Zero(t, res.code, "%s: %s", tc.name, res.stderr)
			assert.Equal(t, tc.want, got, tc.name)
		}
	})

	t.Run("get", func(t *testing.T) {
		for _, tc := range getCases {
			got, res := get(tc.data, tc.key)
			assert.Equal(t, tc.found, res.code == 0, "%s: %s", tc.name, res.stderr)
			assert.Equal(t, tc.want, got, tc.name)
		}
	})

	t.Run("syntax errors", func(t *testing.T) {
		for _, tc := range syntaxErrorCases {
			_, line := g.list(tc.data)
			assert.Equal(t, tc.line, line, tc.name)
		}
	})

	t.Run("bool", func(t *testing.T) {
		for _, tc := range boolCases {
			got, res := get("[a]\n\tk = "+gitQuote(tc.value)+"\n", "a.k", "--type=bool")
			assert.Zero(t, res.code, "%q: %s", tc.value, res.stderr)
			assert.Equal(t, strconv.FormatBool(tc.want), got, "%q", tc.value)
		}
		for _, v := range boolErrorCases {
			_, res := get("[a]\n\tk = "+gitQuote(v)+"\n", "a.k", "--type=bool")
			assert.NotZero(t, res.code, "%q", v)
		}
	})

	t.Run("int", func(t *testing.T) {
		for _, tc := range intCases {
			got, res := get("[a]\n\tk = "+gitQuote(tc.value)+"\n", "a.k", "--type=int")
			assert.Zero(t, res.code, "%q: %s", tc.value, res.stderr)
			assert.Equal(t, strconv.FormatInt(tc.want, 10), got, "%q", tc.value)
		}
		for _, tc := range intErrorCases {
			content := "[a]\n\tk = " + gitQuote(tc.entry.Value) + "\n"
			if tc.entry.Implicit {
				content = "[a]\n\tk\n"
			}
			_, res := get(content, "a.k", "--type=int")
			assert.NotZero(t, res.code, tc.name)
			assert.Equal(t, tc.outOfRange, strings.Contains(res.stderr, "out of range"), "%s: %s", tc.name, res.stderr)
		}
	})

	t.Run("path", func(t *testing.T) {
		for _, tc := range pathCases {
			got, res := get("[a]\n\tk = "+gitQuote(tc.value)+"\n", "a.k", "--type=path")
			assert.Zero(t, res.code, "%q: %s", tc.value, res.stderr)
			want := tc.want
			if tc.home != "" {
				want = strings.Replace(want, tc.home, g.dir, 1)
			}
			assertSamePath(t, want, got, "%q", tc.value)
		}
	})

	// apply runs edits with git on before, and returns the file, or false if
	// an edit failed.
	apply := func(before string, edits []edit) (string, bool) {
		path := g.write(before)
		for _, e := range edits {
			if res := g.run(append([]string{"config", "-f", path}, e.gitArgs()...)...); res.code != 0 {
				return "", false
			}
		}
		return readFile(t, path), true
	}

	t.Run("edits", func(t *testing.T) {
		cases := allEditCases()
		for _, tc := range quotingCases {
			cases = append(cases, editCase{"quoting " + strconv.Quote(tc.value), "[a]\n", edits(set("a.k", tc.value)), "[a]\n\tk = " + tc.written + "\n"})
		}
		for _, tc := range cases {
			got, ok := apply(tc.before, tc.edits)
			if !assert.True(t, ok, "%s: git fails", tc.name) || got == tc.after {
				continue
			}
			// E7: git rewrites a line set to the value it has.
			if tc.after == tc.before {
				want, _ := g.list(tc.after)
				entries, _ := g.list(got)
				assert.Equal(t, want, entries, "%s: git rewrote the line to a different value", tc.name)
				continue
			}
			assert.Equal(t, tc.after, got, tc.name)
		}
	})

	t.Run("git differences are real", func(t *testing.T) {
		for _, tc := range gitDifferenceCases {
			got, ok := apply(tc.before, tc.edits)
			assert.False(t, ok && got == tc.after, "%s: git now agrees", tc.name)
		}
	})

	t.Run("edit errors", func(t *testing.T) {
		for _, tc := range editErrorCases {
			if gitRejects(tc.edit) {
				_, ok := apply(editErrorData, edits(tc.edit))
				assert.False(t, ok, "%s: git succeeds", tc.name)
			}
		}
	})
}

func pick[T any](r *rand.Rand, xs ...T) T { return xs[r.IntN(len(xs))] }

// randomFile builds a small configuration file from fragments chosen to
// exercise the parser and the edits: headers of every form, entries on the
// header line, implicit values, continuations, comments, CRLF, a missing
// final newline.
//
// caseVariants adds headers differing only in case from [a] and [a "x"],
// and legacy adds the legacy headers [a.x] and [a.X]. Both are where this
// package deliberately differs from git's edits (gitDifferenceCases): git
// removes and renames sections by their header text, and places a key
// with an upper-case subsection into a legacy header.
func randomFile(r *rand.Rand, caseVariants, legacy bool) string {
	headers := []string{"[a]", "[b]", `[a "x"]`, `[a "X"]`, `[a ""]`, "[a] k = h", "[b] j"}
	if caseVariants {
		headers = append(headers, "[A]", "[A \"x\"]")
	}
	if legacy {
		headers = append(headers, "[a.x]", "[a.X]")
	}
	entries := []string{
		"\tk = 1", "\tk = 2", "k=v", "\tK = \"q;x\"", "\tk", "\tj = a \\\n  b",
		"\tk = x # c", "    j = w", "\tk = \"\"", "\tj =", "\tk = 3 ; c", "\tk = a\\tb",
	}
	fillers := []string{"# c", "; c", "", "\t# indented"}

	var lines []string
	for range r.IntN(4) {
		if r.IntN(3) == 0 {
			lines = append(lines, pick(r, fillers...))
		}
		lines = append(lines, pick(r, headers...))
		for range r.IntN(4) {
			if r.IntN(4) == 0 {
				lines = append(lines, pick(r, fillers...))
			} else {
				lines = append(lines, pick(r, entries...))
			}
		}
	}
	s := strings.Join(lines, "\n")
	if len(lines) > 0 && r.IntN(8) != 0 {
		s += "\n"
	}
	if r.IntN(8) == 0 {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

// randomEditCase returns a random file and one to three random edits of it,
// mostly ones git accepts.
func randomEditCase(r *rand.Rand) (string, []edit) {
	// Section operations leave out case variants and legacy headers, and
	// legacy headers leave out keys with an upper-case subsection: these
	// are the deliberate differences with git.
	sectionOps := r.IntN(4) == 0
	legacy := !sectionOps && r.IntN(2) == 0
	keys := []string{"a.k", "a.k", "a.j", "a.x.k", "a..k", "b.k", "b.j", "c.new", "a.x.new", "Remote.Origin.URL"}
	if !sectionOps {
		keys = append(keys, "A.K", "a.x.K")
	}
	if !legacy {
		keys = append(keys, "a.X.k")
	}
	key := func() string {
		if r.IntN(20) == 0 {
			return "a.k_y"
		}
		return pick(r, keys...)
	}
	vals := []string{"v", "1", "", " pad", "x # y", `say "hi"`, `a\b`, "tab\tx", "multi\nline", "semi;colon", "x\ry", "é", "h"}

	var es []edit
	for range 1 + r.IntN(3) {
		n := 10
		if sectionOps {
			n = 14
		}
		switch op := r.IntN(n); {
		case op < 4:
			es = append(es, set(key(), pick(r, vals...)))
		case op < 6:
			es = append(es, add(key(), pick(r, vals...)))
		case op < 8:
			es = append(es, replaceAll(key(), pick(r, vals...)))
		case op < 9:
			es = append(es, unset(key()))
		case op < 10:
			es = append(es, unsetAll(key()))
		case op < 11:
			es = append(es, removeSection(pick(r, "a", "b", "c")))
		case op < 12:
			es = append(es, removeSubsection("a", pick(r, "x", "X", "", "y")))
		case op < 13:
			es = append(es, renameSection(pick(r, "a", "b"), pick(r, "z", "b", "a")))
		default:
			es = append(es, renameSubsection("a", pick(r, "x", "X", ""), pick(r, "y", "x", "w", "")))
		}
	}
	return randomFile(r, !sectionOps, legacy), es
}

// gitScenario is a repository and its configuration on disk, set up with
// the environment variables git and Discover will both read.
type gitScenario struct {
	name string
	// setup creates the files under root, sets environment variables with
	// t.Setenv, and returns the directory to run git in and the git
	// directory to give to Discover.
	setup func(t *testing.T, root string) (workDir, gitDir string)
	// fails is true if git, and Load, must fail.
	fails bool
}

// gitInit runs `git init` in dir with the current environment.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	res := runGit(t, dir, processEnv(), "init", "-q", "-b", "main")
	require.Zero(t, res.code, res.stderr)
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

var gitScenarios = []gitScenario{
	{
		name: "every scope",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			unsetenv(t, "GIT_CONFIG_NOSYSTEM")
			t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(root, "etc", "gitconfig"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
			writeFile(t, filepath.Join(root, "etc", "gitconfig"), "[a]\n\tk = system\n[core]\n\tautocrlf = input\n")
			writeFile(t, filepath.Join(root, "xdg", "git", "config"), "[a]\n\tk = xdg\n\txdgonly\n")
			writeFile(t, filepath.Join(home, ".gitconfig"), "[a]\n\tk = home\n[remote \"Origin\"]\n\turl = u\n")

			work := filepath.Join(root, "proj")
			gitInit(t, work)
			res := runGit(t, work, processEnv(), "config", "extensions.worktreeConfig", "true")
			require.Zero(t, res.code, res.stderr)
			appendFile(t, filepath.Join(work, ".git", "config"), "[a]\n\tk = local\n[A \"Sub\"]\n\tK\n")
			writeFile(t, filepath.Join(work, ".git", "config.worktree"), "[a]\n\tk = worktree\n")

			t.Setenv("GIT_CONFIG_COUNT", "2")
			t.Setenv("GIT_CONFIG_KEY_0", "a.k")
			t.Setenv("GIT_CONFIG_VALUE_0", "count")
			t.Setenv("GIT_CONFIG_KEY_1", "A.Flag")
			t.Setenv("GIT_CONFIG_VALUE_1", "")
			t.Setenv("GIT_CONFIG_PARAMETERS", "'a.k'='param' 'a.implicit' 'Remote.Up.URL'='x'")
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name: "GIT_CONFIG_GLOBAL",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			writeFile(t, filepath.Join(home, ".gitconfig"), "[a]\n\tk = ignored\n")
			writeFile(t, filepath.Join(root, "global"), "[a]\n\tk = global\n")
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "global"))
			work := filepath.Join(root, "proj")
			gitInit(t, work)
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name: "includes",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			writeFile(t, filepath.Join(home, ".gitconfig"), `[include]
	path = ~/inc/a
[user]
	name = home
[include]
	path = inc/b
	path = missing
	path = ./d/../d/x
[Include]
	Path = upper
[include "sub"]
	path = notadirective
`)
			writeFile(t, filepath.Join(home, "inc", "a"), "[user]\n\tname = a\n[include]\n\tpath = nested\n")
			writeFile(t, filepath.Join(home, "inc", "nested"), "[user]\n\temail = nested\n")
			writeFile(t, filepath.Join(home, "inc", "b"), "[user]\n\tname = b\n")
			writeFile(t, filepath.Join(home, "d", "x"), "[x]\n\tk = x\n")
			writeFile(t, filepath.Join(home, "upper"), "[x]\n\tupper = 1\n")
			writeFile(t, filepath.Join(home, "notadirective"), "[x]\n\tnot = 1\n")

			work := filepath.Join(root, "proj")
			gitInit(t, work)
			appendFile(t, filepath.Join(work, ".git", "config"), "[include]\n\tpath = ../shared\n[user]\n\tname = local\n")
			writeFile(t, filepath.Join(work, "shared"), "[user]\n\tname = shared\n")

			writeFile(t, filepath.Join(root, "cmdinc"), "[x]\n\tcmd = 1\n")
			t.Setenv("GIT_CONFIG_PARAMETERS", "'include.path'='"+filepath.Join(root, "cmdinc")+"'")
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name: "includeIf",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			s := filepath.ToSlash(root)
			conds := []string{
				"gitdir:" + s + "/work/",
				"gitdir:" + s + "/work",
				"gitdir:" + s + "/work/**",
				"gitdir:" + s + "/w*/",
				"gitdir:" + s + "/work/proj/.git",
				"gitdir:" + s + "/work/proj/.git/",
				"gitdir:work/",
				"gitdir:proj/.git",
				"gitdir:ork/",
				"gitdir:" + strings.ToUpper(s) + "/WORK/",
				"gitdir/i:" + strings.ToUpper(s) + "/WORK/",
				"gitdir:~/../work/",
				"gitdir:" + s + "/./work/",
				"gitdir:" + s + "/link/",
				"onbranch:main",
				"onbranch:ma*",
				"onbranch:mai",
				"onbranch:other",
				"onbranch:main/",
				"onbranch:[[:lower:]]ain",
				"onbranch:[[:digit:]]ain",
				"onbranch:m[![:digit:]]in",
				"onbranch:[[:bogus:]]ain",
				"onbranch:ma[in",
				"nosuch:x",
			}
			var global strings.Builder
			for i, c := range conds {
				fmt.Fprintf(&global, "[includeIf %q]\n\tpath = c%d\n", c, i)
				writeFile(t, filepath.Join(home, fmt.Sprintf("c%d", i)), fmt.Sprintf("[cond]\n\tc%d = %s\n", i, c))
			}
			writeFile(t, filepath.Join(home, ".gitconfig"), global.String())

			work := filepath.Join(root, "work", "proj")
			gitInit(t, work)
			appendFile(t, filepath.Join(work, ".git", "config"), "[includeIf \"onbranch:main\"]\n\tpath = ../local-inc\n")
			writeFile(t, filepath.Join(work, "local-inc"), "[cond]\n\tlocal = yes\n")
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name: "includeIf through a symbolic link",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			s := filepath.ToSlash(root)
			writeFile(t, filepath.Join(home, ".gitconfig"), fmt.Sprintf(
				"[includeIf \"gitdir:%s/work/\"]\n\tpath = real\n[includeIf \"gitdir:%s/link/\"]\n\tpath = link\n", s, s))
			writeFile(t, filepath.Join(home, "real"), "[cond]\n\treal = yes\n")
			writeFile(t, filepath.Join(home, "link"), "[cond]\n\tlink = yes\n")
			gitInit(t, filepath.Join(root, "work", "proj"))
			symlink(t, filepath.Join(root, "work"), filepath.Join(root, "link"))
			// Found from its working directory, git would see the real
			// path only; given the git directory, it matches both.
			work := filepath.Join(root, "link", "proj")
			// git for Windows matches the path as given only if it is
			// written with '/': it compares a backslashed GIT_DIR with the
			// pattern as is, where this package uses '/' for both.
			t.Setenv("GIT_DIR", filepath.ToSlash(filepath.Join(work, ".git")))
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name: "linked worktree",
		setup: func(t *testing.T, root string) (string, string) {
			home := os.Getenv("HOME")
			writeFile(t, filepath.Join(home, ".gitconfig"),
				"[user]\n\tname = t\n\temail = t@t\n[includeIf \"onbranch:wt\"]\n\tpath = wt\n[includeIf \"gitdir:"+filepath.ToSlash(root)+"/main/.git/\"]\n\tpath = main\n")
			writeFile(t, filepath.Join(home, "wt"), "[cond]\n\twt = yes\n")
			writeFile(t, filepath.Join(home, "main"), "[cond]\n\tmain = yes\n")

			main := filepath.Join(root, "main")
			gitInit(t, main)
			for _, args := range [][]string{
				{"commit", "-q", "--allow-empty", "-m", "init"},
				{"worktree", "add", "-q", "../wt", "-b", "wt"},
				{"config", "extensions.worktreeConfig", "true"},
				{"config", "a.k", "local"},
			} {
				res := runGit(t, main, processEnv(), args...)
				require.Zero(t, res.code, res.stderr)
			}
			wt := filepath.Join(root, "wt")
			res := runGit(t, wt, processEnv(), "config", "--worktree", "a.k", "linked")
			require.Zero(t, res.code, res.stderr)
			return wt, filepath.Join(wt, ".git")
		},
	},
	{
		name: "worktree file without the extension",
		setup: func(t *testing.T, root string) (string, string) {
			work := filepath.Join(root, "proj")
			gitInit(t, work)
			writeFile(t, filepath.Join(work, ".git", "config.worktree"), "[a]\n\tk = ignored\n")
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name:  "include cycle",
		fails: true,
		setup: func(t *testing.T, root string) (string, string) {
			work := filepath.Join(root, "proj")
			gitInit(t, work)
			writeFile(t, filepath.Join(os.Getenv("HOME"), ".gitconfig"), "[include]\n\tpath = .gitconfig\n")
			return work, filepath.Join(work, ".git")
		},
	},
	{
		name:  "syntax error in an include",
		fails: true,
		setup: func(t *testing.T, root string) (string, string) {
			work := filepath.Join(root, "proj")
			gitInit(t, work)
			writeFile(t, filepath.Join(os.Getenv("HOME"), ".gitconfig"), "[include]\n\tpath = bad\n")
			writeFile(t, filepath.Join(os.Getenv("HOME"), "bad"), "[a]\n\tk = \"x\n")
			return work, filepath.Join(work, ".git")
		},
	},
}

// gitOrigin returns the file of a `--show-origin` field as this package
// reports it: "" for the command line, paths relative to dir made absolute
// without cleaning them, as git opened them.
func gitOrigin(dir, origin string) string {
	if origin == "command line:" {
		return ""
	}
	path := strings.TrimPrefix(origin, "file:")
	if !filepath.IsAbs(path) {
		path = dir + "/" + path
	}
	return filepath.ToSlash(path)
}

// describe renders entries as git lists them with --show-origin. Origins
// are compared up to their separators, see assertSamePath.
func describe(entries []gitconfig.Entry, withScope bool) []string {
	var out []string
	for i, e := range entries {
		s := filepath.ToSlash(e.Origin.File) + "\t" + list(entries[i : i+1])[0]
		if withScope {
			s = e.Origin.Scope.String() + "\t" + s
		}
		out = append(out, s)
	}
	return out
}

func TestGitLoad(t *testing.T) {
	requireGit(t)

	for _, sc := range gitScenarios {
		t.Run(sc.name, func(t *testing.T) {
			root := cleanGitEnv(t)
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("USERPROFILE", filepath.Join(root, "home"))
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			require.NoError(t, os.MkdirAll(filepath.Join(root, "home"), 0o755))
			workDir, gitDir := sc.setup(t, root)

			env, err := gitconfig.Discover(gitDir)
			require.NoError(t, err)

			t.Run("Load", func(t *testing.T) {
				res := runGit(t, workDir, processEnv(), "config", "--list", "--show-scope", "--show-origin", "-z")
				got, err := env.Load()
				if sc.fails {
					require.NotZero(t, res.code, "git succeeds")
					require.Error(t, err)
					return
				}
				require.Zero(t, res.code, res.stderr)
				require.NoError(t, err)

				var want []string
				for _, r := range splitZ(t, res.stdout, 3) {
					want = append(want, r[0]+"\t"+gitOrigin(workDir, r[1])+"\t"+gitKeyValue(r[2]))
				}
				assert.Equal(t, want, describe(slices.Collect(got.All()), true))
			})

			if sc.fails {
				return
			}

			scopes := []gitconfig.Scope{gitconfig.ScopeGlobal, gitconfig.ScopeLocal, gitconfig.ScopeWorktree}
			if os.Getenv("GIT_CONFIG_NOSYSTEM") == "" {
				// git config --system reads the file even with
				// GIT_CONFIG_NOSYSTEM; Discover leaves it out.
				scopes = append(scopes, gitconfig.ScopeSystem)
			}
			for _, s := range scopes {
				// The scope itself is not compared: git labels
				// config.worktree "local" when asked for --worktree.
				t.Run("LoadScope "+s.String(), func(t *testing.T) {
					if s == gitconfig.ScopeGlobal {
						// `git config --global` reads only the file it
						// writes; LoadScope reads the whole global layer, as
						// git does when it reads every scope.
						res := runGit(t, workDir, processEnv(), "config", "--list", "--show-scope", "--show-origin", "-z")
						require.Zero(t, res.code, res.stderr)
						got, err := env.LoadScope(s)
						require.NoError(t, err)

						var want []string
						for _, r := range splitZ(t, res.stdout, 3) {
							if r[0] == "global" {
								want = append(want, gitOrigin(workDir, r[1])+"\t"+gitKeyValue(r[2]))
							}
						}
						assert.Equal(t, want, describe(slices.Collect(got.All()), false))
						return
					}
					res := runGit(t, workDir, processEnv(), "config", "--"+s.String(), "--includes", "--list", "--show-origin", "-z")
					got, err := env.LoadScope(s)
					if res.code != 0 {
						if strings.Contains(res.stderr, "unable to read config file") {
							require.NoError(t, err)
							assert.Empty(t, slices.Collect(got.All()), "git: absent file")
							return
						}
						require.Error(t, err, "git fails: %s", res.stderr)
						return
					}
					require.NoError(t, err)

					var want []string
					for _, r := range splitZ(t, res.stdout, 2) {
						want = append(want, gitOrigin(workDir, r[0])+"\t"+gitKeyValue(r[1]))
					}
					assert.Equal(t, want, describe(slices.Collect(got.All()), false))
				})
			}
		})
	}
}

// TestGitUpdate checks that git reads what UpdateFile writes and honours
// its lock.
func TestGitUpdate(t *testing.T) {
	g := newIsolatedGit(t)

	t.Run("git reads our writes", func(t *testing.T) {
		path := g.write("[a]\n\tk = v\n")
		err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
			if err := f.Set("a.k", "x # y"); err != nil {
				return err
			}
			return f.Add("remote.we\"ird.url", "u")
		})
		require.NoError(t, err)
		res := g.run("config", "-f", path, "--list", "-z")
		require.Zero(t, res.code, res.stderr)
		assert.Equal(t, "a.k\nx # y\x00remote.we\"ird.url\nu\x00", res.stdout)
	})

	t.Run("git honours our lock", func(t *testing.T) {
		path := g.write("[a]\n\tk = v\n")
		err := gitconfig.Env{}.UpdateFile(path, func(f *gitconfig.File) error {
			res := g.run("config", "-f", path, "a.k", "git")
			assert.NotZero(t, res.code)
			assert.Contains(t, res.stderr, "could not lock config file")
			return f.Set("a.k", "ours")
		})
		require.NoError(t, err)
		assert.Equal(t, "[a]\n\tk = ours\n", readFile(t, path))
	})

}

var dubious = regexp.MustCompile(`dubious ownership in repository at '([^']*)'`)

// TestGitTrust checks that Discover trusts the repositories git trusts, and
// names the path git names when it refuses one. Without root, only the
// layouts owned by the current user are compared: on Windows, that covers a
// test run as an administrator, whose files belong to the Administrators
// group.
func TestGitTrust(t *testing.T) {
	requireGit(t)
	root := runtime.GOOS != "windows" && os.Geteuid() == 0
	for _, tc := range trustCases {
		t.Run(tc.name, func(t *testing.T) {
			if (len(tc.other) > 0 || tc.sudoUID != "") && !root {
				t.Skip("needs root, to give files to another user")
			}
			dir := tc.prepare(t)
			start := filepath.Join(dir, tc.start)

			res := runGit(t, dir, processEnv(), "-C", start, "rev-parse", "--git-dir")
			_, err := gitconfig.Discover(start)
			if res.code == 0 {
				require.NoError(t, err, "git trusts it")
				return
			}
			m := dubious.FindStringSubmatch(res.stderr)
			require.NotNil(t, m, "git fails for another reason: %s", res.stderr)
			require.ErrorIs(t, err, gitconfig.ErrUntrusted, "git refuses it: %s", res.stderr)
			fe, ok := errors.AsType[*gitconfig.FileError](err)
			require.True(t, ok, "got %T", err)
			assertSamePath(t, m[1], fe.Path)
		})
	}
}
