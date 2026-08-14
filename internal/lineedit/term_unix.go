//go:build linux || darwin || dragonfly || freebsd || netbsd || openbsd

package lineedit

import (
	"os"
	"syscall"
	"unsafe"
)

// The Unix console. Raw mode is three ioctls — read the termios, write a modified copy,
// write the original back — and the module has no dependencies (README), so they are
// spelled here rather than pulled in from x/term. The request numbers differ between
// Linux and the BSDs and live in the two files next to this one.
//
// What gets switched off is the kernel's line discipline: ICANON so a key arrives the
// moment it is pressed instead of at the end of a line, ECHO so the editor decides what
// appears, ISIG so Ctrl-C reaches the editor as a byte and abandons the *line* instead of
// killing the process, and IXON so Ctrl-S is a key and not a freeze.
//
// What stays on is OPOST. Nothing in the editor's own drawing depends on it — every line
// it writes carries its own carriage return — but raw mode is entered around a single
// ReadLine, and leaving output post-processing alone means a "\n" written by anything
// else in that window still lands at the start of the next row.

type fileTerm struct {
	in, out *os.File
}

// FileTerminal returns a Terminal over in and out, or false when either of them is not a
// console — a piped script, a redirected log, a test. The check is the termios ioctl
// itself: nothing else answers it, and it is the very call raw mode will need.
func FileTerminal(in, out *os.File) (Terminal, bool) {
	if in == nil || out == nil {
		return nil, false
	}
	var t syscall.Termios
	if ioctlTermios(in.Fd(), ioctlReadTermios, &t) != nil {
		return nil, false
	}
	if ioctlTermios(out.Fd(), ioctlReadTermios, &t) != nil {
		return nil, false
	}
	return &fileTerm{in: in, out: out}, true
}

func (t *fileTerm) Read(p []byte) (int, error)  { return t.in.Read(p) }
func (t *fileTerm) Write(p []byte) (int, error) { return t.out.Write(p) }

func (t *fileTerm) MakeRaw() (func() error, error) {
	var old syscall.Termios
	if err := ioctlTermios(t.in.Fd(), ioctlReadTermios, &old); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	// One byte is enough to wake a read, and no timer runs it out: the editor blocks on
	// the keyboard and nothing else.
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(t.in.Fd(), ioctlWriteTermios, &raw); err != nil {
		return nil, err
	}
	return func() error { return ioctlTermios(t.in.Fd(), ioctlWriteTermios, &old) }, nil
}

func (t *fileTerm) Width() int {
	var ws struct{ rows, cols, xpixel, ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, t.out.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}

func ioctlTermios(fd, req uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}
