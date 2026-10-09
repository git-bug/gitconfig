# Integration

## git-bug (primary consumer)

### Today

`repository/gogit_config.go` implements git-bug's `Config` interfaces
(`repository/config.go`) on top of go-git:

- reads and writes through `cfg.Raw` of go-git's `config.Config`, then
  `repo.SetConfig(cfg)`, which re-serialises the whole file — comments and
  formatting are lost on every write (git-bug#1022), and edits to keys that
  go-git also models in its struct can be silently reverted;
- global config is read with go-git's deprecated `config.LoadConfig` (one
  file only, no includes) and **cannot be written** (`configPanicWriter`);
- `AnyConfig` merges local over global by hand; system scope and includes are
  ignored;
- `resolveRemote` re-implements `url.*.insteadOf`;
- commit identity in `StoreSignedCommit` comes from go-git's
  `cfg.Author`/`cfg.Committer` of the local config only, so `user.name` set
  globally or through an include is not used.

Usage outside `repository/`: `ReadString` ×5, `RemoveAll` ×4, `ReadAll` ×3,
`StoreString` ×2, `ReadBool`, `ReadTimestamp`, `StoreTimestamp` ×1 each.

### Mapping

`env, err := gitconfig.Discover(gitDir)` once per repository (a `.git`
file of a linked worktree is followed); the `Env` is a plain value and can be
kept.

| git-bug | gitconfig |
|---|---|
| `LocalConfig()` | `env.UpdateScope(gitconfig.ScopeLocal, fn)` (write), `env.LoadScope(gitconfig.ScopeLocal)` (read) |
| `GlobalConfig()` | `env.UpdateScope(gitconfig.ScopeGlobal, fn)` (write — no more panic writer), `env.LoadScope(gitconfig.ScopeGlobal)` (read: XDG file and `~/.gitconfig`, with includes) |
| `AnyConfig()` | `env.Load()`: system + global + local + worktree + command, with includes |
| `ReadString(key)` | `cfg.GetAll(key)`: 0 → `ErrNoConfigEntry`, >1 → `ErrMultipleConfigEntry` (git-bug's contract is stricter than git's last-wins; keep it in the adapter, or relax it to `cfg.Get` / `cfg.Value`) |
| `ReadBool(key)` | same, then `Entry.Bool()` — fixes `yes`/`on`/`1`/implicit values, which `strconv.ParseBool` gets wrong |
| `ReadTimestamp(key)` | same, then `strconv.Atoi` as today |
| `ReadAll(prefix)` | `cfg.All()` filtered by git-bug's prefix rule |
| `StoreString(key, v)` | `env.UpdateScope(s, func(f *gitconfig.File) error { return f.Set(key, v) })` |
| `StoreBool` / `StoreTimestamp` | `Set` with `strconv.FormatBool` / `strconv.Itoa` |
| `RemoveAll(prefix)` | one `UpdateScope`: `RemoveSection` / `RemoveSubsection` for whole sections or subsections matched, `UnsetAll(key)` for single keys; absent ones are not an error (the methods return what they removed), as git-bug#1455 requires |
| hand-written key case normalisation | `gitconfig.ParseKey` |
| in-memory config (`config_mem.go`) | a `*gitconfig.File` (zero value or `Parse`) edited directly, read through `File.Config()` |
| `resolveRemote` insteadOf logic | `url.<base>.insteadOf` helper (C8) once available |
| commit identity | `user.name`/`author.name`/`committer.name` looked up in `env.Load()` |

Effect for git-bug users: comments and layout preserved, `git bug` only
rewrites the file when a value actually changes (E7), global config is
writable, `includeIf`-based identities work, and a concurrent `git` command
cannot corrupt the file or lose an update.

Remotes, branches and refspecs can stay on go-git's API for now: they are
read-only uses there, except `CreateRemote` in tests.

## go-git (secondary target)

### Fit with go-git's rules

