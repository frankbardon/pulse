package synth

import (
	"bytes"
	"fmt"
	mrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// E2-S5 test pack: the top-K catch-all design column on the wire.
//
// Every fixture here forces a COLLAPSE — a categorical whose level count
// exceeds ProfileOptions.TopK — and that is the whole point of the file.
// The existing model fixtures (modelFixtureSchema, effectSchema) carry
// three-level categoricals against the default TopK of 32, so
// dummyCategoricalOther is never constructed in them and no assertion
// they make can reach it. That gap is precisely why the catch-all
// serialised as `kind: "numeric"` through four stories and took the
// whole model down with it at SpecFromProfile: on the motivating
// 381,324-row cohort, 105 captured models became 20 applied ones.
//
// A test of this behaviour that does not collapse is not a test of this
// behaviour.

// collapseLevels is the number of levels the fixture categorical
// carries. It must exceed collapseTopK for the catch-all column to
// exist at all.
const collapseLevels = 8

// collapseTopK is the fixture's ProfileOptions.TopK. Three retained
// levels is the smallest set that still leaves a dropped reference AND
// two firing coefficients beside the catch-all, so the assertions can
// tell "the catch-all is present" from "everything collapsed".
const collapseTopK = 3

// collapseSchema is one numeric target against one wide categorical.
// Nothing else: a second predictor would only add ways for the design to
// change without the catch-all being involved.
func collapseSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	brand := encoding.NewDictionary()
	for i := 0; i < collapseLevels; i++ {
		if _, err := brand.Add(fmt.Sprintf("b%d", i)); err != nil {
			t.Fatalf("brand dict add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: brand},
	}}
}

// collapseRows draws `brand` with deliberately UNEQUAL frequencies so
// the top-K ranking is unambiguous — b0, b1 and b2 are the three most
// common and are therefore the retained set, while b3..b7 are the tail
// the catch-all stands for. spend is a strong, exactly-linear function
// of brand plus unit noise, so the candidate clears the variance floor
// (minVarianceExplained) with room to spare and the fit is never the
// thing under question.
func collapseRows(n int, seed uint64) ([]map[string]any, []map[string]bool) {
	rng := mrand.New(mrand.NewPCG(seed, seed*2654435761+1))
	// Weights descending across the first three levels, then a flat
	// tail: 30/25/20 vs 5 each for the remaining five.
	weights := []int{30, 25, 20, 5, 5, 5, 5, 5}
	total := 0
	for _, w := range weights {
		total += w
	}
	rows := make([]map[string]any, n)
	nulls := make([]map[string]bool, n)
	for i := 0; i < n; i++ {
		draw := rng.IntN(total)
		level := 0
		for acc := 0; level < len(weights); level++ {
			acc += weights[level]
			if draw < acc {
				break
			}
		}
		if level >= len(weights) {
			level = len(weights) - 1
		}
		rows[i] = map[string]any{
			"spend": 10 + 9*float64(level) + rng.NormFloat64(),
			"brand": fmt.Sprintf("b%d", level),
		}
		nulls[i] = map[string]bool{}
	}
	return rows, nulls
}

// captureCollapsedProfile runs the whole capture over the collapsing
// fixture and returns the finished document.
func captureCollapsedProfile(t *testing.T) *Profile {
	t.Helper()
	schema := collapseSchema(t)
	rows, nulls := collapseRows(4000, 19)
	prof, err := profileRecords(schema, bytes.NewReader(encodeModelRows(t, schema, rows, nulls)),
		ProfileOptions{TopK: collapseTopK, IncludeStats: true, FitModels: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

// TestProfileModels_CatchAllSerialisesAsACategoricalLevel is the wire
// assertion. regression_record.go's own comment on dummyCategoricalOther
// promises the catch-all "is carried on the wire as an ordinary
// categorical level named otherCategoryLabel ... and that difference
// stays internal to the Kind"; modelPredictorKind had no case for it and
// its default arm answered ModelPredictorNumeric, so the promise was
// false for every collapsing field.
func TestProfileModels_CatchAllSerialisesAsACategoricalLevel(t *testing.T) {
	prof := captureCollapsedProfile(t)
	m, ok := modelByField(prof.Models, "spend")
	if !ok {
		t.Fatalf("no model for spend; warnings=%v", prof.Warnings)
	}

	var catchAll *ModelPredictor
	for i := range m.Predictors {
		if m.Predictors[i].Field == "brand" && m.Predictors[i].Level == otherCategoryLabel {
			catchAll = &m.Predictors[i]
		}
	}
	if catchAll == nil {
		t.Fatalf("fixture produced no catch-all predictor; predictors=%+v — it cannot have tested the collapse", m.Predictors)
	}
	if catchAll.Kind != ModelPredictorCategoricalLevel {
		t.Fatalf("catch-all predictor Kind = %q, want %q", catchAll.Kind, ModelPredictorCategoricalLevel)
	}

	// Every predictor of a categorical-only model is a level, catch-all
	// included: a `numeric` kind anywhere here means some column's kind
	// fell through a default arm again.
	for _, p := range m.Predictors {
		if p.Kind != ModelPredictorCategoricalLevel {
			t.Errorf("predictor %s=%s carries kind %q, want %q",
				p.Field, p.Level, p.Kind, ModelPredictorCategoricalLevel)
		}
	}
}

// TestSpecFromProfile_CollapsedModelIsApplied is the consequence the
// wire kind exists to produce. modelSpecFromProfile refuses a model
// WHOLESALE on one unusable predictor — deliberately, since dropping a
// term would leave the survivors read against a baseline that no longer
// exists — so a mistyped catch-all silently deleted the entire fitted
// relationship for every categorical wide enough to collapse.
func TestSpecFromProfile_CollapsedModelIsApplied(t *testing.T) {
	prof := captureCollapsedProfile(t)
	spec, warnings := SpecFromProfile(prof, 500)

	if len(spec.Models) != 1 {
		t.Fatalf("spec models = %d, want 1; warnings=%v", len(spec.Models), warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "unsupported kind") {
			t.Fatalf("model dropped on predictor kind: %s", w)
		}
	}

	var level string
	for _, p := range spec.Models[0].Predictors {
		if p.Level == otherCategoryLabel {
			level = p.Level
			if p.Kind != ModelPredictorCategoricalLevel {
				t.Fatalf("applied catch-all Kind = %q, want %q", p.Kind, ModelPredictorCategoricalLevel)
			}
		}
	}
	if level == "" {
		t.Fatalf("applied model carries no catch-all term; predictors=%+v", spec.Models[0].Predictors)
	}
}

// TestModelPredictorKind_CoversEveryDummyColumnKind is the guard on the
// bug CLASS rather than the instance. The original defect was not a
// wrong mapping, it was a `default:` arm absorbing an enum member the
// switch was never taught — a shape that fails silently, produces a
// plausible document, and cannot be caught by any test of the members
// that WERE taught. Enumerating the kinds here means the next member
// added to dummyColumnKind fails this test instead of shipping.
func TestModelPredictorKind_CoversEveryDummyColumnKind(t *testing.T) {
	cases := []struct {
		kind dummyColumnKind
		want string
	}{
		{dummyNumeric, ModelPredictorNumeric},
		{dummyCategoricalLevel, ModelPredictorCategoricalLevel},
		{dummySetOption, ModelPredictorSetOption},
		{dummyCategoricalOther, ModelPredictorCategoricalLevel},
	}
	if len(cases) != int(dummyCategoricalOther)+1 {
		t.Fatalf("dummyColumnKind has %d members but this table covers %d — a new kind was added without a mapping",
			int(dummyCategoricalOther)+1, len(cases))
	}
	for _, tc := range cases {
		if got := modelPredictorKind(tc.kind); got != tc.want {
			t.Errorf("modelPredictorKind(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}
