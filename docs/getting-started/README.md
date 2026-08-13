# Getting started

mzs is an embeddable scripting language written in Go; this page is the entry point of the documentation tree.

## What kind of language this is

* **Everything is an expression.** The value of the last one is the value of a script, a function body and a closure; `return` is optional.
* **`{ … }` is a closure, unless it is a dict.** `if`, `while`, `for`, `fn` and `match` arms simply call it for you; in operand position `{a: 1}` and `{}` are dicts, and everything else is a closure.
* **`[ … ]` is always an array**, and a dict is always braces: `[1, 2]` is an array, `[]` the empty array, `{a: 1}` a dict, `{}` the empty dict. So JSON pastes in as source.
* **`x.f(y)` is exactly `f(x, y)`.** One flat namespace, and your own `fn` joins it — there are no methods on one side and functions on the other.
* **`match` replaces the `if/else if` ladder.** Sixteen keywords in the whole language, and exactly one name per operation — `size`, `collect` and `select` are errors that name `len`, `map` and `filter`.

```
[1, 2, 3].map { it * 2 }              # [2,4,6]
fn shout(s) { s.upper + "!" }
"yes".shout                           # YES!
len("привет") == "привет".len         # true
```

The sixteen keywords are `fn if else match while for in break next return try true false nil include export`.

## Your first program

```
text = "the quick brown fox jumps over the lazy dog the fox"
words = text.split(" ")

fn report(ws) {
  freq = ws.tally
  {total: ws.len, unique: freq.len, top: freq.array.max_by { it[1] }}
}

r = words.report                       # UFCS: exactly report(words)
say("total:  ${r["total"]}")
say("unique: ${r["unique"]}")
say("top:    ${r["top"][0]} x${r["top"][1]}")

for w in ["fox", "cat", "the"] {
  say(match w {
    in ["the", "a", "of"] -> "${w}: stop word"
    in words              -> "${w}: seen ${words.count(w)}x"
    else                  -> "${w}: absent"
  })
}
```

```sh
mzs first.mzs
```

```
total:  11
unique: 8
top:    the x3
fox: seen 2x
cat: absent
the: stop word
```

Nothing here is ambient: `say` and `tally` are library functions, but a module such as `json` or `io`
enters the program only through an `include` line.

## Things that surprise newcomers

```
7 / 2                       # 3     — int / int is integer division
7.0 / 2                     # 3.5
"2" + 1                     # error: cannot add int to string
"2".int + 1                 # 3     — conversions are always explicit
"привет".index(/вет/)       # 3     — indices and len are in RUNES, not bytes
[].bool                     # true  — only nil and false are falsy
```

## Where to go next

| You want | Page |
|---|---|
| Build it, run a file, run a one-liner | [install.md](./install.md) |
| Try it interactively | [repl.md](./repl.md) |
| Programs that fit on one line | [cheatsheet.md](./cheatsheet.md) |
| The language, section by section | [../language/README.md](../language/README.md) |
| Every function | [../stdlib/README.md](../stdlib/README.md) |
| Call mzs from Go | [../embedding/README.md](../embedding/README.md) |

Runnable programs live in [`examples/`](../../examples/) — 34 numbered example programs, from
`01_values_and_operators.mzs` to `34_bits_and_bytes.mzs` (36 files: `27_` is a program plus the
two modules it includes).
The normative description is [`SPEC.md`](../../SPEC.md).

## See also

* [install.md](./install.md) — every way to start the interpreter
* [cheatsheet.md](./cheatsheet.md) — one-liners with their real output
* [../language/values.md](../language/values.md) — the nine kinds of value
* [../reference/limitations.md](../reference/limitations.md) — what mzs deliberately does not do
