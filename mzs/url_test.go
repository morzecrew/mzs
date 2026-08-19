package mzs

import (
	"strings"
	"testing"
)

// SPEC §12.17, driven through the front end. What has to be pinned is not only that a URL
// comes apart, but that it goes back together: the two rows are inverses, and the shape
// between them is an ordinary dict a script can edit with `set` (§12.4).

// TestURLParse is the eight keys, decoded.
func TestURLParse(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"every part at once", `include url; include json
url.parse("https://ivan:secret@api.example.com:8443/v1/orders?page=2#top").json`,
			`{"scheme":"https","user":"ivan","password":"secret","host":"api.example.com","port":8443,` +
				`"path":"/v1/orders","query":{"page":"2"},"fragment":"top"}`},
		{"a bare host", `include url; url.parse("http://example.com")["path"]`, ""},
		{"no port is 0 and not the scheme's", `include url; url.parse("https://example.com/")["port"]`, "0"},
		{"the path arrives decoded", `include url; url.parse("https://example.com/%D1%81%D1%87%D0%B5%D1%82%D0%B0/1")["path"]`,
			"/счета/1"},
		{"and so does the fragment", `include url; url.parse("https://example.com/#%D0%B0")["fragment"]`, "а"},
		{"the query is a dict", `include url; url.parse("https://example.com/?a=1&b=2")["query"]["b"]`, "2"},
		{"a repeated key keeps the first, as http does", `include url
url.parse("https://example.com/?tag=a&tag=b")["query"]["tag"]`, "a"},
		{"a relative URL borrows nothing", `include url; include json
url.parse("/docs/index.html?a=1").json`,
			`{"scheme":"","user":"","password":"","host":"","port":0,"path":"/docs/index.html",` +
				`"query":{"a":"1"},"fragment":""}`},
		{"an opaque URL keeps its body in path", `include url; url.parse("mailto:ivan@example.com")["path"]`,
			"ivan@example.com"},
		{"an IPv6 host loses its brackets", `include url; url.parse("http://[::1]:9000/x")["host"]`, "::1"},
		{"a user with no password", `include url; url.parse("https://token@example.com/")["password"]`, ""},
		{"the query of a URL that has none is empty", `include url; url.parse("https://example.com/")["query"].len`, "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLBuild is the way back, escaping each part by the rule of the half it sits in.
func TestURLBuild(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"from nothing at all", `include url; url.build({})`, ""},
		{"a path alone", `include url; url.build({path: "/a b"})`, "/a%20b"},
		{"scheme, host, path", `include url; url.build({scheme: "https", host: "example.com", path: "/hi"})`,
			"https://example.com/hi"},
		{"a port", `include url; url.build({scheme: "http", host: "example.com", port: 8080, path: "/"})`,
			"http://example.com:8080/"},
		{"port 0 is no port", `include url; url.build({scheme: "http", host: "example.com", port: 0})`,
			"http://example.com"},
		{"a query", `include url; url.build({scheme: "https", host: "e.com", path: "/s", query: {q: "мир"}})`,
			"https://e.com/s?q=%D0%BC%D0%B8%D1%80"},
		{"a fragment", `include url; url.build({scheme: "https", host: "e.com", fragment: "раздел"})`,
			"https://e.com#%D1%80%D0%B0%D0%B7%D0%B4%D0%B5%D0%BB"},
		{"a user", `include url; url.build({scheme: "https", user: "ivan", host: "e.com"})`, "https://ivan@e.com"},
		{"a user and a password", `include url; url.build({scheme: "https", user: "ivan", password: "s3", host: "e.com"})`,
			"https://ivan:s3@e.com"},
		{"an IPv6 host is bracketed again",
			`include url; url.build({scheme: "http", host: "::1", port: 9000, path: "/x"})`, "http://[::1]:9000/x"},
		{"an opaque URL stays opaque", `include url; url.build({scheme: "mailto", path: "ivan@example.com"})`,
			"mailto:ivan@example.com"},
		{"the unspecified address is a literal too",
			`include url; url.build({scheme: "http", host: "::"})`, "http://[::]"},
		{"an IPv6 zone survives the trip",
			`include url; url.build(url.parse("http://[fe80::1%25eth0]:8080/x"))`, "http://[fe80::1%25eth0]:8080/x"},
		{"a host that arrives bracketed is not bracketed twice",
			`include url; url.build({scheme: "http", host: "[::1]", port: 9000})`, "http://[::1]:9000"},
		{"an empty query writes no question mark",
			`include url; url.build({scheme: "https", host: "e.com", query: {}})`, "https://e.com"},
		{"editing one part", `include url
url.build(url.parse("https://e.com/v1/orders?page=2").dup.set("path", "/v1/invoices"))`,
			"https://e.com/v1/invoices?page=2"},
		{"and the dict it was copied from is untouched", `include url
u = url.parse("https://e.com/v1/orders")
url.build(u.dup.set("path", "/v1/invoices")) + " " + url.build(u)`,
			"https://e.com/v1/invoices https://e.com/v1/orders"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLRoundTrip is the promise the two rows make together.
func TestURLRoundTrip(t *testing.T) {
	in := evInterp()

	urls := []string{
		"https://api.example.com:8443/v1/orders?page=2&sort=desc#top",
		"http://example.com",
		"https://ivan:secret@example.com/x",
		"http://[::1]:9000/x",
		"mailto:ivan@example.com",
		"/docs/index.html?a=1",
		"https://example.com/%D1%81%D1%87%D0%B5%D1%82%D0%B0",
	}
	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			src := `include url; url.build(url.parse(` + quoteString(u) + `))`
			if got := evStr(t, in, src); got != u {
				t.Errorf("build(parse(%q)) = %q; want %q", u, got, u)
			}
		})
	}

	// What is normalised is the spelling of an escape and never the bytes: a lowercase
	// `%d1` is the same byte written the other way, and `build` writes it its own way.
	t.Run("an escape comes back in upper case", func(t *testing.T) {
		got := evStr(t, in, `include url; url.build(url.parse("https://example.com/%d1%81"))`)
		if want := "https://example.com/%D1%81"; got != want {
			t.Errorf("= %q; want %q", got, want)
		}
	})
}

