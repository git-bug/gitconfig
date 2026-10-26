# Compatibility with git: choices and trade-offs

git is the reference ([REQUIREMENTS.md](REQUIREMENTS.md)), but git has bugs,
quirks and behaviour that suits a command-line tool rather than a library.
This document records, case by case, where this library follows git, where it
departs from it, and why. The observations behind each case are test vectors
in [GIT-BEHAVIOUR.md](GIT-BEHAVIOUR.md).

## Principles

1. **Reads agree with git.** Loading gives the entries, values and includes
   git uses, even where git's parsing is odd but stable (octal integers, a
   value cut at a NUL byte). Programs using this library act next to git:
   disagreeing on the effective configuration is worse than an odd parse.
   Two exceptions: a git bug fixed in a later release is followed as fixed, and loads
   are bounded (X3).
2. **Writes produce git's bytes**, except where git loses or corrupts data,
   or writes a file it cannot read back (E7, E10, X1).
3. **Failing where git works has a cost.** It is acceptable when the library
   cannot give git's answer (the prefix git was installed under), or
   when the input is hostile.
4. **Rare, obscure or debatable cases take the safe option** — refuse, report
   an error, read less — and are revisited if users meet them. Such choices
   are marked *safe default* below.
5. **A quirk is established, not assumed.** Before calling a git behaviour a
   bug, check git's documentation and release notes, and try a recent release, not
   only the installed one: git 2.47.3 rejects the minimum integer, which git
   later fixed as a bug.

## Reading

