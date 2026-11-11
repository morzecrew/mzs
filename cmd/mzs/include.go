package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mzs/mzs"
)

// The CLI's answer to `include lib from "…"` (SPEC §12.8, §15). There are two path forms
// and one rule that tells them apart: a path that starts with `./` or `../` is a *file*,
// looked for next to the file doing the including, and anything else is a *library name*,
// looked for on the search path. Neither form can leave the directory tree it was found
// in, which is the whole of the policy — a script may reach its own neighbours and a
// library its own, and nothing above either.
//
// The two forms exist because they answer different questions. `"./cart.mzs"` is a
// program reaching for more of itself: the file travels with the script and is part of
// it. `"money"` is a program naming something it does not carry — a library installed
// once and shared between projects, which is exactly what a relative path cannot express
// without spelling out where the other project happens to sit today.

// libExt is the suffix a library name is given. A library is source, like everything else
// mzs loads, so there is nothing else it could be.
const libExt = ".mzs"

// libEntry is the file a library directory is entered through, so a library that outgrew
// one file keeps its name: `money/main.mzs` is `include money from "money"` just as
// `money.mzs` was, and the neighbours it now includes are its own business.
const libEntry = "main" + libExt

// libPathEnv names the search path. It is read at every run rather than captured once, so
// a shell that exports it after the binary was built is not a special case.
const libPathEnv = "MZSPATH"

// libDirs is the search path a library name is resolved against: $MZSPATH when it is set,
// and `~/.mzs/lib` when it is not. It is the PATH contract, with the same reason behind
// it — a search path that silently kept a directory the user removed from it would be a
// search path nobody can predict. Somebody who wants both writes both:
//
//	MZSPATH=/opt/team/mzs:$HOME/.mzs/lib mzs report.mzs
//
// Empty entries are dropped rather than read as ".": a trailing colon meaning "and the
// working directory too" is a footgun PATH already regrets, and `./` is how a script says
// that here. Relative entries are made absolute against the working directory, because
// the root check below compares absolute paths and a caller may chdir.
func libDirs() []string {
	raw := os.Getenv(libPathEnv)
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		raw = filepath.Join(home, ".mzs", "lib")
	}
	var dirs []string
	for _, d := range filepath.SplitList(raw) {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		dirs = append(dirs, abs)
	}
	return dirs
}

