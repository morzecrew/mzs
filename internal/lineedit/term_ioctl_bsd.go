//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package lineedit

import "syscall"

// The termios ioctls on the BSDs, macOS included. Same three calls as on Linux, other
// request numbers; see term_ioctl_linux.go.
const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)
