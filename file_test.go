package gitconfig_test

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

var parseValueCases = []struct {
	name string
	data string
	key  string
	want string
}{
	{"plain", "[a]\n\tk = v\n", "a.k", "v"},
	{"no spaces around =", "[a]\nk=v\n", "a.k", "v"},
	{"spaces around =", "[a]\n\tk   =   v\n", "a.k", "v"},
	{"inner whitespace kept", "[a]\n\tk = a    b\n", "a.k", "a    b"},
	{"trailing whitespace trimmed", "[a]\n\tk = v   \n", "a.k", "v"},
	{"quoted whitespace kept", "[a]\n\tk = \" v \"  \n", "a.k", " v "},
	{"quotes mid-value", "[a]\n\tk = a \"b c\" d\n", "a.k", "a b c d"},
	{"quoted semicolon", "[a]\n\tk = \"x;y\"\n", "a.k", "x;y"},
	{"quoted hash then comment", "[a]\n\tk = \"x # y\" # c\n", "a.k", "x # y"},
	{"semicolon comment", "[a]\n\tk = x ; y\n", "a.k", "x"},
	{"hash comment", "[a]\n\tk = x # y\n", "a.k", "x"},
	{"escaped backslash then n", "[a]\n\tk = x\\\\ny\n", "a.k", `x\ny`},
	{"escapes", "[a]\n\tk = a\\tb\\nc\\bd\\\"e\n", "a.k", "a\tb\nc\bd\"e"},
	{"continuation", "[a]\n\tk = one \\\n two\n", "a.k", "one  two"},
	{"continuation inside quotes", "[a]\n\tk = \"a \\\n b\"\n", "a.k", "a  b"},
	{"continuation with CRLF", "[a]\n\tk = one \\\r\n two\n", "a.k", "one  two"},
	{"backslash in comment does not continue", "[a]\n\tk = v # c \\\n\tj = w\n", "a.j", "w"},
	{"CR inside a value", "[a]\n\tk = x\ry\n", "a.k", "x\ry"},
	{"comment after header", "[a] # c\n\tk = v\n", "a.k", "v"},
	{"entry on header line", "[a] k = v\n", "a.k", "v"},
	{"tab after header", "[a]\tk = v\n", "a.k", "v"},
	{"legacy subsection", "[a.B]\n\tk = v\n", "a.b.k", "v"},
	{"legacy subsection with dots", "[a.B.c]\n\tk = v\n", "a.b.c.k", "v"},
	{"escaped quote in subsection", "[a \"s\\\"q\"]\n\tk = v\n", `a.s"q.k`, "v"},
	{"unknown escape in subsection", "[a \"x\\y\"]\n\tk = v\n", "a.xy.k", "v"},
	{"dashes and digits", "[my-sec]\n\tmy-key1 = v\n", "my-sec.my-key1", "v"},
	{"section starting with a digit", "[1a]\n\tk = v\n", "1a.k", "v"},
	{"BOM and CRLF", "\xef\xbb\xbf[a]\r\n\tk = v\r\n", "a.k", "v"},
	{"no final newline", "[a]\n\tk = v", "a.k", "v"},
	{"empty value", "[a]\n\tk =\n", "a.k", ""},
}

func TestParseValues(t *testing.T) {
	for _, tc := range parseValueCases {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := mustConfig(t, tc.data).Get(tc.key)
			require.True(t, ok)
			assert.Equal(t, tc.want, e.Value)
			assert.False(t, e.Implicit)
		})
	}

	t.Run("empty file", func(t *testing.T) {
		assert.Empty(t, slices.Collect(mustConfig(t, "").All()))
	})

	t.Run("comments and blank lines only", func(t *testing.T) {
		assert.Empty(t, slices.Collect(mustConfig(t, "# a\n; b\n\n   \n").All()))
	})

	t.Run("empty section", func(t *testing.T) {
		assert.Empty(t, slices.Collect(mustConfig(t, "[a]\n[b \"c\"]\n").All()))
	})

	t.Run("implicit", func(t *testing.T) {
		assert.Equal(t, []string{"a.k", "a.j=v"}, list(slices.Collect(mustConfig(t, "[a]\n\tk\n\tj = v\n").All())))
	})

	t.Run("entry before any header", func(t *testing.T) {
		// git reads it under a key with no section, which no command can
		// ask for; it is kept in the file but not part of the Config.
		assert.Equal(t, []string{"a.j=w"}, list(slices.Collect(mustConfig(t, "k = v\n[a]\n\tj = w\n").All())))
	})

	t.Run("empty subsection is not none", func(t *testing.T) {
		assert.Equal(t, []string{"a..k=v", "a.k=w"}, list(slices.Collect(mustConfig(t, "[a \"\"]\n\tk = v\n[a]\n\tk = w\n").All())))
	})

	t.Run("name and scope", func(t *testing.T) {
		f, err := gitconfig.Parse("some/name", []byte("[a]\n\tk = v\n"))
		require.NoError(t, err)
		e, ok := f.Config().Get("a.k")
		require.True(t, ok)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeFile, File: "some/name", Line: 2}, e.Origin)
	})
}

var syntaxErrorCases = []struct {
	name string
	data string
	line int
}{
	{"unterminated header", "[a\n\tk = v\n", 1},
	{"space in section name", "[a b]\n\tk = v\n", 1},
	{"newline in subsection", "[a \"x\ny\"]\n", 1},
	{"missing key", "[a]\n= v\n", 2},
	{"key starting with a digit", "[a]\n\t1k = v\n", 2},
	{"underscore in key", "[a]\n\tk_y = v\n", 2},
	{"unterminated quote", "[a]\n\tk = \"x\n", 2},
	{"invalid escape", "[a]\n\tk = x\\q\n", 2},
	{"line after a continuation", "[a]\n\tk = a \\\nb\n[x\n", 4},
	{"CRLF", "[a]\r\n\tk = v\r\n[x\r\n", 3},
	{"NUL", "[a]\n\tk = v\n\x00", 3},
}

