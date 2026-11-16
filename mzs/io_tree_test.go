package mzs

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"sort"
	"strings"
	"testing"
	"time"
)

// RFC 0015: io.stat, io.walk, io.rm, io.mkdir and io.mv. The three that change a tree
// exist only when the host both implements the interface behind them and says so with
// Options.FSMutations, and io.walk only when the host can tell a link from a directory —
// so these tests pin each half of every gate as well as what the members do.

// treeFS is memFS with links and the four optional interfaces. A link is a name in
// links, pointing at a directory or a file in the same maps; Stat follows it, Lstat does
// not.
type treeFS struct {
	*memFS
	links map[string]string
}

func newTreeFS() *treeFS {
	m := newMemFS(nil)
	m.dirs["."] = nil
	return &treeFS{memFS: m, links: map[string]string{}}
}

// parentOf is the directory a name is listed in, with "." for a top-level name.
func parentOf(name string) (string, string) {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ".", name
	}
	if i == 0 {
		return "/", name[1:]
	}
	return name[:i], name[i+1:]
}

func (t *treeFS) addEntry(name string) {
	dir, base := parentOf(name)
	for _, n := range t.dirs[dir] {
		if n == base {
			return
		}
	}
	t.dirs[dir] = append(t.dirs[dir], base)
}

func (t *treeFS) removeEntry(name string) {
	dir, base := parentOf(name)
	names := t.dirs[dir]
	for i, n := range names {
		if n == base {
			t.dirs[dir] = append(names[:i:i], names[i+1:]...)
			return
		}
	}
}

func (t *treeFS) file(name, text string) *treeFS {
	t.files[name] = text
	t.addEntry(name)
	return t
}

func (t *treeFS) dir(name string) *treeFS {
	if _, ok := t.dirs[name]; !ok {
		t.dirs[name] = nil
	}
	if name != "." && name != "/" {
		t.addEntry(name)
	}
	return t
}

func (t *treeFS) link(name, target string) *treeFS {
	t.links[name] = target
	t.addEntry(name)
	return t
}

func (t *treeFS) Stat(name string) (bool, int64, bool, error) {
	if target, ok := t.links[name]; ok {
		return t.memFS.Stat(target)
	}
	return t.memFS.Stat(name)
}

func (t *treeFS) List(dir string) ([]string, error) {
	if target, ok := t.links[dir]; ok {
		dir = target
	}
	return t.memFS.List(dir)
}

func (t *treeFS) Lstat(name string) (bool, int64, bool, bool, error) {
	t.asked = append(t.asked, "lstat "+name)
	if t.fail != nil {
		return false, 0, false, false, t.fail
	}
	if _, ok := t.links[name]; ok {
		return true, 0, false, true, nil
	}
	if s, ok := t.files[name]; ok {
		return true, int64(len(s)), false, false, nil
	}
	if _, ok := t.dirs[name]; ok {
		return true, 0, true, false, nil
	}
	return false, 0, false, false, nil
}

// Create puts a new file in its directory's listing, as a real one would.
func (t *treeFS) Create(name string) (io.WriteCloser, error) {
	t.addEntry(name)
	return t.memFS.Create(name)
}

func (t *treeFS) Remove(name string) error {
	t.asked = append(t.asked, "rm "+name)
	// A directory io.walk yields ends in `/`, and os.Remove takes it so.
	if len(name) > 1 {
		name = strings.TrimSuffix(name, "/")
	}
	if t.fail != nil {
		return t.fail
	}
	switch {
	case t.links[name] != "":
		delete(t.links, name)
	case t.files[name] != "" || hasKey(t.files, name):
		delete(t.files, name)
	case hasDir(t.dirs, name):
		if len(t.dirs[name]) > 0 {
			return errors.New("directory not empty")
		}
		delete(t.dirs, name)
	default:
		return fmt.Errorf("remove %s: %w", name, iofs.ErrNotExist)
	}
	t.removeEntry(name)
	return nil
}

func (t *treeFS) MkdirAll(name string) error {
	t.asked = append(t.asked, "mkdir "+name)
	if t.fail != nil {
		return t.fail
	}
	if hasKey(t.files, name) {
		return errors.New("not a directory")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		t.dir(strings.Join(parts[:i+1], "/"))
	}
	return nil
}

