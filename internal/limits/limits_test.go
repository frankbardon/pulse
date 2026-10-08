package limits

import (
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
)

func wantDefaults() Limits {
	return Limits{
		RequestTimeout:     Unlimited,
		MaxGroups:          10_000_000,
		MaxCrosstabCells:   10_000_000,
		MaxEstimatedMemory: Unlimited,
		MaxMatrixDim:       2_048,
		MaxComposeSlots:    1_000,
		MaxChainStages:     1_000,
		MaxJoinBuildRows:   100_000_000,
	}
}

func TestDefaults(t *testing.T) {
	if got := Defaults(); got != wantDefaults() {
		t.Fatalf("Defaults() = %+v, want %+v", got, wantDefaults())
	}
	if got := Resolve(Limits{}, nil); got != wantDefaults() {
		t.Fatalf("Resolve(zero, nil) = %+v, want defaults", got)
	}
}

func TestNamesAndOptions(t *testing.T) {
	want := map[Name]string{
		RequestTimeout:     "Options.Limits.RequestTimeout",
		MaxGroups:          "Options.Limits.MaxGroups",
		MaxCrosstabCells:   "Options.Limits.MaxCrosstabCells",
		MaxEstimatedMemory: "Options.Limits.MaxEstimatedMemory",
		MaxMatrixDim:       "Options.Limits.MaxMatrixDim",
		MaxComposeSlots:    "Options.Limits.MaxComposeSlots",
		MaxChainStages:     "Options.Limits.MaxChainStages",
		MaxJoinBuildRows:   "Options.Limits.MaxJoinBuildRows",
	}
	names := Names()
	if len(names) != len(want) {
		t.Fatalf("Names() has %d entries, want %d", len(names), len(want))
	}
	for _, n := range names {
		if Option(n) != want[n] {
			t.Errorf("Option(%s) = %q, want %q", n, Option(n), want[n])
		}
	}
	if Option("nope") != "" {
		t.Error("Option of an unknown name is not empty")
	}
}

// setField returns l with the named field set to v.
func setField(t *testing.T, l Limits, n Name, v int64) Limits {
	t.Helper()
	f, ok := lookup(n)
	if !ok {
		t.Fatalf("unknown limit %s", n)
	}
	f.set(&l, v)
	return l
}

// TestResolve_Precedence: per field, a non-zero Options value beats a
// non-zero profile value, which beats the default — every combination
// of the three layers, on every field, with the other fields left at
// their defaults.
func TestResolve_Precedence(t *testing.T) {
	const optV, profV = 7, 11
	cases := []struct {
		name       string
		opt, prof  int64 // 0 = layer unset
		nilProfile bool
		want       func(def int64) int64
	}{
		{"default only (nil profile)", 0, 0, true, func(d int64) int64 { return d }},
		{"default only (empty profile)", 0, 0, false, func(d int64) int64 { return d }},
		{"profile only", 0, profV, false, func(int64) int64 { return profV }},
		{"options only", optV, 0, false, func(int64) int64 { return optV }},
		{"options only (nil profile)", optV, 0, true, func(int64) int64 { return optV }},
		{"options beat profile", optV, profV, false, func(int64) int64 { return optV }},
		{"options unlimited beats profile", Unlimited, profV, false, func(int64) int64 { return Unlimited }},
		{"profile unlimited beats default", 0, Unlimited, false, func(int64) int64 { return Unlimited }},
		{"options beat profile unlimited", optV, Unlimited, false, func(int64) int64 { return optV }},
	}
	for _, f := range fields {
		for _, tc := range cases {
			t.Run(string(f.name)+"/"+tc.name, func(t *testing.T) {
				opts := setField(t, Limits{}, f.name, tc.opt)
				var prof *Limits
				if !tc.nilProfile {
					p := setField(t, Limits{}, f.name, tc.prof)
					prof = &p
				}
				got := Resolve(opts, prof)
				want := setField(t, Defaults(), f.name, tc.want(f.def))
				if got != want {
					t.Fatalf("Resolve = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestResolve_DoesNotMutateInputs(t *testing.T) {
	opts := Limits{MaxGroups: 3}
	prof := Limits{MaxMatrixDim: 4}
	_ = Resolve(opts, &prof)
	if opts != (Limits{MaxGroups: 3}) || prof != (Limits{MaxMatrixDim: 4}) {
		t.Fatal("Resolve mutated an input")
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(Limits{}); err != nil {
		t.Fatalf("zero Limits refused: %v", err)
	}
	for _, f := range fields {
		for _, ok := range []int64{0, Unlimited, 1, 1 << 40} {
			if err := Validate(setField(t, Limits{}, f.name, ok)); err != nil {
				t.Errorf("%s=%d refused: %v", f.name, ok, err)
			}
		}
		for _, bad := range []int64{-2, -1000} {
			err := Validate(setField(t, Limits{}, f.name, bad))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_INVALID {
				t.Fatalf("%s=%d: err = %v, want PULSE_LIMIT_INVALID", f.name, bad, err)
			}
			if ce.Details["limit"] != string(f.name) || ce.Details["value"] != bad {
				t.Errorf("%s=%d: details = %v", f.name, bad, ce.Details)
			}
			if !strings.Contains(ce.Message, Option(f.name)) {
				t.Errorf("%s: message %q does not name %s", f.name, ce.Message, Option(f.name))
			}
		}
	}
}

func TestExceeded(t *testing.T) {
	ce := Exceeded(MaxGroups, 10_000_000, 10_000_001)
	if ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("code = %s", ce.Code)
	}
	want := map[string]any{
		"limit":      "max_groups",
		"configured": int64(10_000_000),
		"observed":   int64(10_000_001),
		"option":     "Options.Limits.MaxGroups",
	}
	if len(ce.Details) != len(want) {
		t.Fatalf("details = %v, want %v", ce.Details, want)
	}
	for k, v := range want {
		if ce.Details[k] != v {
			t.Errorf("details[%s] = %v, want %v", k, ce.Details[k], v)
		}
	}
	for _, sub := range []string{"10,000,000", "10,000,001", "Options.Limits.MaxGroups", "limits.max_groups", "--limit max_groups="} {
		if !strings.Contains(ce.Message, sub) {
			t.Errorf("message %q lacks %q", ce.Message, sub)
		}
	}
	tm := Exceeded(RequestTimeout, int64(2*time.Second), int64(3*time.Second))
	if !strings.Contains(tm.Message, "configured 2s") {
		t.Errorf("timeout message %q does not render the configured duration", tm.Message)
	}
}

func TestGroupThousands(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", -1: "-1", -12345: "-12,345", 100_000_000: "100,000,000"} {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestIsUnlimited(t *testing.T) {
	if !IsUnlimited(Unlimited) || IsUnlimited(1) {
		t.Fatal("IsUnlimited misclassifies")
	}
}
