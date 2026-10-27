package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// explain_compose_fits_test.go pins Explain's Compose root to
// PredictCompose on every way a slot reads a name the batch binds — an
// in-batch `fits` entry (regression or vector), a slot's own `models`
// file — so Explain's verdict and refusal codes are exactly
// predict-compose's, never a standalone slot predict that sees no fit.

// refusalKeys is each refusal as "code@slot-index", sorted: the
// comparable part of a predict-compose error and an Explain refusal.
func refusalKeys(entries []descriptor.EnvelopeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		at := ""
		if v, ok := e.Details["request"]; ok {
			at = "request=" + jsonText(v)
		} else if v, ok := e.Details["fit"]; ok {
			at = "fit=" + jsonText(v)
		}
		out = append(out, e.Code+"@"+at)
	}
	slices.Sort(out)
	return out
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func envEntries(env *descriptor.Envelope) []descriptor.EnvelopeEntry {
	out := make([]descriptor.EnvelopeEntry, 0, len(env.Errors))
	for _, e := range env.Errors {
		out = append(out, *e)
	}
	return out
}

func labelled(r *Request, label string) *Request {
	r.Label = label
	return r
}

func TestExplain_ComposeFitsMatchPredictCompose(t *testing.T) {
	t.Parallel()

	regFS := fitsFacadeFS(t)
	writeCoefModel(t, regFS, facadeModel, 2)
	regP, err := New(Options{FS: regFS})
	if err != nil {
		t.Fatal(err)
	}
	vecP, err := New(Options{FS: vecFacadeFS(t)})
	if err != nil {
		t.Fatal(err)
	}

	mFit := []types.FitSpec{{Name: "m", Request: facadeFitRequest("0", "90")}}
	pop := types.FitSpec{Name: "pop", Request: vecFacadeMatrix(types.MAT_COVARIANCE, "")}
	reg := types.FitSpec{Name: "reg", Request: vecFacadeRegression()}

	coefSlot := facadeFitSlot("coef")
	coefSlot.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "scaled", Expression: `coef("m", "x") * x`}}
	modelsSlot := scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"saved"}`))
	modelsSlot.Models = []types.ModelRef{ref("saved", facadeModel)}
	mahal := vecFacadeRequest("mahal")
	mahal.Attributes = []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"fit":"pop"}`)}}
	mahal.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "s"}}
	popRefSlot := centroidScoring(types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"population":"pop"}}`)
	scoreVec := vecFacadeRequest("s")
	scoreVec.Attributes = []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "yhat", Params: json.RawMessage(`{"fit":"pop"}`)}}
	scoreVec.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "yhat", Label: "s"}}

	cases := []struct {
		name  string
		p     *Pulse
		c     *ComposedRequest
		valid bool
	}{
		{"ATTR_REG_PREDICT reads a regression fit", regP, &ComposedRequest{Fits: mFit, Requests: []*Request{scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m"}`))}}, true},
		{"two slots read one fit", regP, &ComposedRequest{Fits: mFit, Requests: []*Request{
			scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m"}`)), labelled(scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","terms":["x"]}`)), "t")}}, true},
		{"coef() reads a regression fit", regP, &ComposedRequest{Fits: mFit, Requests: []*Request{coefSlot}}, true},
		{"a slot's own models file", regP, &ComposedRequest{Requests: []*Request{modelsSlot}}, true},
		{"MAT params.fit reads a vector fit", vecP, &ComposedRequest{Fits: []types.FitSpec{pop}, Requests: []*Request{vecFacadeMatrix(types.MAT_CORRELATION, `{"fit":"pop"}`)}}, true},
		{"ATTR params.fit reads a vector fit", vecP, &ComposedRequest{Fits: []types.FitSpec{pop}, Requests: []*Request{mahal}}, true},
		{"{population} reads a vector fit", vecP, &ComposedRequest{Fits: []types.FitSpec{pop}, Requests: []*Request{popRefSlot}}, true},
		{"unbound fit name", regP, &ComposedRequest{Fits: mFit, Requests: []*Request{facadeFitSlot("ok"), scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"nope"}`))}}, false},
		{"term not a predictor", regP, &ComposedRequest{Fits: mFit, Requests: []*Request{scoringSlot(fitsFacadeCohort, scoreRef(`{"fit":"m","terms":["week"]}`))}}, false},
		{"regression score names a vector fit", vecP, &ComposedRequest{Fits: []types.FitSpec{pop, reg}, Requests: []*Request{scoreVec}}, false},
		{"fit request refused", regP, &ComposedRequest{Fits: []types.FitSpec{{Name: "m", Request: &Request{
			Cohort:      &types.Cohort{Filename: fitsFacadeCohort},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"nope"}}},
		}}}, Requests: []*Request{facadeFitSlot("s")}}, false},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := tc.p.PredictCompose(ctx, tc.c)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(env.Errors) == 0; got != tc.valid {
				t.Fatalf("predict-compose valid = %v, want %v (%+v)", got, tc.valid, envEntries(env))
			}
			res, err := tc.p.Explain(ctx, descriptor.ExplainRequest{Composed: tc.c})
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid == nil || *res.Valid != tc.valid {
				t.Fatalf("explain valid = %v, predict-compose %v; refusals %+v", res.Valid, tc.valid, res.Refusals)
			}
			want, got := refusalKeys(envEntries(env)), refusalKeys(res.Refusals)
			if !slices.Equal(got, want) {
				t.Errorf("explain refusals %v, predict-compose %v", got, want)
			}
			for i, r := range res.Refusals {
				idx, ok := r.Details["request"].(int)
				if ok && r.Details["slot"] != "requests["+strconv.Itoa(idx)+"]" {
					t.Errorf("refusal %d at request %d names slot %v", i, idx, r.Details["slot"])
				}
			}
		})
	}
}
