package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mzs/internal/doccomment"
	"mzs/internal/parser"
)

// `mzs doc` (RFC 0019 §2): what a script says about itself. Each file's top-level
// declarations with their signatures and the `##` comment above each, as Markdown, or as
// data with --json in the shape the standard library's own documentation has
// (internal/apidoc), so a tool reads the built-in and the user documentation alike.

type docConfig struct {
	json  bool
	paths []string
}

// docFile is one file's documentation, kept in the order it is printed.
type docFile struct {
	path  string
	items []doccomment.Item
}

// docEntry is apidoc.Entry with what a user declaration adds: sig, sem and ex are the
// standard library's three fields, so a reader of one can read the other.
type docEntry struct {
	Sig      string `json:"sig"`
	Sem      string `json:"sem"`
	Ex       string `json:"ex"`
	Kind     string `json:"kind"`
	Exported bool   `json:"exported"`
	Line     int    `json:"line"`
}

func runDoc(argv []string, stdout, stderr io.Writer, stdin io.Reader) int {
	cfg, err := parseDocArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "mzs doc: %v\n", err)
		fmt.Fprintf(stderr, "run 'mzs doc --help' for usage\n")
		return exitUsage
	}
	if cfg == nil {
		fmt.Fprint(stdout, docUsage)
		return exitOK
	}

	code := exitOK
	var files []docFile
	if len(cfg.paths) == 0 {
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "mzs doc: %v\n", err)
			return exitError
		}
		items, err := doccomment.Parse("<stdin>", string(b))
		if err != nil {
			reportDocErr(stderr, err)
			return exitError
		}
		files = append(files, docFile{path: "<stdin>", items: items})
	} else {
		paths, ok := docFiles(cfg.paths, stderr)
		if !ok {
			code = exitError
		}
		for _, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(stderr, "mzs doc: %v\n", err)
				code = exitError
				continue
			}
			items, err := doccomment.Parse(path, string(b))
			if err != nil {
				// A file that does not parse is reported and the rest are still
				// documented, as `mzs fmt` does over a directory.
				reportDocErr(stderr, err)
				code = exitError
				continue
			}
			files = append(files, docFile{path: path, items: items})
		}
	}

	if cfg.json {
		if err := writeDocJSON(stdout, files); err != nil {
			fmt.Fprintf(stderr, "mzs doc: %v\n", err)
			return exitError
		}
		return code
	}
	for i, f := range files {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprint(stdout, docMarkdown(f))
	}
	return code
}

// reportDocErr prints each syntax error with the place it was found, the way the main
// command's --check spells one (§17).
func reportDocErr(stderr io.Writer, err error) {
	var list []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		list = j.Unwrap()
	} else {
		list = []error{err}
	}
	for _, e := range list {
		var pe *parser.Error
		if errors.As(e, &pe) && pe.Pos.IsValid() {
			fmt.Fprintf(stderr, "%s:%d:%d: syntax: %s\n", pe.File, pe.Pos.Line, pe.Pos.Col, pe.Msg)
			continue
		}
		fmt.Fprintf(stderr, "mzs doc: %v\n", e)
	}
}

// docFiles expands the arguments as mzsFiles does — a file is itself, a directory every
// `.mzs` file under it in a stable order — leaving out the test files a directory holds:
// a `_test.mzs` file is the runner's, not part of anything's interface. A file named on
// the command line is documented whatever it is called.
//
// Unlike mzsFiles it does not stop at the first error. A path that cannot be read, or an
// entry of a directory that cannot be, is reported, and the walk goes on with the rest;
// ok says whether everything could be read.
func docFiles(paths []string, stderr io.Writer) (out []string, ok bool) {
	ok = true
	report := func(err error) {
		fmt.Fprintf(stderr, "mzs doc: %v\n", err)
		ok = false
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			report(err)
			continue
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		found := walkDocDir(os.DirFS(p), p, report)
		out = append(out, found...)
	}
	return out, ok
}

