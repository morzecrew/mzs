package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mzs/internal/printer"
)

// `mzs fmt` (§15). One spelling of a program, no options to argue about, and three ways
// to ask for it: print it, write it back, or name the files that are not it.
//
// With no path at all it is a filter — stdin to stdout — which is what an editor's
// "format on save" reaches for and what makes `mzs fmt < a.mzs | diff a.mzs -` work
// without a temporary file.

// fmtConfig is the parsed `mzs fmt` command line.
type fmtConfig struct {
	write bool
	list  bool
	paths []string
}

func runFmt(argv []string, stdout, stderr io.Writer, stdin io.Reader) int {
	cfg, err := parseFmtArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		fmt.Fprintf(stderr, "run 'mzs fmt --help' for usage\n")
		return exitUsage
	}
	if cfg == nil {
		fmt.Fprint(stdout, fmtUsage)
		return exitOK
	}

	if len(cfg.paths) == 0 {
		if cfg.write {
			fmt.Fprintf(stderr, "mzs fmt: -w needs files: there is nothing to write stdin back to\n")
			return exitUsage
		}
		return fmtStdin(cfg, stdout, stderr, stdin)
	}

	files, err := mzsFiles(cfg.paths)
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		return exitError
	}
	code := exitOK
	for _, path := range files {
		if !fmtFile(cfg, path, stdout, stderr) {
			code = exitError
		}
	}
	return code
}

func fmtStdin(cfg *fmtConfig, stdout, stderr io.Writer, stdin io.Reader) int {
	b, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		return exitError
	}
	out, err := printer.Format("<stdin>", string(b))
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		return exitError
	}
	if cfg.list {
		if out != string(b) {
			fmt.Fprintln(stdout, "<stdin>")
		}
		return exitOK
	}
	fmt.Fprint(stdout, out)
	return exitOK
}

// fmtFile formats one file and reports whether it could. A file that does not parse is
// reported and skipped: `mzs fmt` over a directory says everything it has to say about
// every file, rather than stopping at the first one with a typo in it.
func fmtFile(cfg *fmtConfig, path string, stdout, stderr io.Writer) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		return false
	}
	out, err := printer.Format(path, string(b))
	if err != nil {
		fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
		return false
	}
	same := out == string(b)
	switch {
	case cfg.list:
		if !same {
			fmt.Fprintln(stdout, path)
		}
	case cfg.write:
		if same {
			return true
		}
		// The mode is the file's own: formatting a script must not take away the bit
		// that lets it run.
		mode := fs.FileMode(0o644)
		if st, serr := os.Stat(path); serr == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(out), mode); err != nil {
			fmt.Fprintf(stderr, "mzs fmt: %v\n", err)
			return false
		}
	default:
		fmt.Fprint(stdout, out)
	}
	return true
}

// mzsFiles expands the arguments: a file is itself, a directory is every `.mzs` file
// under it, in a stable order. A named file is formatted whatever its extension — the
// person typing it has already decided — and a directory walk takes only `.mzs`, since
// everything else in there belongs to something else.
func mzsFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		var found []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// A dot directory is somebody else's: .git holds objects that happen to
				// end in the right letters often enough to matter.
				if name := d.Name(); path != p && strings.HasPrefix(name, ".") {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".mzs") {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(found)
		out = append(out, found...)
	}
	return out, nil
}

func parseFmtArgs(argv []string) (*fmtConfig, error) {
	cfg := &fmtConfig{}
	endOfFlags := false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if endOfFlags || a == "" || a[0] != '-' || a == "-" {
			cfg.paths = append(cfg.paths, a)
			continue
		}
		switch a {
		case "--":
			endOfFlags = true
		case "-w", "--write":
			cfg.write = true
		case "-l", "--list":
			cfg.list = true
		case "-h", "--help":
			return nil, nil
		default:
			return nil, fmt.Errorf("unknown flag %s", a)
		}
	}
	if cfg.write && cfg.list {
		return nil, fmt.Errorf("-w writes the files and -l names them; pick one")
	}
	return cfg, nil
}

var fmtUsage = fmt.Sprintf(`mzs fmt — the one spelling of a program

Usage:
  mzs fmt [flags] [path...]        format each file, or each .mzs file under each directory
  mzs fmt < a.mzs                  format standard input to standard output

Flags:
  -w, --write   write the result back to each file instead of printing it
  -l, --list    print the name of each file that is not already formatted
  -h, --help    this text

There is nothing to configure: four spaces, spaces around operators, one line per element
once a line grows past %d columns. A comment keeps its place, a heredoc keeps its body,
and a literal keeps the text it was written with.

Exit codes: 0 ok, 1 a file that could not be read, parsed or written, 2 usage.
`, printer.Width)