func (t *treeFS) Rename(from, to string) error {
	t.asked = append(t.asked, "mv "+from+" "+to)
	if t.fail != nil {
		return t.fail
	}
	s, ok := t.files[from]
	if !ok {
		return fmt.Errorf("rename %s: %w", from, iofs.ErrNotExist)
	}
	delete(t.files, from)
	t.removeEntry(from)
	t.file(to, s)
	return nil
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func hasDir(m map[string][]string, k string) bool { _, ok := m[k]; return ok }

// treeOpts hands the tree over with structural changes allowed.
func treeOpts(fs FileSystem) Options {
	return Options{Timeout: 5 * time.Second, FS: fs, FSMutations: true}
}

// sample is the tree the walk tests read:
//
//	a.txt  b/  b/c.mzs  b/d/  b/d/e.mzs  b/up -> .  z.mzs
func sample() *treeFS {
	return newTreeFS().
		file("z.mzs", "z").
		file("a.txt", "a").
		dir("b").
		file("b/c.mzs", "c").
		dir("b/d").
		file("b/d/e.mzs", "e").
		link("b/up", ".")
}

func TestIOMutationsNeedConsentAndAnInterface(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		o    Options
		want []string // what the diagnostic must name; nil means it compiles
	}{
		{"both", treeOpts(sample()), nil},
		{"no consent", Options{FS: sample()}, []string{"Options.FSMutations is off"}},
		{"no interface", Options{FS: newMemFS(nil), FSMutations: true}, []string{"does not implement FileRemover"}},
		{"neither", Options{FS: newMemFS(nil)}, []string{"Options.FSMutations is off", "FileRemover"}},
	} {
		_, err := New(tc.o).Compile("t", "include io\nio.rm(\"x\")")
		if tc.want == nil {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: io.rm compiled", tc.name)
			continue
		}
		for _, w := range append([]string{"io.rm needs the host to allow structural changes"}, tc.want...) {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: error = %v; want it to mention %q", tc.name, err, w)
			}
		}
	}

	// The same for the other two, and for each spelling: a call, and the bare member.
	for _, src := range []string{`io.mkdir("x")`, `f = io.mkdir`, `io.mv("a", "b")`, `f = io.mv`} {
		if _, err := New(Options{FS: sample()}).Compile("t", "include io\n"+src); err == nil ||
			!strings.Contains(err.Error(), "Options.FSMutations is off") {
			t.Errorf("%s without consent: err = %v", src, err)
		}
	}
}

func TestIOClosedMembersAreAbsent(t *testing.T) {
	t.Parallel()

	got := mustEvalIO(t, Options{FS: newMemFS(nil)}, `io.keys.join(",")`)
	if got != "stdin,lines,read,write,append,exists,ls,env,stat" {
		t.Errorf("io.keys without the optional interfaces = %s", got)
	}
	got = mustEvalIO(t, Options{FS: sample()}, `io.keys.join(",")`)
	if got != "stdin,lines,read,write,append,exists,ls,env,stat,walk" {
		t.Errorf("io.keys with LinkStater and no consent = %s", got)
	}
	got = mustEvalIO(t, treeOpts(sample()), `io.keys.join(",")`)
	if got != "stdin,lines,read,write,append,exists,ls,env,stat,walk,rm,mkdir,mv" {
		t.Errorf("io.keys with everything = %s", got)
	}

	// walk needs nothing but LinkStater, and says so when it is missing.
	_, err := New(Options{FS: newMemFS(nil), FSMutations: true}).Compile("t", "include io\nio.walk()")
	if err == nil || !strings.Contains(err.Error(), "LinkStater") {
		t.Errorf("io.walk without LinkStater: err = %v", err)
	}
}

// A script module that took the name `io` is not the built-in one, and a member it does
// not export is an ordinary unknown member, not a capability the host is withholding.
func TestIOClosedMemberOfAScriptModuleIsJustUnknown(t *testing.T) {
	t.Parallel()

	o := Options{Timeout: 5 * time.Second}
	o.ModuleLoader = func(from, path string) (string, string, error) {
		return path, "export fn read(p) { p }", nil
	}
	_, err := New(o).Eval(context.Background(), "include io from \"./io.mzs\"\nio.rm(\"x\")", nil)
	if err == nil || strings.Contains(err.Error(), "structural") ||
		!strings.Contains(err.Error(), "undefined member 'rm'") {
		t.Errorf("err = %v; want the ordinary undefined-member error", err)
	}
}

