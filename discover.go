package gitconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// environ is the process environment, as Discover reads it.
type environ interface {
	Lookup(key string) (string, bool)
}

type osEnviron struct{}

func (osEnviron) Lookup(key string) (string, bool) { return os.LookupEnv(key) }

func get(env environ, key string) string {
	v, _ := env.Lookup(key)
	return v
}

func discover(dir string, env environ) (Env, error) {
	var e Env

	e.Home = get(env, "HOME")
	if e.Home == "" && runtime.GOOS == "windows" {
		e.Home = get(env, "USERPROFILE")
	}

	if g, ok := env.Lookup("GIT_CONFIG_GLOBAL"); ok {
		// Set but empty: no global file at all.
		if g != "" {
			e.GlobalConfig = []string{g}
		}
	} else {
		if xdg := get(env, "XDG_CONFIG_HOME"); xdg != "" {
			e.GlobalConfig = append(e.GlobalConfig, filepath.Join(xdg, "git", "config"))
		} else if e.Home != "" {
			e.GlobalConfig = append(e.GlobalConfig, filepath.Join(e.Home, ".config", "git", "config"))
		}
		if e.Home != "" {
			e.GlobalConfig = append(e.GlobalConfig, filepath.Join(e.Home, ".gitconfig"))
		}
	}

	noSystem := false
	if v, ok := env.Lookup("GIT_CONFIG_NOSYSTEM"); ok {
		b, err := parseBool(v)
		if err != nil {
			return Env{}, &EnvError{Var: "GIT_CONFIG_NOSYSTEM", Err: fmt.Errorf("bad boolean value %q", v)}
		}
		noSystem = b
	}
	switch s, ok := env.Lookup("GIT_CONFIG_SYSTEM"); {
	case noSystem:
	case ok:
		e.SystemConfig = s
	case runtime.GOOS != "windows":
		e.SystemConfig = "/etc/gitconfig"
	}
	// On Windows the system file is under git's installation directory,
	// which this package cannot know: there is none unless
	// GIT_CONFIG_SYSTEM names it.

	cmd, err := countEntries(env)
	if err != nil {
		return Env{}, err
	}
	params, err := parseParameters(get(env, "GIT_CONFIG_PARAMETERS"))
	if err != nil {
		return Env{}, &EnvError{Var: "GIT_CONFIG_PARAMETERS", Err: err}
	}
	e.Command = append(cmd, params...)

	if dir != "" {
		var owned []string
		if e.GitDir, owned, err = findRepo(dir); err != nil {
			return Env{}, err
		}
		if err := e.checkTrust(owned, get(env, "SUDO_UID")); err != nil {
			return Env{}, err
		}
	}
	return e, nil
}

// findRepo returns the git directory of the repository at dir, as git finds
// it when started there, and the paths whose owner git checks. In a working
// tree (dir holds .git, or is a "gitdir:" file), they are the working tree,
// its .git and the git directory; elsewhere, dir alone. The first is the
// path safe.directory lists. A git directory that checkGitDir refuses fails
// with ErrNotRepository.
func findRepo(dir string) (gitDir string, owned []string, err error) {
	if dir, err = filepath.Abs(dir); err != nil {
		return "", nil, err
	}
	dot := filepath.Join(dir, ".git")
	if fi, err := os.Stat(dir); err == nil && fi.Mode().IsRegular() {
		dir, dot = filepath.Dir(dir), dir
	} else if _, err := os.Stat(dot); isMissing(err) {
		if err := checkGitDir(dir); err != nil {
			return "", nil, err
		}
		return dir, []string{dir}, nil
	} else if err != nil {
		return "", nil, &FileError{Path: dot, Err: err}
	}
	if gitDir, err = resolveGitDir(dot); err != nil {
		return "", nil, err
	}
	if err := checkGitDir(gitDir); err != nil {
		return "", nil, err
	}
	return gitDir, []string{dir, dot, gitDir}, nil
}

// checkGitDir fails with ErrNotRepository unless gitDir is a git directory
// as git recognizes one: it holds a HEAD file, and its common directory
// (gitDir itself, or the one its commondir file names in a linked worktree)
// holds objects and refs directories. Any other directory would have its
// config read and written as the local scope. HEAD may also be a symbolic
// link to a branch (core.preferSymlinkRefs), whose target does not exist
// while the branch is unborn or packed: as for git, the link's text must
// start with "refs/". What HEAD holds is not checked, where git also
// requires a ref or an object name.
func checkGitDir(gitDir string) error {
	head := filepath.Join(gitDir, "HEAD")
	fi, err := os.Lstat(head)
	if err != nil {
		return notRepository(gitDir, err)
	}
	switch {
	case fi.Mode().IsRegular():
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(head)
		if err != nil {
			return &FileError{Path: head, Err: err}
		}
		if !strings.HasPrefix(filepath.ToSlash(target), "refs/") {
			return &FileError{Path: gitDir, Err: ErrNotRepository}
		}
	default:
		return &FileError{Path: gitDir, Err: ErrNotRepository}
	}
	r, err := Env{GitDir: gitDir}.repo()
	if err != nil {
		return err
	}
	for _, name := range []string{"objects", "refs"} {
		fi, err := os.Stat(filepath.Join(r.commonDir, name))
		if err != nil {
			return notRepository(gitDir, err)
		}
		if !fi.IsDir() {
			return &FileError{Path: gitDir, Err: ErrNotRepository}
		}
	}
	return nil
}

