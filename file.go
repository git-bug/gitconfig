package gitconfig

import (
	"bytes"
	"errors"
)

// File is the lossless, editable form of one configuration file. Edits
// change only the lines `git config` would change, and produce the same
// bytes; everything else (comments, blank lines, indentation) is kept byte
// for byte.
//
// A File comes from Parse, for content that is not on disk (tests, in-memory
// repositories, a .gitmodules blob), or from Env.UpdateScope and
// Env.UpdateFile, to edit a file on disk. The zero value is an empty file.
//
// A File is mutable and must not be used by several goroutines at once. Use
// Config to take an immutable snapshot; its entries have the scope the File
// was opened for by Env.UpdateScope, and ScopeFile otherwise.
//
// Edits find keys, sections and subsections as Config does: section and name
// case-insensitive, subsection exact, a legacy header [a.B] holding
// subsection "b". git differs in two places: it places a key with an
// upper-case subsection into a matching legacy header, where it reads back
// lowered, and it removes or renames a section only if the header is
// spelled as given.
//
// A new key goes at the end of the last section that holds its section or
// subsection, or into a new section at the end of the file, as git does.
//
// Edits return a *KeyError wrapping ErrInvalidKey if a key or section name
// is one git rejects, or a key has an empty section (".sub.name", which git
// reads from a header [ "sub"]), and wrapping ErrInvalidValue if a value
// contains a NUL byte. An edit that would take the file beyond the limits
// of Parse is a *KeyError wrapping ErrLoadLimit. Removing something that is
// absent is not an error: the methods report what they removed.
//
// An edit cannot inject anything: whatever the key, subsection and value,
// the edited file parses to the entries before the edit with exactly that
// one change. Values are quoted and escaped as git does, and a subsection
// that cannot be written in a header (newline, NUL) is ErrInvalidKey.
type File struct {
	name  string
	scope Scope
	data  []byte
	parsed

	closed bool    // edits fail with ErrClosed
	snap   *Config // Config of data, nil until asked for
}

// Parse parses a configuration file. name is used in errors and in the
// Origin of entries, which have ScopeFile. A file git rejects is a
// *SyntaxError. An entry before any section header, which git reads under a
// key no command can ask for, is kept in the file but is not an entry of
// Config.
//
// data may come from anyone (a .gitmodules blob from a remote): Parse never
// panics, and its time and memory are linear in len(data). It applies the
// limits of a load to its one file: more than 16 MiB, more than 2²⁰ entries
// and distinct section headers, or more than 2²² pieces (blank lines,
// comments, ...) is a *FileError wrapping ErrLoadLimit.
func Parse(name string, data []byte) (*File, error) {
	if len(data) > maxLoadBytes {
		return nil, &FileError{Path: name, Err: ErrLoadLimit}
	}
	// The File keeps data, which its items index: the caller must stay free
	// to reuse its slice.
	data = bytes.Clone(data)
	p, err := parseFile(name, data)
	if errors.Is(err, ErrLoadLimit) {
		return nil, &FileError{Path: name, Err: err}
	}
	if err != nil {
		return nil, err
	}
	return &File{name: name, data: data, parsed: p}, nil
}

// Bytes returns the content of the file. It is identical to the parsed data
// if the file was not edited.
func (f *File) Bytes() []byte {
	return bytes.Clone(f.data)
}

// Config returns an immutable snapshot of the entries of this file. Include
// directives are not followed; use Env to load configuration with includes.
//
// After the callback of Env.UpdateFile returns, Bytes and Config still give
// the last content, but edits fail with ErrClosed.
//
// The snapshot is kept until the next edit, so calling Config repeatedly,
// for example to read values inside an Env.UpdateFile callback, is cheap.
func (f *File) Config() *Config {
	if f.snap == nil {
		f.snap = newConfig(f.entries(f.scope, f.name, nil))
	}
	return f.snap
}

// Set sets the value of key, like `git config <key> <value>`, adding it if
// absent. It fails with ErrMultipleValues if key has several values.
//
// If the value is already set, the file is left unchanged. git would
// rewrite the line in its own format; keeping it is the one deliberate
// difference with git's edits. An implicit entry ("[a]\n\tk") is never
// already set, even for value "": it reads as true, and "k = " as false.
func (f *File) Set(key, value string) error {
	k, section, name, err := f.prepareValue(key, value)
	if err != nil {
		return err
	}
	var sp splice
	switch idx := f.entriesOf(k); len(idx) {
	case 0:
		sp = f.insert(k, section, name, value)
	case 1:
		if it := f.items[idx[0]]; !it.implicit && f.valueOf(it) == value {
			return nil
		}
		sp = f.replaceEntry(idx[0], name, value)
	default:
		return &KeyError{Key: key, Err: ErrMultipleValues}
	}
	return f.apply(key, []splice{sp}, checkKey(k, []string{value}, 0))
}

