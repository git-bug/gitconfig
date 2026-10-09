package gitconfig

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Scope is one layer of git's configuration. Values from later scopes take
// precedence over earlier ones.
//
// ScopeFile, the zero value, is not a layer: it marks entries of a file read
// on its own (Parse, Env.LoadFile, Env.UpdateFile), which may come from
// anyone. git reports such entries as "command"; this package keeps them
// apart so that they are never mistaken for the protected command scope.
type Scope int

const (
	ScopeFile     Scope = iota // a file read on its own, as with git config -f
	ScopeSystem                // /etc/gitconfig or GIT_CONFIG_SYSTEM
	ScopeGlobal                // ~/.gitconfig, the XDG file, or GIT_CONFIG_GLOBAL
	ScopeLocal                 // the repository's config
	ScopeWorktree              // config.worktree, if extensions.worktreeConfig is enabled
	ScopeCommand               // git -c, GIT_CONFIG_COUNT/KEY/VALUE; read-only
)

// String returns the scope as git names it ("system", "global", ...), and
// "file" for ScopeFile.
func (s Scope) String() string {
	switch s {
	case ScopeFile:
		return "file"
	case ScopeSystem:
		return "system"
	case ScopeGlobal:
		return "global"
	case ScopeLocal:
		return "local"
	case ScopeWorktree:
		return "worktree"
	case ScopeCommand:
		return "command"
	}
	return "Scope(" + strconv.Itoa(int(s)) + ")"
}

// Env says where git's configuration comes from: the system file, the global
// files, the repository and the command line. Discover fills it as git
// does; tests and embedders can fill it by hand. Every field is used as given,
// and a zero source field means "none": an Env reads nothing it was not given.
// An Env is a value, but copies share the GlobalConfig and Command slices:
// replace them rather than modify them in place.
//
// Configuration can come from someone else: a repository on a shared disk,
// an unpacked archive, a file reached through include.path. Loading such a
// file is safe (bounded, see Load), but its values are not to be trusted:
// check SafeDirectory before loading or editing a repository's
// configuration, as git does.
type Env struct {
	// Home is the user's home directory. Load uses it to expand "~/" in
	// include paths; pass it to Config.Path and Entry.Path for values.
	// "" makes such paths an error.
	Home string

	// GitDir is the repository's git directory, "" outside a repository.
	// It must be absolute: includeIf "gitdir:" matches absolute paths, and
	// a relative GitDir makes Load fail with ErrNotAbsolute rather than
	// silently not match. The common directory and the worktree
	// configuration are derived from it. includeIf "onbranch:" reads
	// <GitDir>/HEAD when such a condition is met.
	GitDir string

	// SystemConfig is the system configuration file, "" for none.
	SystemConfig string

	// GlobalConfig lists the global configuration files, read in order.
	// Writes go to the last one that exists, or to the last one if none
	// exists: for git's [XDG file, ~/.gitconfig] this picks ~/.gitconfig
	// unless only the XDG file exists.
	GlobalConfig []string

	// Command holds the values given on the command line (git -c,
	// GIT_CONFIG_COUNT, GIT_CONFIG_PARAMETERS), in order.
	Command []CommandEntry
}

// CommandEntry is one value given on the command line. Key is validated by
// Load: a key git rejects makes Load fail with ErrInvalidKey, as git does.
// An include.path given this way is followed; a relative one is
// ErrNotAbsolute, as git refuses relative includes that do not come from a
// file.
type CommandEntry struct {
	Key      string
	Value    string
	Implicit bool // "git -c key" without "=": true as a boolean
}

