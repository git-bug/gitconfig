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
| `[a]` `k = x` NUL `y` | `a.k` | `x`: the value is cut at the NUL *(2.47.3)* |
| `[a "s\"q"]` `k = v` | `a.s"q.k` | `v` |
| `[url "https://a.b/"]` `insteadOf = gh:` | `url.https://a.b/.insteadof` | `gh:` (subsection = everything between the first and the last dot) |
| BOM + `[a]\r\n\tk = v\r\n` | `a.k` | `v` (BOM skipped, CR stripped) |
| `[a]` `k = v` | `--get a.missing` | exit status 1, no output |
| `[a ""]` `k = v` `[a]` `k = w` | `--list` | `a..k=v` `a.k=w` (an empty subsection is distinct from none) *(2.47.3)* |
| `k = v` `[a]` `j = w` | `--list` | `k=v` `a.j=w`: an entry before any header is read under a key with no section, which no command can ask for *(2.47.3)* — this library leaves it out of `Config` (C2) |

### Types

| Value | `--bool` |
|---|---|
| `yes`, `on`, `1`, `true`, `TRUE`, `2`, `-1` | `true` |
| `0`, `off`, `no`, empty string | `false` |
| no `=` at all | `true` |
| `maybe` | `fatal: bad boolean config value 'maybe' for 'a.k'` |
| `-2147483648` | `fatal: bad boolean config value …` *(2.47.3)*; `true` *(2.55.0)*: git 2.47.3 rejects the minimum of the range, a bug fixed in a later release — this library accepts it |
| `yeſ` (long s) | `fatal: bad boolean config value …`: only ASCII letters fold *(2.47.3)* |

| Value | `--int` |
|---|---|
| `1k` / `1K`, `1m`, `1g` | `1024`, `1048576`, `1073741824` |
| `010`, `0x10`, `" 5"` (quoted) | `8`, `16`, `5`: a leading `0` means octal, `0x` hexadecimal *(2.47.3)* |
| `3x` | `fatal: bad numeric config value '3x' for 'a.k' in file f: invalid unit` |
| `99999999999999999999` | `fatal: bad numeric config value '…' for 'a.k' in file f: out of range` |
| `-9223372036854775808` | `… out of range` *(2.47.3)*; `-9223372036854775808` *(2.55.0)*: the same bug, fixed in a later release — this library accepts it |
| `1K` (Kelvin sign) | `… invalid unit` *(2.47.3)* |
| no `=` at all | `fatal: bad numeric config value '' for 'a.k' in file f: invalid unit` *(2.47.3)* |

| Value | `--type=path` |
|---|---|
| `~/x` | `$HOME/x` |
| `%(prefix)/x` | `/usr/x`: the git binary's prefix, set at build time or, for a relocatable build, from the executable's location (an error here until C9) |
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
| empty file | `.sub.k v` | `[ "sub"]` / `\tk = v`, read back as `.sub.k` *(2.47.3)* — this library refuses the key in edits, reads it |
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

Rule: surround with quotes when the value
starts or ends with a space or contains `#`, `;` or a CR; always escape
`\`, `"`, tab and newline.

### Multiple values and missing keys

