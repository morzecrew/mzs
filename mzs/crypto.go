package mzs

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"strings"
)

// Digests, signatures and the two encodings — the `crypto` module (§12.16).
//
// `http` has had both halves since the beginning, a server whose routes are closures and
// a client whose answers are dicts, and the webhook arriving at one of those routes could
// not be checked: there was no hex, no base64, no sha256 and no hmac anywhere in the
// language (§12.11). This module is the missing pair of hands, and it is the whole of it:
//
//	include crypto
//	include http
//
//	http.serve(":8080", {"POST /hook": { (req) ->
//	  signed = crypto.hmac($SECRET, req["body"])
//	  if !crypto.equal(signed, req["headers"]["x-signature"]) { {status: 401} }
//	  else { {ok: true} }
//	}})
//
// **The name is `crypto`, not `hash`.** §12.1 spends `hash` on the FNV-1a of a value, and
// §12.8 gives an include the whole file: `include hash` would make `hash(x)` a compile
// error in every file that wanted a signature, for a module whose contents are honestly
// wider than hashing. Nothing here needs a host capability — like `json` and `decimal`,
// the include is all of it (§14.3): a digest reaches nowhere the process is not already.
//
// **Everything is bytes in and text out.** A string is bytes underneath (§7.1), so `hex`,
// the digests and `hmac` read the string's bytes and answer in lowercase hex, which is the
// form a header carries and the form two of them can be compared in. The decoders go the
// other way and may well answer with bytes that are not valid UTF-8 — exactly what
// `pack_bytes` (§12.3) and `io.read` of a binary file already produce, and nothing raises
// over it: the rune rows of §12.2 then see U+FFFD, and `s.bytes` is how a script looks at
// what really came back.
const (
	// The two alphabets of RFC 4648. `std` is §4, the one with `+/` and `=` padding;
	// `url` is §5 as it is actually sent — `-_` and no padding at all, because a token in
	// a URL or a JWT is spelled without it and stripping the `=` afterwards is the step
	// everyone forgets.
	cryAlphabetStd = "std"
	cryAlphabetURL = "url"

	// The three algorithms, which are also three member names: what `crypto.sha256`
	// computes is what `crypto.hmac(k, m, "sha256")` signs with, and there is no fourth
	// spelling of either list (D17).
	cryAlgSHA256 = "sha256"
	cryAlgSHA1   = "sha1"
	cryAlgMD5    = "md5"
)

func init() {
	// Registration order is `crypto.keys` order: a module is a Dict and a Dict is
	// insertion-ordered (§8.13).
	RegisterModuleFunc("crypto", "hex", 1, 1, cryHex)
	RegisterModuleFunc("crypto", "unhex", 1, 1, cryUnhex)
	RegisterModuleFunc("crypto", "base64", 1, 2, cryBase64)
	RegisterModuleFunc("crypto", "unbase64", 1, 1, cryUnbase64)
	RegisterModuleFunc("crypto", "sha256", 1, 1, cryDigest(cryAlgSHA256))
	RegisterModuleFunc("crypto", "sha1", 1, 1, cryDigest(cryAlgSHA1))
	RegisterModuleFunc("crypto", "md5", 1, 1, cryDigest(cryAlgMD5))
	RegisterModuleFunc("crypto", "hmac", 2, 3, cryHmac)
	RegisterModuleFunc("crypto", "crc32", 1, 1, cryCRC32)
	RegisterModuleFunc("crypto", "equal", 2, 2, cryEqual)
}

// cryStep charges what walking n bytes costs, at the rate `print` charges for writing
// them (§14.1). A digest of eight megabytes is not free and must be interruptible; one
// step per byte would make it cost more than the loop that built the string.
func cryStep(c *Ctx, n int) error { return c.Step(int64(n)/64 + 1) }

// ---------------------------------------------------------------------------
// hex
// ---------------------------------------------------------------------------

func cryHex(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := c.CheckString(hex.EncodedLen(len(s))); err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(s)); err != nil {
		return Nil(), err
	}
	return Str(hex.EncodeToString([]byte(s))), nil
}