// includeRoot is the directory a script's own includes are confined to: where the script
// lives, or the working directory when the source came from -e or a pipe and has no
// directory of its own.
func includeRoot(cfg *config) string {
	if cfg.file != "" && cfg.file != "-" {
		if dir := filepath.Dir(cfg.file); dir != "" {
			return dir
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// fileLoader builds the mzs.ModuleLoader the CLI installs. root is the script's root
// (includeRoot above) and libs is the library search path (libDirs above). An embedder
// gets no loader at all unless it installs one (§14.3).
//
// Which root a `./` include is checked against is decided by the file doing the
// including, not by the program that was started: a library found on the search path is
// rooted in the directory it was found in, so its own `./helpers.mzs` resolves and its
// `../../etc/passwd` does not. That is what lets a library be included from anywhere
// without the confinement either following the caller's tree — where the library is not —
// or being dropped for library code, which is the one place it is least deserved.
func fileLoader(root string, libs []string) mzs.ModuleLoader {
	return func(from, path string) (string, string, error) {
		// A leading `/` is absolute everywhere, but filepath.IsAbs only says so where it
		// is the platform's own spelling of absolute — on Windows `/lib.mzs` is not. One
		// answer for one question: an include path is relative, however it is written.
		if filepath.IsAbs(path) || strings.HasPrefix(filepath.ToSlash(path), "/") {
			return "", "", fmt.Errorf("an include path must be relative")
		}
		if path == "" {
			return "", "", fmt.Errorf("an include path cannot be empty")
		}
		if isFileForm(path) {
			return loadNeighbour(from, path, root, libs)
		}
		return loadLibrary(path, libs)
	}
}

// recordSources wraps a loader so every source it hands the interpreter is kept under the
// resolved name it was handed with. The CLI's diagnostics draw a module's caret line from
// this record rather than reading the file again, which would be a second read outside
// the confinement the first one went through, and could show a line of a file the
// program never compiled.
func recordSources(load mzs.ModuleLoader, into map[string]string) mzs.ModuleLoader {
	return func(from, path string) (string, string, error) {
		resolved, src, err := load(from, path)
		if err == nil {
			into[resolved] = src
		}
		return resolved, src, err
	}
}

// isFileForm reports whether a path names a file next to the including one rather than a
// library. `./` and `../` are the two marks; `..` alone is the same request spelled
// shorter. The path is put through filepath.ToSlash first, which is what makes
// `.\lib.mzs` — the spelling a Windows editor produces — the file form on Windows. On a
// system whose separator is `/` it converts nothing, and it should not: there a backslash
// is an ordinary character in a file name, not a separator, so `..\lib.mzs` is one name
// and reading it as a climb would be the platform's own rule broken.
func isFileForm(path string) bool {
	p := filepath.ToSlash(path)
	return p == "." || p == ".." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}

// loadNeighbour resolves `"./lib.mzs"`: against the directory of the file doing the
// including, then checked against that file's own root.
func loadNeighbour(from, path, root string, libs []string) (string, string, error) {
	base := filepath.Dir(from)
	if base == "" || base == "." || from == "" || from == "-e" || from == "<stdin>" {
		base = root
	}
	resolved, err := filepath.Abs(filepath.Join(base, path))
	if err != nil {
		return "", "", err
	}
	owner, err := ownerRoot(from, root, libs)
	if err != nil {
		return "", "", err
	}
	rel, ok := within(owner, resolved)
	if !ok {
		return "", "", fmt.Errorf("outside the root directory %s", owner)
	}
	src, err := readInRoot(owner, rel, resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, src, nil
}

// loadLibrary resolves `"money"`: the first hit on the search path, trying the file and
// then the directory that a library which outgrew one file is entered through.
//
// A name that is not found says where it looked. That is the one thing the reader cannot
// work out from the source line, and the answer is usually visible in the list — the
// library is installed under a different name, or MZSPATH does not point where it was
// believed to.
func loadLibrary(name string, libs []string) (string, string, error) {
	if err := checkLibName(name); err != nil {
		return "", "", err
	}
	rel := filepath.FromSlash(name)
	for _, dir := range libs {
		// A directory on the search path that is not there is not an error: a path is a
		// list of places a library may live, and MZSPATH naming one that does not exist
		// yet is ordinary.
		root, err := os.OpenRoot(dir)
		if err != nil {
			continue
		}
		src, resolved, err := searchDir(root, dir, rel)
		root.Close()
		switch {
		case err != nil:
			// Something is there under this name and cannot be read — a permission, an
			// I/O error, a link out of the tree. Saying so beats searching on: the next
			// directory's answer would be a different library under the name this one
			// already claimed, and the search order is the whole contract.
			return "", "", err
		case resolved != "":
			return resolved, src, nil
		}
	}
	if len(libs) == 0 {
		return "", "", fmt.Errorf("no such library: the search path is empty (set %s, or create ~/.mzs/lib)", libPathEnv)
	}
	return "", "", fmt.Errorf("no such library: looked for %s and %s in %s",
		name+libExt, filepath.Join(name, libEntry), strings.Join(libs, ", "))
}

// searchDir tries one search directory's two layouts. It answers three ways: found (a
// source and its path), absent (an empty path and no error, so the search moves on), and
// present-but-unreadable, which is an error rather than a fourth kind of absence.
//
// Lstat is what draws that line, and it draws it before the read: a name that is simply
// not there — or that is a directory, or whose parent is a file — is this layout not
// being the one, while a name that exists is this directory's answer, and whatever
// happens next is what the caller is told.
func searchDir(root *os.Root, dir, rel string) (src, resolved string, err error) {
	for _, candidate := range []string{rel + libExt, filepath.Join(rel, libEntry)} {
		info, statErr := root.Lstat(candidate)
		if statErr != nil || info.IsDir() {
			continue
		}
		resolved = filepath.Join(dir, candidate)
		src, err = readInRoot(dir, candidate, resolved)
		if err != nil {
			return "", "", err
		}
		return src, resolved, nil
	}
	return "", "", nil
}

// readInRoot reads one file through os.Root, which is what makes the confinement real
// rather than lexical. The check above compares written paths, and a written path inside
// the root can still *lead* outside it: a symbolic link is a component the writing does
// not show. os.Root refuses exactly that — it follows a link inside the root and refuses
// one that leaves it — so the promise "an include may not leave the tree" is kept by the
// read itself rather than by the spelling of the path.
//
// A refusal is reported in the loader's own words. One policy has one message, whether
// the path climbed out in writing or through a link, and the reader is told the same
// thing either way.
func readInRoot(dir, rel, resolved string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	b, err := root.ReadFile(rel)
	if err == nil {
		return string(b), nil
	}
	if isLink(root, rel) {
		return "", fmt.Errorf("outside the root directory %s", dir)
	}
	// os.Root names the file relative to the root, and with the operation it performed
	// (`openat`). The reader wants the path they can look at, and the operation they
	// asked for, so the one thing kept from it is the reason.
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return "", &fs.PathError{Op: "open", Path: resolved, Err: pe.Err}
	}
	return "", err
}

// isLink reports whether name is a symbolic link, which is the one reason os.Root refuses
// a read of a path the written form has already been checked against the root: a link is
// the component the writing does not show. It is asked only after a refusal, and it
// decides what to *say* rather than what to allow.
//
// The two refusals it stands for are "the link leaves the root" and "the link is
// absolute", and they get one message because the rule is one rule: a path may not leave
// the tree, and an absolute link is treated as leaving it — the tree cannot see where it
// goes, which is the same reason an include path may not be absolute either.
func isLink(root *os.Root, name string) bool {
	info, err := root.Lstat(name)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// checkLibName rejects the spellings that are not library names, each with the spelling
// that was meant. A library name is a name, not a path: it carries no extension and does
// not climb. (Absolute is answered one level up, for both forms at once.)
func checkLibName(name string) error {
	slashed := filepath.ToSlash(name)
	if strings.HasSuffix(slashed, libExt) {
		short := strings.TrimSuffix(slashed, libExt)
		return fmt.Errorf("a library name carries no %s: write %q, or %q for the file next to this one",
			libExt, short, "./"+slashed)
	}
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return fmt.Errorf("a library name cannot contain '..'; %q is the form that may", "./"+slashed)
		}
	}
	return nil
}

// ownerRoot is the directory the file doing the including is confined to: the search
// directory it was found under, or the script's root. The deepest match wins, so a
// library path that points inside the project — MZSPATH=./lib is a reasonable thing to
// write — still roots its libraries in the library directory rather than in the whole
// project.
func ownerRoot(from, root string, libs []string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if from == "" || from == "-e" || from == "<stdin>" {
		return absRoot, nil
	}
	absFrom, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}
	owner := ""
	for _, dir := range append([]string{absRoot}, libs...) {
		if _, ok := within(dir, absFrom); ok && len(dir) > len(owner) {
			owner = dir
		}
	}
	if owner == "" {
		// A file under none of the roots is the script's own file in the ordinary case
		// (the root *is* its directory); anything else is a loader that has already been
		// asked to read something it should not have, so the narrowest answer is right.
		return absRoot, nil
	}
	return owner, nil
}

// within reports whether path is dir or something below it, and hands back the path
// relative to dir — the form os.Root asks for, computed by the check that had to compute
// it anyway. It is the root check both forms end in, written once so the two cannot drift
// apart.
func within(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false
	}
	return rel, rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
