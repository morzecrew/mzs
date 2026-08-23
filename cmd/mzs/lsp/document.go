package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// document is one open file: its text, and the line index every position question is
// answered from.
//
// Two coordinate systems meet here and neither is negotiable. The protocol counts lines
// from zero and characters in UTF-16 code units; mzs counts lines from one and columns in
// runes, "because the corpus is full of Cyrillic and emoji and a byte column would point
// nowhere" (token.Pos). Everything crossing the boundary is converted, which is why a
// caret in a diagnostic lands on the right character in a line of Russian and why a hover
// over a word after an emoji finds the word.
type document struct {
	uri     string
	version int
	text    string
	// lines holds each line without its terminator, so line i is lines[i] and a column
	// is an index into it.
	lines []string
}

func newDocument(uri, text string, version int) *document {
	d := &document{uri: uri, version: version}
	d.setText(text)
	return d
}

func (d *document) setText(text string) {
	d.text = text
	d.lines = strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

// path is the file the URI names, or "" when it names something else. Only `file:` URIs
// have a path, and only they can be jumped to.
func (d *document) path() string { return uriToPath(d.uri) }

// name is what diagnostics call this document: its path when it has one, its URI
// otherwise.
func (d *document) name() string {
	if p := d.path(); p != "" {
		return p
	}
	return d.uri
}

// lspPosition converts a one-based (line, rune column) from the compiler into the
// protocol's zero-based, UTF-16-counted position.
func (d *document) lspPosition(line, col int) position {
	if line < 1 {
		return position{}
	}
	i := line - 1
	if i >= len(d.lines) {
		return position{Line: i, Character: 0}
	}
	return position{Line: i, Character: utf16Len(runePrefix(d.lines[i], col-1))}
}

// lineText returns the zero-based line, or "" past the end.
func (d *document) lineText(i int) string {
	if i < 0 || i >= len(d.lines) {
		return ""
	}
	return d.lines[i]
}

// endPosition is the position just past the last character of the document, which is what
// a whole-document edit replaces up to.
func (d *document) endPosition() position {
	last := len(d.lines) - 1
	if last < 0 {
		return position{}
	}
	return position{Line: last, Character: utf16Len(d.lines[last])}
}

// runePrefix returns the first n runes of s.
func runePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// utf16Prefix returns the prefix of s that is n UTF-16 code units long, rounded down to
// a rune boundary: a client that points into the middle of a surrogate pair gets the
// character it started on rather than half of one.
func utf16Prefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		if units >= n {
			return s[:i]
		}
		units += utf16Units(r)
	}
	return s
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Units(r)
	}
	return n
}

func utf16Units(r rune) int {
	if r > 0xFFFF {
		return len(utf16.Encode([]rune{r}))
	}
	return 1
}

// uriToPath turns a `file:` URI into a path. Anything else — an untitled buffer, a
// document inside an archive — has no path, and the features that need one say so by
// doing nothing.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return ""
	}
	// Windows spells the same thing as /C:/dir/file.
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

// pathToURI is the other direction, for a go-to-definition that lands in another file.
func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}
