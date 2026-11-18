package mzs

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"
	"weak"

	"mzs/internal/ast"
)

// RFC 0019 §3: a Coverage counts the statements and branches that ran, file by file,
// and what never ran is counted too.
func TestCoverageCountsStatementsAndBranches(t *testing.T) {
	lib := "export fn grade(n) {\n" + // 1
		"  if n > 5 {\n" + // 2
		"    \"high\"\n" + // 3
		"  } else {\n" +
		"    \"low\"\n" + // 5
		"  }\n" +
		"}\n" +
		"export fn kind(x) {\n" + // 8
		"  match x {\n" + // 9
		"    1 -> \"one\"\n" + // 10
		"    else -> \"other\"\n" + // 11
		"  }\n" +
		"}\n" +
		"fn unused() {\n" + // 14
		"  1\n" + // 15
		"}\n"
	main := "include lib from \"lib\"\n" + // 1
		"a = lib.grade(9)\n" + // 2
		"b = lib.kind(1)\n" + // 3
		"c = nil ?? 2\n" + // 4
		"d = true ? 1 : 2\n" + // 5
		"[a, b, c, d]\n" // 6
	cov := NewCoverage()
	src := &modSource{files: map[string]string{"lib": lib}}
	o := DefaultOptions()
	o.ModuleLoader = src.loader()
	o.Coverage = cov
	in := New(o)
	p, err := in.Compile("main.mzs", main)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Run(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}

	files := cov.Files()
	if len(files) != 2 || files[0].File != "lib" || files[1].File != "main.mzs" {
		t.Fatalf("files = %+v", files)
	}
	l, m := files[0], files[1]
	// lib: the two fn declarations, the if, "high", "low", the match, two arm bodies,
	// the unused fn and its body.
	if l.Statements != 10 || l.StatementsRun != 7 {
		t.Errorf("lib statements %d/%d, want 7/10", l.StatementsRun, l.Statements)
	}
	if got, want := l.MissedLines(), []int{5, 11, 15}; !reflect.DeepEqual(got, want) {
		t.Errorf("lib missed lines = %v, want %v", got, want)
	}
	// if: then taken, else not; match: arm 0 taken, arm 1 not.
	if l.Branches != 4 || l.BranchesTaken != 2 {
		t.Errorf("lib branches %d/%d, want 2/4", l.BranchesTaken, l.Branches)
	}
	if m.Statements != 6 || m.StatementsRun != 6 || m.Percent() != 100 {
		t.Errorf("main statements %d/%d (%.1f%%), want all 6", m.StatementsRun, m.Statements, m.Percent())
	}
	// `??` took its right side (side 1); the ternary its then (side 0).
	want := []BranchCoverage{
		{Line: 4, Block: 0, Branch: 0, Hits: 0},
		{Line: 4, Block: 0, Branch: 1, Hits: 1},
		{Line: 5, Block: 0, Branch: 0, Hits: 1},
		{Line: 5, Block: 0, Branch: 1, Hits: 0},
	}
	if !reflect.DeepEqual(m.BranchHits, want) {
		t.Errorf("main branches = %+v, want %+v", m.BranchHits, want)
	}

	// A second Run adds to the first.
	if _, err := in.Run(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range cov.Files()[1].Lines {
		if line.Hits != 2 {
			t.Errorf("after two Runs, main line %d hits = %d, want 2", line.Line, line.Hits)
		}
	}
}

// A line is covered only when every statement on it ran.
func TestCoverageLineIsItsLeastRunStatement(t *testing.T) {
	cov := NewCoverage()
	o := DefaultOptions()
	o.Coverage = cov
	in := New(o)
	p, err := in.Compile("m.mzs", "fn f() { 1 }; x = 1; if false { f() }\nx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Run(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	fc := cov.Files()[0]
	if got := fc.MissedLines(); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("missed = %v, want [1]: the body of f and the call never ran", got)
	}
	if fc.Statements != 6 || fc.StatementsRun != 4 {
		t.Errorf("statements %d/%d, want 4/6", fc.StatementsRun, fc.Statements)
	}
}

// Without a Coverage nothing is recorded, and an empty one reports nothing.
func TestCoverageIsOptional(t *testing.T) {
	if got := NewCoverage().Files(); len(got) != 0 {
		t.Errorf("empty coverage = %+v", got)
	}
	if (FileCoverage{}).Percent() != 100 {
		t.Error("a file with no statements is fully covered")
	}
	var zero Coverage // the zero value works too
	o := DefaultOptions()
	o.Coverage = &zero
	in := New(o)
	p, err := in.Compile("z.mzs", "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Run(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	if f := zero.Files(); len(f) != 1 || f[0].StatementsRun != 1 {
		t.Errorf("the zero Coverage recorded %+v", f)
	}
	// A node nothing registered counts nothing, and does not panic.
	zero.hit(&ast.ExprStmt{}, -1)
}

// Different text under one name is two files; the same text compiled twice is one.
func TestCoverageKeepsTextsApart(t *testing.T) {
	cov := NewCoverage()
	o := DefaultOptions()
	o.Coverage = cov
	o.ProgramCache = -1 // compile anew every time, so the same text is two trees
	in := New(o)
	for _, src := range []string{"if true { 1 } else { 2 }", "x = 1\ny = 2\nz = 3", "if true { 1 } else { 2 }"} {
		if _, err := in.Eval(context.Background(), src, nil); err != nil {
			t.Fatal(err)
		}
	}
	files := cov.Files()
	if len(files) != 2 {
		t.Fatalf("files = %+v, want one per text", files)
	}
	for _, f := range files {
		if f.File != "eval" {
			t.Errorf("name = %q", f.File)
		}
		switch f.Branches {
		case 2: // the if, its then and its else: run twice, the else never
			if f.Statements != 3 || f.StatementsRun != 2 || f.BranchesTaken != 1 || f.BranchHits[0].Hits != 2 {
				t.Errorf("the if text = %+v", f)
			}
		case 0: // three assignments, each once
			if f.Statements != 3 || f.StatementsRun != 3 || len(f.MissedLines()) != 0 {
				t.Errorf("the assignments text = %+v", f)
			}
		default:
			t.Errorf("unexpected file %+v", f)
		}
	}
}

// A tree nothing holds any more is collected, and its node slots go with it; the counts
// it made stay with its file.
func TestCoverageReleasesCollectedTrees(t *testing.T) {
	cov := NewCoverage()
	o := DefaultOptions()
	o.Coverage = cov
	o.ProgramCache = -1 // every Eval compiles a tree of its own and drops it
	in := New(o)
	const runs = 50
	for range runs {
		if _, err := in.Eval(context.Background(), "x = 1\ny = 2\nx + y", nil); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for cov.bindings() > 3 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if n := cov.bindings(); n > 3 {
		t.Errorf("%d node slots after %d dropped trees, want at most one tree's 3", n, runs)
	}
	files := cov.Files()
	if len(files) != 1 || files[0].Lines[0].Hits != runs {
		t.Errorf("files = %+v, want one file whose lines ran %d times", files, runs)
	}
}

// A tree's cleanup can run after its address went to a newer tree. The newer tree is
// registered all the same, and the late cleanup leaves its slots alone.
func TestCoverageSurvivesAReusedTreeAddress(t *testing.T) {
	in := New(DefaultOptions())
	p, err := in.Compile("r.mzs", "x = 1\ny = 2")
	if err != nil {
		t.Fatal(err)
	}
	tree := p.tree()
	cov := NewCoverage()
	cov.register("r.mzs", "x = 1\ny = 2", tree)
	if n := cov.bindings(); n != 2 {
		t.Fatalf("%d slots, want 2", n)
	}
	addr := addrOf(tree)

	// What a dead tree at this address would have left behind: an entry whose weak
	// pointer no longer answers this tree, and a cleanup still to come.
	stale := cov.trees[addr]
	other := &ast.Program{}
	cov.trees[addr] = treeReg{weak.Make(other), stale.gen}
	cov.register("r.mzs", "x = 1\ny = 2", tree)
	if cov.trees[addr].gen == stale.gen {
		t.Fatal("a tree at an address an older one held was not registered")
	}
	var keys []slotKey
	for k := range cov.slots {
		keys = append(keys, k)
	}
	cov.release(treeKeys{addr, stale.gen, keys})
	if n := cov.bindings(); n != 2 {
		t.Errorf("the older tree's cleanup removed the newer one's slots: %d left", n)
	}
	if _, ok := cov.trees[addr]; !ok {
		t.Error("the older tree's cleanup unregistered the newer one")
	}
	runtime.KeepAlive(other)
	runtime.KeepAlive(tree)
}
