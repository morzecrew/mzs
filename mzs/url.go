package mzs

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Reading and writing URLs — the `url` module (§12.17).
//
// The other half of the pair `http` left open (§12.11): `http.get` takes a URL and
// `http.serve` hands a request over, and until now a script could neither take one apart
// nor put one together. Both halves are here, and they are inverses:
//
//	include url
//	u = url.parse("https://api.example.com:8443/v1/orders?page=2#top")
//	u["host"]                                       # "api.example.com"
//	u["query"]["page"]                              # "2"
//	url.build(u.set("path", "/v1/invoices"))        # "https://api.example.com:8443/v1/invoices?page=2#top"
//
// **A parsed URL is a plain dict of eight keys**, in the order below, and every one of
// them is *decoded* text: `path` is `/счета/1`, never `/%D1%81%D1%87%D0%B5%D1%82%D0%B0/1`,
// because a script that reads a path compares it against what it was going to write. The
// escaping is `build`'s job on the way back out, which is the only place that knows which
// half of the URL a character sits in.
//
// **Two encodings, and the `+` tells them apart.** `encode`/`decode` are RFC 3986 percent
// -encoding and nothing else: a space is `%20` and a `+` is a plus, which is what a *path*
// segment means by it. `query`/`parse_query` speak the form spelling on top of that, where
// a `+` read back **is** a space — so a foreign `?q=a+b` reads as `a b`, while a value this
// module writes round-trips exactly, `+` and all, because writing escapes it as `%2B`.
const (
	urlKScheme   = "scheme"
	urlKUser     = "user"
	urlKPassword = "password"
	urlKHost     = "host"
	urlKPort     = "port"
	urlKPath     = "path"
	urlKQuery    = "query"
	urlKFragment = "fragment"
)

// urlKeys is the shape of a parsed URL, in the order `parse` writes it and the order a
// diagnostic lists it. It is also the whole set `build` accepts: a key that is not here is
// a misspelling of one that is, and answering an unknown key with silence would let
// `{scheme: …, hots: …}` build a URL with no host in it (§17).
var urlKeys = []string{urlKScheme, urlKUser, urlKPassword, urlKHost, urlKPort, urlKPath, urlKQuery, urlKFragment}

func init() {
	// Registration order is `url.keys` order: a module is a Dict and a Dict is
	// insertion-ordered (§8.13).
	RegisterModuleFunc("url", "parse", 1, 1, urlvParse)
	RegisterModuleFunc("url", "build", 1, 1, urlvBuild)
	RegisterModuleFunc("url", "encode", 1, 1, urlvEncode)
	RegisterModuleFunc("url", "decode", 1, 1, urlvDecode)
	RegisterModuleFunc("url", "query", 1, 1, urlvQuery)
	RegisterModuleFunc("url", "parse_query", 1, 1, urlvParseQuery)
}

// urlStep charges what walking n bytes of text costs, at the rate the rest of the library
// charges for the same walk (§14.1).
func urlStep(c *Ctx, n int) error { return c.Step(int64(n)/64 + 1) }

// ---------------------------------------------------------------------------
// parse
// ---------------------------------------------------------------------------