func TestParseSyntaxError(t *testing.T) {
	for _, tc := range syntaxErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gitconfig.Parse("f", []byte(tc.data))
			var se *gitconfig.SyntaxError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, "f", se.File)
			assert.Equal(t, tc.line, se.Line)
		})
	}
}

var roundTripCases = []string{
	"",
	"\n\n",
	"[a]\n\tk = v\n",
	"[a]\n\tk = v",
	"# comment\n; other\n[a] # c\n\tk = v ; trailing\n\n\n[b \"s\"]\n    j=w\n",
	"\xef\xbb\xbf[a]\r\n\tk = v\r\n",
	"[a]\r\n\tk = v\n\tj = w\r\n",
	"[a] k = v\n[A.B]\n  K\n",
	"[a]\n\tk = one \\\n  two \\\n three\n",
	"[a]\n\tk = \"quoted\"  \"twice\"\n\tj = esc\\\"aped\\\\\n",
	"[a \"\"]\n\tk =\n[a \"s\\\"q\"]\n\tk=\n",
	"\t\t[a]\t\t\n\t\tk\t=\tv\t\t\n",
	"[a]\n\tk = x\ry\n",
	"k = v\n[a]\n\tj = w\n",
	"\xef\xbb\xbf",
}

func TestParseRoundTrip(t *testing.T) {
	for _, data := range roundTripCases {
		t.Run(strconv.Quote(data), func(t *testing.T) {
			f := mustParse(t, data)
			assert.Equal(t, data, string(f.Bytes()))
		})
	}
}

func TestFileZeroValue(t *testing.T) {
	var f gitconfig.File
	assert.Empty(t, f.Bytes())
	assert.Empty(t, slices.Collect(f.Config().All()))

	require.NoError(t, f.Set("a.k", "v"))
	assert.Equal(t, "[a]\n\tk = v\n", string(f.Bytes()))
}

func TestFileConfigSnapshot(t *testing.T) {
	f := mustParse(t, "[a]\n\tk = 1\n[b]\n\tx = y\n")
	before := f.Config()

	require.NoError(t, f.Set("a.j", "2"))

	t.Run("old snapshot unchanged", func(t *testing.T) {
		assert.Equal(t, []string{"a.k=1", "b.x=y"}, list(slices.Collect(before.All())))
	})

	t.Run("new snapshot", func(t *testing.T) {
		after := f.Config()
		assert.Equal(t, []string{"a.k=1", "a.j=2", "b.x=y"}, list(slices.Collect(after.All())))
		e, _ := after.Get("b.x")
		assert.Equal(t, 5, e.Origin.Line)
		e, _ = after.Get("a.j")
		assert.Equal(t, 3, e.Origin.Line)
	})
}

// edit is one editing operation, as data, so that the same cases run
// against File and against `git config`.
type edit struct {
	op   string // the git config option, "" for a plain set
	args []string
}

func set(key, value string) edit        { return edit{"", []string{key, value}} }
func add(key, value string) edit        { return edit{"--add", []string{key, value}} }
func replaceAll(key, value string) edit { return edit{"--replace-all", []string{key, value}} }
func unset(key string) edit             { return edit{"--unset", []string{key}} }
func unsetAll(key string) edit          { return edit{"--unset-all", []string{key}} }
func removeSection(section string) edit { return edit{"--remove-section", []string{section}} }
func removeSubsection(section, sub string) edit {
	return edit{"--remove-section", []string{section, sub}}
}
func renameSection(oldName, newName string) edit {
	return edit{"--rename-section", []string{oldName, newName}}
}
func renameSubsection(section, oldSub, newSub string) edit {
	return edit{"--rename-section", []string{section, oldSub, newSub}}
}

// errNothingRemoved is returned by edit.apply when a removal found nothing:
// not an error for File, but one for git.
var errNothingRemoved = errors.New("nothing removed")

func (e edit) apply(f *gitconfig.File) error {
	var removed bool
	var err error
	switch e.op {
	case "":
		return f.Set(e.args[0], e.args[1])
	case "--add":
		return f.Add(e.args[0], e.args[1])
	case "--replace-all":
		return f.ReplaceAll(e.args[0], e.args[1])
	case "--unset":
		removed, err = f.Unset(e.args[0])
	case "--unset-all":
		var n int
		n, err = f.UnsetAll(e.args[0])
		removed = n > 0
	case "--remove-section":
		if len(e.args) == 1 {
			removed, err = f.RemoveSection(e.args[0])
		} else {
			removed, err = f.RemoveSubsection(e.args[0], e.args[1])
		}
	case "--rename-section":
		if len(e.args) == 2 {
			return f.RenameSection(e.args[0], e.args[1])
		}
		return f.RenameSubsection(e.args[0], e.args[1], e.args[2])
	default:
		panic("unknown edit " + e.op)
	}
	if err == nil && !removed {
		return errNothingRemoved
	}
	return err
}

// gitArgs returns the arguments of the equivalent `git config` command,
// after "-f <file>".
func (e edit) gitArgs() []string {
	args := e.args
	switch {
	case e.op == "--remove-section" && len(args) == 2:
		args = []string{args[0] + "." + args[1]}
	case e.op == "--rename-section" && len(args) == 3:
		args = []string{args[0] + "." + args[1], args[0] + "." + args[2]}
	}
	if e.op == "" {
		return args
	}
	return append([]string{e.op}, args...)
}

func (e edit) String() string {
	return strings.TrimSpace(e.op + " " + fmt.Sprintf("%q", e.args))
}

type editCase struct {
	name   string
	before string
	edits  []edit
	after  string
}

func runEditCases(t *testing.T, cases []editCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := mustParse(t, tc.before)
			for _, e := range tc.edits {
				require.NoError(t, e.apply(f), e)
			}
			assert.Equal(t, tc.after, string(f.Bytes()))

			// The snapshot after the edit is the configuration of the new bytes.
			reparsed := mustConfig(t, string(f.Bytes()))
			assert.Equal(t, slices.Collect(reparsed.All()), slices.Collect(f.Config().All()))
		})
	}
}

