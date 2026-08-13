package main

import (
	"io"
	"os"

	"mzs"
)

// The CLI's answer to mzs.FileSystem (SPEC §12.13). The library ships no implementation
// of this interface on purpose — a script's filesystem is the host's policy, and mzs the
// package is a library that must be safe to embed in a bot that evaluates conditions
// somebody else wrote (§14.3). The CLI is the host where that policy is simple: the
// person typing the command line already owns the machine, so `mzs -e 'io.read(p)'` reads
// the file the shell would have read, with the process's own permissions and nothing
// narrower.
//
// That is deliberately looser than the `include … from` loader above, which *is* confined
// to the script's directory. The two are different questions: an include is the program
// reaching for more of itself, and a path in it comes from a file that may have travelled;
// io.read is the user naming a file on the command line they are typing. Narrowing the
// first costs nothing and closes a real hole; narrowing the second would only mean
// `mzs -e 'include io; io.read("/etc/hostname")'` does not work, which is the whole point
// of the module.
type osFS struct{}

func (osFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) }

func (osFS) Create(name string) (io.WriteCloser, error) { return os.Create(name) }

func (osFS) Append(name string) (io.WriteCloser, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

// Stat separates "not there" from "cannot tell": the first is io.exists answering false,
// the second is an error a script can catch.
func (osFS) Stat(name string) (bool, int64, bool, error) {
	fi, err := os.Stat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, false, nil
		}
		return false, 0, false, err
	}
	return true, fi.Size(), fi.IsDir(), nil
}

// List returns entry names without their directory, which is what `ls` shows and what
// io.ls promises. The order is the module's business: it sorts.
func (osFS) List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

// hostFS is what options() installs. It is a function rather than a value so the flag
// that turns the module off (`--no-io`) has one place to return nil from.
func hostFS() mzs.FileSystem { return osFS{} }
