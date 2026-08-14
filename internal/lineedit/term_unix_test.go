//go:build linux

package lineedit

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The console half of this package is the half a scripted terminal cannot reach: the
// three ioctls, the flags raw mode clears, and the window size. A pseudo-terminal is a
// real one as far as all of that is concerned, so these tests allocate one.
//
// The two request numbers below are the Linux pty ABI and are not in the syscall package.
// They are only used to *make* a terminal for the test; the code under test uses nothing
// but what syscall already defines.
const (
	tiocgptn   = 0x80045430 // ioctl(ptmx, …) → the slave's number
	tiocsptlck = 0x40045431 // ioctl(ptmx, …, 0) → unlock the slave
)

// ioctlOn is the test's own ioctl, and it goes through SyscallConn rather than Fd()
// because Fd() takes the file out of the runtime's poller: after one call to it a read
// deadline is silently ignored, which is a very quiet way to hang a test.
func ioctlOn(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if cerr := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); cerr != nil {
		return cerr
	}
	if errno != 0 {
		return errno
	}
	return nil
}

// openPTY returns the two ends of a fresh pseudo-terminal.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	var unlock int32
	if e := ioctlOn(m, tiocsptlck, unsafe.Pointer(&unlock)); e != nil {
		m.Close()
		t.Skipf("cannot unlock a pseudo-terminal: %v", e)
	}
	var n uint32
	if e := ioctlOn(m, tiocgptn, unsafe.Pointer(&n)); e != nil {
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

func termiosOf(t *testing.T, f *os.File) syscall.Termios {
	t.Helper()
	var tio syscall.Termios
	if err := ioctlTermios(f.Fd(), ioctlReadTermios, &tio); err != nil {
		t.Fatalf("reading the termios: %v", err)
	}
	return tio
}

func setWinsize(t *testing.T, f *os.File, cols uint16) {
	t.Helper()
	ws := struct{ rows, cols, xpixel, ypixel uint16 }{24, cols, 0, 0}
	if e := ioctlOn(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws)); e != nil {
		t.Fatalf("setting the window size: %v", e)
	}
}

// TestFileTerminalNeedsAConsole is the question the REPL asks before it commits to the
// editor, and the answer that keeps `cat session | mzs --repl` working.
func TestFileTerminalNeedsAConsole(t *testing.T) {
	_, slave := openPTY(t)

	if _, ok := FileTerminal(slave, slave); !ok {
		t.Error("FileTerminal said no to a pseudo-terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if _, ok := FileTerminal(r, w); ok {
		t.Error("FileTerminal said yes to a pipe")
	}
	if _, ok := FileTerminal(r, slave); ok {
		t.Error("FileTerminal said yes to a pipe on stdin")
	}
	if _, ok := FileTerminal(slave, w); ok {
		t.Error("FileTerminal said yes to a pipe on stdout")
	}
	if _, ok := FileTerminal(nil, nil); ok {
		t.Error("FileTerminal said yes to nothing at all")
	}
}

// TestMakeRawSwitchesTheLineDiscipline pins what raw mode is for: keys arrive one at a
// time (no ICANON), the terminal does not echo them (no ECHO) and Ctrl-C is a byte rather
// than a signal (no ISIG) — and all of it is put back afterwards.
func TestMakeRawSwitchesTheLineDiscipline(t *testing.T) {
	_, slave := openPTY(t)
	term, ok := FileTerminal(slave, slave)
	if !ok {
		t.Fatal("FileTerminal said no to a pseudo-terminal")
	}
	before := termiosOf(t, slave)

	restore, err := term.MakeRaw()
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	raw := termiosOf(t, slave)
	for _, f := range []struct {
		name string
		bit  uint32
	}{{"ECHO", syscall.ECHO}, {"ICANON", syscall.ICANON}, {"ISIG", syscall.ISIG}, {"IEXTEN", syscall.IEXTEN}} {
		if raw.Lflag&f.bit != 0 {
			t.Errorf("raw mode left %s on", f.name)
		}
	}
	if raw.Iflag&syscall.IXON != 0 {
		t.Error("raw mode left IXON on: Ctrl-S would freeze the terminal")
	}
	if raw.Oflag&syscall.OPOST == 0 {
		t.Error("raw mode cleared OPOST: a '\\n' from anything else would not return the carriage")
	}
	if raw.Cc[syscall.VMIN] != 1 || raw.Cc[syscall.VTIME] != 0 {
		t.Errorf("VMIN/VTIME = %d/%d, want 1/0", raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME])
	}

	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if after := termiosOf(t, slave); after.Lflag != before.Lflag || after.Iflag != before.Iflag {
		t.Errorf("the terminal was left at %#x/%#x, want %#x/%#x",
			after.Iflag, after.Lflag, before.Iflag, before.Lflag)
	}
}

