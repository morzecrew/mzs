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
| `lines` | `-> seq` | the input a line at a time, terminator dropped ([../stdlib/sequences.md](../stdlib/sequences.md)) |
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

`io.stdin` is read once per Run and cached, so a second read answers what the first one got.
`io.lines` is the same input a **line at a time**: a [seq](../stdlib/sequences.md), so it
chains like an array and holds one line rather than all of them.

```sh
$ printf 'one\ntwo\n' | mzs -e 'include io; [io.stdin.lines.len, io.stdin.lines.len, io.lines.len]' --json
[2,2,2]
$ printf 'a\r\nb\r\n' | mzs -e 'include io; io.lines.array' --json
["a","b"]
$ printf 'a\nb\n' | mzs -e 'include io; io.lines.map { it.upper }.take(1).array' --json
["A"]
```

That is what makes an input larger than `MaxStringBytes` something a script can process
rather than only refuse: `io.stdin` promises the whole text and stops at the limit,
`io.lines` promises one line.

```sh
$ mzs --in big.log -e 'include io; io.lines.count { it.has("ERROR") }'   # a 15 MB file
128
$ mzs --in big.log -e 'include io; io.stdin.len'
-e:1:16: io: io.stdin: exceeds the 8388608 byte limit
```

**The two are one reader**, and which is asked first decides what the other can still have.
`io.stdin` first is the lossless order — it keeps the whole text, and every later `io.lines`
splits that string. `io.lines` first takes the reader, and a later `io.stdin` says so
instead of answering `""`:

```sh
$ printf 'a\nb\n' | mzs -e 'include io; [io.lines.len, io.stdin.len]'
-e:1:31: io: io.stdin: the input has already been read line by line by io.lines
```

A line longer than `MaxStringBytes` is a catchable `io` error — the limit bounds one line,
which is the only bound a streaming read can have — and it ends the source: what is left of
that line is not a line, so every later pull reports the same failure rather than handing
back the rest of it as data. The CR of a CRLF is part of the terminator and is not
measured.

For the same reason a second walk of a streamed `io.lines` sees what is left of the reader —
usually nothing. A script that needs two looks takes them from an array:

```sh
$ printf 'a\nb\n' | mzs -e 'include io; ls = io.lines.array; [ls.len, ls.len]' --json
[2,2]
```

### `input` is the third way of asking

[`input(prompt)`](../stdlib/core.md#input) is the same reader, one line at a time, and it
is a plain global — no `include` — because it is the reading half of `print`. Prompting is
what it adds:

```sh
$ printf 'Иван\n' | mzs -e 'input("Имя: ")'
Имя: Иван
```

It counts as a line-at-a-time read, so it takes the reader the way `io.lines` does and a
later `io.stdin` says which member has the bytes:

```sh
$ printf 'a\nb\n' | mzs -e 'include io; input(); io.stdin'
-e:1:25: io: io.stdin: the input has already been read line by line by input
```

After an `io.stdin` there is nothing to take — the text is cached, and consecutive prompts
walk it:

```sh
$ printf 'a\nb\n' | mzs -e 'include io; [io.stdin.len, input(), input(), io.stdin.len]' --json
[4,"a","b",4]
```

`input` and `io.lines` interleave off one reader rather than racing for it, so a script may
prompt for a header and stream the rest:

```sh
$ printf 'a\nb\nc\n' | mzs -e 'include io; [input(), io.lines.array]' --json
["a",["b","c"]]
```

No reader at all is not an error — the text is `""`, the lines are empty and `input()` is
`nil`, so one script runs both in a pipe and out of one.

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
$ echo 'include io; io.lines.array' | mzs --in data.txt - --json
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
none, and `io.stdin`/`io.lines` charge once for the read itself, plus one step per line — so a loop over a
directory spends the budget at the rate of the work it does. `input` reaches no filesystem
and charges one step per line and nothing else, which is what keeps a read loop over a large
pipe bounded by the work rather than by 1000× it:

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
- [../stdlib/core.md](../stdlib/core.md#input) — `input`, the prompting one-line read that needs no include
- [../embedding/filesystem.md](../embedding/filesystem.md) — writing an `FS`, `Stdin`, `Env`
