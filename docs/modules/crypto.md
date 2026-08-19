# The `crypto` module

Hex and base64, three digests, HMAC, a checksum, and the comparison that does not leak —
everything a script needs to sign what it sends and check what arrives.

```
include crypto

crypto.sha256("привет")                     # e58f1e8c55fa105bdd3f40e5037eb0b039b5998d52c05e6cd98878dd2da5cab2
crypto.hmac("s3cret", '{"id":7}')           # c48dcba22b49a9371cdf607a41016238221e38c78d72e9eccb55c75bdc0f3ebc
crypto.base64("Привет")                     # 0J/RgNC40LLQtdGC
```

The module needs no host capability. Like `json` and `decimal`, `include crypto` is the
whole of it: a digest reaches nowhere the process is not already, so there is no flag to
pass and nothing for a sandbox to take away ([sandbox](../reference/sandbox.md)).

## Members

| Call | Signature | Result |
|---|---|---|
| `crypto.hex(s)` / `crypto.unhex(s)` | `(string) -> string` | the bytes as lowercase hex, and back |
| `crypto.base64(s, alphabet = "std")` | `(string, string) -> string` | RFC 4648 §4, or §5 under `"url"` |
| `crypto.unbase64(s)` | `(string) -> string` | either alphabet, padded or not |
| `crypto.sha256(s)` / `crypto.sha1(s)` / `crypto.md5(s)` | `(string) -> string` | the digest, in hex |
| `crypto.hmac(key, msg, alg = "sha256")` | `(string, string, string) -> string` | the signature, in hex |
| `crypto.crc32(s)` | `(string) -> int` | the IEEE checksum, `0..4294967295` |
| `crypto.equal(a, b)` | `(string, string) -> bool` | `==` in constant time |

```
include crypto
crypto.keys.json
# ["hex","unhex","base64","unbase64","sha256","sha1","md5","hmac","crc32","equal"]
```

## Why it is not called `hash`

