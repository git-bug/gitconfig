package gitconfig

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// errInvalidUnit is the reason of a *ValueError for an integer that is not a
// number followed by an optional unit.
var errInvalidUnit = errors.New("invalid unit")

// parseBool interprets a non-implicit value as a boolean.
func parseBool(v string) (bool, error) {
	switch {
	case v == "", equalFoldASCII(v, "false"), equalFoldASCII(v, "no"), equalFoldASCII(v, "off"):
		return false, nil
	case equalFoldASCII(v, "true"), equalFoldASCII(v, "yes"), equalFoldASCII(v, "on"):
		return true, nil
	}
	n, err := parseInt(v)
	if err != nil {
		return false, err
	}
	// As in git, a numeric boolean must fit in 32 bits.
	if n < math.MinInt32 || n > math.MaxInt32 {
		return false, strconv.ErrRange
	}
	return n != 0, nil
}

// parseInt interprets a value as an integer: leading whitespace, an
// optional sign, a decimal, octal ("0" prefix) or hexadecimal ("0x" prefix)
// number, then an optional unit k, m or g, case-insensitive.
func parseInt(v string) (int64, error) {
	s := strings.TrimLeft(v, " \t\n\v\f\r")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}

	// "0x" is a prefix only if a hex digit follows: "0x" alone is the octal
	// number 0 followed by the invalid unit "x".
	base := uint64(10)
	switch {
	case len(s) > 2 && s[0] == '0' && s[1]|0x20 == 'x' && digitValue(s[2]) < 16:
		base = 16
		s = s[2:]
	case len(s) > 0 && s[0] == '0':
		base = 8
	}

	// On overflow, the scan goes on to find where the digits end: an
	// invalid unit after them is reported before the range error.
	var mag uint64
	overflow := false
	n := 0
	for ; n < len(s); n++ {
		d := uint64(digitValue(s[n]))
		if d >= base {
			break
		}
		if mag > (math.MaxUint64-d)/base {
			overflow = true
		} else {
			mag = mag*base + d
		}
	}
	if n == 0 {
		// No digits; git reports this as an invalid unit too.
		return 0, errInvalidUnit
	}

	var unit uint64 = 1
	switch rest := s[n:]; {
	case rest == "":
	case len(rest) == 1 && rest[0]|0x20 == 'k':
		unit = 1 << 10
	case len(rest) == 1 && rest[0]|0x20 == 'm':
		unit = 1 << 20
	case len(rest) == 1 && rest[0]|0x20 == 'g':
		unit = 1 << 30
	default:
		return 0, errInvalidUnit
	}

	// The magnitude of the number and of the result may reach 1<<63 for a
	// negative value.
	limit := uint64(math.MaxInt64)
	if neg {
		limit++
	}
	if overflow || mag > limit || mag > limit/unit {
		return 0, strconv.ErrRange
	}
	mag *= unit
	if neg {
		return int64(-mag), nil
	}
	return int64(mag), nil
}

// digitValue returns the value of c as a digit up to base 36, or 36.
func digitValue(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c|0x20 && c|0x20 <= 'z':
		return int(c|0x20-'a') + 10
	}
	return 36
}

// errPrefix, errNoHome and errUserHome are the reasons of a *ValueError for
// paths this package cannot expand.
var (
	errPrefix   = errors.New("%(prefix)/ is not supported")
	errNoHome   = errors.New("home directory unknown")
	errUserHome = errors.New("~user/ is not supported")
)

// expandPath interprets a value as a path: "~" and "~/" are replaced by
// home. "~user/" and "%(prefix)/" are refused.
func expandPath(v, home string) (string, error) {
	switch {
	case strings.HasPrefix(v, "%(prefix)/"):
		return "", errPrefix
	case v == "~" || strings.HasPrefix(v, "~/"):
		if home == "" {
			return "", errNoHome
		}
		return home + v[1:], nil
	case strings.HasPrefix(v, "~"):
		return "", errUserHome
	}
	return v, nil
}

// equalFoldASCII reports whether s and t are equal, ignoring the case of
// ASCII letters only.
func equalFoldASCII(s, t string) bool {
	if len(s) != len(t) {
		return false
	}
	for i := 0; i < len(s); i++ {
		a, b := s[i], t[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
