<div align="center">

# gitconfig

[![Build Status][ci/badge]][ci/url]
[![Go Reference][godoc/badge]][godoc/url]
[![MIT License][license/badge]][license/url]

[Documentation][godoc/url] - [Issues][issues] - [Compatibility](docs/COMPATIBILITY.md)

</div>

`gitconfig` is a Go library to read and edit git configuration the way git
does.

- **same result as git**: same parsing, same value types (bool, int, path),
  tested against the `git` binary.
- **every source**: system, global, local and worktree files, `GIT_CONFIG_*`
  variables, `include` and `includeIf`. Each value says which file and line
  it came from.
- **lossless edits**: an edit changes only the lines it touches; comments
  and layout are kept.
- **safe alongside git**: writes take git's lock, so a concurrent `git config`
  and this library never overwrite each other.
- **safe on hostile repositories**: untrusted repositories are refused, as
  git refuses them (`safe.directory`).

Where this library deliberately differs from git, and why, is listed in
[COMPATIBILITY.md](docs/COMPATIBILITY.md).

## Installation

```
go get github.com/git-bug/gitconfig
```

## Example

```go
package main

import (
	"fmt"
	"log"

	"github.com/git-bug/gitconfig"
)

func main() {
	// Find the repository and where git reads its configuration from.
	env, err := gitconfig.Discover(".")
	if err != nil {
		log.Fatal(err) // ErrNotRepository, or ErrUntrusted as git refuses it
	}

	// Read the effective configuration: system, global, local, worktree
	// and command scopes, with includes, as git would use it.
	cfg, err := env.Load()
	if err != nil {
		log.Fatal(err)
	}
	if e, ok := cfg.Get("user.name"); ok {
		fmt.Printf("user.name = %s (%s)\n", e.Value, e.Origin.File)
	}

	// Edit the repository's config. Comments and layout are kept, and the
	// file is not touched if the value is already set.
	err = env.UpdateScope(gitconfig.ScopeLocal, func(f *gitconfig.File) error {
		return f.Set("core.editor", "vim")
	})
	if err != nil {
		log.Fatal(err)
	}
}
```

## License

MIT, see [LICENSE](LICENSE). This is a clean room implementation: no code is
derived from git, see [CLEAN-ROOM.md](docs/CLEAN-ROOM.md). It is meant to interoperate with git.

[ci/badge]: https://github.com/git-bug/gitconfig/actions/workflows/test.yml/badge.svg
[ci/url]: https://github.com/git-bug/gitconfig/actions/workflows/test.yml
[godoc/badge]: https://pkg.go.dev/badge/github.com/git-bug/gitconfig.svg
[godoc/url]: https://pkg.go.dev/github.com/git-bug/gitconfig
[license/badge]: https://img.shields.io/badge/license-MIT-blue.svg
[license/url]: LICENSE
[issues]: https://github.com/git-bug/gitconfig/issues