| Before | Command | Result |
|---|---|---|
| `k = 1` `k = 2` | `--unset a.k` | `warning: a.k has multiple values`, exit 5, file unchanged |
| `k = 1` `k = 2` | `a.k 3` | `error: cannot overwrite multiple values with a single value`, exit 5, unchanged |
| `[a]` `k = 1` `[a]` `k = 2` (two sections) | `a.k 3` | same error — values across sections count together |
| `k = 1` `k = 2` `k = 3` | `--replace-all a.k X '^[12]$'` | `k = X` `k = 3` — matches collapse into one, at the last match's position |
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
| `k = v` `[a]` `j = w` | `--unset a.j` | `k = v`: the entry before any header does not keep the section *(2.47.3)* |
| `[a] [b]` `k = v` | `--rename-section a z` | `[z]` `\t[b]` `k = v`: the rest of the header's line moves to the next line *(2.47.3)* |
| `[a] [b]` `k = v` `[c]`… | `--remove-section b` | `fatal: no such section: b`: git scans lines, and only a header starting its line is seen *(2.47.3)* — this library finds headers by parsing and removes `[b]` (X1) |
| BOM + `[a]` `k = v` `[b]`… | `--remove-section a` | `fatal: no such section: a`: the BOM hides the header from the line scan *(2.47.3)* — this library removes it, keeping the BOM (X1) |
| `[a]` `k = x\` ⏎ `[b]` `z = 1` | `--remove-section b` | `[a]` `k = x\`, exit 0: the line `[b]`, part of the value `x[b]`, is taken for a header, and `a.z` is removed with it; `--rename-section b c` changes the value to `x[c]` *(2.47.3)* — this library finds no section `b` (X1) |

Observed rule for E5: after an unset, an empty section is removed unless a
comment line lies between the previous entry or header line (or the start of
the file) and the next section header.

### File handling

- Permissions of the replaced file are preserved (`chmod 600`, then a write → still `600`),
  exactly: with umask `022`, a `664` file stays `664` (the umask does
  not apply).
- Symbolic links are followed: `git config -f link a.k w` with `link -> real`
  edits `real`; `link` stays a symlink.
- A chain of 7 links `l0 -> l1 -> … -> l6 -> real`: git follows 5, then
  replaces `l5` with a regular file holding the edit; `real` is unchanged
  *(2.47.3)* — this library refuses the edit (too many levels of symbolic
  links).
- With `include.path` in `~/.gitconfig`, `git config --global user.name x`
  appends to `~/.gitconfig` itself; the included file is untouched.
- A held lock: `error: could not lock config file .git/config: File exists`,
  exit 255, immediately (no waiting).
- Each write replaces the file through a rename (new inode).
- A file holding only a UTF-8 BOM (as Windows Notepad saves an empty file):
  `git config -f f a.k v` writes `[a]` `\tk = v` *before* the BOM, and the
  file no longer parses *(2.47.3)* — this library keeps the BOM first (X1).

## Scopes

Run with an isolated `HOME`, `XDG_CONFIG_HOME` and `GIT_CONFIG_NOSYSTEM=1`.

| Setup | Result |
|---|---|
| `user.name` in both `$XDG_CONFIG_HOME/git/config` and `~/.gitconfig` | both read, XDG first; `~/.gitconfig` wins. Keys only in the XDG file are still visible (`--show-scope` reports `global`). |
| `GIT_CONFIG_GLOBAL=<file>` | only that file for global scope |
| `HOME` unset, `XDG_CONFIG_HOME` set | the XDG file is read, no `~/.gitconfig`; with neither, no global file and no error *(2.47.3)* |
| `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=user.name GIT_CONFIG_VALUE_0=x` | value from scope `command` |
| `git -c user.name=x` | scope `command` |
| `extensions.worktreeConfig = true` + `git config --worktree` | value in `.git/config.worktree`, scope `worktree`, overrides local |
| no `extensions.worktreeConfig`, single worktree, `git config --worktree x.y z` | succeeds (exit 0), writes to `.git/config` |
| `git config -f f --show-scope --show-origin --list` | `command	file:f	a.k=v` — files read with `-f` have scope `command` |
| no `extensions.worktreeConfig`, single worktree, `git config --worktree --show-scope --list` | the entries of `.git/config`, scope `local` *(2.47.3)* |
| no `extensions.worktreeConfig`, two worktrees, `git config --worktree --list` | `fatal: --worktree cannot be used with multiple working trees unless the config extension worktreeConfig is enabled…`, exit 128 *(2.47.3)* |
| `user.name` in both the XDG file and `~/.gitconfig`, `git config --global --list` (with or without `--includes`) | only `~/.gitconfig` is read: `--global` reads the file it writes to; the XDG file only if `~/.gitconfig` does not exist *(2.47.3)* — `LoadScope(ScopeGlobal)` reads both, as `Load` does |
| missing `~/.gitconfig` / `config.worktree` / `-f` file, `--get a.k` | exit 1, no output: an absent file reads as empty *(2.47.3)* |
| `~/.gitconfig` not readable (`chmod 000`), `git config --list` or `--global --get a.k` | a warning, then the file reads as empty; `--global --list` fails *(2.47.3)* |
| system file not readable, `git config --list` | `fatal: unable to access '<file>': Permission denied`, exit 128 *(2.47.3)* |
| `GIT_CONFIG_COUNT=+1`, `' 1'`, `-0` | accepted: a sign and leading whitespace are allowed *(2.47.3)* — this library takes only digits |
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
| 7 files, each including the next 10 times, the last holding `a.k` | no limit other than depth: 10ⁿ values for n levels — n = 3: 1,000 values in 0.01 s; n = 6: 1,000,000 in 7 s; n = 10 would be ~10¹⁰ values, hours of work and memory exhaustion from 3 KB of files *(2.47.3)* — this library stops at 1000 files, 4 MiB or 2¹⁶ entries read per load (X3) |

### `includeIf` (repository at `$S/work/proj`, branch `main`)

| Condition | Included? |
|---|---|
| `gitdir:$S/work/` | yes (trailing `/` means `/**`) |
| `gitdir:$S/work` | no |
| `gitdir:$S/work/**` | yes |
| `gitdir:$S/***`, `gitdir:$S/***/.git`, `gitdir:$S/work/****/proj/` | yes: a segment of three or more stars is `**`, matching any number of directories, zero included; mixed with other characters (`$S/***proj/`, `$S/w***/.git`) the stars are a single `*` *(2.47.3, 2.56.0)* |
| `gitdir:$S/work/proj/.git` | yes |
| `gitdir:work/` | yes (relative pattern gets an implicit `**/` prefix) |
| `gitdir/i:<upper-cased path>/WORK/` | yes |
| `gitdir:~/../work/` with `HOME=$S/home` | **no**: `.` and `..` in the pattern are compared literally, never normalised — `gitdir:$S/home/../work/` and `gitdir:$S/./work/` do not match either *(2.47.3)* |
| `onbranch:main`, `onbranch:ma*` | yes |
| `onbranch:other` | no |
| `hasconfig:remote.*.url:https://example.com/**` | no until a matching remote is configured, then yes |
| `git -c 'includeIf.gitdir:./.path=…'` | `error: relative config include conditionals must come from files`, then git goes on without the include, exit 0 *(2.47.3)* — this library fails with `ErrNotAbsolute`, having no one to warn |
| `gitdir:C:/x/link/`, `GIT_DIR=C:\x\link\proj\.git` with `link` a symbolic link to `work` (Windows) | **no**, while `gitdir:C:/x/work/` (the real path) matches *(2.55.0.windows.5)* — this library compares both paths with `/` and matches |

