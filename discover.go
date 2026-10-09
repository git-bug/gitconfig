package gitconfig

import (
	"errors"
	"fmt"
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

func discover(gitDir string, env environ) (Env, error) {
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

	if gitDir != "" {
		e.GitDir, err = resolveGitDir(gitDir)
		if err != nil {
			return Env{}, err
		}
	}
	return e, nil
}

// resolveGitDir makes gitDir absolute and follows a "gitdir: <path>" file.
func resolveGitDir(gitDir string) (string, error) {
	gitDir, err := filepath.Abs(gitDir)
	if err != nil {
		return "", err
	}
	// A directory is the git directory itself. One that does not exist (yet)
	// is not an error here: loading finds no files in it.
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