// cryUnhex reads hex in either case. Surrounding blanks are trimmed — the same courtesy
// `decimal.of` extends to text that arrived from a file or a header (§12.15) — and a blank
// *inside* is data, so a wrapped or spaced-out dump is a named failure rather than a
// quietly different answer.
func cryUnhex(c *Ctx, args []Value) (Value, error) {
	s, err := cryText(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(s)); err != nil {
		return Nil(), err
	}
	b, derr := hex.DecodeString(s)
	if derr == nil {
		return Str(string(b)), nil
	}
	var bad hex.InvalidByteError
	switch {
	case errors.As(derr, &bad):
		return Nil(), c.ArgErrorf("%s: %s is not a hex digit, in %s",
			c.Name(), cryByteText(byte(bad)), quoteString(ellipsis(s)))
	case errors.Is(derr, hex.ErrLength):
		return Nil(), c.ArgErrorf("%s: %s has an odd number of digits; hex spells one byte with two",
			c.Name(), quoteString(ellipsis(s)))
	}
	return Nil(), c.ArgErrorf("%s: cannot read %s as hex: %s", c.Name(), quoteString(ellipsis(s)), derr.Error())
}

// ---------------------------------------------------------------------------
// base64
// ---------------------------------------------------------------------------

func cryBase64(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	enc, err := cryAlphabet(c, args, 1)
	if err != nil {
		return Nil(), err
	}
	if err := c.CheckString(enc.EncodedLen(len(s))); err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(s)); err != nil {
		return Nil(), err
	}
	return Str(enc.EncodeToString([]byte(s))), nil
}

// cryAlphabet reads the optional alphabet argument. The modes are named rather than
// flagged, as `decimal`'s rounding modes are (§12.15): a module member takes its arguments
// by position (§8.7), so `crypto.base64(s, true)` would be a call whose second half no
// reader can name.
func cryAlphabet(c *Ctx, args []Value, i int) (*base64.Encoding, error) {
	if i >= len(args) {
		return base64.StdEncoding, nil
	}
	name, err := argStr(c, args[i])
	if err != nil {
		return nil, err
	}
	switch name {
	case cryAlphabetStd:
		return base64.StdEncoding, nil
	case cryAlphabetURL:
		return base64.RawURLEncoding, nil
	}
	return nil, c.ArgErrorf("%s: unknown alphabet %s; the alphabets are %s and %s",
		c.Name(), quoteString(name), quoteString(cryAlphabetStd), quoteString(cryAlphabetURL))
}

// cryUnbase64 is one decoder for all four spellings, and it takes no alphabet argument on
// purpose: the script did not choose how the token it received was written. The alphabet
// is read off the text — `-_` is §5, `+/` is §4, and neither says nothing at all — and the
// padding is read off its end. Text carrying both alphabets is not a spelling anyone emits,
// so it is refused by name rather than half-decoded.
func cryUnbase64(c *Ctx, args []Value) (Value, error) {
	s, err := cryText(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(s)); err != nil {
		return Nil(), err
	}
	url := strings.ContainsAny(s, "-_")
	std := strings.ContainsAny(s, "+/")
	if url && std {
		return Nil(), c.ArgErrorf("%s: %s mixes the two alphabets: '-_' is the url spelling and '+/' the std one",
			c.Name(), quoteString(ellipsis(s)))
	}
	enc := base64.StdEncoding
	if url {
		enc = base64.URLEncoding
	}
	if !strings.HasSuffix(s, "=") {
		enc = enc.WithPadding(base64.NoPadding)
	}
	b, derr := enc.DecodeString(s)
	if derr == nil {
		return Str(string(b)), nil
	}
	var corrupt base64.CorruptInputError
	if errors.As(derr, &corrupt) {
		return Nil(), c.ArgErrorf("%s: %s is not base64 (at byte %d)",
			c.Name(), quoteString(ellipsis(s)), int(corrupt))
	}
	return Nil(), c.ArgErrorf("%s: cannot read %s as base64: %s",
		c.Name(), quoteString(ellipsis(s)), derr.Error())
}

// ---------------------------------------------------------------------------
// Digests and signatures
// ---------------------------------------------------------------------------

// cryDigest builds the sha256/sha1/md5 rows from one body, since they differ only in which
// constructor they call. All three answer in lowercase hex: a digest travels in a header
// and is compared against one, and a raw-byte answer would only ever be `crypto.hex`'d by
// the next line.
func cryDigest(alg string) HostFunc {
	return func(c *Ctx, args []Value) (Value, error) {
		s, err := argStr(c, args[0])
		if err != nil {
			return Nil(), err
		}
		if err := cryStep(c, len(s)); err != nil {
			return Nil(), err
		}
		h := cryHasher(alg)()
		h.Write([]byte(s))
		return cryHexValue(c, h.Sum(nil))
	}
}

