package synth

import (
	"bytes"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// effectSchema is the fixture shape the selection tests use: one numeric
// target, one categorical carrying a real effect and one carrying a
// negligible one. Two candidates rather than one is the point — the rule
// has to SEPARATE them, and a fixture with a single candidate can only
// show it admitting or rejecting everything.
func effectSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	mk := func(levels ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range levels {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict add %q: %v", v, err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "strong", Type: encoding.FieldTypeCategoricalU8, Nullable: true,
			Dictionary: mk("s0", "s1", "s2")},
		{Name: "weak", Type: encoding.FieldTypeCategoricalU8, Nullable: true,
			Dictionary: mk("w0", "w1", "w2")},
	}}
}

// effectRows generates n rows of
//
//	y = strongDelta*strongLevel + weakDelta*weakLevel + N(0,1)
//
// with both categoricals drawn uniformly and independently. With unit
// noise, a three-level field spaced by delta explains (2/3)delta^2 /
// ((2/3)delta^2 + 1) of y's variance — so the caller picks an effect
// SIZE directly, which is what lets the row-count test hold effect size
// fixed while moving n.
func effectRows(n int, strongDelta, weakDelta float64, seed uint64) ([]map[string]any, []map[string]bool) {
	rng := mrand.New(mrand.NewPCG(seed, seed*2654435761+1))
	rows := make([]map[string]any, n)
	nulls := make([]map[string]bool, n)
	for i := 0; i < n; i++ {
		s := rng.IntN(3)
		w := rng.IntN(3)
		y := strongDelta*float64(s) + weakDelta*float64(w) + rng.NormFloat64()
		rows[i] = map[string]any{
			"y":      y,
			"strong": fmt.Sprintf("s%d", s),
			"weak":   fmt.Sprintf("w%d", w),
		}
		nulls[i] = map[string]bool{}
	}
	return rows, nulls
}

// predictorFields returns the distinct source fields a model's
// coefficients were expanded from, in first-seen order.
func predictorFields(m FieldModel) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range m.Predictors {
		if seen[p.Field] {
			continue
		}
		seen[p.Field] = true
		out = append(out, p.Field)
	}
	for _, r := range m.References {
		if seen[r.Field] {
			continue
		}
		seen[r.Field] = true
		out = append(out, r.Field)
	}
	return out
}

// fitEffectCohort runs the whole capture over a generated effect cohort
// and returns the model for "y".
func fitEffectCohort(t *testing.T, n int, strongDelta, weakDelta float64, seed uint64, opts ProfileOptions) (FieldModel, *Profile) {
	t.Helper()
	schema := effectSchema(t)
	rows, nulls := effectRows(n, strongDelta, weakDelta, seed)
	opts.FitModels = true
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)), opts)
	if err != nil {
		t.Fatalf("profileRecords(n=%d): %v", n, err)
	}
	m, ok := modelByField(prof.FittedModels(), "y")
	if !ok {
		t.Fatalf("no model for y at n=%d; warnings=%v", n, prof.Warnings)
	}
	return m, prof
}

// TestModelSelection_AdmitsStrongRejectsNegligible is the rule's core
// claim: a candidate that carries a meaningful share of the target's
// variance enters the model and one that carries a trivial share does
// not, on the SAME cohort, so nothing about sample size or noise level
// can explain the difference.
//
// The deltas are chosen against the closed form in effectRows: 0.25
// spacing puts `strong` at ~4% of y's variance (four times the floor)
// and 0.05 spacing puts `weak` at ~0.17% (a sixth of it).
func TestModelSelection_AdmitsStrongRejectsNegligible(t *testing.T) {
	m, _ := fitEffectCohort(t, 4000, 0.25, 0.05, 7, ProfileOptions{})
	got := predictorFields(m)
	if len(got) != 1 || got[0] != "strong" {
		t.Fatalf("admitted predictors = %v, want exactly [strong]", got)
	}
	// A rejected candidate must be absent, not present with a small
	// coefficient — a design column costs a degree of freedom whether or
	// not its coefficient is near zero.
	for _, p := range m.Predictors {
		if p.Field == "weak" {
			t.Errorf("weak entered the design as %q", p.Column)
		}
	}
	if m.R2 <= 0 {
		t.Errorf("R2 = %v, want a positive fit once the real predictor is in", m.R2)
	}
}

