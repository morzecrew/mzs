# Filesystem, stdin, env and modules

The four `Options` fields that let a script reach outside the process, and the rule that
the host — not the library — resolves every path.

```go
in := mzs.New(mzs.Options{
	FS:           myFS{},          // installs the io module
	Stdin:        os.Stdin,        // io.stdin drains it, io.lines streams it; nil is empty input
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
the first one read; `nil` is empty input, not an error. `Env` is usually `os.Getenv`; `nil`
means every name is unset, and an empty result counts as unset, so
`io.env("STAGE", "prod")` falls back the way `${STAGE:-prod}` does in a shell.

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

Failures are `name` errors that quote what the loader said:

```
no loader:     name: cannot include "./x.mzs": the host did not enable module loading
loader error:  name: cannot include "./nope.mzs": nope.mzs: not found
```

Built-in modules (`json`, `math`, `time`, `date`, `http`, `io`) never go through the loader.

## See also

- [./README.md](./README.md) — the full `Options` table
- [./functions.md](./functions.md) — adding capabilities the interfaces do not cover
- [../modules/io.md](../modules/io.md) — the `io` module from the script side
- [../reference/sandbox.md](../reference/sandbox.md) — what is granted by default
