package gitconfig

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The sentinel errors below are returned wrapped in a typed error that says
// which key, file or value is concerned; test them with errors.Is, and get
// the context with errors.As:
//
//   - ParseKey and File edits return a *KeyError.
//   - Parse returns a *SyntaxError, or a *FileError for load limits.
//   - Typed reads (Config.Bool, Entry.Int, ...) return a *ValueError.
//   - Env methods and Discover return a *FileError for anything about a
//     file (reading, a *SyntaxError, includes, load limits, locking,
//     writing), a *KeyError for an invalid CommandEntry key, and ErrNoScope
//     unwrapped, as no file is concerned. The error returned by the
//     callback of Env.UpdateFile or Env.UpdateScope is returned as is.
//   - Discover also returns an *EnvError for an environment variable it
//     cannot interpret, ErrNotRepository in a *FileError for a directory
//     that is not a repository, and ErrUntrusted in a *FileError for a
//     repository git would refuse.
//
// Error messages name files, whose paths can come from a configuration
// file someone else wrote. As in git, but stricter, control characters
// (newline and tab included), bidirectional controls and bytes that are not
// UTF-8 are replaced with '?', so that a path cannot drive the terminal it
// is printed to, nor forge a line or disguise itself. The fields of the
// errors, and Entry, Origin and Key, hold the raw data: quote them before
// printing them yourself.
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
	// files, 4 MiB, or 2¹⁶ entries and distinct section headers in total
	// from files (entries of Env.Command are not counted).
	// Parse applies the byte and entry limits to its one file, and keeps at
	// most 2¹⁸ pieces of it (blank lines, comments, ...); an edit that would
	// take a File beyond these limits fails with it.
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

	// ErrUntrusted is returned by Discover for a repository git would
	// refuse: not owned by the current user, and not listed in
	// safe.directory. The *FileError's Path is the path to list.
	ErrUntrusted = errors.New("gitconfig: repository not owned by the current user nor listed in safe.directory")

	// ErrNotRepository is returned by Discover for a directory that is not
	// a repository: the git directory expected, its .git when it exists and
	// the directory itself otherwise, holds no HEAD file, or its common
	// directory no objects and refs directories. The *FileError's Path is
	// that git directory.
	ErrNotRepository = errors.New("gitconfig: not a git repository")

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
	return sanitize(fmt.Sprintf("%v: %q", e.Err, e.Key))
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
	// A syntax error already names the file: don't repeat it.
	if se, ok := errors.AsType[*SyntaxError](e.Err); ok && se.File == e.Path {
		return se.Error()
	}
	// The wrapped error may name a path too (*fs.PathError).
	return sanitize(fmt.Sprintf("gitconfig: %s: %v", e.Path, e.Err))
}

func (e *FileError) Unwrap() error {
	return e.Err
}

// EnvError reports an environment variable Discover cannot interpret, as
// git refuses it: a malformed GIT_CONFIG_NOSYSTEM, GIT_CONFIG_COUNT or
// GIT_CONFIG_PARAMETERS, or a missing GIT_CONFIG_KEY_<n> or
// GIT_CONFIG_VALUE_<n>.
type EnvError struct {
	Var string // the variable's name, e.g. "GIT_CONFIG_COUNT"
	Err error  // the reason
}

func (e *EnvError) Error() string {
	return sanitize(fmt.Sprintf("gitconfig: %s: %v", e.Var, e.Err))
}

func (e *EnvError) Unwrap() error {
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
	return sanitize(fmt.Sprintf("gitconfig: %s line %d in file %s", e.Msg, e.Line, e.File))
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
		msg = fmt.Sprintf("gitconfig: bad %s value %q", e.Type, e.Entry.Value)
		// No key for the default of Config.Path given an invalid key.
		if e.Entry.Key != (Key{}) {
			msg += fmt.Sprintf(" for %q", e.Entry.Key.String())
		}
	}
	if e.Entry.Origin.File != "" {
		msg += fmt.Sprintf(" in file %s", e.Entry.Origin.File)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return sanitize(msg)
}

func (e *ValueError) Unwrap() error {
	return e.Err
}

// sanitize replaces with '?' what could drive a terminal or disguise a
// path: control characters (C0, DEL, C1), bidirectional controls, and bytes
// that are not UTF-8. git does this for C0 and DEL only, but keeps tab and
// newline; no message of this package spans lines, and a newline in a path
// could forge one.
func sanitize(msg string) string {
	i := 0
	for i < len(msg) && ' ' <= msg[i] && msg[i] < 0x7f {
		i++
	}
	if i == len(msg) {
		return msg
	}
	var sb strings.Builder
	sb.WriteString(msg[:i])
	for _, r := range msg[i:] {
		// An invalid byte reads as utf8.RuneError, which is printed as '?' too.
		if r < ' ' || 0x7f <= r && r <= 0x9f || r == utf8.RuneError || unicode.Is(unicode.Bidi_Control, r) {
			r = '?'
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
