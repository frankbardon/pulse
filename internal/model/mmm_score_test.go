package model

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// bayesFits is a Compose fits list of REG_MMM_BAYES entries: "draws"
// returns its draws, "nodraws" does not, "geo" is a geo panel that does,
// and "hill" is a REG_MMM_HILL fit.
func bayesFits() []types.FitSpec {
	fit := func(name string, typ types.RegressionType, params string) types.FitSpec {
		return types.FitSpec{Name: name, Request: &types.Request{Regressions: []*types.RegressionSpec{{
			Type: typ, Target: "sales", Predictors: []string{"tv", "price"}, Params: json.RawMessage(params)}}}}
	}
	return []types.FitSpec{
		fit("draws", types.REG_MMM_BAYES, `{"channels":["tv"],"time":"week","return_draws":true}`),
		fit("nodraws", types.REG_MMM_BAYES, `{"channels":["tv"],"time":"week"}`),
		fit("geo", types.REG_MMM_BAYES, `{"channels":["tv"],"time":"week","geo":"market","return_draws":true}`),
		fit("hill", types.REG_MMM_HILL, `{"channels":["tv"]}`),
	}
}

// TestCheckScore_MMMBayes: a REG_MMM_BAYES fit scores only with its
// draws (draws_required, on ATTR_REG_PREDICT and a params.fit slot
// alike), and it alone takes a whole-model lower / upper bound — every
// other model regression keeps bound_unsupported, a built-in the
// single-term rule; a geo panel's signature names its geo field.
func TestCheckScore_MMMBayes(t *testing.T) {
	t.Parallel()

	cat := CatalogFromFits(bayesFits())
	scorable := func(types.RegressionType) bool { return true }
	for _, tc := range []struct {
		params, reason string
	}{
		{`{"fit":"draws"}`, ""},
		{`{"fit":"draws","bound":"lower"}`, ""},
		{`{"fit":"draws","bound":"upper"}`, ""},
		{`{"fit":"draws","terms":["tv"],"bound":"lower"}`, ReasonTermsUnsupported},
		{`{"fit":"nodraws"}`, ReasonDrawsRequired},
		{`{"fit":"nodraws","bound":"upper"}`, ReasonDrawsRequired},
		{`{"fit":"hill","bound":"lower"}`, ReasonBoundUnsupported},
	} {
		_, _, err := CheckScoreFor(&types.Attribute{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(tc.params)}, cat, scorable)
		if tc.reason == "" {
			if err != nil {
				t.Errorf("%s refused: %v", tc.params, err)
			}
			continue
		}
		if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != tc.reason {
			t.Errorf("%s: %v, want reason %s", tc.params, err, tc.reason)
		}
	}
	_, _, err := CheckScoreFor(&types.Attribute{Type: types.ATTR_REG_PREDICT, Params: json.RawMessage(`{"fit":"ols","bound":"lower"}`)}, scoreCatalog(), nil)
	if err == nil || err.Details["reason"] != ReasonBoundTerms {
		t.Errorf("built-in whole-model bound: %v, want %s", err, ReasonBoundTerms)
	}
	_, _, err = CheckFitReference(&types.Attribute{Type: types.ATTR_REG_FITTED, Params: json.RawMessage(`{"fit":"nodraws"}`)}, cat, scorable)
	if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != ReasonDrawsRequired || err.Details["attribute"] != string(types.ATTR_REG_FITTED) {
		t.Errorf("params.fit over a draws-less fit: %v", err)
	}
	if sig, _ := cat.Signature("geo"); !slices.Equal(sig.ScoreFields, []string{"market"}) || sig.DrawsMissing {
		t.Errorf("geo signature fields %v, draws missing %v", sig.ScoreFields, sig.DrawsMissing)
	}
	if sig, _ := cat.Signature("hill"); sig.ScoreFields != nil || sig.DrawsMissing {
		t.Errorf("hill signature fields %v, draws missing %v", sig.ScoreFields, sig.DrawsMissing)
	}
}

