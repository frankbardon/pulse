package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TEST_KS ties warning (vibe-2026-10-31 E1-S9, stats-review WS-10): on
// the ASYMPTOTIC arm (Σw₁·Σw₂ ≥ 10000) the limiting Kolmogorov
// distribution assumes a continuous variable, so when tied
// observations make up ≥ 50% of the pooled sample the result carries
// PULSE_TEST_TIES_DOMINATE — the rank tests' rule and message. Weighted,
// a row of weight w > 1 is itself a tie of size w (the expansion's tie
// runs). The exact arm conditions on the tie pattern and stays silent.

var ksSpec = types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}

func ksTiesWarned(res *types.TestResult) bool {
	for _, w := range res.Warnings {
		if strings.HasPrefix(w, string(errors.PULSE_TEST_TIES_DOMINATE)+":") {
			return true
		}
	}
	return false
}

// ksTieHeavyRows: 5 distinct values per group, each row weighing w.
func ksTieHeavyRows(w float64) []wRow {
	var rows []wRow
	for i := range 5 {
		rows = append(rows, wRow{x: float64(i), g: "a", w: w})
		rows = append(rows, wRow{x: float64(i) + 0.5, g: "b", w: w})
	}
	return rows
}

// ksExpand physically expands integer weights into unit rows.
func ksExpand(rows []wRow) []wRow {
	var out []wRow
	for _, r := range rows {
		for range int(r.w) {
			out = append(out, wRow{x: r.x, y: r.y, g: r.g, w: 1})
		}
	}
	return out
}

// ksDistinctRows: n distinct values per group, unit weights.
func ksDistinctRows(n int) []wRow {
	var rows []wRow
	for i := range n {
		rows = append(rows, wRow{x: float64(2 * i), g: "a", w: 1})
		rows = append(rows, wRow{x: float64(2*i) + 1.25, g: "b", w: 1})
	}
	return rows
}

func TestKS_TiesWarning_WeightedAsymptotic(t *testing.T) {
	t.Parallel()

	// Σw = 150 per group (22500 ≥ 10000: asymptotic); every row is a tie
	// run of 30 on the expansion.
	rows := ksTieHeavyRows(30)
	w := runWeightedRowTest(t, ksSpec, rows, types.WeightKindFrequency)
	if !ksTiesWarned(w) {
		t.Fatalf("weighted tie-heavy asymptotic: want %s, got %v", errors.PULSE_TEST_TIES_DOMINATE, w.Warnings)
	}
	// The same pair expanded and run unweighted (n 150/150) warns alike.
	u := runWeightedRowTest(t, ksSpec, ksExpand(rows), "")
	if !ksTiesWarned(u) {
		t.Fatalf("unweighted expansion: want %s, got %v", errors.PULSE_TEST_TIES_DOMINATE, u.Warnings)
	}
	// Mixed: group a's 5 rows weigh 30 (tie runs of 30), group b's 150
	// unit rows are distinct — ties are exactly half of Σw = 300, so it
	// warns only if each weighted row counts as a tie of its weight.
	var mixed []wRow
	for i := range 5 {
		mixed = append(mixed, wRow{x: float64(i), g: "a", w: 30})
	}
	for i := range 150 {
		mixed = append(mixed, wRow{x: float64(i) + 0.25, g: "b", w: 1})
	}
	if res := runWeightedRowTest(t, ksSpec, mixed, types.WeightKindFrequency); !ksTiesWarned(res) {
		t.Fatalf("mixed weighted: want %s, got %v", errors.PULSE_TEST_TIES_DOMINATE, res.Warnings)
	}
	if res := runWeightedRowTest(t, ksSpec, ksExpand(mixed), ""); !ksTiesWarned(res) {
		t.Fatalf("mixed expanded: want %s, got %v", errors.PULSE_TEST_TIES_DOMINATE, res.Warnings)
	}
	wj, _ := json.Marshal(w.Warnings)
	uj, _ := json.Marshal(u.Warnings)
	if string(wj) != string(uj) {
		t.Fatalf("weighted warnings %s != expanded unweighted %s", wj, uj)
	}
}

func TestKS_TiesWarning_NoTiesSilent(t *testing.T) {
	t.Parallel()

	// 120 distinct values per group (14400: asymptotic), weighted at
	// unit weight and unweighted: no tie, no warning.
	rows := ksDistinctRows(120)
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, ""} {
		res := runWeightedRowTest(t, ksSpec, rows, kind)
		if len(res.Warnings) != 0 {
			t.Fatalf("kind %q, no ties: want no warning, got %v", kind, res.Warnings)
		}
	}
}

func TestKS_TiesWarning_ExactSilent(t *testing.T) {
	t.Parallel()

	// Σw = 15 per group (225 < 10000: exact) with every row a tie run of
	// 3, and its unweighted expansion: the exact arm conditions on the
	// tie pattern and never warns.
	rows := ksTieHeavyRows(3)
	for _, tc := range []struct {
		kind types.WeightKind
		rows []wRow
	}{{types.WeightKindFrequency, rows}, {"", ksExpand(rows)}} {
		res := runWeightedRowTest(t, ksSpec, tc.rows, tc.kind)
		if len(res.Warnings) != 0 {
			t.Fatalf("kind %q, exact arm: want no warning, got %v", tc.kind, res.Warnings)
		}
	}
}

func TestKSPost_TiesWarning(t *testing.T) {
	t.Parallel()

	post, err := newKSPost(&ksSpec, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := func(rows []wRow) *types.TestResult {
		t.Helper()
		var in []map[string]any
		for _, r := range rows {
			in = append(in, map[string]any{"g": r.g, "x": r.x})
		}
		res, err := post.Run(in)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run(ksExpand(ksTieHeavyRows(30))); !ksTiesWarned(res) {
		t.Fatalf("post tie-heavy asymptotic: want %s, got %v", errors.PULSE_TEST_TIES_DOMINATE, res.Warnings)
	}
	if res := run(ksDistinctRows(120)); len(res.Warnings) != 0 {
		t.Fatalf("post no ties: want no warning, got %v", res.Warnings)
	}
	if res := run(ksExpand(ksTieHeavyRows(3))); len(res.Warnings) != 0 {
		t.Fatalf("post exact arm: want no warning, got %v", res.Warnings)
	}
}
