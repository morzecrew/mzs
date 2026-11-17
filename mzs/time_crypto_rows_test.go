package mzs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// RFC 0016, part 4b: time.mono and the crypto rows. The strftime directives and t.ms are
// in time_test.go, next to the directives they join.

// time.mono measures from the first reading in the Run, through the host clock.
func TestTimeMonoMeasuresFromTheFirstReading(t *testing.T) {
	t.Parallel()
	base := time.Date(2025, 3, 12, 14, 30, 15, 0, time.UTC)
	ticks := 0
	o := DefaultOptions()
	o.EnableTime = true
	o.Now = func() time.Time {
		ticks++
		return base.Add(time.Duration(ticks) * 1500 * time.Millisecond)
	}
	in := New(o)
	v, err := in.Eval(context.Background(), `include time; a = time.mono(); b = time.mono(); c = time.mono(); [a, b, c]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Inspect(); got != "[0,1.5,3]" {
		t.Errorf("three readings = %s; want [0,1.5,3]", got)
	}
	// A second Run has an origin of its own.
	v, err = in.Eval(context.Background(), `include time; time.mono()`, nil)
	if err != nil || v.Float() != 0 {
		t.Errorf("the first reading of a new Run = %s, %v; want 0.0", v.Inspect(), err)
	}
}

func TestTimeMonoNeedsAClock(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.EnableTime = true
	_, err := New(o).Eval(context.Background(), `include time; time.mono()`, nil)
	if err == nil || !strings.Contains(err.Error(), "the host did not enable a clock") {
		t.Errorf("time.mono without Options.Now: error = %v; want the clock to be named", err)
	}
}

func TestCryptoRowsOfRFC0016(t *testing.T) {
	t.Parallel()
	cases := []struct{ src, want string }{
		{`crypto.sha512("abc")`, `"ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"`},
		{`crypto.sha384("abc")`, `"cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7"`},
		{`crypto.sha512("привет").take(16)`, `"544f0ba9e87f40e9"`},
		// RFC 4231, test case 2.
		{`crypto.hmac("Jefe", "what do ya want for nothing?", "sha512")`, `"164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737"`},
		{`crypto.hmac("Jefe", "what do ya want for nothing?", "sha384")`, `"af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649"`},
	}
	for _, tt := range cases {
		got, err := evalRow(t, "include crypto; "+tt.src)
		if err != nil || got != tt.want {
			t.Errorf("%s = %s, %v; want %s", tt.src, got, err, tt.want)
		}
	}
}

// crypto.random reads Options.CryptoRand, and only that: Options.Rand is not enough.
func TestCryptoRandomReadsTheHostSource(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.CryptoRand = bytes.NewReader([]byte{0x00, 0x01, 0xab, 0xff, 0x10})
	in := New(o)
	v, err := in.Eval(context.Background(), `include crypto; [crypto.random(4), crypto.random(0)]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Inspect(); got != `["0001abff",""]` {
		t.Errorf("crypto.random from a fixed source = %s; want [\"0001abff\",\"\"]", got)
	}
	// The source has one byte left: a short read is an error, never a short token.
	_, err = in.Eval(context.Background(), `include crypto; crypto.random(2)`, nil)
	if err == nil || !strings.Contains(err.Error(), "source of randomness failed") {
		t.Errorf("a source that runs short: error = %v; want it named", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy pool on fire") }

func TestCryptoRandomRefuses(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.CryptoRand = failingReader{}
	o.MaxStringBytes = 64
	in := New(o)
	for src, want := range map[string]string{
		`crypto.random(-1)`:  "expects a non-negative byte count, got -1",
		`crypto.random("4")`: "expects an integer",
		`crypto.random(1)`:   "entropy pool on fire",
		`crypto.random(33)`:  "string",
	} {
		_, err := in.Eval(context.Background(), "include crypto; "+src, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v; want it to contain %q", src, err, want)
		}
	}
}

func TestCryptoRandomIsAbsentWithoutCryptoRand(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.Rand = nil
	in := New(o)
	v, err := in.Eval(context.Background(), `include crypto; crypto.keys.has("random")`, nil)
	if err != nil || v.Inspect() != "false" {
		t.Errorf(`crypto.keys.has("random") without CryptoRand = %s, %v; want false`, v.Inspect(), err)
	}
	_, err = in.Eval(context.Background(), `include crypto; crypto.random(16)`, nil)
	if err == nil || !strings.Contains(err.Error(), "Options.CryptoRand") {
		t.Errorf("crypto.random without CryptoRand: error = %v; want it to name Options.CryptoRand", err)
	}
}

// The guards a script cannot reach through the module: a count past what one string may
// hold, a step budget the bytes overrun, a row called without its source, and t.ms on a
// value that is not a time.
func TestCryptoRandomAndTimeGuards(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	o.CryptoRand = bytes.NewReader(make([]byte, 1<<20))
	in := New(o)
	_, err := in.Eval(context.Background(), `include crypto; crypto.random(3000000000)`, nil)
	if err == nil || !strings.Contains(err.Error(), "is too many") {
		t.Errorf("crypto.random(3000000000): error = %v; want the count refused", err)
	}

	// Past half of MaxInt32 the hex length would not fit a 32-bit int.
	_, err = in.Eval(context.Background(), `include crypto; crypto.random(1073741824)`, nil)
	if err == nil || !strings.Contains(err.Error(), "is too many") {
		t.Errorf("crypto.random(2^30): error = %v; want the count refused", err)
	}

	o.StepBudget = 50
	o.MaxStringBytes = 1 << 22
	_, err = New(o).Eval(context.Background(), `include crypto; crypto.random(1000000)`, nil)
	if err == nil || !strings.Contains(err.Error(), "step") {
		t.Errorf("crypto.random past the step budget: error = %v; want a step limit", err)
	}

	c := colCtx(t, DefaultOptions())
	_, err = cryRandom(c, []Value{Int(4)})
	if err == nil || !strings.Contains(err.Error(), "Options.CryptoRand") {
		t.Errorf("cryRandom with no source: error = %v; want it to name Options.CryptoRand", err)
	}

	tc := colCtx(t, colTimeOpts())
	if _, err := colInvoke(tc, KTime, "ms", Int(1)); err == nil {
		t.Error("ms on an int: want an error, got none")
	}
}

// strftime stops building once its output passes the string cap: fifty `%s` are a
// 100-byte layout that would render as 500 bytes.
func TestTimeStrftimeStopsAtTheStringCap(t *testing.T) {
	t.Parallel()
	o := colTimeOpts()
	o.MaxStringBytes = 100
	c := colCtx(t, o)
	_, err := colInvoke(c, KTime, "strftime", timeOf(colClock()), Str(strings.Repeat("%s", 50)))
	if err == nil || !strings.Contains(err.Error(), "string too large") {
		t.Errorf("strftime past the cap: error = %v; want the string limit", err)
	}
	if out, _ := timevStrftime(colClock(), strings.Repeat("%s", 50), 100); len(out) > 100+20 {
		t.Errorf("timevStrftime built %d bytes past a cap of 100; want it to stop within one directive", len(out))
	}
	got, err := colInvoke(c, KTime, "strftime", timeOf(colClock()), Str(strings.Repeat("%s", 9)))
	if err != nil || len(got.Str()) != 90 {
		t.Errorf("nine %%s under the cap = %q, %v; want 90 digits", got.Str(), err)
	}
}

// gateReader blocks until open is closed, then answers zeros.
type gateReader struct{ open chan struct{} }

func (g gateReader) Read(p []byte) (int, error) {
	<-g.open
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// crypto.random reads with the Run's lock released, as io and http do: a source that
// waits on a task gets its answer instead of holding the lock the task needs.
func TestCryptoRandomReadsOutsideTheRunLock(t *testing.T) {
	t.Parallel()
	g := gateReader{open: make(chan struct{})}
	o := DefaultOptions()
	o.CryptoRand = g
	o.Timeout = 5 * time.Second
	in := New(o)
	in.Register("release", 0, func(c *Ctx, _ []Value) (Value, error) {
		close(g.open)
		return Nil(), nil
	})
	src := `include crypto
async fn opener() { release() }
t = opener()
crypto.random(2)`
	type result struct {
		v   Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := in.Eval(context.Background(), src, nil)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err != nil || r.v.Str() != "0000" {
			t.Errorf("crypto.random while a task opens its source = %q, %v; want \"0000\"", r.v.Str(), r.err)
		}
	case <-time.After(3 * time.Second):
		close(g.open) // let the stuck Run finish, so the goroutine does not outlive the test
		t.Fatal("crypto.random held the Run's lock while it waited on its source: the task that opens it never ran")
	}
}
