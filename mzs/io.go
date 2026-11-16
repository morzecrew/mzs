package mzs

import (
	"bufio"
	"errors"
	"io"
	iofs "io/fs"
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
//
// Five members reach the shape of the tree rather than one file: io.stat answers what
// io.exists already fetched, io.walk lists a tree, and io.rm, io.mkdir and io.mv change one.
// FileSystem never grew for them. Each of the last four is backed by an optional interface
// the host's filesystem may implement (LinkStater, FileRemover, DirMaker, FileMover), and
// the three that change a tree also need Options.FSMutations — the host saying so, since a
// FileSystem may satisfy an interface by accident. A member whose gate is closed is absent
// from the module, and naming it is a compile error that says which half is missing.
//
// The third way of asking is `input` (§12.1), which is a global rather than a member of
// this module and is implemented here anyway, because it reads the same reader and there
// is only ever one implementation of a thing (D17).

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
	RegisterModuleFunc("io", "stat", 1, 1, iovStat)
	RegisterModuleFunc("io", "walk", 0, 1, iovWalk)
	RegisterModuleFunc("io", "rm", 1, 1, iovRm)
	RegisterModuleFunc("io", "mkdir", 1, 1, iovMkdir)
	RegisterModuleFunc("io", "mv", 2, 2, iovMv)

	// io.walk reads, so it needs only a filesystem that can tell a link from a directory;
	// the three members that change a tree need the host's explicit consent on top.
	setMemberGate("io", "walk", func(o *Options) string {
		if _, ok := o.FS.(LinkStater); ok {
			return ""
		}
		return "io.walk needs a filesystem that can tell a link from a directory: Options.FS does not implement LinkStater"
	})
	setMemberGate("io", "rm", mutationGate("io.rm", "FileRemover", func(fs FileSystem) bool { _, ok := fs.(FileRemover); return ok }))
	setMemberGate("io", "mkdir", mutationGate("io.mkdir", "DirMaker", func(fs FileSystem) bool { _, ok := fs.(DirMaker); return ok }))
	setMemberGate("io", "mv", mutationGate("io.mv", "FileMover", func(fs FileSystem) bool { _, ok := fs.(FileMover); return ok }))

	// `input` is a §12.1 global and not a member of this module — see bivInput for why
	// the console's reading half needs no include when its writing half never did.
	RegisterBuiltin(Builtin{Name: "input", Max: 1, Fn: bivInput})
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
					if sh.stdinTakenBy == "" {
						sh.stdinTakenBy = "io.lines"
					}
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
			line, ok, err := ioReadLine(c, c.rs.sh.stdinRd, "io.lines")
			if err != nil || !ok {
				mode = exhausted
				return Nil(), false, err
			}
			return Str(line), true, nil
		}
	})
}

// ---------------------------------------------------------------------------
// input (§12.1)
// ---------------------------------------------------------------------------

// bivInput is the global `input(prompt = nil)`: write the prompt, read one line, hand it
// back without its terminator, and answer `nil` when there is no line left.
//
// It is a §12.1 builtin and not a member of this module because a console is one thing
// with two directions, and the half that writes has never needed an include: `print` and
// `input` are the pair, exactly as `print` and `println` are. What the pair may *reach*
// is still the host's to grant — `print` writes to Options.Stdout and `input` reads
// Options.Stdin, and a host that installed neither has a script that can prompt nobody
// and hear nothing rather than one that fails to compile. That is the shape every other
// stream capability already has (§14.3): the CLI's `--no-io` withholds the reader too, so
// `input` is `nil` under it.
//
// The implementation lives in io.go because the reader does. `input` is one pull of the
// same stdin `io.stdin` drains and `io.lines` streams (§12.13); a second implementation
// of "one line off the input" is the kind of thing D17 exists to forbid.
func bivInput(c *Ctx, args []Value) (Value, error) {
	if len(args) > 0 {
		// The prompt is written before the read and gets no newline of its own: the
		// answer is typed where the cursor already is. Nothing here flushes, because
		// nothing here buffers — Options.Stdout is the host's writer, and the CLI's is
		// the console itself.
		if err := writeOut(c, c.Out(), args[0].Str()); err != nil {
			return Nil(), err
		}
	}
	// One line is one step, the charge `io.lines` pays per element. The 1000 of
	// ioStepCost is what reaching the *filesystem* costs and no line of input does, which
	// is what keeps `while (line = input()) { … }` over a large pipe a loop the budget
	// bounds at the rate of the work rather than 1000× that.
	if err := c.Step(1); err != nil {
		return Nil(), err
	}
	line, ok, err := inputLine(c)
	if err != nil || !ok {
		return Nil(), err
	}
	return Str(line), nil
}