func edits(e ...edit) []edit { return e }

var setCases = []editCase{
	{"new key at the end of the last matching section",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tj = 2\n", edits(set("a.new", "v")),
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tj = 2\n\tnew = v\n"},
	{"new key before the next section",
		"[core]\n\tbare = false\n\tfilemode = true\n[user]\n\tname = n\n", edits(set("core.editor", "vim")),
		"[core]\n\tbare = false\n\tfilemode = true\n\teditor = vim\n[user]\n\tname = n\n"},
	{"new key before a blank line",
		"[a]\n\tk = v\n\n[b]\n\tx = y\n", edits(set("a.j", "w")),
		"[a]\n\tk = v\n\tj = w\n\n[b]\n\tx = y\n"},
	{"new key before a trailing comment",
		"[a]\n\tk = v\n# tail\n", edits(set("a.j", "w")),
		"[a]\n\tk = v\n\tj = w\n# tail\n"},
	{"new key before an indented comment",
		"[a]\n\tk = v\n\t# c\n[b]\n\tx = y\n", edits(set("a.j", "w")),
		"[a]\n\tk = v\n\tj = w\n\t# c\n[b]\n\tx = y\n"},
	{"new key in an empty section",
		"[a]\n[b]\n\tx = y\n", edits(set("a.j", "w")),
		"[a]\n\tj = w\n[b]\n\tx = y\n"},
	{"new key after a header line entry",
		"[a] k = v\n", edits(set("a.j", "w")),
		"[a] k = v\n\tj = w\n"},
	{"new key without final newline",
		"[a]\n\tk = v", edits(set("a.j", "w")),
		"[a]\n\tk = v\n\tj = w\n"},
	{"new key in a CRLF file",
		"[a]\r\n\tk = v\r\n", edits(set("a.j", "w")),
		"[a]\r\n\tk = v\r\n\tj = w\n"},
	{"existing indentation kept, new line with a tab",
		"[a]\n    k = v\n", edits(set("a.j", "w")),
		"[a]\n    k = v\n\tj = w\n"},
	{"section matched case-insensitively",
		"[A]\n\tk = v\n", edits(set("a.j", "w")),
		"[A]\n\tk = v\n\tj = w\n"},
	{"legacy subsection, by its canonical key",
		"[a.B]\n\tk = v\n", edits(set("a.b.j", "w")),
		"[a.B]\n\tk = v\n\tj = w\n"},
	{"subsection matched case-sensitively",
		"[remote \"Origin\"]\n\turl = u\n", edits(set("remote.origin.url", "v")),
		"[remote \"Origin\"]\n\turl = u\n[remote \"origin\"]\n\turl = v\n"},
	{"new section in an empty file keeps the caller's case",
		"", edits(set("Core.Editor", "vim")),
		"[Core]\n\tEditor = vim\n"},
	{"new subsection keeps the caller's case",
		"", edits(set("Remote.Origin.URL", "u")),
		"[Remote \"Origin\"]\n\tURL = u\n"},
	{"new section after the last one",
		"[a]\n\tk = v\n", edits(set("b.j", "w")),
		"[a]\n\tk = v\n[b]\n\tj = w\n"},
	{"new section after comments",
		"# c\n", edits(set("a.k", "v")),
		"# c\n[a]\n\tk = v\n"},
	{"new subsection with quote and backslash",
		"", edits(set(`remote.we"ird\x.url`, "u")),
		"[remote \"we\\\"ird\\\\x\"]\n\turl = u\n"},
	{"new empty subsection, then no subsection",
		"", edits(set("a..k", "v"), set("a.k", "w")),
		"[a \"\"]\n\tk = v\n[a]\n\tk = w\n"},
	{"replaced with the caller's key spelling, header kept",
		"[CORE]\n\tEditor = nano\n", edits(set("core.editor", "vim")),
		"[CORE]\n\teditor = vim\n"},
	{"replaced in a header line: the line is split",
		"[a] k = v\n", edits(set("a.k", "w")),
		"[a]\n\tk = w\n"},
	{"replaced continuation lines",
		"[a]\n\tk = one \\\n  two\n\tz = 1\n", edits(set("a.k", "new")),
		"[a]\n\tk = new\n\tz = 1\n"},
	{"replaced drops the trailing comment",
		"[a]\n\tk = old ; note\n", edits(set("a.k", "new")),
		"[a]\n\tk = new\n"},
	{"replaced in a CRLF file",
		"[a]\r\n\tk = v\r\n", edits(set("a.k", "w")),
		"[a]\r\n\tk = w\n"},
	{"implicit replaced",
		"[a]\n\tk\n", edits(set("a.k", "false")),
		"[a]\n\tk = false\n"},
	{"implicit is never already set",
		"[a]\n\tk\n", edits(set("a.k", "")),
		"[a]\n\tk = \n"},

	// E7: a value already set leaves the line as it is. git rewrites it.
	{"same value, own formatting kept",
		"[a]\n    k=v\n", edits(set("a.k", "v")),
		"[a]\n    k=v\n"},
	{"same value, quotes and comment kept",
		"[a]\n\tk = \"x\"  # c\n", edits(set("a.k", "x")),
		"[a]\n\tk = \"x\"  # c\n"},
	{"same empty value",
		"[a]\n\tk =\n", edits(set("a.k", "")),
		"[a]\n\tk =\n"},
	{"same value in a header line",
		"[a] k = v\n", edits(set("a.k", "v")),
		"[a] k = v\n"},
}

func TestFileSet(t *testing.T) {
	runEditCases(t, setCases)
}

var quotingCases = []struct {
	value   string
	written string
}{
	{"plain", "plain"},
	{"inner  space", "inner  space"},
	{"", ""},
	{"x # y", `"x # y"`},
	{"#x", `"#x"`},
	{"semi;colon", `"semi;colon"`},
	{"  pad", `"  pad"`},
	{"trail ", `"trail "`},
	{"\ttab", `\ttab`},
	{"tab\t", `tab\t`},
	{`say "hi"`, `say \"hi\"`},
	{`x"y`, `x\"y`},
	{`"`, `\"`},
	{`a\b`, `a\\b`},
	{`x\`, `x\\`},
	{"tab\tin", `tab\tin`},
	{"multi\nline", `multi\nline`},
	{"x\ry", "\"x\ry\""},
	{"é ✓", "é ✓"},
}

func TestFileSetQuoting(t *testing.T) {
	for _, tc := range quotingCases {
		t.Run(strconv.Quote(tc.value), func(t *testing.T) {
			f := mustParse(t, "[a]\n")
			require.NoError(t, f.Set("a.k", tc.value))
			assert.Equal(t, "[a]\n\tk = "+tc.written+"\n", string(f.Bytes()))

			e, ok := mustConfig(t, string(f.Bytes())).Get("a.k")
			require.True(t, ok)
			assert.Equal(t, tc.value, e.Value)
		})
	}
}

var replaceAllCases = []editCase{
	{"values collapse at the last one",
		"[a]\n\tk = 1\n\tj = j\n\tk = 2\n", edits(replaceAll("a.k", "X")),
		"[a]\n\tj = j\n\tk = X\n"},
	{"across sections, emptied section kept",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tk = 2\n", edits(replaceAll("a.k", "X")),
		"[a]\n[b]\n\tx = y\n[a]\n\tk = X\n"},
	{"single value",
		"[a]\n\tk = 1\n", edits(replaceAll("a.k", "X")),
		"[a]\n\tk = X\n"},
	{"absent key added",
		"[a]\n\tj = 1\n", edits(replaceAll("a.k", "X")),
		"[a]\n\tj = 1\n\tk = X\n"},
}

func TestFileReplaceAll(t *testing.T) {
	runEditCases(t, replaceAllCases)
}

var addCases = []editCase{
	{"after the existing value",
		"[a]\n\tk = 1\n", edits(add("a.k", "2")),
		"[a]\n\tk = 1\n\tk = 2\n"},
	{"same value added again",
		"[a]\n\tk = 1\n", edits(add("a.k", "1")),
		"[a]\n\tk = 1\n\tk = 1\n"},
	{"at the end of the last matching section",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tj = 2\n", edits(add("a.k", "2")),
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tj = 2\n\tk = 2\n"},
	{"new section",
		"", edits(add("a.k", "1")),
		"[a]\n\tk = 1\n"},
}

func TestFileAdd(t *testing.T) {
	runEditCases(t, addCases)
}

var unsetCases = []editCase{
	{"one of two keys",
		"[a]\n\tk = v\n\tj = w\n", edits(unset("a.k")),
		"[a]\n\tj = w\n"},
	{"continuation lines",
		"[a]\n\tk = one \\\n two\n\tj = w\n", edits(unset("a.k")),
		"[a]\n\tj = w\n"},
	{"implicit",
		"[a]\n\tk\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[b]\n\tx = y\n"},
	{"key on the header line",
		"[a] k = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[b]\n\tx = y\n"},
	{"only key of the file",
		"[a]\n\tk = v\n", edits(unset("a.k")),
		""},
	{"case-insensitive key",
		"[A]\n\tK = v\n\tj = w\n", edits(unset("a.k")),
		"[A]\n\tj = w\n"},
	{"entry before any header kept",
		"k = v\n[a]\n\tj = w\n", edits(unset("a.j")),
		"k = v\n"},

	// E5: the emptied section is removed unless a comment lies between
	// the previous entry or header and the next section.
	{"empty section removed",
		"[a]\n\tk = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[b]\n\tx = y\n"},
	{"blank lines do not keep the section",
		"[a]\n\n\tk = v\n\t\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[b]\n\tx = y\n"},
	{"comment inside keeps the section",
		"[a]\n# keep\n\tk = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[a]\n# keep\n[b]\n\tx = y\n"},
	{"comment after keeps the section",
		"[a]\n\tk = v\n# after\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[a]\n# after\n[b]\n\tx = y\n"},
	{"comment before the header keeps the section",
		"# before\n[a]\n\tk = v ; trailing\n[b]\n\tx = y\n", edits(unset("a.k")),
		"# before\n[a]\n[b]\n\tx = y\n"},
	{"comment then blank line before the header",
		"# before\n\n[a]\n\tk = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"# before\n\n[a]\n[b]\n\tx = y\n"},
	{"comment after the previous section's last entry",
		"[z]\n\tq = 1\n# before\n[a]\n\tk = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"[z]\n\tq = 1\n# before\n[a]\n[b]\n\tx = y\n"},
	{"comment above an earlier header does not count",
		"# top\n[z]\n\tq = 1\n[a]\n\tk = v\n[b]\n\tx = y\n", edits(unset("a.k")),
		"# top\n[z]\n\tq = 1\n[b]\n\tx = y\n"},
}

func TestFileUnset(t *testing.T) {
	runEditCases(t, unsetCases)

	t.Run("absent", func(t *testing.T) {
		const data = "[a]\n\tk = v\n"
		f := mustParse(t, data)
		removed, err := f.Unset("a.missing")
		require.NoError(t, err)
		assert.False(t, removed)
		assert.Equal(t, data, string(f.Bytes()))
	})
}

var unsetAllCases = []editCase{
	{"several values",
		"[a]\n\tk = 1\n\tj = 0\n\tk = 2\n", edits(unsetAll("a.k")),
		"[a]\n\tj = 0\n"},
	{"across sections",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tk = 2\n", edits(unsetAll("a.k")),
		"[b]\n\tx = y\n"},
	{"single value",
		"[a]\n\tk = 1\n\tj = 0\n", edits(unsetAll("a.k")),
		"[a]\n\tj = 0\n"},
}

func TestFileUnsetAll(t *testing.T) {
	runEditCases(t, unsetAllCases)

	t.Run("count", func(t *testing.T) {
		f := mustParse(t, "[a]\n\tk = 1\n[b]\n\tk = 2\n[a]\n\tk = 3\n\tk\n")
		n, err := f.UnsetAll("a.k")
		require.NoError(t, err)
		assert.Equal(t, 3, n)
	})

	t.Run("absent", func(t *testing.T) {
		const data = "[a]\n\tk = 1\n"
		f := mustParse(t, data)
		n, err := f.UnsetAll("a.j")
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Equal(t, data, string(f.Bytes()))
	})
}

var removeSectionCases = []editCase{
	{"header, comments inside and keys",
		"# top\n[a]\n# c\n\tk = v\n[b]\n\tx = y\n", edits(removeSection("a")),
		"# top\n[b]\n\tx = y\n"},
	{"every occurrence",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tk = 2\n", edits(removeSection("a")),
		"[b]\n\tx = y\n"},
	{"subsections kept",
		"[a]\n\tk = w\n[a \"x\"]\n\tk = v\n", edits(removeSection("a")),
		"[a \"x\"]\n\tk = v\n"},
	{"subsection",
		"[a \"x\"]\n\tk = v\n[a]\n\tk = w\n", edits(removeSubsection("a", "x")),
		"[a]\n\tk = w\n"},
	{"empty subsection",
		"[a \"\"]\n\tk = v\n[a]\n\tk = w\n", edits(removeSubsection("a", "")),
		"[a]\n\tk = w\n"},
}

func TestFileRemoveSection(t *testing.T) {
	runEditCases(t, removeSectionCases)

	t.Run("absent", func(t *testing.T) {
		const data = "[a]\n\tk = v\n[a \"x\"]\n\tk = v\n"
		f := mustParse(t, data)

		removed, err := f.RemoveSection("b")
		require.NoError(t, err)
		assert.False(t, removed)

		removed, err = f.RemoveSubsection("a", "X")
		require.NoError(t, err)
		assert.False(t, removed, "subsections are case-sensitive")

		removed, err = f.RemoveSubsection("a", "")
		require.NoError(t, err)
		assert.False(t, removed)

		assert.Equal(t, data, string(f.Bytes()))
	})
}

var renameSectionCases = []editCase{
	{"only the header changes",
		"[a]\n# c\n\tk = v\n[b]\n\tx = y\n", edits(renameSection("a", "z")),
		"[z]\n# c\n\tk = v\n[b]\n\tx = y\n"},
	{"every occurrence",
		"[a]\n\tk = 1\n[b]\n\tx = y\n[a]\n\tk = 2\n", edits(renameSection("a", "z")),
		"[z]\n\tk = 1\n[b]\n\tx = y\n[z]\n\tk = 2\n"},
	{"subsections not renamed",
		"[a]\n\tk = 1\n[a \"x\"]\n\tk = 2\n", edits(renameSection("a", "z")),
		"[z]\n\tk = 1\n[a \"x\"]\n\tk = 2\n"},
	{"entry on the header line",
		"[a] k = v\n", edits(renameSection("a", "z")),
		"[z]\n\tk = v\n"},
	{"existing target not checked",
		"[a]\n\tk = 1\n[b]\n\tk = 2\n", edits(renameSection("a", "b")),
		"[b]\n\tk = 1\n[b]\n\tk = 2\n"},
	{"two headers on a line",
		"[a] [b]\n\tk = v\n", edits(renameSection("a", "z")),
		"[z]\n\t[b]\n\tk = v\n"},
	{"subsection",
		"[remote \"old\"]\n\turl = u\n[remote \"other\"]\n\turl = o\n", edits(renameSubsection("remote", "old", "new")),
		"[remote \"new\"]\n\turl = u\n[remote \"other\"]\n\turl = o\n"},
	{"subsection escaped",
		"[remote \"old\"]\n\turl = u\n", edits(renameSubsection("remote", "old", `n"e\w`)),
		"[remote \"n\\\"e\\\\w\"]\n\turl = u\n"},
	{"empty subsection",
		"[a \"\"]\n\tk = v\n[a]\n\tk = w\n", edits(renameSubsection("a", "", "x")),
		"[a \"x\"]\n\tk = v\n[a]\n\tk = w\n"},
}

func TestFileRenameSection(t *testing.T) {
	runEditCases(t, renameSectionCases)

	t.Run("absent", func(t *testing.T) {
		const data = "[a]\n\tk = v\n[b \"x\"]\n\tk = v\n"
		f := mustParse(t, data)
		require.ErrorIs(t, f.RenameSection("missing", "z"), gitconfig.ErrNotFound)
		require.ErrorIs(t, f.RenameSubsection("b", "y", "z"), gitconfig.ErrNotFound)
		require.ErrorIs(t, f.RenameSubsection("a", "", "z"), gitconfig.ErrNotFound)
		assert.Equal(t, data, string(f.Bytes()))
	})
}

// gitDifferenceCases are edits where this package deliberately differs from
// git.
//
// E10: keys and sections are matched by their canonical form, so a legacy
// header [a.B] holds subsection "b", and section names are
// case-insensitive. git matches legacy headers case-insensitively when it
// places a new key (so that Set("a.B.j") writes a key that reads back as
// a.b.j, and Set("a.B.k") can make a.b.k multi-valued), and matches the
// header text as written when it removes or renames a section.
//
// X1: sections are found by parsing, where git scans lines. git takes a
// line starting with '[' inside a continued value for a header, and does
// not see a header right after a BOM; it writes a new section after a file
// holding only a BOM, which then does not parse.
var gitDifferenceCases = []editCase{
	{"remove section: header-like line in a continued value",
		"[a]\n\tk = x\\\n[b]\n\tz = 1\n[b]\n\ty = 2\n", edits(removeSection("b")),
		"[a]\n\tk = x\\\n[b]\n\tz = 1\n"},
	{"rename section: header-like line in a continued value",
		"[a]\n\tk = x\\\n[b]\n[b]\n\ty = 2\n", edits(renameSection("b", "c")),
		"[a]\n\tk = x\\\n[b]\n[c]\n\ty = 2\n"},
	{"remove section after a BOM",
		"\xef\xbb\xbf[a]\n\tk = v\n[b]\n\tx = y\n", edits(removeSection("a")),
		"\xef\xbb\xbf[b]\n\tx = y\n"},
	{"rename section after a BOM",
		"\xef\xbb\xbf  [a] k = v\n", edits(renameSection("a", "z")),
		"\xef\xbb\xbf[z]\n\tk = v\n"},
	{"remove the second header of a line",
		"[a] [b]\n\tk = v\n[c]\n\tx = y\n", edits(removeSection("b")),
		"[a][c]\n\tx = y\n"}, // the space is the indentation of [b]
	{"set in a file holding only a BOM",
		"\xef\xbb\xbf", edits(set("a.k", "v")),
		"\xef\xbb\xbf[a]\n\tk = v\n"},

	{"legacy subsection, other case: new subsection",
		"[a.B]\n\tk = v\n", edits(set("a.B.j", "w")),
		"[a.B]\n\tk = v\n[a \"B\"]\n\tj = w\n"},
	{"legacy subsection, other case: existing value not replaced",
		"[a.B]\n\tk = v\n", edits(set("a.B.k", "w")),
		"[a.B]\n\tk = v\n[a \"B\"]\n\tk = w\n"},
	{"remove section, other case",
		"[A]\n\tk = v\n[b]\n\tx = y\n", edits(removeSection("a")),
		"[b]\n\tx = y\n"},
	{"rename section, other case",
		"[A]\n\tk = v\n", edits(renameSection("a", "z")),
		"[z]\n\tk = v\n"},
	{"remove legacy subsection by its canonical name",
		"[a.X]\n\tk = v\n[a \"X\"]\n\tk = w\n", edits(removeSubsection("a", "x")),
		"[a \"X\"]\n\tk = w\n"},
	{"rename legacy subsection by its canonical name",
		"[a.X]\n\tk = v\n", edits(renameSubsection("a", "x", "y")),
		"[a \"y\"]\n\tk = v\n"},
}

func TestFileGitDifferences(t *testing.T) {
	runEditCases(t, gitDifferenceCases)

	t.Run("header-like line in a continued value is not a section", func(t *testing.T) {
		const data = "[a]\n\tk = x\\\n[b]\n\tz = 1\n"
		f := mustParse(t, data)
		removed, err := f.RemoveSection("b")
		require.NoError(t, err)
		assert.False(t, removed)
		require.ErrorIs(t, f.RenameSection("b", "c"), gitconfig.ErrNotFound)
		assert.Equal(t, data, string(f.Bytes()))
	})

	t.Run("legacy subsection is not its header's spelling", func(t *testing.T) {
		const data = "[a.X]\n\tk = v\n"
		f := mustParse(t, data)
		removed, err := f.RemoveSubsection("a", "X")
		require.NoError(t, err)
		assert.False(t, removed)
		require.ErrorIs(t, f.RenameSubsection("a", "X", "y"), gitconfig.ErrNotFound)
		assert.Equal(t, data, string(f.Bytes()))
	})
}

// allEditCases are the edits that git must perform identically, except
// where E7 keeps a line git rewrites.
func allEditCases() []editCase {
	return slices.Concat(setCases, replaceAllCases, addCases, unsetCases, unsetAllCases, removeSectionCases, renameSectionCases)
}

const editErrorData = "[a]\n\tk = 1\n\tk = 2\n[b]\n\tx = y\n[c \"s\"]\n\tz = 1\n[d]\n\tk = 1\n[b]\n[d]\n\tk = 2\n"

var editErrorCases = []struct {
	name string
	edit edit
	err  error
	key  string // KeyError.Key, if any
}{
	{"Set on multiple values", set("a.k", "3"), gitconfig.ErrMultipleValues, "a.k"},
	{"Set on multiple values across sections", set("d.k", "3"), gitconfig.ErrMultipleValues, "d.k"},
	{"Unset on multiple values", unset("a.k"), gitconfig.ErrMultipleValues, "a.k"},

	{"Set invalid key", set("a.k_y", "v"), gitconfig.ErrInvalidKey, "a.k_y"},
	{"Set without section", set("k", "v"), gitconfig.ErrInvalidKey, "k"},
	// git accepts it and writes the header [ "sub"].
	{"Set empty section", set(".sub.k", "v"), gitconfig.ErrInvalidKey, ".sub.k"},
	{"Add empty section", add(".sub.k", "v"), gitconfig.ErrInvalidKey, ".sub.k"},
	{"Set newline in subsection", set("a.x\ny.k", "v"), gitconfig.ErrInvalidKey, "a.x\ny.k"},
	{"Add invalid key", add("a.1k", "v"), gitconfig.ErrInvalidKey, "a.1k"},
	{"ReplaceAll invalid key", replaceAll("a.-k", "v"), gitconfig.ErrInvalidKey, "a.-k"},
	{"Unset invalid key", unset("a b.k"), gitconfig.ErrInvalidKey, "a b.k"},
	{"UnsetAll invalid key", unsetAll("a"), gitconfig.ErrInvalidKey, "a"},
	{"RemoveSection invalid name", removeSection("a b"), gitconfig.ErrInvalidKey, ""},
	{"RenameSection invalid new name", renameSection("b", "a_b"), gitconfig.ErrInvalidKey, ""},
	// git accepts it and writes a header it then cannot parse (X1).
	{"RenameSubsection newline", renameSubsection("c", "s", "x\ny"), gitconfig.ErrInvalidKey, ""},

	// Cannot be passed to git: arguments are C strings.
	{"Set NUL in subsection", set("a.x\x00y.k", "v"), gitconfig.ErrInvalidKey, "a.x\x00y.k"},
	{"Set NUL in value", set("b.x", "x\x00y"), gitconfig.ErrInvalidValue, "b.x"},
	{"Add NUL in value", add("b.x", "\x00"), gitconfig.ErrInvalidValue, "b.x"},
	{"ReplaceAll NUL in value", replaceAll("a.k", "\x00"), gitconfig.ErrInvalidValue, "a.k"},
}

func TestFileEditErrors(t *testing.T) {
	for _, tc := range editErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			f := mustParse(t, editErrorData)

			err := tc.edit.apply(f)
			require.ErrorIs(t, err, tc.err)
			var ke *gitconfig.KeyError
			require.ErrorAs(t, err, &ke)
			if tc.key != "" {
				assert.Equal(t, tc.key, ke.Key)
			}
			assert.Equal(t, editErrorData, string(f.Bytes()))
		})
	}
}

