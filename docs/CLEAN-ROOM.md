# Clean room

git is licensed GPL-2.0-only; this library is MIT (see [LICENSE](../LICENSE)).
It is written from git's observable behaviour, never from its code:

- **Never read git's source**: its C files and headers, scripts, test suite
  (`t/`), patches, diffs or blame, wherever they are (GitHub, kernel.org,
  mailing lists, a local checkout, a search engine snippet). The same goes
  for any GPL-licensed implementation, libgit2 included.
- **Allowed**: running the `git` binary, which is the oracle; git's user
  documentation (man pages, git-scm.com), its release notes, and its commit
  messages (not their diffs), to establish whether a behaviour is a bug.
  Learn from them; do not copy their text.
- Behaviour is established by experiment and recorded as test vectors in
  [GIT-BEHAVIOUR.md](GIT-BEHAVIOUR.md), with the git version used.
- Code, comments, documentation and commit messages never name git's
  internal functions, variables or source files.
- If a task seems to need git's source, stop and ask.
