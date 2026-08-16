package mzs

import (
	"bufio"
	"errors"
	"io"
	"sort"
	"strings"
)

// The `io` module (§12.13) — stdin, files and the environment, and the second and last
// place the standard library reaches outside the process.
//
// It is the mirror image of `http` (§12.11). `http` is installed unconditionally because
// the CLI's work is scripts that fetch and serve, and a flag on every command line bought
// nothing; `io` is installed only when the host hands over an Options.FS, because reading
// a file is the capability an embedder is most likely to be embedding mzs to avoid. A
// dialogue condition out of a store (§19) must not be able to open /etc/passwd, and with
// the zero Options it cannot even name the module: `include io` is a compile error that
// says which field is missing.
//
// The host writes the FileSystem, so **the host resolves paths**. Nothing here joins,
// cleans or checks a name — a script's "../../etc/shadow" arrives at the interface
// verbatim, and whether that is allowed is the policy of the code that implemented it,
// exactly as it already is for `include … from` and ModuleLoader (§12.8). The library
// contains no code that opens a real file; the CLI does, because the CLI's host is the
// person who typed the command line.
//
// Everything that can go wrong on the way to a file is an ordinary catchable error —
// `try io.read(p) else ""` is how a script handles a missing file (§8.11) — and a read
// larger than MaxStringBytes is one too, not a limit: the size is a property of what the
// world handed over rather than of the script's own arithmetic, and nothing is evaded by
// catching it, because the reader stops at the limit either way (§14.2).
//
// Options.Stdin and Options.Env are separately optional inside the module: with no reader
// `io.stdin` is "" and with no Env every name is unset, so a script written for a pipe
// still runs where there is none instead of failing on line one. `io.stdin` and `io.lines`
// are two ways of asking that one reader — the whole text, or a line at a time — and the
// order they are asked in decides what the second one can still have.

func init() {
	SetModuleGate("io", ModuleGate{NeedsFS: true})
	RegisterModuleFunc("io", "stdin", 0, 0, iovStdin)
	RegisterModuleFunc("io", "lines", 0, 0, iovLines)
	RegisterModuleFunc("io", "read", 1, 1, iovRead)
	RegisterModuleFunc("io", "write", 2, 2, iovWrite)
	RegisterModuleFunc("io", "append", 2, 2, iovAppend)
	RegisterModuleFunc("io", "exists", 1, 1, iovExists)
	RegisterModuleFunc("io", "ls", 0, 1, iovLs)
	RegisterModuleFunc("io", "env", 1, 2, iovEnv)
}

// ioStepCost is what one filesystem operation is charged before it starts. It is the
// same figure http.go charges a request, and for the same reason: a loop over a
// directory must spend the Run's budget at the rate of the work it actually does, not at
// the rate of the one AST node that spells it (§14.1).
const ioStepCost = 1000

// ---------------------------------------------------------------------------
// stdin
// ---------------------------------------------------------------------------