// FuzzParse checks that Parse never panics, that it keeps the input byte for
// byte (E1), and that the entries it reads have valid keys.
func FuzzParse(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := gitconfig.Parse("f", data)
		if err != nil {
			var se *gitconfig.SyntaxError
			require.ErrorAs(t, err, &se)
			require.Equal(t, "f", se.File)
			require.GreaterOrEqual(t, se.Line, 1)
			return
		}
		require.Equal(t, data, file.Bytes())

		for e := range file.Config().All() {
			k, err := gitconfig.ParseKey(e.Key.String())
			require.NoError(t, err)
			require.Equal(t, e.Key, k)
			require.Equal(t, gitconfig.ScopeFile, e.Origin.Scope)
			require.GreaterOrEqual(t, e.Origin.Line, 1)
			if e.Implicit {
				require.Empty(t, e.Value)
			}
		}
	})
}

// FuzzEdit checks that an edit cannot inject anything (X1): the edited file
// parses to the entries before the edit with exactly that one change, and a
// failed edit changes nothing.
func FuzzEdit(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add([]byte(s), uint8(0), "a.k", "v")
	}
	f.Add([]byte("[a]\n\tk = 1\n\tk = 2\n"), uint8(1), "a.k", "x\n[b]\n\tj = w")
	f.Add([]byte("[a]\n"), uint8(0), "a.x\"]\n[b \"y.k", "v")
	f.Add([]byte("[a \"x\"]\n\tk = v\n"), uint8(2), "a.x.k", "\" ; # \\")
	f.Add([]byte("[a]\n\tk = v\n[b]\n"), uint8(3), "A.K", "")
	f.Add([]byte("[a] k = v\n"), uint8(4), "a.k", "")

	f.Fuzz(func(t *testing.T, data []byte, op uint8, key, value string) {
		file, err := gitconfig.Parse("f", data)
		if err != nil {
			return
		}
		before := slices.Collect(file.Config().All())
		original := slices.Clone(file.Bytes())
		k, keyErr := gitconfig.ParseKey(key)
		if keyErr == nil && k.Section == "" {
			keyErr = errors.New("empty section: read, never written")
		}

		var mine []gitconfig.Entry
		for _, e := range before {
			if e.Key == k {
				mine = append(mine, e)
			}
		}

		var want []string // values of key after the edit
		switch op % 5 {
		case 0:
			err = file.Set(key, value)
			want = []string{value}
		case 1:
			err = file.Add(key, value)
			want = append(values(mine), value)
		case 2:
			err = file.ReplaceAll(key, value)
			want = []string{value}
		case 3:
			var n int
			n, err = file.UnsetAll(key)
			if err == nil {
				require.Equal(t, len(mine), n)
			}
		case 4:
			var removed bool
			removed, err = file.Unset(key)
			if err == nil {
				require.Equal(t, len(mine) == 1, removed)
			}
		}

		if err != nil {
			require.Equal(t, original, file.Bytes(), "a failed edit changed the file")
			switch {
			case keyErr != nil:
				require.ErrorIs(t, err, gitconfig.ErrInvalidKey)
			case errors.Is(err, gitconfig.ErrInvalidValue):
				require.True(t, strings.ContainsRune(value, 0) && op%5 <= 2)
			default:
				require.ErrorIs(t, err, gitconfig.ErrMultipleValues)
				require.True(t, op%5 == 0 || op%5 == 4)
				require.Greater(t, len(mine), 1)
			}
			return
		}
		require.NoError(t, keyErr, "an edit accepted an invalid key")

		reparsed, err := gitconfig.Parse("f", file.Bytes())
		require.NoError(t, err, "the edit produced a file that does not parse:\n%q", file.Bytes())
		after := slices.Collect(reparsed.Config().All())
		require.Equal(t, after, slices.Collect(file.Config().All()), "snapshot differs from the written bytes")

		strip := func(entries []gitconfig.Entry, keep func(gitconfig.Key) bool) []gitconfig.Entry {
			var out []gitconfig.Entry
			for _, e := range entries {
				if keep(e.Key) {
					e.Origin = gitconfig.Origin{}
					out = append(out, e)
				}
			}
			return out
		}
		others := func(key gitconfig.Key) bool { return key != k }
		require.Equal(t, strip(before, others), strip(after, others), "other entries changed")

		var got []gitconfig.Entry
		for _, e := range after {
			if e.Key == k {
				got = append(got, e)
			}
		}
		require.Equal(t, want, values(got))
		if op%5 == 0 || op%5 == 2 {
			require.False(t, got[0].Implicit)
		}
	})
}