// inputLine pulls one line from whichever form the input has already taken, so that
// `input` composes with the module instead of competing with it (§12.13):
//
//   - `io.stdin` has been drained → the lines of that cached text, from a cursor the Run
//     shares, so consecutive `input()` calls answer consecutive lines and the whole text
//     is still there for anything that asks again;
//   - otherwise → straight off the reader, which from then on is what the line-at-a-time
//     members have: a later `io.stdin` raises the "already read line by line" error of
//     §12.13 naming `input`, because the bytes really are gone.
//
// The end of the input is `nil` and not an error, and so is a host that installed no
// reader at all: `while (line = input()) { … }` is the loop this shape is for, and a
// script written for a pipe still runs outside one.
func inputLine(c *Ctx) (string, bool, error) {
	sh := c.rs.sh
	if sh.stdinRead {
		// The cursor walks the cached string rather than a []string of its lines: the
		// text is already in memory once and splitting it again on every prompt would put
		// it there twice. The rules are textLines': the CR of a CRLF belongs to the
		// terminator, and a trailing newline ends the text instead of starting an empty
		// last line.
		if sh.stdinAt >= len(sh.stdin) {
			return "", false, nil
		}
		rest := sh.stdin[sh.stdinAt:]
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			sh.stdinAt = len(sh.stdin)
			return rest, true, nil
		}
		sh.stdinAt += i + 1
		return strings.TrimSuffix(rest[:i], "\r"), true, nil
	}
	if c.rs.opts.Stdin == nil {
		return "", false, nil
	}
	if sh.stdinTakenBy == "" {
		// The member that took the reader is the one the later `io.stdin` names, so a
		// second taker does not overwrite the first: what a script wants to be told is
		// where its input went, and it went to whichever spelling reached it first.
		sh.stdinTakenBy = "input"
	}
	if sh.stdinRd == nil {
		sh.stdinRd = bufio.NewReader(c.rs.opts.Stdin)
	}
	return ioReadLine(c, sh.stdinRd, "input")
}

