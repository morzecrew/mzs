//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"mzs/internal/lineedit"
)

// The one thing a scripted reader cannot check: that a *console* gets the line editor
// rather than the scanner. It takes a real terminal to ask, so this allocates one.
//
// The two request numbers are the Linux pty ABI and are not in the syscall package. They
// only make a terminal for the test — nothing under test uses them.
const (
	tiocgptn   = 0x80045430 // ioctl(ptmx, …) → the slave's number
	tiocsptlck = 0x40045431 // ioctl(ptmx, …, 0) → unlock the slave
)

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), tiocsptlck, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		t.Skipf("cannot unlock a pseudo-terminal: %v", e)
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), tiocgptn, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		t.Skipf("cannot name a pseudo-terminal: %v", e)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// TestNewLineSourceOnATerminal is the wiring the whole feature hangs from: a console on
// both ends gets the editor, with the prompt coloured when the environment allows it and
// plain when it does not.
func TestNewLineSourceOnATerminal(t *testing.T) {
	_, slave := openPTY(t)
	cfg, _, _, err := parseArgs([]string{"--repl"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}

	unsetEnv(t, "NO_COLOR")
	t.Setenv("TERM", "xterm-256color")
	lines, session := newLineSource(cfg, slave, slave)
	ed, ok := lines.(*editorSource)
	if !ok {
		t.Fatalf("lines = %T, want the line editor", lines)
	}
	if ed.fallback == nil {
		t.Error("the editor was installed without the scanner to fall back to")
	}
	if !strings.Contains(session.prompt, "\x1b[") || !strings.Contains(session.contPrompt, "\x1b[") {
		t.Errorf("prompts = %q / %q, want them coloured", session.prompt, session.contPrompt)
	}
	if !strings.Contains(session.prompt, "mzs>") || !strings.Contains(session.contPrompt, "...>") {
		t.Errorf("prompts = %q / %q, want the same two prompts as ever", session.prompt, session.contPrompt)
	}

	t.Setenv("NO_COLOR", "1")
	_, plain := newLineSource(cfg, slave, slave)
	if plain.prompt != "mzs> " || plain.contPrompt != "...> " {
		t.Errorf("prompts = %q / %q under NO_COLOR, want them plain", plain.prompt, plain.contPrompt)
	}
}

// TestREPLOverATerminal runs the loop itself against a console: the keys go in through
// the pty, the values come back out of it, and the arrow keys work on the way.
func TestREPLOverATerminal(t *testing.T) {
	master, slave := openPTY(t)
	cfg, _, _, err := parseArgs([]string{"--repl"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	t.Setenv("TERM", "xterm-256color")
	unsetEnv(t, "NO_COLOR")

	lines, session := newLineSource(cfg, slave, slave)
	ed, ok := lines.(*editorSource)
	if !ok {
		t.Fatalf("lines = %T, want the line editor", lines)
	}
	// Raw first: the keys are written before the loop reads them, and a line discipline
	// still in canonical mode would echo them and rewrite the carriage returns.
	term, ok := lineedit.FileTerminal(slave, slave)
	if !ok {
		t.Fatal("FileTerminal said no to a pseudo-terminal")
	}
	restore, err := term.MakeRaw()
	if err != nil {
		t.Fatalf("raw mode: %v", err)
	}
	defer restore()

	// Drain the console: nobody reading it means the loop blocks once it fills up.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := master.Read(buf); err != nil {
				return
			}
		}
	}()

	keys := "12\x1b[D0\r" + // an arrow into the middle of the line
		"\x1b[A\x1b[A\r" + // and back through the history to the same line
		"oops\x03" + // Ctrl-C throws the line away
		".exit\r"
	if _, err := master.WriteString(keys); err != nil {
		t.Fatalf("writing the keys: %v", err)
	}

	var out, errOut strings.Builder
	if code := replLoop(cfg, &out, &errOut, ed, session); code != exitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if got := strings.Count(out.String(), "102"); got != 2 {
		t.Errorf("stdout = %q, want 102 twice — once typed, once recalled", out.String())
	}
	if errOut.String() != "" {
		t.Errorf("stderr = %q, want nothing: the interrupted line leaves no diagnostic", errOut.String())
	}
}
