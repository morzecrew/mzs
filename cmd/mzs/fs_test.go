package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RFC 0015 on the CLI's own filesystem: the real os calls behind io.walk, io.rm,
// io.mkdir and io.mv, real symbolic links, and --read-only.

// tree lays out a directory with a file, a subdirectory, a link to a directory and a
// link that makes a cycle, and returns its root.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "src", "lib"), 0o755))
	must(os.WriteFile(filepath.Join(root, "src", "a.mzs"), []byte("a"), 0o644))
	must(os.WriteFile(filepath.Join(root, "src", "lib", "b.mzs"), []byte("b"), 0o644))
	must(os.WriteFile(filepath.Join(root, "src", "z.txt"), []byte("z"), 0o644))
	if err := os.Symlink("..", filepath.Join(root, "src", "lib", "up")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	must(os.Symlink("src", filepath.Join(root, "lnk")))
	return root
}

func TestCLIWalkDoesNotFollowLinks(t *testing.T) {
	t.Parallel()

	root := tree(t)
	src := `include io; include path
r = $root
io.walk(r).map { path.rel(it, r) }.array.join(" ")`
	code, out, errOut := exec([]string{"-v", "root=" + root, "-e", src}, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// The cycle src/lib/up -> .. is a name, and lnk -> src is not entered either. `rel`
	// cleans, so a directory's trailing `/` is not in these.
	if got := strings.TrimSpace(out); got != "lnk src src/a.mzs src/lib src/lib/b.mzs src/lib/up src/z.txt" {
		t.Errorf("walk = %q", got)
	}
	// The yielded names themselves keep the `/` of a directory.
	code, out, errOut = exec([]string{"-v", "root=" + root, "-e", `include io; io.walk($root).filter { it.ends_with("/") }.array.len`}, "")
	if code != 0 || strings.TrimSpace(out) != "2" {
		t.Errorf("directories = %q (exit %d: %s)", out, code, errOut)
	}
	// A root that is a link is refused, trailing `/` or not.
	for _, r := range []string{"lnk", "lnk/"} {
		code, _, errOut = exec([]string{"-v", "root=" + filepath.Join(root, r), "-e", `include io; io.walk($root).array`}, "")
		if code == 0 || !strings.Contains(errOut, "is a symbolic link") {
			t.Errorf("walk(%q): exit %d, %s", r, code, errOut)
		}
	}
}

func TestCLIStructuralChanges(t *testing.T) {
	t.Parallel()

	root := tree(t)
	src := `include io; include path
r = $root
io.mkdir(path.join(r, "out/deep"))
io.write(path.join(r, "out/deep/x.txt"), "x")
io.mv(path.join(r, "out/deep/x.txt"), path.join(r, "out/y.txt"))
a = io.rm(path.join(r, "out/y.txt"))
b = io.rm(path.join(r, "out/y.txt"))
c = try io.rm(path.join(r, "out")) else (e) -> e["kind"]
d = io.rm(path.join(r, "lnk"))
[a, b, c, d, io.exists(path.join(r, "src/a.mzs"))].json`
	code, out, errOut := exec([]string{"-v", "root=" + root, "-e", src}, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := strings.TrimSpace(out); got != `[true,false,"io",true,true]` {
		t.Errorf("result = %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "deep")); err != nil {
		t.Errorf("mkdir did not create out/deep: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "lnk")); !os.IsNotExist(err) {
		t.Errorf("rm of the link left it: %v", err)
	}
}

func TestCLIReadOnly(t *testing.T) {
	t.Parallel()

	root := tree(t)
	for _, src := range []string{`io.rm($root)`, `io.mkdir($root)`, `io.mv($root, $root)`} {
		code, _, errOut := exec([]string{"--read-only", "-v", "root=" + root, "-e", "include io\n" + src}, "")
		if code == 0 || !strings.Contains(errOut, "needs the host to allow structural changes") ||
			!strings.Contains(errOut, "--read-only") {
			t.Errorf("%s under --read-only: exit %d, %s", src, code, errOut)
		}
	}
	target := filepath.Join(root, "new.txt")
	for _, src := range []string{`io.write($p, "x")`, `io.append($p, "x")`} {
		code, _, errOut := exec([]string{"--read-only", "-v", "p=" + target, "-e", "include io\n" + src}, "")
		if code == 0 || !strings.Contains(errOut, "the filesystem is read-only (mzs --read-only)") {
			t.Errorf("%s under --read-only: exit %d, %s", src, code, errOut)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("a read-only write created the file: %v", err)
	}
	// Reading is untouched, walk included.
	code, out, errOut := exec([]string{"--read-only", "-v", "root=" + root, "-e",
		`include io; [io.read($root + "/src/a.mzs"), io.walk($root).array.len, io.stat($root)["dir"]].json`}, "")
	if code != 0 || strings.TrimSpace(out) != `["a",7,true]` {
		t.Errorf("reads under --read-only = %q (exit %d: %s)", out, code, errOut)
	}
	// mzs test takes the flag too.
	file := filepath.Join(root, "ro_test.mzs")
	if err := os.WriteFile(file, []byte("include io\ninclude test\ntest.case(\"w\") { test.raises(\"io\") { io.write(\"/nonexistent/x\", \"x\") } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = exec([]string{"test", "--read-only", file}, "")
	if code != 0 {
		t.Errorf("mzs test --read-only: exit %d: %s %s", code, out, errOut)
	}
}

// "Cannot tell" is not "not there": a name the OS refuses to look at is an error.
func TestCLILstatFailureIsAnError(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 300)
	code, _, errOut := exec([]string{"-v", "p=" + long, "-e", `include io; io.walk($p).array`}, "")
	if code == 0 || !strings.Contains(errOut, "io.walk") || strings.Contains(errOut, "no such directory") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}
