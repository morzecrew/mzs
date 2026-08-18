# Input: the pipe, `--in`, line mode

How a program and its data reach `mzs`, and what `-n` does with them.

## The pipe carries either the program or the data, never both

A reader has one set of bytes. If the program already came from `-e` or from a file, stdin
is free to be data and reaches the script as `io.stdin` / `io.lines`. If it did not, stdin
is the program.

```sh
$ printf 'alpha 1\nbeta 2\ngamma 3\n' > data.txt

$ cat data.txt | mzs -e 'include io; io.lines.len'      # program in -e, so stdin is data
3

$ printf 'include io; println(io.lines.len)\n' | mzs        # stdin is the program
0

$ mzs -e 'include io; [io.stdin, io.lines.array]' < /dev/null # no data is not an error
["",[]]
```

[`input(prompt)`](../stdlib/core.md#input) reads that same data stream one line at a
time, and needs no `include`:

```sh
$ printf 'Иван\n' | mzs -e 'input("Имя: ")'
Имя: Иван
```

`io.lines` is a [seq](../stdlib/sequences.md) — the input a line at a time, so a file
larger than `MaxStringBytes` is ordinary work where `io.stdin` would refuse it. The two
share one reader: ask for the whole text first and every later `io.lines` splits it; stream
the lines first and `io.stdin` says so rather than answering `""`.

## `--in` names the data explicitly

`--in <path>` always wins, which is what lets the program stay on the pipe:

```sh
$ printf '$_.len\n' > lenprog.mzs
$ cat lenprog.mzs | mzs --in data.txt -l
7
6
7
```

It also beats a stdin that does have data:

```sh
$ printf 'ignored\n' | mzs -n --in data.txt -e '$_'
alpha 1
beta 2
gamma 3
```

## `-n` and `-l`

`-n` runs the same compiled program once per line of the data stream, with the line in
`$_`. The terminator is dropped, CRLF like LF.

```sh
$ cat data.txt | mzs -n -e '$_.split(" ")[0]'
alpha
beta
gamma

$ cat data.txt | mzs -l -e '$_.split(" ")[1].int * 10'
10
20
30
```

`-l` is `-n` plus "print each value that is not `nil`", which is what makes a *script file*
or a piped program speak — `-e` already prints. With `-p`, nils print too:

```sh
$ cat data.txt | mzs -n -e 'if $_.starts_with("b") { $_ }'
beta 2

$ cat data.txt | mzs -n -p -e 'if $_.starts_with("b") { $_ }'

beta 2

```

## What carries between lines

Each line is its own run: its own frame, its own timeout, its own step budget. `$variables`
are fed back in between lines; locals are not.

```sh
$ cat data.txt | mzs -n -e '$n = ($n ?? 0) + 1'
1
2
3

$ cat data.txt | mzs -n -e 'n = (n ?? 0) + 1'
-e:1:6: name: undefined variable 'n' (did you mean 'debug'?)
  n = (n ?? 0) + 1
       ^
```

Because the budget is per line, a `-t 0.2` run over a slow input takes as long as the input
does. Reach for `io.lines` when the program needs to see the input as a whole — it is still
read a line at a time, so the memory cost is the same as `-n`'s.

Under `-n` the CLI owns the reader, so `io.stdin` is `""` and `input()` is `nil` — the line
is in `$_`:

```sh
$ cat data.txt | mzs -n -e 'include io; "[" + io.stdin + "]"'
[]
[]
[]
```

## Streaming

Lines are read as they arrive, not slurped, so `tail -f | mzs -n …` prints as the file
grows:

```sh
$ { echo one; sleep 1; echo two; } | timeout 0.5 mzs -n -e '$_'
one
```

An input line longer than 4 MiB fails the read (exit 1) rather than delivering half a line:

```sh
$ mzs -n --in huge-one-line.txt -e '$_.len'
mzs: reading input: bufio.Scanner: token too long
```

## `--bool` is grep's question

Exit 0 when **any** line was truthy, 1 when none was. Every line still runs, so a program
that prints does not print a different amount depending on where the first match landed.

```sh
$ cat data.txt | mzs -n --bool -e '$_ ~ /beta/' && echo MATCH
MATCH
$ cat data.txt | mzs -n --bool -e '$_ ~ /zeta/' && echo MATCH
$ echo $?
1
```

An error on a line stops the run and says which line it was:

```sh
$ printf '1\n2\nx\n' | mzs -n -e '$_.int + 1 == 3 ? raise("bad line") : $_'
1
mzs: input line 2:
-e:1:19: raise: bad line
  $_.int + 1 == 3 ? raise("bad line") : $_
                    ^
```

## Argument errors

```sh
$ cat script.mzs | mzs -n
mzs: -n has nothing to read: stdin is the program itself; give the data with --in <path>

$ mzs -n                      # a terminal, no -e and no file
mzs: -n needs a program: pass -e '<source>' or a script file

$ mzs -n --repl -e '1'
mzs: -n reads stdin as data and --repl reads it as commands; pick one
run 'mzs --help' for usage
```

All three exit 2; only the third is a flag-parsing error, which is why only it adds the
`run 'mzs --help'` line. `--no-io` does not affect `-n`: the CLI reads the lines, not the
script.

## See also

- [./README.md](./README.md) — flags, printing rules, exit codes
- [./diagnostics.md](./diagnostics.md) — the error format and `--check`
- [../modules/io.md](../modules/io.md) — `io.stdin`, `io.lines`, files
- [../stdlib/core.md](../stdlib/core.md#input) — `input`, the prompting read of one line
- [../language/host-variables.md](../language/host-variables.md) — `$_` and the other `$vars`