// TestModelSelection_AdmissionInvariantToRowCount is the guard against
// this rule ever being "improved" into a significance test.
//
// At 1,000 rows a 0.17%-of-variance predictor is not significant; at
// 100,000 rows it emphatically is (its standard error has shrunk by a
// factor of ten while the effect has not moved at all). A p-value gate
// would therefore admit `weak` at the larger row count and reject it at
// the smaller one — "all predictors enter" wearing a lab coat, and
// silent, because every coefficient it admits is real, merely
// negligible. An ABSOLUTE floor on variance explained cannot do that:
// the same effect size decides the same way at every n.
//
// The effect sizes are held fixed and only n moves.
func TestModelSelection_AdmissionInvariantToRowCount(t *testing.T) {
	const (
		strongDelta = 0.25 // ~4% of y's variance
		weakDelta   = 0.05 // ~0.17% of y's variance
	)
	counts := []int{1_000, 10_000, 100_000}
	var first []string
	for _, n := range counts {
		m, _ := fitEffectCohort(t, n, strongDelta, weakDelta, 99, ProfileOptions{})
		got := predictorFields(m)
		if first == nil {
			first = got
			if len(got) != 1 || got[0] != "strong" {
				t.Fatalf("n=%d admitted %v, want exactly [strong]", n, got)
			}
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("n=%d admitted %v, n=%d admitted %v — admissions must not move with row count",
				n, got, counts[0], first)
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("n=%d admitted %v, n=%d admitted %v — admissions must not move with row count",
					n, got, counts[0], first)
			}
		}
	}
}

// TestModelSelection_ZeroSurvivingPredictorsIsAModel covers the
// no-candidate-clears-the-floor case end to end: it is a complete model,
// not an error and not a skip. The intercept is the field's own mean and
// the residual scale is its own spread, so generation reading it back
// reproduces the marginal exactly.
func TestModelSelection_ZeroSurvivingPredictorsIsAModel(t *testing.T) {
	// Both candidates negligible.
	m, prof := fitEffectCohort(t, 4000, 0.02, 0.02, 5, ProfileOptions{})
	if len(m.Predictors) != 0 {
		t.Fatalf("Predictors = %+v, want none", m.Predictors)
	}
	if len(m.References) != 0 {
		t.Errorf("References = %+v, want none", m.References)
	}
	if m.R2 != 0 {
		t.Errorf("R2 = %v, want exactly 0", m.R2)
	}
	// y is standard-normal noise plus two negligible shifts, so its own
	// spread is a shade above 1 and the intercept sits near its mean.
	if m.ResidualStd < 0.9 || m.ResidualStd > 1.2 {
		t.Errorf("ResidualStd = %v, want the field's own spread (~1)", m.ResidualStd)
	}
	if m.NObs != 4000 {
		t.Errorf("NObs = %d, want 4000 — every row supports an intercept", m.NObs)
	}
	if len(m.Residuals) != 4000 {
		t.Errorf("len(Residuals) = %d, want 4000", len(m.Residuals))
	}
	found := false
	for _, w := range prof.Warnings {
		if w == modelNoPredictorWarning("y") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want the no-predictor notice for y", prof.Warnings)
	}
}

