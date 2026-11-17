package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multinomial_score_test.go scores a bound REG_MULTINOMIAL fit end to
// end (U68 E1-S2 #655): a Compose `fits` entry binds the fit (and saves
// it), one slot scores every row — P(y = category) and the log-odds
// against the reference through ONE for_each item each, and the class —
// the columns agree with each other (probabilities sum to 1, the class
// is the most probable category's 0-based index, the log-odds are
// log(π_k / π_ref)), the fit reloaded through `models` scores the same
// bytes on every DecodeWorkers / ShardWorkers count, and the scoring
// rule's refusals are identical at predict and at run. The per-row
// figures are pinned to R's fitted(multinom) / predict(type = "probs")
// by internal/processing/regression/multinomial_oracle_test.go.

// mnCategories are the fit's categories in the cohort's dictionary
// order (mnRow), the reference mid first.
var mnCategories = []string{"mid", "lo", "hi"}

// mnScoreSlot scores every row (one group per id): p_<category> and
// lo_<category> through one for_each item each, and the class.
func mnScoreSlot(t testing.TB, cohort, models string) *Request {
	t.Helper()
	cats, _ := json.Marshal(mnCategories)
	fe := `"for_each":{"axes":[{"name":"c","values":` + string(cats) + `}]}`
	m := ""
	if models != "" {
		m = `"models":[{"name":"mf","path":"` + models + `"}],`
	}
	var r Request
	if err := json.Unmarshal([]byte(`{"label":"score","cohort":{"filename":"`+cohort+`"},`+m+`
		"groups":[{"type":"GROUP_RANGE","field":"id","interval":1}],
		"attributes":[
			{"type":"ATTR_REG_PREDICT","label":"class","params":{"fit":"mf","output":"class"}},
			{`+fe+`,"type":"ATTR_REG_PREDICT","label":"p_{{c}}","params":{"fit":"mf","category":{"$var":"c"}}},
			{`+fe+`,"type":"ATTR_REG_PREDICT","label":"lo_{{c}}","params":{"fit":"mf","category":{"$var":"c"},"scale":"link"}}
		],
		"aggregations":[
			{"type":"AGG_MIN","field":"class","label":"class"},
			{`+fe+`,"type":"AGG_MIN","field":"p_{{c}}","label":"p_{{c}}"},
			{`+fe+`,"type":"AGG_MIN","field":"lo_{{c}}","label":"lo_{{c}}"}
		]}`), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func mnFitsRequest(t testing.TB, cohort, save string) *ComposedRequest {
	return &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mf", Request: mnReq(cohort, mnSpec(nil)), Save: save}},
		Requests: []*Request{mnScoreSlot(t, cohort, "")},
	}
}

func TestMultinomial_ScoresBoundSavedAndReloaded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	single, sharded := mnModes(t)
	var want string
	for _, m := range append(single, sharded...) {
		save := "models/mf-" + m.name + ".pulse-model.json"
		creq := mnFitsRequest(t, m.cohort, save)
		if env, err := m.p.PredictCompose(ctx, creq); err != nil || len(env.Errors) != 0 {
			t.Fatalf("%s: predict %v %+v", m.name, err, env)
		}
		out, err := m.p.Compose(ctx, creq)
		if err != nil {
			t.Fatalf("%s: Compose: %v", m.name, err)
		}
		fit := out.Fits["mf"]
		if fit == nil || fit.RegressionResult == nil || fit.RegressionResult.Error != nil || !slices.Equal(fit.RegressionResult.Categories, mnCategories) {
			t.Fatalf("%s: fit %+v", m.name, fit)
		}
		data := out.Responses[0].Data
		if len(data) != mnRowsN {
			t.Fatalf("%s: %d rows, want %d", m.name, len(data), mnRowsN)
		}
		sawClass := map[float64]bool{}
		for _, row := range data {
			sum, best := 0.0, 0
			pi := make([]float64, len(mnCategories))
			for k, cat := range mnCategories {
				p, ok := row["p_"+cat].(float64)
				if !ok || p <= 0 || p >= 1 {
					t.Fatalf("%s: row %v p_%s = %v", m.name, row["id"], cat, row["p_"+cat])
				}
				pi[k] = p
				sum += p
				if p > pi[best] {
					best = k
				}
			}
			if math.Abs(sum-1) > 1e-12 {
				t.Errorf("%s: row %v probabilities sum to %v", m.name, row["id"], sum)
			}
			if row["class"] != float64(best) {
				t.Errorf("%s: row %v class %v, want %d (%v)", m.name, row["id"], row["class"], best, pi)
			}
			sawClass[float64(best)] = true
			for k, cat := range mnCategories {
				lo, _ := row["lo_"+cat].(float64)
				if math.Abs(lo-math.Log(pi[k]/pi[0])) > 1e-9 || (k == 0 && lo != 0) {
					t.Errorf("%s: row %v lo_%s = %v, log(π/π_ref) %v", m.name, row["id"], cat, lo, math.Log(pi[k]/pi[0]))
				}
			}
		}
		if len(sawClass) < 2 {
			t.Errorf("%s: every row's class is %v — vacuous", m.name, sawClass)
		}
		got := mustJSON(t, m.name, data)
		if want == "" {
			want = got
		} else if got != want {
			t.Errorf("%s scores differ from %s", m.name, single[0].name)
		}
		// Saved, then reloaded through `models`: the same bytes.
		loaded, err := m.p.Process(ctx, mnScoreSlot(t, m.cohort, save))
		if err != nil {
			t.Fatalf("%s: models reload: %v", m.name, err)
		}
		if a := mustJSON(t, m.name, loaded.Data); a != got {
			t.Errorf("%s: the reloaded model scores differently", m.name)
		}
	}
}

