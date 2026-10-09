package gitconfig_test

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/git-bug/gitconfig"
)

// mustParse parses data as a file named "f".
func mustParse(t testing.TB, data string) *gitconfig.File {
	t.Helper()
	f, err := gitconfig.Parse("f", []byte(data))
	require.NoError(t, err)
	return f
}

// mustConfig parses data as a file named "f" and returns its configuration.
func mustConfig(t testing.TB, data string) *gitconfig.Config {
	t.Helper()
	return mustParse(t, data).Config()
}

// list renders entries as `git config --list` does: "key=value", or "key"
// for an implicit value.
func list(entries []gitconfig.Entry) []string {
	var out []string
	for _, e := range entries {
		if e.Implicit {
			out = append(out, e.Key.String())
		} else {
			out = append(out, e.Key.String()+"="+e.Value)
		}
	}
	return out
}

func values(entries []gitconfig.Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Value)
	}
	return out
}

func TestParseKey(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		for _, tc := range []struct {
			in   string
			want gitconfig.Key
			str  string
		}{
			{"core.editor", gitconfig.Key{Section: "core", Name: "editor"}, "core.editor"},
			{"Core.EDITOR", gitconfig.Key{Section: "core", Name: "editor"}, "core.editor"},
			{"remote.Origin.URL", gitconfig.Key{Section: "remote", Subsection: "Origin", HasSubsection: true, Name: "url"}, "remote.Origin.url"},
			{"a..k", gitconfig.Key{Section: "a", HasSubsection: true, Name: "k"}, "a..k"},
			{"url.https://a.b/.insteadOf", gitconfig.Key{Section: "url", Subsection: "https://a.b/", HasSubsection: true, Name: "insteadof"}, "url.https://a.b/.insteadof"},
			{"a.b.c.k", gitconfig.Key{Section: "a", Subsection: "b.c", HasSubsection: true, Name: "k"}, "a.b.c.k"},
			{`a.s"q\x].k`, gitconfig.Key{Section: "a", Subsection: `s"q\x]`, HasSubsection: true, Name: "k"}, `a.s"q\x].k`},
			{"a.x y.k", gitconfig.Key{Section: "a", Subsection: "x y", HasSubsection: true, Name: "k"}, "a.x y.k"},
			{"my-sec.my-key1", gitconfig.Key{Section: "my-sec", Name: "my-key1"}, "my-sec.my-key1"},
			{"1a.k", gitconfig.Key{Section: "1a", Name: "k"}, "1a.k"},
			{"a.k-", gitconfig.Key{Section: "a", Name: "k-"}, "a.k-"},
		} {
			t.Run(tc.in, func(t *testing.T) {
				k, err := gitconfig.ParseKey(tc.in)
				require.NoError(t, err)
				assert.Equal(t, tc.want, k)
				assert.Equal(t, tc.str, k.String())
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		for _, in := range []string{
			"",
			"a",
			".k",
			"a.",
			"a..",
			"a.1k",
			"a.-k",
			"a.k_y",
			"a.k y",
			"a b.k",
			"a_b.k",
			"a.x\ny.k",
			"a.x\x00y.k",
			"é.k",
		} {
			t.Run(strconv.Quote(in), func(t *testing.T) {
				_, err := gitconfig.ParseKey(in)
				require.ErrorIs(t, err, gitconfig.ErrInvalidKey)
				var ke *gitconfig.KeyError
				require.ErrorAs(t, err, &ke)
				assert.Equal(t, in, ke.Key)
			})
		}
	})
}

func FuzzParseKey(f *testing.F) {
	for _, s := range []string{
		"core.editor", "Remote.Origin.URL", "a..k", "a.b.c.k", `a.s"q\x.k`,
		"url.https://a.b/.insteadOf", "a", "a.1k", "a.x\ny.k", "a.x\x00y.k",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		k, err := gitconfig.ParseKey(s)
		if err != nil {
			require.ErrorIs(t, err, gitconfig.ErrInvalidKey)
			return
		}

		// The canonical form is a fixed point.
		k2, err := gitconfig.ParseKey(k.String())
		require.NoError(t, err)
		require.Equal(t, k, k2)

		// Any valid key can be written and read back.
		var file gitconfig.File
		require.NoError(t, file.Set(s, "v"))
		reparsed, err := gitconfig.Parse("f", file.Bytes())
		require.NoError(t, err)
		e, ok := reparsed.Config().Get(s)
		require.True(t, ok)
		require.Equal(t, k, e.Key)
		require.Equal(t, "v", e.Value)
	})
}