// FuzzSectionEdit checks that removing or renaming a section cannot touch
// anything else (X1): the edited file parses to the entries before the
// edit, without the section's entries or with them renamed.
func FuzzSectionEdit(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add([]byte(s), false, "a", "", false, "z")
		f.Add([]byte(s), true, "a", "x", true, "y")
	}
	f.Add([]byte("[a]\n\tk = x\\\n[b]\n\tz = 1\n[b]\n\ty = 2\n"), false, "b", "", false, "c")
	f.Add([]byte("[a]\n\tk = x\\\n[b]\n[b] y = 2\n"), true, "b", "", false, "c")

	f.Fuzz(func(t *testing.T, data []byte, rename bool, section, sub string, hasSub bool, newName string) {
		file, err := gitconfig.Parse("f", data)
		if err != nil {
			return
		}
		before := slices.Collect(file.Config().All())
		original := slices.Clone(file.Bytes())

		inSection := func(k gitconfig.Key) bool {
			return k.Section == strings.ToLower(section) && k.HasSubsection == hasSub && (!hasSub || k.Subsection == sub)
		}
		matched := slices.ContainsFunc(before, func(e gitconfig.Entry) bool { return inSection(e.Key) })

		var want []gitconfig.Entry
		for _, e := range before {
			e.Origin = gitconfig.Origin{}
			if inSection(e.Key) {
				if !rename {
					continue
				}
				if hasSub {
					e.Key.Subsection = newName
				} else {
					e.Key.Section = strings.ToLower(newName)
				}
			}
			want = append(want, e)
		}

		switch {
		case rename && hasSub:
			err = file.RenameSubsection(section, sub, newName)
		case rename:
			err = file.RenameSection(section, newName)
		default:
			var removed bool
			if hasSub {
				removed, err = file.RemoveSubsection(section, sub)
			} else {
				removed, err = file.RemoveSection(section)
			}
			if err == nil && matched {
				require.True(t, removed, "entries of the section but nothing removed")
			}
		}

		if err != nil {
			require.Equal(t, original, file.Bytes(), "a failed edit changed the file")
			if !errors.Is(err, gitconfig.ErrInvalidKey) {
				require.ErrorIs(t, err, gitconfig.ErrNotFound)
				require.False(t, matched, "entries of the section but not found")
			}
			return
		}

		reparsed, err := gitconfig.Parse("f", file.Bytes())
		require.NoError(t, err, "the edit produced a file that does not parse:\n%q", file.Bytes())
		after := slices.Collect(reparsed.Config().All())
		require.Equal(t, after, slices.Collect(file.Config().All()), "snapshot differs from the written bytes")
		for i := range after {
			after[i].Origin = gitconfig.Origin{}
		}
		require.Equal(t, want, after)
	})
}