// TestURLEncodedSlash pins the one shape that does not survive `parse` and `build`. The
// path a script reads is decoded text (§12.17), and `%2F` decodes to the character that
// separates segments — so it is rebuilt as a separator, and the rebuilt URL names something
// else. Over-escaping is the harmless neighbour of that case: `%41` is `A` on the way in and
// `A` on the way out, which RFC 3986 §6.2.2.2 calls the same URL.
func TestURLEncodedSlash(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"the path decodes", `include url; url.parse("https://e.com/a%2Fb")["path"]`, "/a/b"},
		{"and rebuilds as a separator", `include url; url.build(url.parse("https://e.com/a%2Fb"))`,
			"https://e.com/a/b"},
		{"an over-escaped character rebuilds as itself",
			`include url; url.build(url.parse("https://e.com/a%41b"))`, "https://e.com/aAb"},
		{"an encoded question mark stays encoded",
			`include url; url.build(url.parse("https://e.com/a%3Fb"))`, "https://e.com/a%3Fb"},
		{"and so does an encoded hash",
			`include url; url.build(url.parse("https://e.com/a%23b"))`, "https://e.com/a%23b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLEncodeDecode is RFC 3986 and nothing on top of it: the `+` is a plus.
func TestURLEncodeDecode(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"a space is %20", `include url; url.encode("a b")`, "a%20b"},
		{"a plus is escaped", `include url; url.encode("a+b")`, "a%2Bb"},
		{"the unreserved set is left alone", `include url; url.encode("aZ0-._~")`, "aZ0-._~"},
		{"a slash is not unreserved", `include url; url.encode("a/b")`, "a%2Fb"},
		{"bytes, not runes", `include url; url.encode("д")`, "%D0%B4"},
		{"decode reads %XX in either case", `include url; url.decode("%d0%b4")`, "д"},
		{"decode leaves the plus alone", `include url; url.decode("a+b")`, "a+b"},
		{"a round trip", `include url; url.decode(url.encode("а б+в/г")) == "а б+в/г"`, "true"},
		{"nothing to do", `include url; url.encode("")`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLQuery is the form half: what a dict may hold and what an array of values means.
func TestURLQuery(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"a pair", `include url; url.query({q: "мир"})`, "q=%D0%BC%D0%B8%D1%80"},
		{"the dict's own order", `include url; url.query({b: 1, a: 2})`, "b=1&a=2"},
		{"numbers and bools have one written form", `include url; url.query({n: 2, f: 1.5, ok: true})`,
			"n=2&f=1.5&ok=true"},
		{"nil is a key with an empty value", `include url; url.query({debug: nil})`, "debug="},
		{"an array repeats the key", `include url; url.query({tag: ["a", "b"]})`, "tag=a&tag=b"},
		{"an empty array writes nothing", `include url; url.query({tag: [], a: 1})`, "a=1"},
		{"an empty dict is an empty string", `include url; url.query({})`, ""},
		{"a space is %20 on the way out", `include url; url.query({q: "a b"})`, "q=a%20b"},
		{"and a plus survives as one", `include url; url.parse_query(url.query({q: "a+b"}))["q"]`, "a+b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLParseQuery is the reading half, where a `+` is a space because a form sent it.
func TestURLParseQuery(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"a pair", `include url; url.parse_query("a=1")["a"]`, "1"},
		{"a leading question mark is allowed", `include url; url.parse_query("?a=1")["a"]`, "1"},
		{"a plus is a space", `include url; url.parse_query("q=a+b")["q"]`, "a b"},
		{"and %20 is one too", `include url; url.parse_query("q=a%20b")["q"]`, "a b"},
		{"a key with no value", `include url; url.parse_query("flag")["flag"]`, ""},
		{"an empty pair is skipped", `include url; url.parse_query("a=1&&b=2").len`, "2"},
		{"the first of a repeated key wins", `include url; url.parse_query("a=1&a=2")["a"]`, "1"},
		{"an equals sign inside the value", `include url; url.parse_query("t=a=b")["t"]`, "a=b"},
		{"a semicolon is an ordinary character", `include url; url.parse_query("a=1;b=2")["a"]`, "1;b=2"},
		{"the empty query", `include url; url.parse_query("").len`, "0"},
		{"an encoded key", `include url; url.parse_query("%D0%B0=1")["а"]`, "1"},
		{"the order of the query is kept", `include url; url.parse_query("b=1&a=2").keys.json`, `["b","a"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestURLRefuses pins the diagnostics: a URL assembled out of a misspelled key is wrong in
// a way nothing downstream can see, so every one of them is loud (§17).
func TestURLRefuses(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, kind, msg string }{
		{"text that is not a URL", `include url; url.parse("http://a b/")`,
			ErrKindArgument, "cannot read"},
		{"a port that is not a number", `include url; url.parse("http://example.com:80x/")`,
			ErrKindArgument, "cannot read"},
		{"a bad escape in the path", `include url; url.parse("http://example.com/%zz")`,
			ErrKindArgument, "cannot read"},
		{"a bad escape in the query", `include url; url.parse("http://example.com/?a=%zz")`,
			ErrKindArgument, "cannot decode"},
		{"a misspelled key", `include url; url.build({schema: "https", host: "e.com"})`,
			ErrKindArgument, `unknown key "schema"`},
		{"a scheme with a space in it", `include url; url.build({scheme: "ht tp", host: "e.com"})`,
			ErrKindArgument, "is not a scheme"},
		{"a host that would move the boundary", `include url; url.build({scheme: "https", host: "e.com/x"})`,
			ErrKindArgument, "is not a host"},
		{"a password with no user", `include url; url.build({host: "e.com", password: "s3"})`,
			ErrKindArgument, "password with no user"},
		{"a user with no host", `include url; url.build({scheme: "https", user: "ivan", path: "a"})`,
			ErrKindArgument, "user with no host"},
		{"a host and a port written in the host", `include url; url.build({host: "example.com:443"})`,
			ErrKindArgument, `a port goes in "port"`},
		{"a bracketed host that is not a literal", `include url; url.build({host: "[::1]:bad"})`,
			ErrKindArgument, "IPv6 literal"},
		{"empty brackets are not one either", `include url; url.build({host: "[]"})`,
			ErrKindArgument, "IPv6 literal"},
		{"nor is a stray closing bracket", `include url; url.build({host: "a]b"})`,
			ErrKindArgument, "IPv6 literal"},
		{"a float port is refused, not truncated", `include url; url.build({host: "e.com", port: 8080.9})`,
			ErrKindType, `"port" must be an int, got float`},
		{"an explicit nil is a value, not an omission", `include url; url.build({host: "e.com", query: nil})`,
			ErrKindType, `"query" must be a dict`},
		{"and so is a nil path", `include url; url.build({host: "e.com", path: nil})`,
			ErrKindType, `"path" must be a string`},
		{"a port with no host", `include url; url.build({scheme: "https", port: 8080})`,
			ErrKindArgument, "port with no host"},
		{"a port out of range", `include url; url.build({host: "e.com", port: 70000})`,
			ErrKindArgument, "port must be 0..65535"},
		{"a part that is not a string", `include url; url.build({host: 42})`,
			ErrKindType, `"host" must be a string`},
		{"a port that is not a number", `include url; url.build({host: "e.com", port: "8080"})`,
			ErrKindType, `"port" must be an int`},
		{"a query that is not a dict", `include url; url.build({host: "e.com", query: "a=1"})`,
			ErrKindType, `"query" must be a dict`},
		{"a nested dict has no query spelling", `include url; url.query({a: {b: 1}})`,
			ErrKindType, "a query value must be"},
		{"nor does a function", `include url; url.query({a: { (x) -> x }})`,
			ErrKindType, "a query value must be"},
		{"nor does an array of dicts", `include url; url.query({a: [{b: 1}]})`,
			ErrKindType, "a query value must be"},
		{"a query key is text", `include url; url.query({1 -> "a"})`,
			ErrKindType, "a query key must be a string"},
		{"a bad escape while reading a query", `include url; url.parse_query("a=%zz")`,
			ErrKindArgument, "cannot decode"},
		{"parse takes a string", `include url; url.parse(42)`, ErrKindType, "expects a string"},
		{"build takes a dict", `include url; url.build("https://e.com")`, ErrKindType, "expects a dict"},
		{"query takes a dict", `include url; url.query("a=1")`, ErrKindType, "expects a dict"},
		{"encode takes a string", `include url; url.encode(nil)`, ErrKindType, "expects a string"},
		{"a % that starts nothing", `include url; url.decode("100%")`, ErrKindArgument, "cannot decode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := evErr(t, in, tt.src, nil)
			if e.Kind != tt.kind {
				t.Errorf("%s kind = %q; want %q (%s)", tt.src, e.Kind, tt.kind, e.Msg)
			}
			if !strings.Contains(e.Msg, tt.msg) {
				t.Errorf("%s = %q; want it to contain %q", tt.src, e.Msg, tt.msg)
			}
		})
	}
}

// TestURLLimits: the rows that build a string ask first, and the one that builds a dict
// counts its entries, so a host's caps are the caps (§14.2).
func TestURLLimits(t *testing.T) {
	t.Run("a built URL is a string", func(t *testing.T) {
		in := New(Options{MaxStringBytes: 8})
		e := evErr(t, in, `include url; url.build({scheme: "https", host: "example.com", path: "/hello"})`, nil)
		if e.Kind != ErrKindLimit {
			t.Errorf("kind = %q (%s); want %q", e.Kind, e.Msg, ErrKindLimit)
		}
	})
	t.Run("a written query is a string too", func(t *testing.T) {
		in := New(Options{MaxStringBytes: 8})
		e := evErr(t, in, `include url; url.query({a: "one", b: "two", c: "three"})`, nil)
		if e.Kind != ErrKindLimit {
			t.Errorf("kind = %q (%s); want %q", e.Kind, e.Msg, ErrKindLimit)
		}
	})
	t.Run("a query of many pairs is a collection", func(t *testing.T) {
		in := New(Options{MaxCollection: 4})
		e := evErr(t, in, `include url; url.parse_query("a=1&b=2&c=3&d=4&e=5&f=6")`, nil)
		if e.Kind != ErrKindLimit {
			t.Errorf("kind = %q (%s); want %q", e.Kind, e.Msg, ErrKindLimit)
		}
	})
}

// TestURLNeedsNoCapability: like `http` itself, the module is installed without a host
// option — it reads and writes text and reaches nowhere (§14.3).
func TestURLNeedsNoCapability(t *testing.T) {
	in := New(Options{})
	if got := evStr(t, in, `include url; url.parse("https://e.com/x")["host"]`); got != "e.com" {
		t.Errorf("host = %s; want e.com", got)
	}
	e := evErr(t, in, `include url; url("https://e.com")`, nil)
	if !strings.Contains(e.Msg, "is a module, not a function") {
		t.Errorf(`url("…") = %q; want the module diagnostic`, e.Msg)
	}
}

// TestURLStepBudget: reading and writing a URL is interruptible like everything else
// (§14.1) — the text may be as long as a string is allowed to be.
func TestURLStepBudget(t *testing.T) {
	long := strings.Repeat("a", 200_000)
	vars := map[string]Value{"$s": Str(long)}

	tests := []struct{ name, src string }{
		{"parse", `include url; url.parse("https://e.com/" + $s)`},
		{"encode", `include url; url.encode($s)`},
		{"decode", `include url; url.decode($s)`},
		{"parse_query", `include url; url.parse_query("a=" + $s)`},
		{"build", `include url; url.build({scheme: "https", host: "e.com", path: "/" + $s})`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(Options{StepBudget: 1024})
			e := evErr(t, in, tt.src, vars)
			if e.Kind != ErrKindLimit {
				t.Errorf("%s kind = %q (%s); want %q", tt.src, e.Kind, e.Msg, ErrKindLimit)
			}
		})
	}
}
