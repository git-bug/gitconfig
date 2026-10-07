# Git behaviour, verified

Every entry below was observed with **git 2.43.0** on Linux, using
`git config -f <file>` unless stated otherwise; entries marked *(2.47.3)* were
observed with git 2.47.3. These are the first test
vectors; the differential test harness (comparing with `git config`) must reproduce
them and grow far beyond them.

Notation: file content is shown with `\t` for tabs and `⏎` for the end of a
line where it matters. "→" means "git returns" or "the file becomes".

Where the reason for a behaviour is not established, this document says
*observed* and does not guess. Test more inputs against git before
generalising any of them.

## Reading

| Input | Query | git |
|---|---|---|
| `[a]` `k = first` `k = last` | `--get a.k` | `last` (single lookups return the last value) |
| `[a]` `k = x\\ny` (backslash backslash n) | `a.k` | `x\ny` (escaped backslash, then a literal `n`) |
| `[a]` `k = a "b c" d` | `a.k` | `a b c d` (quotes removed, may appear mid-value) |
| `[a]` `k = a    b` | `a.k` | `a    b` (inner whitespace kept; only leading and trailing outside quotes trimmed) |
| `[a]` `k = one \` ⏎ ` two` | `a.k` | `one  two` (continuation: backslash-newline removed) |
| `[a] # c` then `k = v` | `a.k` | `v` (comment after a header) |
| `[a] k = v` | `a.k` | `v` (entry on the header line) |
| `[a.B]` `k = v` | `a.b.k` | `v` (a legacy subsection is lowered, as `--list` shows: `a.b.k`) |
| `[a.B]` `k = v` | `a.B.k` | exit status 1, no output *(2.47.3)* |
| `[CORE]` `Editor = vim` | `core.editor` | `vim` |
| `[a]` `k = "x;y"` | `a.k` | `x;y` |
| `[a]` `k` | `a.k` | empty string; as `--bool`: `true` |
| `[a "s\"q"]` `k = v` | `a.s"q.k` | `v` |
| `[url "https://a.b/"]` `insteadOf = gh:` | `url.https://a.b/.insteadof` | `gh:` (subsection = everything between the first and the last dot) |
| BOM + `[a]\r\n\tk = v\r\n` | `a.k` | `v` (BOM skipped, CR stripped) |
| `[a]` `k = v` | `--get a.missing` | exit status 1, no output |
| `[a ""]` `k = v` `[a]` `k = w` | `--list` | `a..k=v` `a.k=w` (an empty subsection is distinct from none) *(2.47.3)* |

### Types

| Value | `--bool` |
|---|---|
| `yes`, `on`, `1`, `true`, `TRUE`, `2`, `-1` | `true` |
| `0`, `off`, `no`, empty string | `false` |
| no `=` at all | `true` |
| `maybe` | `fatal: bad boolean config value 'maybe' for 'a.k'` |

| Value | `--int` |
|---|---|
| `1k` / `1K`, `1m`, `1g` | `1024`, `1048576`, `1073741824` |
| `3x` | `fatal: bad numeric config value '3x' for 'a.k' in file f: invalid unit` |
| `99999999999999999999` | `fatal: bad numeric config value '…' for 'a.k' in file f: out of range` |
| no `=` at all | `fatal: bad numeric config value '' for 'a.k' in file f: invalid unit` *(2.47.3)* |

