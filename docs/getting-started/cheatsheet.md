# Cheat sheet

Every row is a complete program: `mzs -e '<line>'` prints the result shown.

## Text

| One-liner | Result |
|---|---|
| `"  ОПЕРАТОР ".lower.trim == "оператор"` | `true` |
| `"Ivan Petrov".split(" ").first` | `Ivan` |
| `user, host = "ivan@x.ru".split("@"); host` | `x.ru` |
| `"one,two".split(",").map { it.upper }.join(" ")` | `ONE TWO` |
| `"naïve".len` | `5` (runes) |
| `"naïve".bytes.len` | `6` (bytes) |
| `"hello".chars.reverse.join("")` | `olleh` |
| `"Ivan".ljust(8, ".")` | `Ivan....` |
| `"%.2f" % [3.14159]` | `3.14` |
| `"".int + 1200` | `1200` |
| `"12abc".int` | `12` |

`int` never raises: a missing or malformed string is `0`, which is what makes arithmetic on
host values safe.

## Collections

| One-liner | Result |
|---|---|
| `[1,2,3,4].filter { it % 2 == 0 }` | `[2,4]` |
| `[1,2,3].reduce(0) { (a, x) -> a + x }` | `6` |
| `(0..6).map { it }.each_slice(2).array` | `[[0,1],[2,3],[4,5],[6]]` |
| `"yes,no,yes".split(",").tally.json` | `{"yes":2,"no":1}` |
| `[3,1,2].sort { (a, b) -> b <=> a }` | `[3,2,1]` |
| `[1,2,3].zip([4,5,6])` | `[[1,4],[2,5],[3,6]]` |
| `[1,1,2].union([2,3])` | `[1,2,3]` |
| `["a","b"].to_set.has("a")` | `true` |
| `(1..1_000_000_000).seq.filter { it % 7 == 0 }.take(3).array` | `[7,14,21]` |
| `[].first ?? "empty"` | `empty` |
| `{a: 1}.merge({b: 2}).json` | `{"a":1,"b":2}` |
| `{a: 1, b: 2}.map { (k, v) -> "${k}=${v}" }.join("&")` | `a=1&b=2` |
| `{a: {b: {c: 7}}}.dig("a", "b", "c")` | `7` |

## JSON

| One-liner | Result |
|---|---|
| `include json; json.parse('{"a":[1,2]}').dig("a", 1)` | `2` |
| `include json; try json.parse("{oops") else "broken json"` | `broken json` |

```sh
$ mzs -e 'include json; json.pretty({a: 1})'
{
  "a": 1
}
```

`'…'` is an mzs string too, so a row that uses one needs shell double quotes:
`mzs -e "include json; json.parse('{\"a\":[1,2]}').dig(\"a\", 1)"`.

## Numbers

| One-liner | Result |
|---|---|
| `7 / 2` | `3` |
| `7.0 / 2` | `3.5` |
| `2 ** 10` | `1024` |
| `255.str(16)` | `ff` |
| `1.256.round(2)` | `1.26` |
| `popcount(255)` | `8` |
| `(1..5).reduce(1) { (a, x) -> a * x }` | `120` |
| `include math; math.sqrt(2)` | `1.4142135623730951` |
| `0.1 + 0.2` | `0.30000000000000004` — floats are binary |
| `include decimal; decimal.str(decimal.plus(decimal.of("0.1"), decimal.of("0.2")))` | `0.3` |
| `include decimal; decimal.str(decimal.of("1500.3"), 2)` | `1500.30` |
| `include decimal; decimal.split(decimal.of("10"), 3, 2).map { decimal.str(it, 2) }` | `["3.34","3.33","3.33"]` |

## Signatures, encodings and URLs