### `hasconfig:remote.*.url:` (remote `origin` at `https://example.com/a/b`)

Observed with git 2.43.0 and 2.56.0 (`alpine:edge`), with the same results.

| Condition or setup | Included? |
|---|---|
| `https://example.com/**`, `https://example.com/a/*`, `**/b`, `**`, `https://***/b`, `https://example.com/a/[a-c]` | yes |
| `https://example.com/*`, `https://example.com/` | no: `*` stops at `/`, a trailing `/` adds nothing |
| `https://EXAMPLE.com/**`, `example.com/**`, `*`, empty | no: case-sensitive, no implicit `**/` prefix |
| `hasconfig:remote.origin.url:**`, `hasconfig:remote.*.pushurl:**`, `hasconfig:user.name:**`, `HASCONFIG:remote.*.url:**`, `hasconfig:REMOTE.*.URL:**` | no: only this exact prefix is a condition |
| URL in the local file, condition in the global file | yes |
| URL from `-c`, `GIT_CONFIG_COUNT` | yes |
| URL further down the same file, or in a plain include | yes: every URL counts, read before or after |
| URL `[remote ""]` (empty name), `[Remote "o"] URL` | yes |
| `[remote] url` (no name), only a `pushurl` | no |
| `git config --global --includes`, `--local --includes`, `-f <file> --includes` | only the URLs of that scope or file count |
| URL in a file included by the condition, matching or not, or below it through `include.path` | `fatal: remote URLs cannot be configured in file directly or indirectly included by includeIf.hasconfig:remote.*.url`, exit 128 |
| URL in a file included by a `gitdir:` or `onbranch:` condition that holds, with any `hasconfig:` condition met, even one with a missing file | the same fatal error; without a `hasconfig:` condition, or with the `gitdir:` condition false, it is read |
| `hasconfig:` condition only inside an include that is not followed | not met: no error |
| syntax error in the file of a `hasconfig:` condition that does not match | `fatal: bad config line 1`: the file is read |
| implicit `[remote "o"] url` (no `=`) with a `hasconfig:` condition met | segmentation fault (2.43.0 and 2.56.0) — this library takes it as no URL |

