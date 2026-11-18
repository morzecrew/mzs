package mzs

import (
	"context"
	"reflect"
	"testing"
)

func profileOf(t *testing.T, o Options, name, src string) (Result, *Profile) {
	t.Helper()
	o.Profile = true
	in := New(o)
	p, err := in.Compile(name, src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.RunResult(context.Background(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile == nil {
		t.Fatal("no profile with Options.Profile set")
	}
	return res, res.Profile
}

func funcNamed(p *Profile, label string) (ProfileFunc, bool) {
	for _, f := range p.Funcs {
		if f.Label() == label {
			return f, true
		}
	}
	return ProfileFunc{}, false
}

// RFC 0019 §4: the profile's steps are the Run's steps, attributed to the function that
// took them, with the calls under it added to its total.
func TestProfileAttributesSteps(t *testing.T) {
	src := "fn score(x) {\n" + // 1
		"  s = 0\n" +
		"  for i in 1..x { s += i }\n" +
		"  s\n" +
		"}\n" +
		"fn load() {\n" + // 6
		"  [1, 2, 3].map { (x) -> score(x * 10) }\n" + // 7
		"}\n" +
		"load().sum\n"
	res, p := profileOf(t, DefaultOptions(), "rank.mzs", src)
	if p.Steps != res.Steps {
		t.Errorf("profile steps %d, Run steps %d", p.Steps, res.Steps)
	}
	var self int64
	for _, f := range p.Funcs {
		self += f.Self
	}
	if self != p.Steps {
		t.Errorf("the functions' own steps add to %d, want %d", self, p.Steps)
	}
	score, ok := funcNamed(p, "score rank.mzs:1")
	if !ok {
		t.Fatalf("no score in %+v", p.Funcs)
	}
	if score.Calls != 3 || score.Self != score.Total || score.Self == 0 {
		t.Errorf("score = %+v, want 3 calls and a total that is its own", score)
	}
	if p.Funcs[0].Name != "score" {
		t.Errorf("first function = %+v, want the most steps of its own first", p.Funcs[0])
	}
	load, _ := funcNamed(p, "load rank.mzs:6")
	closure, _ := funcNamed(p, "{} rank.mzs:7")
	mapf, _ := funcNamed(p, ".map")
	if closure.Total != closure.Self+score.Total || mapf.Total != mapf.Self+closure.Total || load.Total != load.Self+mapf.Total {
		t.Errorf("totals do not nest: load %+v, map %+v, {} %+v, score %+v", load, mapf, closure, score)
	}
	top, _ := funcNamed(p, "(top level) rank.mzs:1")
	if top.Total != p.Steps || top.Calls != 1 {
		t.Errorf("top level = %+v, want every step in its total", top)
	}
	want := []string{"(top level) rank.mzs:1", "load rank.mzs:6", ".map", "{} rank.mzs:7", "score rank.mzs:1"}
	found := false
	for _, s := range p.Stacks {
		if reflect.DeepEqual(s.Frames, want) {
			found = s.Steps == score.Self
		}
	}
	if !found {
		t.Errorf("no stack %v with score's steps in %+v", want, p.Stacks)
	}
}

// The same input gives the same profile: steps are not time.
func TestProfileIsDeterministic(t *testing.T) {
	src := "fn f(n) { if n < 2 { n } else { f(n - 1) + f(n - 2) } }\nf(12)"
	_, a := profileOf(t, DefaultOptions(), "fib.mzs", src)
	_, b := profileOf(t, DefaultOptions(), "fib.mzs", src)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two runs differ:\n%+v\n%+v", a, b)
	}
	// A recursive function's total counts each step once, however deep it recursed.
	f, _ := funcNamed(a, "f fib.mzs:1")
	top, _ := funcNamed(a, "(top level) fib.mzs:1")
	if f.Total > top.Total || f.Total < f.Self {
		t.Errorf("f = %+v against top level %+v", f, top)
	}
	if f.Calls != 465 {
		t.Errorf("f calls = %d, want 465 for fib(12)", f.Calls)
	}
}

// A module's statements are an (include) frame, and a task's steps land under the call
// that started it.
func TestProfileModulesAndTasks(t *testing.T) {
	src := &modSource{files: map[string]string{"lib": "x = [1, 2, 3].sum\nexport fn twice(v) { v * 2 }\n"}}
	o := DefaultOptions()
	o.ModuleLoader = src.loader()
	_, p := profileOf(t, o, "main.mzs", "include lib from \"lib\"\nasync fn job() { lib.twice(4) }\nt = job()\nt.await")
	inc, ok := funcNamed(p, "(include) lib:1")
	if !ok || inc.Self == 0 || inc.Calls != 1 {
		t.Errorf("include frame = %+v (found %v)", inc, ok)
	}
	job, ok := funcNamed(p, "job main.mzs:2")
	twice, _ := funcNamed(p, "twice lib:2")
	if !ok || job.Total < twice.Total || twice.Calls != 1 {
		t.Errorf("job = %+v, twice = %+v", job, twice)
	}
}

// Without Options.Profile there is no profile.
func TestProfileIsOptional(t *testing.T) {
	in := New(DefaultOptions())
	p, err := in.Compile("x.mzs", "1 + 1")
	if err != nil {
		t.Fatal(err)
	}
	res, err := in.RunResult(context.Background(), p, nil)
	if err != nil || res.Profile != nil {
		t.Errorf("profile = %+v, err = %v", res.Profile, err)
	}
	if (ProfileFunc{Name: ".len"}).Label() != ".len" {
		t.Error("a builtin's label is its name")
	}
}