// A host that hands the members over in a module of its own has bypassed the gate, and a
// script still cannot change a tree without Options.FSMutations.
func TestIOMutationsAreCheckedWhenTheGateIsBypassed(t *testing.T) {
	t.Parallel()

	full := treeOpts(sample())
	mod, ok := LookupModule("io", &full)
	if !ok {
		t.Fatal("no io module")
	}
	rm := mod.Get(Str("rm"))
	fs := sample()
	in := New(Options{Timeout: 5 * time.Second, FS: fs})
	in.RegisterModule("files", map[string]Value{"rm": rm})
	_, err := in.Eval(context.Background(), "include files\nfiles.rm(\"a.txt\")", nil)
	if err == nil || !strings.Contains(err.Error(), "Options.FSMutations") {
		t.Fatalf("err = %v; want the FSMutations refusal", err)
	}
	if _, ok := fs.files["a.txt"]; !ok {
		t.Fatal("the file was removed")
	}
}

func TestIOStat(t *testing.T) {
	t.Parallel()

	o := treeOpts(sample())
	if got := mustEvalIO(t, o, `io.stat("a.txt").json`); got != `{"size":1,"dir":false}` {
		t.Errorf("stat of a file = %s", got)
	}
	if got := mustEvalIO(t, o, `io.stat("b").json`); got != `{"size":0,"dir":true}` {
		t.Errorf("stat of a directory = %s", got)
	}
	if got := mustEvalIO(t, o, `io.stat("nope") == nil`); got != "true" {
		t.Errorf("stat of nothing = %s", got)
	}
	// Stat follows a link, as FileSystem.Stat does.
	if got := mustEvalIO(t, o, `io.stat("b/up")["dir"]`); got != "true" {
		t.Errorf("stat of a link to a directory = %s", got)
	}
	// It needs no optional interface.
	if got := mustEvalIO(t, Options{FS: newMemFS(map[string]string{"x": "abc"})}, `io.stat("x")["size"]`); got != "3" {
		t.Errorf("stat on a plain FileSystem = %s", got)
	}
	fs := sample()
	fs.fail = errors.New("permission denied")
	if got := mustEvalIO(t, treeOpts(fs), `try io.stat("a.txt") else (e) -> e["kind"] + ": " + e["message"]`); got != `io: io.stat "a.txt": permission denied` {
		t.Errorf("a failed stat = %s", got)
	}
}

func TestIOWalk(t *testing.T) {
	t.Parallel()

	o := treeOpts(sample())
	// Depth-first, sorted per directory, directories with a trailing `/` and then
	// entered, the link yielded and not entered.
	want := "a.txt b/ b/c.mzs b/d/ b/d/e.mzs b/up z.mzs"
	if got := mustEvalIO(t, o, `io.walk.array.join(" ")`); got != want {
		t.Errorf("io.walk = %s, want %s", got, want)
	}
	if got := mustEvalIO(t, o, `io.walk(".").array.join(" ")`); got != want {
		t.Errorf("io.walk(\".\") = %s", got)
	}
	// Under a root the paths carry it, with its trailing `/`s removed first.
	for _, root := range []string{"b", "b/", "b///"} {
		got := mustEvalIO(t, o, fmt.Sprintf(`io.walk(%q).array.join(" ")`, root))
		if got != "b/c.mzs b/d/ b/d/e.mzs b/up" {
			t.Errorf("io.walk(%q) = %s", root, got)
		}
	}
	// It composes with path.match into a glob.
	got := mustEvalIO(t, o, "include path\nio.walk.filter { path.match(\"**/*.mzs\", it) }.array.join(\" \")")
	if got != "b/c.mzs b/d/e.mzs z.mzs" {
		t.Errorf("walk + match = %s", got)
	}
}

