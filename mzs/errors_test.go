package mzs

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// §17: every name error carries a suggestion, drawn from a Levenshtein-1 search over the
// candidates plus an exact lookup in the rename table of §19.2.
func TestSuggest(t *testing.T) {
	methods := []string{"len", "lower", "upper", "trim", "replace", "split", "has"}

	tests := []struct {
		name       string
		typed      string
		candidates []string
		want       string
	}{
		{"rename table wins outright", "downcase", methods, "lower"},
		{"rename table with no near miss", "gsub", methods, "replace"},
		{"conversion rename", "to_i", methods, "int"},
		{"one edit from a real method", "lowerr", methods, "lower"},
		{"a transposition is one edit", "tirm", methods, "trim"},
		{"two edits from a real method", "lowwerr", methods, ""},
		{"no candidates, no near miss", "lowerr", nil, ""},
		{"one edit from a renamed spelling", "lenght", methods, "len"},
		{"nothing close", "штука", methods, ""},
		{"empty name", "", methods, ""},
		{"never the name itself", "now", []string{"now", "nod"}, "nod"},
		{"the name itself and nothing else", "now", []string{"now"}, ""},
		// A different case is a typo, not the name: `NOW()` is fixed by `now`.
		{"the name in another case is still suggested", "NOW", []string{"now", "pow"}, "now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suggest(tt.typed, tt.candidates); got != tt.want {
				t.Errorf("suggest(%q) = %q; want %q", tt.typed, got, tt.want)
			}
		})
	}
}

func TestUndefinedNameErrors(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{"method names the receiver kind", undefinedMethodError(KString, "штука"),
			"undefined method 'штука' for string"},
		{"method suggests the rename", undefinedMethodError(KString, "downcase"),
			"undefined method 'downcase' for string (did you mean 'lower'?)"},
		{"function suggests the rename", undefinedFunctionError("puts", nil),
			"undefined function 'puts' (did you mean 'println'?)"},
		{"variable is a name error", undefinedVariableError("штука", nil),
			"undefined variable 'штука'"},
		{"variable suggests a visible local", undefinedVariableError("sentt", []string{"sent"}),
			"undefined variable 'sentt' (did you mean 'sent'?)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Kind != ErrKindName {
				t.Errorf("Kind = %q; want %q", tt.err.Kind, ErrKindName)
			}
			if tt.err.Msg != tt.want {
				t.Errorf("Msg = %q; want %q", tt.err.Msg, tt.want)
			}
		})
	}
}

// §17: `<file>:<line>:<col>: <kind>: <message>`.
func TestErrorFormatting(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want string
	}{
		{"positioned", (&Error{Kind: ErrKindType, Msg: "cannot add int to string"}).At("cond#12", 1, 12),
			"cond#12:1:12: type: cannot add int to string"},
		{"no position", &Error{Kind: ErrKindRaise, Msg: "bad"}, "raise: bad"},
		{"At does not overwrite", (&Error{Kind: ErrKindName, Msg: "x", File: "a", Line: 2, Col: 3}).At("b", 9, 9),
			"a:2:3: name: x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q; want %q", got, tt.want)
			}
		})
	}
}

// §8.11 and §14.1: `try … else …` catches script errors and nothing else. A limit is
// unrecoverable and must always reach the host.
func TestErrorCatchable(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want bool
	}{
		{"type error", typeErrorf("cannot add int to string"), true},
		{"raise", raiseError("bad", Nil()), true},
		{"name error", nameErrorf("undefined variable 'x'"), true},
		{"timeout", limitErrorf(ErrTimeout, "timed out"), false},
		{"budget", limitErrorf(ErrBudget, "budget"), false},
		{"depth", limitErrorf(ErrDepth, "depth"), false},
		{"canceled", limitErrorf(ErrCanceled, "canceled"), false},
		{"host fatal", wrapError(ErrKindRaise, ErrFatal, "host said no"), false},
		{"internal", newError(ErrKindInternal, "bug"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Catchable(); got != tt.want {
				t.Errorf("Catchable() = %v; want %v", got, tt.want)
			}
		})
	}
}

