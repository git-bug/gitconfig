package gitconfig

import (
	"iter"
)

// Config is an immutable, ordered set of entries, from a single file or from
// several scopes. It is safe for concurrent use. A nil *Config is empty.
//
// Keys are given as in `git config`: "section.name" or
// "section.subsection.name". Section and name are case-insensitive, the
// subsection is case-sensitive; a legacy header [a.B] holds subsection "b",
// as git lowers it. An invalid key is never present: every read
// treats it as absent. Keys are usually constants in the caller's code;
// check them once with ParseKey, for example in a test.
//
// Methods returning a small result the caller owns return a slice (GetAll,
// Subsections); methods walking the configuration return an iterator (All,
// Section, Subsection).
type Config struct {
	// unexported
}

// Get returns the last entry for key, as `git config --get` does.
func (c *Config) Get(key string) (Entry, bool) {
	panic("not implemented")
}

// GetAll returns every entry for key, in order. The returned slice is owned
// by the caller.
func (c *Config) GetAll(key string) []Entry {
	panic("not implemented")
}

// All returns every entry, in order.
func (c *Config) All() iter.Seq[Entry] {
	panic("not implemented")
}

// Section returns every entry of section [section] itself, in order; entries
// of its subsections are not included. section is case-insensitive.
func (c *Config) Section(section string) iter.Seq[Entry] {
	panic("not implemented")
}

// Subsection returns every entry of subsection [section "sub"], in order.
// section is case-insensitive, sub is case-sensitive. sub "" is the empty
// subsection [section ""], distinct from [section].
func (c *Config) Subsection(section, sub string) iter.Seq[Entry] {
	panic("not implemented")
}

// Subsections returns the distinct subsections of section, in order of first
// appearance. For example Subsections("remote") lists the remote names.
// Entries of [section] itself have no subsection and are not listed; ""
// stands for [section ""].
func (c *Config) Subsections(section string) []string {
	panic("not implemented")
}

// Value returns the last value for key, or def if key is absent.
func (c *Config) Value(key string, def string) string {
	panic("not implemented")
}

// Bool returns the last value for key as a boolean, or def if key is absent.
// A value that is not a valid boolean is a *ValueError; the result is then
// def. To tell an absent key from one set to def, use Get and Entry.Bool.
func (c *Config) Bool(key string, def bool) (bool, error) {
	panic("not implemented")
}

// Int returns the last value for key as an integer, or def if key is absent.
// A value that is not a valid integer is a *ValueError; the result is then
// def.
func (c *Config) Int(key string, def int64) (int64, error) {
	panic("not implemented")
}

// Path returns the last value for key as a path, as Entry.Path does with
// home, usually Env.Home. If key is absent, def is expanded the same way. A
// value or a def that cannot be expanded is a *ValueError (for def, its Entry
// has the key, Value def and a zero Origin); the result is then def as given.
func (c *Config) Path(key, home, def string) (string, error) {
	panic("not implemented")
}

// Entry is one occurrence of a key in a configuration file. It is plain
// data: two entries with the same fields are equal.
type Entry struct {
	Key Key

	// Value is the decoded value: quotes removed, escapes resolved,
	// continuation lines joined, unquoted leading and trailing whitespace
	// trimmed.
	Value string

	// Implicit is true for a key written without '=' ("[a]\n\tk"). Value is
	// then "", and the entry reads as true as a boolean.
	Implicit bool

	Origin Origin
}

// Origin tells where an entry comes from, as `git config --show-scope
// --show-origin` does.
type Origin struct {
	// Scope is the scope the entry was loaded for. Entries of Env.LoadFile,
	// and of the Config of a File from Parse or Env.UpdateFile, have
	// ScopeFile (git reports "command" for `git config -f`). A File from
	// Env.UpdateScope has that scope.
	Scope Scope

	// File is the file that holds the entry, possibly an included one, as
	// git opened it: an include's relative path is joined to the including
	// file's directory and not cleaned. "" for git -c. On Windows, the
	// separators are those of the paths given, where git writes some
	// paths with '/'.
	File string

	Line int // 1-based; 0 for git -c
}

// Bool interprets the value as git does: true, yes, on and non-zero integers
// are true; false, no, off, 0 and "" are false; an implicit value is true.
// Case is ignored. Anything else is a *ValueError.
func (e Entry) Bool() (bool, error) {
	panic("not implemented")
}

// Int interprets the value as git does, including the k, m and g suffixes.
// An invalid unit, an out-of-range value or an implicit value is a
// *ValueError.
func (e Entry) Int() (int64, error) {
	panic("not implemented")
}

// Path interprets the value as a path, as git does for `--type=path` and for
// include paths: a leading "~/" is replaced by home, usually Env.Home. A "~/"
// with an empty home, a leading "%(prefix)/" (the prefix of the git binary,
// which depends on how it was built) and an implicit value are a
// *ValueError. Config.Path
// does the same for a key.
func (e Entry) Path(home string) (string, error) {
	panic("not implemented")
}

// Key is the canonical form of a key, as printed by `git config --list`:
// section and name in lower case, subsection verbatim.
type Key struct {
	Section string

	// Subsection is meaningful only if HasSubsection is set. git
	// distinguishes an empty subsection ("a..k", header [a ""]) from none
	// ("a.k", header [a]).
	Subsection    string
	HasSubsection bool

	Name string
}