var parseSeeds = []string{
	"",
	"[a]\n\tk = v\n",
	"[a] k = v\n",
	"[a]\n\tk\n",
	"[a \"\"]\n\tk = v\n[a]\n\tk = w\n",
	"[a.B]\n\tk = v\n",
	"[a \"s\\\"q\"]\n\tk = v\n",
	"[a]\n\tk = a \"b c\" d ; c\n",
	"[a]\n\tk = one \\\n two\n",
	"\xef\xbb\xbf[a]\r\n\tk = v\r\n",
	"# c\n[a]\n# keep\n\tk = 1\n\tk = 2\n[b]\n\tx = y\n",
	"[a]\n\tk = x\\\\ny\\t\\\"\n",
	"[a\n",
	"[a]\n\tk = \"x\n",
	"k = v\n[a]\n\tj = w\n",
	"[a]\n\tk = x\\\n[b]\n",
	"\xef\xbb\xbf",
}

// sized repeats unit until the result is at least n bytes long.
func sized(header, unit string, n int) []byte {
	var buf bytes.Buffer
	buf.WriteString(header)
	for buf.Len() < n {
		buf.WriteString(unit)
	}
	return buf.Bytes()
}

// BenchmarkParse measures Parse on realistic files and on inputs built to
// hit the parser's slow paths. Throughput (MB/s) must not fall as the size
// grows: parsing is linear (X2).
func BenchmarkParse(b *testing.B) {
	inputs := []struct {
		name string
		gen  func(n int) []byte
	}{
		{"realistic", func(n int) []byte { return sized("", bigConfig(1), n) }},
		{"one long value", func(n int) []byte { return append(sized("[a]\n\tk = ", "x", n), '\n') }},
		{"continuations", func(n int) []byte { return append(sized("[a]\n\tk = ", "x \\\n", n), '\n') }},
		{"quotes", func(n int) []byte { return append(sized("[a]\n\tk = ", `"x" `, n), '\n') }},
		{"escapes", func(n int) []byte { return append(sized("[a]\n\tk = ", `\"\\\t`, n), '\n') }},
		{"same key", func(n int) []byte { return sized("[a]\n", "\tk = v\n", n) }},
		{"headers", func(n int) []byte { return sized("", "[a \"x\"]\n", n) }},
		{"long subsection", func(n int) []byte { return append(sized("[a \"", `\"`, n), "\"]\n"...) }},
		{"comments", func(n int) []byte { return sized("", "# ;;;; [a] \\\n", n) }},
	}
	for _, in := range inputs {
		for _, n := range []int{1 << 10, 1 << 16, 1 << 20} {
			data := in.gen(n)
			b.Run(fmt.Sprintf("%s/%dKiB", in.name, n>>10), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					_, _ = gitconfig.Parse("f", data)
				}
			})
		}
	}
}