| Value | `--type=path` |
|---|---|
| `~/x` | `$HOME/x` |
| `%(prefix)/x` | `/usr/x` (git's compiled-in prefix, unknown to a library: an error here until C9) |
| no `=` at all | `error: missing value for 'a.k'`, then `fatal: bad config line 2` *(2.47.3)* |

### Keys

`git config <key> v` rejects `a.1k`, `a.k_y`, `a.-k`, `a b.k` with
`error: invalid key: <key>`, and `a` with `error: key does not contain a section: a`.
Key names: ASCII letters, digits and `-`, starting with a letter.

## Editing

### Placement and formatting

| Before | Command | After |
|---|---|---|
| `[a]` `k = 1` `[b]` `x = y` `[a]` `j = 2` | `a.new v` | appended after `j = 2`: new keys go at the end of the **last** matching section |
| `[core]` `bare = false` `filemode = true` `[user]`… | `core.editor vim` | `\teditor = vim` appended after `filemode = true` |
| empty file | `Core.Editor vim` | `[Core]` / `\tEditor = vim` — new section and key keep the caller's case |
| empty file | `Remote.Origin.URL u` | `[Remote "Origin"]` / `\tURL = u` |
| `[CORE]` `Editor = nano` | `core.editor vim` | `[CORE]` / `\teditor = vim` — header untouched, the replaced line uses the caller's key spelling |
| `[a] k = v` | `a.k w` | `[a]` / `\tk = w` — the shared line is split |
| `[a]` `k = v` (no final newline) | `a.j w` | `[a]` / `\tk = v` / `\tj = w` |
| `[a]` `k = one \` ⏎ `  two` `z = 1` | `a.k new` | `\tk = new` replaces both physical lines; `z = 1` kept |
| `[a.B]` `k = v` | `a.B.j w` or `a.b.j w` | appended into `[a.B]` in both cases, where it reads back as `a.b.j` *(2.47.3)* — this library writes `a.B.j` into a new `[a "B"]` (E10) |
| `[a.B]` `k = v` | `a.B.k w` | `\tk = w` appended into `[a.B]`: `a.b.k` now has two values *(2.47.3)* — this library adds `[a "B"]` (E10) |
| `[a]` `␣␣␣␣k = v` (spaces) | `a.j w` | existing line untouched, new line indented with a tab |
| `[a]` `k = old ; note` | `a.k new` | `\tk = new` — the trailing comment is dropped |
| `[a]` `k` (implicit) | `a.k false` | `\tk = false` |
| `[a]` `k = v` | `a.k v` (same value) | content unchanged, but the file **is** rewritten (new mtime) |
| `[a]` `␣␣␣␣k=v` | `a.k v` (same value) | `\tk = v` — the line is rewritten in git's format anyway (this library keeps it, E7) *(2.47.3)* |
| empty file | `a..k v` | `[a ""]` / `\tk = v`; a later `a.k w` adds a separate `[a]` *(2.47.3)* |
| new subsection `we"ird\\x` | `remote.<it>.url u` | `[remote "we\"ird\\\\x"]` — `"` and `\` escaped in the header |

### Value quoting on write

| Value | Written as |
|---|---|
| `x # y` | `"x # y"` |
| `semi;colon` | `"semi;colon"` |
| `  pad` | `"  pad"` |
| `trail ` | `"trail "` |
| `say "hi"` | `say \"hi\"` (quotes escaped, no surrounding quotes) |
| `a\b` | `a\\b` |
| tab inside | `tab\tin` |
| leading or trailing tab | `\ttab`, `tab\t` (escaped, no quotes) *(2.47.3)* |
| newline inside | `multi\nline` |
| empty | `k = ` (nothing after `= `) |

Observed rule: surround with quotes when the value starts or ends with a
space or contains `#` or `;`; always escape `\`, `"`, tab and newline.

### Multiple values and missing keys

| Before | Command | Result |
|---|---|---|
| `k = 1` `k = 2` | `--unset a.k` | `warning: a.k has multiple values`, exit 5, file unchanged |
| `k = 1` `k = 2` | `a.k 3` | `error: cannot overwrite multiple values with a single value`, exit 5, unchanged |
| `[a]` `k = 1` `[a]` `k = 2` (two sections) | `a.k 3` | same error — values across sections count together |
| `k = 1` `k = 2` `k = 3` | `--replace-all a.k X '^[12]$'` | `k = X` `k = 3` — matches collapse into one, at the first match's position |
| `k = 1` | `--replace-all a.k X '^9$'` (no match) | `k = 1` `k = X` — added *(2.47.3)* |
| `k = 1` | `--add a.k 2` | `k = 1` `k = 2` |
| `k = v` | `--unset a.missing` | exit 5, unchanged |