// ioReadLine reads one line, without its terminator and without a trailing CR, under
// MaxStringBytes — which now bounds one *line* rather than the whole input, and is the
// only bound a streaming read can have. A line over it is the catchable io error an
// oversized read has always been (§14.2), never a truncation.
//
// `what` is the member asking — `io.lines` or `input` — because a diagnostic about the
// input should name the spelling the script actually used.
//
// The lock is held throughout, for the reason ioStdinText gives: two tasks pulling from
// one reader would take each other's lines.
func ioReadLine(c *Ctx, r *bufio.Reader, what string) (string, bool, error) {
	max := c.rs.opts.MaxStringBytes
	tooLong := func() error {
		// Failing the *source* and not just this pull: what is left of the line that
		// overran is not a line, and handing it back on the next pull would turn a
		// diagnostic into silent corruption. There is no resynchronising on a boundary
		// that is not there, so the input stops being readable line by line here.
		c.rs.sh.stdinLineBad = true
		return c.ErrorfKind(ErrKindIO, "%s: a line exceeds the %d byte limit", what, max)
	}
	if c.rs.sh.stdinLineBad {
		return "", false, c.ErrorfKind(ErrKindIO, "%s: a line exceeds the %d byte limit", what, max)
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
			// One byte of slack: the fragment may end on the '\r' of a CRLF whose '\n'
			// is in the next chunk, and that '\r' is a terminator rather than content.
			if len(buf)-1 > max {
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
			return "", false, c.ErrorfKind(ErrKindIO, "%s: %v", what, err)
		}
		// The CR of a CRLF is part of the terminator, so it is dropped before the line is
		// measured: a file off a Windows machine reaches the same limit as any other.
		line := strings.TrimSuffix(string(buf), "\r")
		if len(line) > max {
			return "", false, tooLong()
		}
		return line, true, nil
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
	if sh.stdinTakenBy != "" {
		// The bytes are gone and the rest of them is not "the whole of stdin". Say which
		// member has them rather than answering "" and letting a script conclude that
		// nothing was piped in (§12.13).
		return "", c.ErrorfKind(ErrKindIO, "io.stdin: the input has already been read line by line by %s", sh.stdinTakenBy)
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

// iovStat is `io.stat(path)`: `{size: int, dir: bool}` for a name that exists and `nil`
// for one that does not, from the same FileSystem.Stat io.exists asks — which already
// fetched both and threw them away. It follows links, as Stat does.
func iovStat(c *Ctx, args []Value) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	var (
		ok   bool
		size int64
		dir  bool
		serr error
	)
	c.blocking(func() { ok, size, dir, serr = fs.Stat(path) })
	if serr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.stat %s: %v", quoteString(path), serr)
	}
	if !ok {
		return Nil(), nil
	}
	d := NewOrderedDict()
	_ = d.Set(Str("size"), Int(size))
	_ = d.Set(Str("dir"), Bool(dir))
	return dictOf(d), nil
}

// ---------------------------------------------------------------------------
// Structure: walk, rm, mkdir, mv
// ---------------------------------------------------------------------------

// mutationGate is the member gate of a member that changes the shape of a tree: it needs
// Options.FSMutations, which is the host saying so, and an FS that can do it (§12.13).
// The sentence names whichever of the two is missing, because they are fixed in
// different places.
func mutationGate(member, iface string, has func(FileSystem) bool) func(*Options) string {
	return func(o *Options) string {
		var missing []string
		if !o.FSMutations {
			missing = append(missing, "Options.FSMutations is off")
		}
		if !has(o.FS) {
			missing = append(missing, "Options.FS does not implement "+iface)
		}
		if len(missing) == 0 {
			return ""
		}
		// The CLI grants both unless it was told --read-only, so that is the one thing a
		// person at a command line can change; an embedder reads the field names.
		return member + " needs the host to allow structural changes: " + strings.Join(missing, " and ") + " (mzs without --read-only)"
	}
}

// iovWalk is `io.walk(dir = ".")`: every path under dir, depth-first, each directory's
// entries in sorted order (§8.13), as a lazy seq (§12.14) — a tree is listed one
// directory at a time, as far as the script reads it, and a second traversal walks it
// again.
//
// A directory is yielded with a trailing `/` and then entered; a symbolic link is yielded
// as a name and never entered, whatever it points to, so a walk of a tree that does not
// change while it runs cannot follow a link out of the tree or round a cycle of them.
// What happens under concurrent changes is the host's List to settle (LinkStater). The
// root is the path the script named with its trailing `/`s removed — `link/` would ask the
// host for the directory the link names — and nothing else about it is touched: `..` in
// it is not resolved, because `missing/../x` must fail on the host exactly as it would
// anywhere else, and a link among its parent components resolves as it would for io.read,
// since which names a script may reach is the host's policy. A root that is itself a link
// is an error rather than an empty walk.
//
// The paths are the root joined with what is under it, so `io.walk("src")` yields
// `src/a.mzs`; under the default root `.` they are the bare relative names `a.mzs`, the
// names io.ls answers. Each List and each Lstat is one filesystem operation and is charged
// as one (ioStepCost).
func iovWalk(c *Ctx, args []Value) (Value, error) {
	root := Str(".")
	if len(args) > 0 && !args[0].IsNil() {
		root = args[0]
	}
	fs, dir, err := ioTarget(c, root)
	if err != nil {
		return Nil(), err
	}
	ls, ok := fs.(LinkStater)
	if !ok {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.walk: Options.FS does not implement LinkStater")
	}
	for len(dir) > 1 && strings.HasSuffix(dir, "/") {
		dir = dir[:len(dir)-1]
	}
	// The root lives in the cursor's closure, so it is recorded on the seq as well: the
	// memory walk counts what the seq holds through refs, and a long root kept alive in a
	// walk nobody has finished must be counted like any other string (§14.2).
	return seqOf(walkSeq(fs, ls, dir).with(Str(dir))), nil
}

// walkFrame is one directory of a walk in progress: its path, and its sorted entries once
// it has been listed.
type walkFrame struct {
	dir    string
	listed bool
	names  []string
	at     int
}

func walkSeq(fs FileSystem, ls LinkStater, root string) *Seq {
	return seqSource(func() func(*Ctx) (Value, bool, error) {
		var (
			stack   []*walkFrame
			started bool
			done    bool
			// held is how many names the open directories hold between them. A walk
			// keeps each ancestor's listing while it is inside a child, so the bound that
			// matters is the sum down the current branch, not one directory's count.
			held int
		)
		fail := func(err error) (Value, bool, error) {
			done = true
			return Nil(), false, err
		}
		return func(c *Ctx) (Value, bool, error) {
			if done {
				return Nil(), false, nil
			}
			if !started {
				started = true
				if err := c.Step(ioStepCost); err != nil {
					return fail(err)
				}
				var (
					ok, dir, link bool
					lerr          error
				)
				c.blocking(func() { ok, _, dir, link, lerr = ls.Lstat(root) })
				switch {
				case lerr != nil:
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: %v", quoteString(root), lerr))
				case !ok:
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: no such directory", quoteString(root)))
				case link:
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: is a symbolic link, and walk does not follow links", quoteString(root)))
				case !dir:
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: not a directory", quoteString(root)))
				}
				stack = append(stack, &walkFrame{dir: root})
			}
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if !top.listed {
					top.listed = true
					if err := c.Step(ioStepCost); err != nil {
						return fail(err)
					}
					var (
						names []string
						lerr  error
					)
					c.blocking(func() { names, lerr = fs.List(top.dir) })
					if lerr != nil {
						return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: %v", quoteString(top.dir), lerr))
					}
					if err := c.CheckCollection(held + len(names)); err != nil {
						return fail(err)
					}
					held += len(names)
					sort.Strings(names)
					top.names = names
				}
				if top.at >= len(top.names) {
					held -= len(top.names)
					stack = stack[:len(stack)-1]
					continue
				}
				name := top.names[top.at]
				top.at++
				if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
					// Not an entry of the directory but a way back into it or above it:
					// a List that answered one would otherwise send the walk round in a
					// circle the step budget is the only end of.
					continue
				}
				p := walkJoin(top.dir, name)
				// A yielded path is a string the script gets, and a deep enough tree makes
				// one longer than MaxStringBytes (a directory's with its `/`). Like a read
				// that size, it is the world's doing rather than the script's, so it is a
				// catchable io error naming the root, not a limit — and it is checked before
				// the host is asked anything about a name the walk could not hand over.
				tooLong := func() (Value, bool, error) {
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: a path under it exceeds the %d byte limit", quoteString(root), c.rs.opts.MaxStringBytes))
				}
				if len(p) > c.rs.opts.MaxStringBytes {
					return tooLong()
				}
				if err := c.Step(ioStepCost); err != nil {
					return fail(err)
				}
				var (
					ok, dir, link bool
					lerr          error
				)
				c.blocking(func() { ok, _, dir, link, lerr = ls.Lstat(p) })
				switch {
				case lerr != nil:
					return fail(c.ErrorfKind(ErrKindIO, "io.walk %s: %v", quoteString(p), lerr))
				case !ok:
					// Listed and gone by the time it was looked at: a walk reports what
					// is there, and this no longer is.
					continue
				case link:
					return Str(p), true, nil
				case dir:
					if len(p)+1 > c.rs.opts.MaxStringBytes {
						return tooLong()
					}
					stack = append(stack, &walkFrame{dir: p})
					return Str(p + "/"), true, nil
				default:
					return Str(p), true, nil
				}
			}
			done = true
			return Nil(), false, nil
		}
	})
}