// TestModelSelection_TopKCollapseBoundsDesignWidth is the structural
// half of the story, and the one that decides whether --fit-models does
// anything at all on a real cohort.
//
// A 200-level categorical expands to 199 design columns under a full
// dictionary expansion. The motivating cohort's `brand` has 1,906 levels
// and, beside fifteen other categoricals, took the design to 2,420
// columns — past every cap, so every numeric target was skipped and the
// flag produced nothing. Applying the top-K collapse the rest of profile
// capture already applies bounds the width by the SAME per-field knob,
// and the tail becomes one catch-all column rather than disappearing
// into the reference level.
func TestModelSelection_TopKCollapseBoundsDesignWidth(t *testing.T) {
	const levels = 200
	dict := encoding.NewDictionary()
	for i := 0; i < levels; i++ {
		if _, err := dict.Add(fmt.Sprintf("b%03d", i)); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: dict},
	}}
	// Each level shifts y by a level-specific amount large relative to
	// the noise, so `brand` unambiguously clears the floor and the test
	// is measuring the COLLAPSE rather than the selection.
	rng := mrand.New(mrand.NewPCG(4, 5))
	rows := make([]map[string]any, 0, levels*40)
	nulls := make([]map[string]bool, 0, levels*40)
	for rep := 0; rep < 40; rep++ {
		for i := 0; i < levels; i++ {
			rows = append(rows, map[string]any{
				"y":     float64(i) + rng.NormFloat64()*0.1,
				"brand": fmt.Sprintf("b%03d", i),
			})
			nulls = append(nulls, map[string]bool{})
		}
	}
	data := encodeModelRows(t, schema, rows, nulls)

	for _, topK := range []int{5, 32} {
		t.Run(fmt.Sprintf("top-k-%d", topK), func(t *testing.T) {
			fresh := &encoding.Schema{Fields: []encoding.Field{
				{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
				{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: dict},
			}}
			prof, err := profileRecords(fresh, bytes.NewReader(data),
				ProfileOptions{FitModels: true, TopK: topK})
			if err != nil {
				t.Fatalf("profileRecords: %v", err)
			}
			m, ok := modelByField(prof.FittedModels(), "y")
			if !ok {
				t.Fatalf("no model for y — a 200-level categorical must not skip the field; warnings=%v",
					prof.Warnings)
			}
			// topK retained levels plus one catch-all, minus the dropped
			// reference.
			if len(m.Predictors) != topK {
				t.Errorf("len(Predictors) = %d, want %d (top-%d + other − reference)",
					len(m.Predictors), topK, topK)
			}
			others := 0
			for _, p := range m.Predictors {
				if p.Level == otherCategoryLabel {
					others++
				}
			}
			if others != 1 {
				t.Errorf("%d catch-all columns, want exactly 1 — the collapsed tail must be a "+
					"column of its own, not folded into the reference level", others)
			}
			if len(m.References) != 1 || m.References[0].Field != "brand" {
				t.Fatalf("References = %+v, want one brand baseline", m.References)
			}
			if m.References[0].Level == otherCategoryLabel {
				t.Error("the catch-all must never be the reference: a baseline meaning " +
					"\"one of the other 168 brands\" is uninterpretable")
			}
		})
	}
}

// TestModelSelection_NoUserFacingOverride pins the "fully automatic"
// requirement to something a change can break.
//
// Selection reads exactly one caller-supplied number — ProfileOptions.TopK
// — and it reads it because the top-K collapse is a property of the
// DOCUMENT that every other section already honours, not because
// selection is tunable. No other capture flag may move which predictors
// are admitted, and there is no floor knob at all.
func TestModelSelection_NoUserFacingOverride(t *testing.T) {
	schema := effectSchema(t)
	rows, nulls := effectRows(4000, 0.25, 0.05, 21)
	data := encodeModelRows(t, schema, rows, nulls)

	variants := []struct {
		name string
		opts ProfileOptions
	}{
		{"bare", ProfileOptions{}},
		{"include-stats", ProfileOptions{IncludeStats: true}},
		{"include-correlations", ProfileOptions{IncludeCorrelations: true}},
		{"conditional", ProfileOptions{IncludeConditional: true}},
		{"fit-shape", ProfileOptions{FitShape: true}},
		{"sample-limit-larger-than-cohort", ProfileOptions{SampleLimit: 100_000}},
		{"seeded", ProfileOptions{Seed: 12345}},
		{"correlation-top-k", ProfileOptions{IncludeCorrelations: true, CorrelationTopK: 1}},
	}
	var want []string
	for _, v := range variants {
		fresh := effectSchema(t)
		opts := v.opts
		opts.FitModels = true
		prof, err := profileRecords(fresh, bytes.NewReader(data), opts)
		if err != nil {
			t.Fatalf("%s: profileRecords: %v", v.name, err)
		}
		m, ok := modelByField(prof.FittedModels(), "y")
		if !ok {
			t.Fatalf("%s: no model for y; warnings=%v", v.name, prof.Warnings)
		}
		got := predictorFields(m)
		if want == nil {
			want = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s admitted %v, bare run admitted %v — no flag but TopK may move selection",
				v.name, got, want)
		}
	}
}

