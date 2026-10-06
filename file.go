package gitconfig

// File is the lossless, editable form of one configuration file. Edits
// change only the lines `git config` would change, and produce the same
// bytes; everything else is kept byte for byte.
//
// A File is mutable and must not be used by several goroutines at once. Use
// Config to take an immutable snapshot. The zero value is an empty file.
//
// A File comes from Parse, for content that is not on disk (tests, in-memory
// repositories, a .gitmodules blob), or from Env.UpdateScope and
// Env.UpdateFile, to edit a file on disk.
type File struct {
	// unexported
}

// Parse parses a configuration file. name is used in errors and in the
// Origin of entries. A file git rejects is a *SyntaxError.
func Parse(name string, data []byte) (*File, error) {
	panic("not implemented")
}

// Bytes returns the content of the file. It is identical to the parsed data
// if the file was not edited.
func (f *File) Bytes() []byte {
	panic("not implemented")
}

// Config returns an immutable snapshot of the entries of this file. Include
// directives are not followed; use Env to load configuration with includes.
func (f *File) Config() *Config {
	panic("not implemented")
}

// Set sets the value of key, like `git config <key> <value>`. It fails with
// ErrMultipleValues if key has several values. It is Replace with the zero
// Match.
func (f *File) Set(key, value string) error {
	panic("not implemented")
}

// Replace replaces the single value of key matched by m, or adds the value if
// none matches, like `git config <key> <value> <pattern>`. It fails with
// ErrMultipleValues if m matches several values.
func (f *File) Replace(key, value string, m Match) error {
	panic("not implemented")
}

// ReplaceAll replaces all values of key matched by m with a single value,
// like `git config --replace-all`.
func (f *File) ReplaceAll(key, value string, m Match) error {
	panic("not implemented")
}

// Add adds a value to key, keeping existing ones, like `git config --add`.
func (f *File) Add(key, value string) error {
	panic("not implemented")
}

// Unset removes the single value of key matched by m, like
// `git config --unset <key> [<pattern>]`. It fails with ErrNotFound if none
// matches and with ErrMultipleValues if several do.
func (f *File) Unset(key string, m Match) error {
	panic("not implemented")
}

// UnsetAll removes all values of key matched by m, like
// `git config --unset-all`. It fails with ErrNotFound if none matches.
func (f *File) UnsetAll(key string, m Match) error {
	panic("not implemented")
}

// RemoveSection removes a section ("core") or subsection ("remote.origin")
// and all its entries, like `git config --remove-section`. It fails with
// ErrNotFound if the section is absent.
func (f *File) RemoveSection(name string) error {
	panic("not implemented")
}

// RenameSection renames a section or subsection, like
// `git config --rename-section`. It fails with ErrNotFound if the section is
// absent.
func (f *File) RenameSection(oldName, newName string) error {
	panic("not implemented")
}

// Match selects values for Replace, ReplaceAll, Unset and UnsetAll, with git's
// semantics. The zero Match matches every value.
type Match struct {
	// unexported
}

// Regexp matches values against a POSIX extended regular expression, as git
// does, using Go's POSIX syntax (regexp.CompilePOSIX, leftmost-longest). It
// may differ from git's libc regcomp in rare cases, such as locale-dependent
// character classes. A leading '!' negates the match.
func Regexp(pattern string) (Match, error) {
	panic("not implemented")
}

// Fixed matches values equal to value, like `git config --fixed-value`.
func Fixed(value string) Match {
	panic("not implemented")
}