// TestMultinomial_ScoreRefusalsPredictMatchRuntime: params.category's
// faults — none named, an unknown one on a loaded model file (whose
// categories predict reads), level / cumulative (ordinal's), terms,
// bound — and the params.fit fitted slot, refused by PredictCompose and
// by Compose with the same code, message and details. Over a `fits`
// entry the categories are the data's: predict admits a category the run
// then refuses with the rule's own outcome_unknown.
func TestMultinomial_ScoreRefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	save := "models/refusal.pulse-model.json"
	if _, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, mnSpec(nil)), Save: save}},
		Requests: []*Request{{Label: "n", Cohort: &types.Cohort{Filename: mnCohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	slot := func(typ types.AttributeType, params string, models bool) *Request {
		r := &Request{Label: "s", Cohort: &types.Cohort{Filename: mnCohort},
			Attributes:   []*types.Attribute{{Type: typ, Label: "s", Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "s", Label: "s"}}}
		if models {
			r.Models = []types.ModelRef{{Name: "mf", Path: save}}
		}
		return r
	}
	for _, tc := range []struct {
		name   string
		typ    types.AttributeType
		params string
		models bool
		reason string
	}{
		{"no category", types.ATTR_REG_PREDICT, `{"fit":"mf"}`, false, "outcome_required"},
		{"link without category", types.ATTR_REG_PREDICT, `{"fit":"mf","scale":"link"}`, false, "outcome_required"},
		{"unknown category of a loaded model", types.ATTR_REG_PREDICT, `{"fit":"mf","category":"top"}`, true, "outcome_unknown"},
		{"no category on a loaded model", types.ATTR_REG_PREDICT, `{"fit":"mf"}`, true, "outcome_required"},
		{"category beside class", types.ATTR_REG_PREDICT, `{"fit":"mf","category":"lo","output":"class"}`, false, "outcome_unused"},
		{"cumulative", types.ATTR_REG_PREDICT, `{"fit":"mf","category":"lo","output":"cumulative"}`, false, "output_unknown"},
		{"level", types.ATTR_REG_PREDICT, `{"fit":"mf","level":"lo"}`, false, "outcome_unsupported"},
		{"terms", types.ATTR_REG_PREDICT, `{"fit":"mf","category":"lo","terms":["x1"]}`, false, "terms_unsupported"},
		{"bound", types.ATTR_REG_PREDICT, `{"fit":"mf","category":"lo","bound":"upper"}`, false, "bound_unsupported"},
		{"fitted slot", types.ATTR_REG_FITTED, `{"fit":"mf"}`, false, "type_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			creq := &ComposedRequest{Requests: []*Request{slot(tc.typ, tc.params, tc.models)}}
			if !tc.models {
				creq.Fits = []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, mnSpec(nil))}}
			}
			env, err := p.PredictCompose(ctx, creq)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_MODEL_SCORE_INVALID) || env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict = %+v, want %s", env.Errors, tc.reason)
			}
			_, rerr := p.Compose(ctx, creq)
			ce := codedOf(t, rerr)
			pe := env.Errors[0]
			if string(ce.Code) != pe.Code || ce.Message != pe.Message || !jsonEqual(t, ce.Details, pe.Details) {
				t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
			}
		})
	}
	creq := &ComposedRequest{Fits: []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, mnSpec(nil))}},
		Requests: []*Request{slot(types.ATTR_REG_PREDICT, `{"fit":"mf","category":"top"}`, false)}}
	if env, err := p.PredictCompose(ctx, creq); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict over a fit's data-decided categories: %v %+v", err, env)
	}
	_, rerr := p.Compose(ctx, creq)
	ce := codedOf(t, rerr)
	if ce.Details["reason"] != "outcome_unknown" || !reflect.DeepEqual(fmt.Sprint(ce.Details["available"]), "[mid lo hi]") {
		t.Fatalf("runtime: %v %v, want outcome_unknown over [mid lo hi]", rerr, ce.Details)
	}
}

// TestMultinomial_CoefOverBoundFit: coef() reads a bound multinomial
// fit's "<category>:<term>" coefficients — the fit's own figures.
func TestMultinomial_CoefOverBoundFit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	out, err := p.Compose(ctx, &ComposedRequest{
		Fits: []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, mnSpec(nil))}},
		Requests: []*Request{{Label: "c", Cohort: &types.Cohort{Filename: mnCohort},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "b", Expression: `coef("mf", "hi:x1")`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "b", Label: "b"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := out.Fits["mf"].RegressionResult.Coefficients["hi:x1"]
	got := out.Responses[0].Data[0]["b"]
	if !strings.EqualFold(strconv.FormatFloat(want, 'g', -1, 64), fmt.Sprint(got)) {
		t.Errorf("coef(mf, hi:x1) = %v, fit %v", got, want)
	}
}