// urlvParse takes a URL apart. What it never does is guess the missing halves: a relative
// URL has an empty `scheme` and an empty `host` rather than a borrowed one, and a URL with
// no port has `port` 0 rather than the 443 its scheme would have used — that number is a
// fact about the client that is going to dial, not about the text that was read.
func urlvParse(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := urlStep(c, len(s)); err != nil {
		return Nil(), err
	}
	u, perr := url.Parse(s)
	if perr != nil {
		return Nil(), c.ArgErrorf("%s: cannot read %s as a URL: %s",
			c.Name(), quoteString(ellipsis(s)), urlReason(perr))
	}

	port := int64(0)
	if p := u.Port(); p != "" {
		// url.Parse has already refused a port that is not digits, so this cannot fail
		// for any reason but width, and a port that wide is not one.
		n, cerr := strconv.ParseInt(p, 10, 32)
		if cerr != nil || n < 0 || n > urlMaxPort {
			return Nil(), c.ArgErrorf("%s: %s is not a port (0..%d)", c.Name(), quoteString(p), urlMaxPort)
		}
		port = n
	}

	query, err := urlSplitQuery(c, u.RawQuery)
	if err != nil {
		return Nil(), err
	}

	// An opaque URL — `mailto:ivan@example.com`, `urn:isbn:…` — has no authority and no
	// path to speak of; what follows the colon is its body, and `path` is where a reader
	// looks for it. `build` writes it back the same way, off the same shape.
	path := u.Path
	if u.Opaque != "" {
		path = u.Opaque
	}

	user, password := "", ""
	if u.User != nil {
		user = u.User.Username()
		password, _ = u.User.Password()
	}

	d := NewOrderedDictCap(len(urlKeys))
	_ = d.Set(Str(urlKScheme), Str(u.Scheme))
	_ = d.Set(Str(urlKUser), Str(user))
	_ = d.Set(Str(urlKPassword), Str(password))
	_ = d.Set(Str(urlKHost), Str(u.Hostname()))
	_ = d.Set(Str(urlKPort), Int(port))
	_ = d.Set(Str(urlKPath), Str(path))
	_ = d.Set(Str(urlKQuery), dictOf(query))
	_ = d.Set(Str(urlKFragment), Str(u.Fragment))
	return dictOf(d), nil
}