// TestMMMBayesDrawsRefusal_SaveAndHoldout: a REG_MMM_BAYES holdout and
// a Compose fit's save without return_draws are PROCESSING_CONFIG
// draws_required; with it, and on any other type, they pass.
func TestMMMBayesDrawsRefusal_SaveAndHoldout(t *testing.T) {
	t.Parallel()

	fits := bayesFits()
	all := func(types.RegressionType) bool { return true }
	for _, f := range fits {
		spec := *f.Request.Regressions[0]
		spec.Holdout = "week > 50"
		err := HoldoutScorerRefusal(&spec, all)
		want := f.Name == "nodraws"
		if (err != nil) != want || (want && (err.Code != errors.PROCESSING_CONFIG || err.Details["reason"] != ReasonDrawsRequired || err.Details["slot"] != HoldoutKey)) {
			t.Errorf("%s holdout: %v, want refused %v", f.Name, err, want)
		}
	}
	for i, f := range fits {
		f.Save = "m" + FileSuffix
		err := CheckFits([]types.FitSpec{f})
		want := f.Name == "nodraws"
		var ce *errors.CodedError
		if want && (!errorsAsCoded(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["reason"] != ReasonDrawsRequired || ce.Details["fit"] != 0) {
			t.Errorf("fits[%d] %s save: %v, want draws_required", i, f.Name, err)
		}
		if !want && err != nil {
			t.Errorf("fits[%d] %s save refused: %v", i, f.Name, err)
		}
	}
}

func errorsAsCoded(err error, out **errors.CodedError) bool {
	ce, ok := err.(*errors.CodedError)
	if ok {
		*out = ce
	}
	return ok
}

// TestCatalogWithModels_MMMScoreFace: a loaded REG_MMM_BAYES file's
// signature reads its draws and geo field off its outputs exactly as
// the bound result's does.
func TestCatalogWithModels_MMMScoreFace(t *testing.T) {
	t.Parallel()

	file := func(outputs string) *File {
		var outs map[string]json.RawMessage
		_ = json.Unmarshal([]byte(outputs), &outs)
		return &File{Type: types.REG_MMM_BAYES, Terms: []string{"tv"}, Coefficients: map[string]Float{InterceptKey: 1}, Outputs: outs}
	}
	files := []*File{file(`{"draws":{}}`), file(`{"seed":1}`), file(`{"draws":{},"hierarchy":{"geo":"market"}}`)}
	cat := CatalogWithModels(nil, refs("a", "a"+FileSuffix, "b", "b"+FileSuffix, "c", "c"+FileSuffix), files)
	for name, want := range map[string]struct {
		missing bool
		fields  []string
	}{"a": {false, nil}, "b": {true, nil}, "c": {false, []string{"market"}}} {
		sig, _ := cat.Signature(name)
		if sig.DrawsMissing != want.missing || !slices.Equal(sig.ScoreFields, want.fields) {
			t.Errorf("%s: draws missing %v, fields %v; want %v, %v", name, sig.DrawsMissing, sig.ScoreFields, want.missing, want.fields)
		}
		b := &Bound{Type: types.REG_MMM_BAYES, Result: files[int(name[0]-'a')].Result()}
		if bs := b.Signature(); bs.DrawsMissing != sig.DrawsMissing || !slices.Equal(bs.ScoreFields, sig.ScoreFields) {
			t.Errorf("%s: bound face %v %v, file face %v %v", name, bs.DrawsMissing, bs.ScoreFields, sig.DrawsMissing, sig.ScoreFields)
		}
	}
}

// TestAdstock_FixedAt: fixed_at is no kernel param (Params drops it, so
// the record still reads as WIN_DISTRIBUTED_LAG params); a file's
// adstock may carry only posterior_mean, and only on a REG_MMM_BAYES
// fit.
func TestAdstock_FixedAt(t *testing.T) {
	t.Parallel()

	a := Adstock{Kernel: "geometric", Decay: f64(0.5), MaxLag: 4, FixedAt: MMMAdstockFixedAtPosteriorMean}
	if strings.Contains(string(a.Params()), "fixed_at") {
		t.Fatalf("Params %s carries fixed_at", a.Params())
	}
	if _, err := a.Weights(); err != nil {
		t.Fatal(err)
	}
	base := File{Type: types.REG_MMM_BAYES, Predictors: []string{"tv"}, Coefficients: map[string]Float{"k:tv": 1}}
	for _, tc := range []struct {
		typ     types.RegressionType
		fixedAt string
		bad     bool
	}{
		{types.REG_MMM_BAYES, MMMAdstockFixedAtPosteriorMean, false},
		{types.REG_MMM_BAYES, "", false},
		{types.REG_MMM_BAYES, "median", true},
		{types.REG_MMM_HILL, MMMAdstockFixedAtPosteriorMean, true},
	} {
		f := base
		f.Type = tc.typ
		rec := a
		rec.FixedAt = tc.fixedAt
		f.Adstock = map[string]Adstock{"tv": rec}
		if msg := adstockRule(&f); (msg != "") != tc.bad {
			t.Errorf("%s fixed_at %q: rule %q, want refused %v", tc.typ, tc.fixedAt, msg, tc.bad)
		}
	}
}

// TestGeoTally: concurrent adds count exactly, partitions fold, a reset
// clears, and the warning names the sorted geos with aligned counts.
func TestGeoTally(t *testing.T) {
	t.Parallel()

	var nilTally *GeoTally
	if nilTally.Warning("x", nil) != nil {
		t.Fatal("a nil tally warned")
	}
	a, b := NewGeoTally("market", []string{"east", "west"}), NewGeoTally("market", []string{"east", "west"})
	if a.Warning("x", nil) != nil {
		t.Fatal("an empty tally warned")
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { a.Add("north") })
	}
	wg.Wait()
	b.Add("south")
	b.Add("north")
	a.Merge(b)
	w := a.Warning("p", map[string]any{"attribute": "p"})
	if w == nil || w.Code != string(errors.PULSE_MMM_GEO_UNSEEN) {
		t.Fatalf("warning %+v", w)
	}
	if !slices.Equal(w.Details["geos"].([]string), []string{"north", "south"}) || !slices.Equal(w.Details["rows"].([]int64), []int64{51, 1}) ||
		w.Details["field"] != "market" || w.Details["attribute"] != "p" || !strings.Contains(w.Message, "52 row(s)") {
		t.Fatalf("warning %+v", w)
	}
	a.Reset()
	if a.Warning("p", nil) != nil {
		t.Fatal("a reset tally warned")
	}
}
