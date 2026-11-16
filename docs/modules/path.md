# The `path` module

File names taken apart and put together — lexically, and always with `/`.

```
include path

path.join("in", name)            # "in/report.json" — one separator, cleaned
path.ext("a/b.tar.gz")           # ".gz"
path.stem("a/b.tar.gz")          # "b.tar"
```

`"in/" + name` is not a join: it gives `in//x` for a name that starts with a slash and
`inx` when someone drops the slash. `name.ends_with(".json")` is not an extension test
either. The module needs no host capability — no member looks at a filesystem, the working
directory or the platform, so every answer depends on the arguments alone — and
`include path` is the whole of it ([sandbox](../reference/sandbox.md)).

## Members

| Call | Result |
|---|---|
| `path.join(*parts)` | the parts joined with `/` and cleaned; empty parts dropped; `""` for none |
| `path.dir(p)` | everything before the last element, cleaned; `"."` when there is none |
| `path.base(p)` | the last element, without trailing `/`s |
| `path.ext(p)` | the last `.suffix` of the base, with the dot; `""` for `.gitignore` |
| `path.stem(p)` | the base without the extension |
| `path.clean(p)` | `.`, `..` and doubled `/` resolved; `"."` for `""` |
| `path.split(p)` | the elements of the cleaned path, as an array |
| `path.rel(p, base)` | `p` relative to `base`, or `nil` when no relative path says it |
| `path.is_abs(p)` | whether `p` starts with `/` |
| `path.match(pattern, name)` | a shell glob over one name, with `**` across directories |

```sh
$ mzs -e 'include path; path.join("in", "../out", "x.json")'
out/x.json
$ mzs -e 'include path; path.join("a/", "/b", "", "c")'
a/b/c
$ mzs -e 'include path; [path.dir("a/b/c.txt"), path.dir("a/b/"), path.dir("x")]' --json
["a/b","a","."]
$ mzs -e 'include path; [path.base("a/b/"), path.stem("b.tar.gz"), path.ext("b.tar.gz")]' --json
["b","b.tar",".gz"]
```

A trailing `/` is not an element of its own: `dir("a/b/")` is `"a"` and `base("a/b/")` is
`"b"`, the same split either way. The last element is taken off before anything is
cleaned, so `dir("a/b/..")` is `"a/b"` — the directory the `..` is written in. `stem(p) + ext(p) == base(p)` for every `p`.

### The extension of a hidden file

A name whose only dot is its first character has no extension — the dot hides the file, it
does not say what is in it:

```sh
$ mzs -e 'include path; [path.ext(".gitignore"), path.ext("a/.profile.bak")]' --json
["",".bak"]
```

### split and join

`split` is the elements of the cleaned path, with a rooted path starting at `"/"`, so
`path.join(*path.split(p))` is `path.clean(p)`:

```sh
$ mzs -e 'include path; path.split("/usr/local/bin")' --json
["/","usr","local","bin"]
$ mzs -e 'include path; path.split(".")' --json
[]
```

### rel

`rel(p, base)` is what `join(base, …)` needs to reach `p`. It is `nil` when no relative path
can say it: one of the two is rooted and the other is not, or `base` climbs out with `..`
further than `p` does — the answer would need the name of a directory neither argument
contains.

```sh
$ mzs -e 'include path; [path.rel("a/b/c", "a"), path.rel("a", "a/b/c")]' --json
["b/c","../.."]
$ mzs -e 'include path; [path.rel("/etc", "a"), path.rel("x", "..")]' --json
[null,null]
```

## Lexical means lexical

`clean` resolves `..` against the element written before it. On a real disk that element
may be a symbolic link, and `a/link/..` may not be `a` — the module never asks, because it
never looks. Which names a script may reach, and how a `/` name becomes the platform's own,
is the host's `FileSystem` to decide ([io](./io.md#the-host-decides-what-a-path-means)).

```sh
$ mzs -e 'include path; [path.clean("a//b/./c/.."), path.clean("/../x")]' --json
["a/b","/x"]
```

There is no `path.native` and no `\` separator: a Windows host converts in its
`FileSystem`, where it converts every other name the script hands it.

## match

`*` and `?` match inside one element, `[a-c]` and `[^a-c]` are classes and `\` escapes. An
element that is exactly `**` matches any number of elements, none included:

```sh
$ mzs -e 'include path; [path.match("*.mzs", "src/a.mzs"), path.match("**/*.mzs", "src/a.mzs")]' --json
[false,true]
$ mzs -e 'include path; path.match("src/**/test_*.mzs", "src/test_a.mzs")'
true
$ mzs -e 'include path; path.match("[", "x")'
-e:1:20: argument: path.match: malformed pattern "["
```

The name is matched as written. A directory that [`io.walk`](./io.md#trees-stat-walk-rm-mkdir-mv)
yields as `src/lib/` ends in an empty element, which `**` matches and `*.mzs` does not —
so a walk filtered by a pattern for files never lists a directory:

```sh
$ mzs -e 'include path; [path.match("**/*.mzs", "src/lib/"), path.match("src/**", "src/lib/")]' --json
[false,true]
$ mzs -e 'include io; include path; io.walk.filter { path.match("**/*.mzs", it) }.array' --json
["src/a.mzs","src/lib/b.mzs"]
```

That is the glob; there is no `io.glob`. A match never backtracks across elements, so a
pattern of many `**` is no slower than it looks, and it is charged up front for the work
inside each element too — a pattern built to be slow runs out of step budget before it
runs.

`match` is a keyword, so it is only ever reached as `path.match(…)`.

## See also

- [./io.md](./io.md) — `io.walk`, `io.mkdir`, `io.mv`, `io.rm`, and the filesystem the host supplies
- [./README.md](./README.md) — include, module rules, the module table
- [../../examples/51_file_tree.mzs](../../examples/51_file_tree.mzs) — `path` and `io.walk` on a real tree
