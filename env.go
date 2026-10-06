package gitconfig

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
	panic("not implemented")
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
// It fails if the home directory is needed and cannot be determined, if a
// gitdir file is malformed, or if GIT_CONFIG_COUNT or GIT_CONFIG_PARAMETERS
// is malformed, as git does.
//
// It is the only function of this package that reads the process
// environment.
func Discover(gitDir string) (Env, error) {
	panic("not implemented")
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
func (e Env) Load() (*Config, error) {
	panic("not implemented")
}

// LoadProtected returns the configuration of the protected scopes only, with
// includes followed: system, global and command line, in that order. This is
// what git uses for keys a repository must not be able to set, such as
// safe.directory or safe.bareRepository. The repository's configuration is
// not read, so this is safe to call before SafeDirectory.
func (e Env) LoadProtected() (*Config, error) {
	panic("not implemented")
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
	panic("not implemented")
}

// LoadScope returns the configuration of exactly one scope, with includes
// followed, like `git config --includes --<scope>` (git itself follows
// includes there only with --includes). Lower scopes are not included.
//
// A scope with no file (SystemConfig "", a global or worktree file that
// does not exist) is empty, as for `git config --get`. ScopeWorktree reads
// the file WritePath gives: without extensions.worktreeConfig, that is the
// repository's config, and its entries have ScopeLocal, as git reports.
// It fails with ErrNoScope for ScopeFile, and for ScopeLocal and
// ScopeWorktree where WritePath does.
func (e Env) LoadScope(s Scope) (*Config, error) {
	panic("not implemented")
}

// LoadFile returns the configuration of the file at path, with includes
// followed, like `git config --includes -f <path>` (git itself follows
// includes there only with --includes). Its entries have ScopeFile. A
// missing file is empty.
func (e Env) LoadFile(path string) (*Config, error) {
	panic("not implemented")
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
	panic("not implemented")
}

// UpdateScope edits the file of scope s, as UpdateFile does; the File's
// entries have the scope LoadScope gives them. Writes never go into an
// included file.
func (e Env) UpdateScope(s Scope, fn func(*File) error) error {
	panic("not implemented")
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
// If fn returns an error or panics, the lock is released and nothing is
// written; the panic then continues. Readers see either the old or the new file, never a partial one.
// After fn returns, edits of the File fail with ErrClosed.
//
// Following the symbolic link in step 1 is what git does, and what dotfile
// managers rely on. It also means that whoever controls the directory of
// path controls which file is written: do not edit the configuration of a
// repository you have not checked with SafeDirectory and an ownership check.
func (e Env) UpdateFile(path string, fn func(*File) error) error {
	panic("not implemented")
}

// lockAndUpdate implements Env.UpdateFile.
func lockAndUpdate(path string, fn func(*File) error) error {
	panic("not implemented")
}
