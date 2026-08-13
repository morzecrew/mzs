# Install and run

How to build the `mzs` binary and every way to hand it a program.

## Build

```sh
git clone https://github.com/morzecrew/mzs.git && cd mzs
go build ./...                # build every package
go install ./cmd/mzs          # puts the binary in $(go env GOPATH)/bin
```

`go.mod` asks for **Go 1.26** and contains no `require` line at all:

```sh
$ go list -m all
mzs
```

Zero external modules, no cgo, no subprocesses. A release build is one file that needs
nothing at run time:

```sh
$ CGO_ENABLED=0 go build -ldflags="-s -w" -o mzs ./cmd/mzs
$ file mzs
mzs: ELF 64-bit LSB executable, x86-64, ..., statically linked, ..., stripped
```

Copy that file anywhere — no interpreter, no shared library, no `GOPATH`.

```sh
$ mzs --version
mzs 2.0.0
```

## Run a file

```
say("hello")
say(6 * 7)
```

```sh
$ mzs hello.mzs
hello
42
```

A file is quiet: it prints what it prints with `say`/`print`, and the value of the last
expression is **not** printed unless you ask with `-p` or `--json`.

```sh
$ echo '6 * 7' > answer.mzs
$ mzs answer.mzs            # prints nothing
$ mzs -p answer.mzs
42
$ mzs --json answer.mzs
42
```

## Run a file with arguments

Everything after the script name is the script's, not the CLI's, and arrives in `$ARGV`.

```
say("argv: ${$ARGV}")
say($ARGV.len)
```

```sh
$ mzs args.mzs one two
argv: ["one","two"]
2
```

## Run a one-liner

`-e` prints the last value by default; so do `-l` and the REPL, and `--json` turns
printing on even for a file.

```sh
$ mzs -e '"  ОПЕРАТОР ".lower.trim == "оператор"'
true
$ mzs -e '{a: 1, b: [1, 2]}' --json
{"a":1,"b":[1,2]}
$ mzs --bool -e '"yes" == "yes"'; echo $?
0
$ mzs --bool -e 'false'; echo $?
1
```

`-e` is repeatable and the pieces are joined with newlines. `--bool` prints nothing and
answers with the exit code.

## Run from stdin

With no `-e` and no file, the pipe carries the **program**:

```sh
$ echo 'say("from stdin"); 6 * 7' | mzs
from stdin
$ echo '6 * 7' | mzs - -p
42
```

## Run once per input line

With `-e` or a file the program is already in hand, so the pipe carries **data** instead.
`-n` runs the program for every line, and the line is `$_`.

```sh
$ printf 'a\nbb\nccc\n' | mzs -n -e '"${$_}: ${$_.len}"'
a: 1
bb: 2
ccc: 3
```

`$variables` survive from line to line, locals do not. `--in <path>` names the data file
explicitly, which is how the program can come through the pipe at the same time.

## The REPL

```sh
$ mzs
mzs 2.0.0 — .help for help, .exit to quit
mzs> 6 * 7
42
```

The REPL starts by itself when stdin is a terminal and no program was given; `--repl`
forces it. See [repl.md](./repl.md).

## Checking without running

```sh
$ mzs --check hello.mzs
hello.mzs: ok
```

Exit codes: `0` ok, `1` a run-time error (or a falsy value under `--bool`), `2` a bad
argument, `3` a timeout or an exhausted step budget.

## See also

* [repl.md](./repl.md) — the interactive session
* [cheatsheet.md](./cheatsheet.md) — one-liners to paste after `-e`
* [../cli/README.md](../cli/README.md) — every flag, and the printing rules in full
* [../cli/input.md](../cli/input.md) — pipe versus data, `--in`, `-n` and `-l`
