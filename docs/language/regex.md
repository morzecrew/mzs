# Regular expressions

`/…/` is a literal value with Ruby-flavoured semantics, matched with `~`, and backed by one
of two engines picked at compile time.

## Matching

```
"Привет!" ~ /привет/i        # true
"hello" !~ /\d/              # true
nil ~ /a/                    # false — an unbound $var simply does not match
"a" == /a/                   # syntax: '==' with a regex operand: use '~' to match
```

`~` and `!~` answer a bool and nothing else. Where a match is, not just whether, is
`index`. A regex literal may not span lines: a newline inside `/…/` is
`syntax: newline in regex literal; write \n instead`.

## Two engines, one syntax

The backend is decided once, at compile time, and never changes. RE2 (Go's `regexp`) runs
everything it can do correctly; a pattern using any construct below goes to the bundled
backtracking engine instead.

| Construct | Example |
|---|---|
| word boundary | `\b`, `\B` |
| lookaround | `(?=…)` `(?!…)` `(?<=…)` `(?<!…)` |
| backreference | `\1`…`\9`, `\k<name>` |
| atomic group | `(?>…)` |
| possessive quantifier | `a++`, `a*+`, `a?+` |
| POSIX class | `[[:alpha:]]` |

```
["aaab" ~ /a++b/, "aaa" ~ /a++a/]        # [true,false]  — possessive, no giving back
["aaab" ~ /(?>a+)b/, "aaa" ~ /(?>a+)a/]  # [true,false]
"the the".captures(/\b(\w+) \1\b/).json  # ["the the","the"]
"1500 ₽".captures(/\d+(?= ₽)/)[0]        # "1500"
"main.mzs" ~ /^(?!test_).*\.mzs$/        # true
```

`\G`, `\K` and `(?(…)` route there too and are refused, at compile time:
`regex: cannot compile /\Kabc/: mzs/rx/bt: pattern not supported: \K`.

## The step budget

The backtracking engine counts steps per match attempt (`Options.RegexSteps`, default
200 000; there is no CLI flag for it). Exceeding it is an error, not a hang:

```sh
$ mzs -e '("a" * 25 + "!") ~ /\b(a+)+b/'
-e:1:18: regex: mzs/rx/bt: regex step budget exceeded      # exit 1
```

The same pattern without `\b` is RE2's, which has no such problem: `("a" * 25 + "!") ~
/(a+)+b/` is `false`, immediately. The budget error is catchable, with kind `"regex"`.

## What differs from Go's `regexp`

```
"x\nfoo" ~ /^foo$/                              # true — ^ and $ are always line anchors
["x\nfoo" ~ /\Afoo/, "foo\nx" ~ /\Afoo/]        # [false,true]
["x\nfoo" ~ /foo\z/, "foo\nx" ~ /foo\z/]        # [true,false]
["foo\n" ~ /foo\Z/, "foo\n" ~ /foo\z/]          # [true,false] — \Z allows one trailing \n
```

`^`/`$` need no flag; `\A`, `\z`, `\Z` are the string anchors.

```
text = "еда, а не еды; поедим"
[text ~ /\bеда\b/, text ~ /\bед\b/, text ~ /\bеды\b/]     # [true,false,true]
["ПРИВЕТ" ~ /привет/i, "ЁЖ" ~ /ёж/i]                      # [true,true]
```

`\b` is Unicode-aware (a word rune is `\p{L}`, `\p{N}` or `_`) and `i` folds case by
Unicode. `\w`, `\d` and `\s` are **ASCII** in both engines — use `\p{L}`, `[[:alpha:]]` or
an explicit class for other scripts:

```
["Привет" ~ /^\w+$/, "Привет" ~ /^\p{L}+$/, "Привет" ~ /^[[:alpha:]]+$/]   # [false,true,true]
"мама мыла".matches(/[а-я]+/).json                                         # ["мама","мыла"]
```

Every index a regex produces or consumes is a **rune** index:

```
["привет".index(/вет/), "привет мир".index(/мир/), "a👋b".index(/b/)]      # [3,7,2]
```

## Flags

| Flag | Effect |
|---|---|
| `i` | case-insensitive, Unicode folding |
| `m` | `.` matches a newline (Ruby's `m`) |
| `s` | the same thing; canonicalised to `m` |
| `x` | ignore unescaped whitespace and `#` comments outside classes |
| `u` | accepted, no effect |

```
["a\nb" ~ /a.b/, "a\nb" ~ /a.b/m, "a\nb" ~ /a.b/s]     # [false,true,true]
["A-1043" ~ / [A-Z] - \d+ /x, "a b" ~ /a[ ]b/x]        # [true,true]
[/a/s.flags, /a/mi.flags, /a/.flags]                   # ["m","im",""]
["ПРИВЕТ" ~ /(?i)привет/, "aBc" ~ /a(?i:b)c/]          # [true,true]
```

Inline `(?i)` and `(?i:…)` work too. Because a literal is one line, `x` buys spacing within
that line, not a multi-line pattern.

## Dynamic regexes

`regex(pattern, flags)` compiles a string; results go through a bounded LRU cache
(`Options.RegexCacheSize`, default 256).

```
regex("^\d+$")                             # /^d+$/ — in a string "\d" is just "d"
re = regex("^\\d+$")                       # the doubled backslash is the real one
["42" ~ re, "4x" ~ re, re.source].json     # [true,false,"^\\d+$"]

word = "мир"
"Мир и труд" ~ regex("\\b" + word + "\\b", "i")     # true
```

Single quotes avoid the doubling: `regex('\bмир\b', 'i')`. A bad pattern or flag raises at
the call:

```sh
$ mzs -e 'regex("(")'
-e:1:1: regex: cannot compile /(/: missing closing ): `(?m)(`
$ mzs -e 'regex("a", "q")'
-e:1:1: regex: unknown regex flag q
```

Inside a *literal* the same doubling is a bug — `\\b` is a backslash then `b` — and it is
not repaired silently:

```sh
$ mzs --check w.mzs                   # x = "еда" ~ /\\bеда\\b/
w.mzs:1:13: warning: "\\b" matches a literal backslash; did you mean "\b"? (pattern probably came from a JSON string)
```

## Regex functions

Every one is UFCS, so `s.f(re)` and `re.f(s)` are the same call.

| Name | Result |
|---|---|
| `captures(re, s)` | array of the first match: whole match at 0, groups after — `nil` if no match |
| `matches(re, s)` | every match: the text when there are no groups, the groups when there are |
| `index(re, s)` | rune index of the first match, or `nil` |
| `source(re)` | the pattern text between the slashes |
| `flags(re)` | the canonical flag letters |

```
s = "order A-1043"
[s.captures(/([A-Z])-(\d+)/), /([A-Z])-(\d+)/.captures(s)].json
# [["A-1043","A","1043"],["A-1043","A","1043"]]

m = "2026-08-13".captures(/(?<y>\d{4})-(?<mo>\d{2})-(?<d>\d{2})/)
[m[0], m["y"], m["mo"], m[3]].json      # ["2026-08-13","2026","08","13"]

"ab".captures(/(a)(x)?(b)/).json        # ["ab","a",null,"b"]  — a group that did not take part
"a=1, b=2".matches(/(\w+)=(\d+)/).json  # [["a","1"],["b","2"]]
[inspect("abc".captures(/\d/)), "abc".matches(/\d/).json].json   # ["nil","[]"]

re = /^\+?\d+$/i
[re.source, re.flags, str(re)].json     # ["^\\+?\\d+$","i","/^\\+?\\d+$/i"]
```

A regex is also accepted by `count`, `index`, `split`, `replace`, `replace_first` and by
`match` arms. `has` is string-only: `"abc".has(/b/)` is
`type: has expects a string, got regex`.

[examples/10_regex_toolkit.mzs](../../examples/10_regex_toolkit.mzs) exercises all of it.

## See also

- [strings.md](./strings.md) — literals, escapes, interpolation
- [../stdlib/strings.md](../stdlib/strings.md) — `split`, `replace`, `count` in full
- [control-flow.md](./control-flow.md) — regex arms in `match`
- [../cli/diagnostics.md](../cli/diagnostics.md) — `--check` and its warnings