### Removing keys and sections

| Before | Command | After |
|---|---|---|
| `[a]` `k = v` `[b]` `x = y` | `--unset a.k` | `[b]` `x = y` — empty section removed |
| `[a]` `# keep` `k = v` `[b]`… | `--unset a.k` | `[a]` `# keep` `[b]`… — kept, a comment remains in it |
| `# before` `[a]` `k = v ; trailing` `[b]`… | `--unset a.k` | `# before` `[a]` `[b]`… — header kept because of the comment before it; the trailing comment plays no part *(2.47.3)* |
| `# before` ⏎ (blank) `[a]` `k = v` `[b]`… | `--unset a.k` | header kept: blank lines between comment and header do not matter *(2.47.3)* |
| `[z]` `q = 1` `# before` `[a]` `k = v` `[b]`… | `--unset a.k` | header kept: a comment after the previous section's last entry counts *(2.47.3)* |
| `# top` `[z]` `q = 1` `[a]` `k = v` `[b]`… | `--unset a.k` | `[a]` removed: a comment above an earlier header does not count *(2.47.3)* |
| `[a]` `k = v` `# after` `[b]`… | `--unset a.k` | `[a]` `# after` kept |
| `[a]` `# c` `k = v` `[b]`… | `--rename-section a z` | only the header changes |
| `[a ""]` `k = v` `[a]` `k = w` | `--remove-section a.` | `[a]` `k = w` — `a.` names the empty subsection *(2.47.3)* |
| `[a ""]` `k = v` `[a]` `k = w` | `--rename-section a. b.x` | `[b "x"]` `k = v` `[a]` `k = w` *(2.47.3)* |
| `[A]` `k = v` | `--remove-section a`, `--rename-section a z` | `fatal: no such section: a`, exit 128: the header text is compared as written *(2.47.3)* — this library matches section names case-insensitively (E10) |
| `[a.X]` `k = v` | `--remove-section a.X` / `a.x` | removed / `no such section` *(2.47.3)* — this library removes it as subsection `x` only (E10) |
| `[c "s"]` `z = 1` | `--rename-section c.s 'c.x⏎y'` | exit 0, writes `[c "x⏎y"]`, which git then refuses: `bad config line 1` *(2.47.3)* — this library refuses the name (X1) |
| `[a]` `k = 1` `[b]` `k = 2` | `--rename-section a b` | `[b]` `k = 1` `[b]` `k = 2`, exit 0: an existing target is not checked, both sections read as one (`--get-all b.k` → `1` `2`); the same for subsections *(2.47.3)* |
| `# top` `[a]` `# c` `k = v` `[b]`… | `--remove-section a` | `# top` `[b]`… — header, inner comment and keys removed; the comment before the header kept |

Observed rule for E5: after an unset, an empty section is removed unless a
comment line lies between the previous entry or header line (or the start of
the file) and the next section header.

### File handling

- Permissions of the replaced file are preserved (`chmod 600`, then a write → still `600`),
  exactly: with umask `022`, a `664` file stays `664` (the umask does
  not apply).
- Symbolic links are followed: `git config -f link a.k w` with `link -> real`
  edits `real`; `link` stays a symlink.
- With `include.path` in `~/.gitconfig`, `git config --global user.name x`
  appends to `~/.gitconfig` itself; the included file is untouched.
- A held lock: `error: could not lock config file .git/config: File exists`,
  exit 255, immediately (no waiting).
- Each write replaces the file through a rename (new inode).

## Scopes

Run with an isolated `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_NOSYSTEM=1`.