// TestMakeRawNeedsATerminal: the ioctl is the check. A file descriptor that is not a
// console fails it, and the caller gets an error it can fall back from.
func TestMakeRawNeedsATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if restore, err := (&fileTerm{in: r, out: w}).MakeRaw(); err == nil {
		restore()
		t.Error("MakeRaw succeeded on a pipe")
	}
}

func TestWidth(t *testing.T) {
	master, slave := openPTY(t)
	term, ok := FileTerminal(slave, slave)
	if !ok {
		t.Fatal("FileTerminal said no to a pseudo-terminal")
	}
	setWinsize(t, master, 100)
	if got := term.Width(); got != 100 {
		t.Errorf("Width = %d, want 100", got)
	}
	setWinsize(t, master, 40)
	if got := term.Width(); got != 40 {
		t.Errorf("Width = %d, want 40 — the size is asked for again on every redraw", got)
	}

	// A pipe has no window size, and the editor's answer to that is 80 columns.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if got := (&fileTerm{in: r, out: w}).Width(); got != 0 {
		t.Errorf("Width of a pipe = %d, want 0", got)
	}
}

// TestReadLineOverAPTY is the whole package on a real terminal: raw mode on a real file
// descriptor, keys decoded off a real read, and the drawing landing on a real console.
func TestReadLineOverAPTY(t *testing.T) {
	master, slave := openPTY(t)
	setWinsize(t, master, 80)
	term, ok := FileTerminal(slave, slave)
	if !ok {
		t.Fatal("FileTerminal said no to a pseudo-terminal")
	}

	// Raw first, so that the line discipline neither echoes the keys back nor turns the
	// carriage returns into newlines before the editor ever sees them.
	restore, err := term.MakeRaw()
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer restore()

	keys := "1 + 2\r" + // a plain line
		"12" + left + "0\r" + // an arrow into the middle of it
		up + up + "\r" // and back through the history
	if _, err := master.WriteString(keys); err != nil {
		t.Fatalf("writing the keys: %v", err)
	}

	ed := New(term)
	ed.Highlight = func(src []rune) []string {
		c := make([]string, len(src))
		for i := range c {
			c[i] = "33"
		}
		return c
	}
	var got []string
	for i := 0; i < 3; i++ {
		line, err := ed.ReadLine("mzs> ")
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		got = append(got, line)
	}
	want := []string{"1 + 2", "102", "1 + 2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}

	// What the editor drew is waiting in the terminal's own buffer — three short lines,
	// far less than a pty holds, so it is read here rather than drained in parallel. The
	// deadline is what ends the read: the console stays open, so there is no EOF to wait
	// for.
	if err := master.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Skipf("this terminal takes no read deadline: %v", err)
	}
	var out strings.Builder
	buf := make([]byte, 4096)
	for !drawnEverything(out.String()) {
		n, err := master.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break // the deadline: whatever is here is what was drawn
		}
	}
	if drawn := out.String(); !drawnEverything(drawn) {
		t.Errorf("the terminal was sent %q, want the prompt and the colours", drawn)
	}
}

func drawnEverything(s string) bool {
	return strings.Contains(s, "\x1b[33m") && strings.Contains(s, "mzs> ")
}
