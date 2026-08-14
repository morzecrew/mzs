//go:build linux

package lineedit

import "syscall"

// The termios ioctls on Linux. The BSDs spell them differently; see term_ioctl_bsd.go.
const (
	ioctlReadTermios  = syscall.TCGETS
	ioctlWriteTermios = syscall.TCSETS
)
