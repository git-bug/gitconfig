# Requirements

Priorities: **MUST** blocks the first release, **SHOULD** is expected soon
after, **LATER** is wanted eventually, **WON'T** is out of scope on purpose.

The reference is the `git` binary. Behaviour marked *verified* has a test
vector in [GIT-BEHAVIOUR.md](GIT-BEHAVIOUR.md). Where the library departs
from git, and the principles that decide it, are in
[COMPATIBILITY.md](COMPATIBILITY.md).

## 1. Correctness — git is the specification

| ID | Requirement | Prio |
|---|---|---|
| C1 | Parsing matches git: section/subsection/key syntax and case rules, legacy `[a.B]` headers, an empty subsection (`[a ""]`, key `a..k`) distinct from none, quoting and escapes, `#`/`;` comments, continuation lines, implicit (`=`-less) values, entries on the header line, BOM, CRLF. *verified* | MUST |
| C2 | A file git rejects is rejected, with file name and line number. A file git accepts is accepted. No silent skipping of lines, with one exception: an entry before any section header, which git reads under a key with no section that no command can ask for, is kept in the file (edits handle it as git does) but is not part of `Config`. A header whose subsection holds a NUL byte, which git cuts at the NUL, is rejected. *verified* | MUST |
| C3 | Lookup of a single value returns the **last** value, across files and scopes, as git does. *verified* | MUST |
| C4 | Multi-valued keys keep all values in file order. | MUST |
| C5 | Typed interpretation matches git: bool (`true/yes/on/1/any non-zero int`, `false/no/off/0/""`, implicit = true, case-insensitive, anything else is an error), int with `k/m/g` suffixes and range errors, path (`~/`; `%(prefix)/` is an error, as it depends on how the git binary was built and is recorded nowhere a library can read, see C9). Every read treats an invalid key as absent; only edits and `ParseKey` report it (`ErrInvalidKey`). A typed getter that fails returns its default. `Config` and `Entry` are plain data: `Config.Path` and `Entry.Path` take the home directory, and `Config.Path` expands its default too. *verified* | MUST |
| C6 | Typed interpretation is on demand: a value that is not a valid bool only errors when read as a bool, as in git. Loading never fails because of a value's type. | MUST |
| C7 | Colour and expiry-date types. | LATER |
| C8 | `url.<base>.insteadOf` / `pushInsteadOf` longest-match rewriting and `<section>.<url>.<key>` URL matching (`git config --get-urlmatch`, used by `http.<url>.*`). | SHOULD |
| C9 | `%(prefix)/` paths expanded with a caller-supplied prefix. | LATER |

## 2. Lossless editing

| ID | Requirement | Prio |
|---|---|---|
| E1 | Parsing then serialising an unedited file reproduces it **byte for byte**, including comments, blank lines, indentation, quoting style, invalid-but-accepted oddities and a missing final newline. | MUST |
| E2 | An edit changes only the lines git's own edit would change, and produces the same bytes git produces. *verified* for the cases in GIT-BEHAVIOUR.md | MUST |
| E3 | The editing operations of `git config`: set (error on multiple values), `--add`, `--replace-all`, `--unset` (error on multiple), `--unset-all`, `--remove-section`, `--rename-section`. | MUST |
| E4 | Values are quoted and escaped on write exactly as git does; any Go string that git can store round-trips; a value git cannot store (containing NUL) is rejected with `ErrInvalidValue`. *verified* | MUST |
| E5 | Empty-section cleanup after unset matches git: removed unless a comment lies between the previous entry or header and the next section (so a comment just above the header keeps it too). *verified* | MUST |
| E6 | Writes never go into an included file; they go to the file being edited, as git does. *verified* | MUST |
| E7 | Setting a key to the value it already has leaves its line as it is (git rewrites it in its own format, e.g. `    k=v` → `\tk = v`), and committing an edit that leaves the content unchanged does not touch the file (git always rewrites it). A deliberate improvement over git, asked for by git-bug#1022: a line nobody changed is kept byte for byte. An implicit entry is never "already set" (it reads as true, `k = ` as false). *verified* | SHOULD |
| E10 | Edits find keys, sections and subsections by their canonical form, as reads do: section and name case-insensitive, subsection exact, a legacy header `[a.B]` holding subsection `b`. A deliberate difference with git, which places a key with an upper-case subsection into a matching legacy header (where it reads back lowered, and can make a key multi-valued, breaking X1) and removes or renames a section only when the header is spelled as given. *verified* | MUST |
| E8 | Adding comments (`git config --comment`, git ≥ 2.45). | LATER |
| E9 | Value patterns for set, `--replace-all`, `--unset` and `--unset-all`: regexp, negated regexp, `--fixed-value`, with Go's linear-time regexp engine (X2). Neither git-bug nor go-git uses them. | LATER |