## Ownership and `safe.directory`

Observed with **git 2.56.0** (`alpine:edge`, Linux), as user `alice` (uid
1000) unless stated, `bob` being uid 1001. "Refused" means `fatal: detected
dubious ownership in repository at '<path>'`, exit 128. The repository is
`/r/w` (working tree) with `.git` inside, all owned by `alice` except the
path named.

| Owned by `bob` | Result |
|---|---|
| nothing | accepted |
| `/r/w` (top of the working tree) | refused, at `/r/w` — also when run from a subdirectory owned by `alice` |
| `/r/w/.git` (directory) | refused, at `/r/w` |
| `/r/w/.git` as a `gitdir:` file | refused, at `/r/w` |
| the directory a `gitdir:` file points to | refused, at `/r/w` |
| `/r/w/.git/config` | accepted: files inside the git directory are not checked |
| `/r` (parent of the working tree) | accepted |
| a symbolic link to `/r/w` (`chown -h`), used as `-C` | accepted: only the target counts |
| a bare repository `/r/b.git` | refused, at `/r/b.git` |
| `/r/b.git/config` in a bare repository | accepted |
| linked worktree `/r/lw`: `/r/w/.git/worktrees/lw` | refused, at `/r/lw` |
| linked worktree `/r/lw`: `/r/w/.git` or `/r/w` | accepted: the main repository is not checked |
| `/r/w`, mode 777, or group `alice` | refused: only the owner uid counts |

| Setup | Result |
|---|---|
| `/r/w/.git` owned by `bob`, `-c safe.directory=/r/w` | accepted |
| `/r/w/.git` owned by `bob`, `-c safe.directory=/r/w/.git` | refused: the entry must name the working tree, whichever path failed |
| `gitdir:` target owned by `bob`, `safe.directory` = the working tree / the target | accepted / refused |
| bare `/r/b.git` owned by `bob`, `safe.directory=/r/b.git` | accepted |
| `/r/w` owned by `bob`, `GIT_DIR=/r/w/.git` (with or without `GIT_WORK_TREE`) | accepted: nothing is checked when the repository is given rather than discovered |
| `/r/w` owned by `bob`, `git config --show-scope --get-regexp foo` (`foo.bar` in `.git/config`) | exit 1, no output: the repository is treated as absent, its configuration silently skipped |
| the same, `git config --local --list` | `fatal: --local can only be used inside a git repository`, exit 128 |
| the same, `git rev-parse --show-toplevel` | refused |
| as root, repository owned by `alice`, no `SUDO_UID` | refused |
| as root, owned by `alice`, `SUDO_UID=1000` | accepted |
| as root, owned by `alice`, `SUDO_UID=1001` or `SUDO_UID=abc` | refused |
| as root, owned by root, `SUDO_UID=1000` | accepted: root is still trusted |
| as `alice`, owned by `bob`, `SUDO_UID=1001` | refused: `SUDO_UID` counts only for root |
| as root, owned by `alice`, `SUDO_UID` = `' 1000'`, `+1000`, `01000` | accepted |
| as root, owned by `alice`, `SUDO_UID` = `'1000 '`, `1000x` | refused |
| as root, owned by `alice`, `SUDO_UID=4294968296` (2³² + 1000) | accepted: the value wraps around to 1000 |

