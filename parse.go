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
// and entries is in the tables of parsed.
type item struct {
	kind      itemKind
	inSection bool    // false for an entry before any header
	implicit  bool    // entries only: no '='
	open      openEnd // entries only
	sec       int32   // the section a header opens or an entry is in: index in parsed.secs
	text      int32   // entries only: index in parsed.texts
	start     int     // bytes of the item: data[start:end]
	end       int
	line      int // line of start, 1-based
}

// entryText is the key name and the value of an entry.
type entryText struct {
	name  string // lowered
	value string // decoded
}

// parsed is a parsed file: its items, and the data they refer to.
type parsed struct {
	items []item
	secs  []sectionKey // secs[0] is the zero sectionKey, for entries before any header
	texts []entryText
}

func (p *parsed) secOf(it item) sectionKey { return p.secs[it.sec] }

// entries appends the entries of the file, those before any header left
// out, to out.
func (p *parsed) entries(scope Scope, file string, out []Entry) []Entry {
	for _, it := range p.items {
		if it.kind != itemEntry || !it.inSection {
			continue
		}
		sec := p.secs[it.sec]
		t := p.texts[it.text]
		out = append(out, Entry{
			Key:      Key{Section: sec.section, Subsection: sec.subsection, HasSubsection: sec.hasSub, Name: t.name},
			Value:    t.value,
			Implicit: it.implicit,
			Origin:   Origin{Scope: scope, File: file, Line: it.line},
		})
	}
	return out
}
func (p *parsed) nameOf(it item) string  { return p.texts[it.text].name }
func (p *parsed) valueOf(it item) string { return p.texts[it.text].value }

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

	sec       int32 // current section
	inSection bool
	secIndex  map[sectionKey]int32
	names     map[string]string // key names, interned
	buf       []byte            // scratch for values
}

// parse splits data into items, or returns the *SyntaxError git would
// report.
func parse(name string, data []byte) (parsed, error) {
	p := &parser{name: name, data: data, line: 1, secIndex: make(map[sectionKey]int32), names: make(map[string]string)}
	p.items = make([]item, 0, 2*bytes.Count(data, []byte{'\n'})+4)
	p.secs = []sectionKey{{}}
	if err := p.run(); err != nil {
		return parsed{}, err
	}
	return p.parsed, nil
}

func (p *parser) errorAt(line int, msg string) error {
	return &SyntaxError{File: p.name, Line: line, Msg: msg}
}

func (p *parser) add(kind itemKind, start, end int) *item {
	p.items = append(p.items, item{kind: kind, start: start, end: end, line: p.line, sec: p.sec, inSection: p.inSection})
	return &p.items[len(p.items)-1]
}

// addEntry adds an entry item.
func (p *parser) addEntry(start, end int, name []byte, value string) *item {
	it := p.add(itemEntry, start, end)
	n, ok := p.names[string(name)]
	if !ok {
		n = lowerASCII(string(name))
		p.names[string(name)] = n
	}
	it.text = int32(len(p.texts))
	p.texts = append(p.texts, entryText{n, value})
	return it
}

// setSection makes sec the current section.
func (p *parser) setSection(sec sectionKey) {
	i, ok := p.secIndex[sec]
	if !ok {
		i = int32(len(p.secs))
		p.secs = append(p.secs, sec)
		p.secIndex[sec] = i
	}
	p.sec = i
	p.inSection = true
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
	name := lowerASCII(string(d[start+1 : i]))
	if i == len(d) {
		return p.errorAt(p.line+1, "unterminated section header")
	}

	var sec sectionKey
	switch {
	case d[i] == ']':
		if name == "" {
			return p.errorAt(p.line, "empty section name")
		}
		sec.section = name
		if dot := strings.IndexByte(name, '.'); dot >= 0 {
			// Legacy header [a.B]: subsection "b".
			sec = sectionKey{name[:dot], name[dot+1:], true}
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
		var sub []byte
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
		sec = sectionKey{name, string(sub), true}
		if dot := strings.IndexByte(name, '.'); dot >= 0 {
			// [a.b "x"]: git reads the keys as a.b.x.<name>.
			sec = sectionKey{name[:dot], name[dot+1:] + "." + string(sub), true}
		}

	default:
		return p.errorAt(p.line, "invalid section header")
	}

	p.setSection(sec)
	p.pos = i
	p.add(itemHeader, start, i)
	return nil
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
		p.pos = i
		p.addEntry(start, i, name, "").implicit = true
		return nil
	}
	if d[j] != '=' {
		return p.errorAt(p.line, "invalid key")
	}

	value, end, lines, open, err := p.value(j + 1)
	if err != nil {
		return err
	}
	p.pos = end
	p.addEntry(start, end, name, value).open = open
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
	for i < len(d) {
		c := d[i]
		if isNewline(d, i) {
			if inQuote {
				return "", 0, 0, 0, p.errorAt(p.line+lines, "unterminated quote")
			}
			break
		}
		switch {
		case c == '\\':
			if i+1 == len(d) {
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
			if len(buf) > 0 && pending < 0 {
				pending = i
			}
			i++
		case c == '#' || c == ';':
			goto end
		default:
			flush(i)
			buf = append(buf, c)
			i++
		}
	}
	if inQuote {
		return "", 0, 0, 0, p.errorAt(p.line+lines, "unterminated quote")
	}
end:
	if n := bytes.IndexByte(buf, 0); n >= 0 {
		// git stops reading the value at a NUL byte.
		buf = buf[:n]
	}
	return string(buf), i, lines, open, nil
}