| One-liner | Result |
|---|---|
| `include crypto; crypto.sha256("abc")` | `ba7816bf…f20015ad` |
| `include crypto; crypto.hmac("key", "msg")` | `2d93cbc1…b1b8c628` |
| `include crypto; crypto.equal(a, b)` | the comparison a signature check needs |
| `include crypto; crypto.base64("Hi")` | `SGk=` |
| `include crypto; crypto.unbase64("SGk")` | `Hi` — padded or not, either alphabet |
| `include crypto; crypto.hex("Hi")` | `4869` |
| `include url; url.parse("https://e.com:8443/x?a=1")["port"]` | `8443` |
| `include url; url.query({q: "a b", tag: ["x","y"]})` | `q=a%20b&tag=x&tag=y` |
| `include url; url.parse_query("q=a+b")["q"]` | `a b` |

## Regex

| One-liner | Result |
|---|---|
| `"привет".index(/вет/)` | `3` (rune index) |
| `"Привет!".lower ~ /привет\|hello/i` | `true` |
| `"tel: +7 999 123-45-67".replace(/\D/, "")` | `79991234567` |
| `"foo=1;bar=2".matches(/(\w+)=(\d)/).dict.json` | `{"foo":"1","bar":"2"}` |
| `"2026-08-13".captures(/(\d+)-(\d+)-(\d+)/)[1]` | `2026` |
| `regex("прив" + "ет", "i").index("Привет")` | `0` |

`~` is the match operator and always yields a bool; the position of a match is `index`.

## Files

These four assume POSIX — `/tmp` writable, `/etc` present, `HOME` an absolute path — so
the *results* are what a Linux or macOS shell prints; elsewhere the calls are the same and
the values differ.

| One-liner | Result |
|---|---|
| `include io; io.write("/tmp/d.txt", "a\nb\n"); io.read("/tmp/d.txt").lines.len` | `2` |
| `include io; io.ls("/etc").len > 0` | `true` |
| `include io; io.exists("/no/such")` | `false` |
| `include io; io.env("HOME").starts_with("/")` | `true` |
| `include io; io.lines.filter { it.has("ERROR") }.take(10).array` | the first ten matching lines, streamed |

The CLI grants `io` by default; `--no-io` withholds it, and an embedding host has to supply a
filesystem before any of these names exist.

## Control flow

| One-liner | Result |
|---|---|
| `match 7 { in 1..5 -> "small"; else -> "big" }` | `big` |
| `a = 7; if a in 1..20 { "hit" } else { "miss" }` | `hit` |
| `"вет" in "привет"` | `true` |
| `fn area(w, h = 2) { w * h }; area(3, h = 5)` | `15` |
| `x = 5; x > 3 ? "yes" : "no"` | `yes` |
| `n = 0; n += 1 while n < 5; n` | `5` |
| `a, b = [1, 2]; a + b` | `3` |
| `nil ?? "fallback"` | `fallback` |
| `try raise("nope") else "caught"` | `caught` |
| `try { raise("nope") } else (e) { e["kind"] }` | `raise` |
| `$log = ""; try { 1 } ensure { $log = "released" }; $log` | `released` |
| `fn fib(n) { n < 2 ? n : fib(n-1) + fib(n-2) }; fib(20)` | `6765` |
| `add = fn(a, b) { a + b }; add(2, 3)` | `5` |
| `add = (a, b) -> { a + b }; add(2, 3)` | `5` |
| `println("done"); exit(2)` | `done`, exit code `2` |
| `{1 -> "A", 2 -> "B"}[2]` | `B` |
| `record Money(amount, currency = "RUB"); Money(1500).amount` | `1500` |
| `record Money(a, c); type(Money(1, "RUB"))` | `Money` |
| `record M(a); match M(1) { M -> "shape"; else -> "no" }` | `shape` |

## See also

* [README.md](./README.md) — the three rules these lines rely on
* [install.md](./install.md) — `--json`, `--bool`, `-n` and the other ways to run
* [../stdlib/README.md](../stdlib/README.md) — every function by receiver
* [../language/operators.md](../language/operators.md) — `~`, `??`, `<=>`, `..` in full
