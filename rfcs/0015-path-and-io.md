# RFC 0015 — The `path` module, and the rest of `io`

**Status:** Draft · **Area:** stdlib, embedding · **Depends on:** — · **Audit:** S4

## Summary

Two parts. (1) A pure module `path` for taking file names apart and putting them together.
(2) Five more members on `io` — `stat`, `rm`, `mkdir`, `mv`, `walk` — each backed by an
*optional* interface the host's `FileSystem` may implement, so that no existing host
breaks and no host grants a capability by accident.

This RFC disagrees with a sentence in SPEC §12.13 and says so: "There is no `io.rm`, no
`io.mkdir` … anything structural is the host's to do, or the shell's."

## Motivation

`io` reads, writes, appends, lists and tests existence. A script that processes a
directory of files hits three walls at once:

```
for name in io.ls("in") {
  src = "in/" + name                        # joining by hand; "in//x" and "in" + "/x" both happen
  next if !name.ends_with(".json")          # extension by string match
  …
  io.write("out/" + name, result)           # fails if out/ does not exist — and cannot create it
}
# and the temp file written along the way can never be deleted
```

* `io.ls` gives names with no way to tell a file from a directory, though
  `FileSystem.Stat` already returns `(exists, size, dir)` — the information is fetched and
  thrown away by `io.exists`.
* There is no recursion, so "every `.mzs` under `src/`" is not writable.
* Cleanup is impossible. "The shell's to do" is a fair answer for a one-liner and not for
  `mzs script.mzs`, which is the other half of the CLI's stated work.

The §12.13 position protects embedders, and this design keeps that protection: nothing
becomes available to a host that does not opt in.

## Design

### 1. `path` — pure, ungated

```
include path
path.join("in", name)            # "in/report.json" — one separator, cleaned
path.ext("a/b.tar.gz")           # ".gz"
path.stem("a/b.tar.gz")          # "b.tar"
```

| Member | Signature | Semantics |
|---|---|---|
| `join` | `(*parts: string) -> string` | joined with `/`, then `clean`ed; empty parts are dropped |
| `dir` | `(p) -> string` | everything before the last element; `"."` when there is none |
| `base` | `(p) -> string` | the last element |
| `ext` | `(p) -> string` | the last `.suffix` of `base`, with the dot; `""` if none or if the name starts with its only dot (`.gitignore`) |
| `stem` | `(p) -> string` | `base` without `ext` |
| `clean` | `(p) -> string` | resolves `.`, `..` and doubled separators lexically |
| `split` | `(p) -> array` | the elements: `"a/b/c"` → `["a","b","c"]` |
| `rel` | `(p, base) -> string \| nil` | `p` relative to `base`, lexically; `nil` when it cannot be expressed |
| `is_abs` | `(p) -> bool` | starts with `/` |
| `match` | `(pattern, name) -> bool` | shell glob on one name: `*`, `?`, `[a-z]`, `**` across separators |

Purely lexical and **always `/`** — the module never looks at the filesystem or the
platform, so its answers are deterministic (D15) and it needs no gate. Converting to the
platform's separator is the `FileSystem` implementation's job, as path policy already is
(§12.13). Because `match` is a keyword, it is reached as `path.match(…)` — `MethodName`
already admits keywords (§4).

### 2. `io` — five members behind optional interfaces

```go
// Each is optional. io.<member> exists only when Options.FS implements it.
type FileRemover interface { Remove(name string) error }          // a file or an EMPTY directory
type DirMaker    interface { MkdirAll(name string) error }
type FileMover   interface { Rename(from, to string) error }
```

| Member | Signature | Needs | Semantics |
|---|---|---|---|
| `stat` | `(path) -> dict \| nil` | nothing new | `{size: int, dir: bool}`, or `nil` when absent. Uses the existing `FileSystem.Stat` |
| `walk` | `(dir = ".") -> seq` | nothing new | every path under `dir`, depth-first, sorted per directory (§8.13), lazily (§12.14). Directories are yielded with a trailing `/`. Built on `List` + `Stat` |
| `rm` | `(path) -> bool` | `FileRemover` | removes a file or an empty directory; `false` when it was not there. **Not recursive** — a tree is removed by walking it, where the script has to say so |
| `mkdir` | `(path) -> nil` | `DirMaker` | creates the directory and its parents; existing is fine |
| `mv` | `(from, to) -> nil` | `FileMover` | rename within the filesystem; replaces a file at `to` |

When the host's `FS` lacks the interface the member is **absent**, and the compile-time
diagnostic names the reason, as `include io` does today:
`name: io.rm needs the host's filesystem to allow removal (FileRemover)`.

`FileSystem` itself is untouched, so every existing implementation compiles (§21.1).
The CLI's filesystem implements all three: "the person typing `mzs -e` already owns the
machine". `--no-io` withholds everything, as now; a new `--read-only` withholds `write`,
`append`, `rm`, `mkdir` and `mv`.

`walk` does not follow symbolic links, and the CLI's confinement rules for `include`
(§15) are unaffected: `io` has always been unconfined on the CLI by design.

## Compatibility

Additive: a new module, new members, optional Go interfaces. The contradicted sentence of
§12.13 is rewritten to describe the opt-in instead.

## Alternatives

* **Keep the §12.13 line.** Coherent, and it leaves "write a temp file, delete it" outside
  the language permanently. The optional-interface route gets the capability without
  weakening any default.
* **`io.glob(pattern)`.** Subsumed by `io.walk(dir).filter { path.match("**/*.mzs", it) }`,
  which composes and is lazy.
* **Recursive `rm`.** The most dangerous one-liner in computing; not in a language whose
  variables are `nil` when unset (`io.rm($dir + "/")`).
* **mtime in `stat`.** Needs the clock capability to be meaningful and an interface change
  to fetch; deferred until `FileSystem` has a reason to grow.

## Open questions

* Should `io.ls` gain a counterpart that returns `stat` dicts, or is `ls` + `stat` enough?
* Windows: `path` is `/`-only by design. Is a `path.native(p)` needed for printing?
* Temp files: `io.temp()` needs randomness and a directory policy — the host's. Deferred.

## Implementation

`mzs/path.go` (over Go's `path`, not `path/filepath`), `mzs/io.go`, `mzs/options.go`
(three interfaces), `cmd/mzs/fs.go` (implement them; `--read-only`), SPEC §12.13 + a new
`path` section, §13.2, §14.3, `docs/modules/io.md`, `docs/embedding/filesystem.md`.