// urlReason keeps the part of net/url's message that says what is wrong and drops the part
// that repeats the input back — a diagnostic quotes the input once, and it has already.
func urlReason(err error) string {
	var e *url.Error
	if errors.As(err, &e) {
		return e.Err.Error()
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// ---------------------------------------------------------------------------
// build
// ---------------------------------------------------------------------------

// urlMaxPort is the width of a TCP port. 0 is not one of them, which is what makes it the
// answer to "this URL names no port" rather than a port that has to be told from absence.
const urlMaxPort = 65535

// urlvBuild puts a URL back together out of the dict `parse` produces, escaping each part
// by the rule of the half it sits in. Every key is optional — `url.build({path: "/a b"})`
// is `/a%20b` — and every key is checked, because a URL assembled out of a misspelled key
// is wrong in a way nothing downstream can see.
func urlvBuild(c *Ctx, args []Value) (Value, error) {
	d, err := dictvOf(c, args[0])
	if err != nil {
		return Nil(), err
	}
	for _, k := range d.Keys() {
		if k.Kind() != KString || !urlKnownKey(k.Str()) {
			return Nil(), c.ArgErrorf("%s: unknown key %s; a URL is built from %s",
				c.Name(), quoteString(k.Str()), strings.Join(quoteAll(urlKeys), ", "))
		}
	}

	scheme, err := urlStrKey(c, d, urlKScheme)
	if err != nil {
		return Nil(), err
	}
	if scheme != "" && !urlValidScheme(scheme) {
		return Nil(), c.ArgErrorf("%s: %s is not a scheme: a letter, then letters, digits, '+', '-' or '.'",
			c.Name(), quoteString(ellipsis(scheme)))
	}
	host, err := urlStrKey(c, d, urlKHost)
	if err != nil {
		return Nil(), err
	}
	if i := strings.IndexAny(host, "/?#@ \t\r\n"); i >= 0 {
		return Nil(), c.ArgErrorf("%s: %s is not a host: it holds %s, which would move the boundary between the parts",
			c.Name(), quoteString(ellipsis(host)), quoteString(host[i:i+1]))
	}
	if strings.ContainsAny(host, ":[]") && !urlIPv6(host) {
		return Nil(), c.ArgErrorf("%s: %s is not a host: a colon or a bracket means an IPv6 literal like \"::1\", and a port goes in %s",
			c.Name(), quoteString(ellipsis(host)), quoteString(urlKPort))
	}
	path, err := urlStrKey(c, d, urlKPath)
	if err != nil {
		return Nil(), err
	}
	fragment, err := urlStrKey(c, d, urlKFragment)
	if err != nil {
		return Nil(), err
	}
	user, err := urlStrKey(c, d, urlKUser)
	if err != nil {
		return Nil(), err
	}
	password, err := urlStrKey(c, d, urlKPassword)
	if err != nil {
		return Nil(), err
	}
	if user == "" && password != "" {
		return Nil(), c.ArgErrorf("%s: a password with no user has no URL to be written in", c.Name())
	}
	if user != "" && host == "" {
		// `https://ivan@` is not a URL, and Go writes the parts it can — dropping the user
		// on the way. A part that cannot be written is refused rather than lost.
		return Nil(), c.ArgErrorf("%s: a user with no host has nothing to sit in front of", c.Name())
	}

	out := url.URL{Scheme: scheme, Path: path, Fragment: fragment}
	if host != "" {
		out.Host = urlHostPort(host, 0)
	}
	if port, ok, perr := urlPortKey(c, d); perr != nil {
		return Nil(), perr
	} else if ok {
		if host == "" {
			return Nil(), c.ArgErrorf("%s: a port with no host has nothing to belong to", c.Name())
		}
		out.Host = urlHostPort(host, port)
	}
	if user != "" {
		out.User = url.User(user)
		if password != "" {
			out.User = url.UserPassword(user, password)
		}
	}
	if q, ok := d.Get(Str(urlKQuery)); ok {
		// A key that is there is read, whatever it holds: an explicit `nil` is a type error
		// rather than a second way of leaving the query out, exactly as it is for every
		// other optional argument in the library (§12.15).
		raw, qerr := urlEncodeQuery(c, q)
		if qerr != nil {
			return Nil(), qerr
		}
		out.RawQuery = raw
	}
	// An opaque URL is the one shape a scheme, a host and a path cannot describe between
	// them: `mailto:ivan@example.com` has a body and no authority, and writing it through
	// Path would produce `mailto://ivan%40example.com`. It is recognised the way `parse`
	// left it — a scheme, no host, and a path that does not start at the root.
	if scheme != "" && host == "" && path != "" && !strings.HasPrefix(path, "/") {
		out.Path, out.Opaque = "", path
	}

	s := out.String()
	if err := c.CheckString(len(s)); err != nil {
		return Nil(), err
	}
	if err := urlStep(c, len(s)); err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

// urlHostPort joins a host and a port, bracketing an IPv6 literal — `[::1]:8080` — which is
// the one host that cannot simply be followed by a colon. A host that arrives already
// bracketed is left alone: `parse` hands the literal over bare, but a dict written by hand
// may well carry the brackets, and two of them are not a URL.
func urlHostPort(host string, port int64) string {
	if strings.Contains(host, ":") && !(strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]")) {
		host = "[" + host + "]"
	}
	if port == 0 {
		return host
	}
	return host + ":" + strconv.FormatInt(port, 10)
}

// urlIPv6 reports whether a host holding a colon is an IPv6 literal — the one host that may
// hold one — with or without the brackets and with an optional zone. The judgement is
// net.ParseIP's rather than a rule of this file's own, because "is this an address" is a
// question the standard library already answers exactly. Everything else with a colon in it
// is `example.com:443`: a host and a port written in the field for the host, which
// bracketing would turn into `[example.com:443]` and call a URL.
func urlIPv6(host string) bool {
	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") {
			return false
		}
		host = host[1 : len(host)-1]
	}
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	return net.ParseIP(host) != nil
}

func urlKnownKey(name string) bool {
	for _, k := range urlKeys {
		if k == name {
			return true
		}
	}
	return false
}