// ReplaceAll replaces all values of key with a single value, or adds it if
// key is absent, like `git config --replace-all <key> <value>`. The new
// value takes the place of the last old one.
func (f *File) ReplaceAll(key, value string) error {
	k, section, name, err := f.prepareValue(key, value)
	if err != nil {
		return err
	}
	var splices []splice
	if idx := f.entriesOf(k); len(idx) == 0 {
		splices = append(splices, f.insert(k, section, name, value))
	} else {
		// The values collapse into one, at the place of the last.
		for _, i := range idx[:len(idx)-1] {
			splices = append(splices, f.removeEntry(i))
		}
		splices = append(splices, f.replaceEntry(idx[len(idx)-1], name, value))
	}
	return f.apply(key, splices, checkKey(k, []string{value}, 0))
}

// Add adds a value to key, keeping existing ones, like `git config --add`.
// The new value is read last, so it is the one Config.Get returns.
func (f *File) Add(key, value string) error {
	k, section, name, err := f.prepareValue(key, value)
	if err != nil {
		return err
	}
	var want []string
	for _, i := range f.entriesOf(k) {
		want = append(want, f.valueOf(f.items[i]))
	}
	want = append(want, value)
	// Implicit values are kept: only the new one must not be.
	return f.apply(key, []splice{f.insert(k, section, name, value)}, checkKey(k, want, len(want)-1))
}

// Unset removes the value of key, like `git config --unset <key>`, and
// reports whether there was one. It fails with ErrMultipleValues if key has
// several values.
func (f *File) Unset(key string) (removed bool, err error) {
	k, _, _, err := f.prepare(key)
	if err != nil {
		return false, err
	}
	idx := f.entriesOf(k)
	switch len(idx) {
	case 0:
		return false, nil
	case 1:
		err := f.apply(key, f.unsetEntries(idx), checkKey(k, nil, 0))
		return err == nil, err
	}
	return false, &KeyError{Key: key, Err: ErrMultipleValues}
}

// UnsetAll removes all values of key, like `git config --unset-all <key>`,
// and returns how many were removed.
func (f *File) UnsetAll(key string) (removed int, err error) {
	k, _, _, err := f.prepare(key)
	if err != nil {
		return 0, err
	}
	idx := f.entriesOf(k)
	if len(idx) == 0 {
		return 0, nil
	}
	if err := f.apply(key, f.unsetEntries(idx), checkKey(k, nil, 0)); err != nil {
		return 0, err
	}
	return len(idx), nil
}

// RemoveSection removes section [section] and all its entries, like
// `git config --remove-section <section>`, and reports whether it was
// present. Its subsections are kept.
func (f *File) RemoveSection(section string) (removed bool, err error) {
	if err := f.prepareSection(section); err != nil {
		return false, err
	}
	return f.removeSection(section, sectionKey{section: lowerASCII(section)})
}

// RemoveSubsection removes subsection [section "sub"] and all its entries,
// like `git config --remove-section <section>.<sub>`, and reports whether
// it was present. sub "" is the empty subsection [section ""].
func (f *File) RemoveSubsection(section, sub string) (removed bool, err error) {
	if err := f.prepareSection(section, sub); err != nil {
		return false, err
	}
	return f.removeSection(section, sectionKey{lowerASCII(section), sub, true})
}

// RenameSection renames section [oldName] to [newName], like
// `git config --rename-section`. Subsections are not renamed. It fails with
// ErrNotFound if the section is absent.
//
// Only the headers change, and whether [newName] exists is not checked: if
// it does, both sections remain and read as one, as with git. To refuse
// instead, check f.Config() first, in the same Env.UpdateFile callback.
func (f *File) RenameSection(oldName, newName string) error {
	if err := f.prepareSection(oldName); err != nil {
		return err
	}
	if err := sectionArg(newName); err != nil {
		return err
	}
	to := sectionKey{section: lowerASCII(newName)}
	return f.renameSection(oldName, sectionKey{section: lowerASCII(oldName)}, to, formatHeader(newName, "", false))
}

// RenameSubsection renames subsection [section "oldSub"] to
// [section "newSub"], as `git remote rename` does for the remote's section.
// It fails with ErrNotFound if the subsection is absent. As for
// RenameSection, an existing [section "newSub"] is not checked: `git remote
// rename` refuses one, and a caller doing the same checks
// f.Config().Subsections first.
func (f *File) RenameSubsection(section, oldSub, newSub string) error {
	if err := f.prepareSection(section, oldSub, newSub); err != nil {
		return err
	}
	sec := lowerASCII(section)
	return f.renameSection(section, sectionKey{sec, oldSub, true}, sectionKey{sec, newSub, true}, formatHeader(section, newSub, true))
}
