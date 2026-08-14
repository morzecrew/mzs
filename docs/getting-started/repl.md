# The REPL

An interactive session: `mzs` with a terminal on stdin and no program, or `mzs --repl` anywhere.

## A session

```
$ mzs
mzs 0.1.0 — .help for help, .exit to quit
mzs> s = "  HELLO "
"  HELLO "
mzs> s.lower.trim
"hello"
mzs> fn double(n) { n * 2 }
#<fn double>
mzs> double(21)
42
mzs> xs = [3, 1, 2]
[3,1,2]
mzs> xs.sort
[1,2,3]
mzs> xs
[3,1,2]
mzs> say("hi")
hi
nil
```

Every line prints the value of its last expression in **inspect** form — strings keep their
quotes, `nil` is shown rather than dropped. That is not the `-e` rule: `mzs -e '"hi"'` prints
`hi` without quotes.

## Continuation

A line with an unclosed `{`, `(` or `[` keeps reading at `...> `:

```
mzs> fn area(w, h) {
...>   w * h
...> }
#<fn area>
mzs> area(3, 4)
12
```

An empty line aborts the continuation and compiles what there is, so the abandoned construct
becomes the error you need:

```
mzs> [1, 2,
...>
repl:1:7: syntax: expected ']' in array literal, found end of input
  [1, 2,
        ^
```

Only brackets and unterminated literals continue. `1 +` is a finished line that happens to be
wrong, and it says so at once:

```
mzs> 1 +
repl:1:4: syntax: unexpected end of input
  1 +
     ^
```

## Commands

| Command | Effect |
|---|---|
| `.help` | the built-in help text |
| `.exit` | leave — `.quit`, `:q` and Ctrl-D do the same |
| `.clear` | forget every line of the session |
| `.src` | print the session's accumulated source |
| `.vars` | print the bound `$variables` |

```
mzs> a = 1
1
mzs> b = 2
2
mzs> .src
a = 1
b = 2
```

`$variables` come from the command line, not from the session, and `.vars` lists them by name:

```
$ mzs --repl -v name=Ivan -v n=3
mzs 0.1.0 — .help for help, .exit to quit
mzs> .vars
$n = "3"
$name = "Ivan"
```

## What persists

Local variables and functions carry over between lines. `.clear` drops them:

```
mzs> x = 1
1
mzs> .clear
session cleared
mzs> x
repl:1:1: name: undefined variable 'x' (did you mean 'debug'?)
  x
  ^
```

A line that fails is **not** added to the session, so a typo leaves no trace.

The session keeps its state by re-running the lines that already worked and printing only the
output the replay had not produced before — which is why the transcript above shows `hi` once.
Two consequences:

* the language stays deterministic across the replay, so a fresh interpreter is built per line;
* a line with a side effect outside the process runs again on every later line. An
  `io.append(path, "x")` entered once, followed by three more lines, appends four times.
  Keep files, HTTP calls and other external writes out of a REPL session.

## See also

* [install.md](./install.md) — the other ways to start the interpreter
* [cheatsheet.md](./cheatsheet.md) — lines worth pasting into a session
* [../cli/README.md](../cli/README.md) — flags, exit codes, printing rules
* [../cli/diagnostics.md](../cli/diagnostics.md) — how to read the error format above