- Standard library only, no global state, no `init()`, no panics (AGENTS.md).
- Licence: MIT, compatible with Apache-2.0.
- go-git keeps the repository on go-billy (often rooted at the repository,
  or in memory) and already has git's lock protocol for references
  (`dotgit.lockFile`: exclusive create, timeout, Windows retries, fallback
  when rename is unsupported). It uses `Parse`, `File` and `Config`, which do
  no I/O (N4), and keeps its own locking and file routing.
  `RepositoryFilesystem` must route `config.lock` like `config`, as it
  already does for the references' lock files.
- `Env` reads the operating system's filesystem only. It fits go-git's
  system and global scopes, which go-git reads from the real disk today.
  Includes of a repository config on go-billy need N9 (LATER).
- In-memory storage and reftable have no plain `HEAD` file:
  `includeIf "onbranch:"` needs S10 (LATER) there.
- Go version aligned with go-git's `go.mod`.

### Fit with RFC #2224 and #2002

go-git's open RFCs propose making `plumbing/format/config` the model, a
`Getter` / `ConfigSet` read API, an `xconfig.Combined` helper for scopes, and
retiring the typed `config.Config`. This library provides the same concepts
with the lossless file model and git-exact editing those RFCs do not cover:

| RFC concept | gitconfig |
|---|---|
| `format/config.Config` (one parsed file) | `*gitconfig.File` |
| `Getter` (`Lookup`, `GetAll`) | `*gitconfig.Config` (`Get`, `GetAll`, `All`, `Section`, `Subsection`, `Subsections`) |
| `ConfigSet` | `*gitconfig.Config` returned by `Env.Load` |
| `xconfig.Combined` | `gitconfig.Env` + `Env.Load` |
| typed getters with defaults (#2002) | `Config.Bool/Int(key, def)`, `Config.Path(key, home, def)`, `Entry.Bool/Int/Path` |
| write path (not covered by the RFCs) | `File` edits and `File.Bytes()` under go-git's lock; `Env.UpdateFile` / `Env.UpdateScope` on the real disk |

If go-git adopts it, align the type names with whatever the RFC settles on
before 1.0.

### Transitional path inside go-git

Adoption does not need the full RFC first:

1. **Writes** (fixes git-bug#1022 for every go-git user): in
   `storage/filesystem.ConfigStorage.SetConfig`, instead of writing
   `cfg.Marshal()`, take `config.lock` with go-git's lock helper, read
   `config` under the lock and `Parse` it, compare the entries of `cfg` with
   those of the parsed file, apply the difference as `Set`/`ReplaceAll`/`UnsetAll`/`RemoveSection`/`RemoveSubsection` operations. Comments, layout
   and unknown keys survive and the write becomes atomic. Concurrent updates
   to keys the caller did not change survive too; a concurrent change to the
   same key is still last-writer-wins, as with `git config`. Then write
   `File.Bytes()` to the lock file and rename it, only if it changed.
2. **Reads**: build `config.Config` from a `gitconfig.Config` (from
   `File.Config()`; following includes from go-billy needs N9) instead of gcfg,
   which fixes booleans and continuation lines.
3. **Scopes**: replace the config loader plugin and `ConfigScoped` with
   `gitconfig.Env` and `Env.Load` (removes the process-global plugin
   registry). This works as is for a repository on the real disk; for
   in-memory storage, the local `File.Config()` would have to be combined
   with the other scopes, which the API does not offer yet.
4. **API**: expose `gitconfig.Config` per RFC #2224 and deprecate the typed
   struct.

go-git-specific needs to keep in mind: `url.insteadOf`/`pushInsteadOf`
(go-git#865, #844), `http.<url>.*` matching (go-git#474), `safe.directory`
(`Env.SafeDirectory`, plus an ownership check go-git must do itself), `core.sharedRepository` permissions on written files
(go-git#843, #1572) — set by go-git's own write path.
