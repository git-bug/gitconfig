package gitconfig

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// Limits of one load (requirement X3 of docs/REQUIREMENTS.md): git's
// include depth, and bounds on the work and memory a set of files built to
// multiply them can cause. Parse and edits apply the byte and entry limits
// to their one file.
//
// Memory follows the entries, not the bytes: one entry ("k\n") can cost
// a hundred times its size. Distinct section headers count as entries, as
// the parser keeps each one. A parsed File also keeps every piece of the
// file (blank lines, comments, ...), at most maxFileItems of them: four per
// entry, as a line holds indentation, an entry, a comment and a newline.
//
// A typical configuration has tens to hundreds of entries; the limits leave
// room for one that tools have appended to for years.
const (
	maxIncludeDepth = 10
	maxLoadFiles    = 1000
	maxLoadBytes    = 4 << 20
	maxLoadEntries  = 1 << 16
	maxFileItems    = 4 * maxLoadEntries
)

// repo is the repository of an Env.
type repo struct {
	gitDir    string // as given: the worktree's git directory
	commonDir string // the repository's git directory
}

// repo returns the repository of the Env, or nil outside a repository.
func (e Env) repo() (*repo, error) {
	if e.GitDir == "" {
		return nil, nil
	}
	if !filepath.IsAbs(e.GitDir) {
		return nil, &FileError{Path: e.GitDir, Err: ErrNotAbsolute}
	}
	r := &repo{gitDir: e.GitDir, commonDir: e.GitDir}
	data, err := readSmall(filepath.Join(e.GitDir, "commondir"))
	switch {
	case err == nil:
		dir := strings.TrimRight(string(data), "\r\n")
		if !filepath.IsAbs(dir) {
			if dir, err = joinReal(e.GitDir, dir); err != nil {
				return nil, &FileError{Path: e.GitDir, Err: err}
			}
		}
		r.commonDir = filepath.Clean(dir)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, &FileError{Path: filepath.Join(e.GitDir, "commondir"), Err: err}
	}
	return r, nil
}

// joinReal joins the relative path rel to the directory dir as the system
// resolves it: the links of dir are followed before the ".." of rel apply,
// where filepath.Join would apply them first.
func joinReal(dir, rel string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(real, rel), nil
}

func (r *repo) localConfig() string {
	return filepath.Join(r.commonDir, "config")
}

func (r *repo) worktreeConfig() string {
	return filepath.Join(r.gitDir, "config.worktree")
}

// worktreeConfigEnabled reports whether extensions.worktreeConfig is set
// in the repository's config.
func (r *repo) worktreeConfigEnabled() (bool, error) {
	data, err := readConfigFile(r.localConfig(), maxLoadBytes)
	if isMissing(err) {
		return false, nil
	}
	if err != nil {
		return false, &FileError{Path: r.localConfig(), Err: err}
	}
	var entries []Entry
	err = parseEntries(r.localConfig(), data, ScopeLocal, new(maxLoadEntries), func(e Entry) error {
		if e.Key == worktreeConfigKey {
			entries = append(entries, e)
		}
		return nil
	})
	if err != nil {
		return false, &FileError{Path: r.localConfig(), Err: err}
	}
	return worktreeConfigIn(entries, r.localConfig())
}

var worktreeConfigKey = Key{Section: "extensions", Name: "worktreeconfig"}

// worktreeConfigIn returns the last value of extensions.worktreeConfig in
// the entries of file path: git does not take it from included files.
func worktreeConfigIn(entries []Entry, path string) (bool, error) {
	enabled := false
	for _, e := range entries {
		if e.Key != worktreeConfigKey || e.Origin.File != path {
			continue
		}
		b, err := e.Bool()
		if err != nil {
			return false, &FileError{Path: path, Err: err}
		}
		enabled = b
	}
	return enabled, nil
}

// severalWorktrees reports whether the repository has linked worktrees.
func (r *repo) severalWorktrees() bool {
	entries, err := os.ReadDir(filepath.Join(r.commonDir, "worktrees"))
	return err == nil && len(entries) > 0
}

