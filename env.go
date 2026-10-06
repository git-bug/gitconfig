package gitconfig

// Scope is one layer of git's configuration. Values from later scopes take
// precedence over earlier ones.
type Scope int

const (
	System   Scope = iota + 1 // /etc/gitconfig or GIT_CONFIG_SYSTEM
	Global                    // ~/.gitconfig, the XDG file, or GIT_CONFIG_GLOBAL
	Local                     // the repository's config
	Worktree                  // config.worktree, if extensions.worktreeConfig is enabled
	Command                   // git -c, GIT_CONFIG_COUNT/KEY/VALUE; read-only
)

// String returns the scope as git names it ("system", "global", ...).
func (s Scope) String() string {
	panic("not implemented")
}

// Env says where git's configuration comes from: the system file, the global
// files, the repository and the command line. Discover fills it as git does;
// tests and embedders can fill it by hand. Every field is used as given, and a
// zero field means "none": an Env reads nothing it was not given.
type Env struct {
	FS FS

	// Home is the user's home directory, used to expand "~/" in include
	// paths.
	Home string

	// GitDir is the repository's git directory, "" outside a repository.
	// The common directory, the worktree configuration and the current
	// branch are derived from it.
	GitDir string

	// SystemConfig is the system configuration file, "" for none.
	SystemConfig string

	// GlobalConfig lists the global configuration files, read in order.
	// Writes go to the last one that exists, or to the last one if none
	// exists: for git's [XDG file, ~/.gitconfig] this picks ~/.gitconfig
	// unless only the XDG file exists.
	GlobalConfig []string

	// Command holds the entries given on the command line (git -c,
	// GIT_CONFIG_COUNT, GIT_CONFIG_PARAMETERS). Their Origin is ignored.
	Command []Entry
}

// Discover finds where git's configuration comes from, the way git does,
// using the running process: environment variables (GIT_CONFIG_GLOBAL,
// XDG_CONFIG_HOME, …), the user's home directory and the platform's system
// configuration file.
//
// gitDir is the repository's git directory, such as ".git". Pass "" when not
// in a repository: only the system, global and command-line scopes exist then.
//
// The returned Env reads and writes the real disk. Set its FS field to use
// another filesystem (go-billy, in-memory).
//
// It is the only function of this package that reads the process
// environment.
func Discover(gitDir string) (Env, error) {
	panic("not implemented")
}

// Load returns the effective configuration: every scope, in git's order of
// precedence, with includes followed. This is what git itself uses.
func (e Env) Load() (*Config, error) {
	panic("not implemented")
}

// LoadScope returns the configuration of exactly one scope, with includes
// followed, like `git config --<scope>`. Lower scopes are not included.
// It fails with ErrNoScope if the scope does not exist in this Env.
func (e Env) LoadScope(s Scope) (*Config, error) {
	panic("not implemented")
}

// LoadFile returns the configuration of the file at path, with includes
// followed, like `git config -f <path>`. Its entries have no Scope.
func (e Env) LoadFile(path string) (*Config, error) {
	panic("not implemented")
}

// Path returns the file that `git config --<scope>` writes to. It fails with
// ErrNoScope if the scope cannot be written in this Env (Command always,
// Local outside a repository, Worktree unless extensions.worktreeConfig is
// set, ...).
func (e Env) Path(s Scope) (string, error) {
	panic("not implemented")
}

// UpdateScope edits the file of scope s, as UpdateFile does. Writes never go
// into an included file.
func (e Env) UpdateScope(s Scope, fn func(*File) error) error {
	panic("not implemented")
}

// UpdateFile edits the file at path with git's lock protocol, like
// `git config -f <path>`:
//
//  1. create <path>.lock exclusively, or fail with ErrLocked;
//  2. read and parse path under the lock (a missing file is empty);
//  3. call fn;
//  4. if the content changed, write it to the lock file and rename it over
//     path, keeping the file's permission bits; otherwise leave path alone.
//
// If fn returns an error or panics, the lock is released and nothing is
// written. Readers see either the old or the new file, never a partial one.
func (e Env) UpdateFile(path string, fn func(*File) error) error {
	panic("not implemented")
}