// urlValidScheme is RFC 3986's rule: a letter, then letters, digits, `+`, `-` and `.`.
func urlValidScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
		case i > 0 && (ch >= '0' && ch <= '9' || ch == '+' || ch == '-' || ch == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// urlStrKey reads one optional string key of the build dict. A key that is absent is "",
// and a key that is present is read as it is: `nil` is a value and not a second way of
// leaving something out, exactly as it is everywhere else (§12.15).
func urlStrKey(c *Ctx, d *OrderedDict, name string) (string, error) {
	v, ok := d.Get(Str(name))
	if !ok {
		return "", nil
	}
	s, err := argStr(c, v)
	if err != nil {
		return "", c.TypeErrorf("%s: %s must be a string, got %s", c.Name(), quoteString(name), v.TypeName())
	}
	return s, nil
}

// urlPortKey reads the port. 0 and absence are the same answer — no port — so a dict that
// came out of `parse` builds back without a special case.
func urlPortKey(c *Ctx, d *OrderedDict) (int64, bool, error) {
	v, ok := d.Get(Str(urlKPort))
	if !ok {
		return 0, false, nil
	}
	if v.Kind() != KInt {
		// A float is refused rather than truncated, the way a decimal's `scale` is
		// (§12.15): `8080.9` has no reading as a port, and answering `8080` would send a
		// request to a service the script did not name.
		return 0, false, c.TypeErrorf("%s: %s must be an int, got %s",
			c.Name(), quoteString(urlKPort), v.TypeName())
	}
	n := v.Int()
	if n < 0 || n > urlMaxPort {
		return 0, false, c.ArgErrorf("%s: port must be 0..%d, got %d", c.Name(), urlMaxPort, n)
	}
	if n == 0 {
		return 0, false, nil
	}
	return n, true, nil
}

// ---------------------------------------------------------------------------
// encode and decode
// ---------------------------------------------------------------------------

// urlvEncode percent-encodes everything outside RFC 3986's unreserved set — the letters,
// the digits, `-`, `.`, `_` and `~`. A space becomes `%20` and not `+`: the plus is the
// form spelling of a space and lives in `query`/`parse_query`, and a value that means a
// literal plus is the one that would quietly change meaning if this row spoke it too.
func urlvEncode(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := c.CheckString(3 * len(s)); err != nil {
		return Nil(), err
	}
	if err := urlStep(c, len(s)); err != nil {
		return Nil(), err
	}
	return Str(urlEscape(s)), nil
}

// urlvDecode reads `%XX` in either case and leaves everything else, `+` included, alone.
// A `%` that is not the start of one is an error and not a literal: text that reaches a
// decoder is text somebody encoded, and half of it decoding is how a path turns into a
// different path without a word.
func urlvDecode(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := urlStep(c, len(s)); err != nil {
		return Nil(), err
	}
	out, derr := url.PathUnescape(s)
	if derr != nil {
		return Nil(), c.ArgErrorf("%s: cannot decode %s: %s",
			c.Name(), quoteString(ellipsis(s)), urlReason(derr))
	}
	return Str(out), nil
}

const urlHexDigits = "0123456789ABCDEF"

func urlEscape(s string) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if !urlUnreserved(s[i]) {
			n++
		}
	}
	if n == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2*n)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if urlUnreserved(ch) {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(urlHexDigits[ch>>4])
		b.WriteByte(urlHexDigits[ch&0x0f])
	}
	return b.String()
}