// The dict bound by `try X else (e) -> …` (§8.11).
func TestErrorValue(t *testing.T) {
	e := raiseError("нет", Dict(Str("code"), Int(7)))
	e.At("s.mzs", 3, 4)
	v := e.ErrorValue()

	tests := []struct {
		key  string
		want string
	}{
		{"message", "нет"},
		{"kind", ErrKindRaise},
		{"line", "3"},
		{"data", `{"code":7}`},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := v.Get(Str(tt.key)).Str(); got != tt.want {
				t.Errorf("err[%q] = %q; want %q", tt.key, got, tt.want)
			}
		})
	}
}

// §13.5: the kinds the runtime keeps for itself are the ones a script could otherwise
// lie with — `try` reads two of them and a host reads the third.
func TestCheckKind(t *testing.T) {
	tests := []struct {
		kind string
		ok   bool
	}{
		{"user", true},
		{"billing", true},
		{"json", true},   // a runtime kind a script may still name: it claims nothing
		{"limit", false}, // what `try` refuses to catch
		{"exit", false},  // what a host reads as a status
		{"internal", false},
		{"syntax", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			err := checkKind(tt.kind)
			if (err == nil) != tt.ok {
				t.Fatalf("checkKind(%q) = %v; want ok = %v", tt.kind, err, tt.ok)
			}
			if err != nil && err.Kind != ErrKindArgument {
				t.Errorf("checkKind(%q).Kind = %q; want %q", tt.kind, err.Kind, ErrKindArgument)
			}
		})
	}
}

// §8.11: the dict a handler binds knows the error it came from, and nothing in the
// language can see that link. It is what `raise(e)` re-throws with.
func TestErrorValueRemembersItsError(t *testing.T) {
	e := raiseError("нет", Nil())
	e.At("s.mzs", 3, 4)
	v := e.ErrorValue()

	if got := errorSource(v); got != e {
		t.Fatalf("errorSource() = %v; want the error the dict was made from", got)
	}
	var keys []string
	for _, k := range v.odict().Keys() {
		keys = append(keys, k.Str())
	}
	if got, want := strings.Join(keys, ","), "message,kind,file,line,col,stack"; got != want {
		t.Errorf("the dict has keys %s; want the six of §8.11, in order: %s", got, want)
	}
	// A copy is a value like any other: provenance does not travel through the language.
	if got := errorSource(dictOf(v.odict().Clone())); got != nil {
		t.Errorf("a cloned dict kept the source %v; only the bound dict carries it", got)
	}
	if got := errorSource(Dict(Str("message"), Str("нет"))); got != nil {
		t.Errorf("a hand-built dict has a source %v; want none", got)
	}
}

func TestAsErrorKeepsSentinels(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind string
		sentinel error
	}{
		{"already an Error", typeErrorf("boom"), ErrKindType, nil},
		{"timeout sentinel", ErrTimeout, ErrKindLimit, ErrTimeout},
		{"wrapped budget", errors.New("x: " + ErrBudget.Error()), ErrKindRaise, nil},
		{"plain error", errors.New("boom"), ErrKindRaise, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := asError(tt.err)
			if got.Kind != tt.wantKind {
				t.Errorf("Kind = %q; want %q", got.Kind, tt.wantKind)
			}
			if tt.sentinel != nil && !errors.Is(got, tt.sentinel) {
				t.Errorf("errors.Is(%v, %v) = false; want true", got, tt.sentinel)
			}
			if got.Msg == "" || !strings.Contains(got.Error(), got.Msg) {
				t.Errorf("Error() = %q; want it to carry the message", got.Error())
			}
		})
	}
}

// §8.10: return/break/next travel as sentinel values, never as Go panics, and a stdlib
// method must be able to recognise a `break` without unwrapping anything itself.
func TestControlSignals(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCtrl  bool
		wantBreak bool
		wantVal   int64
	}{
		{"break carries its value", breakSignal(Int(1)), true, true, 1},
		{"return is not a break", returnSignal(Int(2)), true, false, 0},
		{"next is not a break", nextSignal(Int(3)), true, false, 0},
		{"a script error is not a signal", typeErrorf("boom"), false, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := isBreak(tt.err)
			if ok != tt.wantBreak {
				t.Fatalf("isBreak() = %v; want %v", ok, tt.wantBreak)
			}
			if ok && v.Int() != tt.wantVal {
				t.Errorf("break value = %d; want %d", v.Int(), tt.wantVal)
			}
			if _, isCtrl := ctrlOf(tt.err); isCtrl != tt.wantCtrl {
				t.Errorf("ctrlOf() = %v; want %v", isCtrl, tt.wantCtrl)
			}
		})
	}
}

