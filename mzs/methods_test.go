package mzs

import "testing"

// D17: every operation has exactly one name. The rename table of §19.2 lists the Ruby
// spellings the migration rewrites; none of them may exist in the standard library, or
// the did-you-mean diagnostic would point at a name that also works.
func TestNoAliasesForRenamedNames(t *testing.T) {
	for _, name := range renameKeys {
		t.Run(name, func(t *testing.T) {
			if HasMethodAnyKind(name) {
				t.Errorf("%q is registered as a method; D17 forbids the alias, use %q",
					name, renameTable[name])
			}
			if HasBuiltin(name) {
				t.Errorf("%q is registered as a builtin; D17 forbids the alias, use %q",
					name, renameTable[name])
			}
		})
	}
}

// The UFCS hooks the compile step uses (§6.3 step 2, §8.7) must agree with dispatch:
// HasMethod is the per-kind answer, HasMethodAnyKind the kind-blind one Compile can ask
// before a receiver exists.
func TestMethodHooksAgreeWithLookup(t *testing.T) {
	kinds := []Kind{KNil, KBool, KInt, KFloat, KString, KRegex, KArray, KDict, KFunc,
		KTime, KRange, KTask, KSeq, KAny}

	for _, k := range kinds {
		t.Run(k.String(), func(t *testing.T) {
			for _, name := range MethodNames(k) {
				if !HasMethod(k, name) {
					t.Errorf("HasMethod(%s, %q) = false; want true", k, name)
				}
				if !HasMethodAnyKind(name) {
					t.Errorf("HasMethodAnyKind(%q) = false; want true", name)
				}
			}
			if HasMethod(k, "нетакого") {
				t.Errorf("HasMethod(%s, \"нетакого\") = true; want false", k)
			}
			// The universal table is reachable from every kind (§12.1).
			for _, name := range MethodNames(KAny) {
				if !HasMethod(k, name) {
					t.Errorf("HasMethod(%s, %q) = false; the KAny table must be reachable", k, name)
				}
			}
		})
	}
}

// A gated row is absent rather than failing loudly, so a script that never asks for a
// clock or randomness cannot tell the capability exists (§14.3).
func TestMethodAvailability(t *testing.T) {
	tests := []struct {
		name string
		m    Method
		opts Options
		want bool
	}{
		{"plain row", Method{Name: "len"}, Options{}, true},
		{"rand row without a source", Method{Name: "sample", NeedsRand: true}, Options{}, false},
		{"now row without a clock", Method{Name: "now", NeedsNow: true}, Options{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.opts
			if got := tt.m.Available(&o); got != tt.want {
				t.Errorf("Available() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestMethodCheckArity(t *testing.T) {
	tests := []struct {
		name    string
		m       Method
		n       int
		wantErr bool
	}{
		{"exact", Method{Name: "get", Min: 1, Max: 2}, 1, false},
		{"optional argument", Method{Name: "get", Min: 1, Max: 2}, 2, false},
		{"too few", Method{Name: "get", Min: 1, Max: 2}, 0, true},
		{"too many", Method{Name: "get", Min: 1, Max: 2}, 3, true},
		{"variadic takes anything", Method{Name: "push", Min: 0, Max: -1}, 9, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.CheckArity(tt.n)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckArity(%d) error = %v; wantErr %v", tt.n, err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if e := asError(err); e.Kind != ErrKindArgument {
				t.Errorf("Kind = %q; want %q", e.Kind, ErrKindArgument)
			}
		})
	}
}
