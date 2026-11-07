package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// extensions_params_fold_test.go pins predict's fold of a loaded model's
// figures into an expression-capable extension param (U67 E5-S2, #708
// part 2): a coef() / response() read of the request's own loaded
// `models` entry resolves at predict to the very number the runtime
// hands the factory — nothing under unresolved_scalars — and a value or
// reader refusal is predict's with the runtime's code, message and
// details, in every category and on the Facet and Compose surfaces. A
// read of a Compose batch fit stays unresolved.

// foldModel is an inline OLS (score's coefficient 1.5, standard errors,
// no intervals): coef("own", "score", "lower") is
// PULSE_MODEL_INTERVALS_UNAVAILABLE.
const foldModel = `{"model_format_version": 1, "type": "REG_OLS", "target": "qty",
	"predictors": ["score"], "terms": ["score"],
	"coefficients": {"(intercept)": 0.5, "score": 1.5},
	"std_errors": {"score": 0.25}, "n_obs": 40}`

func foldModels() []types.ModelRef {
	return []types.ModelRef{{Name: "own", Inline: json.RawMessage(foldModel)}}
}

// foldScale is the scale param reading src.
func foldScale(src string) json.RawMessage {
	return json.RawMessage(`{"scale":` + strconv.Quote(src) + `}`)
}

// foldRefusals are reads predict folds into a refusal the runtime
// raises: a non-finite folded value, a reader refusal.
var foldRefusals = []struct{ name, src string }{
	{"not finite", `coef("own", "score") / 0`},
	{"intervals unavailable", `coef("own", "score", "low" + "er")`},
	{"term unknown", `coef("own", "z" + "")`},
}

// sameFoldRefusal fails unless predict's first error is the runtime's err:
// code, message and details.
func sameFoldRefusal(t *testing.T, errs []*descriptor.EnvelopeEntry, err error) {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("runtime accepted (%v) what predict refused: %+v", err, errs)
	}
	if len(errs) == 0 {
		t.Fatalf("predict accepted what the runtime refused: %v", ce)
	}
	got := errs[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message || jsonOf(got.Details) != jsonOf(ce.Details) {
		t.Fatalf("predict %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
	}
}

// TestExtensions_ParamsExpressionFoldsLoadedModels: per category, a
// param reading the request's loaded model predicts valid with nothing
// unresolved and the factory receives the folded number; each refused
// read is refused identically by Predict and Process.
func TestExtensions_ParamsExpressionFoldsLoadedModels(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, exprExtensions(c))
	ctx := context.Background()
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			req := func(src string) *types.Request {
				r := withReqParams(tc.request(cohort, foldScale(src)))
				r.Models = foldModels()
				return r
			}
			for _, r := range []struct{ src, want string }{
				{`coef("own", "score") * params.k`, "4.5"},
				{`coef("own", "score", "se") + coef("own", "(intercept)")`, "0.75"},
			} {
				res, err := p.Predict(ctx, req(r.src))
				if err != nil || !res.Valid || len(res.UnresolvedScalars) != 0 {
					t.Fatalf("Predict(%s) = valid %v unresolved %+v errors %v / %v", r.src, res.Valid, res.UnresolvedScalars, res.Errors, err)
				}
				c.reset()
				if _, err := p.Process(ctx, req(r.src)); err != nil {
					t.Fatalf("Process(%s): %v", r.src, err)
				}
				if got := c.scales(t, tc.category); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != r.want }) {
					t.Fatalf("%s factory saw scale %v, want only %s", tc.category, got, r.want)
				}
			}
			for _, r := range foldRefusals {
				t.Run(r.name, func(t *testing.T) {
					_, rerr := p.Process(ctx, req(r.src))
					res, err := p.Predict(ctx, req(r.src))
					if err != nil {
						t.Fatal(err)
					}
					if res.Valid {
						t.Fatalf("predict valid, runtime %v", rerr)
					}
					sameFoldRefusal(t, res.Errors, rerr)
				})
			}
		})
	}
}

// TestExtensions_ParamsExpressionFoldFacet: a facet's extension
// filterer reading the facet's loaded model folds at PredictFacet to
// the number FacetSchema hands the filterer; a refused read is refused
// identically.
func TestExtensions_ParamsExpressionFoldFacet(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, onlyCategory(exprExtensions(c), "filterer"))
	ctx := context.Background()
	req := func(src string) *pulse.FacetRequest {
		return &pulse.FacetRequest{Cohort: cohort, Fields: []string{"score"}, Params: types.Params{"k": json.Number("3")}, Models: foldModels(),
			Filterers: []*types.Filterer{{Type: "FILTER_ACME_STRICT", Field: "score", Params: foldScale(src)}}}
	}
	const src = `coef("own", "score") * params.k`
	env, err := p.PredictFacet(ctx, req(src))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictFacet: %v %s", err, jsonOf(env.Errors))
	}
	if us := env.Data.(*pulse.FacetPredictResult).UnresolvedScalars; len(us) != 0 {
		t.Fatalf("PredictFacet left the loaded-model read unresolved: %+v", us)
	}
	c.reset()
	if _, err := p.FacetSchema(ctx, req(src)); err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if got := c.scales(t, "filterer"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "4.5" }) {
		t.Fatalf("filterer saw scale %v, want only 4.5", got)
	}
	for _, r := range foldRefusals {
		t.Run(r.name, func(t *testing.T) {
			_, rerr := p.FacetSchema(ctx, req(r.src))
			env, err := p.PredictFacet(ctx, req(r.src))
			if err != nil {
				t.Fatal(err)
			}
			sameFoldRefusal(t, env.Errors, rerr)
		})
	}
}