## 3. Scopes and includes

| ID | Requirement | Prio |
|---|---|---|
| S1 | Scopes system, global, local, worktree, command, with git's precedence (later wins). | MUST |
| S2 | Global is **both** `$XDG_CONFIG_HOME/git/config` (default `~/.config/git/config`) and `~/.gitconfig`, read in that order. *verified* | MUST |
| S3 | `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`, `GIT_CONFIG_NOSYSTEM`, `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_<n>`/`GIT_CONFIG_VALUE_<n>`, `GIT_CONFIG_PARAMETERS` (`-c`). *verified for the first and the count form* | MUST |
| S4 | Worktree scope (`config.worktree`) when `extensions.worktreeConfig` is true. Without the extension, reading or writing the worktree scope uses the local config (entries reported as `local`) if the repository has a single worktree, and fails if it has several. *verified* | MUST |
| S5 | Each value carries its origin (scope, file, line) — the information of `git config --show-origin --show-scope`. Values read from an explicit file (`-f`, `Parse`) have their own scope, `ScopeFile`, where git reports `command`, so that untrusted content is never mistaken for the protected command scope; a file opened for a scope has that scope. File names are as git opened them (an include's relative path joined to the including file's directory, not cleaned). *verified* | MUST |
| S6 | `include.path`: spliced at the position of the directive (position decides precedence), relative to the including file, `~` expanded, missing files ignored, depth limit 10 with git's error. An `include.path` given on the command line is followed; a relative one is an error, as in git. *verified* | MUST |
| S7 | `includeIf` conditions `gitdir:`, `gitdir/i:` (matched against the git directory as given and its real path, as git does; the git directory must be absolute; glob matching where `*` and `?` stop at `/` and `**/` spans directories, implicit `**/` prefix for relative patterns and `/**` suffix for a trailing `/`), `onbranch:`. The current branch is read from `HEAD` when needed. *verified* | MUST |
| S10 | `onbranch:` for repositories whose `HEAD` is not a plain file (reftable, in-memory storage), with a branch supplied by the caller. | LATER |
| S11 | Reading one scope whose file is absent (no system file, missing global or worktree file) gives an empty configuration, as `git config --get` does (`--list` fails there). Reading the local or worktree scope outside a repository fails, as in git. *verified* | MUST |
| S8 | `includeIf "hasconfig:remote.*.url:…"`, including git's rule that files it includes may not define remote URLs. *verified* | SHOULD |
| S9 | Protected configuration: some keys (e.g. `safe.directory`) only honoured from system, global and command scopes. See X5. | MUST |

## 4. Writes and concurrency

| ID | Requirement | Prio |
|---|---|---|
| W1 | Writes use git's lock-file protocol: create `<file>.lock` exclusively, write, rename over `<file>`. Readers never see a partial file; a concurrent `git` and this library exclude each other. | MUST |
| W2 | A held lock is reported as a distinct error immediately (git does not wait). No automatic removal of stale locks. | MUST |
| W3 | Read-modify-write is atomic: the file is re-read **after** taking the lock and the edit applied to that content. No lost updates. | MUST |
| W4 | The replaced file keeps its permission bits exactly, regardless of the umask. *verified* | MUST |
| W5 | Configuration snapshots (`Config`) are immutable and safe to share between goroutines without locking. The editable `File` is not shared. | MUST |
| W6 | Optional bounded wait for a held lock (zero fails immediately, as git). Programs running next to git will meet held locks. go-git has its own lock helper and would not use this. | LATER |
| W7 | Writes follow symbolic links, as git does: the link's target is locked and replaced, the link is kept (dotfile managers symlink `~/.gitconfig`). *verified* | MUST |

## 5. Engineering constraints