// Discover finds where git's configuration comes from, the way git does,
// using the running process: environment variables (GIT_CONFIG_GLOBAL,
// XDG_CONFIG_HOME, …), the user's home directory, the working directory and
// the platform's system configuration file.
//
// gitDir is the repository's git directory, such as ".git", or the
// repository itself for a bare one; it is made absolute. If gitDir is a file
// holding "gitdir: <path>", as .git is in a linked worktree or a submodule,
// the path it names is used. Finding the repository is left to the caller:
// Discover does not search parent directories and does not read GIT_DIR.
// Pass "" when not in a repository: only the system, global and command-line
// scopes exist then.
//
// Without a home directory, Home is "" and the global files are those git
// reads then: the XDG file if XDG_CONFIG_HOME is set, no ~/.gitconfig.
//
// It fails if a gitdir file is malformed, or if GIT_CONFIG_COUNT or
// GIT_CONFIG_PARAMETERS is malformed, as git does.
//
// It is the only function of this package that reads the process
// environment.
func Discover(gitDir string) (Env, error) {
	return discover(gitDir, osEnviron{})
}

// Load returns the effective configuration: every scope, in git's order of
// precedence, with includes followed. This is what git itself uses. The view
// of one scope is the entries with that Origin.Scope.
//
// Includes are followed as git does (depth limit 10, ErrIncludeDepth), with
// two safety bounds git lacks: an include must be a regular file
// (ErrNotRegular, so a FIFO or a device cannot hang or exhaust the process),
// and one load reads at most 1000 files and 64 MiB in total (ErrLoadLimit, so
// a file including itself many times cannot multiply the work exponentially).
// These are reached only by files built to reach them.
//
// A global file that cannot be read for lack of permission is skipped, as
// git does; LoadProtected skips such a system file too.
func (e Env) Load() (*Config, error) {
	l, err := e.newLoader()
	if err != nil {
		return nil, err
	}
	if err := l.system(false); err != nil {
		return nil, err
	}
	if err := l.global(); err != nil {
		return nil, err
	}
	if l.repo != nil {
		start := len(l.entries)
		if err := l.loadFile(l.repo.localConfig(), ScopeLocal, false); err != nil {
			return nil, err
		}
		ok, err := worktreeConfigIn(l.entries[start:], l.repo.localConfig())
		if err != nil {
			return nil, err
		}
		if ok {
			if err := l.loadFile(l.repo.worktreeConfig(), ScopeWorktree, false); err != nil {
				return nil, err
			}
		}
	}
	if err := l.command(); err != nil {
		return nil, err
	}
	return l.config(), nil
}

// system loads the system scope, if any. If skipUnreadable, a file that
// cannot be read for lack of permission is skipped.
func (l *loader) system(skipUnreadable bool) error {
	if l.env.SystemConfig == "" {
		return nil
	}
	return l.loadFile(l.env.SystemConfig, ScopeSystem, skipUnreadable)
}

// global loads the global scope, skipping the files that cannot be read for
// lack of permission, as git does.
func (l *loader) global() error {
	for _, path := range l.env.GlobalConfig {
		if err := l.loadFile(path, ScopeGlobal, true); err != nil {
			return err
		}
	}
	return nil
}

// LoadProtected returns the configuration of the protected scopes only, with
// includes followed: system, global and command line, in that order. This is
// what git uses for keys a repository must not be able to set, such as
// safe.directory or safe.bareRepository. The repository's configuration is
// not read, so this is safe to call before SafeDirectory.
func (e Env) LoadProtected() (*Config, error) {
	l, err := e.newLoader()
	if err != nil {
		return nil, err
	}
	if err := l.system(true); err != nil {
		return nil, err
	}
	if err := l.global(); err != nil {
		return nil, err
	}
	if err := l.command(); err != nil {
		return nil, err
	}
	return l.config(), nil
}