// notRepository wraps the error met checking the git directory gitDir: a
// missing path means it is not a repository.
func notRepository(gitDir string, err error) error {
	if isMissing(err) {
		err = ErrNotRepository
	}
	return &FileError{Path: gitDir, Err: err}
}

// checkTrust fails with ErrUntrusted unless the current user owns every
// path of owned that exists, or safe.directory lists the first.
func (e Env) checkTrust(owned []string, sudoUID string) error {
	for _, p := range owned {
		ok, err := ownedByCurrentUser(p, sudoUID)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return &FileError{Path: p, Err: err}
		}
		if ok {
			continue
		}
		// Without the repository, as git: its HEAD and path must not
		// match an includeIf that lists it.
		p := e
		p.GitDir = ""
		c, err := p.LoadProtected()
		if err != nil {
			return err
		}
		if !safeDirectory(c, owned[0], e.Home) {
			return &FileError{Path: owned[0], Err: ErrUntrusted}
		}
		return nil
	}
	return nil
}

// resolveGitDir follows a "gitdir: <path>" file; any other path is the git
// directory itself. gitDir is absolute.
func resolveGitDir(gitDir string) (string, error) {
	fi, err := os.Stat(gitDir)
	if err != nil || !fi.Mode().IsRegular() {
		return gitDir, nil
	}
	data, err := readSmall(gitDir)
	if err != nil {
		return "", &FileError{Path: gitDir, Err: err}
	}
	path, ok := strings.CutPrefix(strings.TrimRight(string(data), "\r\n"), "gitdir: ")
	if !ok || path == "" {
		return "", &FileError{Path: gitDir, Err: errors.New("invalid gitfile format")}
	}
	if !filepath.IsAbs(path) {
		if path, err = joinReal(filepath.Dir(gitDir), path); err != nil {
			return "", &FileError{Path: gitDir, Err: err}
		}
	}
	return path, nil
}

// countEntries reads GIT_CONFIG_COUNT, GIT_CONFIG_KEY_<n> and
// GIT_CONFIG_VALUE_<n>. The count is digits only, where git also accepts a
// sign and leading whitespace.
func countEntries(env environ) ([]CommandEntry, error) {
	count := get(env, "GIT_CONFIG_COUNT")
	if count == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(count, 10, 32)
	if err != nil || count[0] < '0' || count[0] > '9' {
		return nil, &EnvError{Var: "GIT_CONFIG_COUNT", Err: fmt.Errorf("bogus count %q", count)}
	}
	var entries []CommandEntry
	for i := range int(n) {
		keyVar, valueVar := "GIT_CONFIG_KEY_"+strconv.Itoa(i), "GIT_CONFIG_VALUE_"+strconv.Itoa(i)
		key, ok := env.Lookup(keyVar)
		if !ok {
			return nil, &EnvError{Var: keyVar, Err: errMissingVar}
		}
		value, ok := env.Lookup(valueVar)
		if !ok {
			return nil, &EnvError{Var: valueVar, Err: errMissingVar}
		}
		entries = append(entries, CommandEntry{Key: key, Value: value})
	}
	return entries, nil
}

var (
	errMissingVar      = errors.New("missing")
	errBogusParameters = errors.New("bogus format")
)

// parseParameters parses GIT_CONFIG_PARAMETERS, where git passes its -c
// options to the commands it runs: items separated by whitespace, each a
// single-quoted key, then "=" and a single-quoted value, or "=" alone for
// an option without a value. Inside a quoted string, a quote is written as
// a backslash and a quote between two quoted strings, and so may be an
// exclamation mark:
//
//	'e.f'='x'\''y' 'g.h'=''\!'z'
//
// The older form of an item, a single-quoted "key=value", is accepted too.
func parseParameters(s string) ([]CommandEntry, error) {
	var entries []CommandEntry
	for s != "" {
		key, rest, ok := unquote(s)
		if !ok {
			return nil, errBogusParameters
		}
		var c CommandEntry
		if v, ok := strings.CutPrefix(rest, "="); ok {
			// The current form: 'key'='value', or 'key'= for no value.
			c.Key = key
			if v == "" || isParamSpace(v[0]) {
				c.Implicit = true
				rest = v
			} else {
				c.Value, rest, ok = unquote(v)
				if !ok {
					return nil, errBogusParameters
				}
			}
		} else if k, v, ok := strings.Cut(key, "="); ok {
			// The older form: 'key=value'.
			c.Key, c.Value = k, v
		} else {
			c.Key, c.Implicit = key, true
		}
		if rest != "" && !isParamSpace(rest[0]) {
			return nil, errBogusParameters
		}
		entries = append(entries, c)
		s = strings.TrimLeft(rest, " \t\n\v\f\r")
	}
	return entries, nil
}

func isParamSpace(c byte) bool {
	return strings.IndexByte(" \t\n\v\f\r", c) >= 0
}

// unquote reads a single-quoted string at the start of s.
func unquote(s string) (value, rest string, ok bool) {
	if s == "" || s[0] != '\'' {
		return "", "", false
	}
	var sb strings.Builder
	s = s[1:]
	for {
		i := strings.IndexByte(s, '\'')
		if i < 0 {
			return "", "", false
		}
		sb.WriteString(s[:i])
		s = s[i+1:]
		if len(s) >= 3 && s[0] == '\\' && (s[1] == '\'' || s[1] == '!') && s[2] == '\'' {
			sb.WriteByte(s[1])
			s = s[3:]
			continue
		}
		return sb.String(), s, true
	}
}