| ID | Requirement                                                                                                                                                                                                          | Prio |
|---|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---|
| N1 | Standard library only. Just testify for tests.                                                                                                                                                                       | MUST |
| N2 | No global mutable state, no `init()` side effects, no panics in library code (go-git rules).                                                                                                                         | MUST |
| N3 | Only `Discover` reads the process environment (variables, home directory, platform defaults). Everything else uses what an `Env` was given. | MUST |
| N4 | `Parse`, `File` and `Config` do no I/O: a caller with its own storage (go-git, in-memory repositories) reads the bytes, takes git's lock itself, edits and writes `File.Bytes()`. `Env` reads and writes the operating system's filesystem only. | MUST |
| N9 | Loading with includes from a caller's filesystem (go-billy), through a single read-file function. | LATER |
| N5 | Go version no newer than the oldest of git-bug's and go-git's `go.mod` (both `go 1.26.0` today).                                                                                                                     | MUST |
| N6 | Works on Linux, macOS and Windows (path handling, CRLF, rename-over-existing).                                                                                                                                       | MUST |
| N7 | MIT licence, compatible with go-git (Apache-2.0) and git-bug. No code derived from git (GPL-2.0-only): see [CLEAN-ROOM.md](CLEAN-ROOM.md). | MUST |
| N8 | Errors are sentinel values wrapped in typed errors (`KeyError`, `FileError`, `SyntaxError`, `ValueError`, `EnvError`) usable with `errors.Is`/`errors.As`, and carry file/line/key context like git's messages. Which method returns which is listed in `errors.go`.                                                                        | MUST |

## 6. Security

Configuration is often written by someone else: a repository on a shared
disk or unpacked from an archive (CVE-2022-24765), a `.gitmodules` blob from
a remote, any file reached through `include.path`. Reading it must be safe,
and callers must be able to tell whether to trust it.

| ID | Requirement | Prio |
|---|---|---|
| X1 | No injection through edits: whatever key, subsection and value an edit is given, the edited file parses to the previous entries with exactly that change. A subsection containing a newline or NUL is rejected (as by git). Sections to remove or rename are found by parsing, where git scans lines: git takes a line starting with `[` inside a continued value for a header (removing the entries after it, or rewriting the value), and misses a header after a BOM or after another header on the same line. A file holding only a BOM keeps it first, where git writes the new section before it and the file no longer parses. *verified* Fuzzed: parse ∘ edit ∘ parse, for key and section edits. | MUST |
| X2 | Parsing untrusted input never panics and is linear in time and memory. Parser and edits are fuzzed continuously. | MUST |
| X3 | Bounded loads: git's include depth limit, plus at most 1000 files, 4 MiB and 2¹⁶ entries and distinct section headers read by one load, counting every read (an include fan-out of 10 per level makes git read 10ⁿ files from a few KB, *verified*; an entry costs about a hundred times its size in memory, so the entry count, not the bytes, bounds memory); `Parse` and edits apply the same limits to their one file; configuration files and includes must be regular files (git hangs on a FIFO and fails on a directory, *verified*). A deliberate improvement over git, reached only by files built to reach it. | MUST |
| X4 | Errors do not leak or spoof: a syntax error never quotes the file's content (an include may point at a secret file), values in messages are quoted with `%q`, and paths, which a configuration file can choose, have their control characters (newline and tab included), bidirectional controls and non-UTF-8 bytes replaced with `?`: stricter than git 2.47.3 and 2.56.0, which replace only C0 controls but tab and newline, and DEL (*verified*); no terminal escape sequences, forged lines or disguised paths. | MUST |
| X5 | Trust is checked before the repository is read, as git does: `Discover` refuses (`ErrUntrusted`) a repository that is neither owned by the current user (on Unix, also the `SUDO_UID` user for root; on Windows, the Administrators group for an administrator) nor listed in `safe.directory`, with git's rules for which paths are checked and listed. `safe.directory` is read from the protected scopes only (`Env.LoadProtected`: system, global, command; never `Parse` content or an explicit file), never from the repository's configuration. An `Env` filled by hand is not checked, as git does not check `GIT_DIR`. Each entry's scope is known. *verified* | MUST |
| X6 | Writes cannot be redirected through the lock file: it is created with `O_CREATE\|O_EXCL` and never through a symbolic link. Following a link at the config path itself is git's behaviour (W7) and is kept; the documentation says that only X5 protects against a hostile `.git` directory. | MUST |

## 7. Won't

- No typed projection of the config into a big struct (that is the source of
  go-git's round-trip bugs). Domain helpers (remotes, branches) can be built on
  top, outside the core.
- No support for non-git config dialects or arbitrary formatting choices
  (gopasspw/gitconfig issues #16 and #18).
- No invention of behaviour git does not have, except the departures listed
  in [COMPATIBILITY.md](COMPATIBILITY.md).
- No `git config --edit`-style interactive features, no `--global` writes
  without the caller choosing the file.
