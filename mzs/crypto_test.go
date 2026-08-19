package mzs

import (
	"strings"
	"testing"
)

// SPEC §12.16, driven through the front end. The vectors are the published ones —
// sha256("abc"), the RFC 2202 hmac keys — because a digest that is merely self-consistent
// is a digest nobody else can check, and checking somebody else's is the whole job.

// TestCryptoHex is the encoding that carries a signature in a header.
func TestCryptoHex(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"two bytes", `include crypto; crypto.hex("Hi")`, "4869"},
		{"the empty string", `include crypto; crypto.hex("")`, ""},
		{"bytes, not runes", `include crypto; crypto.hex("é")`, "c3a9"},
		{"back again", `include crypto; crypto.unhex("4869")`, "Hi"},
		{"upper case reads too", `include crypto; crypto.unhex("C3A9")`, "é"},
		{"a round trip", `include crypto; crypto.unhex(crypto.hex("привет")) == "привет"`, "true"},
		{"blanks around it are shed", `include crypto; crypto.unhex("  4869\n")`, "Hi"},
		{"a byte that is not text is still a byte", `include crypto; crypto.unhex("ff").bytes.json`, "[255]"},
		{"and the rune rows then see U+FFFD", `include crypto; crypto.unhex("ff").len`, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestCryptoBase64 pins both alphabets and the one decoder that reads either.
func TestCryptoBase64(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"the standard alphabet pads", `include crypto; crypto.base64("Hi")`, "SGk="},
		{"the url alphabet does not", `include crypto; crypto.base64("~~~?", "url")`, "fn5-Pw"},
		{"and it spells the other two characters", `include crypto; crypto.base64("~~~?")`, "fn5+Pw=="},
		{"the empty string", `include crypto; crypto.base64("")`, ""},
		{"a round trip through std", `include crypto; crypto.unbase64(crypto.base64("привет")) == "привет"`, "true"},
		{"a round trip through url", `include crypto; crypto.unbase64(crypto.base64("привет", "url")) == "привет"`, "true"},
		{"padded url text reads too", `include crypto; crypto.unbase64("fn5-Pw==")`, "~~~?"},
		{"unpadded std text reads too", `include crypto; crypto.unbase64("SGk")`, "Hi"},
		{"blanks around it are shed", `include crypto; crypto.unbase64("  SGk=  ")`, "Hi"},
		{"a jwt header", `include crypto; crypto.unbase64("eyJhbGciOiJIUzI1NiJ9")`, `{"alg":"HS256"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestCryptoDigests is the published vectors. `md5` and `sha1` are here to read what
// already exists, and what already exists has to hash the same.
func TestCryptoDigests(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"sha256 of abc", `include crypto; crypto.sha256("abc")`,
			"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"sha256 of nothing", `include crypto; crypto.sha256("")`,
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"sha1 of abc", `include crypto; crypto.sha1("abc")`, "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"md5 of abc", `include crypto; crypto.md5("abc")`, "900150983cd24fb0d6963f7d28e17f72"},
		{"the bytes are hashed, not the runes", `include crypto; crypto.md5("é")`,
			"66ddcd97cfdeabb2f6fb8a999b4bc76f"},
		{"hmac-sha256 of the fox", `include crypto
crypto.hmac("key", "The quick brown fox jumps over the lazy dog")`,
			"f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"},
		{"hmac-sha1 of the fox", `include crypto
crypto.hmac("key", "The quick brown fox jumps over the lazy dog", "sha1")`,
			"de7c9b85b8b78aa6bc8a7a36f70a90701c9db4d9"},
		{"hmac-md5 of the fox", `include crypto
crypto.hmac("key", "The quick brown fox jumps over the lazy dog", "md5")`,
			"80070713463e7749b90c2dc24911e275"},
		{"a key that is bytes rather than text", `include crypto
crypto.hmac(crypto.unhex("ff00"), "m")`, "ba195d2f23a7663e26d840eb8cd7ac549113d9f6a2cbaf3f8298303aedaa6138"},
		{"a signature is hex and can be compared as text", `include crypto
crypto.equal(crypto.hmac("k", "m"), crypto.hmac("k", "m"))`, "true"},
		{"crc32 is the ieee one", `include crypto; crypto.crc32("hello")`, "907060870"},
		{"crc32 of nothing", `include crypto; crypto.crc32("")`, "0"},
		{"crc32 is unsigned", `include crypto; crypto.crc32("а") > 0`, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestCryptoEqual is the row that exists because `==` leaks. What it must do is answer
// the same as `==` would — the timing is what differs, and that is not testable here.
func TestCryptoEqual(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, want string }{
		{"equal", `include crypto; crypto.equal("abc", "abc")`, "true"},
		{"one byte apart", `include crypto; crypto.equal("abc", "abd")`, "false"},
		{"a prefix is not equal", `include crypto; crypto.equal("abc", "ab")`, "false"},
		{"empty and empty", `include crypto; crypto.equal("", "")`, "true"},
		{"case matters, as it does to a digest", `include crypto; crypto.equal("AB", "ab")`, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evStr(t, in, tt.src); got != tt.want {
				t.Errorf("%s = %s; want %s", tt.src, got, tt.want)
			}
		})
	}
}

// TestCryptoRefuses pins the diagnostics. Each one names what to write instead, because
// each is a mistake with one correct next line (§17).
func TestCryptoRefuses(t *testing.T) {
	in := evInterp()

	tests := []struct{ name, src, kind, msg string }{
		{"an odd number of digits", `include crypto; crypto.unhex("abc")`,
			ErrKindArgument, "odd number of digits"},
		{"a digit that is not one", `include crypto; crypto.unhex("zz")`,
			ErrKindArgument, "is not a hex digit"},
		{"a blank inside is data", `include crypto; crypto.unhex("48 69")`,
			ErrKindArgument, "is not a hex digit"},
		{"an unknown alphabet", `include crypto; crypto.base64("x", "base64url")`,
			ErrKindArgument, `the alphabets are "std" and "url"`},
		{"both alphabets at once", `include crypto; crypto.unbase64("a-b+c")`,
			ErrKindArgument, "mixes the two alphabets"},
		{"text that is not base64", `include crypto; crypto.unbase64("!!!!")`,
			ErrKindArgument, "is not base64"},
		{"a length base64 never has", `include crypto; crypto.unbase64("SGkgd")`,
			ErrKindArgument, "is not base64"},
		{"an unknown algorithm", `include crypto; crypto.hmac("k", "m", "sha512")`,
			ErrKindArgument, `the algorithms are "sha256", "sha1" and "md5"`},
		{"a number where text is expected", `include crypto; crypto.sha256(42)`,
			ErrKindType, "expects a string"},
		{"a dict is not a message", `include crypto; crypto.hmac("k", {a: 1})`,
			ErrKindType, "expects a string"},
		{"and neither is nil", `include crypto; crypto.equal("a", nil)`,
			ErrKindType, "expects a string"},
		{"the alphabet is text", `include crypto; crypto.base64("x", true)`,
			ErrKindType, "expects a string"},
		{"too many arguments", `include crypto; crypto.sha256("a", "b")`,
			ErrKindArgument, "expects 1 argument(s), got 2"},
		{"too few", `include crypto; crypto.hmac("k")`,
			ErrKindArgument, "at least 2"},
		{"too many where the row is variadic", `include crypto; crypto.hmac("k", "m", "md5", "x")`,
			ErrKindArgument, "at most 3"},
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

// TestCryptoIsNotHash is the reason for the name. `hash` is the FNV-1a of §12.1, and an
// `include hash` would have made `hash(x)` a compile error in every file that wanted a
// signature (§12.8); `include crypto` costs nothing.
func TestCryptoIsNotHash(t *testing.T) {
	in := evInterp()

	if got := evStr(t, in, `include crypto; type(hash("abc"))`); got != "int" {
		t.Errorf(`type(hash("abc")) after include crypto = %s; want int`, got)
	}
	if got := evStr(t, in, `include crypto; crypto.keys.first`); got != "hex" {
		t.Errorf("crypto.keys.first = %s; want hex", got)
	}
	e := evErr(t, in, `include crypto; crypto("abc")`, nil)
	if !strings.Contains(e.Msg, "is a module, not a function") {
		t.Errorf("crypto(\"abc\") = %q; want the module diagnostic", e.Msg)
	}
}

// TestCryptoNeedsNoCapability: the module is installed like `json`, so a host that hands
// out no filesystem, no clock and no randomness still has it (§14.3).
func TestCryptoNeedsNoCapability(t *testing.T) {
	in := New(Options{})
	if got := evStr(t, in, `include crypto; crypto.sha1("")`); got != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("sha1 of nothing = %s", got)
	}
}

// TestCryptoLimits: every row that builds a string asks first, so a host's cap is the cap
// (§14.2), and the failure is a limit error rather than an allocation.
func TestCryptoLimits(t *testing.T) {
	tests := []struct{ name, src string }{
		{"hex doubles the length", `include crypto; crypto.hex("привет")`},
		{"base64 grows it too", `include crypto; crypto.base64("привет")`},
		{"a digest is 64 characters", `include crypto; crypto.sha256("a")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := New(Options{MaxStringBytes: 8})
			e := evErr(t, in, tt.src, nil)
			if e.Kind != ErrKindLimit {
				t.Errorf("%s kind = %q (%s); want %q", tt.src, e.Kind, e.Msg, ErrKindLimit)
			}
		})
	}
}

