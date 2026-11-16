# Filesystem, stdin, env and modules

The `Options` fields that let a script reach outside the process, and the rule that the
host — not the library — resolves every path.

```go
in := mzs.New(mzs.Options{
	FS:           myFS{},          // installs the io module
	Stdin:        os.Stdin,        // io.stdin drains it, io.lines and input() pull it; nil is empty input
	Env:          os.Getenv,       // io.env; nil means every name is unset
	ModuleLoader: myLoader,        // include x from "./x.mzs"
})
```

All four are `nil` by default. With the zero `Options` a script cannot even name the `io`
module:

```
include io   # name: module 'io' needs a filesystem: the host did not install Options.FS
```

## The FileSystem interface

```go
type FileSystem interface {
	Open(name string) (io.ReadCloser, error)      // io.read closes the reader
	Create(name string) (io.WriteCloser, error)   // truncate or create; io.write closes it
	Append(name string) (io.WriteCloser, error)   // create when absent
	Stat(name string) (exists bool, size int64, dir bool, err error)
	List(dir string) ([]string, error)            // entry names, without their path
}
```

**The host resolves paths.** The library never joins, cleans or checks a name — a script's
`"../etc/passwd"` arrives at your `Open` verbatim, and whether that is allowed is your
policy. `ModuleLoader` works the same way, for the same reason.

An error returned from any method becomes an ordinary catchable script error. A file larger
than `MaxStringBytes` is a catchable error too, since the size is a property of what the
world handed over. "Not there" is not an error: `Stat` reports it as `(false, 0, false, nil)`
and `io.exists` answers `false`.

## An in-memory FileSystem

```go
type memFS map[string]string

type memFile struct {
	fs   memFS
	name string
	buf  bytes.Buffer
}

func (f *memFile) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *memFile) Close() error                { f.fs[f.name] = f.buf.String(); return nil }

// check is the whole of the path policy — the only place it can live.
func (m memFS) check(name string) error {
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return fmt.Errorf("%s: outside the sandbox", name)
	}
	return nil
}

func (m memFS) Open(name string) (io.ReadCloser, error) {
	if err := m.check(name); err != nil { return nil, err }
	s, ok := m[name]
	if !ok { return nil, fmt.Errorf("%s: no such file", name) }
	return io.NopCloser(strings.NewReader(s)), nil
}
func (m memFS) Create(name string) (io.WriteCloser, error) {
	if err := m.check(name); err != nil { return nil, err }
	return &memFile{fs: m, name: name}, nil
}
func (m memFS) Append(name string) (io.WriteCloser, error) {
	if err := m.check(name); err != nil { return nil, err }
	f := &memFile{fs: m, name: name}
	f.buf.WriteString(m[name])
	return f, nil
}
func (m memFS) Stat(name string) (bool, int64, bool, error) {
	if err := m.check(name); err != nil { return false, 0, false, err }
	s, ok := m[name]
	return ok, int64(len(s)), false, nil
}
// List returns immediate children by their own name, the way os.ReadDir does — matching
// on the raw prefix would let List("data") see "database.txt", and would return "sub/x"
// for a file a level down.
func (m memFS) List(dir string) ([]string, error) {
	if err := m.check(dir); err != nil { return nil, err }
	prefix := strings.TrimSuffix(dir, "/") + "/"
	if dir == "" || dir == "." { prefix = "" }
	seen := map[string]bool{}
	var out []string
	for name := range m {
		if !strings.HasPrefix(name, prefix) { continue }
		entry, _, _ := strings.Cut(strings.TrimPrefix(name, prefix), "/")
		if entry == "" || seen[entry] { continue }
		seen[entry] = true
		out = append(out, entry)
	}
	sort.Strings(out) // a map range is unordered; a listing should not be
	return out, nil
}
```

Driving it with a script:

```go
fs := memFS{"data/a.txt": "one\ntwo\n"}
in := mzs.New(mzs.Options{
	FS:     fs,
	Stdin:  strings.NewReader("x\ny\n"),
	Env:    func(k string) string { return map[string]string{"STAGE": "dev"}[k] },
	Stdout: os.Stdout,
})
in.Eval(context.Background(), script, nil)
```

```
include io
println("lines: ${io.read("data/a.txt").lines.len}")
println("exists: ${io.exists("data/a.txt")} ${io.exists("data/nope.txt")}")
println("wrote: ${io.write("data/b.txt", "hi")} + ${io.append("data/b.txt", "!")}")
println("ls: ${io.ls("data/")}")
println("env: ${io.env("STAGE", "prod")} ${io.env("MISSING", "prod")}")
println("stdin: ${io.stdin.lines}")
println("missing: ${try io.read("data/nope.txt") else "fallback"}")
println("escape: ${try io.read("../etc/passwd") else "denied"}")
```

```
lines: 2
exists: true false
wrote: 2 + 1
ls: ["a.txt","b.txt"]
env: dev prod
stdin: ["x","y"]
missing: fallback
escape: denied
```

Afterwards `fs["data/b.txt"] == "hi!"`. `io.ls` sorts what `List` returned, so a script that
lists a directory prints the same thing twice in a row.

