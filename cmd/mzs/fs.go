package main

import (
	"errors"
	"io"
	"os"

	"mzs/mzs"
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

// Lstat answers io.walk, which must see a link as a link: os.Stat follows it.
func (osFS) Lstat(name string) (bool, int64, bool, bool, error) {
	fi, err := os.Lstat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, false, false, nil
		}
		return false, 0, false, false, err
	}
	return true, fi.Size(), fi.IsDir(), fi.Mode()&os.ModeSymlink != 0, nil
}

// The three structural operations (§12.13), each the os call a shell command would make:
// rm on one name (never recursive), mkdir -p, mv. They exist for scripts because the CLI
// sets Options.FSMutations — the person typing the command already owns the machine — and
// --read-only is how that person takes them back for one command.
func (osFS) Remove(name string) error     { return os.Remove(name) }
func (osFS) MkdirAll(name string) error   { return os.MkdirAll(name, 0o777) }
func (osFS) Rename(from, to string) error { return os.Rename(from, to) }

// readOnlyFS is the filesystem under --read-only: everything that reads, and nothing that
// writes. It does not implement FileRemover, DirMaker or FileMover, so io.rm, io.mkdir and
// io.mv are absent even before Options.FSMutations is consulted; io.write and io.append
// are part of FileSystem itself and exist, and fail when called.
type readOnlyFS struct{ fs osFS }

var errReadOnly = errors.New("the filesystem is read-only (mzs --read-only)")

func (r readOnlyFS) Open(name string) (io.ReadCloser, error)     { return r.fs.Open(name) }
func (readOnlyFS) Create(string) (io.WriteCloser, error)         { return nil, errReadOnly }
func (readOnlyFS) Append(string) (io.WriteCloser, error)         { return nil, errReadOnly }
func (r readOnlyFS) Stat(name string) (bool, int64, bool, error) { return r.fs.Stat(name) }
func (r readOnlyFS) List(dir string) ([]string, error)           { return r.fs.List(dir) }
func (r readOnlyFS) Lstat(name string) (bool, int64, bool, bool, error) {
	return r.fs.Lstat(name)
}

// hostFS is what options() installs. It is a function rather than a value so the flag
// that turns the module off (`--no-io`) has one place to return nil from, and the flag
// that takes writing away (`--read-only`) one place to choose the filesystem that cannot.
func hostFS(readOnly bool) mzs.FileSystem {
	if readOnly {
		return readOnlyFS{}
	}
	return osFS{}
}
