package gitconfig

import (
	"strings"
)

// ParseKey parses "section.name" or "section.subsection.name" as git does:
// the subsection is everything between the first and the last dot, section
// and name are lowered. A key git rejects is a *KeyError wrapping
// ErrInvalidKey: the section must hold only letters, digits and '-', the
// name must also start with a letter, and the subsection may hold anything
// but a newline or a NUL byte. Reads treat an invalid key as absent; only
// ParseKey and edits report it.
func ParseKey(s string) (Key, error) {
	k, ok := parseKey(s)
	if !ok {
		return Key{}, &KeyError{Key: s, Err: ErrInvalidKey}
	}
	return k, nil
}

// parseKey is ParseKey without the error value, for reads.
//
// A key with an empty section is valid when it has a subsection
// (".sub.name", "..name"): git reads it from a header [ "sub"] or [.], and
// every entry read must have a key ParseKey accepts. Edits refuse it.
func parseKey(s string) (Key, bool) {
	// The subsection may itself contain dots: only the first and the last
	// dot are separators.
	first := strings.IndexByte(s, '.')
	last := strings.LastIndexByte(s, '.')
	if first < 0 {
		return Key{}, false
	}
	name := s[last+1:]
	if !validName(name) {
		return Key{}, false
	}
	k := Key{Name: lowerASCII(name)}
	if first == last {
		// No subsection: the section may not be empty (".name").
		if !validSection(s[:first]) || first == 0 {
			return Key{}, false
		}
		k.Section = lowerASCII(s[:first])
		return k, true
	}
	if !validSection(s[:first]) || !validSubsection(s[first+1:last]) {
		return Key{}, false
	}
	k.Section = lowerASCII(s[:first])
	k.Subsection = s[first+1 : last]
	k.HasSubsection = true
	return k, true
}

// String returns the key as "section.name" or "section.subsection.name"
// ("section..name" for an empty subsection).
func (k Key) String() string {
	if k.HasSubsection {
		return k.Section + "." + k.Subsection + "." + k.Name
	}
	return k.Section + "." + k.Name
}

// sectionKey identifies a section or subsection in canonical form: the part
// of a Key before its name.
type sectionKey struct {
	section    string
	subsection string
	hasSub     bool
}

func (k Key) sectionKey() sectionKey {
	return sectionKey{k.Section, k.Subsection, k.HasSubsection}
}

// isKeyChar reports whether c may appear in a section or key name.
func isKeyChar(c byte) bool {
	return isAlpha(c) || '0' <= c && c <= '9' || c == '-'
}

func isAlpha(c byte) bool {
	return 'a' <= c|0x20 && c|0x20 <= 'z'
}

// validName reports whether s is a valid key name: a letter, then letters,
// digits and dashes.
func validName(s string) bool {
	if s == "" || !isAlpha(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isKeyChar(s[i]) {
			return false
		}
	}
	return true
}

// validSection reports whether s holds only the characters of a section
// name. It may be empty.
func validSection(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isKeyChar(s[i]) {
			return false
		}
	}
	return true
}

// validSubsection reports whether s can be written in a header: anything
// but a newline or a NUL byte.
func validSubsection(s string) bool {
	return !strings.ContainsAny(s, "\n\x00")
}

// lowerASCII lowers ASCII letters only: the format is ASCII, and Unicode
// folding would accept names git rejects.
func lowerASCII(s string) string {
	// Most names are already lower case: return s itself unless a letter
	// needs lowering, so that the common case does not allocate.
	for i := 0; i < len(s); i++ {
		if 'A' <= s[i] && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if 'A' <= b[j] && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