// A name the library has but this interpreter cannot reach is gated on a host capability
// (D15), and its diagnostic says which one instead of suggesting the name itself.
func TestCapabilityGapNamesTheOption(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"a clock, called", "now()", "undefined function 'now': it needs a clock, and the host did not install Options.Now (mzs --time)"},
		{"randomness, called", "rand()", "undefined function 'rand': it needs randomness, and the host did not install Options.Rand (mzs --rand)"},
		{"randomness, uuid", "uuid()", "undefined function 'uuid': it needs randomness"},
		{"a gated method", "[1, 2].shuffle", "undefined method 'shuffle' for array: it needs randomness"},
		{"a gated name as a value", "x = now", "undefined variable 'now': it needs a clock"},
		// No row of any kind is spelled `now`, so the compile pass refuses the method
		// itself (undefinedMethod) rather than leaving it to dispatch.
		{"a gated builtin in method position", "5.now", "undefined method 'now': it needs a clock"},
	}

	// A row of some other kind is not this receiver's to enable: with randomness
	// installed `"x".shuffle` would still be undefined, so naming the option would send
	// the reader to switch on something that cannot help.
	for _, src := range []string{`"x".shuffle`, `1.shuffle`} {
		t.Run("no row for this kind: "+src, func(t *testing.T) {
			_, err := New(Options{}).Eval(context.Background(), src, nil)
			if err == nil || strings.Contains(err.Error(), "Options.Rand") || !strings.Contains(err.Error(), "undefined method 'shuffle'") {
				t.Errorf("%s = %v; want undefined method without naming Options.Rand", src, err)
			}
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Options{}).Eval(context.Background(), tt.src, nil)
			if err == nil {
				t.Fatalf("%s ran; want a name error", tt.src)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q; want it to contain %q", msg, tt.want)
			}
			if strings.Contains(msg, "did you mean") {
				t.Errorf("error = %q; a gated name needs no suggestion", msg)
			}
			var me *Error
			if !errors.As(err, &me) || me.Kind != ErrKindName {
				t.Errorf("error = %#v; want kind %q", err, ErrKindName)
			}
		})
	}

	// A *Program compiled where the option was installed can be run by an Interp where it
	// is not. The compile pass saw a builtin and let it through, so the refusal happens at
	// run time — and must name the option there too, for a call and for a method alike.
	t.Run("compiled with the option, run without it", func(t *testing.T) {
		withAll := New(Options{
			Now:  func() time.Time { return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) },
			Rand: rand.New(rand.NewSource(1)),
		})
		for _, c := range []struct{ src, want string }{
			{"now()", "undefined function 'now': it needs a clock"},
			{"[1, 2].shuffle", "undefined method 'shuffle' for array: it needs randomness"},
		} {
			p, err := withAll.Compile("t.mzs", c.src)
			if err != nil {
				t.Fatalf("Compile(%s): %v", c.src, err)
			}
			_, err = New(Options{}).Run(context.Background(), p, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Run(%s) without the option = %v; want an error containing %q", c.src, err, c.want)
			}
		}
	})

	t.Run("installed, it works", func(t *testing.T) {
		o := Options{Now: func() time.Time { return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) }}
		v, err := New(o).Eval(context.Background(), "now().year", nil)
		if err != nil || v.Str() != "2026" {
			t.Fatalf("now().year = %v, %v; want 2026", v, err)
		}
	})

	// A name the host took away on purpose is not one it forgot to enable: the message
	// must not point at the option that would bring it back.
	t.Run("removed with Unregister, no option is named", func(t *testing.T) {
		in := New(Options{})
		in.Unregister("now")
		_, err := in.Eval(context.Background(), "now()", nil)
		if err == nil {
			t.Fatal("now() ran after Unregister")
		}
		msg := err.Error()
		if strings.Contains(msg, "Options.Now") || strings.Contains(msg, "did you mean 'now'") {
			t.Errorf("error = %q; want neither the option nor the name itself", msg)
		}
	})
}
