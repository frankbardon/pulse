package synth

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// thinFixtureSchema builds a one-target, one-categorical cohort shape
// with the caller's levels in the caller's order. Level ORDER matters:
// the reference level modelColumns drops is the first retained level in
// dictionary order, so the first name given is the baseline every other
// coefficient — and every shrunk coefficient — is read against.
func thinFixtureSchema(t *testing.T, levels []string, targets ...string) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range levels {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add %q: %v", v, err)
		}
	}
	fields := make([]encoding.Field, 0, len(targets)+1)
	for _, name := range targets {
		fields = append(fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Nullable: true})
	}
	fields = append(fields, encoding.Field{
		Name: "seg", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: dict,
	})
	return &encoding.Schema{Fields: fields}
}

// thinFixtureRows lays out `counts[i]` rows at level `levels[i]`, each
// carrying `base + effects[i]` on every target plus a deterministic
// within-level ramp so the target has residual variance to explain.
func thinFixtureRows(levels []string, counts []int, effects []float64, base float64, targets ...string) ([]map[string]any, []map[string]bool) {
	var rows []map[string]any
	var nulls []map[string]bool
	for i, level := range levels {
		for j := 0; j < counts[i]; j++ {
			row := map[string]any{"seg": level}
			// The ramp is tiny relative to the effects so it moves the
			// residual scale without moving the level means the fit is
			// asked to recover.
			ramp := float64(j%7) * 0.1
			for _, name := range targets {
				row[name] = base + effects[i] + ramp
			}
			rows = append(rows, row)
			nulls = append(nulls, map[string]bool{})
		}
	}
	return rows, nulls
}

func modelPredictorLevels(m FieldModel) map[string]float64 {
	out := make(map[string]float64, len(m.Predictors))
	for _, p := range m.Predictors {
		out[p.Level] = p.Coefficient
	}
	return out
}

// TestProfileModels_ThinLevelShrinkageScalesWithThinness is the story's
// central acceptance bar and covers three claims at once on one fit:
//
//   - a level below minLevelObservations has its coefficient pulled
//     toward zero (i.e. toward the field's reference level);
//   - the shrinkage SCALES with thinness rather than switching on at the
//     threshold — two levels with the SAME generating effect and
//     different support keep visibly different fractions of it;
//   - a well-supported level in the same design keeps essentially all of
//     its coefficient, so the penalty is not a blanket flattening.
func TestProfileModels_ThinLevelShrinkageScalesWithThinness(t *testing.T) {
	levels := []string{"base", "big", "rare40", "rare5"}
	counts := []int{3055, 900, 40, 5}
	effects := []float64{0, 10, 40, 40}
	schema := thinFixtureSchema(t, levels, "spend")
	rows, nulls := thinFixtureRows(levels, counts, effects, 100, "spend")

	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	m, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}

	// The penalty is minLevelObservations pseudo-observations spread
	// over the admitted rows; alpha·NObs recovers the pseudo-count,
	// which is the reading FieldModel.ShrinkageAlpha documents.
	if m.ShrinkageAlpha <= 0 {
		t.Fatalf("ShrinkageAlpha = %v, want > 0 — the design carries two thin levels", m.ShrinkageAlpha)
	}
	if got := m.ShrinkageAlpha * float64(m.NObs); math.Abs(got-minLevelObservations) > 1e-9 {
		t.Errorf("alpha*NObs = %.6f, want %d pseudo-observations", got, minLevelObservations)
	}

	coef := modelPredictorLevels(m)
	rare5, ok := coef["rare5"]
	if !ok {
		t.Fatalf("no coefficient for rare5; a thin level is shrunk, never dropped: %+v", m.Predictors)
	}
	rare40, ok := coef["rare40"]
	if !ok {
		t.Fatalf("no coefficient for rare40: %+v", m.Predictors)
	}
	big, ok := coef["big"]
	if !ok {
		t.Fatalf("no coefficient for big: %+v", m.Predictors)
	}

	// Both rare levels are generated with the SAME +40 effect, so the
	// only thing separating their fitted coefficients is their support.
	if rare5 >= 0.25*40 {
		t.Errorf("rare5 coefficient = %.4f, want well under %.1f — 5 observations must not buy a free coefficient",
			rare5, 0.25*40)
	}
	if rare40 <= 2*rare5 {
		t.Errorf("rare40 = %.4f vs rare5 = %.4f: shrinkage must scale with thinness, not switch on at the threshold",
			rare40, rare5)
	}
	if rare40 >= 0.75*40 {
		t.Errorf("rare40 coefficient = %.4f, want visibly shrunk (< %.1f)", rare40, 0.75*40)
	}
	if big <= 0.85*10 {
		t.Errorf("big coefficient = %.4f, want ≥ %.1f — a well-supported level keeps its coefficient",
			big, 0.85*10)
	}
	if big > 10.5 {
		t.Errorf("big coefficient = %.4f, want no more than its generating effect", big)
	}
}

