//go:build !(linux || darwin || dragonfly || freebsd || netbsd || openbsd)

package lineedit

import "os"

// Everywhere else — Windows above all — there is no raw mode here yet, so FileTerminal
// says no and the caller keeps the plain line reader it already has. That is a REPL
// without the arrow keys and without colour, which is exactly the REPL these platforms
// have today; nothing regresses, and the interface is the one place a console
// implementation has to be added.
func FileTerminal(in, out *os.File) (Terminal, bool) { return nil, false }
