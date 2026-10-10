// Package gitconfig reads and edits git configuration files exactly like git
// does: same parsing, same value semantics, same scopes and includes, and
// edits that change only the lines they touch.
//
// The API has three parts:
//
//   - [Config] is an immutable set of entries, for reading.
//   - [File] is the lossless, editable form of a single file.
//   - [Env] says where git's configuration comes from, and is the only way
//     to the disk: it loads scopes or files (following includes) and
//     edits them with git's lock file protocol, so that git and this
//     package can safely write the same file.
//
// Reading the effective configuration of a repository and changing a value:
//
//	env, err := gitconfig.Discover(".") // the working tree
//	if err != nil { ... }               // ErrNotRepository, or ErrUntrusted
//
//	cfg, err := env.Load()
//	if err != nil { ... }
//	if e, ok := cfg.Get("user.name"); ok {
//		fmt.Println(e.Value)
//	}
//
//	err = env.UpdateScope(gitconfig.ScopeLocal, func(f *gitconfig.File) error {
//		return f.Set("core.editor", "vim")
//	})
//
// Nothing in this package reads the process environment or the home
// directory, except [Discover], and only [Discover] and the methods of [Env]
// touch the filesystem. An Env built by hand is fully described by its
// fields. [Parse], [File] and [Config] do no I/O, for callers that store
// configuration their own way.
//
// Errors wrap the sentinel errors of this package (ErrLocked,
// ErrInvalidKey, ...) in a typed error giving the key, file or value
// concerned; the documentation of the error variables says which function
// returns which type.
package gitconfig