// TestProfileModels_NoThinLevelLeavesTheFitUnpenalized pins the gate.
// A design where every level clears the threshold is fitted by plain
// OLS: alpha stays zero, nothing is warned, and the generating
// coefficients come back exactly. Shrinkage responds to a measured
// condition; it is not a tax on every model.
func TestProfileModels_NoThinLevelLeavesTheFitUnpenalized(t *testing.T) {
	levels := []string{"base", "mid", "high"}
	// Multiples of the ramp period, so every level's within-group ramp
	// has the same mean and the generating effects ARE the group-mean
	// contrasts — otherwise "recovered exactly" would be asserting
	// against a model the fixture does not actually generate.
	counts := []int{399, 301, 203}
	effects := []float64{0, 10, 25}
	schema := thinFixtureSchema(t, levels, "spend")
	rows, nulls := thinFixtureRows(levels, counts, effects, 100, "spend")

	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	m, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}
	if m.ShrinkageAlpha != 0 {
		t.Errorf("ShrinkageAlpha = %v, want 0 — every level clears %d observations",
			m.ShrinkageAlpha, minLevelObservations)
	}
	coef := modelPredictorLevels(m)
	for level, want := range map[string]float64{"mid": 10, "high": 25} {
		if math.Abs(coef[level]-want) > 1e-6 {
			t.Errorf("coefficient for %q = %.8f, want %v recovered exactly", level, coef[level], want)
		}
	}
	for _, w := range prof.Warnings {
		if strings.Contains(w, "thin model level") {
			t.Errorf("unexpected thin-level warning on a fully supported design: %q", w)
		}
	}
}

// TestProfileModels_ThinLevelWarningNamesFieldAndLevel checks the
// warning is actionable: it names the (field, level) key the document
// addresses the coefficient by, the support that triggered it, the
// threshold, and what happened to the coefficient. It also checks the
// posture — a thin level is warned about and SHIPPED, never refused and
// never silently dropped.
func TestProfileModels_ThinLevelWarningNamesFieldAndLevel(t *testing.T) {
	levels := []string{"base", "big", "rare5"}
	counts := []int{600, 400, 5}
	effects := []float64{0, 10, 40}
	schema := thinFixtureSchema(t, levels, "spend")
	rows, nulls := thinFixtureRows(levels, counts, effects, 100, "spend")

	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("a thin level must never fail the run: %v", err)
	}
	m, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("a thin level must never cost the field its model; warnings=%v", prof.Warnings)
	}
	if _, ok := modelPredictorLevels(m)["rare5"]; !ok {
		t.Fatalf("thin level dropped from the model instead of shrunk: %+v", m.Predictors)
	}

	var got string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, "thin model level ") {
			got = w
			break
		}
	}
	if got == "" {
		t.Fatalf("no thin model-level warning; warnings=%v", prof.Warnings)
	}
	for _, want := range []string{
		"seg=rare5",         // the (field, level) key the document uses
		"x spend",           // the model it was thin in
		"only 5 supporting", // the support that triggered it
		fmt.Sprintf("(below %d)", minLevelObservations),
		"shrunk toward its reference level",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q missing %q", got, want)
		}
	}
}