| Case | git | This library | Why |
|---|---|---|---|
| Entry before any section header | read under a key with no section, which no command can ask for | kept in the file (edits around it behave as git's), not part of `Config` | failing would refuse files git reads; the key cannot be addressed (C2) |
| NUL byte in a value | the value is cut at the NUL | the same | 1 |
| NUL byte in a header's subsection | the key is cut at the NUL, so the file reads as a different key | the file is refused | hostile input; agreeing means reading a key the file does not spell (C2) — *safe default* |
| Legacy header `[a.B]` | subsection lowered to `b` | the same | 1 |
| Header `[ "sub"]` (empty section) | read as `.sub.k` | the same | 1 |

## Typed values

| Case | git | This library | Why |
|---|---|---|---|
| `010`, `0x10`, `" 5"` as an integer | 8, 16, 5 (`0` and `0x` prefixes) | the same | 1 |
| Minimum value (`-9223372036854775808` as an int, `-2147483648` as a bool) | rejected by 2.47.3, accepted by 2.55.0 | accepted | 1: a bug fixed in a later release |
| Case of `true`, `yes`, `k`, … | ASCII only | ASCII only | the format is ASCII; Go's Unicode folding would accept `yeſ` or the Kelvin sign |
| `%(prefix)/` in a path | the prefix of the git binary: set at build time, or, for a relocatable build such as Git for Windows, worked out from where the executable is | error | it depends on how the git binary was built and is recorded nowhere a library can read, and which git will run is unknown too; a wrong guess would silently pick another file; the caller can supply it (C9) |
| `~user/` in a path | that user's home | error | needs the system's user database — *safe default* |

## Includes and conditions

| Case | git | This library | Why |
|---|---|---|---|
| Include depth | at most 10 | the same | 1 |
| Include fan-out, FIFOs, devices | unbounded work, hangs on a FIFO | at most 1000 files, 4 MiB and 2¹⁶ entries per load; only regular files | X3 |
| Missing include | ignored | the same | 1 |
| Global file not readable (permission) | skipped, with a warning | skipped; also the system file in `LoadProtected` | 1 |
| `.` and `..` in a `gitdir:` pattern | compared as written, never normalised | the same | 1 |
| Relative `gitdir:./` from the command line | prints an error, goes on without the include | `ErrNotAbsolute` | no one to warn — *safe default* |
| Relative `include.path` from the command line | error | `ErrNotAbsolute` | 1 |
| Windows: `GIT_DIR` written with `\`, pattern with `/`, through a symbolic link | the path as given does not match | matches | looks like a git for Windows bug |
| `hasconfig:remote.*.url:` | supported | the same | 1 |
| `hasconfig:remote.*.url:` met, remote URL in a file included by a `gitdir:` or `onbranch:` condition | fatal error, as for one included by `hasconfig:` itself | the same | 1, though it looks like a git bug |
| `hasconfig:remote.*.url:` met, implicit `remote.<name>.url` (no `=`) | segmentation fault | not a URL: matches nothing | git gives no answer |
| `onbranch:` with reftable | supported | treated as false | needs the ref store (S10) |

## Scopes and discovery

| Case | git | This library | Why |
|---|---|---|---|
| Scope of a file read on its own (`-f`) | `command` | `ScopeFile` | a file from anyone is never mistaken for the protected command scope |
| `--global` reads | only the file it writes to | `LoadScope(ScopeGlobal)` reads every global file, as `Load` does | one meaning for "the global layer" |
| `HOME` unset | the XDG file if `XDG_CONFIG_HOME` is set, no `~/.gitconfig`, no error | the same | 1, 3 |
| `GIT_CONFIG_COUNT=+1`, `' 1'`, `-0` | accepted | error | nobody writes them — *safe default* |
| System file | set at build time (`$(prefix)/etc/gitconfig`) | `/etc/gitconfig` on Unix, none elsewhere | a git built with another prefix reads another file; callers can set `SystemConfig` |
| `safe.directory` entry that does not resolve | skipped | the same | 1 |
| `safe.directory = .` | trusted at the top of a repository | skipped | needs the working directory — *safe default* |
| Repository ownership check | done when git finds the repository, not for `GIT_DIR` | done by `Discover`, not for an `Env` filled by hand | 1 (X5) |
| What is checked, and listed in `safe.directory` | depends on where git starts: a working tree, or a git directory | the same, from the path given to `Discover` | 1 — the caller should pass the working tree, which users list |
| Untrusted repository | `git config` silently skips the local scope; other commands fail | `Discover` fails with `ErrUntrusted` | a silently missing scope surprises; reading less is not safer than refusing — *safe default* |
| `Discover` given a directory that is not a repository | git searches the parent directories, and fails with "not a git repository" when there is none | `ErrNotRepository`: a git directory must hold a `HEAD` file, and its common directory `objects/` and `refs/` directories, as for git, which also requires `HEAD` to hold a ref or an object name | `Discover` does not search; reading any directory's `config` as the local scope would read and write a file that is not git's — *safe default* |
| `SUDO_UID` = `' 1000'`, `+1000`, or beyond 32 bits | accepted; a value beyond 32 bits wraps around (2³² + 1000 is 1000) | ignored: digits only, at most 32 bits | the wrap-around trusts a user nobody named — *safe default* |
| Administrators group on Windows | trusted for an administrator (Git for Windows commit messages, 2022 and 2025) | the same | 1 — not observed: no Windows git to run but CI's |
| UNC paths on Windows | must be listed in `safe.directory` since Git for Windows 2.54 (release notes) | owner checked like any path | the release note is not precise enough to follow; not observed |
| Paths on Windows | some written with `/` (`HOME`), some with `\` | the caller's separators, kept | matching git's spelling serves no one |

## Editing

| Case | git | This library | Why |
|---|---|---|---|
| Setting the value a key already has | the line is rewritten in git's format, the file replaced | nothing written | E7 |
| Section and key case, legacy `[a.B]` | headers matched as written by section edits; keys placed into `[a.B]` | canonical form everywhere | E10 |
| A line starting with `[` inside a continued value | taken for a header: entries removed, values rewritten | not a header | X1 |
| A header after a BOM, or after another header on its line | not seen by section edits | seen | X1 |
| New key in a file holding only a BOM | written before the BOM: the file no longer parses | written after it | X1 |
| New line after a value continued into the end of the file | joins that value | the value is ended first | X1 |
| Subsection with a newline in a rename | written, then unreadable | `ErrInvalidKey` | X1 |
| Any edit whose result would not parse | written | refused, file unchanged | X1 |
| Key with an empty section (`.sub.k`) | written under `[ "sub"]` | `ErrInvalidKey` (reads still accept it) | nobody means it — *safe default* |
| Comment at the end of a replaced line | dropped | the same | the comment describes the old value |
| Comment just before the next header, on `--remove-section` | removed with the section | the same | which section it belongs to is ambiguous; git's unset rule (E5) treats it the same way |
| Rename onto an existing section | not checked: both read as one | the same; callers can check `Config` first | 2 |

## Files and locking

| Case | git | This library | Why |
|---|---|---|---|
| Held lock | fails at once | `ErrLocked` at once | 1 |
| Symbolic link at the edited path | followed, link kept | the same (W7) | dotfile managers rely on it |
| More than 5 levels of symbolic links | replaces the 5th link with a regular file | error | git destroys a link it meant to follow |
| Permissions | kept exactly | the same | 1 |
| Content unchanged by the edit | file rewritten | not written | E7 |
| File larger than 4 MiB, or with more than 2¹⁶ entries | edited | `ErrLoadLimit` | X3 — *safe default* |
| Windows: another process holding the file for a moment | — | open and rename retried for up to a second | concurrent writers would otherwise fail |

## Adding a case

Observe git (recent release and the installed one), check its documentation
and release notes, decide with the principles above, then add a vector to
GIT-BEHAVIOUR.md, a row here, and tests: unit tests for the library's choice,
interop tests only where git's answer does not depend on its version.
