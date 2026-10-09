package gitconfig

import (
	"errors"
	"slices"
	"strings"
)

// Edits are computed on the items of the file as splices of its bytes, in
// the places and the format git's own edits use. The result is parsed again
// and checked against the expected entries before it replaces the file, so
// that no edit can change anything but what it was asked to.

// errUnsafeEdit is returned, wrapped in a *KeyError, if an edit would have
// changed other entries than its own. It is a bug of this package if it
// ever happens.
var errUnsafeEdit = errors.New("gitconfig: internal error: the edit would change other entries")

// splice replaces data[start:end] with text.
type splice struct {
	start, end int
	text       string
}

// editKey validates key for an edit and returns it in canonical form, with
// its section, subsection and name as the caller spelled them, for new
// lines and headers.
func editKey(key string) (k Key, section, sub, name string, err error) {
	k, ok := parseKey(key)
	if !ok || k.Section == "" {
		return Key{}, "", "", "", &KeyError{Key: key, Err: ErrInvalidKey}
	}
	first := strings.IndexByte(key, '.')
	last := strings.LastIndexByte(key, '.')
	section, name = key[:first], key[last+1:]
	if k.HasSubsection {
		sub = key[first+1 : last]
	}
	return k, section, sub, name, nil
}

// prepare validates the key and, if value is not nil, the value of an edit.
func (f *File) prepare(key string, value *string) (k Key, section, sub, name string, err error) {
	if err := f.checkOpen(key); err != nil {
		return Key{}, "", "", "", err
	}
	k, section, sub, name, err = editKey(key)
	if err != nil {
		return Key{}, "", "", "", err
	}
	if value != nil {
		if err := checkValue(key, *value); err != nil {
			return Key{}, "", "", "", err
		}
	}
	return k, section, sub, name, nil
}

func checkValue(key, value string) error {
	if strings.IndexByte(value, 0) >= 0 {
		return &KeyError{Key: key, Err: ErrInvalidValue}
	}
	return nil
}

func (f *File) checkOpen(key string) error {
	if f.closed {
		return &KeyError{Key: key, Err: ErrClosed}
	}
	return nil
}

// entriesOf returns the indexes of the entries of key.
func (f *File) entriesOf(k Key) []int {
	var idx []int
	sk := k.sectionKey()
	for i, it := range f.items {
		if it.kind == itemEntry && it.inSection && f.secOf(it) == sk && f.nameOf(it) == k.Name {
			idx = append(idx, i)
		}
	}
	return idx
}

// flatEntry is an entry as the checks of an edit compare them: entries
// before any header included, origins left out.
type flatEntry struct {
	inSection bool
	sec       sectionKey
	name      string
	value     string
	implicit  bool
}

func (e flatEntry) is(k Key) bool {
	return e.inSection && e.sec == k.sectionKey() && e.name == k.Name
}

// flatCursor walks the entries of a parsed file, without copying them out:
// the checks of an edit compare two whole files.
type flatCursor struct {
	p    *parsed
	i    int
	keep func(flatEntry) bool
}

// next returns the next entry keep accepts, or false at the end.
func (c *flatCursor) next() (flatEntry, bool) {
	for c.i < len(c.p.items) {
		it := c.p.items[c.i]
		c.i++
		if it.kind != itemEntry {
			continue
		}
		e := flatEntry{it.inSection, c.p.secOf(it), c.p.nameOf(it), c.p.valueOf(it), it.implicit}
		if c.keep == nil || c.keep(e) {
			return e, true
		}
	}
	return flatEntry{}, false
}

// sameEntries reports whether the entries of before that keepBefore accepts,
// changed by conv, are the entries of after that keepAfter accepts. A nil
// function keeps every entry, or changes none.
func sameEntries(before, after *parsed, keepBefore, keepAfter func(flatEntry) bool, conv func(flatEntry) flatEntry) bool {
	b := flatCursor{p: before, keep: keepBefore}
	a := flatCursor{p: after, keep: keepAfter}
	for {
		eb, okB := b.next()
		ea, okA := a.next()
		if okB != okA {
			return false
		}
		if !okB {
			return true
		}
		if conv != nil {
			eb = conv(eb)
		}
		if eb != ea {
			return false
		}
	}
}

