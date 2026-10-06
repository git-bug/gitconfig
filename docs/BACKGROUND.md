# Background: why a new library

Findings from October 2026, reproduced against go-git `main` (v6 development),
git-bug `trunk` and gopasspw/gitconfig `80b56f9`, compared with git 2.43.0.

## go-git

go-git's `config.Config` is a typed projection of the file plus a parallel
`Raw` representation, re-serialised in full on every write. Parsing is done by
`go-git/gcfg/v2` through callbacks that discard comments and layout.

### Data lost or corrupted on write

1. Comments and blank lines are dropped on every write (git-bug#1022).
   `format/config.Config` has `Comment` and `Includes` fields that are never
   populated.
2. Every write injects `[core] bare = false` / `filemode = true`, even into an
   empty file, moves `[core]` to the end and merges duplicate sections.
3. `pushurl` is merged into `URLs` on read and written back as an extra
   `url = <pushurl>`, so fetches can go to the push URL.
4. Clearing a typed field does not remove the option (`User.Name = ""`,
   `Mirror = false`).
5. Edits made through `Raw` to keys the struct also models are overwritten by
   the struct's values on `Marshal`.
6. Continuation lines are read with an embedded newline and written back as
   `"one \n two"`; git reads `one  two`.
7. `Marshal()` mutates its receiver (`Raw`), a data race when a config is
   shared.

### Values misread

8. Booleans compare against the literal `"true"`/`"false"`: implicit values,
   `yes`, `on`, `1`, `TRUE` read as false; `filemode = 0`/`off`/`no` reads as
   **true** (go-git#2002).
9. Strict validation at load time makes repositories unopenable: a negative
   refspec (`^refs/…`) fails with "malformed refspec" (go-git#1927).
10. Only a handful of keys are modelled (go-git#592), pushing callers to `Raw`
    and into problem 5.

### Scopes and files

11. `[include]` and `[includeIf]` are kept as text but never followed
    (go-git#110).
12. The global loader reads `~/.gitconfig` **or** the XDG file; git reads both.
13. Global and system config cannot be written.
14. `ConfigScoped` flattens scopes, drops `Raw` and provenance, must not be
    written back, and needs a process-global plugin registry (go-git#2224).
15. `pushInsteadOf` is not supported (go-git#865), clone ignores global
    `insteadOf` (go-git#844).

### Concurrency

16. The config is truncated and rewritten in place, without lock: readers can
    see an empty or partial file, concurrent writers corrupt it, git's
    `config.lock` is ignored.
17. Read-modify-write helpers (`CreateRemote`, …) lose concurrent updates
    (5 of 8 concurrent `CreateRemote` calls lost in a test).
18. In-memory storage hands out its stored pointer: data races, and mutations
    that bypass `SetConfig`.
19. Submodule sections are written in random (map) order.
20. In linked worktrees, `config.lock` would be routed to the worktree's git
    dir instead of the common dir.

16–20 are fixed on go-git branch `config-check-and-set` (not merged), together
with a `CheckAndSetConfig` storer method. Open RFCs go-git#2224 and #2002, with
draft PRs #2221 and #2223, aim at replacing the typed model.

## git-bug

`repository/gogit_config.go` works around go-git:

- case-insensitive key normalisation done by hand;
- `RemoveAll` warns that clearing `Raw` does not clear struct-held values;
  missing sections had to be ignored explicitly (git-bug#1455);
- global writes panic (`configPanicWriter`);
- `url.*.insteadOf` resolved by hand (a150cdb0), and the push URL taken as
  `URLs[len-1]`, which only works because of go-git problem 3;
- every operation rewrites `.git/config` and loses comments (git-bug#1022).

## gopasspw/gitconfig

MIT, ~1.7k lines, single maintainer, depends on the whole `gopass` module and
has a package-level `CompatMode` variable. It keeps the file's lines (the right
idea) but parses with line/regexp heuristics and re-renders lines it parsed.
Verified against git:

| Case | gopasspw/gitconfig | git |
|---|---|---|
| unrelated `Set` on a file with `q = "x;y"` | rewrites it as `q = x;y` | then reads `x` |
| `Set("a.k", "x # y")` | writes `k = x # y` unquoted | reads `x` |
| `Set` with leading spaces, quotes, newline | written raw | value mangled, stray line |
| `Set("core.editor")` under `[CORE]` | file not updated, no error | old value |
| `Set("Core.Editor")` | adds a duplicate `[Core]` section | — |
| `k = first` / `k = last` | `first` | `last` |
| value after an include directive is overridden by it | not overridden | included value wins |
| missing include file | load fails | ignored |
| `includeIf "gitdir:/x/work/"` | never matches (swapped arguments, workdir instead of gitdir, no globs) | matches |
| `x\\ny` | real newline | `x\ny` |
| `a "b c" d`, continuation, `[a] # c` header, `[a.B]` header | wrong / lost | handled |
| unset last key | leaves empty `[a]` | removes it |
| `Set` on a multi-valued key | silent no-op if the value is present | error |
| global files | XDG **or** `~/.gitconfig` | both |

Writes are in place with `os.WriteFile`, no lock; the library declares itself
not thread-safe. Its tests (4k lines) did not catch any of the above, which is
the main argument for differential testing against git.