func TestIOWalkRoot(t *testing.T) {
	t.Parallel()

	fs := sample().link("lnk", "b")
	o := treeOpts(fs)
	for root, want := range map[string]string{
		"lnk":     `io.walk "lnk": is a symbolic link, and walk does not follow links`,
		"lnk/":    `io.walk "lnk": is a symbolic link, and walk does not follow links`,
		"a.txt":   `io.walk "a.txt": not a directory`,
		"missing": `io.walk "missing": no such directory`,
	} {
		got := mustEvalIO(t, o, fmt.Sprintf(`try io.walk(%q).array else (e) -> e["message"]`, root))
		if got != want {
			t.Errorf("io.walk(%q) = %s, want %s", root, got, want)
		}
	}
	// `/` stays `/`, and its children get one separator.
	root := newTreeFS().dir("/").file("/x", "x")
	if got := mustEvalIO(t, treeOpts(root), `io.walk("/").array.join(" ")`); got != "/x" {
		t.Errorf("io.walk(\"/\") = %s", got)
	}
	// `..` in the root is the host's to resolve, so it reaches the host as written.
	fs.asked = nil
	mustEvalIO(t, o, `try io.walk("missing/../b").array else nil`)
	if len(fs.asked) == 0 || fs.asked[0] != "lstat missing/../b" {
		t.Errorf("asked = %v; want the root unresolved", fs.asked)
	}
}

func TestIOWalkIsLazy(t *testing.T) {
	t.Parallel()

	fs := sample()
	o := treeOpts(fs)
	if got := mustEvalIO(t, o, `w = io.walk; w.take(1).array.join(" ")`); got != "a.txt" {
		t.Fatalf("first = %s", got)
	}
	// One look at the root, one list of it, one look at the first entry — and nothing
	// of b/ or below.
	if strings.Join(fs.asked, ",") != "lstat .,.,lstat a.txt" {
		t.Errorf("asked = %v", fs.asked)
	}
	// Naming the seq does nothing at all.
	fs.asked = nil
	mustEvalIO(t, o, `w = io.walk; nil`)
	if len(fs.asked) != 0 {
		t.Errorf("an untraversed walk asked %v", fs.asked)
	}
	// A second traversal walks again, and sees what changed.
	got := mustEvalIO(t, o, `w = io.walk("b/d"); first = w.array.join(" "); io.write("b/d/f.mzs", "f"); first + " | " + w.array.join(" ")`)
	if got != "b/d/e.mzs | b/d/e.mzs b/d/f.mzs" {
		t.Errorf("two traversals = %s", got)
	}
}

func TestIOWalkFailures(t *testing.T) {
	t.Parallel()

	// An entry listed and gone before it is looked at is not reported.
	fs := sample()
	fs.dirs["."] = append(fs.dirs["."], "ghost")
	if got := mustEvalIO(t, treeOpts(fs), `io.walk.array.join(" ")`); strings.Contains(got, "ghost") {
		t.Errorf("walk reported a vanished entry: %s", got)
	}
	// A directory that cannot be listed is a catchable io error from the pull that needed it.
	got := mustEvalIO(t, treeOpts(&listFails{treeFS: sample(), dir: "b/d"}), `
		seen = []
		r = try { for p in io.walk { seen.push(p) } } else (e) -> e["kind"] + ": " + e["message"]
		seen.join(" ") + " | " + r`)
	if got != `a.txt b/ b/c.mzs b/d/ | io: io.walk "b/d": cannot list` {
		t.Errorf("a failed list = %s", got)
	}
	// Every Lstat and List is a filesystem operation, charged as one.
	o := treeOpts(sample())
	o.StepBudget = 3 * ioStepCost
	_, err := evalIO(t, o, `io.walk.array`)
	if !errors.Is(err, ErrBudget) {
		t.Errorf("a walk under a small budget: err = %v", err)
	}
	// One directory's entries are one collection.
	o = treeOpts(sample())
	o.MaxCollection = 3
	_, err = evalIO(t, o, `io.walk.array`)
	if err == nil {
		t.Error("a directory larger than MaxCollection was listed")
	}
}

// listFails is a tree whose List fails for one directory.
type listFails struct {
	*treeFS
	dir string
}

func (l *listFails) List(dir string) ([]string, error) {
	if dir == l.dir {
		return nil, errors.New("cannot list")
	}
	return l.treeFS.List(dir)
}