// getCases are lookups of a single key, also checked against git.
var getCases = []struct {
	name  string
	data  string
	key   string
	want  string
	found bool
}{
	{"last value wins", "[a]\n\tk = first\n\tk = last\n", "a.k", "last", true},
	{"last value across sections", "[a]\n\tk = first\n[b]\n\tx = y\n[a]\n\tk = last\n", "a.k", "last", true},
	{"missing key", "[a]\n\tk = v\n", "a.missing", "", false},
	{"missing section", "[a]\n\tk = v\n", "b.k", "", false},
	{"section and name are case-insensitive", "[CORE]\n\tEditor = vim\n", "core.EDITOR", "vim", true},
	{"subsection is case-sensitive", "[remote \"Origin\"]\n\turl = u\n", "remote.origin.url", "", false},
	{"subsection exact case", "[remote \"Origin\"]\n\turl = u\n", "remote.Origin.url", "u", true},
	{"legacy subsection, lower", "[a.B]\n\tk = v\n", "a.b.k", "v", true},
	{"legacy subsection is lowered", "[a.B]\n\tk = v\n", "a.B.k", "", false},
	{"legacy and quoted subsection are one", "[a.b]\n\tk = 1\n[a \"b\"]\n\tk = 2\n", "a.b.k", "2", true},
	{"subsection with dots", "[a \"b.c\"]\n\tk = v\n", "a.b.c.k", "v", true},
	{"subsection everything between first and last dot", "[url \"https://a.b/\"]\n\tinsteadOf = gh:\n", "url.https://a.b/.insteadof", "gh:", true},
	{"empty subsection", "[a \"\"]\n\tk = v\n[a]\n\tk = w\n", "a..k", "v", true},
	{"no subsection is not the empty one", "[a \"\"]\n\tk = v\n[a]\n\tk = w\n", "a.k", "w", true},
	{"invalid key is absent", "[a]\n\tk = v\n", "a.k_y", "", false},
	{"key without section is absent", "[a]\n\tk = v\n", "a", "", false},
	{"implicit value", "[a]\n\tk\n", "a.k", "", true},
}

func TestConfigGet(t *testing.T) {
	for _, tc := range getCases {
		t.Run(tc.name, func(t *testing.T) {
			c := mustConfig(t, tc.data)
			e, ok := c.Get(tc.key)
			require.Equal(t, tc.found, ok)
			assert.Equal(t, tc.want, e.Value)
			if !ok {
				assert.Equal(t, gitconfig.Entry{}, e)
			}
		})
	}

	t.Run("canonical key", func(t *testing.T) {
		c := mustConfig(t, "[A.Sub]\n\tKeY = v\n[Remote \"Origin\"]\n\tURL = u\n")
		e, ok := c.Get("a.sub.key")
		require.True(t, ok)
		assert.Equal(t, "a.sub.key", e.Key.String())
		e, ok = c.Get("remote.Origin.url")
		require.True(t, ok)
		assert.Equal(t, gitconfig.Key{Section: "remote", Subsection: "Origin", HasSubsection: true, Name: "url"}, e.Key)
	})

	t.Run("origin", func(t *testing.T) {
		c := mustConfig(t, "# c\n[a] k = 1\n\n[a]\n\tk = 2\n")
		got := c.GetAll("a.k")
		require.Len(t, got, 2)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeFile, File: "f", Line: 2}, got[0].Origin)
		assert.Equal(t, gitconfig.Origin{Scope: gitconfig.ScopeFile, File: "f", Line: 5}, got[1].Origin)
	})

	t.Run("implicit", func(t *testing.T) {
		c := mustConfig(t, "[a]\n\tk\n\tj =\n")
		e, _ := c.Get("a.k")
		assert.True(t, e.Implicit)
		e, _ = c.Get("a.j")
		assert.False(t, e.Implicit)
		assert.Equal(t, "", e.Value)
	})
}