// apply splices the bytes of the file, parses the result, and keeps it if
// check accepts the entries before and after the edit.
func (f *File) apply(key string, splices []splice, check func(before, after *parsed) bool) error {
	slices.SortFunc(splices, func(a, b splice) int { return a.start - b.start })
	data := make([]byte, 0, len(f.data)+64)
	at := 0
	for _, s := range splices {
		data = append(data, f.data[at:s.start]...)
		data = append(data, s.text...)
		at = s.end
	}
	data = append(data, f.data[at:]...)
	p, err := parse(f.name, data)
	if err != nil || !check(&f.parsed, &p) {
		return &KeyError{Key: key, Err: errUnsafeEdit}
	}
	f.data = data
	f.parsed = p
	f.snap = nil
	return nil
}

// checkKey returns a check that the entries of other keys are unchanged and
// that k has the values want after the edit. Only the first implicitOK of
// them may be implicit.
func checkKey(k Key, want []string, implicitOK int) func(before, after *parsed) bool {
	return func(before, after *parsed) bool {
		other := func(e flatEntry) bool { return !e.is(k) }
		if !sameEntries(before, after, other, other, nil) {
			return false
		}
		got := flatCursor{p: after, keep: func(e flatEntry) bool { return e.is(k) }}
		for i := 0; ; i++ {
			e, ok := got.next()
			if !ok {
				return i == len(want)
			}
			if i == len(want) || e.value != want[i] || e.implicit && i >= implicitOK {
				return false
			}
		}
	}
}

// Layout helpers. A token is a header or an entry; the whitespace before a
// token on its line is its own.

// ownStart returns where the token at i starts, its whitespace included.
func (f *File) ownStart(i int) int {
	if i > 0 && f.items[i-1].kind == itemSpace {
		return f.items[i-1].start
	}
	return f.items[i].start
}

// firstOnLine reports whether the token at i is the first of its line.
func (f *File) firstOnLine(i int) bool {
	j := i - 1
	if j >= 0 && f.items[j].kind == itemSpace {
		j--
	}
	return j < 0 || f.items[j].kind == itemNewline || f.items[j].kind == itemBOM
}

// newlineAfter returns the index of the first newline after item i, or -1
// if the file ends first.
func (f *File) newlineAfter(i int) int {
	for j := i + 1; j < len(f.items); j++ {
		if f.items[j].kind == itemNewline {
			return j
		}
	}
	return -1
}

// lineEnd returns the end of the line of item i, its newline included.
func (f *File) lineEnd(i int) int {
	if n := f.newlineAfter(i); n >= 0 {
		return f.items[n].end
	}
	return len(f.data)
}

// terminator returns what must be written at the end of the file before a
// new line can start there.
func (f *File) terminator() string {
	if len(f.items) == 0 {
		return ""
	}
	last := f.items[len(f.items)-1]
	switch {
	case last.kind == itemNewline || last.kind == itemBOM:
		return ""
	case last.kind == itemEntry && last.open == continued:
		// A blank line ends the value.
		return "\n"
	case last.kind == itemEntry && last.open == backslashEOF:
		return "\n\n"
	}
	return "\n"
}

// formatEntry returns the line git writes for an entry.
func formatEntry(name, value string) string {
	return "\t" + name + " = " + quoteValue(value) + "\n"
}

// quoteValue quotes and escapes a value as git writes it: in quotes if it
// starts or ends with a space or contains '#', ';' or a CR; backslash,
// double quote, tab and newline escaped.
func quoteValue(v string) string {
	var sb strings.Builder
	quote := strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ") || strings.ContainsAny(v, "#;\r")
	if quote {
		sb.WriteByte('"')
	}
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case '\\', '"':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\t':
			sb.WriteString(`\t`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteByte(c)
		}
	}
	if quote {
		sb.WriteByte('"')
	}
	return sb.String()
}

