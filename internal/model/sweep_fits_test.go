package model

import (
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sweepSlot is one expanded sweep slot: a grouped request holding an
// aggregation, a vector battery and the regressions given.
func sweepSlot(regs ...*types.RegressionSpec) *types.Request {
	return &types.Request{
		Label:        "slot",
		Cohort:       &types.Cohort{Filename: "c.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x > 0"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "x2", Expression: "x * 2"},
			{Type: types.ATTR_MAHALANOBIS, Fields: []string{"a", "b"}},
		},
		Matrices:    []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}},
		Regressions: regs,
	}
}

func namedOLS(name string, predictors ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_OLS, Name: name, Target: "y", Predictors: predictors}
}

func bound(name, regression string) types.FitSpec {
	return types.FitSpec{Name: name, FromSweep: &types.FitFromSweep{Regression: regression}}
}

// readsByLabel is a SweepScope.Reads that reads fit in any slot whose
// label is "reader".
func readsByLabel(slot *types.Request, fit string) bool {
	return slot.Label == "reader" && fit == "best"
}

// TestCheckFitsIn_FromSweep is the table test of every from_sweep
// PULSE_FIT_INVALID reason, plus the valid binding.
func TestCheckFitsIn_FromSweep(t *testing.T) {
	t.Parallel()

	ranked := func(slots ...*types.Request) SweepScope {
		return SweepScope{Present: true, Ranked: true, Slots: slots, Reads: readsByLabel}
	}
	reader := sweepSlot(namedOLS("m", "x"))
	reader.Label = "reader"
	withReq := bound("best", "m")
	withReq.Request = olsReq(ols())
	other := sweepSlot(namedOLS("m", "x"))
	other.Regressions[0].Target = "z"
	cases := []struct {
		name    string
		fits    []types.FitSpec
		scope   SweepScope
		reason  string
		details map[string]any
	}{
		{"valid", []types.FitSpec{bound("best", "m")}, ranked(sweepSlot(namedOLS("m", "x")), sweepSlot(namedOLS("m", "x"))), "", nil},
		{"with request", []types.FitSpec{withReq}, ranked(sweepSlot(namedOLS("m", "x"))), ReasonFromSweepWithRequest, nil},
		{"no sweep", []types.FitSpec{bound("best", "m")}, SweepScope{}, ReasonFromSweepNoSweep, nil},
		{"no rank", []types.FitSpec{bound("best", "m")}, SweepScope{Present: true, Slots: []*types.Request{sweepSlot(namedOLS("m", "x"))}}, ReasonFromSweepNoRank, nil},
		{"regression unknown", []types.FitSpec{bound("best", "nope")}, ranked(sweepSlot(namedOLS("m", "x"))), ReasonFromSweepRegressionUnknown,
			map[string]any{"regression": "nope", "available": []string{"m"}}},
		{"signature varies predictors", []types.FitSpec{bound("best", "m")}, ranked(sweepSlot(namedOLS("m", "x")), sweepSlot(namedOLS("m", "x")), sweepSlot(namedOLS("m", "w"))), ReasonFromSweepSignatureVaries,
			map[string]any{"regression": "m", "slot": 2}},
		{"signature varies target", []types.FitSpec{bound("best", "m")}, ranked(sweepSlot(namedOLS("m", "x")), other), ReasonFromSweepSignatureVaries, map[string]any{"slot": 1}},
		{"signature varies absent", []types.FitSpec{bound("best", "m")}, ranked(sweepSlot(namedOLS("m", "x")), sweepSlot(namedOLS("n", "x"))), ReasonFromSweepSignatureVaries, map[string]any{"slot": 1}},
		{"cycle", []types.FitSpec{bound("best", "m")}, ranked(sweepSlot(namedOLS("m", "x")), reader), ReasonFromSweepCycle, map[string]any{"regression": "m", "slot": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckFitsIn(tc.fits, tc.scope)
			if tc.reason == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			ce := coded(t, err)
			if ce.Code != errors.PULSE_FIT_INVALID || ce.Details["reason"] != tc.reason || ce.Details["fit"] != 0 || ce.Details["name"] != "best" {
				t.Fatalf("got %s %v", ce.Code, ce.Details)
			}
			for k, v := range tc.details {
				if !reflect.DeepEqual(ce.Details[k], v) {
					t.Fatalf("details[%s] = %#v, want %#v", k, ce.Details[k], v)
				}
			}
		})
	}
}

// TestCheckFitsIn_FromSweepSaveAndCollision: a from_sweep fit's save is
// judged as a regression fit's, and its name joins the one namespace.
func TestCheckFitsIn_FromSweepSaveAndCollision(t *testing.T) {
	t.Parallel()

	scope := SweepScope{Present: true, Ranked: true, Slots: []*types.Request{sweepSlot(namedOLS("m", "x"))}}
	saved := bound("best", "m")
	saved.Save = "best.pulse-population.json"
	if ce := coded(t, CheckFitsIn([]types.FitSpec{saved}, scope)); ce.Code != errors.PULSE_MODEL_SAVE_INVALID || ce.Details["fit"] != 0 {
		t.Fatalf("save: %s %v", ce.Code, ce.Details)
	}
	saved.Save = "best" + FileSuffix
	if err := CheckFitsIn([]types.FitSpec{saved}, scope); err != nil {
		t.Fatalf("valid save refused: %v", err)
	}
	dup := []types.FitSpec{{Name: "best", Request: olsReq(ols())}, bound("best", "m")}
	if ce := coded(t, CheckFitsIn(dup, scope)); ce.Code != errors.PULSE_MODEL_NAME_COLLISION || ce.Details["fit"] != 1 {
		t.Fatalf("collision: %s %v", ce.Code, ce.Details)
	}
	// CheckFits is the sweep-free scope.
	if ce := coded(t, CheckFits([]types.FitSpec{bound("best", "m")})); ce.Details["reason"] != ReasonFromSweepNoSweep {
		t.Fatalf("CheckFits: %v", ce.Details)
	}
}

