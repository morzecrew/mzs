package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLib writes one fixture file, making its directory first. Every test below is a
// small tree of libraries, and the tree is the interesting part of the fixture.
func writeLib(t *testing.T, path, src string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// setLibPath points the search path at dirs for one test. It also clears the home
// directory the default would come from, so a test never reads the machine's own
// ~/.mzs/lib — MZSPATH is set here, which already means the default is not consulted, but
// a test that stops setting it must not quietly start passing because of a file on the
// developer's disk.
func setLibPath(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv(libPathEnv, strings.Join(dirs, string(os.PathListSeparator)))
}

// A library name — a path with no `./` — is looked for on the search path, in the two
// layouts a library can have: one file, or a directory entered through main.mzs (§12.8,
// §15). These tests set an environment variable, so none of them is parallel.
func TestCLILibraryLayouts(t *testing.T) {
	lib := t.TempDir()
	writeLib(t, filepath.Join(lib, "text.mzs"), "export fn shout(s) { s.upper + \"!\" }\n")
	writeLib(t, filepath.Join(lib, "money", "main.mzs"),
		"include cents from \"./cents.mzs\"\nexport fn show(c) { cents.two(c) + \" EUR\" }\n")
	writeLib(t, filepath.Join(lib, "money", "cents.mzs"),
		"export fn two(c) { \"${c / 100}.${(c % 100).str.rjust(2, \"0\")}\" }\n")
	writeLib(t, filepath.Join(lib, "shop", "cart.mzs"),
		"export fn label() { \"cart\" }\n")
	writeLib(t, filepath.Join(lib, "till.mzs"),
		"include money from \"money\"\nexport fn receipt(c) { \"total: \" + money.show(c) }\n")

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "one file is a library",
			src:  `include text from "text"; text.shout("hi")`,
			want: "HI!\n",
		},
		{
			name: "a directory is entered through main.mzs",
			src:  `include money from "money"; money.show(1550)`,
			want: "15.50 EUR\n",
		},
		{
			name: "a library includes its own neighbours",
			src:  `include money from "money"; money.keys.join(",")`,
			want: "show\n",
		},
		{
			name: "a name may carry a subdirectory",
			src:  `include cart from "shop/cart"; cart.label()`,
			want: "cart\n",
		},
		{
			name: "a library includes another library by name",
			src:  `include till from "till"; till.receipt(500)`,
			want: "total: 5.00 EUR\n",
		},
		{
			name: "one library loads once, however it is reached",
			src:  `include a from "text"; include b from "text"; a == b`,
			want: "true\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLibPath(t, lib)
			code, out, errOut := exec([]string{"-e", tt.src}, "")
			if code != exitOK {
				t.Fatalf("exit = %d (stdout %q, stderr %q)", code, out, errOut)
			}
			if out != tt.want {
				t.Errorf("stdout = %q; want %q", out, tt.want)
			}
		})
	}
}