func TestConfigGetAll(t *testing.T) {
	c := mustConfig(t, "[a]\n\tk = 1\n[b]\n\tk = x\n[A]\n\tK = 2\n[a \"s\"]\n\tk = no\n[a]\n\tk = 3\n")

	t.Run("in order across sections", func(t *testing.T) {
		assert.Equal(t, []string{"1", "2", "3"}, values(c.GetAll("a.k")))
	})
	t.Run("absent", func(t *testing.T) {
		assert.Empty(t, c.GetAll("a.missing"))
		assert.Empty(t, c.GetAll("a.k_y"))
	})
	t.Run("caller owns the slice", func(t *testing.T) {
		got := c.GetAll("a.k")
		got[0].Value = "changed"
		assert.Equal(t, []string{"1", "2", "3"}, values(c.GetAll("a.k")))
	})
}

func TestConfigIterators(t *testing.T) {
	c := mustConfig(t, `[core]
	bare = false
[remote "origin"]
	url = o
[Remote "upstream"]
	url = u
[remote "Origin"]
	url = O
[remote ""]
	url = empty
[remote.legacy]
	url = l
[core]
	editor = vim
[remote "origin"]
	fetch = f
[include]
	path = x
`)

	t.Run("All", func(t *testing.T) {
		assert.Equal(t, []string{
			"core.bare=false",
			"remote.origin.url=o",
			"remote.upstream.url=u",
			"remote.Origin.url=O",
			"remote..url=empty",
			"remote.legacy.url=l",
			"core.editor=vim",
			"remote.origin.fetch=f",
			"include.path=x",
		}, list(slices.Collect(c.All())))
	})

	t.Run("Section excludes subsections", func(t *testing.T) {
		assert.Equal(t, []string{"core.bare=false", "core.editor=vim"}, list(slices.Collect(c.Section("CORE"))))
		assert.Empty(t, slices.Collect(c.Section("remote")))
		assert.Empty(t, slices.Collect(c.Section("missing")))
	})

	t.Run("Subsection", func(t *testing.T) {
		assert.Equal(t, []string{"remote.origin.url=o", "remote.origin.fetch=f"}, list(slices.Collect(c.Subsection("Remote", "origin"))))
		assert.Equal(t, []string{"remote.Origin.url=O"}, list(slices.Collect(c.Subsection("remote", "Origin"))))
		assert.Equal(t, []string{"remote..url=empty"}, list(slices.Collect(c.Subsection("remote", ""))))
		assert.Equal(t, []string{"remote.legacy.url=l"}, list(slices.Collect(c.Subsection("remote", "legacy"))))
		assert.Empty(t, slices.Collect(c.Subsection("core", "")))
	})

	t.Run("Subsections in order of first appearance", func(t *testing.T) {
		assert.Equal(t, []string{"origin", "upstream", "Origin", "", "legacy"}, c.Subsections("remote"))
		assert.Empty(t, c.Subsections("core"))
		assert.Empty(t, c.Subsections("missing"))
	})

	t.Run("early break", func(t *testing.T) {
		n := 0
		for range c.All() {
			n++
			break
		}
		assert.Equal(t, 1, n)
	})
}