// TestCryptoStepBudget: a digest of a long string is interruptible. Every row charges what
// walking the bytes costs before it walks them, so a script cannot hash a megabyte outside
// the budget that bounds everything else (§14.1).
func TestCryptoStepBudget(t *testing.T) {
	long := strings.Repeat("a", 200_000)
	vars := map[string]Value{"$s": Str(long)}

	tests := []struct{ name, src string }{
		{"hex", `include crypto; crypto.hex($s)`},
		{"unhex", `include crypto; crypto.unhex(crypto.hex($s))`},
		{"base64", `include crypto; crypto.base64($s)`},
		{"unbase64", `include crypto; crypto.unbase64($s)`},
		{"sha256", `include crypto; crypto.sha256($s)`},
		{"hmac", `include crypto; crypto.hmac("k", $s)`},
		{"crc32", `include crypto; crypto.crc32($s)`},
		{"equal", `include crypto; crypto.equal($s, $s)`},
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

// TestCryptoTakesOneString is the argument check of the rows that take exactly one, so no
// row reaches its algorithm with something that is not text (§9.1).
func TestCryptoTakesOneString(t *testing.T) {
	in := evInterp()

	srcs := []string{
		`include crypto; crypto.hex(1)`,
		`include crypto; crypto.unhex(1)`,
		`include crypto; crypto.base64(1)`,
		`include crypto; crypto.unbase64(1)`,
		`include crypto; crypto.sha1(1)`,
		`include crypto; crypto.md5(1)`,
		`include crypto; crypto.crc32(1)`,
		`include crypto; crypto.hmac(1, "m")`,
		`include crypto; crypto.equal(1, "a")`,
	}
	for _, src := range srcs {
		t.Run(src, func(t *testing.T) {
			if e := evErr(t, in, src, nil); e.Kind != ErrKindType {
				t.Errorf("%s kind = %q (%s); want %q", src, e.Kind, e.Msg, ErrKindType)
			}
		})
	}
}