// The search path is read in order, and it is $MZSPATH or the default, never both: a
// search path that kept a directory the user took out of it is one nobody can predict.
func TestCLILibrarySearchOrder(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeLib(t, filepath.Join(first, "greet.mzs"), `export fn who() { "first" }`+"\n")
	writeLib(t, filepath.Join(second, "greet.mzs"), `export fn who() { "second" }`+"\n")
	writeLib(t, filepath.Join(second, "only.mzs"), `export fn who() { "only in the second" }`+"\n")

	t.Run("the first directory holding the name wins", func(t *testing.T) {
		setLibPath(t, first, second)
		code, out, errOut := exec([]string{"-e", `include g from "greet"; g.who()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if out != "first\n" {
			t.Errorf("stdout = %q; want the first directory's answer", out)
		}
	})

	t.Run("the later directories are still searched", func(t *testing.T) {
		setLibPath(t, first, second)
		code, out, errOut := exec([]string{"-e", `include o from "only"; o.who()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if out != "only in the second\n" {
			t.Errorf("stdout = %q; want the second directory's answer", out)
		}
	})

	// Without MZSPATH the path is ~/.mzs/lib, which is what makes a library installed
	// once work with nothing exported in the shell.
	t.Run("the default is ~/.mzs/lib", func(t *testing.T) {
		home := t.TempDir()
		writeLib(t, filepath.Join(home, ".mzs", "lib", "greet.mzs"), `export fn who() { "home" }`+"\n")
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv(libPathEnv, "")

		code, out, errOut := exec([]string{"-e", `include g from "greet"; g.who()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if out != "home\n" {
			t.Errorf("stdout = %q; want the home library's answer", out)
		}
	})

	// …and with MZSPATH set, the home library is not searched as well. Somebody who
	// wants both writes both.
	t.Run("MZSPATH replaces the default rather than extending it", func(t *testing.T) {
		home := t.TempDir()
		writeLib(t, filepath.Join(home, ".mzs", "lib", "home_only.mzs"), `export fn who() { "home" }`+"\n")
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv(libPathEnv, first)

		code, _, errOut := exec([]string{"-e", `include g from "home_only"; g.who()`}, "")
		if code == exitOK {
			t.Fatal("the home library was searched even though MZSPATH was set")
		}
		if !strings.Contains(errOut, "no such library") {
			t.Errorf("stderr = %q; want the not-found diagnostic", errOut)
		}
	})

	// With no MZSPATH and no home directory to build the default from, the search path is
	// empty — and the diagnostic says so rather than listing nowhere.
	t.Run("an empty search path says so", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		t.Setenv(libPathEnv, "")

		code, _, errOut := exec([]string{"-e", `include g from "greet"`}, "")
		if code == exitOK {
			t.Fatal("a library was found with no search path to find it on")
		}
		if !strings.Contains(errOut, "the search path is empty") ||
			!strings.Contains(errOut, libPathEnv) {
			t.Errorf("stderr = %q; want the empty-search-path diagnostic", errOut)
		}
	})

	// An empty entry is dropped rather than read as ".": a trailing separator must not
	// quietly make the working directory a library directory.
	t.Run("an empty entry is not the working directory", func(t *testing.T) {
		dir := t.TempDir()
		writeLib(t, filepath.Join(dir, "here.mzs"), `export fn who() { "here" }`+"\n")
		t.Chdir(dir)
		setLibPath(t, first, "")

		code, _, errOut := exec([]string{"-e", `include h from "here"`}, "")
		if code == exitOK {
			t.Fatal("an empty MZSPATH entry made the working directory a library directory")
		}
		if !strings.Contains(errOut, "no such library") {
			t.Errorf("stderr = %q; want the not-found diagnostic", errOut)
		}
	})

	// A directory on the path that is not there is not an error: a search path is a list
	// of places a library may live, and naming one that does not exist yet is ordinary.
	t.Run("a search directory that is not there is skipped", func(t *testing.T) {
		setLibPath(t, filepath.Join(t.TempDir(), "not-created"), second)

		code, out, errOut := exec([]string{"-e", `include o from "only"; o.who()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if out != "only in the second\n" {
			t.Errorf("stdout = %q; want the second directory's answer", out)
		}
	})

	// A relative entry is resolved against the working directory, which is where the
	// person typing it means, and is absolute by the time the root check compares it.
	t.Run("a relative entry resolves against the working directory", func(t *testing.T) {
		project := t.TempDir()
		writeLib(t, filepath.Join(project, "vendor", "util.mzs"), `export fn who() { "vendored" }`+"\n")
		t.Chdir(project)
		setLibPath(t, "vendor")

		code, out, errOut := exec([]string{"-e", `include u from "util"; u.who()`}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if out != "vendored\n" {
			t.Errorf("stdout = %q; want the vendored library's answer", out)
		}
	})
}

// The root policy (§14.3) survives the search path: it is now one root per directory tree
// rather than one for the program, and a library is rooted where it was found.
func TestCLILibraryRootIsEnforced(t *testing.T) {
	lib := t.TempDir()
	project := t.TempDir()
	secret := writeLib(t, filepath.Join(project, "secret.mzs"), `export fn s() { "secret" }`+"\n")

	rel, err := filepath.Rel(lib, secret)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the fixture really does climb out of the library directory, so the refusal
	// below is the policy's doing and not a missing file's.
	if !strings.HasPrefix(rel, "..") {
		t.Fatalf("the fixture path %q does not leave the library directory", rel)
	}
	writeLib(t, filepath.Join(lib, "nosy.mzs"),
		`include x from "`+filepath.ToSlash(rel)+`"`+"\nexport fn peek() { x.s() }\n")

	t.Run("a library cannot climb out of its own directory", func(t *testing.T) {
		setLibPath(t, lib)
		code, _, errOut := exec([]string{"-e", `include n from "nosy"; n.peek()`}, "")
		if code == exitOK {
			t.Fatal("a library read a file above its search directory")
		}
		if !strings.Contains(errOut, "outside the root directory") {
			t.Errorf("stderr = %q; want the root-policy diagnostic", errOut)
		}
		if !strings.Contains(errOut, lib) {
			t.Errorf("stderr = %q; want the library's own directory named as the root", errOut)
		}
	})

	// The library directory sitting inside the project is the ordinary vendored layout,
	// and the deepest root still wins: a library is confined to itself, not to the
	// project that happens to hold it.
	t.Run("a library inside the project is rooted in the library directory", func(t *testing.T) {
		vendor := filepath.Join(project, "vendor")
		writeLib(t, filepath.Join(vendor, "nosy.mzs"),
			"include x from \"../secret.mzs\"\nexport fn peek() { x.s() }\n")
		main := writeLib(t, filepath.Join(project, "main.mzs"),
			"include n from \"nosy\"\nprintln(n.peek())\n")
		setLibPath(t, vendor)

		code, _, errOut := exec([]string{main}, "")
		if code == exitOK {
			t.Fatal("a vendored library reached a file of the project above it")
		}
		if !strings.Contains(errOut, "outside the root directory "+vendor) {
			t.Errorf("stderr = %q; want %s named as the root", errOut, vendor)
		}
	})

	// The script's own root is unchanged: `./` still means the file next to this one,
	// and still cannot climb.
	t.Run("the script's own root is unchanged", func(t *testing.T) {
		setLibPath(t, lib)
		escaping := writeLib(t, filepath.Join(project, "escape.mzs"),
			"include x from \"../etc/passwd\"\n")
		code, _, errOut := exec([]string{escaping}, "")
		if code == exitOK {
			t.Fatal("a script climbed out of its own root")
		}
		if !strings.Contains(errOut, "outside the root directory") {
			t.Errorf("stderr = %q; want the root-policy diagnostic", errOut)
		}
	})
}

// A path may not leave the tree it belongs to, and a symbolic link is a path: the check
// is made by the read itself (os.Root), not by the spelling, so a link that is lexically
// inside the root and leads out of it is refused like any other way out. A link that
// stays inside is followed, which is what keeps an ordinary alias working.
func TestCLIIncludeFollowsNoLinkOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	writeLib(t, filepath.Join(outside, "secret.mzs"), `export fn s() { "leaked" }`+"\n")

	t.Run("a script's link out of its root is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(outside, "secret.mzs"), filepath.Join(dir, "link.mzs")); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		main := writeLib(t, filepath.Join(dir, "main.mzs"),
			"include x from \"./link.mzs\"\nprintln(x.s())\n")
		setLibPath(t, t.TempDir())

		code, out, errOut := exec([]string{main}, "")
		if code == exitOK {
			t.Fatalf("an include followed a link out of its root (stdout %q)", out)
		}
		if strings.Contains(out, "leaked") {
			t.Errorf("stdout = %q; the file outside the root was read", out)
		}
		if !strings.Contains(errOut, "outside the root directory") {
			t.Errorf("stderr = %q; want the root-policy diagnostic", errOut)
		}
	})

	t.Run("a link inside the root is followed", func(t *testing.T) {
		dir := t.TempDir()
		writeLib(t, filepath.Join(dir, "real.mzs"), `export fn s() { "inside" }`+"\n")
		if err := os.Symlink("real.mzs", filepath.Join(dir, "alias.mzs")); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		main := writeLib(t, filepath.Join(dir, "main.mzs"),
			"include x from \"./alias.mzs\"\nprintln(x.s())\n")
		setLibPath(t, t.TempDir())

		code, out, errOut := exec([]string{main}, "")
		if code != exitOK {
			t.Fatalf("exit = %d (stderr %q)", code, errOut)
		}
		if !strings.Contains(out, "inside") {
			t.Errorf("stdout = %q; want the aliased module's answer", out)
		}
	})

	// A library that is a link out of its search directory is *reported*, not passed
	// over: the name was claimed here, and a same-named library further down the path
	// would answer for a file this one was refused.
	t.Run("a library that links out of its directory is refused, not skipped", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		if err := os.Symlink(filepath.Join(outside, "secret.mzs"), filepath.Join(first, "secret.mzs")); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}
		writeLib(t, filepath.Join(second, "secret.mzs"), `export fn s() { "the second directory" }`+"\n")
		setLibPath(t, first, second)

		code, out, errOut := exec([]string{"-e", `include s from "secret"; s.s()`}, "")
		if code == exitOK {
			t.Fatalf("an escaping library link was followed or skipped (stdout %q)", out)
		}
		if strings.Contains(out, "the second directory") {
			t.Errorf("stdout = %q; the search moved on from a name that was already claimed", out)
		}
		if !strings.Contains(errOut, "outside the root directory "+first) {
			t.Errorf("stderr = %q; want %s named as the root", errOut, first)
		}
	})
}