func TestIORm(t *testing.T) {
	t.Parallel()

	fs := sample()
	o := treeOpts(fs)
	if got := mustEvalIO(t, o, `[io.rm("a.txt"), io.rm("a.txt"), io.exists("a.txt")].json`); got != "[true,false,false]" {
		t.Errorf("rm twice = %s", got)
	}
	// A link is removed, not what it names.
	if got := mustEvalIO(t, o, `[io.rm("b/up"), io.exists("b/c.mzs")].json`); got != "[true,true]" {
		t.Errorf("rm of a link = %s", got)
	}
	// Not recursive: a directory with something in it stays, and says why.
	if got := mustEvalIO(t, o, `try io.rm("b") else (e) -> e["kind"] + ": " + e["message"]`); got != `io: io.rm "b": directory not empty` {
		t.Errorf("rm of a full directory = %s", got)
	}
	// A tree is removed by walking it, deepest first.
	got := mustEvalIO(t, o, `
		for p in io.walk("b").array.reverse { io.rm(p) }
		[io.rm("b"), io.exists("b")].json`)
	if got != "[true,false]" {
		t.Errorf("rm of a walked tree = %s", got)
	}
}

func TestIOMkdirAndMv(t *testing.T) {
	t.Parallel()

	fs := sample()
	o := treeOpts(fs)
	got := mustEvalIO(t, o, `
		r = io.mkdir("out/deep")
		again = io.mkdir("out/deep")
		io.write("out/deep/x.txt", "x")
		io.mv("out/deep/x.txt", "out/y.txt")
		[r, again, io.exists("out/deep/x.txt"), io.read("out/y.txt"), io.stat("out/deep")["dir"]].json`)
	if got != `[null,null,false,"x",true]` {
		t.Errorf("mkdir and mv = %s", got)
	}
	if got := mustEvalIO(t, o, `try io.mkdir("a.txt") else (e) -> e["message"]`); got != `io.mkdir "a.txt": not a directory` {
		t.Errorf("mkdir over a file = %s", got)
	}
	if got := mustEvalIO(t, o, `try io.mv("nope", "x") else (e) -> e["message"]`); !strings.HasPrefix(got, `io.mv "nope" "x": rename nope`) {
		t.Errorf("mv of nothing = %s", got)
	}
	// The names reach the host as the script wrote them.
	fs.asked = nil
	mustEvalIO(t, o, `io.mkdir("../up/./x"); try io.mv("../a", "./b") else nil`)
	var mine []string
	for _, a := range fs.asked {
		if strings.HasPrefix(a, "mkdir") || strings.HasPrefix(a, "mv") {
			mine = append(mine, a)
		}
	}
	sort.Strings(mine)
	if strings.Join(mine, ",") != "mkdir ../up/./x,mv ../a ./b" {
		t.Errorf("asked = %v", mine)
	}
}

func TestIOStructuralArguments(t *testing.T) {
	t.Parallel()

	o := treeOpts(sample())
	for _, src := range []string{`io.rm(1)`, `io.mkdir(nil)`, `io.mv("a", 2)`, `io.stat([])`, `io.walk(3)`} {
		if _, err := evalIO(t, o, src); err == nil || !strings.Contains(err.Error(), "type") {
			t.Errorf("%s: err = %v; want a type error", src, err)
		}
	}
}

// A program compiled where io.rm was granted and run where it is not reaches the member at
// run time, and is told the same thing the compile pass would have said.
func TestIOClosedMemberAtRunTime(t *testing.T) {
	t.Parallel()

	prog, err := New(treeOpts(sample())).Compile("t", "include io\nio.rm(\"a.txt\")")
	if err != nil {
		t.Fatal(err)
	}
	fs := sample()
	_, err = New(Options{Timeout: 5 * time.Second, FS: fs}).Run(context.Background(), prog, nil)
	if err == nil || !strings.Contains(err.Error(), "io.rm needs the host to allow structural changes: Options.FSMutations is off") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := fs.files["a.txt"]; !ok {
		t.Fatal("the file was removed")
	}
}

// With consent but no interface, a member reached around the gate still cannot act.
func TestIOMutatorWithoutTheInterface(t *testing.T) {
	t.Parallel()

	full := treeOpts(sample())
	mod, _ := LookupModule("io", &full)
	in := New(Options{Timeout: 5 * time.Second, FS: newMemFS(nil), FSMutations: true})
	in.RegisterModule("files", map[string]Value{"mkdir": mod.Get(Str("mkdir")), "mv": mod.Get(Str("mv"))})
	for _, src := range []string{`files.mkdir("x")`, `files.mv("a", "b")`} {
		_, err := in.Eval(context.Background(), "include files\n"+src, nil)
		if err == nil || !strings.Contains(err.Error(), "Options.FS cannot do this") {
			t.Errorf("%s: err = %v", src, err)
		}
	}
}

