package gitconfig

// ParseKey parses "section.name" or "section.subsection.name" as git does:
// the subsection is everything between the first and the last dot, section
// and name are lowered. A key git rejects is ErrInvalidKey, including a
// subsection containing a newline or a NUL byte. Reads treat an invalid key
// as absent; only ParseKey and edits report it.
func ParseKey(s string) (Key, error) {
	panic("not implemented")
}

// String returns the key as "section.name" or "section.subsection.name"
// ("section..name" for an empty subsection).
func (k Key) String() string {
	if k.HasSubsection {
		return k.Section + "." + k.Subsection + "." + k.Name
	}
	return k.Section + "." + k.Name
}
