package mzs

import (
	"errors"
	"strings"
	"testing"
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
			"undefined function 'puts' (did you mean 'say'?)"},
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