// TestProfileModels_ThinLevelWarningsBounded is the usability gate.
// Thinness is a property of the LEVEL, so the naive per-(target, level)
// emission restates one fact once per fitted model — the shape that gave
// the predecessor's fidelity report ~4,500 lines. Two bounds have to
// hold: aggregation across targets (one line per distinct level, however
// many models it was shrunk in) and the hard cap with a counted summary.
func TestProfileModels_ThinLevelWarningsBounded(t *testing.T) {
	const nLevels = 32
	levels := make([]string, nLevels)
	counts := make([]int, nLevels)
	effects := make([]float64, nLevels)
	for i := range levels {
		levels[i] = fmt.Sprintf("L%02d", i)
		counts[i] = 12
		effects[i] = float64(i) * 5
	}
	schema := thinFixtureSchema(t, levels, "spend", "revenue")
	rows, nulls := thinFixtureRows(levels, counts, effects, 100, "spend", "revenue")

	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	if len(prof.FittedModels()) != 2 {
		t.Fatalf("want a model for each of the two targets, got %d; warnings=%v",
			len(prof.FittedModels()), prof.Warnings)
	}

	var named []string
	var summary []string
	for _, w := range prof.Warnings {
		switch {
		case strings.HasPrefix(w, "thin model level "):
			named = append(named, w)
		case strings.Contains(w, "further thin model level"):
			summary = append(summary, w)
		}
	}
	if len(named) == 0 {
		t.Fatalf("fixture must produce thin levels; warnings=%v", prof.Warnings)
	}
	if len(named) > maxThinLevelWarnings {
		t.Errorf("named thin-level warnings = %d, above the %d cap", len(named), maxThinLevelWarnings)
	}
	if len(summary) != 1 {
		t.Errorf("want exactly one counted summary line for the suppressed tail, got %d: %v",
			len(summary), summary)
	}
	// Aggregation, not just truncation: no level may be named twice even
	// though it was shrunk in both models.
	seen := make(map[string]bool, len(named))
	for _, w := range named {
		key := w[:strings.Index(w, " x ")]
		if seen[key] {
			t.Errorf("level named twice across targets: %q", key)
		}
		seen[key] = true
		if !strings.Contains(w, "in 2 model(s)") {
			t.Errorf("warning %q should report the blast radius across both models", w)
		}
	}
}

// TestProfileModels_ShrinkageAbsorbsCollinearityInsteadOfRefitting pins
// an interaction that is real, invisible in the output, and easy to
// reintroduce as a bug report: a ridge-augmented Gram is
// positive-definite even for exactly-collinear columns, so a fit that
// carries a thin level never triggers the solver refusal E2-S1 uses as
// its redundancy signal. Both nested candidates survive and are jointly
// shrunk, which is what ridge is for — see
// TestProfileModels_CollinearCandidatesResolvedByRefit for the same
// fixture above the threshold, where the refit still narrows.
func TestProfileModels_ShrinkageAbsorbsCollinearityInsteadOfRefitting(t *testing.T) {
	coarse := encoding.NewDictionary()
	for _, v := range []string{"a", "b"} {
		if _, err := coarse.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	fine := encoding.NewDictionary()
	for _, v := range []string{"a1", "b1"} {
		if _, err := fine.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "coarse", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: coarse},
		{Name: "fine", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: fine},
	}}
	rows, nulls := nestedCategoricalRows(minLevelObservations - 10)
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	m, ok := modelByField(prof.FittedModels(), "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}
	if m.ShrinkageAlpha <= 0 {
		t.Fatalf("fixture must be thin enough to engage shrinkage; alpha = %v", m.ShrinkageAlpha)
	}
	if got := predictorFields(m); len(got) != 2 {
		t.Errorf("admitted predictors = %v, want both nested candidates retained under ridge", got)
	}
}