// A library that is there and cannot be read is an error, not an absence: searching on
// would answer with a different library under a name this directory already claimed, and
// the first-directory-wins rule is the whole of the search contract.
func TestCLILibraryUnreadableIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode; the refusal under test cannot happen")
	}
	first, second := t.TempDir(), t.TempDir()
	unreadable := writeLib(t, filepath.Join(first, "greet.mzs"), `export fn who() { "first" }`+"\n")
	writeLib(t, filepath.Join(second, "greet.mzs"), `export fn who() { "second" }`+"\n")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	setLibPath(t, first, second)

	code, out, errOut := exec([]string{"-e", `include g from "greet"; g.who()`}, "")
	if code == exitOK {
		t.Fatalf("an unreadable library was passed over (stdout %q)", out)
	}
	if strings.Contains(out, "second") {
		t.Errorf("stdout = %q; the search moved on from a name that was already claimed", out)
	}
	if !strings.Contains(errOut, "permission denied") || !strings.Contains(errOut, unreadable) {
		t.Errorf("stderr = %q; want the read failure, naming the file", errOut)
	}
}

// Each refusal names the spelling that was meant. A path is one of two things, and every
// message below says which one the writer was reaching for.
func TestCLILibraryDiagnostics(t *testing.T) {
	lib := t.TempDir()
	writeLib(t, filepath.Join(lib, "text.mzs"), "export fn shout(s) { s.upper }\n")

	tests := []struct {
		name   string
		src    string
		errHas []string
	}{
		{
			name:   "a missing library says where it looked",
			src:    `include nope from "nope"`,
			errHas: []string{"no such library", "nope.mzs", filepath.Join("nope", "main.mzs"), lib},
		},
		{
			name:   "a library name carries no extension",
			src:    `include text from "text.mzs"`,
			errHas: []string{"a library name carries no .mzs", `"text"`, `"./text.mzs"`},
		},
		{
			name:   "a library name cannot climb",
			src:    `include x from "a/../../b"`,
			errHas: []string{"cannot contain '..'", `"./a/../../b"`},
		},
		{
			name:   "an absolute path is still refused",
			src:    `include x from "/etc/hostname"`,
			errHas: []string{"an include path must be relative"},
		},
		{
			name:   "an empty path is refused",
			src:    `include x from ""`,
			errHas: []string{"an include path cannot be empty"},
		},
		{
			name:   "a bare name is not looked for next to the script",
			src:    `include self from "self"`,
			errHas: []string{"no such library"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLibPath(t, lib)
			// The neighbour the bare-name case must not find: `self` exists next to the
			// program, and only the `./` form may reach it.
			dir := t.TempDir()
			writeLib(t, filepath.Join(dir, "self.mzs"), "export fn f() { 1 }\n")
			t.Chdir(dir)

			code, out, errOut := exec([]string{"-e", tt.src}, "")
			if code != exitError {
				t.Fatalf("exit = %d (stdout %q, stderr %q); want %d", code, out, errOut, exitError)
			}
			for _, want := range tt.errHas {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q; want it to contain %q", errOut, want)
				}
			}
		})
	}
}

