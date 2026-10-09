package gitconfig_test

import (
	"fmt"
	"log"

	"github.com/git-bug/gitconfig"
)

// Read the effective configuration of a repository, as git would use it.
func ExampleEnv_Load() {
	env, err := gitconfig.Discover(".")
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := env.Load()
	if err != nil {
		log.Fatal(err)
	}

	if e, ok := cfg.Get("user.name"); ok {
		fmt.Println("user:", e.Value)
	}

	bare, err := cfg.Bool("core.bare", false)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("bare:", bare)
}

// Change a value in the repository's config. Comments and layout are kept,
// and the file is not touched if the value is already set.
func ExampleEnv_UpdateScope() {
	env, err := gitconfig.Discover(".")
	if err != nil {
		log.Fatal(err)
	}
	err = env.UpdateScope(gitconfig.ScopeLocal, func(f *gitconfig.File) error {
		return f.Set("core.editor", "vim")
	})
	if err != nil {
		log.Fatal(err)
	}
}