func BenchmarkFile(b *testing.B) {
	for _, n := range []int{10, 1000} {
		data := []byte(bigConfig(n))

		b.Run(fmt.Sprintf("Set/n=%d", n), func(b *testing.B) {
			f, err := gitconfig.Parse("f", data)
			require.NoError(b, err)
			i := 0
			for b.Loop() {
				i++
				_ = f.Set("remote.r1.url", strconv.Itoa(i))
			}
		})
		b.Run(fmt.Sprintf("SetUnchanged/n=%d", n), func(b *testing.B) {
			f, err := gitconfig.Parse("f", data)
			require.NoError(b, err)
			for b.Loop() {
				_ = f.Set("user.name", "Someone")
			}
		})
		b.Run(fmt.Sprintf("SetNewSection/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				f, _ := gitconfig.Parse("f", data)
				_ = f.Set("new.k", "v")
			}
		})
		b.Run(fmt.Sprintf("ConfigAfterEdit/n=%d", n), func(b *testing.B) {
			f, err := gitconfig.Parse("f", data)
			require.NoError(b, err)
			i := 0
			for b.Loop() {
				i++
				_ = f.Set("user.name", strconv.Itoa(i))
				f.Config()
			}
		})
		b.Run(fmt.Sprintf("Bytes/n=%d", n), func(b *testing.B) {
			f, err := gitconfig.Parse("f", data)
			require.NoError(b, err)
			for b.Loop() {
				f.Bytes()
			}
		})
	}
}