`Stdin` is drained once per Run and cached: the second `io.stdin` of a program answers what
the first one read; `nil` is empty input, not an error. It is also what the global
[`input(prompt)`](../stdlib/core.md#input) reads — one line per call, with no `include`,
because the console is the pair of fields a host wires together: `Stdout` for `print` and
`Stdin` for `input`. A host that installs neither has a script that prompts nobody and
hears `nil`. `Env` is usually `os.Getenv`; `nil`
means every name is unset, and an empty result counts as unset, so
`io.env("STAGE", "prod")` falls back the way `${STAGE:-prod}` does in a shell.

## Walking and changing a tree: four optional interfaces

`FileSystem` never grows, so the implementation above keeps compiling. Four members of `io`
are backed by interfaces of their own instead, and each member exists only when your
`FileSystem` implements its interface:

```go
type LinkStater interface {   // io.walk
	Lstat(name string) (exists bool, size int64, dir, link bool, err error)
}
type FileRemover interface { Remove(name string) error }     // io.rm
type DirMaker    interface { MkdirAll(name string) error }   // io.mkdir
type FileMover   interface { Rename(from, to string) error } // io.mv
```

**The three that change a tree also need `Options.FSMutations: true`.** Go interfaces are
satisfied implicitly, so a `FileSystem` that already had a `Remove` method for reasons of its
own must not hand scripts `io.rm` when you upgrade mzs. The field is the deliberate act, and
its zero value keeps every existing host where it was:

```go
in := mzs.New(mzs.Options{FS: myFS{}, FSMutations: true})
```

A member you did not grant is absent, and using it is a compile error that says which half
is missing:

```
io.rm needs the host to allow structural changes: Options.FSMutations is off (mzs without --read-only)
io.walk needs a filesystem that can tell a link from a directory: Options.FS does not implement LinkStater
```

What each method is expected to do:

* **`Remove`** deletes a file, an *empty* directory or a link (not what it points to). A
  name that does not exist is an error for which `errors.Is(err, fs.ErrNotExist)` holds —
  `os.Remove`'s errors do — and `io.rm` answers `false` for it instead of raising.
* **`MkdirAll`** creates the directory and every missing parent; one that already exists
  is not an error, as with `os.MkdirAll`.
* **`Rename`** moves within the filesystem and replaces a file at `to`, as `os.Rename` does.
* **`Lstat`** is `Stat` for the name itself: `link` is true for a symbolic link, and then
  `dir` and `size` describe the link. Not there is `(false, 0, false, false, nil)`.

`io.walk` lists with `List`, looks at each entry with `Lstat`, and never descends into a
name `Lstat` called a link — so on a tree that does not change while it runs, a walk cannot
leave the tree through a link or go round a cycle of them. If a directory can be replaced by
a link *while* the walk runs and that matters to you, the guarantee is yours to keep in
`List`: it must follow no link in **any** component of the path, not only the last.
`O_NOFOLLOW` covers the last component only, and an `os.Root` confines a walk to its tree
but still follows links that stay inside it, so neither is enough on its own. On Linux it
means opening each component in turn with `openat(…, O_NOFOLLOW|O_DIRECTORY)` from the one
before, or `openat2` with `RESOLVE_NO_SYMLINKS`. Other cycles — a bind mount of an ancestor
— are not links; bound those in your own `List` if you have them.

A read-only host is one whose `Create` and `Append` fail and which implements none of the
three mutating interfaces — the CLI's `--read-only` is exactly that.

## ModuleLoader

```go
type ModuleLoader func(from, path string) (resolved string, src string, err error)
```

`from` is the including program's name, `path` is the string the script wrote. Return the
resolved name — the key the per-Run module cache uses, so two spellings of one file load
once — and its source.

```go
in := mzs.New(mzs.Options{
	ModuleLoader: func(from, p string) (string, string, error) {
		resolved := path.Clean(path.Join(path.Dir(from), p))
		if strings.HasPrefix(resolved, "..") {
			return "", "", fmt.Errorf("%s: outside the module root", p)
		}
		src, ok := lib[resolved]                       // lib is map[string]string
		if !ok { return "", "", fmt.Errorf("%s: not found", resolved) }
		return resolved, src, nil
	},
})
prog, _ := in.Compile("app/main.mzs", `
include m from "../lib/math.mzs"
"${m.double(21)} / ${m.limit}"`)
v, err := in.Run(ctx, prog, nil)   // v.Inspect() == "\"42 / 10\"", err == nil
```

`CompileIncludes: true` lets `Compile` call the loader as well, once per `include … from`
at the top level of a file, to learn the field names of the records the module exports
(`m.amount`, `m.amount = v`, `money.Money {amount} ->` across files). Nothing else is kept:
each `Run` still asks the loader and runs the module. It is off by default because a loader
need not answer a second read the way it answered the first; turn it on when yours is a
plain read of something that does not change under it.

Failures are `name` errors that quote what the loader said:

```
no loader:     name: cannot include "./x.mzs": the host did not enable module loading
loader error:  name: cannot include "./nope.mzs": nope.mzs: not found
```

Built-in modules (`json`, `math`, `time`, `date`, `http`, `io`, `path`, …) never go through the loader.

## See also

- [./README.md](./README.md) — the full `Options` table
- [./functions.md](./functions.md) — adding capabilities the interfaces do not cover
- [../modules/io.md](../modules/io.md) — the `io` module from the script side
- [../reference/sandbox.md](../reference/sandbox.md) — what is granted by default