// TestSweepFitRequest: the fit request keeps the slot's rows and the
// named regression alone (save cleared), drops every part a fit refuses,
// and never mutates the slot.
func TestSweepFitRequest(t *testing.T) {
	t.Parallel()

	saved := namedOLS("m", "x")
	saved.Save = "slot" + FileSuffix
	slot := sweepSlot(namedOLS("other", "x"), saved)
	r := SweepFitRequest(slot, "m")
	if r == nil || len(r.Regressions) != 1 || r.Regressions[0].Name != "m" || r.Regressions[0].Save != "" {
		t.Fatalf("regressions: %+v", r)
	}
	if saved.Save == "" || len(slot.Regressions) != 2 || len(slot.Attributes) != 2 || slot.Groups == nil {
		t.Fatal("slot mutated")
	}
	if r.Cohort != slot.Cohort || len(r.Filterers) != 1 || len(r.Attributes) != 1 || r.Attributes[0].Type != types.ATTR_FORMULA {
		t.Fatalf("rows not kept: %+v", r)
	}
	if r.Groups != nil || r.Aggregations != nil || r.Matrices != nil || r.Label != "" {
		t.Fatalf("refused parts kept: %+v", r)
	}
	if FitKindOf(r) != types.FitKindRegression {
		t.Fatalf("kind %q", FitKindOf(r))
	}
	if CheckFits([]types.FitSpec{{Name: "f", Request: r}}) != nil {
		t.Fatal("the derived request is not a valid fit request")
	}
	if SweepFitRequest(slot, "nope") != nil {
		t.Fatal("unknown regression resolved")
	}
}

// TestResolveSweepFits_Catalog: the resolved from_sweep fit carries the
// first slot's regression signature into CatalogFromFits; a list without
// one comes back as the same slice.
func TestResolveSweepFits_Catalog(t *testing.T) {
	t.Parallel()

	scope := SweepScope{Present: true, Ranked: true, Slots: []*types.Request{sweepSlot(namedOLS("m", "x", "w")), sweepSlot(namedOLS("m", "x", "w"))}}
	plain := []types.FitSpec{{Name: "a", Request: olsReq(ols())}}
	if got := ResolveSweepFits(plain, scope); &got[0] != &plain[0] {
		t.Fatal("a sweep-free list was copied")
	}
	fits := []types.FitSpec{{Name: "a", Request: olsReq(ols())}, bound("best", "m")}
	got := ResolveSweepFits(fits, scope)
	if fits[1].Request != nil {
		t.Fatal("caller's list mutated")
	}
	if got[1].FromSweep == nil || got[1].Request == nil {
		t.Fatalf("resolved: %+v", got[1])
	}
	sig, ok := CatalogFromFits(got)["best"]
	if !ok || sig.Kind != types.FitKindRegression || sig.Type != types.REG_OLS || sig.Target != "y" || !slices.Equal(sig.Terms, []string{"x", "w"}) || sig.Source != FitSource(1) {
		t.Fatalf("signature %+v", sig)
	}
	// Unresolved, the entry binds nothing statically.
	if _, ok := CatalogFromFits(fits)["best"]; ok {
		t.Fatal("an unresolved from_sweep fit is in the catalog")
	}
}

// TestResolveSweepFits_ModelRegressionTerms: a sweep-bound model
// regression's declared term catalog reaches the catalog, and a term
// catalog that varies by slot is refused.
func TestResolveSweepFits_ModelRegressionTerms(t *testing.T) {
	t.Parallel()

	hill := func(channels string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_MMM_HILL, Name: "mmm", Target: "sales", Predictors: []string{"tv"}, Params: []byte(`{"channels":` + channels + `}`)}
	}
	scope := SweepScope{Present: true, Ranked: true, Slots: []*types.Request{sweepSlot(hill(`["tv"]`)), sweepSlot(hill(`["tv"]`))}}
	fits := []types.FitSpec{bound("mmm_best", "mmm")}
	if err := CheckFitsIn(fits, scope); err != nil {
		t.Fatal(err)
	}
	sig := CatalogFromFits(ResolveSweepFits(fits, scope))["mmm_best"]
	if !sig.CoefficientsKnown || !slices.Contains(sig.Coefficients, MMMHalfSaturationPrefix+"tv") {
		t.Fatalf("terms %+v", sig)
	}
	scope.Slots[1] = sweepSlot(hill(`["tv","radio"]`))
	if ce := coded(t, CheckFitsIn(fits, scope)); ce.Details["reason"] != ReasonFromSweepSignatureVaries {
		t.Fatalf("got %v", ce.Details)
	}
}