// walkDocDir lists the documentable files under fsys, which is the directory base,
// sorted. Every error is reported and the walk goes on: the callback returns nil after
// one, so a directory that could be read in part still has the entries that were read
// visited, and its siblings are walked as well.
func walkDocDir(fsys fs.FS, base string, report func(error)) []string {
	var found []string
	// The callback never returns an error, so WalkDir's own result is always nil.
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			// fs.FS errors name the path inside fsys; the reader wants the one they
			// typed.
			var pe *fs.PathError
			if errors.As(err, &pe) {
				pe.Path = filepath.Join(base, filepath.FromSlash(pe.Path))
			}
			report(err)
			return nil
		}
		if d.IsDir() {
			// A dot directory is somebody else's (.git), as for mzs fmt.
			if name != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".mzs") && !strings.HasSuffix(name, "_test.mzs") {
			found = append(found, filepath.Join(base, filepath.FromSlash(name)))
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// ordered is the file's items with the exported names first, each group in source order.
func ordered(items []doccomment.Item) []doccomment.Item {
	out := make([]doccomment.Item, 0, len(items))
	for _, exported := range []bool{true, false} {
		for _, it := range items {
			if it.Exported == exported {
				out = append(out, it)
			}
		}
	}
	return out
}

// docMarkdown renders one file: a heading for the file, and one for each declaration
// with its signature as code and its doc comment under it, as it was written.
func docMarkdown(f docFile) string {
	var b strings.Builder
	b.WriteString("# " + f.path + "\n")
	items := ordered(f.items)
	if len(items) == 0 {
		b.WriteString("\nNothing is declared at the top level.\n")
		return b.String()
	}
	for _, it := range items {
		sig := it.Sig
		if it.Exported {
			sig = "export " + sig
		}
		b.WriteString("\n## " + codeSpan(sig) + "\n")
		if it.Doc != "" {
			b.WriteString("\n" + it.Doc + "\n")
		}
	}
	return b.String()
}

// writeDocJSON writes {"modules": {path: {name: entry}}}: the `modules` table of
// internal/apidoc, with a file's path where a built-in module's name stands.
func writeDocJSON(w io.Writer, files []docFile) error {
	modules := map[string]map[string]docEntry{}
	for _, f := range files {
		table := map[string]docEntry{}
		for _, it := range f.items {
			table[it.Name] = docEntry{
				Sig:      codeSpan(it.Sig),
				Sem:      it.Doc,
				Kind:     it.Kind,
				Exported: it.Exported,
				Line:     it.Line,
			}
		}
		modules[f.path] = table
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"modules": modules})
}

// codeSpan writes s as one Markdown code span. A default may hold a backtick — `"`"` is a
// string — so the fence is one backtick longer than the longest run inside, and a space
// pads a span that starts or ends with one (CommonMark §6.1).
func codeSpan(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return fence + s + fence
}

func parseDocArgs(argv []string) (*docConfig, error) {
	cfg := &docConfig{}
	endOfFlags := false
	for _, a := range argv {
		if a == "-" && !endOfFlags {
			continue // stdin, as for `mzs fmt -`
		}
		if endOfFlags || a == "" || a[0] != '-' {
			cfg.paths = append(cfg.paths, a)
			continue
		}
		switch a {
		case "--":
			endOfFlags = true
		case "--json":
			cfg.json = true
		case "-h", "--help":
			return nil, nil
		default:
			return nil, fmt.Errorf("unknown flag %s", a)
		}
	}
	return cfg, nil
}

const docUsage = `mzs doc — what a script says about itself

Usage:
  mzs doc [--json] [path...]   document each file, or each .mzs file under each directory
  mzs doc < lib.mzs            document standard input

A run of '##' comment lines directly above a fn, a record, an export or a top-level
binding is that declaration's documentation; a plain '#' is an ordinary comment. Each
file prints as a Markdown section, exported names first, each with its signature and its
doc text. A directory's *_test.mzs files are skipped.

Flags:
  --json      print {"modules": {path: {name: {sig, sem, ex, kind, exported, line}}}},
              the shape of the standard library's own documentation
  -h, --help  this text

Exit codes: 0 ok, 1 a file that could not be read or parsed, 2 usage.
`