// SafeDirectory reports whether dir is allowed by the safe.directory values
// of LoadProtected, with git's matching rules ("*", a trailing "/*", an empty
// value resetting the list). dir is the repository's working tree, or its
// git directory for a bare repository, as git checks.
//
// The repository's configuration is not read. It does not check who owns
// dir: git trusts a repository owned by the current user without consulting
// safe.directory, and that check is platform specific and left to the
// caller. A false result therefore means "not listed", not "unsafe".
func (e Env) SafeDirectory(dir string) (bool, error) {
	c, err := e.LoadProtected()
	if err != nil {
		return false, err
	}
	dir = realPath(dir)
	if !filepath.IsAbs(dir) {
		// The entries that count are absolute. Joining the real working
		// directory drops a leading ".." correctly: it has no links.
		if wd, err := os.Getwd(); err == nil {
			dir = filepath.Join(realPath(wd), dir)
		}
	}
	safe := false
	for _, entry := range c.GetAll("safe.directory") {
		switch v := entry.Value; {
		case v == "":
			safe = false
		case v == "*":
			safe = true
		case strings.HasSuffix(v, "/*"):
			prefix, err := expandPath(strings.TrimSuffix(v, "*"), e.Home)
			if err != nil || !filepath.IsAbs(prefix) {
				// As for exact entries, relative ones are skipped.
				continue
			}
			if p, err := filepath.EvalSymlinks(prefix); err == nil {
				if strings.HasPrefix(dir, strings.TrimSuffix(p, string(filepath.Separator))+string(filepath.Separator)) {
					safe = true
				}
			}
		default:
			p, err := expandPath(v, e.Home)
			if err != nil || !filepath.IsAbs(p) {
				// "." and unexpandable values are skipped.
				continue
			}
			if p, err := filepath.EvalSymlinks(p); err == nil && p == dir {
				safe = true
			}
		}
	}
	return safe, nil
}

// realPath returns path with symbolic links resolved, or cleaned if it
// cannot be resolved.
func realPath(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return filepath.Clean(path)
}

// LoadScope returns the configuration of exactly one scope, with includes
// followed, like `git config --includes --<scope>` (git itself follows
// includes there only with --includes). Lower scopes are not included.
//
// ScopeGlobal is the whole global layer, every file of GlobalConfig, as in
// Load. `git config --global` differs: it reads only the file it writes to
// (WritePath), so keys of the XDG file are not listed while ~/.gitconfig
// exists.
//
// A scope with no file (SystemConfig "", a global or worktree file that
// does not exist) is empty, as for `git config --get`. ScopeWorktree reads
// the file WritePath gives: without extensions.worktreeConfig, that is the
// repository's config, and its entries have ScopeLocal, as git reports.
// It fails with ErrNoScope for ScopeFile, and for ScopeLocal and
// ScopeWorktree where WritePath does.
func (e Env) LoadScope(s Scope) (*Config, error) {
	l, err := e.newLoader()
	if err != nil {
		return nil, err
	}
	switch s {
	case ScopeSystem:
		err = l.system(false)
	case ScopeGlobal:
		err = l.global()
	case ScopeLocal, ScopeWorktree:
		var path string
		var scope Scope
		path, scope, err = scopeFile(l.repo, s)
		if err == nil {
			err = l.loadFile(path, scope, false)
		}
	case ScopeCommand:
		err = l.command()
	default:
		return nil, ErrNoScope
	}
	if err != nil {
		return nil, err
	}
	return l.config(), nil
}

// LoadFile returns the configuration of the file at path, with includes
// followed, like `git config --includes -f <path>` (git itself follows
// includes there only with --includes). Its entries have ScopeFile. A
// missing file is empty.
func (e Env) LoadFile(path string) (*Config, error) {
	l, err := e.newLoader()
	if err != nil {
		return nil, err
	}
	if err := l.loadFile(path, ScopeFile, false); err != nil {
		return nil, err
	}
	return l.config(), nil
}

