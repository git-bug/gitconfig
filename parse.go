package gitconfig

import (
	"bytes"
	"strings"
)

// A file is kept as its bytes plus a list of items covering them exactly,
// in order: parsing never drops a byte, and edits splice the bytes, then
// parse them again.

type itemKind uint8

const (
	itemBOM     itemKind = iota // the UTF-8 byte order mark, first in the file
	itemSpace                   // spaces, tabs and lone CRs
	itemNewline                 // "\n" or "\r\n"
	itemComment                 // from '#' or ';' to the end of the line, newline excluded
	itemHeader                  // from '[' to ']'
	itemEntry                   // from the key to the end of the value, continuation lines included
)

// item is one piece of the file. It holds no pointer, so that a large file
// costs the garbage collector nothing to scan: the decoded data of headers
// and entries is in the tables of parsed. Offsets are 32-bit, as a file is
// at most maxLoadBytes: an item is 24 bytes.
type item struct {
	kind     itemKind
	implicit bool    // entries only: no '='
	open     openEnd // entries only
	sec      int32   // the section a header opens or an entry is in: index in parsed.secs, 0 before any header
	text     int32   // entries only: index in parsed.texts
	start    int32   // bytes of the item: data[start:end]
	end      int32
	line     int32 // line of start, 1-based
}

// entryText is the key name and the value of an entry.
type entryText struct {
	name  string // lowered
	value string // decoded
}

// parsed is a parsed file: its items, and the data they refer to.
type parsed struct {
	items []item
	// secs[0] is the zero sectionKey, for entries before any header. No
	// header has it: a header always has a section name or a subsection.
	secs  []sectionKey
	texts []entryText
}

func (p *parsed) secOf(it item) sectionKey { return p.secs[it.sec] }
func (p *parsed) nameOf(it item) string    { return p.texts[it.text].name }
func (p *parsed) valueOf(it item) string   { return p.texts[it.text].value }

// entries appends the entries of the file, those before any header left
// out, to out.
func (p *parsed) entries(scope Scope, file string, out []Entry) []Entry {
	for _, it := range p.items {
		if it.kind != itemEntry || it.sec == 0 {
			continue
		}
		sec := p.secs[it.sec]
		t := p.texts[it.text]
		out = append(out, Entry{
			Key:      Key{Section: sec.section, Subsection: sec.subsection, HasSubsection: sec.hasSub, Name: t.name},
			Value:    t.value,
			Implicit: it.implicit,
			Origin:   Origin{Scope: scope, File: file, Line: int(it.line)},
		})
	}
	return out
}

// openEnd tells how an entry at the end of the file ends, for an edit that
// adds a line after it.
type openEnd uint8

const (
	closed       openEnd = iota // the next line is not part of the value
	continued                   // a backslash-newline ends the file: the next line would continue the value
	backslashEOF                // a backslash ends the file: a newline would continue the value
)

var bom = []byte("\xef\xbb\xbf")

type parser struct {
	parsed
	name string
	data []byte
	pos  int
	line int

	// budget is what remains of the entries and distinct header spellings
	// allowed, shared by the files of a load.
	budget *int

	// emit, if set, receives the entries of the file, those before any
	// header left out, and no item is kept: the parse of a load. An error
	// it returns stops the parse and is returned as is.
	emit  func(Entry) error
	scope Scope // of the entries given to emit

	sec      int32 // current section
	secIndex map[sectionKey]int32
	headers  map[string]int32  // the section of each header, by its bytes
	names    map[string]string // key names, interned
	buf      []byte            // scratch for values and subsections
}

func newParser(name string, data []byte, budget *int) *parser {
	return &parser{
		name: name, data: data, line: 1, budget: budget,
		parsed:   parsed{secs: []sectionKey{{}}},
		secIndex: make(map[sectionKey]int32),
		headers:  make(map[string]int32),
		names:    make(map[string]string),
	}
}

// parseFile splits data into items, or returns the *SyntaxError git would
// report. Beyond the limits of one file, it returns ErrLoadLimit.
func parseFile(name string, data []byte) (parsed, error) {
	if len(data) > maxLoadBytes {
		return parsed{}, ErrLoadLimit
	}
	p := newParser(name, data, new(maxLoadEntries))
	p.items = make([]item, 0, min(2*bytes.Count(data, []byte{'\n'})+4, maxFileItems))
	if err := p.run(); err != nil {
		return parsed{}, err
	}
	return p.parsed, nil
}

// parseEntries gives the entries of data to emit, in order, with scope and
// name as their origin, without keeping the items of the file. It returns
// the *SyntaxError git would report, ErrLoadLimit once budget is spent, or
// the error of emit.
//
// Like git, it stops at the first error: entries before it have been given
// to emit, and an include they hold has been followed.
func parseEntries(name string, data []byte, scope Scope, budget *int, emit func(Entry) error) error {
	p := newParser(name, data, budget)
	p.emit, p.scope = emit, scope
	return p.run()
}

func (p *parser) errorAt(line int, msg string) error {
	return &SyntaxError{File: p.name, Line: line, Msg: msg}
}