What is checked depends on where git starts, not on the layout of the
repository: started in a working tree, git checks the working tree, its
`.git` and the git directory, and `safe.directory` must name the working
tree; started in a git directory, it checks that directory alone, and
`safe.directory` must name it.

| Setup (`bob` owns the path named) | Result |
|---|---|
| normal repository, `git -C /r/w/.git`, `/r/w` owned by `bob` | accepted: the working tree is not checked |
| normal repository, `git -C /r/w/.git`, `.git` owned by `bob`, `safe.directory=/r/w` / `=/r/w/.git` | refused, at `/r/w/.git` / accepted |
| bare repository named `.git` at `/r/p/.git`, `/r/p` owned by `bob`, `git -C /r/p/.git` | accepted |
| the same, `git -C /r/p` | refused, at `/r/p` (git looks for `/r/p/.git` from there) |
| bare `/r/p/.git` owned by `bob`, `safe.directory=/r/p/.git` / `=/r/p` | accepted / refused, at `/r/p/.git` |
| bare `/r/dots` with `core.worktree=/r/home` (and `core.bare=false`), `/r/home` owned by `bob` | accepted: `core.worktree` is not checked |
| the same, `/r/dots` owned by `bob`, `safe.directory=/r/dots` / `=/r/home` | accepted / refused, at `/r/dots` |
| `--separate-git-dir=/r/sep/x` for `/r/w`, `/r/w` owned by `bob`, `git -C /r/sep/x` | accepted |
| linked worktree `/r/lw` owned by `bob`, `git -C /r/w/.git/worktrees/lw` / `git -C /r/lw` | accepted / refused, at `/r/lw` |

## What is a repository

Observed with git 2.47.3, as `git rev-parse --git-dir` run in `/r/w` with
`GIT_CEILING_DIRECTORIES=/r`. "Refused" means `fatal: not a git repository`.
Unless stated, `/r/w/.git` holds `HEAD` (`ref: refs/heads/main`) and the
directories `objects` and `refs`.

| `/r/w/.git` | Result |
|---|---|
| as stated | accepted |
| `HEAD` alone | refused |
| no `objects`, or no `refs` | refused |
| `objects` or `refs` a regular file | refused |
| `objects` a symbolic link to a directory | accepted |
| `HEAD` a directory | refused |
| `HEAD` holding `garbage` | refused |
| `HEAD` holding an object name | accepted |
| `HEAD` a symbolic link to `refs/heads/main`, which does not exist | accepted |
| `HEAD` a symbolic link to `refs/heads`, a directory | accepted |
| `HEAD` a symbolic link to `refs/../../outside`, which does not exist | accepted: only the link's text is checked, which must start with `refs/` |
| `HEAD` a symbolic link to `./refs/heads/main`, to `refs`, to `nothere`, or to the absolute path of `/r/w/.git/refs/heads/main` | refused |
| `HEAD` a symbolic link to a directory outside `refs` (`/etc`) | refused |
| created by `git init --ref-format=reftable`: `refs` is a directory holding a regular file `heads`, `HEAD` holds `ref: refs/heads/.invalid` | accepted |

A linked worktree's git directory, `/r/w/.git/worktrees/lw`, holds `HEAD`
and `commondir` (`../..`) but no `objects`:

| Linked worktree `/r/lw` | Result |
|---|---|
| as created by `git worktree add` | accepted |
| no `refs` in `worktrees/lw` | accepted |
| no `refs`, or no `objects`, in `/r/w/.git` | refused |
| no `commondir` | refused |