| Setup | Result |
|---|---|
| `user.name` in both `$XDG_CONFIG_HOME/git/config` and `~/.gitconfig` | both read, XDG first; `~/.gitconfig` wins. Keys only in the XDG file are still visible (`--show-scope` reports `global`). |
| `GIT_CONFIG_GLOBAL=<file>` | only that file for global scope |
| `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=user.name GIT_CONFIG_VALUE_0=x` | value from scope `command` |
| `git -c user.name=x` | scope `command` |
| `extensions.worktreeConfig = true` + `git config --worktree` | value in `.git/config.worktree`, scope `worktree`, overrides local |
| no `extensions.worktreeConfig`, single worktree, `git config --worktree x.y z` | succeeds (exit 0), writes to `.git/config` |
| `git config -f f --show-scope --show-origin --list` | `command	file:f	a.k=v` — files read with `-f` have scope `command` |
| no `extensions.worktreeConfig`, single worktree, `git config --worktree --show-scope --list` | the entries of `.git/config`, scope `local` *(2.47.3)* |
| no `extensions.worktreeConfig`, two worktrees, `git config --worktree --list` | `fatal: --worktree cannot be used with multiple working trees unless the config extension worktreeConfig is enabled…`, exit 128 *(2.47.3)* |
| missing `~/.gitconfig` / `config.worktree` / `-f` file, `--get a.k` | exit 1, no output: an absent file reads as empty *(2.47.3)* |
| missing `~/.gitconfig` / `config.worktree` / `-f` file, `--list` | `fatal: unable to read config file '<file>': No such file or directory`, exit 128 *(2.47.3)* |
| outside a repository, `git config --local --list` | `fatal: --local can only be used inside a git repository`, exit 128 *(2.47.3)* |

## Includes

git follows includes by default only when it reads every scope (a plain
`git config <key>` in a repository, and every other git command). With `-f`,
`--global`, `--system` or `--local` it needs `--includes`; without it, the
include directives are listed as ordinary entries and nothing is read. The
differential tests must pass `--includes` in those modes.

Every row below, and the `includeIf` table, was re-run with git 2.47.3 with
an isolated `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_NOSYSTEM=1`, and gave
the same results.

| Setup | Result |
|---|---|
| `[user] name = home` then `[include] path = sub/inc` | with `--includes`: `inc`, the included value wins (later position); `-f` without `--includes`: `home` |
| `[include] path = ~/tinc` then `[user] name = after` | `after` wins; `~` expanded |
| `[include] path = missing` | silently ignored |
| relative path in `.git/config` | relative to `.git/` (origin shown as `file:.git/lc`) — read without a scope option; `--local` without `--includes` does not follow it |
| a file including itself | `fatal: exceeded maximum include depth (10) while including` / `\t<path>` / `from` / `\t<path>` / `This might be due to circular includes.` (one message over five lines, 2.47.3) |
| `path` is a directory | `warning: unable to access 'd': Is a directory`, then `fatal: bad config line 2` *(2.47.3)* |
| `path` is a FIFO | hangs until something writes to the FIFO *(2.47.3)* — this library refuses it (X3) |
| `path = /dev/zero` | `fatal: bad config line 1 in file /dev/zero` (NUL is a syntax error) *(2.47.3)* |
| 7 files, each including the next 10 times, the last holding `a.k` | no limit other than depth: 10ⁿ values for n levels — n = 3: 1,000 values in 0.01 s; n = 6: 1,000,000 in 7 s; n = 10 would be ~10¹⁰ values, hours of work and memory exhaustion from 3 KB of files *(2.47.3)* — this library stops at 1000 files or 64 MiB read per load (X3) |

### `includeIf` (repository at `$S/work/proj`, branch `main`)

| Condition | Included? |
|---|---|
| `gitdir:$S/work/` | yes (trailing `/` means `/**`) |
| `gitdir:$S/work` | no |
| `gitdir:$S/work/**` | yes |
| `gitdir:$S/work/proj/.git` | yes |
| `gitdir:work/` | yes (relative pattern gets an implicit `**/` prefix) |
| `gitdir/i:<upper-cased path>/WORK/` | yes |
| `gitdir:~/../work/` with `HOME=$S/home` | **no**: `.` and `..` in the pattern are compared literally, never normalised — `gitdir:$S/home/../work/` and `gitdir:$S/./work/` do not match either *(2.47.3)* |
| `onbranch:main`, `onbranch:ma*` | yes |
| `onbranch:other` | no |
| `hasconfig:remote.*.url:https://example.com/**` | no until a matching remote is configured, then yes |