// Every host failure is a catchable io error naming the path.
func TestIOStructuralHostFailures(t *testing.T) {
	t.Parallel()

	fs := sample()
	fs.fail = errors.New("disk on fire")
	o := treeOpts(fs)
	for src, want := range map[string]string{
		`io.mkdir("x")`:      `io: io.mkdir "x": disk on fire`,
		`io.mv("a", "b")`:    `io: io.mv "a" "b": disk on fire`,
		`io.rm("a.txt")`:     `io: io.rm "a.txt": disk on fire`,
		`io.walk.array`:      `io: io.walk ".": disk on fire`,
		`io.walk("b").array`: `io: io.walk "b": disk on fire`,
	} {
		got := mustEvalIO(t, o, fmt.Sprintf(`try %s else (e) -> e["kind"] + ": " + e["message"]`, src))
		if got != want {
			t.Errorf("%s = %s, want %s", src, got, want)
		}
	}
	// An entry the host cannot look at fails the pull that reached it, after the ones before.
	got := mustEvalIO(t, treeOpts(&lstatFails{treeFS: sample(), name: "b"}), `
		seen = []
		r = try { for p in io.walk { seen.push(p) } } else (e) -> e["message"]
		seen.join(" ") + " | " + r`)
	if got != `a.txt | io.walk "b": cannot look` {
		t.Errorf("a failed Lstat = %s", got)
	}
	// io.mv checks its second argument too.
	if _, err := evalIO(t, o, `io.mv("a", nil)`); err == nil || !strings.Contains(err.Error(), "type") {
		t.Errorf("mv to nil: %v", err)
	}
}

// lstatFails is a tree whose Lstat fails for one name.
type lstatFails struct {
	*treeFS
	name string
}

func (l *lstatFails) Lstat(name string) (bool, int64, bool, bool, error) {
	if name == l.name {
		return false, 0, false, false, errors.New("cannot look")
	}
	return l.treeFS.Lstat(name)
}

// A List that answers names that are not entries — a way back into the directory or above
// it — does not send the walk round in a circle.
func TestIOWalkIgnoresNamesThatAreNotEntries(t *testing.T) {
	t.Parallel()

	fs := sample()
	fs.dirs["b/d"] = append(fs.dirs["b/d"], "", ".", "..", "x/y")
	if got := mustEvalIO(t, treeOpts(fs), `io.walk("b/d").array.join(" ")`); got != "b/d/e.mzs" {
		t.Errorf("walk = %s", got)
	}
}

// The compile-time report of a withheld member is for a name that is only ever the
// built-in module. One the unit rebinds anywhere — an assignment, or a later include of a
// script module — may hold the member by the time the line runs, and the check waits for
// run time, where it says the same thing if the member really is missing.
func TestIOClosedMemberOfAReboundName(t *testing.T) {
	t.Parallel()

	o := Options{Timeout: 5 * time.Second, FS: sample()}
	if got := mustEvalIO(t, o, `io = {rm: 1}; io.rm`); got != "1" {
		t.Errorf("a reassigned io = %s", got)
	}
	o.ModuleLoader = func(from, path string) (string, string, error) {
		return path, `export fn rm(p) { "stub " + p }`, nil
	}
	got := mustEvalIO(t, o, "fn probe() { io.rm(\"x\") }\ninclude io from \"./stub.mzs\"\nprobe()")
	if got != "stub x" {
		t.Errorf("a function reading the later include = %s", got)
	}
	// Called before the replacement, the same function meets the built-in module, and
	// the run-time check says what the compile pass would have.
	_, err := evalIO(t, o, "fn probe() { io.rm(\"x\") }\nprobe()\ninclude io from \"./stub.mzs\"")
	if err == nil || !strings.Contains(err.Error(), "io.rm needs the host to allow structural changes") {
		t.Errorf("err = %v", err)
	}
}

// The walk's root is held by the seq for the memory walk, not only by its cursor.
func TestIOWalkHoldsItsRoot(t *testing.T) {
	t.Parallel()

	v, err := evalIO(t, treeOpts(sample()), `io.walk("b///")`)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.p.(*Seq)
	if !ok || len(s.refs) != 1 || s.refs[0].Str() != "b" {
		t.Errorf("refs = %v", s)
	}
}