// iovStdin is `io.stdin`: the whole of Options.Stdin, read once and kept for the rest of
// the Run. A reader can only be drained once, so the second `io.stdin` of a program must
// answer what the first one read — not "".
func iovStdin(c *Ctx, args []Value) (Value, error) {
	s, err := ioStdinText(c)
	if err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

// iovLines is the lines of the input as a **seq** (§12.14): the split of §12.2 over the
// same text, one line at a time. The terminator is dropped, so a trailing newline does not
// add an empty last field, and a CRLF file reads like an LF one.
//
// It is lazy because this is the member a gigabyte arrives through. `io.stdin` cannot be:
// it promises the whole text and stops at MaxStringBytes with a diagnostic (§14.2). A seq
// promises one line, so `io.lines.filter { … }.take(10)` reads exactly as far as it must
// and holds one line at a time — which is what makes a log larger than the limit something
// a script can process rather than something it can only refuse.
func iovLines(c *Ctx, args []Value) (Value, error) {
	// One member, one charge, before any of it happens — the same 1000 steps every other
	// member of the module pays. The per-line cost is the seq's own step per element.
	if err := c.Step(ioStepCost); err != nil {
		return Nil(), err
	}
	return seqOf(ioLineSeq()), nil
}

// ioLineSeq is the source behind io.lines. Which of the two ways to read the input it
// takes is decided at the **first pull**, not when io.lines is written, because a program
// may name the seq on one line and consume it several lines later:
//
//   - `io.stdin` has already been read → the lines of that cached text, so a script that
//     asks for both loses nothing and the whole input is still there for a second look;
//   - otherwise → straight off the reader, and from then on the reader is what io.lines
//     has, which is what a pipe is.
//
// A second traversal of the streaming form sees what is left of the reader — usually
// nothing. That is the "a stateful source is stateful" rule of §12.14 in its plainest
// form, and materialising with `.array` is how a script that needs two looks takes them.
func ioLineSeq() *Seq {
	return seqSource(func() func(*Ctx) (Value, bool, error) {
		// Which of the two the cursor took is a flag of its own and not "cached != nil":
		// the lines of an empty text are no lines at all, and reading that as "not the
		// cached form" would send the cursor to a reader that is not there.
		const (
			unopened = iota
			fromCache
			fromReader
			exhausted
		)
		var (
			mode   = unopened
			cached []string
			at     int
		)
		return func(c *Ctx) (Value, bool, error) {
			if mode == unopened {
				sh := c.rs.sh
				switch {
				case sh.stdinRead:
					mode, cached = fromCache, textLines(sh.stdin)
				case c.rs.opts.Stdin != nil:
					mode = fromReader
					sh.stdinLines = true
					if sh.stdinRd == nil {
						sh.stdinRd = bufio.NewReader(c.rs.opts.Stdin)
					}
				default:
					mode = exhausted
				}
			}
			if mode == exhausted {
				return Nil(), false, nil
			}
			if mode == fromCache {
				if at >= len(cached) {
					return Nil(), false, nil
				}
				line := cached[at]
				at++
				return Str(line), true, nil
			}
			line, ok, err := ioReadLine(c, c.rs.sh.stdinRd)
			if err != nil || !ok {
				mode = exhausted
				return Nil(), false, err
			}
			return Str(line), true, nil
		}
	})
}

// ioReadLine reads one line, without its terminator and without a trailing CR, under
// MaxStringBytes — which now bounds one *line* rather than the whole input, and is the
// only bound a streaming read can have. A line over it is the catchable io error an
// oversized read has always been (§14.2), never a truncation.
//
// The lock is held throughout, for the reason ioStdinText gives: two tasks pulling from
// one reader would take each other's lines.
func ioReadLine(c *Ctx, r *bufio.Reader) (string, bool, error) {
	max := c.rs.opts.MaxStringBytes
	tooLong := func() error {
		return c.ErrorfKind(ErrKindIO, "io.lines: a line exceeds the %d byte limit", max)
	}
	var buf []byte
	for {
		// ReadSlice hands back a window on the reader's own buffer, so the append is what
		// makes the line ours; it answers ErrBufferFull for a line longer than that buffer
		// and io.EOF for a last line with no terminator.
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			if len(buf) > max {
				return "", false, tooLong()
			}
			continue
		case err == nil:
			buf = buf[:len(buf)-1] // the '\n' itself is not part of the line
		case errors.Is(err, io.EOF):
			// A file that ends with a newline has no empty last line, which is what
			// textLines says for the same input read whole.
			if len(buf) == 0 {
				return "", false, nil
			}
		default:
			return "", false, c.ErrorfKind(ErrKindIO, "io.lines: %v", err)
		}
		if len(buf) > max {
			return "", false, tooLong()
		}
		return strings.TrimSuffix(string(buf), "\r"), true, nil
	}
}

