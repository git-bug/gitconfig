package gitconfig

import (
	"iter"
	"slices"
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
// When a key has several values, the last one wins, as in git: Get, Value,
// Bool, Int and Path use it, GetAll returns them all.
type Config struct {
	entries []Entry
	index   map[Key][]int // positions in entries, by key

	// subsections lists the distinct subsections of each section, in order
	// of first appearance.
	subsections map[string][]string
}

// newConfig returns a Config of entries. It takes ownership of the slice.
func newConfig(entries []Entry) *Config {
	c := &Config{
		entries:     entries,
		index:       make(map[Key][]int),
		subsections: make(map[string][]string),
	}
	seen := make(map[sectionKey]bool)
	for i, e := range entries {
		c.index[e.Key] = append(c.index[e.Key], i)
		if sk := e.Key.sectionKey(); sk.hasSub && !seen[sk] {
			seen[sk] = true
			c.subsections[sk.section] = append(c.subsections[sk.section], sk.subsection)
		}
	}
	return c
}

// lookup returns the positions of the entries of key.
func (c *Config) lookup(key string) []int {
	if c == nil {
		return nil
	}
	k, ok := parseKey(key)
	if !ok {
		return nil
	}
	return c.index[k]
}

// filter returns the entries of one section or subsection.
func (c *Config) filter(sk sectionKey) iter.Seq[Entry] {
	return func(yield func(Entry) bool) {
		if c == nil {
			return
		}
		for _, e := range c.entries {
			if e.Key.sectionKey() == sk && !yield(e) {
				return
			}
		}
	}
}

// Get returns the last entry for key, as `git config --get` does.
func (c *Config) Get(key string) (Entry, bool) {
	idx := c.lookup(key)
	if len(idx) == 0 {
		return Entry{}, false
	}
	return c.entries[idx[len(idx)-1]], true
}

// GetAll returns every entry for key, in order. The returned slice is owned
// by the caller.
func (c *Config) GetAll(key string) []Entry {
	idx := c.lookup(key)
	if len(idx) == 0 {
		return nil
	}
	out := make([]Entry, len(idx))
	for i, j := range idx {
		out[i] = c.entries[j]
	}
	return out
}

// All returns every entry, in order.
func (c *Config) All() iter.Seq[Entry] {
	if c == nil {
		return slices.Values([]Entry(nil))
	}
	return slices.Values(c.entries)
}

// Section returns every entry of section [section] itself, in order; entries
// of its subsections are not included. section is case-insensitive.
func (c *Config) Section(section string) iter.Seq[Entry] {
	return c.filter(sectionKey{section: lowerASCII(section)})
}

// Subsection returns every entry of subsection [section "sub"], in order.
// section is case-insensitive, sub is case-sensitive. sub "" is the empty
// subsection [section ""], distinct from [section].
func (c *Config) Subsection(section, sub string) iter.Seq[Entry] {
	return c.filter(sectionKey{lowerASCII(section), sub, true})
}

// Subsections returns the distinct subsections of section, in order of first
// appearance. For example Subsections("remote") lists the remote names.
// Entries of [section] itself have no subsection and are not listed; ""
// stands for [section ""].
func (c *Config) Subsections(section string) []string {
	if c == nil {
		return nil
	}
	return slices.Clone(c.subsections[lowerASCII(section)])
}

// Value returns the last value for key, or def if key is absent.
func (c *Config) Value(key string, def string) string {
	if e, ok := c.Get(key); ok {
		return e.Value
	}
	return def
}

// Bool returns the last value for key as a boolean, or def if key is absent.
// A value that is not a valid boolean is a *ValueError; the result is then
// def. To tell an absent key from one set to def, use Get and Entry.Bool.
func (c *Config) Bool(key string, def bool) (bool, error) {
	e, ok := c.Get(key)
	if !ok {
		return def, nil
	}
	b, err := e.Bool()
	if err != nil {
		return def, err
	}
	return b, nil
}

// Int returns the last value for key as an integer, or def if key is absent.
// A value that is not a valid integer is a *ValueError; the result is then
// def.
func (c *Config) Int(key string, def int64) (int64, error) {
	e, ok := c.Get(key)
	if !ok {
		return def, nil
	}
	n, err := e.Int()
	if err != nil {
		return def, err
	}
	return n, nil
}

// Path returns the last value for key expanded as a path by Entry.Path, or
// def if key is absent, expanded the same way. home is the home directory,
// usually Env.Home.
//
// A value that cannot be expanded is a *ValueError, and the result is then
// def, unexpanded. This also holds for def itself: its error reports it
// with an empty Origin, and a zero Key if key is invalid.
func (c *Config) Path(key, home, def string) (string, error) {
	e, ok := c.Get(key)
	if !ok {
		// Expand def as an entry of key, so that an error names the key.
		k, _ := parseKey(key)
		e = Entry{Key: k, Value: def}
	}
	p, err := e.Path(home)
	if err != nil {
		return def, err
	}
	return p, nil
}

// error returns a *ValueError for e read as typ.
func (e Entry) error(typ string, err error) error {
	return &ValueError{Entry: e, Type: typ, Err: err}
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

	// File is the file that holds the entry, possibly an included one, or
	// "" for the command line. Like git, an included file's relative path
	// is appended to the including file's directory without cleaning it:
	// "/home/u/.config/git/../work.inc". On Windows the separators are
	// kept as given, where git shows some paths with '/'.
	File string

	Line int // 1-based; 0 for git -c
}

// Bool interprets the value as git does: true, yes, on and non-zero integers
// are true; false, no, off, 0 and "" are false; an implicit value is true.
// Case is ignored. Anything else is a *ValueError.
func (e Entry) Bool() (bool, error) {
	if e.Implicit {
		return true, nil
	}
	b, err := parseBool(e.Value)
	if err != nil {
		return false, e.error("bool", err)
	}
	return b, nil
}

// Int interprets the value as git does: a decimal number, or octal with a
// leading "0" and hexadecimal with a leading "0x" as in C (so "010" is 8),
// optionally followed by a unit k, m or g (×1024, ×1024², ×1024³), case
// ignored. An invalid number or unit, a value out of the int64 range or an
// implicit value is a *ValueError.
func (e Entry) Int() (int64, error) {
	if e.Implicit {
		return 0, e.error("int", nil)
	}
	n, err := parseInt(e.Value)
	if err != nil {
		return 0, e.error("int", err)
	}
	return n, nil
}

// Path interprets the value as a path, as git does for `--type=path` and for
// include paths: a leading "~/", or a value of "~" alone, is replaced by
// home, usually Env.Home. Other values are returned unchanged; relative
// paths are not resolved.
//
// These are a *ValueError: "~" or "~/" with an empty home, "~user/" (not
// supported), a leading "%(prefix)/" (the install prefix of the git binary,
// unknown to this package) and an implicit value.
func (e Entry) Path(home string) (string, error) {
	if e.Implicit {
		return "", e.error("path", nil)
	}
	p, err := expandPath(e.Value, home)
	if err != nil {
		return "", e.error("path", err)
	}
	return p, nil
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
