# The io module

`include io` gives a script stdin, files and the environment — eight members, and only when
the host handed over a filesystem.

```sh
$ mzs -e 'include io; io.write("notes.txt", "café\nbar\n")'
10
$ mzs -e 'include io; io.append("notes.txt", "baz\n")'
4
$ mzs -e 'include io; io.read("notes.txt").lines' --json
["café","bar","baz"]
$ mzs -e 'include io; io.exists("notes.txt")'
true
$ mzs -e 'include io; io.ls' --json
["notes.txt"]
$ mzs -e 'include io; io.env("MZS_MODE", "dev")'
dev
```

## Members

| Member | Signature | Returns |
|---|---|---|
| `stdin` | `-> string` | all of the data stream, read once per Run |
| `lines` | `-> array` | `io.stdin` split on lines, terminator dropped |
| `read` | `(path) -> string` | the whole file |
| `write` | `(path, s) -> int` | bytes written; truncates or creates |
| `append` | `(path, s) -> int` | bytes written; creates when absent |
| `exists` | `(path) -> bool` | `false` for a name that is not there |
| `ls` | `(dir = ".") -> array` | entry names, sorted |
| `env` | `(name, default = nil) -> string \| default` | unset **or empty** takes the default |

Counts differ on purpose: `write`/`append` return **bytes**, everything else in the language
counts **runes**.

```sh
$ mzs -e 'include io; s = "café\n"; [s.len, io.write("one.txt", s)]'
[5,6]
```

## There is no rm, mkdir or open

The eight members above are the whole module, and an unknown member is an error naming it.

```sh
$ mzs -e 'include io; io.rm("notes.txt")'
-e:1:16: name: undefined member 'rm' in module 'io'
  include io; io.rm("notes.txt")
                 ^
```

Reading, writing and listing is what a script needs; creating and removing directories is the
shell's job, or the host's. `io.open` does not exist either — there are no file handles, only
whole strings.

## stdin

`io.stdin` is read once per Run and cached, so a second read answers what the first one got;
`io.lines` is that same text split.

```sh
$ printf 'one\ntwo\n' | mzs -e 'include io; [io.stdin.lines.len, io.stdin.lines.len, io.lines.len]' --json
[2,2,2]
$ printf 'a\r\nb\r\n' | mzs -e 'include io; io.lines' --json
["a","b"]
```

No reader at all is not an error — the text is `""` and the lines are `[]`, so one script runs
both in a pipe and out of one.

```sh
$ mzs -e 'include io; inspect(io.stdin)' < /dev/null
""
```

The CLI feeds `io.stdin` from the **data** stream, never from the program (see
[../cli/input.md](../cli/input.md)). Under `-n` / `-l` the CLI owns the reader — it is splitting
it into `$_` one line at a time — so `io.stdin` is empty there:

```sh
$ printf 'a\nb\n' | mzs -n -e 'include io; inspect(io.stdin) + " / " + $_'
"" / a
"" / b
```

`--in <path>` names the data explicitly, which is what makes program-on-the-pipe work:

```sh
$ printf 'D1\nD2\n' > data.txt
$ echo 'include io; io.lines' | mzs --in data.txt - --json
["D1","D2"]
```

## Errors

Everything the outside world can refuse is an ordinary catchable error of kind `io`
naming the path.

```sh
$ mzs -e 'include io; io.read("gone.txt")'
-e:1:16: io: io.read "gone.txt": open gone.txt: no such file or directory
  include io; io.read("gone.txt")
                 ^
$ mzs -e 'include io; try io.read("gone.txt") else "(default)"'
(default)
$ mzs -e 'include io; try io.read("gone.txt") else (e) -> e["message"]'
io.read "gone.txt": open gone.txt: no such file or directory
```

A missing name is `false` from `io.exists`, not an error — not being there is the answer the
question asked for. Oversize is an error too, not a truncation:

```sh
$ mzs -t 20 -e 'include io; try io.read("huge.txt") else (e) -> e["message"]'
io.read "huge.txt": exceeds the 8388608 byte limit
```

Every member that reaches the filesystem charges 1000 steps before it starts — `io.env` charges
none, and `io.stdin`/`io.lines` only on the read that drains the reader — so a loop over a
directory spends the budget at the rate of the work it does:

```sh
$ mzs --steps 2000 -e 'include io; (0..4).map { io.exists("notes.txt") }.len'
-e:1:29: limit: step budget exceeded (2000 steps)
  include io; (0..4).map { io.exists("notes.txt") }.len
                              ^
```

A limit error is not catchable and ends the Run with exit code 3; a file that is merely
missing is not a limit.

## The host decides what a path means

Nothing in the module joins, cleans or judges a name: what a script writes reaches
`Options.FS` verbatim, and whether `"../../etc/shadow"` is allowed is the policy of the code
that implemented the interface.

```go
type FileSystem interface {
	Open(name string) (io.ReadCloser, error)
	Create(name string) (io.WriteCloser, error)
	Append(name string) (io.WriteCloser, error)
	Stat(name string) (exists bool, size int64, dir bool, err error)
	List(dir string) ([]string, error)
}
```

The library ships no implementation. The CLI ships one, and its policy is the loosest there
is — the person typing the command line already owns the machine:

```sh
$ mzs -e 'include io; io.exists("/etc/passwd")'
true
$ mzs -e 'include io; io.ls("/etc").has("hostname")'
true
```

`Options.Stdin` and `Options.Env` are separately optional inside the module: without a reader
`io.stdin` is `""`, without an `Env` every name is unset and `io.env` returns its default.

## Without a filesystem the include does not compile

`io` is installed only when the host supplies an `Options.FS`. With the zero `Options` the name
does not exist, and `--no-io` is how the CLI takes the capability back for one command — no
`FS`, no `Env`, no `Stdin`:

```sh
$ mzs --no-io -e 'include io; io.stdin'
-e:1:9: name: module 'io' needs a filesystem: the host did not install Options.FS
  include io; io.stdin
          ^
```

That is a **compile** error, so `--check` catches it without running anything, and the exit
code is 1. `--no-io` does not take `-n` away: the CLI reads the lines, not the script.

A worked program using every member is [../../examples/32_io_files.mzs](../../examples/32_io_files.mzs).

## See also

- [./README.md](./README.md) — include, module rules, the module table
- [./http.md](./http.md) — the other module that reaches outside the process, and needs no option
- [../cli/input.md](../cli/input.md) — pipe vs data, `--in`, `-n` line mode
- [../embedding/filesystem.md](../embedding/filesystem.md) — writing an `FS`, `Stdin`, `Env`
