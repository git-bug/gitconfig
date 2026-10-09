package gitconfig

// The patterns of includeIf "gitdir:" and "onbranch:" are matched as paths:
// '*' and '?' do not match '/', "**/" at the start of a segment matches any
// number of directories, "**" ending the pattern after a '/' (or alone)
// matches anything, "[...]" is a class of characters ("[!...]" or "[^...]"
// negated, "a-z" a range) and '\' quotes the next character.
//
// The pattern is compiled into tokens and matched by simulating every
// possible position in the pattern at once, so that the time is linear in
// the length of the name for a given pattern: patterns come from
// configuration files someone else may have written.

type globKind uint8

const (
	globLit   globKind = iota // one given character
	globAny                   // '?': any character but '/'
	globClass                 // "[...]": a character of the class, never '/'
	globStar                  // '*': any run of characters without '/'
	globDirs                  // "**/": nothing, or anything ending with '/'
	globAll                   // final "**": anything
)

type globToken struct {
	kind   globKind
	c      byte      // globLit
	ranges [][2]byte // globClass: inclusive ranges
	negate bool      // globClass
}

func compileGlob(p string) []globToken {
	var toks []globToken
	segStart := true
	for i := 0; i < len(p); {
		c := p[i]
		switch {
		case c == '*' && segStart && i+1 < len(p) && p[i+1] == '*' && (i+2 == len(p) || p[i+2] == '/'):
			if i+2 == len(p) {
				toks = append(toks, globToken{kind: globAll})
				i += 2
				continue
			}
			if len(toks) == 0 || toks[len(toks)-1].kind != globDirs {
				// "**/**/" is "**/".
				toks = append(toks, globToken{kind: globDirs})
			}
			i += 3
			continue // still at the start of a segment
		case c == '*':
			for i < len(p) && p[i] == '*' {
				i++
			}
			toks = append(toks, globToken{kind: globStar})
		case c == '?':
			toks = append(toks, globToken{kind: globAny})
			i++
		case c == '[':
			if tok, n, ok := compileClass(p[i:]); ok {
				toks = append(toks, tok)
				i += n
			} else {
				toks = append(toks, globToken{kind: globLit, c: c})
				i++
			}
		case c == '\\' && i+1 < len(p):
			toks = append(toks, globToken{kind: globLit, c: p[i+1]})
			i += 2
		default:
			toks = append(toks, globToken{kind: globLit, c: c})
			i++
		}
		segStart = toks[len(toks)-1].kind == globLit && toks[len(toks)-1].c == '/'
	}
	return toks
}

// compileClass compiles the class at the start of p, and returns its length,
// or false if it is not terminated.
func compileClass(p string) (globToken, int, bool) {
	tok := globToken{kind: globClass}
	i := 1
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		tok.negate = true
		i++
	}
	first := true
	for i < len(p) {
		c := p[i]
		if c == ']' && !first {
			return tok, i + 1, true
		}
		first = false
		if c == '\\' && i+1 < len(p) {
			i++
			c = p[i]
		}
		lo, hi := c, c
		if i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']' {
			hi = p[i+2]
			if hi == '\\' && i+3 < len(p) {
				hi = p[i+3]
				i++
			}
			i += 2
		}
		tok.ranges = append(tok.ranges, [2]byte{lo, hi})
		i++
	}
	return globToken{}, 0, false
}

func (t globToken) inClass(c byte, fold bool) bool {
	in := false
	for _, r := range t.ranges {
		if r[0] <= c && c <= r[1] || fold && r[0] <= swapCase(c) && swapCase(c) <= r[1] {
			in = true
			break
		}
	}
	return in != t.negate
}

func swapCase(c byte) byte {
	if isAlpha(c) {
		return c ^ 0x20
	}
	return c
}

func sameByte(a, b byte, fold bool) bool {
	return a == b || fold && isAlpha(a) && a^0x20 == b
}

// globMatch reports whether name matches pattern. With fold, the case of
// ASCII letters is ignored.
func globMatch(pattern, name string, fold bool) bool {
	toks := compileGlob(pattern)
	n := int32(len(toks))
	// A state is a position in the pattern, 2*s for token s, or 2*s+1 for
	// the inside of the directories matched by the globDirs token s. Only
	// the states reached are visited.
	cur := newStateSet(2*n + 2)
	next := newStateSet(2*n + 2)

	// enter adds the state of token s and those reachable without a
	// character: past a '*', "**/" or final "**".
	enter := func(set *stateSet, s int32) {
		for set.add(2*s) && s < n {
			if k := toks[s].kind; k != globStar && k != globDirs && k != globAll {
				break
			}
			s++
		}
	}

	enter(cur, 0)
	for i := 0; i < len(name); i++ {
		c := name[i]
		next.reset()
		for _, id := range cur.list {
			s := id / 2
			if id%2 == 1 {
				// Inside "**/": anything, and a '/' may end it.
				next.add(id)
				if c == '/' {
					enter(next, s+1)
				}
				continue
			}
			if s == n {
				continue
			}
			switch t := toks[s]; t.kind {
			case globLit:
				if sameByte(c, t.c, fold) {
					enter(next, s+1)
				}
			case globAny:
				if c != '/' {
					enter(next, s+1)
				}
			case globClass:
				if c != '/' && t.inClass(c, fold) {
					enter(next, s+1)
				}
			case globStar:
				if c != '/' {
					enter(next, s)
				}
			case globDirs:
				next.add(id + 1)
				if c == '/' {
					enter(next, s+1)
				}
			case globAll:
				enter(next, s)
			}
		}
		if len(next.list) == 0 {
			return false
		}
		cur, next = next, cur
	}
	return cur.has(2 * n)
}

// stateSet is a set of states, listed in the order they were added.
type stateSet struct {
	list []int32
	mark []uint32 // mark[id] == gen if id is in the set
	gen  uint32
}

func newStateSet(size int32) *stateSet {
	return &stateSet{mark: make([]uint32, size), gen: 1}
}

func (ss *stateSet) reset() {
	ss.list = ss.list[:0]
	ss.gen++
}

// add adds id and reports whether it was absent.
func (ss *stateSet) add(id int32) bool {
	if ss.mark[id] == ss.gen {
		return false
	}
	ss.mark[id] = ss.gen
	ss.list = append(ss.list, id)
	return true
}

func (ss *stateSet) has(id int32) bool {
	return ss.mark[id] == ss.gen
}
