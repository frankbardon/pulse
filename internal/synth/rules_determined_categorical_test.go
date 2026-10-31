package synth_test

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// determinedCategoricalSpec declares `band` with NO distribution, and
// rules decides who writes it. `score` and `tail` are the RNG probes.
func determinedCategoricalSpec(bandType string, withBand bool, rules string) string {
	band := ""
	if withBand {
		band = `{"name": "band", "type": "` + bandType + `"},`
	}
	return `{"row_count": 400, "fields": [
		{"name": "score", "type": "u8", "distribution": "uniform", "params": {"min": 0, "max": 9.999}},
		` + band + `
		{"name": "tail", "type": "f64", "distribution": "normal", "params": {"mean": 0, "std": 1}}
	], "rules": [` + rules + `]}`
}

const bandRule = `{"set_expr": {"band": "round(score) >= 5 ? \"high\" : \"low\""}}`

// TestSpec_RuleDeterminedCategoricalNeedsNoSampler is #365(a), the
// accepted half: an unconditional set_expr writes `band` on every row,
// so the spec no longer has to declare a throwaway weighted_categorical
// for it — and the sampler-less field consumes no RNG, so every other
// field is cell-identical to a spec without `band` at all.
func TestSpec_RuleDeterminedCategoricalNeedsNoSampler(t *testing.T) {
	t.Parallel()

	spec, err := synth.ParseSpec([]byte(determinedCategoricalSpec("categorical_u8", true, bandRule)))
	if err != nil {
		t.Fatalf("a rule-determined categorical must need no distribution: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	score := readF64Field(t, data, "score")
	bands := readCategoricalField(t, data, "band")
	for i := range score {
		want := "low"
		if score[i] >= 5 {
			want = "high"
		}
		if bands[i] != want {
			t.Fatalf("row %d: band=%q for score %v, want %q", i, bands[i], score[i], want)
		}
	}

	bare, err := synth.ParseSpec([]byte(determinedCategoricalSpec("", false, "")))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := synth.SynthBytes(bare, synth.Options{Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"score", "tail"} {
		a, b := readF64Field(t, data, f), readF64Field(t, plain, f)
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%s row %d moved (%v vs %v): the sampler-less field consumed RNG", f, i, a[i], b[i])
			}
		}
	}
}

// TestSpec_EmptyDistributionStillRefusedWithoutAPriorityZeroRule is the
// refused half: every rule shape that leaves some row to the field's own
// generation keeps the empty distribution an error, as does a
// non-categorical field and a sampler-less field carrying params.
func TestSpec_EmptyDistributionStillRefusedWithoutAPriorityZeroRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, bandType, rules, want string
	}{
		{"no rule", "categorical_u8", "", "field has empty distribution"},
		{"gated by when", "categorical_u8",
			`{"when": "score > 2", "set": {"band": "high"}}`, "field has empty distribution"},
		{"set_null only", "categorical_u8",
			`{"set_null": ["band"]}`, "field has empty distribution"},
		{"self-reading set_expr", "categorical_u8",
			`{"set_expr": {"band": "band + \"x\""}}`, "field has empty distribution"},
		{"not a categorical", "u8",
			`{"set_expr": {"band": "score"}}`, "field has empty distribution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := synth.ParseSpec([]byte(determinedCategoricalSpec(tc.bandType, true, tc.rules)))
			assertValidation(t, err, tc.want)
		})
	}

	t.Run("params without a distribution", func(t *testing.T) {
		raw := strings.Replace(determinedCategoricalSpec("categorical_u8", true, bandRule),
			`"type": "categorical_u8"}`, `"type": "categorical_u8", "params": {"values": ["high", "low"]}}`, 1)
		_, err := synth.ParseSpec([]byte(raw))
		assertValidation(t, err, "takes no params")
	})
}

func assertValidation(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected refusal %q, got nil", want)
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("expected SERVICE_VALIDATION, got %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not carry %q", err, want)
	}
}