func TestConfigNil(t *testing.T) {
	var c *gitconfig.Config

	_, ok := c.Get("a.k")
	assert.False(t, ok)
	assert.Empty(t, c.GetAll("a.k"))
	assert.Empty(t, slices.Collect(c.All()))
	assert.Empty(t, slices.Collect(c.Section("a")))
	assert.Empty(t, slices.Collect(c.Subsection("a", "b")))
	assert.Empty(t, c.Subsections("a"))
	assert.Equal(t, "def", c.Value("a.k", "def"))

	b, err := c.Bool("a.k", true)
	require.NoError(t, err)
	assert.True(t, b)

	i, err := c.Int("a.k", 42)
	require.NoError(t, err)
	assert.EqualValues(t, 42, i)

	p, err := c.Path("a.k", "/home/u", "~/x")
	require.NoError(t, err)
	assert.Equal(t, "/home/u/x", p)
}

func TestConfigConcurrentReads(t *testing.T) {
	c := mustConfig(t, "[a]\n\tk = 1\n\tk = 2\n[b \"s\"]\n\tx = yes\n")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				e, _ := c.Get("a.k")
				assert.Equal(t, "2", e.Value)
				assert.Len(t, c.GetAll("a.k"), 2)
				assert.Len(t, slices.Collect(c.All()), 3)
				assert.Equal(t, []string{"s"}, c.Subsections("b"))
				b, err := c.Bool("b.s.x", false)
				assert.NoError(t, err)
				assert.True(t, b)
			}
		})
	}
	wg.Wait()
}

var boolCases = []struct {
	value string
	want  bool
}{
	{"true", true},
	{"TRUE", true},
	{"yes", true},
	{"Yes", true},
	{"on", true},
	{"oN", true},
	{"1", true},
	{"2", true},
	{"-1", true},
	{"1k", true},
	{"0x1", true},
	{"false", false},
	{"FALSE", false},
	{"no", false},
	{"nO", false},
	{"off", false},
	{"0", false},
	{"00", false},
	{"", false},
}

var boolErrorCases = []string{"maybe", "tru", "yes!", "1x", "0.5", "-2147483649", "yeſ", "falſe"}