// spend takes one from the budget.
func (p *parser) spend() error {
	if *p.budget <= 0 {
		return ErrLoadLimit
	}
	*p.budget--
	return nil
}

// add adds an item, unless the parse keeps no item.
func (p *parser) add(kind itemKind, start, end int) {
	if p.emit == nil {
		p.items = append(p.items, item{kind: kind, start: int32(start), end: int32(end), line: int32(p.line), sec: p.sec})
	}
}

// addEntry adds an entry item, or gives the entry to emit.
func (p *parser) addEntry(start, end int, name []byte, value string, implicit bool, open openEnd) error {
	if err := p.spend(); err != nil {
		return err
	}
	n, ok := p.names[string(name)]
	if !ok {
		n = lowerASCII(string(name))
		p.names[string(name)] = n
	}
	if p.emit != nil {
		if p.sec == 0 {
			return nil
		}
		sec := p.secs[p.sec]
		return p.emit(Entry{
			Key:      Key{Section: sec.section, Subsection: sec.subsection, HasSubsection: sec.hasSub, Name: n},
			Value:    value,
			Implicit: implicit,
			Origin:   Origin{Scope: p.scope, File: p.name, Line: p.line},
		})
	}
	p.items = append(p.items, item{
		kind: itemEntry, implicit: implicit, open: open, sec: p.sec, text: int32(len(p.texts)),
		start: int32(start), end: int32(end), line: int32(p.line),
	})
	p.texts = append(p.texts, entryText{n, value})
	return nil
}

// section returns the index of sec in p.secs, adding it if absent.
func (p *parser) section(sec sectionKey) int32 {
	i, ok := p.secIndex[sec]
	if !ok {
		i = int32(len(p.secs))
		p.secs = append(p.secs, sec)
		p.secIndex[sec] = i
	}
	return i
}

// isSpace reports whether data[i] is whitespace between tokens: a space, a
// tab, or a CR that does not start a CRLF.
func isSpace(data []byte, i int) bool {
	switch data[i] {
	case ' ', '\t':
		return true
	case '\r':
		return !isNewline(data, i)
	}
	return false
}

// isNewline reports whether a newline, "\n" or "\r\n", starts at data[i].
func isNewline(data []byte, i int) bool {
	return data[i] == '\n' || data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n'
}

// newlineLen returns the length of the newline at data[i].
func newlineLen(data []byte, i int) int {
	if data[i] == '\r' {
		return 2
	}
	return 1
}

func (p *parser) run() error {
	d := p.data
	if bytes.HasPrefix(d, bom) {
		p.add(itemBOM, 0, len(bom))
		p.pos = len(bom)
	}
	for p.pos < len(d) {
		// Each turn adds at most one item.
		if len(p.items) >= maxFileItems {
			return ErrLoadLimit
		}
		start := p.pos
		c := d[start]
		switch {
		case isSpace(d, start):
			for p.pos < len(d) && isSpace(d, p.pos) {
				p.pos++
			}
			p.add(itemSpace, start, p.pos)
		case isNewline(d, start):
			p.pos += newlineLen(d, start)
			p.add(itemNewline, start, p.pos)
			p.line++
		case c == '#' || c == ';':
			end := len(d)
			if i := bytes.IndexByte(d[start:], '\n'); i >= 0 {
				end = start + i
				if d[end-1] == '\r' {
					end--
				}
			}
			p.pos = end
			p.add(itemComment, start, end)
		case c == '[':
			if err := p.header(); err != nil {
				return err
			}
		case isAlpha(c):
			if err := p.entry(); err != nil {
				return err
			}
		default:
			return p.errorAt(p.line, "invalid line")
		}
	}
	return nil
}

func (p *parser) header() error {
	d := p.data
	start := p.pos
	i := start + 1
	for i < len(d) && (isKeyChar(d[i]) || d[i] == '.') {
		i++
	}
	nameEnd := i
	if i == len(d) {
		// git counts the end of the line it stopped at, and so reports the
		// next line for an unterminated header or value.
		return p.errorAt(p.line+1, "unterminated section header")
	}

	// sub reuses the parser's scratch buffer, and gives it back grown.
	sub := p.buf[:0]
	defer func() { p.buf = sub[:0] }()
	hasSub := false
	switch {
	case d[i] == ']':
		if nameEnd == start+1 {
			return p.errorAt(p.line, "empty section name")
		}
		i++

	case isSpace(d, i):
		for i < len(d) && isSpace(d, i) {
			i++
		}
		if i == len(d) || d[i] != '"' {
			return p.errorAt(p.line, "invalid section header")
		}
		i++
		for {
			if i == len(d) || d[i] == '\n' {
				return p.errorAt(p.line, "unterminated subsection")
			}
			c := d[i]
			if c == '"' {
				break
			}
			if c == '\\' {
				i++
				if i == len(d) || d[i] == '\n' {
					return p.errorAt(p.line, "unterminated subsection")
				}
				c = d[i]
			}
			if c == 0 {
				// git cuts the subsection at the NUL, reading a key the
				// file does not spell.
				return p.errorAt(p.line, "NUL byte in subsection")
			}
			sub = append(sub, c)
			i++
		}
		i++
		if i == len(d) || isNewline(d, i) {
			return p.errorAt(p.line+1, "unterminated section header")
		}
		if d[i] != ']' {
			return p.errorAt(p.line, "invalid section header")
		}
		i++
		hasSub = true

	default:
		return p.errorAt(p.line, "invalid section header")
	}

	// The section is decoded once for each spelling of a header: files
	// repeat their headers.
	sec, ok := p.headers[string(d[start:i])]
	if !ok {
		if err := p.spend(); err != nil {
			return err
		}
		sec = p.section(headerKey(lowerASCII(string(d[start+1:nameEnd])), sub, hasSub))
		p.headers[string(d[start:i])] = sec
	}
	p.sec = sec
	p.pos = i
	p.add(itemHeader, start, i)
	return nil
}