`hash(x)` is already a function — the FNV-1a of any value ([core](../stdlib/core.md)) — and
an `include` gives a name to its module for the whole file
([modules](./README.md#a-module-is-never-callable)). `include hash` would therefore have
made `hash(x)` a compile error in every file that wanted a signature. `crypto` costs
nothing and says more:

```
include crypto
type(hash("abc"))          # int — the function is still there
```

## Checking a webhook

The reason the module exists. `http` has had both halves since the beginning
([http](./http.md)); this is the line that was missing:

```
include crypto
include http

http.serve(":8080", {
  "POST /hook": { (req) ->
    signed = crypto.hmac($SECRET, req["body"])
    if !crypto.equal(signed, req["headers"]["x-signature"]) { {status: 401, body: "bad signature"} }
    else { {ok: true} }
  }
})
```

`crypto.equal` is not decoration. Written with `==`, that check tells an attacker — through
how long it takes — how much of a forged signature is right, one byte at a time. What
`equal` does not hide is the length of the two strings, and it need not: the length of a
digest is a fact about the algorithm.

A signature that arrives base64-encoded is compared after decoding *the other one*, so the
comparison stays in one spelling:

```
include crypto
want = crypto.base64(crypto.unhex(crypto.hmac($SECRET, body)))
crypto.equal(want, header)
```

## Bytes in, text out

Every row reads the **bytes** of its argument, not its runes:

```
include crypto
crypto.hex("é")            # c3a9 — two bytes, one rune
crypto.md5("é")            # 66ddcd97cfdeabb2f6fb8a999b4bc76f
```

The digests answer in lowercase hex, which is the form a header carries. The decoders go
the other way and may well hand back bytes that are not valid UTF-8 — the same thing
`pack_bytes` ([arrays](../stdlib/arrays.md)) and `io.read` of a binary file already produce.
Nothing raises over it, and `bytes` is how a script looks at what really came back:

```
include crypto
crypto.unhex("ff").bytes.json     # [255]
crypto.unhex("ff").len            # 1 — the rune rows see one U+FFFD
```

## Two spellings of base64, one reader

`"url"` is RFC 4648 §5 as it is actually sent: `-` and `_` for `+` and `/`, and **no
padding**, because that is how a token travels in a URL or a JWT.

```
include crypto
crypto.base64("?~~~ ok")           # P35+fiBvaw==
crypto.base64("?~~~ ok", "url")    # P35-fiBvaw
```

`unbase64` takes no alphabet argument, because the script did not choose how the token it
received was spelled. The alphabet is read off the text and the padding off its end, so all
four spellings decode:

```
include crypto
[crypto.unbase64("SGk="), crypto.unbase64("SGk"),
 crypto.unbase64("fn5-Pw"), crypto.unbase64("fn5+Pw==")].json
# ["Hi","Hi","~~~?","~~~?"]

crypto.unbase64("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9")   # {"alg":"HS256","typ":"JWT"}
```

Both decoders shed the blanks around their input, the way `decimal.of` does — text that
came out of a file or a header usually has a newline on it. A blank *inside* is data, and
saying so is better than decoding something else.

## The algorithms are the member list

`sha256`, `sha1` and `md5` — what `crypto.sha256` computes is what `crypto.hmac` signs with,
and there is no fourth name for either list. `sha1` and `md5` are here to read what already
exists: a webhook signed with sha1 is still a webhook that has to be verified, and neither
is a choice worth making for something new.

```
include crypto
[crypto.hmac("key", "msg"),
 crypto.hmac("key", "msg", "sha1"),
 crypto.hmac("key", "msg", "md5")].json
# ["2d93cbc1be167bcb1637a4a23cbff01a7878f0c50ee833954ea5221bb1b8c628",
#  "102900b72b7bf1031eec76b4804b66052376896b",
#  "18e3548c59ad40dd03907b7aeee71d67"]
```

`crc32` is the IEEE polynomial — the one `crc32` means in zip, gzip and everywhere else a
script meets one — and it is a checksum, not a signature: it says a byte flipped, never who
sent it.

```
include crypto
crypto.crc32("mzs")        # 2744266907
```

## Errors

Every failure is an ordinary catchable `argument` or `type` error, and each names the fix:

```
include crypto
try crypto.unhex("abc") else (e) -> e["message"]
# crypto.unhex: "abc" has an odd number of digits; hex spells one byte with two

try crypto.unhex("нет") else (e) -> e["message"]
# crypto.unhex: 0xd0 is not a hex digit, in "нет"

try crypto.unbase64("a-b+c") else (e) -> e["message"]
# crypto.unbase64: "a-b+c" mixes the two alphabets: '-_' is the url spelling and '+/' the std one

try crypto.base64("x", "base64url") else (e) -> e["message"]
# crypto.base64: unknown alphabet "base64url"; the alphabets are "std" and "url"

try crypto.hmac("k", "m", "sha512") else (e) -> e["message"]
# crypto.hmac: unknown algorithm "sha512"; the algorithms are "sha256", "sha1" and "md5"
```

A byte that is not a hex digit is named in hex when it is not printable, because the first
byte of `"н"` is not the character `Ð` and a diagnostic that said so would send the reader
looking for something that is not in the text.

## Limits

Every row charges the walk over its bytes against the step budget, so hashing a megabyte is
interruptible like any other loop, and every row that builds a string asks `MaxStringBytes`
first — `hex` doubles a length, `base64` grows it by a third
([limits](../reference/sandbox.md)). Both failures are `limit` errors, which `try` does not
catch.

## See also

- [./README.md](./README.md) — why `include` is required, and what a module is
- [./http.md](./http.md) — the server and client this module was written for
- [./url.md](./url.md) — the other half of the same gap
- [../stdlib/strings.md](../stdlib/strings.md) — `bytes`, and what a string is underneath