// TestExtensions_ParamsExpressionFoldCompose: a Compose slot's param
// reading the slot's own loaded model folds (nothing unresolved, the
// factory sees the number on both orchestrators) and its refusal is
// the runtime's located at the slot; one reading a batch fit stays
// unresolved at requests[i].
func TestExtensions_ParamsExpressionFoldCompose(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, onlyCategory(exprExtensions(c), "aggregator"))
	ctx := context.Background()
	agg := strictCases()[0]
	batch := func(src string) *pulse.ComposedRequest {
		r := withReqParams(agg.request(cohort, foldScale(src)))
		r.Models = foldModels()
		return &pulse.ComposedRequest{Requests: []*pulse.Request{r},
			Fits: []types.FitSpec{{Name: "batch", Request: &types.Request{Cohort: cohort,
				Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "qty", Predictors: []string{"score"}}}}}}}
	}
	const src = `coef("own", "score") * params.k`
	env, err := p.PredictCompose(ctx, batch(src))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictCompose: %v %s", err, jsonOf(env.Errors))
	}
	if us := env.Data.(*pulse.ComposePredictResult).UnresolvedScalars; len(us) != 0 {
		t.Fatalf("PredictCompose left the loaded-model read unresolved: %+v", us)
	}
	runs := map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error){
		"Compose": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) { return p.Compose(ctx, r) },
		"ComposeParallel": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, r, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		},
	}
	for name, run := range runs {
		c.reset()
		if _, err := run(batch(src)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := c.scales(t, "aggregator"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "4.5" }) {
			t.Fatalf("%s: factory saw scale %v, want only 4.5", name, got)
		}
	}
	bad := foldRefusals[1].src
	env, err = p.PredictCompose(ctx, batch(bad))
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := p.Compose(ctx, batch(bad))
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) ||
		env.Errors[0].Details["path"] != ce.Details["path"] || env.Errors[0].Details["reason"] != ce.Details["reason"] {
		t.Fatalf("PredictCompose %+v, Compose %v", env.Errors, rerr)
	}

	const batchSrc = `coef("batch", "score") * params.k`
	env, err = p.PredictCompose(ctx, batch(batchSrc))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictCompose batch fit: %v %s", err, jsonOf(env.Errors))
	}
	us := env.Data.(*pulse.ComposePredictResult).UnresolvedScalars
	if len(us) != 1 || us[0].Path != "requests[0].aggregations[0].params.scale" || !slices.Equal(us[0].Reads, []string{"coef"}) {
		t.Fatalf("PredictCompose batch-fit unresolved = %+v", us)
	}
}

// TestExtensions_SpecValidateSeesFoldedModelReads: an extension's
// spec-only Validate hook is handed an expression param reading the
// request's (or the facet's) loaded model as the number the runtime
// folds it to — Predict and PredictFacet report the runtime's very
// refusal, byte for byte, never a deferred (unjudged) slot.
func TestExtensions_SpecValidateSeesFoldedModelReads(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, prepExtensions())
	const params = `{"echo":true,"k":"coef(\"own\", \"score\") * 2"}`
	t.Run("request", func(t *testing.T) {
		requirePrepParity(t, p, cohort, prepCase{"aggregator", func(c *types.Cohort, raw json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Models: foldModels(),
				Aggregations: []*types.Aggregation{{Type: prepAgg, Fields: []string{"score", "qty"}, Label: "a", Params: raw}}}
		}, params, `"k":3`})
	})
	t.Run("facet", func(t *testing.T) {
		req := &types.FacetRequest{Cohort: cohort, Fields: []string{"region"}, Models: foldModels(),
			Filterers: []*types.Filterer{{Type: prepFilter, Field: "score", Params: json.RawMessage(params)}}}
		_, err := p.FacetSchema(context.Background(), req)
		ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
		if !strings.Contains(ce.Message, `"k":3`) {
			t.Fatalf("runtime hook saw %q, want k folded to 3", ce.Message)
		}
		env, err := p.PredictFacet(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 1 {
			t.Fatalf("PredictFacet errors = %+v, want exactly the runtime's refusal", env.Errors)
		}
		if got := env.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
			t.Errorf("PredictFacet = %s %q %v\nFacetSchema = %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
		}
	})
}
