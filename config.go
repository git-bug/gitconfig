package gitconfig

import "iter"

// Config is an immutable, ordered set of entries, from a single file or from
// several scopes. It is safe for concurrent use. A nil *Config is empty.
//
// Keys are given as in `git config`: "section.name" or
// "section.subsection.name". Section and name are case-insensitive, the
// subsection is case-sensitive. An invalid key is never present: Get, GetAll
// and Subsections report it as not found, Bool and Int fail with
// ErrInvalidKey. Use ParseKey to check a key beforehand.
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

// Subsections returns the distinct subsections of section, in order of first
// appearance. For example Subsections("remote") lists the remote names.
func (c *Config) Subsections(section string) []string {
	panic("not implemented")
}

// Bool returns the last value for key as a boolean, or def if key is absent.
// A value that is not a valid boolean is a *ValueError, never def. An invalid
// key is ErrInvalidKey.
func (c *Config) Bool(key string, def bool) (bool, error) {
	panic("not implemented")
}

// Int returns the last value for key as an integer, or def if key is absent.
// A value that is not a valid integer is a *ValueError, never def. An invalid
// key is ErrInvalidKey.
func (c *Config) Int(key string, def int64) (int64, error) {
	panic("not implemented")
}

// Entry is one occurrence of a key in a configuration file.
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
	// Scope is Command for entries of Env.LoadFile and File.Config, as git
	// reports for `git config -f`. Those have a File; command-line entries
	// (git -c) do not.
	Scope Scope
	File  string // the file that holds the entry, possibly an included one; "" for git -c
	Line  int    // 1-based; 0 for git -c
}

// Bool interprets the value as git does: true, yes, on and non-zero integers
// are true; false, no, off, 0 and "" are false; an implicit value is true.
// Case is ignored. Anything else is a *ValueError.
func (e Entry) Bool() (bool, error) {
	panic("not implemented")
}

// Int interprets the value as git does, including the k, m and g suffixes.
// An invalid unit or an out-of-range value is a *ValueError.
func (e Entry) Int() (int64, error) {
	panic("not implemented")
}

// Key is the canonical form of a key, as printed by `git config --list`:
// section and name in lower case, subsection verbatim.
type Key struct {
	Section    string
	Subsection string // "" when there is none
	Name       string
}

// ParseKey parses "section.name" or "section.subsection.name" as git does:
// the subsection is everything between the first and the last dot, section
// and name are lowered. A key git rejects is ErrInvalidKey.
func ParseKey(s string) (Key, error) {
	panic("not implemented")
}

// String returns the key as "section.name" or "section.subsection.name".
func (k Key) String() string {
	panic("not implemented")
}