func TestEntryBool(t *testing.T) {
	for _, tc := range boolCases {
		t.Run(strconv.Quote(tc.value), func(t *testing.T) {
			got, err := gitconfig.Entry{Value: tc.value}.Bool()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("implicit", func(t *testing.T) {
		got, err := gitconfig.Entry{Implicit: true}.Bool()
		require.NoError(t, err)
		assert.True(t, got)
	})

	// Not in boolCases, which are checked against git: see the minimum of
	// TestEntryInt.
	t.Run("minimum", func(t *testing.T) {
		got, err := gitconfig.Entry{Value: "-2147483648"}.Bool()
		require.NoError(t, err)
		assert.True(t, got)
	})

	for _, value := range boolErrorCases {
		t.Run("invalid "+strconv.Quote(value), func(t *testing.T) {
			e := gitconfig.Entry{Key: gitconfig.Key{Section: "a", Name: "k"}, Value: value}
			_, err := e.Bool()
			var ve *gitconfig.ValueError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, "bool", ve.Type)
			assert.Equal(t, e, ve.Entry)
		})
	}
}

var intErrorCases = []struct {
	name       string
	entry      gitconfig.Entry
	outOfRange bool
}{
	{"invalid unit", gitconfig.Entry{Value: "3x"}, false},
	{"not a number", gitconfig.Entry{Value: "v"}, false},
	{"empty", gitconfig.Entry{Value: ""}, false},
	{"implicit", gitconfig.Entry{Implicit: true}, false},
	{"too large", gitconfig.Entry{Value: "99999999999999999999"}, true},
	{"too large with unit", gitconfig.Entry{Value: "8589934592g"}, true},
	{"too small", gitconfig.Entry{Value: "-9223372036854775809"}, true},
	{"too small with unit", gitconfig.Entry{Value: "-8589934593g"}, true},
	{"non-ASCII unit", gitconfig.Entry{Value: "1\u212a"}, false}, // Kelvin sign, lowered to k by Unicode
}

var intCases = []struct {
	value string
	want  int64
}{
	{"0", 0},
	{"42", 42},
	{"-5", -5},
	{"1k", 1024},
	{"1K", 1024},
	{"-1k", -1024},
	{"1m", 1 << 20},
	{"1M", 1 << 20},
	{"1g", 1 << 30},
	{"1G", 1 << 30},
	{"0x10", 16},
	{"010", 8}, // octal
	{"9223372036854775807", 9223372036854775807},
	{"-9223372036854775807", -9223372036854775807},
	{"-8589934591g", -8589934591 << 30},
	{"8589934591g", 8589934591 << 30},
}

func TestEntryInt(t *testing.T) {
	for _, tc := range intCases {
		t.Run(tc.value, func(t *testing.T) {
			got, err := gitconfig.Entry{Value: tc.value}.Int()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	// Not in intCases, which are checked against git: git 2.47.3
	// rejects the minimum, a bug fixed in a later release.
	t.Run("minimum", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  int64
		}{
			{"-9223372036854775808", math.MinInt64},
			{"-8589934592g", math.MinInt64},
		} {
			got, err := gitconfig.Entry{Value: tc.value}.Int()
			require.NoError(t, err, tc.value)
			assert.Equal(t, tc.want, got, tc.value)
		}
	})

	for _, tc := range intErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.entry.Key = gitconfig.Key{Section: "a", Name: "k"}
			_, err := tc.entry.Int()
			var ve *gitconfig.ValueError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, "int", ve.Type)
			assert.Equal(t, tc.entry, ve.Entry)
			assert.Equal(t, tc.outOfRange, errors.Is(err, strconv.ErrRange))
		})
	}
}

var pathErrorCases = []struct {
	name  string
	entry gitconfig.Entry
	home  string
}{
	{"home unknown", gitconfig.Entry{Value: "~/x"}, ""},
	{"prefix", gitconfig.Entry{Value: "%(prefix)/x"}, "/home/u"},
	{"implicit", gitconfig.Entry{Implicit: true}, "/home/u"},
}

var pathCases = []struct {
	value string
	home  string
	want  string
}{
	{"~/x", "/home/u", "/home/u/x"},
	{"~/", "/home/u", "/home/u/"},
	{"~", "/home/u", "/home/u"},
	{"/abs/x", "", "/abs/x"},
	{"rel/x", "", "rel/x"},
	{"x/~/y", "", "x/~/y"},
}

func TestEntryPath(t *testing.T) {
	for _, tc := range pathCases {
		t.Run(tc.value, func(t *testing.T) {
			got, err := gitconfig.Entry{Value: tc.value}.Path(tc.home)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	for _, tc := range pathErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.entry.Path(tc.home)
			var ve *gitconfig.ValueError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, "path", ve.Type)
			assert.Equal(t, tc.entry, ve.Entry)
		})
	}
}

