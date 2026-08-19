# The `url` module

Taking a URL apart, putting one together, and the two encodings that are not the same
encoding.

```
include url

u = url.parse("https://api.example.com:8443/v1/orders?page=2#top")
u["host"]                                       # "api.example.com"
u["query"]["page"]                              # "2"
url.build(u.set("path", "/v1/invoices"))        # https://api.example.com:8443/v1/invoices?page=2#top
```

Like `http` itself, the module needs no host capability: it reads and writes text and
reaches nowhere ([sandbox](../reference/sandbox.md)). `include url` is the whole of it.

## Members

| Call | Signature | Result |
|---|---|---|
| `url.parse(s)` | `(string) -> dict` | the eight keys below, every one decoded |
| `url.build(d)` | `(dict) -> string` | the inverse; every key optional, an unknown key an error |
| `url.encode(s)` / `url.decode(s)` | `(string) -> string` | RFC 3986 percent-encoding |
| `url.query(d)` | `(dict) -> string` | a query string, in the dict's own order |
| `url.parse_query(s)` | `(string) -> dict` | a query string back; a leading `?` allowed |

```
include url
url.keys.json      # ["parse","build","encode","decode","query","parse_query"]
```

## What `parse` returns

```
include url
include json
url.parse("https://api.example.com:8443/v1/orders?page=2&sort=desc#top").json
# {"scheme":"https","user":"","password":"","host":"api.example.com","port":8443,
#  "path":"/v1/orders","query":{"page":"2","sort":"desc"},"fragment":"top"}
```

| Key | Type | |
|---|---|---|
| `scheme` | string | `"https"`; `""` for a relative URL |
| `user` / `password` | string | the userinfo, decoded; `""` when there is none |
| `host` | string | no port, and no brackets around an IPv6 literal |
| `port` | int | **`0` when the URL names none** |
| `path` | string | decoded; an opaque URL keeps its body here |
| `query` | dict | parsed, in the order it was written |
| `fragment` | string | decoded, without the `#` |

Nothing is filled in. A relative URL has an empty scheme and an empty host rather than
borrowed ones, and a URL with no port has `port` 0 rather than the 443 its scheme would have
used — that number is a fact about the client that is going to dial, not about the text
that was read.

```
include url
url.parse("/docs/index.html?a=1")["path"]        # "/docs/index.html"
url.parse("https://example.com/")["port"]        # 0
url.parse("http://[::1]:9000/x")["host"]         # "::1"
url.parse("mailto:ivan@example.com")["path"]     # "ivan@example.com"
```

The last one is an **opaque** URL: no authority, and what follows the colon is its body.
`build` writes it back the same way, off the same shape.

## Decoded going in, escaped coming out

`parse` hands back text a script can compare against what it was going to write:

```
include url
u = url.parse("https://example.com/%D1%81%D1%87%D0%B5%D1%82%D0%B0/1")
u["path"]                          # "/счета/1"
u["path"] == "/счета/1"            # true
url.build(u)                       # https://example.com/%D1%81%D1%87%D0%B5%D1%82%D0%B0/1
```

`build` escapes each part by the rule of the half it sits in, which is the only place that
knows which half a character is in. A URL that goes through both comes back the same URL,
spelled the way `build` spells it — an over-escaped character comes back plain
(`/a%41b` → `/aAb`), which RFC 3986 §6.2.2.2 calls the same URL.

### What does not survive the round trip

An **encoded slash**. `%2F` decodes to the character that separates segments, so the path
comes back as two of them:

```
include url
url.parse("https://e.com/a%2Fb")["path"]              # "/a/b"
url.build(url.parse("https://e.com/a%2Fb"))           # https://e.com/a/b — a different URL
```

That is what a decoded `path` costs. It is paid deliberately: an escaped path would make
every comparison a decoding exercise, and refusing such a URL outright would put an API that
identifies things by encoded path (`/api/v4/projects/group%2Fproject`) out of reach of even
reading its host. A script that has to *forward* such a URL forwards the text it received;
`parse` is for reading it.

## Two encodings, and the `+` tells them apart

`encode` and `decode` are RFC 3986 and nothing on top: everything outside the unreserved set
(`A-Za-z0-9-._~`) becomes `%XX`, a space is `%20`, and a `+` is a plus — which is what a
*path* segment means by it.

```
include url
url.encode("счёт 7")                       # %D1%81%D1%87%D1%91%D1%82%207
url.decode("%D1%81%D1%87%D1%91%D1%82%207") # счёт 7
url.encode("a+b")                          # a%2Bb
url.decode("a+b")                          # a+b
```

`query` and `parse_query` speak the **form** spelling over that, where a `+` read back *is*
a space. The two rules together mean a foreign `?q=a+b` reads as `a b`, while a value this
module writes round-trips exactly, plus and all:

```
include url
url.parse_query("q=a+b")["q"]                    # "a b"
url.parse_query(url.query({q: "a+b"}))["q"]      # "a+b"
```