// cryHmac signs a message with a key, hex again. `hmac` is what a webhook is checked with,
// which is why `equal` stands beside it: the comparison is half the operation.
func cryHmac(c *Ctx, args []Value) (Value, error) {
	key, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	msg, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}
	alg, err := cryAlg(c, args, 2)
	if err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(key)+len(msg)); err != nil {
		return Nil(), err
	}
	mac := hmac.New(cryHasher(alg), []byte(key))
	mac.Write([]byte(msg))
	return cryHexValue(c, mac.Sum(nil))
}

// cryAlg reads the optional algorithm name. The message names all three, because the next
// guess is the interesting part of a diagnostic (§17).
func cryAlg(c *Ctx, args []Value, i int) (string, error) {
	if i >= len(args) {
		return cryAlgSHA256, nil
	}
	name, err := argStr(c, args[i])
	if err != nil {
		return "", err
	}
	if cryHasher(name) == nil {
		return "", c.ArgErrorf("%s: unknown algorithm %s; the algorithms are %s, %s and %s",
			c.Name(), quoteString(name),
			quoteString(cryAlgSHA256), quoteString(cryAlgSHA1), quoteString(cryAlgMD5))
	}
	return name, nil
}

// cryHasher maps an algorithm name to its constructor, and nil is how `cryAlg` knows a name
// is not one of the three. `sha1` and `md5` are here to *read* what already exists — a
// webhook signed with sha1 is still a webhook that has to be verified — and never because
// they are a choice worth making today.
func cryHasher(alg string) func() hash.Hash {
	switch alg {
	case cryAlgSHA256:
		return sha256.New
	case cryAlgSHA1:
		return sha1.New
	case cryAlgMD5:
		return md5.New
	}
	return nil
}

// cryHexValue renders a digest. The length is fixed by the algorithm and small, but a host
// may set MaxStringBytes to anything at all (§14.2), so even this asks.
func cryHexValue(c *Ctx, sum []byte) (Value, error) {
	if err := c.CheckString(hex.EncodedLen(len(sum))); err != nil {
		return Nil(), err
	}
	return Str(hex.EncodeToString(sum)), nil
}

// ---------------------------------------------------------------------------
// crc32 and the comparison
// ---------------------------------------------------------------------------

// cryCRC32 is the IEEE polynomial — the one "crc32" means in zip, gzip and every other
// place a script meets one. It answers with the unsigned 32-bit value, `0..4294967295`,
// which is an Int here and needs no wrapping story (§7.1). It is a checksum and not a
// signature: it says a byte flipped, never who sent it.
func cryCRC32(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(s)); err != nil {
		return Nil(), err
	}
	return Int(int64(crc32.ChecksumIEEE([]byte(s)))), nil
}

// cryEqual compares two strings in time that does not depend on where they first differ.
// It is not a nicety: `signed == header` leaks, one byte at a time, how much of a forged
// signature is right, and `==` is exactly what a script reaches for. What the comparison
// does not hide is the *length*, and it need not — the length of a digest is a fact about
// the algorithm and not about the secret.
func cryEqual(c *Ctx, args []Value) (Value, error) {
	a, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	b, err := argStr(c, args[1])
	if err != nil {
		return Nil(), err
	}
	if err := cryStep(c, len(a)); err != nil {
		return Nil(), err
	}
	return Bool(hmac.Equal([]byte(a), []byte(b))), nil
}

// cryByteText names the byte a decoder tripped over. A printable one is quoted as itself;
// anything else is written in hex, because the byte in front of a decoder is often half a
// rune — the first byte of "н" is not "Ð", and a diagnostic that says so sends the reader
// looking for a character that is not in the text.
func cryByteText(b byte) string {
	if b >= 0x20 && b < 0x7f {
		return quoteString(string(rune(b)))
	}
	return fmt.Sprintf("0x%02x", b)
}

// cryText is the argument of the two decoders: a string, with the blanks around it shed.
func cryText(c *Ctx, v Value) (string, error) {
	s, err := argStr(c, v)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}