// walkJoin puts an entry under the directory it was listed from. Under the root `.` the
// entry is its own path, the way io.ls names it; under `/` there is no second separator.
func walkJoin(dir, name string) string {
	switch {
	case dir == ".":
		return name
	case strings.HasSuffix(dir, "/"):
		return dir + name
	}
	return dir + "/" + name
}

// iovRm is `io.rm(path)`: remove a file, an empty directory or a link, and answer whether
// there was one to remove — `false` for a name that was not there, as io.exists answers
// for it. It is not recursive and there is no flag that makes it so: a tree is removed by
// walking it, where the script has to spell out what it is deleting.
func iovRm(c *Ctx, args []Value) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	rm, err := ioMutator[FileRemover](c, fs)
	if err != nil {
		return Nil(), err
	}
	var rerr error
	c.blocking(func() { rerr = rm.Remove(path) })
	switch {
	case rerr == nil:
		return Bool(true), nil
	case errors.Is(rerr, iofs.ErrNotExist):
		return Bool(false), nil
	}
	return Nil(), c.ErrorfKind(ErrKindIO, "io.rm %s: %v", quoteString(path), rerr)
}

// iovMkdir is `io.mkdir(path)`: the directory and every missing parent. One that is
// already there is not an error — the script asked for a directory, and there is one.
func iovMkdir(c *Ctx, args []Value) (Value, error) {
	fs, path, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	mk, err := ioMutator[DirMaker](c, fs)
	if err != nil {
		return Nil(), err
	}
	var merr error
	c.blocking(func() { merr = mk.MkdirAll(path) })
	if merr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.mkdir %s: %v", quoteString(path), merr)
	}
	return Nil(), nil
}