// TestVarianceExplained_Matrix exercises the statistic itself, including
// every shape that must answer 0 because the question is unanswerable
// rather than answered in the negative.
func TestVarianceExplained_Matrix(t *testing.T) {
	// group builds a groupScore from an explicit sample so the expected
	// value can be reasoned about by hand.
	group := func(vals ...float64) groupScore {
		var c varCell
		for _, v := range vals {
			c.add(v)
		}
		return groupScore{n: c.n, mean: c.mean(), m2: c.m2()}
	}
	// rep repeats a value n times: a group with no internal spread.
	rep := func(v float64, n int) groupScore {
		vals := make([]float64, n)
		for i := range vals {
			vals[i] = v
		}
		return group(vals...)
	}

	cases := []struct {
		name   string
		groups []groupScore
		want   float64
		tol    float64
	}{
		{
			name:   "single group has nothing to explain",
			groups: []groupScore{rep(1, 100)},
			want:   0,
		},
		{
			name:   "empty groups are not groups",
			groups: []groupScore{rep(1, 100), {}, {}},
			want:   0,
		},
		{
			name:   "below the observation floor",
			groups: []groupScore{rep(0, 5), rep(10, 5)},
			want:   0,
		},
		{
			name:   "no variance at all",
			groups: []groupScore{rep(3, 50), rep(3, 50)},
			want:   0,
		},
		{
			name:   "perfect separation",
			groups: []groupScore{rep(0, 50), rep(10, 50)},
			want:   1,
			tol:    1e-12,
		},
		{
			// Two groups of 50 centred at ±1 carrying 50 units of
			// within-group spread each: SSB = 100, SSW = 100, so raw
			// eta-squared is exactly 0.5 and the adjustment takes one
			// degree of freedom off it.
			name: "half the variance, adjusted down by one degree of freedom",
			groups: []groupScore{
				{n: 50, mean: -1, m2: 50},
				{n: 50, mean: 1, m2: 50},
			},
			want: 1 - 0.5*(99.0/98.0),
			tol:  1e-12,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := varianceExplained(tc.groups)
			tol := tc.tol
			if tol == 0 {
				tol = 1e-12
			}
			if math.Abs(got-tc.want) > tol {
				t.Errorf("varianceExplained = %.12f, want %.12f", got, tc.want)
			}
		})
	}
}

// TestVarianceExplained_NullPredictorScoreDoesNotGrowWithGroupCount is
// the reason the statistic is ADJUSTED rather than raw.
//
// Raw eta-squared is biased up by roughly (k−1)/(N−1): a candidate with
// no real effect scores higher purely for having more groups, so a raw
// floor would admit a 33-group collapsed categorical over a 2-group one
// for no reason but its width. The adjustment removes exactly that term,
// which is what makes ONE floor comparable across candidates of
// different cardinality.
func TestVarianceExplained_NullPredictorScoreDoesNotGrowWithGroupCount(t *testing.T) {
	// A null predictor: every group is a fresh sample of the same
	// distribution, so all between-group spread is sampling noise.
	build := func(k, per int, seed uint64) []groupScore {
		rng := mrand.New(mrand.NewPCG(seed, 77))
		out := make([]groupScore, 0, k)
		for g := 0; g < k; g++ {
			var c varCell
			for i := 0; i < per; i++ {
				c.add(rng.NormFloat64())
			}
			out = append(out, groupScore{n: c.n, mean: c.mean(), m2: c.m2()})
		}
		return out
	}
	for _, k := range []int{2, 8, 33} {
		score := varianceExplained(build(k, 300, uint64(k)*31+1))
		if score >= minVarianceExplained {
			t.Errorf("a null predictor with %d groups scored %.4f, at or above the %.4f floor — "+
				"the degrees-of-freedom adjustment is not doing its job",
				k, score, minVarianceExplained)
		}
	}
}