func TestConfigTypedGetters(t *testing.T) {
	c := mustConfig(t, `[a]
	bool = maybe
	bool = yes
	badbool = maybe
	int = 2k
	badint = 3x
	path = ~/p
	badpath = %(prefix)/p
	implicit
`)

	t.Run("Value", func(t *testing.T) {
		assert.Equal(t, "yes", c.Value("a.bool", "def"))
		assert.Equal(t, "def", c.Value("a.missing", "def"))
		assert.Equal(t, "", c.Value("a.implicit", "def"))
	})

	t.Run("Bool", func(t *testing.T) {
		for _, tc := range []struct {
			key  string
			def  bool
			want bool
			err  bool
		}{
			{"a.bool", false, true, false}, // only the last value is interpreted
			{"a.implicit", false, true, false},
			{"a.missing", true, true, false},
			{"a.missing", false, false, false},
			{"a.badbool", true, true, true},
			{"a.badbool", false, false, true},
		} {
			t.Run(fmt.Sprintf("%s/%v", tc.key, tc.def), func(t *testing.T) {
				got, err := c.Bool(tc.key, tc.def)
				assert.Equal(t, tc.want, got)
				if tc.err {
					var ve *gitconfig.ValueError
					require.ErrorAs(t, err, &ve)
					assert.Equal(t, "badbool", ve.Entry.Key.Name)
					assert.Equal(t, 4, ve.Entry.Origin.Line)
				} else {
					require.NoError(t, err)
				}
			})
		}
	})

	t.Run("Int", func(t *testing.T) {
		got, err := c.Int("a.int", 0)
		require.NoError(t, err)
		assert.EqualValues(t, 2048, got)

		got, err = c.Int("a.missing", 7)
		require.NoError(t, err)
		assert.EqualValues(t, 7, got)

		got, err = c.Int("a.badint", 7)
		var ve *gitconfig.ValueError
		require.ErrorAs(t, err, &ve)
		assert.EqualValues(t, 7, got)

		got, err = c.Int("a.implicit", 7)
		require.ErrorAs(t, err, &ve)
		assert.EqualValues(t, 7, got)
	})

	t.Run("Path", func(t *testing.T) {
		got, err := c.Path("a.path", "/home/u", "def")
		require.NoError(t, err)
		assert.Equal(t, "/home/u/p", got)

		got, err = c.Path("a.badpath", "/home/u", "def")
		var ve *gitconfig.ValueError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "def", got)

		got, err = c.Path("a.path", "", "def")
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "def", got)
	})

	t.Run("Path expands the default", func(t *testing.T) {
		got, err := c.Path("a.missing", "/home/u", "~/d")
		require.NoError(t, err)
		assert.Equal(t, "/home/u/d", got)
	})

	t.Run("Path with a default that cannot be expanded", func(t *testing.T) {
		got, err := c.Path("a.missing", "", "~/d")
		var ve *gitconfig.ValueError
		require.ErrorAs(t, err, &ve)
		assert.Equal(t, "~/d", got)
		assert.Equal(t, "~/d", ve.Entry.Value)
		assert.Equal(t, gitconfig.Key{Section: "a", Name: "missing"}, ve.Entry.Key)
		assert.Equal(t, gitconfig.Origin{}, ve.Entry.Origin)
	})
}

// bigConfig returns a configuration of n remotes and n branches, the shape
// of a busy repository's config.
func bigConfig(n int) string {
	var sb strings.Builder
	sb.WriteString("[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n")
	for i := range n {
		fmt.Fprintf(&sb, "[remote \"r%d\"]\n\turl = https://example.com/r%d.git\n\tfetch = +refs/heads/*:refs/remotes/r%d/*\n", i, i, i)
		fmt.Fprintf(&sb, "[branch \"b%d\"]\n\tremote = r%d\n\tmerge = refs/heads/b%d\n", i, i, i)
	}
	sb.WriteString("[user]\n\tname = Someone\n")
	return sb.String()
}

func BenchmarkConfig(b *testing.B) {
	for _, n := range []int{10, 1000} {
		c := mustConfig(b, bigConfig(n))
		b.Run(fmt.Sprintf("Get/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				c.Get("user.name")
			}
		})
		b.Run(fmt.Sprintf("GetMissing/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				c.Get("core.editor")
			}
		})
		b.Run(fmt.Sprintf("GetAll/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				c.GetAll("remote.r1.fetch")
			}
		})
		b.Run(fmt.Sprintf("Subsections/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				c.Subsections("remote")
			}
		})
		b.Run(fmt.Sprintf("Bool/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				_, _ = c.Bool("core.filemode", false)
			}
		})
	}
}
