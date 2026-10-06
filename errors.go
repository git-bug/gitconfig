package gitconfig

import "errors"

var (
	// ErrNotFound is returned when the key or section to unset, remove or
	// rename is absent.
	ErrNotFound = errors.New("gitconfig: not found")

	// ErrMultipleValues is returned when a single-value operation finds
	// several values.
	ErrMultipleValues = errors.New("gitconfig: multiple values")

	// ErrInvalidKey is returned when an edit, ParseKey, Config.Bool or
	// Config.Int is given a key or section name that git rejects.
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

	// ErrNoScope is returned when a scope does not exist or cannot be written
	// in an Env.
	ErrNoScope = errors.New("gitconfig: scope not available")
)

// SyntaxError reports a file git refuses to parse.
type SyntaxError struct {
	File string
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string {
	panic("not implemented")
}

// ValueError reports a value that cannot be read as the requested type.
type ValueError struct {
	Entry Entry
	Type  string // "bool", "int", "path"
	Err   error  // the reason, e.g. strconv.ErrRange; may be nil
}

func (e *ValueError) Error() string {
	panic("not implemented")
}

func (e *ValueError) Unwrap() error {
	return e.Err
}