// branch returns the branch HEAD points to, or "" if it is detached or
// cannot be read.
func (r *repo) branch() string {
	data, err := readSmall(filepath.Join(r.gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	s, ok := strings.CutPrefix(string(data), "ref:")
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	name, ok := strings.CutPrefix(s, "refs/heads/")
	if !ok {
		return ""
	}
	return name
}

// readSmall reads a small file of the repository, such as HEAD.
func readSmall(path string) ([]byte, error) {
	return readConfigFile(path, 4096)
}

// readConfigFile reads a regular file, at most limit bytes. A file that is
// not regular is ErrNotRegular, read without blocking: a FIFO would block
// until someone writes to it. os.DevNull, which git documents to skip a
// scope, reads as empty.
func readConfigFile(path string, limit int64) ([]byte, error) {
	if path == os.DevNull {
		return nil, nil
	}
	var data []byte
	err := retryTransient(func() error {
		f, err := openRegular(path)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return ErrNotRegular
		}
		if fi.Size() > limit {
			return ErrLoadLimit
		}
		data, err = io.ReadAll(io.LimitReader(f, limit+1))
		if err == nil && int64(len(data)) > limit {
			err = ErrLoadLimit
		}
		return err
	})
	return data, err
}

// isMissing reports whether err means that a file does not exist: also
// when a directory of its path is a file, as git takes it.
func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// loader loads files into a list of entries, following includes, within
// the limits of one load.
type loader struct {
	env  Env
	repo *repo

	files   int
	bytes   int64
	budget  int // entries and distinct headers that may still be read
	entries []Entry

	branch     string
	branchRead bool

	realGitDir  string // with '/', "" if it cannot be resolved
	realDirRead bool

	conds map[condKey]bool // includeIf conditions evaluated so far

	// includeIf "hasconfig:remote.*.url:" needs every remote URL of the
	// load, including those read after it: its includes are followed, and
	// config drops them if no URL matches.
	urls        []string     // the remote URLs read
	urlCond     bool         // such a condition was met
	urlIncludes []urlInclude // the includes it followed, by start
	inIncludeIf int          // includeIf includes being loaded
	condURL     *Origin      // of the first remote URL read from one of them
}

// urlInclude is an include of "hasconfig:remote.*.url:<glob>", and the
// entries it read: l.entries[start:end]. Two of them are nested or
// disjoint.
type urlInclude struct {
	glob       string
	start, end int
}

const urlCondPrefix = "hasconfig:remote.*.url:"

// isRemoteURL reports whether k is a remote URL, remote.<name>.url.
func isRemoteURL(k Key) bool {
	return k.Section == "remote" && k.HasSubsection && k.Name == "url"
}

// condKey is an includeIf condition and the file that holds it, which
// "gitdir:./" depends on.
type condKey struct{ cond, from string }

func (e Env) newLoader() (*loader, error) {
	r, err := e.repo()
	if err != nil {
		return nil, err
	}
	return &loader{env: e, repo: r, budget: maxLoadEntries}, nil
}

// config returns the configuration loaded. Once every remote URL is known,
// the includes of the hasconfig:remote.*.url: conditions that match none
// are dropped.
func (l *loader) config() *Config {
	if len(l.urlIncludes) == 0 {
		return newConfig(l.entries)
	}
	matches := make(map[string]bool)
	// A new slice: l.entries and the offsets of l.urlIncludes stay valid.
	entries := make([]Entry, 0, len(l.entries))
	next := 0 // l.entries[:next] are copied or dropped
	for _, inc := range l.urlIncludes {
		if inc.start < next {
			// Within an include already dropped.
			continue
		}
		ok, found := matches[inc.glob]
		if !found {
			toks, valid := compileGlob(inc.glob)
			ok = valid && slices.ContainsFunc(l.urls, func(url string) bool { return matchTokens(toks, url, false) })
			matches[inc.glob] = ok
		}
		if !ok {
			entries = append(entries, l.entries[next:inc.start]...)
			next = inc.end
		}
	}
	return newConfig(append(entries, l.entries[next:]...))
}

// add appends e to the entries. It notes a remote URL for
// hasconfig:remote.*.url:, and fails as git does on one read from a file
// included by an includeIf once such a condition is met.
func (l *loader) add(e Entry) error {
	l.entries = append(l.entries, e)
	if !isRemoteURL(e.Key) {
		return nil
	}
	// An implicit URL is no URL (git crashes on it).
	if !e.Implicit {
		l.urls = append(l.urls, e.Value)
	}
	if l.inIncludeIf > 0 && l.condURL == nil {
		l.condURL = &e.Origin
		return l.urlError()
	}
	return nil
}

// urlError returns git's error once a hasconfig:remote.*.url: condition is
// met and a remote URL was read from a file included by an includeIf, in
// either order, matching or not.
func (l *loader) urlError() error {
	if o := l.condURL; l.urlCond && o != nil {
		return &FileError{Path: o.File, Err: fmt.Errorf("%w (line %d)", ErrRemoteURLInInclude, o.Line)}
	}
	return nil
}

// loadFile loads the file at path for scope. A missing file is empty. If
// skipUnreadable, a file that cannot be read for lack of permission is
// skipped, as git does for global files.
func (l *loader) loadFile(path string, scope Scope, skipUnreadable bool) error {
	err := l.load(path, scope, 0)
	// Only path itself is skipped: an unreadable include of it still fails.
	if skipUnreadable {
		if fe, ok := errors.AsType[*FileError](err); ok && fe.Path == path && errors.Is(fe.Err, fs.ErrPermission) {
			return nil
		}
	}
	return err
}

func (l *loader) load(path string, scope Scope, depth int) error {
	l.files++
	if l.files > maxLoadFiles {
		return &FileError{Path: path, Err: ErrLoadLimit}
	}
	// Read at most what remains of the byte budget of the load.
	data, err := readConfigFile(path, maxLoadBytes-l.bytes)
	if isMissing(err) {
		return nil
	}
	if err != nil {
		return &FileError{Path: path, Err: err}
	}
	l.bytes += int64(len(data))

	// The entries of an include go right after its include.path entry, so
	// that later entries of this file take precedence over them: includes
	// are followed as the file is parsed. As in git, an error in an include
	// is reported before a syntax error further down this file.
	var includeErr error
	err = parseEntries(path, data, scope, &l.budget, func(e Entry) error {
		if includeErr = l.add(e); includeErr != nil {
			return includeErr
		}
		includeErr = l.include(e, path, scope, depth)
		return includeErr
	})
	if includeErr != nil {
		return includeErr
	}
	if err != nil {
		return &FileError{Path: path, Err: err}
	}
	return nil
}

// include follows e if it is an include directive. from is the file that
// holds it, "" for the command line.
func (l *loader) include(e Entry, from string, scope Scope, depth int) error {
	urlGlob, isURLCond := "", false
	switch {
	case e.Key.Section == "include" && !e.Key.HasSubsection:
		if e.Key.Name != "path" {
			return nil
		}
	case e.Key.Section == "includeif" && e.Key.HasSubsection:
		// As in git, a hasconfig:remote.*.url: condition is met by any key
		// of its section, path or not.
		if urlGlob, isURLCond = strings.CutPrefix(e.Key.Subsection, urlCondPrefix); isURLCond {
			l.urlCond = true
			if err := l.urlError(); err != nil {
				return err
			}
		}
		if e.Key.Name != "path" {
			return nil
		}
		// A hasconfig:remote.*.url: include is followed for now: see config.
		if !isURLCond {
			ok, err := l.condition(e.Key.Subsection, from)
			if err != nil || !ok {
				return err
			}
		}
	default:
		return nil
	}

	if e.Implicit {
		return l.fail(from, e, "missing value for include.path")
	}
	if e.Value == "" {
		// An empty path includes nothing.
		return nil
	}
	path, err := e.Path(l.env.Home)
	if err != nil {
		return &FileError{Path: from, Err: err}
	}
	if !filepath.IsAbs(path) {
		if from == "" {
			return &FileError{Path: path, Err: ErrNotAbsolute}
		}
		path = dirPrefix(from) + path
	}
	if depth+1 > maxIncludeDepth {
		return &FileError{Path: path, Err: fmt.Errorf("%w while including %s from %s; this might be due to circular includes", ErrIncludeDepth, path, from)}
	}
	if e.Key.Section == "includeif" {
		l.inIncludeIf++
		defer func() { l.inIncludeIf-- }()
	}
	// Added before loading, so that it comes before the includes it holds.
	i := len(l.urlIncludes)
	if isURLCond {
		l.urlIncludes = append(l.urlIncludes, urlInclude{urlGlob, len(l.entries), 0})
	}
	err = l.load(path, scope, depth+1)
	if isURLCond {
		l.urlIncludes[i].end = len(l.entries)
	}
	return err
}

// fail reports an invalid include directive, as git does: a bad line of
// the file, or a bad command-line entry.
func (l *loader) fail(from string, e Entry, msg string) error {
	if from == "" {
		return &KeyError{Key: e.Key.String(), Err: errors.New("gitconfig: " + msg)}
	}
	return &FileError{Path: from, Err: &SyntaxError{File: from, Line: e.Origin.Line, Msg: msg}}
}

// dirPrefix returns path up to and including its last separator: an
// include's relative path is appended to it as is, not cleaned, as git
// does.
func dirPrefix(path string) string {
	i := strings.LastIndexByte(path, '/')
	if runtime.GOOS == "windows" {
		i = max(i, strings.LastIndexByte(path, '\\'))
	}
	return path[:i+1]
}

// condition evaluates the condition of an includeIf, once per condition
// and file: a file can repeat path under one costly gitdir: pattern.
func (l *loader) condition(cond, from string) (bool, error) {
	k := condKey{cond, from}
	if ok, found := l.conds[k]; found {
		return ok, nil
	}
	ok, err := l.evalCondition(cond, from)
	if err != nil {
		return false, err
	}
	if l.conds == nil {
		l.conds = make(map[condKey]bool)
	}
	l.conds[k] = ok
	return ok, nil
}

// evalCondition evaluates the condition of an includeIf, but
// hasconfig:remote.*.url:, which include and config handle. Unknown
// conditions are false.
func (l *loader) evalCondition(cond, from string) (bool, error) {
	switch {
	case strings.HasPrefix(cond, "gitdir:"):
		return l.gitdir(cond[len("gitdir:"):], from, false)
	case strings.HasPrefix(cond, "gitdir/i:"):
		return l.gitdir(cond[len("gitdir/i:"):], from, true)
	case strings.HasPrefix(cond, "onbranch:"):
		if l.repo == nil {
			return false, nil
		}
		if !l.branchRead {
			l.branch, l.branchRead = l.repo.branch(), true
		}
		if l.branch == "" {
			return false, nil
		}
		pattern := cond[len("onbranch:"):]
		if strings.HasSuffix(pattern, "/") {
			pattern += "**"
		}
		return globMatch(pattern, l.branch, false), nil
	}
	return false, nil
}

// gitdir evaluates includeIf "gitdir:<pattern>": "~/" is the home
// directory, "./" the absolute directory of the including file, a relative
// pattern matches at any depth ("**/" prepended) and a trailing '/' matches
// everything below ("**" appended). The pattern is matched against the git
// directory as given and its real path.
func (l *loader) gitdir(pattern, from string, fold bool) (bool, error) {
	switch {
	case strings.HasPrefix(pattern, "./"):
		if from == "" {
			return false, &FileError{Path: pattern, Err: ErrNotAbsolute}
		}
		dir := dirPrefix(from)
		if !filepath.IsAbs(from) {
			abs, err := filepath.Abs(from)
			if err != nil {
				return false, &FileError{Path: from, Err: err}
			}
			dir = dirPrefix(abs)
		}
		pattern = filepath.ToSlash(dir) + pattern[2:]
	case strings.HasPrefix(pattern, "~/"):
		if l.env.Home == "" {
			return false, nil
		}
		pattern = strings.TrimSuffix(filepath.ToSlash(l.env.Home), "/") + pattern[1:]
	}
	// Checked only now: a "./" pattern on the command line is an error even
	// outside a repository.
	if l.repo == nil {
		return false, nil
	}
	if !isAbsPattern(pattern) {
		pattern = "**/" + pattern
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	dir := filepath.ToSlash(l.repo.gitDir)
	if globMatch(pattern, dir, fold) {
		return true, nil
	}
	if !l.realDirRead {
		if real, err := filepath.EvalSymlinks(l.repo.gitDir); err == nil {
			l.realGitDir = filepath.ToSlash(real)
		}
		l.realDirRead = true
	}
	if l.realGitDir != "" && l.realGitDir != dir {
		return globMatch(pattern, l.realGitDir, fold), nil
	}
	return false, nil
}

// isAbsPattern reports whether a gitdir pattern, written with '/', is
// absolute.
func isAbsPattern(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return runtime.GOOS == "windows" && filepath.IsAbs(filepath.FromSlash(p))
}

// command loads the entries of the command line.
func (l *loader) command() error {
	for _, c := range l.env.Command {
		k, err := ParseKey(c.Key)
		if err != nil {
			return err
		}
		e := Entry{Key: k, Value: c.Value, Implicit: c.Implicit, Origin: Origin{Scope: ScopeCommand}}
		if c.Implicit {
			e.Value = ""
		}
		if err := l.add(e); err != nil {
			return err
		}
		if err := l.include(e, "", ScopeCommand, 0); err != nil {
			return err
		}
	}
	return nil
}
