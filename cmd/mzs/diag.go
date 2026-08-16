package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"mzs/mzs"
)

// reportErr renders every diagnostic in err as `file:line:col: kind: message`
// followed by the offending source line and a caret (§17). One-liners are typed by
// hand into a shell or a web input, so pointing at the rune that broke is most of
// the value the CLI adds.
//
// Only the first diagnostic on any one source line is shown. That is the whole
// promise of the §5.6 table: someone who pasted Ruby gets one precise fix-it, not
// the cascade the recovering parser produces after it — `{ |x| x }` reports
// "closure parameters are parenthesised" and stops, instead of adding a complaint
// about the closing '|'. Errors on other lines are unaffected, so a broken file
// still lists every place that needs a hand.
func reportErr(w io.Writer, name, src string, err error) {
	seen := map[int]bool{}
	for _, e := range flatten(err) {
		var me *mzs.Error
		if !errors.As(e, &me) {
			fmt.Fprintf(w, "%s: %v\n", name, e)
			continue
		}
		if me.Line > 0 {
			if seen[me.Line] {
				continue
			}
			seen[me.Line] = true
		}
		fmt.Fprintln(w, me.Error())
		writeCaret(w, src, me.Line, me.Col)
	}
}

// reportWarnings prints compile-time warnings, which never fail a build unless the
// host sets StrictWarnings (§17).
func reportWarnings(w io.Writer, name, src string, ws []mzs.Warning) {
	for _, warn := range ws {
		fmt.Fprintf(w, "%s:%d:%d: warning: %s\n", name, warn.Line, warn.Col, warn.Msg)
		writeCaret(w, src, warn.Line, warn.Col)
	}
}

// flatten unpacks the errors.Join a recovering parser produces, so all ten syntax
// errors from one compile are shown rather than the joined blob.
func flatten(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			out = append(out, flatten(e)...)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []error{err}
}

func writeCaret(w io.Writer, src string, line, col int) {
	text, ok := sourceLine(src, line)
	if !ok || col <= 0 {
		return
	}
	fmt.Fprintf(w, "  %s\n", text)
	fmt.Fprintf(w, "  %s^\n", pad(text, col))
}

// sourceLine returns the 1-based line, handling all three line endings of §3.1.
func sourceLine(src string, line int) (string, bool) {
	if line <= 0 || src == "" {
		return "", false
	}
	n := 1
	start := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c != '\n' && c != '\r' {
			continue
		}
		if n == line {
			return strings.TrimRight(src[start:i], "\r"), true
		}
		if c == '\r' && i+1 < len(src) && src[i+1] == '\n' {
			i++
		}
		n++
		start = i + 1
	}
	if n == line {
		return src[start:], true
	}
	return "", false
}

// pad builds the caret's indent from the line's own prefix so a tab stays a tab and
// the marker still lands under the right rune. Col counts runes, never bytes.
func pad(text string, col int) string {
	var sb strings.Builder
	i := 0
	for _, r := range text {
		if i >= col-1 {
			break
		}
		if r == '\t' {
			sb.WriteByte('\t')
		} else {
			sb.WriteByte(' ')
		}
		i++
	}
	for ; i < col-1; i++ {
		sb.WriteByte(' ')
	}
	return sb.String()
}

// codeFor separates "the script is wrong" from "the script ran away", so a caller
// can retry the second and never the first.
func codeFor(err error) int {
	switch {
	case errors.Is(err, mzs.ErrTimeout), errors.Is(err, mzs.ErrBudget),
		errors.Is(err, mzs.ErrDepth), errors.Is(err, mzs.ErrCanceled):
		return exitLimit
	}
	return exitError
}
