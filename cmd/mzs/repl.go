package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"mzs"
	"mzs/internal/lexer"
	"mzs/internal/token"
)

// repl is the interactive front door. mzs has no way to hand a live Env back to a
// host — a *Program is immutable and every Run gets a fresh frame — so the session
// keeps its state the only way that is both correct and cheap: it replays the lines
// that already worked and prints only the output the replay had not produced
// before. Evaluation is deterministic (§8.13), so the replayed prefix writes the
// same bytes every time and the delta is exactly this line's output. The
// interpreter is rebuilt for each line to keep that true under --rand, whose
// generator would otherwise carry its position into the replay.
func repl(cfg *config, stdout, stderr io.Writer, stdin io.Reader) int {
	var sink bytes.Buffer

	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)

	fmt.Fprintf(stdout, "%s — .help for help, .exit to quit\n", version)

	var history []string
	shown := 0

	for {
		fmt.Fprint(stdout, "mzs> ")
		line, ok := readLine(sc)
		if !ok {
			fmt.Fprintln(stdout)
			return exitOK
		}
		switch strings.TrimSpace(line) {
		case "":
			continue
		case ".exit", ".quit", ":q":
			return exitOK
		case ".help":
			fmt.Fprint(stdout, replHelp)
			continue
		case ".clear":
			history, shown = nil, 0
			sink.Reset()
			fmt.Fprintln(stdout, "session cleared")
			continue
		case ".src":
			fmt.Fprintln(stdout, strings.Join(history, "\n"))
			continue
		case ".vars":
			for _, k := range sortedVarNames(cfg.vars) {
				fmt.Fprintf(stdout, "%s = %s\n", k, cfg.vars[k].Inspect())
			}
			continue
		}

		in := mzs.New(options(cfg, &sink, stderr, nil))
		prog, pending, full, err := compileMaybeContinued(in, history, line, sc, stdout)
		if err != nil {
			reportErr(stderr, "repl", full, err)
			continue
		}

		sink.Reset()
		res, runErr := in.RunResult(context.Background(), prog, cfg.vars)
		produced := sink.String()
		if len(produced) > shown {
			fmt.Fprint(stdout, produced[shown:])
		}
		if runErr != nil {
			reportErr(stderr, "repl", full, runErr)
			continue
		}

		history = append(history, pending)
		shown = len(produced)
		fmt.Fprintln(stdout, res.Value.Inspect())
	}
}

// compileMaybeContinued compiles history plus the new line, and keeps asking for
// more input while the line is still open — so `fn f(a, b) {` on its own line opens
// a definition instead of printing an error.
func compileMaybeContinued(in *mzs.Interp, history []string, line string, sc *bufio.Scanner, out io.Writer) (prog *mzs.Program, pending, full string, err error) {
	pending = line
	for incomplete(pending) {
		fmt.Fprint(out, "...> ")
		next, ok := readLine(sc)
		if !ok || strings.TrimSpace(next) == "" {
			// A blank line aborts the continuation; compiling what there is turns
			// the abandoned construct into the error the user needs to see.
			break
		}
		pending += "\n" + next
	}
	full = strings.Join(append(append([]string{}, history...), pending), "\n")
	prog, err = in.Compile("repl", full)
	return prog, pending, full, err
}

// incomplete reports whether the pending source is still open: a '(', '[' or '{'
// with no partner, or a string or regex literal the line ended in the middle of.
// mzs has no 'end' to wait for (§5.6), so the brackets are the whole story, and
// asking the lexer rather than counting bytes keeps a '}' inside a string or a
// comment from closing anything. Nothing else continues: `1 +` is a finished line
// that happens to be wrong, and saying so at once beats a prompt the user has to
// escape with a blank line.
func incomplete(src string) bool {
	toks, errs := lexer.Lex("repl", src)
	for _, e := range errs {
		if strings.Contains(e.Msg, "unterminated") {
			return true
		}
	}
	depth := 0
	for _, t := range toks {
		switch t.Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			depth--
		}
	}
	return depth > 0
}

func readLine(sc *bufio.Scanner) (string, bool) {
	if !sc.Scan() {
		return "", false
	}
	return sc.Text(), true
}

func sortedVarNames(vars map[string]mzs.Value) []string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

const replHelp = `  .help    this text
  .exit    leave (:q and Ctrl-D work too)
  .clear   forget every line of this session
  .src     show the session's accumulated source
  .vars    show the bound $variables

  Locals and functions carry over between lines; $vars come from -v/--vars.
  A line with an unclosed '{', '(' or '[' keeps reading at the ...> prompt;
  a blank line aborts it.

  mzs> s = "  ОПЕРАТОР ".lower.trim
  mzs> s ~ /оператор/i
  mzs> (0..6).map { it * 2 }.each_slice(2).array
  mzs> fn f(a, b) { a += b; return a }
  mzs> match s { in ["да", "ага"] -> 1; else -> 0 }
`