// ioStdinText drains Options.Stdin into the Run's shared cache.
//
// The read happens with the interpreter's lock held, unlike every other wait in this
// package. That is deliberate: "drain exactly once" is an invariant across the whole Run,
// and releasing the lock mid-read would let a task of §8.14 start a second read of a
// reader that has already given its bytes away. One Run reads stdin once, and the tasks
// that ask for it afterwards get the string.
func ioStdinText(c *Ctx) (string, error) {
	sh := c.rs.sh
	if sh.stdinRead {
		return sh.stdin, nil
	}
	if sh.stdinLines {
		// The bytes are gone and the rest of them is not "the whole of stdin". Say which
		// member has them rather than answering "" and letting a script conclude that
		// nothing was piped in (§12.13).
		return "", c.ErrorfKind(ErrKindIO, "io.stdin: the input has already been read line by line by io.lines")
	}
	sh.stdinRead = true
	r := c.rs.opts.Stdin
	if r == nil {
		return "", nil
	}
	if err := c.Step(ioStepCost); err != nil {
		return "", err
	}
	b, err := ioReadLimited(c, r, "io.stdin", ioLockedRead)
	if err != nil {
		return "", err
	}
	sh.stdin = string(b)
	return sh.stdin, nil
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// iovRead is `io.read(path)`: the whole file as a string, bounded by MaxStringBytes.
func iovRead(c *Ctx, args []Value) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}

	var (
		rc   io.ReadCloser
		oerr error
	)
	c.blocking(func() { rc, oerr = fs.Open(path) })
	if oerr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.read %s: %v", quoteString(path), oerr)
	}
	defer rc.Close()

	b, err := ioReadLimited(c, rc, "io.read "+quoteString(path), ioUnlockedRead)
	if err != nil {
		return Nil(), err
	}
	return Str(string(b)), nil
}

// iovWrite is `io.write(path, s)`, truncating or creating, and returns the number of
// bytes written — which is what a script checks, and the only thing it could check
// without a stat it did not ask for.
func iovWrite(c *Ctx, args []Value) (Value, error) {
	return ioPut(c, "io.write", args, func(fs FileSystem, path string) (io.WriteCloser, error) {
		return fs.Create(path)
	})
}

// iovAppend is `io.append(path, s)`: the same, onto the end of a file that may not exist
// yet. It is a member of its own rather than an option of io.write because appending to a
// log is not "writing with a flag" — the two are different operations, and D17 asks for
// one name each.
func iovAppend(c *Ctx, args []Value) (Value, error) {
	return ioPut(c, "io.append", args, func(fs FileSystem, path string) (io.WriteCloser, error) {
		return fs.Append(path)
	})
}

// ioPut is the shared body of write and append: check the arguments, open the way the
// caller asked, write, close. Close is checked as carefully as Write, because a buffered
// implementation reports a full disk there and nowhere else.
func ioPut(c *Ctx, name string, args []Value, open func(FileSystem, string) (io.WriteCloser, error)) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	text, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}

	var (
		wc   io.WriteCloser
		oerr error
	)
	c.blocking(func() { wc, oerr = open(fs, path) })
	if oerr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "%s %s: %v", name, quoteString(path), oerr)
	}

	var (
		n     int
		werr  error
		cerr  error
		bytes = []byte(text)
	)
	c.blocking(func() {
		n, werr = wc.Write(bytes)
		cerr = wc.Close()
	})
	if werr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "%s %s: %v", name, quoteString(path), werr)
	}
	if cerr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "%s %s: %v", name, quoteString(path), cerr)
	}
	return Int(int64(n)), nil
}

// iovExists is `io.exists(path)`. A name that is not there is `false`, not an error: not
// being there is the answer the question asked for. A filesystem that fails to answer at
// all — a permission error on the directory above — still raises, because that is a
// different thing from "no".
func iovExists(c *Ctx, args []Value) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	var (
		ok   bool
		serr error
	)
	c.blocking(func() { ok, _, _, serr = fs.Stat(path) })
	if serr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.exists %s: %v", quoteString(path), serr)
	}
	return Bool(ok), nil
}