func urlUnreserved(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		return true
	case ch == '-', ch == '.', ch == '_', ch == '~':
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// query and parse_query
// ---------------------------------------------------------------------------

// urlvQuery writes a query string out of a dict, in the dict's own order (§8.13). A value
// may be a string, a number, a bool or nil — nil is the key with an empty value, which is
// what `?debug=` says — and an **array** repeats the key, which is the only spelling
// `tag=a&tag=b` has. A nested dict is a type error: `a[b]=1` and `a.b=1` are two framework
// conventions and neither is the standard, so there is nothing here to guess (D16).
func urlvQuery(c *Ctx, args []Value) (Value, error) {
	d, err := dictvOf(c, args[0])
	if err != nil {
		return Nil(), err
	}
	s, err := urlEncodeQuery(c, dictOf(d))
	if err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

func urlEncodeQuery(c *Ctx, v Value) (string, error) {
	if v.Kind() != KDict {
		return "", c.TypeErrorf("%s: %s must be a dict, got %s", c.Name(), quoteString(urlKQuery), v.TypeName())
	}
	d := v.odict()
	if err := c.Step(int64(d.Len()) + 1); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, k := range d.Keys() {
		if k.Kind() != KString {
			return "", c.TypeErrorf("%s: a query key must be a string, got %s", c.Name(), k.TypeName())
		}
		val, _ := d.Get(k)
		if val.Kind() == KArray {
			for _, e := range val.Elems() {
				if err := urlWritePair(c, &b, k.Str(), e); err != nil {
					return "", err
				}
			}
			continue
		}
		if err := urlWritePair(c, &b, k.Str(), val); err != nil {
			return "", err
		}
	}
	s := b.String()
	if err := c.CheckString(len(s)); err != nil {
		return "", err
	}
	return s, nil
}

func urlWritePair(c *Ctx, b *strings.Builder, key string, val Value) error {
	text, err := urlQueryText(c, val)
	if err != nil {
		return err
	}
	if b.Len() > 0 {
		b.WriteByte('&')
	}
	b.WriteString(urlEscape(key))
	b.WriteByte('=')
	b.WriteString(urlEscape(text))
	// Asked per pair rather than once at the end: a dict of a thousand long values is
	// under MaxCollection and over MaxStringBytes, and the limit is there to stop the
	// string from being built, not to describe it afterwards (§14.2).
	return c.CheckString(b.Len())
}

// urlQueryText is what a query value may be. The conversion is the `str` of §12.7 for the
// kinds that have one written form, and a type error for the kinds whose form is a
// decision — a dict, a function, a seq — rather than a fact.
func urlQueryText(c *Ctx, v Value) (string, error) {
	switch v.Kind() {
	case KString, KInt, KFloat, KBool:
		return v.Str(), nil
	case KNil:
		return "", nil
	}
	return "", c.TypeErrorf("%s: a query value must be a string, a number, a bool, nil or an array of them, got %s",
		c.Name(), v.TypeName())
}

// urlvParseQuery reads a query string into a dict. A leading `?` is allowed, because the
// text a script has in hand is usually the one it copied out of a URL.
//
// A repeated key keeps its **first** value, which is what `http` already does with a
// request's query and its headers (§12.11): a script reaching for `q["page"]` wants the
// string, and one convention across the two modules beats a shape that changes with the
// input. The `;` that once separated pairs is an ordinary character of a value here, as it
// is everywhere else since it was withdrawn.
func urlvParseQuery(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := urlStep(c, len(s)); err != nil {
		return Nil(), err
	}
	d, err := urlSplitQuery(c, strings.TrimPrefix(s, "?"))
	if err != nil {
		return Nil(), err
	}
	return dictOf(d), nil
}

func urlSplitQuery(c *Ctx, raw string) (*OrderedDict, error) {
	d := NewOrderedDict()
	if raw == "" {
		return d, nil
	}
	// Counted before it is split: the count is one pass over bytes the runtime already
	// holds, where the slice of pieces is a fresh header per pair, and a query over the
	// limit should be refused rather than built and then refused.
	n := strings.Count(raw, "&") + 1
	if err := c.CheckCollection(n); err != nil {
		return nil, err
	}
	if err := c.Step(int64(n)); err != nil {
		return nil, err
	}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawVal, _ := strings.Cut(pair, "=")
		key, err := urlFormUnescape(c, rawKey, pair)
		if err != nil {
			return nil, err
		}
		val, err := urlFormUnescape(c, rawVal, pair)
		if err != nil {
			return nil, err
		}
		k := Str(key)
		if d.Has(k) {
			continue
		}
		if err := d.Set(k, Str(val)); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// urlFormUnescape decodes one half of a pair by the form rule: `%XX`, and `+` for a space.
func urlFormUnescape(c *Ctx, s, pair string) (string, error) {
	out, err := url.QueryUnescape(s)
	if err != nil {
		return "", c.ArgErrorf("%s: cannot decode %s in %s: %s",
			c.Name(), quoteString(ellipsis(s)), quoteString(ellipsis(pair)), urlReason(err))
	}
	return out, nil
}