// headerKey returns the section of a header with the lowered name, and the
// subsection sub if hasSub.
func headerKey(name string, sub []byte, hasSub bool) sectionKey {
	dot := strings.IndexByte(name, '.')
	switch {
	case !hasSub && dot < 0:
		return sectionKey{section: name}
	case !hasSub:
		// Legacy header [a.B]: subsection "b".
		return sectionKey{name[:dot], name[dot+1:], true}
	case dot < 0:
		return sectionKey{name, string(sub), true}
	}
	// [a.b "x"]: git reads the keys as a.b.x.<name>.
	return sectionKey{name[:dot], name[dot+1:] + "." + string(sub), true}
}

func (p *parser) entry() error {
	d := p.data
	start := p.pos
	i := start
	for i < len(d) && isKeyChar(d[i]) {
		i++
	}
	name := d[start:i]
	j := i
	for j < len(d) && (d[j] == ' ' || d[j] == '\t') {
		j++
	}
	if j == len(d) || isNewline(d, j) {
		// No '=': an implicit value. The entry ends with its name; the
		// whitespace after it is an item of its own.
		p.pos = i
		return p.addEntry(start, i, name, "", true, closed)
	}
	if d[j] != '=' {
		return p.errorAt(p.line, "invalid key")
	}

	value, end, lines, open, err := p.value(j + 1)
	if err != nil {
		return err
	}
	p.pos = end
	if err := p.addEntry(start, end, name, value, false, open); err != nil {
		return err
	}
	p.line += lines
	return nil
}

// value decodes the value starting at data[i], up to a comment, the end of
// the line or the end of the file. It returns the value, where it ends and
// how many continuation lines it spans, and how it ends if it ends the file.
//
// Whitespace outside quotes is skipped before the value, and held back
// inside it: an escape, a quote or any other character writes it out first,
// and the end of the value drops it.
func (p *parser) value(i int) (string, int, int, openEnd, error) {
	d := p.data
	buf := p.buf[:0]
	defer func() { p.buf = buf[:0] }()
	pending := -1 // start of held-back whitespace, -1 if none
	inQuote := false
	lines := 0
	open := closed
	flush := func(at int) {
		if pending >= 0 {
			buf = append(buf, d[pending:at]...)
			pending = -1
		}
	}
loop:
	for i < len(d) && !isNewline(d, i) {
		c := d[i]
		switch {
		case c == '\\':
			if i+1 == len(d) {
				// A backslash ends the file. Outside quotes it is dropped.
				if inQuote {
					return "", 0, 0, 0, p.errorAt(p.line+lines+1, "unterminated quote")
				}
				flush(i)
				i++
				open = backslashEOF
				continue
			}
			flush(i)
			if isNewline(d, i+1) {
				i += 1 + newlineLen(d, i+1)
				lines++
				if i == len(d) {
					open = continued
				}
				continue
			}
			switch d[i+1] {
			case 't':
				buf = append(buf, '\t')
			case 'n':
				buf = append(buf, '\n')
			case 'b':
				buf = append(buf, '\b')
			case '"', '\\':
				buf = append(buf, d[i+1])
			default:
				return "", 0, 0, 0, p.errorAt(p.line+lines, "invalid escape")
			}
			i += 2
		case c == '"':
			flush(i)
			inQuote = !inQuote
			i++
		case inQuote:
			buf = append(buf, c)
			i++
		case c == ' ' || c == '\t' || c == '\r':
			// A CR here is a lone one: the loop stops at a CRLF.
			if len(buf) > 0 && pending < 0 {
				pending = i
			}
			i++
		case c == '#' || c == ';':
			break loop
		default:
			flush(i)
			buf = append(buf, c)
			i++
		}
	}
	// A comment ends the value only outside quotes: the quote is open only
	// at a newline or the end of the file.
	if inQuote {
		return "", 0, 0, 0, p.errorAt(p.line+lines, "unterminated quote")
	}
	if n := bytes.IndexByte(buf, 0); n >= 0 {
		// git stops reading the value at a NUL byte.
		buf = buf[:n]
	}
	return string(buf), i, lines, open, nil
}