// iovLs is `io.ls(dir = ".")`: the entry names of a directory, sorted. The order is the
// module's, not the filesystem's, because a script that lists a directory and prints it
// must print the same thing twice in a row (§8.13).
func iovLs(c *Ctx, args []Value) (Value, error) {
	dir := Str(".")
	if len(args) > 0 && !args[0].IsNil() {
		dir = args[0]
	}
	fs, path, err := ioTarget(c, dir)
	if err != nil {
		return Nil(), err
	}

	var (
		names []string
		lerr  error
	)
	c.blocking(func() { names, lerr = fs.List(path) })
	if lerr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.ls %s: %v", quoteString(path), lerr)
	}
	if err := c.CheckCollection(len(names)); err != nil {
		return Nil(), err
	}
	sort.Strings(names)
	out := make([]Value, len(names))
	for i, n := range names {
		out[i] = Str(n)
	}
	return arrayOf(out), nil
}

// ---------------------------------------------------------------------------
// Environment
// ---------------------------------------------------------------------------

// iovEnv is `io.env(name, default = nil)`. An unset name is the default, and an empty
// value counts as unset — which is what Options.Env being an os.Getenv-shaped function
// means, and what `${HOME:-/tmp}` already means to everyone who will type this.
func iovEnv(c *Ctx, args []Value) (Value, error) {
	name, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	def := argAt(args, 1)
	env := c.rs.opts.Env
	if env == nil {
		return def, nil
	}
	if v := env(name); v != "" {
		return Str(v), nil
	}
	return def, nil
}

// ---------------------------------------------------------------------------
// Shared plumbing
// ---------------------------------------------------------------------------

// ioTarget is the check every member that touches a file starts with: a string path, a
// filesystem to hand it to, and a step charged before any of it happens. The nil check is
// unreachable through `include io` — the gate is what installs the module — and is here
// for the host that builds the members into a module of its own with RegisterModule.
func ioTarget(c *Ctx, v Value) (FileSystem, string, error) {
	path, err := argStr(c, v)
	if err != nil {
		return nil, "", err
	}
	fs := c.rs.opts.FS
	if fs == nil {
		return nil, "", c.ErrorfKind(ErrKindIO, "%s: the host did not install Options.FS", c.Name())
	}
	if err := c.Step(ioStepCost); err != nil {
		return nil, "", err
	}
	return fs, path, nil
}

// Whether the wait inside a read releases the interpreter for the Run's other tasks
// (§8.14). A file read may — nothing else in the Run can touch that reader — and the
// stdin drain may not, for the reason ioStdinText gives.
const (
	ioUnlockedRead = true
	ioLockedRead   = false
)

// ioReadLimited reads r under MaxStringBytes. Overrunning it is not a truncation: a
// script that asked for a file gets either the file or a diagnostic, never a prefix it
// cannot tell from the whole. The diagnostic is a catchable error and not a limit,
// exactly as an oversized HTTP response is (§12.11, §14.2): the size is a property of
// what the world handed over, so `try io.read(p) else …` is the right way to meet it,
// and nothing is evaded by catching it — the reader stops at the limit either way.
func ioReadLimited(c *Ctx, r io.Reader, what string, unlocked bool) ([]byte, error) {
	max := c.rs.opts.MaxStringBytes
	var (
		b    []byte
		rerr error
	)
	read := func() { b, rerr = io.ReadAll(io.LimitReader(r, int64(max)+1)) }
	if unlocked {
		c.blocking(read)
	} else {
		read()
	}
	if rerr != nil {
		return nil, c.ErrorfKind(ErrKindIO, "%s: %v", what, rerr)
	}
	if len(b) > max {
		return nil, c.ErrorfKind(ErrKindIO, "%s: exceeds the %d byte limit", what, max)
	}
	return b, nil
}
