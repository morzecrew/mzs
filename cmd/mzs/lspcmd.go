package main

import (
	"fmt"
	"io"

	"mzs/cmd/mzs/lsp"
)

// `mzs lsp` (§15): the language server, speaking the Language Server Protocol over
// stdin and stdout. It prints nothing to either on its own — the stream is the protocol —
// so anything worth saying about the server itself goes to stderr.

func runLSP(argv []string, stdout, stderr io.Writer, stdin io.Reader) int {
	for _, a := range argv {
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, lspUsage)
			return exitOK
		default:
			fmt.Fprintf(stderr, "mzs lsp: unknown argument %s\n", a)
			fmt.Fprintf(stderr, "run 'mzs lsp --help' for usage\n")
			return exitUsage
		}
	}
	if err := lsp.Serve(stdin, stdout, version); err != nil {
		fmt.Fprintf(stderr, "mzs lsp: %v\n", err)
		return exitError
	}
	return exitOK
}

const lspUsage = `mzs lsp — the language server

Usage:
  mzs lsp                  speak the Language Server Protocol on stdin and stdout

It is started by an editor, not by a person. In VS Code the extension in editors/vscode
starts it; anything else needs a line of configuration naming the command.

What it answers:
  diagnostics       the compiler's errors and warnings, on open, on change and on save
  hover             the signature and the meaning of the name under the cursor
  definition        the fn, record or include that bound the name; a file for an include
  completion        members after a '.', and names, modules and keywords elsewhere
  formatting        the whole document through 'mzs fmt'
  symbols           the outline: what the file declares

Every capability is installed while checking, so 'include time' is not an error in an
editor that cannot know which flags the program will be run with (SPEC §12.8).
`
