package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TEST_PROP_Z expected-count guard (vibe-2026-10-31 E1-S3, stats-review
// WS-13): the normal approximation behind z weakens when any group's
// expected successes or failures under the null rate — N*_g·p̂ or
// N*_g·(1 − p̂), p̂ the pooled rate (two-proportion) or p0
// (one-proportion) — is below 5. The result then carries
// PULSE_TEST_EXPECTED_COUNT_TOO_LOW, as TEST_CHISQ's does; N*_g is the
// test's own base (Σw_g under frequency, Kish n_eff_g under
// probability).

// propZRows builds per-group rows: succ "yes" then (n − succ) "no", each
// weighing w.
func propZRows(w float64, groups ...struct {
	g       string
	n, succ int
}) []cRow {
	var rows []cRow
	for _, gr := range groups {
		for i := range gr.n {
			o := "no"
			if i < gr.succ {
				o = "yes"
			}
			rows = append(rows, cRow{g: gr.g, o: o, w: w})
		}
	}
	return rows
}

type pzGroup = struct {
	g       string
	n, succ int
}

func expectedLow(res *types.TestResult) bool {
	for _, w := range res.Warnings {
		if strings.HasPrefix(w, string(errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW)+":") {
			return true
		}
	}
	return false
}

func TestPropZ_ExpectedCountGuard_Unweighted(t *testing.T) {
	t.Parallel()

	// Pooled 0.1 on 10 + 10 rows: expected successes 1 per group.
	low := runCountTest(t, propZSpec, propZRows(1, pzGroup{"a", 10, 1}, pzGroup{"b", 10, 1}), "")
	if !expectedLow(low) {
		t.Fatalf("pooled 0.1, n 10/10: want %s, got %v", errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, low.Warnings)
	}
	// Pooled 0.5 on 50 + 50 rows: expected 25 / 25 per group.
	ok := runCountTest(t, propZSpec, propZRows(1, pzGroup{"a", 50, 25}, pzGroup{"b", 50, 25}), "")
	if expectedLow(ok) || len(ok.Warnings) != 0 {
		t.Fatalf("pooled 0.5, n 50/50: want no warning, got %v", ok.Warnings)
	}
	// The failure side counts too: pooled 0.95 on 40 + 40 rows leaves 2
	// expected failures per group.
	fail := runCountTest(t, propZSpec, propZRows(1, pzGroup{"a", 40, 38}, pzGroup{"b", 40, 38}), "")
	if !expectedLow(fail) {
		t.Fatalf("pooled 0.95, n 40/40: want %s, got %v", errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, fail.Warnings)
	}
}

// TestPropZ_ExpectedCountGuard_Weighted: 6 + 6 rows at pooled 0.5, every
// row weighing 4. Frequency reads N*_g = Σw_g = 24 (expected 12, no
// warning); probability reads N*_g = n_eff_g = 6 (expected 3, warns) —
// the base the z itself uses.
func TestPropZ_ExpectedCountGuard_Weighted(t *testing.T) {
	t.Parallel()

	rows := propZRows(4, pzGroup{"a", 6, 3}, pzGroup{"b", 6, 3})
	if freq := runCountTest(t, propZSpec, rows, types.WeightKindFrequency); expectedLow(freq) {
		t.Fatalf("frequency (N*_g 24): want no warning, got %v", freq.Warnings)
	}
	if prob := runCountTest(t, propZSpec, rows, types.WeightKindProbability); !expectedLow(prob) {
		t.Fatalf("probability (N*_g 6): want %s, got %v", errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, prob.Warnings)
	}
}

// TestPropZ_ExpectedCountGuard_OneProportion reads N*·p0 and N*·(1 − p0).
func TestPropZ_ExpectedCountGuard_OneProportion(t *testing.T) {
	t.Parallel()

	one := func(p0 string) types.Test {
		return types.Test{Type: types.TEST_PROP_Z, Field: "o",
			Params: json.RawMessage(`{"success":"yes","p0":` + p0 + `}`)}
	}
	rows := propZRows(1, pzGroup{"a", 20, 4})
	if low := runCountTest(t, one("0.1"), rows, ""); !expectedLow(low) {
		t.Fatalf("p0 0.1, n 20: want %s, got %v", errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW, low.Warnings)
	}
	if ok := runCountTest(t, one("0.5"), rows, ""); expectedLow(ok) {
		t.Fatalf("p0 0.5, n 20: want no warning, got %v", ok.Warnings)
	}
}

// TestPropZ_ExpectedCountGuard_Adjusted: the CUPED variant reads the
// same pooled base.
func TestPropZ_ExpectedCountGuard_Adjusted(t *testing.T) {
	t.Parallel()

	spec := types.Test{Type: types.TEST_PROP_Z, Field: "o", SplitBy: "g",
		Params: json.RawMessage(`{"success":"yes","covariate":"w"}`)}
	rows := propZRows(1, pzGroup{"a", 10, 1}, pzGroup{"b", 10, 1})
	for i := range rows {
		rows[i].w = float64(i%3 + 1)
	}
	res := runCountTest(t, spec, rows, "")
	if res.Variant != "two_proportion_adjusted" || !expectedLow(res) {
		t.Fatalf("adjusted pooled 0.1, n 10/10: variant %q warnings %v", res.Variant, res.Warnings)
	}
}