// WritePath returns the file that `git config --<scope>` writes to. For
// ScopeWorktree, that is config.worktree if extensions.worktreeConfig is
// set, otherwise the repository's config as for ScopeLocal, provided the
// repository has a single worktree. It fails with ErrNoScope if the scope
// cannot be written in this Env: ScopeFile and ScopeCommand always,
// ScopeSystem with no SystemConfig, ScopeGlobal with no GlobalConfig,
// ScopeLocal and ScopeWorktree outside a repository, ScopeWorktree in a
// repository with several worktrees and no extensions.worktreeConfig.
func (e Env) WritePath(s Scope) (string, error) {
	path, _, err := e.writeTarget(s)
	return path, err
}

// writeTarget returns the file WritePath returns, and the scope its entries
// have.
func (e Env) writeTarget(s Scope) (string, Scope, error) {
	switch s {
	case ScopeSystem:
		if e.SystemConfig == "" {
			return "", 0, ErrNoScope
		}
		return e.SystemConfig, s, nil
	case ScopeGlobal:
		if len(e.GlobalConfig) == 0 {
			return "", 0, ErrNoScope
		}
		for i := len(e.GlobalConfig) - 1; i >= 0; i-- {
			if _, err := os.Lstat(e.GlobalConfig[i]); err == nil {
				return e.GlobalConfig[i], s, nil
			}
		}
		return e.GlobalConfig[len(e.GlobalConfig)-1], s, nil
	case ScopeLocal, ScopeWorktree:
		r, err := e.repo()
		if err != nil {
			return "", 0, err
		}
		return scopeFile(r, s)
	}
	return "", 0, ErrNoScope
}

// scopeFile returns the file of the local or worktree scope of r, nil
// outside a repository, and the scope its entries have: without
// extensions.worktreeConfig, the worktree scope is the local config, if the
// repository has a single worktree.
func scopeFile(r *repo, s Scope) (string, Scope, error) {
	if r == nil {
		return "", 0, ErrNoScope
	}
	if s == ScopeLocal {
		return r.localConfig(), ScopeLocal, nil
	}
	ok, err := r.worktreeConfigEnabled()
	switch {
	case err != nil:
		return "", 0, err
	case ok:
		return r.worktreeConfig(), ScopeWorktree, nil
	case r.severalWorktrees():
		return "", 0, ErrNoScope
	}
	return r.localConfig(), ScopeLocal, nil
}

// UpdateScope edits the file of scope s, as UpdateFile does; the File's
// entries have the scope LoadScope gives them. Writes never go into an
// included file.
func (e Env) UpdateScope(s Scope, fn func(*File) error) error {
	path, scope, err := e.writeTarget(s)
	if err != nil {
		return err
	}
	return updateFile(path, scope, fn)
}

// UpdateFile edits the file at path with git's lock protocol, like
// `git config -f <path>`:
//
//  1. if path is a symbolic link, follow it (up to 5 levels, relative
//     targets resolved against the link's directory) and edit the target:
//     the link itself is kept;
//  2. create <target>.lock exclusively, never through a symbolic link, or
//     fail with ErrLocked immediately, as git does;
//  3. read and parse the target under the lock (a missing file is empty),
//     into a File;
//  4. call fn, exactly once;
//  5. if the content changed, write it to the lock file, give it the
//     target's permission bits exactly (chmod, so the umask does not apply;
//     a new file gets 0666 minus the umask), and rename it over the target;
//     otherwise leave the target alone.
//
// On Windows, opening or replacing the target is retried for up to a second
// while another process holds it, as a concurrent writer or a reader does
// for a moment.
//
// If fn returns an error or panics, the lock is released and nothing is
// written; the panic then continues. Readers see either the old or the new
// file, never a partial one. After fn returns, edits of the File fail with
// ErrClosed.
//
// Following the symbolic link in step 1 is what git does, and what dotfile
// managers rely on. It also means that whoever controls the directory of
// path controls which file is written: do not edit the configuration of a
// repository you have not checked with SafeDirectory and an ownership check.
func (e Env) UpdateFile(path string, fn func(*File) error) error {
	return updateFile(path, ScopeFile, fn)
}