// formatHeader returns a section header as git writes it.
func formatHeader(section, sub string, hasSub bool) string {
	if !hasSub {
		return "[" + section + "]"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return "[" + section + " \"" + r.Replace(sub) + "\"]"
}

// replaceEntry returns the splice that rewrites the entry at i.
func (f *File) replaceEntry(i int, name, value string) splice {
	line := formatEntry(name, value)
	if !f.firstOnLine(i) {
		// After a header on the same line: the line is split.
		line = "\n" + line
	}
	return splice{f.ownStart(i), f.entryLineEnd(i), line}
}

// removeEntry returns the splice that removes the entry at i: its line, or
// the rest of the line after a header, which then ends with a newline.
func (f *File) removeEntry(i int) splice {
	if f.firstOnLine(i) {
		return splice{f.ownStart(i), f.entryLineEnd(i), ""}
	}
	return splice{f.ownStart(i), f.entryLineEnd(i), "\n"}
}

// entryLineEnd returns where the line of the entry at i ends for git when it
// removes or rewrites it: after its newline, and after the CR of the next
// line if that line is a blank CRLF line (git turns it into a blank LF line).
func (f *File) entryLineEnd(i int) int {
	n := f.newlineAfter(i)
	if n < 0 {
		return len(f.data)
	}
	end := f.items[n].end
	if n+1 < len(f.items) && f.items[n+1].kind == itemNewline && f.items[n+1].end-f.items[n+1].start == 2 {
		end++
	}
	return end
}

// unsetEntries returns the splices that remove the entries at idx, all of
// the same key, with the headers of their section where nothing remains in
// them, as git does. git decides for each entry on the file as it is before
// the edit:
//
//   - Headers of the same section that follow each other count as one.
//   - Looking back from the entry, the headers go only if one of them is
//     reached before any other entry or a header of another section.
//   - Looking forward, past the other removed entries, an entry before the
//     next header of another section keeps them.
//   - A comment between the previous entry or header of another section and
//     that next header keeps them too, except the comments at the end of the
//     removed entries' lines.
//
// The region removed then starts after the previous entry's line, or right
// after the previous header or a BOM (the rest of its line becomes a
// newline), and ends at the next header, its indentation included.
func (f *File) unsetEntries(idx []int) []splice {
	// Comments at the end of a removed entry's line play no part.
	trailing := make(map[int]bool)
	for _, i := range idx {
		for j := i + 1; j < len(f.items) && f.items[j].kind != itemNewline; j++ {
			if f.items[j].kind == itemComment {
				trailing[j] = true
			}
		}
	}
	// comments[j] counts the comments of items[:j] that play a part.
	comments := make([]int, len(f.items)+1)
	for j, it := range f.items {
		comments[j+1] = comments[j]
		if it.kind == itemComment && !trailing[j] {
			comments[j+1]++
		}
	}

	// From the last entry back, so that a forward scan reaching a removed
	// entry can stop where that entry's own scan did.
	forward := make(map[int]int, len(idx))
	splices := make([]splice, len(idx))
	for n := len(idx) - 1; n >= 0; n-- {
		splices[n] = f.unsetEntry(idx[n], forward, comments)
	}

	// Merge the regions that overlap: a region may hold other entries.
	slices.SortFunc(splices, func(a, b splice) int { return a.start - b.start })
	var merged []splice
	for _, sp := range splices {
		if n := len(merged); n > 0 && sp.start < merged[n-1].end {
			merged[n-1].end = max(merged[n-1].end, sp.end)
			continue
		}
		merged = append(merged, sp)
	}
	return merged
}

// unsetEntry returns the splice that removes the entry at i. forward holds
// where the forward scan of each removed entry after i stopped; the scan of
// i is added to it. comments counts the comments that play a part, as
// unsetEntries computes it.
func (f *File) unsetEntry(i int, forward map[int]int, comments []int) splice {
	sec := f.items[i].sec
	sameHeader := func(it item) bool { return it.kind == itemHeader && f.secOf(it) == f.secs[sec] }

	headers := false
	b := i - 1
	for ; b >= 0; b-- {
		it := f.items[b]
		if it.kind == itemEntry || it.kind == itemHeader && !sameHeader(it) {
			break
		}
		if sameHeader(it) {
			headers = true
		}
	}
	fw := i + 1
	for ; fw < len(f.items); fw++ {
		if end, ok := forward[fw]; ok {
			// A removed entry: the scan goes on as its own did.
			fw = end
			break
		}
		it := f.items[fw]
		if it.kind == itemEntry || it.kind == itemHeader && !sameHeader(it) {
			break
		}
	}
	forward[i] = fw
	if !headers || fw < len(f.items) && f.items[fw].kind == itemEntry {
		return f.removeEntry(i)
	}

	start, text := 0, ""
	switch {
	case b < 0:
		if len(f.items) > 0 && f.items[0].kind == itemBOM {
			// git ends the line after a BOM as after a header.
			start, text = f.items[0].end, "\n"
		}
	case f.items[b].kind == itemEntry:
		start = f.lineEnd(b)
	default:
		start, text = f.items[b].end, "\n"
	}
	end := len(f.data)
	if fw < len(f.items) {
		end = f.items[fw].start
	}
	// The region is items[from:fw]: start and end are item boundaries.
	from, _ := slices.BinarySearchFunc(f.items, start, func(it item, start int) int { return it.start - start })
	if comments[fw]-comments[from] > 0 {
		return f.removeEntry(i)
	}
	return splice{start, end, text}
}

// insert returns the splice that adds a new entry for k, at the end of the
// last section that matches, or in a new section at the end of the file.
func (f *File) insert(k Key, section, sub, name, value string) splice {
	sk := k.sectionKey()
	h := -1
	for i, it := range f.items {
		if it.kind == itemHeader && f.secOf(it) == sk {
			h = i
		}
	}
	line := formatEntry(name, value)
	if h < 0 {
		return splice{len(f.data), len(f.data), f.terminator() + formatHeader(section, sub, k.HasSubsection) + "\n" + line}
	}

	last := h
	for i := h + 1; i < len(f.items) && f.items[i].kind != itemHeader; i++ {
		if f.items[i].kind == itemEntry {
			last = i
		}
	}
	if f.items[last].kind == itemEntry {
		if n := f.newlineAfter(last); n >= 0 {
			end := f.items[n].end
			if f.entryLineEnd(last) != end {
				// git's end of the entry takes the CR of a blank CRLF
				// line: the new line goes after that blank line.
				end = f.items[n+1].end
			}
			return splice{end, end, line}
		}
		return splice{len(f.data), len(f.data), f.terminator() + line}
	}
	// After a header: right after its newline, or splitting its line.
	if last+1 < len(f.items) && f.items[last+1].kind == itemNewline {
		end := f.items[last+1].end
		return splice{end, end, line}
	}
	end := f.items[last].end
	return splice{end, end, "\n" + line}
}

// sectionArg validates the name of a section given to a section edit.
func sectionArg(section string) error {
	if section == "" || !validSection(section) {
		return &KeyError{Key: section, Err: ErrInvalidKey}
	}
	return nil
}

// headersOf returns the indexes of the headers of sk.
func (f *File) headersOf(sk sectionKey) []int {
	var idx []int
	for i, it := range f.items {
		if it.kind == itemHeader && f.secOf(it) == sk {
			idx = append(idx, i)
		}
	}
	return idx
}

// removeSection removes every header of sk and what follows it up to the
// next header, whose own whitespace is kept.
func (f *File) removeSection(name string, sk sectionKey) (bool, error) {
	headers := f.headersOf(sk)
	if len(headers) == 0 {
		return false, nil
	}
	var splices []splice
	for _, h := range headers {
		end := len(f.data)
		for j := h + 1; j < len(f.items); j++ {
			if f.items[j].kind == itemHeader {
				end = f.ownStart(j)
				break
			}
		}
		splices = append(splices, splice{f.ownStart(h), end, ""})
	}
	err := f.apply(name, splices, func(before, after *parsed) bool {
		return sameEntries(before, after, func(e flatEntry) bool { return !e.inSection || e.sec != sk }, nil, nil)
	})
	return err == nil, err
}

// renameSection rewrites every header of sk as header, and checks that the
// entries of sk now have section to.
func (f *File) renameSection(name string, sk, to sectionKey, header string) error {
	headers := f.headersOf(sk)
	if len(headers) == 0 {
		return &KeyError{Key: name, Err: ErrNotFound}
	}
	var splices []splice
	done := -1 // headers up to this index are rewritten with their line
	for _, h := range headers {
		if h <= done {
			continue
		}
		if !f.firstOnLine(h) {
			splices = append(splices, splice{f.items[h].start, f.items[h].end, header})
			continue
		}
		// git rewrites the line: the header alone, then the rest of the
		// line, if any, on a line of its own.
		rest := h + 1
		for rest < len(f.items) && f.items[rest].kind == itemSpace {
			rest++
		}
		end := len(f.items)
		if n := f.newlineAfter(h); n >= 0 {
			end = n + 1
		}
		var text strings.Builder
		text.WriteString(header + "\n")
		if rest < end && f.items[rest].kind != itemNewline {
			text.WriteByte('\t')
			for _, it := range f.items[rest:end] {
				if it.kind == itemHeader && f.secOf(it) == sk {
					text.WriteString(header)
				} else {
					text.Write(f.data[it.start:it.end])
				}
			}
		}
		splices = append(splices, splice{f.ownStart(h), f.lineEnd(h), text.String()})
		done = end - 1
	}
	return f.apply(name, splices, func(before, after *parsed) bool {
		return sameEntries(before, after, nil, nil, func(e flatEntry) flatEntry {
			if e.inSection && e.sec == sk {
				e.sec = to
			}
			return e
		})
	})
}