// `mzs test` runs its files through the same loader, so a test file may include the
// library it is testing by name — which is the layout a library of one's own has: the
// tests live beside it and the name is the same one its users write.
func TestCLILibraryFromTestFiles(t *testing.T) {
	lib := t.TempDir()
	writeLib(t, filepath.Join(lib, "money.mzs"), "export fn double(x) { x * 2 }\n")

	dir := t.TempDir()
	writeLib(t, filepath.Join(dir, "money_test.mzs"),
		"include test\ninclude money from \"money\"\ntest.case(\"double\") { test.eq(money.double(21), 42) }\n")

	setLibPath(t, lib)
	code, out, errOut := exec([]string{"test", dir}, "")
	if code != exitOK {
		t.Fatalf("exit = %d (stdout %q, stderr %q)", code, out, errOut)
	}
	if !strings.Contains(out, "0 failed") {
		t.Errorf("stdout = %q; want a passing report", out)
	}
}

// The loader is also reachable without a program around it, and one branch is only
// reachable that way: a file that belongs to none of the known roots. The CLI never hands
// it one — a script's root *is* its directory — so this pins the fallback that decides
// what happens if it ever does, which is that the narrowest root wins and the read is
// refused.
func TestFileLoaderRootsAnUnknownFileNarrowly(t *testing.T) {
	root, lib := t.TempDir(), t.TempDir()
	writeLib(t, filepath.Join(root, "here.mzs"), "export fn f() { 1 }\n")

	stranger := filepath.Join(t.TempDir(), "stranger.mzs")
	writeLib(t, stranger, "export fn f() { 2 }\n")

	load := fileLoader(root, []string{lib})
	if _, _, err := load(stranger, "./neighbour.mzs"); err == nil {
		t.Fatal("a file under no root reached its own neighbour")
	} else if !strings.Contains(err.Error(), "outside the root directory "+root) {
		t.Errorf("err = %v; want the script root named", err)
	}

	// …and the script's own root still resolves from it, which is what makes the fallback
	// the narrow answer rather than a refusal of everything.
	if resolved, src, err := load(filepath.Join(root, "main.mzs"), "./here.mzs"); err != nil {
		t.Errorf("err = %v; want the neighbour under the root to load", err)
	} else if src == "" || resolved != filepath.Join(root, "here.mzs") {
		t.Errorf("resolved = %q, src = %q; want the file under the root", resolved, src)
	}
}
