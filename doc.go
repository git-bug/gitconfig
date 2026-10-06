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
//     edits them under git's lock.
//
// Reading the effective configuration of a repository and changing a value:
//
//	env, err := gitconfig.Discover(".git")
//	if err != nil { ... }
//
//	cfg, err := env.Load()
//	if err != nil { ... }
//	name, ok := cfg.Get("user.name")
//
//	err = env.UpdateScope(gitconfig.Local, func(f *gitconfig.File) error {
//		return f.Set("core.editor", "vim")
//	})
//
// Nothing in this package reads the process environment, the home directory
// or the real filesystem, except [Discover]. An Env built by hand is fully
// described by its fields.
package gitconfig
