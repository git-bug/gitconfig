package gitconfig

import (
	"errors"
	"fmt"
)

// The sentinel errors below are returned wrapped in a typed error that says
// which key, file or value is concerned; test them with errors.Is, and get
// the context with errors.As:
//
//   - ParseKey and File edits return a *KeyError.
//   - Parse returns a *SyntaxError.
//   - Typed reads (Config.Bool, Entry.Int, ...) return a *ValueError.
//   - Env methods and Discover return a *FileError for anything about a
//     file (reading, a *SyntaxError, includes, load limits, locking,
//     writing), a *KeyError for an invalid CommandEntry key, and ErrNoScope
//     unwrapped, as no file is concerned. The error returned by the
//     callback of Env.UpdateFile or Env.UpdateScope is returned as is.
var (
	// ErrNotFound is returned when the section or subsection to rename is
	// absent. Removing something absent is not an error.
	ErrNotFound = errors.New("gitconfig: not found")

	// ErrMultipleValues is returned when a single-value operation finds
	// several values.
	ErrMultipleValues = errors.New("gitconfig: multiple values")

	// ErrInvalidKey is returned when an edit, ParseKey or a CommandEntry is
	// given a key or section name that git rejects. Reads treat such a key
	// as absent.
	ErrInvalidKey = errors.New("gitconfig: invalid key")

	// ErrInvalidValue is returned when an edit is given a value git cannot
	// store: one that contains a NUL byte.
	ErrInvalidValue = errors.New("gitconfig: invalid value")

	// ErrLocked is returned when the lock file exists: another process is
	// writing.
	ErrLocked = errors.New("gitconfig: config file is locked")

	// ErrIncludeDepth is returned when includes nest more than 10 levels,
	// usually because of a cycle.
	ErrIncludeDepth = errors.New("gitconfig: exceeded maximum include depth")

	// ErrLoadLimit is returned when one load would read more than 1000
	// files or 64 MiB in total.
	ErrLoadLimit = errors.New("gitconfig: exceeded load limits")

	// ErrNotRegular is returned when a configuration file or an include is
	// not a regular file (a directory, a FIFO, a device).
	ErrNotRegular = errors.New("gitconfig: not a regular file")

	// ErrNotAbsolute is returned when Env.GitDir, or an include.path given
	// on the command line, is a relative path.
	ErrNotAbsolute = errors.New("gitconfig: path is not absolute")

	// ErrNoScope is returned when a scope does not exist or cannot be written
	// in an Env.
	ErrNoScope = errors.New("gitconfig: scope not available")

	// ErrClosed is returned when a File is edited after the callback of
	// Env.UpdateFile or Env.UpdateScope has returned.
	ErrClosed = errors.New("gitconfig: file is closed")
)

// KeyError reports an edit or a command-line entry that failed for a key or
// section name.
type KeyError struct {
	Key string // as given by the caller
	Err error
}

func (e *KeyError) Error() string {
	return fmt.Sprintf("%v: %q", e.Err, e.Key)
}

func (e *KeyError) Unwrap() error {
	return e.Err
}

// FileError reports a failure to load, lock or write a file. Path is the
// file concerned: the lock file for ErrLocked, as in git's "could not lock
// config file".
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string {
	var se *SyntaxError
	if errors.As(e.Err, &se) && se.File == e.Path {
		return se.Error()
	}
	return fmt.Sprintf("gitconfig: %s: %v", e.Path, e.Err)
}

func (e *FileError) Unwrap() error {
	return e.Err
}

// SyntaxError reports a file git refuses to parse. Msg describes the problem
// but never quotes the file's content, which may be a file that is not a
// configuration file at all, reached through an include.
type SyntaxError struct {
	File string
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("gitconfig: %s line %d in file %s", e.Msg, e.Line, e.File)
}

// ValueError reports a value that cannot be read as the requested type. Its
// message quotes the value with %q, so that control characters in a value
// cannot reach a terminal.
type ValueError struct {
	Entry Entry
	Type  string // "bool", "int", "path"
	Err   error  // the reason, e.g. strconv.ErrRange; may be nil
}

func (e *ValueError) Error() string {
	var msg string
	if e.Entry.Implicit {
		msg = fmt.Sprintf("gitconfig: missing %s value for %q", e.Type, e.Entry.Key.String())
	} else {
		msg = fmt.Sprintf("gitconfig: bad %s value %q for %q", e.Type, e.Entry.Value, e.Entry.Key.String())
	}
	if e.Entry.Origin.File != "" {
		msg += fmt.Sprintf(" in file %s", e.Entry.Origin.File)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ValueError) Unwrap() error {
	return e.Err
}