## Building a query

```
include url
url.query({q: "счёт 7", tag: ["a", "b"], ok: true, page: 2, debug: nil})
# q=%D1%81%D1%87%D1%91%D1%82%207&tag=a&tag=b&ok=true&page=2&debug=
```

A value may be a string, a number, a bool or nil — nil is the key with an empty value,
which is what `?debug=` says — and an **array** repeats the key, which is the only spelling
`tag=a&tag=b` has. A nested dict is a type error: `a[b]=1` and `a.b=1` are two framework
conventions and neither is the standard, so there is nothing here to guess.

The order is the dict's own order, so two runs of the same program build the same string.

## A repeated key keeps the first

```
include url
include json
url.parse_query("?q=a+b&tag=x&tag=y&flag").json    # {"q":"a b","tag":"x","flag":""}
```

That is what `http` already does with a request's query and its headers
([http](./http.md)): a script reaching for `q["page"]` wants the string, not a shape that
changes with the input. The cost is worth saying out loud — `?tag=a&tag=b` does **not**
survive `parse`, and `build` writes back what `parse` saw. Writing repeats is the other
direction and is supported, through the array above.

A key with no `=` is present with an empty value, and an empty pair (`a=1&&b=2`) is skipped.
The `;` that once separated pairs is an ordinary character of a value.

## Building from nothing

Every key is optional:

```
include url
url.build({path: "/a b"})                                      # /a%20b
url.build({scheme: "https", host: "example.com"})              # https://example.com
url.build({scheme: "http", host: "::1", port: 9000, path: "/x"})   # http://[::1]:9000/x
url.build({scheme: "mailto", path: "ivan@example.com"})        # mailto:ivan@example.com
url.build({scheme: "https", host: "api.example.com", path: "/v1/orders",
           query: {page: 2, sort: "desc"}})
# https://api.example.com/v1/orders?page=2&sort=desc
```

## Errors

`build` checks every key, because a URL assembled out of a misspelled one is wrong in a way
nothing downstream can see:

```
include url
try url.build({schema: "https"}) else (e) -> e["message"]
# url.build: unknown key "schema"; a URL is built from "scheme", "user", "password",
# "host", "port", "path", "query", "fragment"

try url.build({scheme: "ht tp", host: "e.com"}) else (e) -> e["message"]
# url.build: "ht tp" is not a scheme: a letter, then letters, digits, '+', '-' or '.'

try url.build({host: "e.com/x"}) else (e) -> e["message"]
# url.build: "e.com/x" is not a host: it holds "/", which would move the boundary between the parts

try url.build({host: "example.com:443"}) else (e) -> e["message"]
# url.build: "example.com:443" is not a host: a colon or a bracket means an IPv6 literal like "::1",
# and a port goes in "port"

try url.build({host: "e.com", port: 8080.9}) else (e) -> e["message"]
# url.build: "port" must be an int, got float

try url.build({host: "e.com", password: "s"}) else (e) -> e["message"]
# url.build: a password with no user has no URL to be written in

try url.build({scheme: "https", port: 8080}) else (e) -> e["message"]
# url.build: a port with no host has nothing to belong to

try url.build({scheme: "https", user: "ivan", path: "a"}) else (e) -> e["message"]
# url.build: a user with no host has nothing to sit in front of

try url.query({a: {b: 1}}) else (e) -> e["message"]
# url.query: a query value must be a string, a number, a bool, nil or an array of them, got dict
```

Reading fails the same way — as an ordinary catchable `argument` error naming the text:

```
include url
try url.parse("http://a b/") else (e) -> e["message"]
# url.parse: cannot read "http://a b/" as a URL: invalid character " " in host name

try url.decode("100%") else (e) -> e["message"]
# url.decode: cannot decode "100%": invalid URL escape "%"

try url.parse_query("a=%zz") else (e) -> e["message"]
# url.parse_query: cannot decode "%zz" in "a=%zz": invalid URL escape "%zz"
```

A key that is *there* is read, whatever it holds: `url.build({host: "e.com", query: nil})` is
a type error rather than a second way of leaving the query out — an optional key is left out
by leaving it out.

A `%` that starts nothing is an error rather than a literal: text that reaches a decoder is
text somebody encoded, and half of it decoding is how a path turns into a different path
without a word.

## Limits

Reading and writing a URL charges its length against the step budget, a built URL is checked
against `MaxStringBytes`, and a query with more pairs than `MaxCollection` is a limit error
before the dict is built ([sandbox](../reference/sandbox.md)). All three are `limit` errors,
which `try` does not catch.

## See also

- [./http.md](./http.md) — the client and server this module was written for
- [./crypto.md](./crypto.md) — the other half of the same gap
- [./README.md](./README.md) — why `include` is required
- [../stdlib/dicts.md](../stdlib/dicts.md) — `set`, `merge` and `dig` on a parsed URL