// iovMv is `io.mv(from, to)`: a rename within the filesystem, replacing a file at to.
func iovMv(c *Ctx, args []Value) (Value, error) {
	fs, from, err := ioTarget(c, args[0])
	if err != nil {
		return Nil(), err
	}
	to, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}
	mv, err := ioMutator[FileMover](c, fs)
	if err != nil {
		return Nil(), err
	}
	var merr error
	c.blocking(func() { merr = mv.Rename(from, to) })
	if merr != nil {
		return Nil(), c.ErrorfKind(ErrKindIO, "io.mv %s %s: %v", quoteString(from), quoteString(to), merr)
	}
	return Nil(), nil
}

// ioMutator is the run-time half of a mutation member's gate. Through `include io` it
// cannot fail — the gate is what installs the member — but a host that builds the members
// into a module of its own with RegisterModule has bypassed the gate, and the check that
// a script cannot change a tree without Options.FSMutations must not depend on how the
// member was reached.
func ioMutator[T any](c *Ctx, fs FileSystem) (T, error) {
	var zero T
	if !c.rs.opts.FSMutations {
		return zero, c.ErrorfKind(ErrKindIO, "%s: the host did not set Options.FSMutations", c.Name())
	}
	m, ok := fs.(T)
	if !ok {
		return zero, c.ErrorfKind(ErrKindIO, "%s: Options.FS cannot do this", c.Name())
	}
	return m, nil
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