// TestPlanShrinkage_Matrix covers the decision itself, independent of
// any fit: which column kinds count toward thinness, what alpha comes
// out, and the guards that leave a fit unpenalized.
func TestPlanShrinkage_Matrix(t *testing.T) {
	col := func(kind dummyColumnKind, field, level string) dummyColumn {
		return dummyColumn{Kind: kind, Field: field, Level: level}
	}
	cases := []struct {
		name      string
		columns   []dummyColumn
		support   []int
		contrib   int
		wantAlpha float64
		wantThin  []thinLevel
	}{
		{
			name:    "every level clears the threshold",
			columns: []dummyColumn{col(dummyCategoricalLevel, "seg", "a")},
			support: []int{minLevelObservations},
			contrib: 1000,
		},
		{
			name:      "one thin categorical level",
			columns:   []dummyColumn{col(dummyCategoricalLevel, "seg", "a"), col(dummyCategoricalLevel, "seg", "b")},
			support:   []int{4, 900},
			contrib:   1000,
			wantAlpha: float64(minLevelObservations) / 1000,
			wantThin:  []thinLevel{{field: "seg", level: "a", n: 4}},
		},
		{
			name:      "the collapsed catch-all and a set option count too",
			columns:   []dummyColumn{col(dummyCategoricalOther, "seg", otherCategoryLabel), col(dummySetOption, "assets", "eco")},
			support:   []int{3, 7},
			contrib:   500,
			wantAlpha: float64(minLevelObservations) / 500,
			wantThin: []thinLevel{
				{field: "seg", level: otherCategoryLabel, n: 3},
				{field: "assets", level: "eco", n: 7},
			},
		},
		{
			name: "a rarely-non-zero numeric predictor is not a thin level",
			// hits on a scalar column counts rows where the value was
			// non-zero, which is not a support at all.
			columns: []dummyColumn{col(dummyNumeric, "score", "")},
			support: []int{2},
			contrib: 1000,
		},
		{
			name:    "an intercept-only fit has nothing to shrink",
			columns: nil,
			support: nil,
			contrib: 1000,
		},
		{
			name:    "no admitted row leaves alpha undefined rather than infinite",
			columns: []dummyColumn{col(dummyCategoricalLevel, "seg", "a")},
			support: []int{0},
			contrib: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fieldFit{target: "spend", columns: tc.columns, support: tc.support, contrib: tc.contrib}
			f.planShrinkage()
			if math.Abs(f.alpha-tc.wantAlpha) > 1e-12 {
				t.Errorf("alpha = %v, want %v", f.alpha, tc.wantAlpha)
			}
			if len(f.thin) != len(tc.wantThin) {
				t.Fatalf("thin = %+v, want %+v", f.thin, tc.wantThin)
			}
			for i := range f.thin {
				if f.thin[i] != tc.wantThin[i] {
					t.Errorf("thin[%d] = %+v, want %+v", i, f.thin[i], tc.wantThin[i])
				}
			}
		})
	}
}

// TestThinLevelWarnings_AggregateOrderAndCap covers the rendering
// contract directly: one line per distinct (field, level) however many
// models carried it, the THINNEST support reported against the model it
// occurred in, thinnest-first ordering so a truncated list keeps the
// worst offenders, and a counted summary for the tail.
func TestThinLevelWarnings_AggregateOrderAndCap(t *testing.T) {
	fits := []*fieldFit{
		{target: "spend", thin: []thinLevel{{field: "seg", level: "a", n: 20}, {field: "seg", level: "b", n: 3}}},
		{target: "revenue", thin: []thinLevel{{field: "seg", level: "a", n: 8}}},
	}
	got := thinLevelWarnings(fits)
	if len(got) != 2 {
		t.Fatalf("want one line per distinct level, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "seg=b") || !strings.Contains(got[0], "only 3 supporting") {
		t.Errorf("thinnest level must lead the list, got %q", got[0])
	}
	if !strings.Contains(got[1], "seg=a") || !strings.Contains(got[1], "only 8 supporting") {
		t.Errorf("aggregate must report the THINNEST support and its model, got %q", got[1])
	}
	if !strings.Contains(got[1], "x revenue") {
		t.Errorf("aggregate must name the model the level was thinnest in, got %q", got[1])
	}
	if !strings.Contains(got[1], "in 2 model(s)") {
		t.Errorf("aggregate must report how many models were shrunk, got %q", got[1])
	}
	if strings.Contains(got[0], "model(s)") {
		t.Errorf("a single-model level should read as a sentence, got %q", got[0])
	}

	// Overflow: one line per distinct level up to the cap, then one
	// counted summary — never a wall.
	var many []*fieldFit
	fit := &fieldFit{target: "spend"}
	for i := 0; i < maxThinLevelWarnings+7; i++ {
		fit.thin = append(fit.thin, thinLevel{field: "seg", level: fmt.Sprintf("L%02d", i), n: i + 1})
	}
	many = append(many, fit)
	got = thinLevelWarnings(many)
	if len(got) != maxThinLevelWarnings+1 {
		t.Fatalf("want %d named lines plus one summary, got %d", maxThinLevelWarnings, len(got))
	}
	if !strings.Contains(got[len(got)-1], "+7 further thin model level(s)") {
		t.Errorf("summary must count the suppressed tail, got %q", got[len(got)-1])
	}
	if strings.Contains(got[len(got)-1], "thin model level seg=") {
		t.Errorf("summary line must not masquerade as a named level: %q", got[len(got)-1])
	}
	if thinLevelWarnings(nil) != nil {
		t.Error("no thin level must produce no warning at all")
	}
}
