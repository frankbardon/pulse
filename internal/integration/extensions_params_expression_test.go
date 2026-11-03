package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Expression-capable extension params (U79 E1-S3): a "number" ParamMeta
// declaring Expression takes a JSON number or a scalar-expression
// string, resolved by the pre-resolution pass (internal/extscalar)
// before the extension's factory decodes its params — so the factory
// only ever sees a plain number — in every one of the nine extension
// categories. Predict and the runtime agree on every verdict: a bad
// expression is refused PULSE_REQUEST_PARAM_INVALID reason param_* with
// identical code, message and details; one calling a request-bound
// function (lookup()) is listed under predict's unresolved_scalars and
// resolved at run time.

// exprParams is strictParams with scale expression-capable.
var exprParams = []pulse.ParamMeta{
	{Name: "scale", JSONType: "number", Required: true, Expression: true, Description: "A required number or expression."},
	{Name: "tag", JSONType: "string", Description: "An optional string."},
}

// paramCapture records the params object every factory call received,
// per category.
type paramCapture struct {
	mu   sync.Mutex
	seen map[string][]string
}

func (c *paramCapture) add(cat string, raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string][]string{}
	}
	c.seen[cat] = append(c.seen[cat], string(raw))
}

func (c *paramCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = nil
}

// scales are the raw "scale" values category cat's factory received.
func (c *paramCapture) scales(t *testing.T, cat string) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, raw := range c.seen[cat] {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("%s factory params %s: %v", cat, raw, err)
		}
		out = append(out, string(m["scale"]))
	}
	return out
}

// captureFilterer is a keep-all filterer builder recording its params.
type captureFilterer struct{ c *paramCapture }

func (f captureFilterer) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	f.c.add("filterer", spec.Params)
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

// exprExtensions is strictExtensions declaring exprParams, every
// factory wrapped to record the params it receives, plus a lookup
// table "rate" (rate("a") = 4) for the request-bound case.
func exprExtensions(c *paramCapture) pulse.Extensions {
	ext := withParams(strictExtensions(), exprParams)
	agg := ext.Aggregators[0].Factory
	ext.Aggregators[0].Factory = func(s *types.Aggregation, sc *encoding.Schema) (extend.Aggregator, error) {
		c.add("aggregator", s.Params)
		return agg(s, sc)
	}
	attr := ext.Attributes[0].Factory
	ext.Attributes[0].Factory = func(s *types.Attribute, sc *encoding.Schema) (extend.AttributeComputer, error) {
		c.add("attribute", s.Params)
		return attr(s, sc)
	}
	ext.Filterers[0].Factory = func() extend.FiltererBuilder { return captureFilterer{c} }
	grp := ext.Groupers[0].Factory
	ext.Groupers[0].Factory = func(s *types.Group, sc *encoding.Schema) (extend.Grouper, error) {
		c.add("grouper", s.Params)
		return grp(s, sc)
	}
	win := ext.Windows[0].Factory
	ext.Windows[0].Factory = func(s *types.Window, o extend.WindowOptions) (extend.WindowComputer, error) {
		c.add("window", s.Params)
		return win(s, o)
	}
	feat := ext.Features[0].Factory
	ext.Features[0].Factory = func(s *types.Feature, sc *encoding.Schema) (extend.FeatureComputer, error) {
		c.add("feature", s.Params)
		return feat(s, sc)
	}
	tst := ext.Tests[0].RowFactory
	ext.Tests[0].RowFactory = func(s *types.Test, sc *encoding.Schema) (extend.RowTest, error) {
		c.add("test", s.Params)
		return tst(s, sc)
	}
	mat := ext.MatrixOps[0].Factory
	ext.MatrixOps[0].Factory = func(s *types.MatrixSpec, sc *encoding.Schema) (extend.MatrixOperator, error) {
		c.add("matrix", s.Params)
		return mat(s, sc)
	}
	reg := ext.Regressions[0].Factory
	ext.Regressions[0].Factory = func(s *types.RegressionSpec, sc *encoding.Schema) (extend.Regression, error) {
		c.add("regression", s.Params)
		return reg(s, sc)
	}
	return withRateTable(ext)
}

// withRateTable is ext plus the lookup table "rate" (rate("a") = 4).
func withRateTable(ext pulse.Extensions) pulse.Extensions {
	ext.LookupTables = map[string]pulse.LookupTable{"rate": {Description: "A rate per key.", Rows: map[string]float64{"a": 4}}}
	return ext
}

// withReqParams is req with the request params k = 3, label = "a".
func withReqParams(req *types.Request) *types.Request {
	req.Params = types.Params{"k": json.Number("3"), "label": "a"}
	return req
}

// TestExtensions_ParamsExpressionEveryCategory: per category, an
// expression over params and literals predicts valid with nothing
// unresolved and runs with the factory receiving the plain number; a
// lookup() expression is unresolved at predict and resolved at run
// time; every bad expression is refused with the identical code,
// message and details on both sides.
func TestExtensions_ParamsExpressionEveryCategory(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, exprExtensions(c))
	ctx := context.Background()
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			req := func(params string) *types.Request {
				return withReqParams(tc.request(cohort, json.RawMessage(params)))
			}
			resolved := []struct{ params, want string }{
				{`{"scale":"params.k * 2"}`, "6"},
				{`{"scale":"params.k / 2","tag":"x"}`, "1.5"},
				{`{"scale":"2 + 0.25"}`, "2.25"},
			}
			for _, r := range resolved {
				res, err := p.Predict(ctx, req(r.params))
				if err != nil || !res.Valid || len(res.UnresolvedScalars) != 0 {
					t.Fatalf("Predict(%s) = valid %v unresolved %v errors %v / %v", r.params, res.Valid, res.UnresolvedScalars, res.Errors, err)
				}
				c.reset()
				if _, err := p.Process(ctx, req(r.params)); err != nil {
					t.Fatalf("Process(%s): %v", r.params, err)
				}
				if got := c.scales(t, tc.category); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != r.want }) {
					t.Fatalf("%s factory saw scale %v, want only %s", tc.category, got, r.want)
				}
			}

			t.Run("request-bound unresolved at predict", func(t *testing.T) {
				params := `{"scale":"lookup(\"rate\", params.label)"}`
				res, err := p.Predict(ctx, req(params))
				if err != nil || !res.Valid {
					t.Fatalf("Predict = valid %v errors %v / %v", res.Valid, res.Errors, err)
				}
				if len(res.UnresolvedScalars) != 1 {
					t.Fatalf("unresolved = %+v, want one", res.UnresolvedScalars)
				}
				u := res.UnresolvedScalars[0]
				if u.Path != tc.slot+".params.scale" || u.Expression != `lookup("rate", params.label)` || !slices.Equal(u.Reads, []string{"lookup"}) {
					t.Fatalf("unresolved = %+v", u)
				}
				c.reset()
				if _, err := p.Process(ctx, req(params)); err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got := c.scales(t, tc.category); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "4" }) {
					t.Fatalf("%s factory saw scale %v, want only 4", tc.category, got)
				}
			})

			refusals := []struct {
				name, params, reason string
			}{
				{"does not compile", `{"scale":"params.k +"}`, "param_invalid"},
				{"unknown identifier", `{"scale":"nope * 2"}`, "param_invalid"},
				{"yields a string", `{"scale":"params.label"}`, "param_not_numeric"},
				{"yields an infinity", `{"scale":"params.k / 0"}`, "param_not_finite"},
			}
			for _, r := range refusals {
				t.Run(r.name, func(t *testing.T) {
					_, err := p.Process(ctx, req(r.params))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID {
						t.Fatalf("Process err = %v, want PULSE_REQUEST_PARAM_INVALID", err)
					}
					if ce.Details["reason"] != r.reason || ce.Details["path"] != tc.slot+".params.scale" || ce.Details["param"] != "scale" {
						t.Fatalf("runtime details = %v, want reason %s", ce.Details, r.reason)
					}
					res, err := p.Predict(ctx, req(r.params))
					if err != nil {
						t.Fatalf("Predict: %v", err)
					}
					if res.Valid || len(res.Errors) == 0 {
						t.Fatalf("predict valid, runtime refused %s", ce.Message)
					}
					got := res.Errors[0]
					if got.Code != string(ce.Code) || got.Message != ce.Message || jsonOf(got.Details) != jsonOf(ce.Details) {
						t.Fatalf("predict %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
					}
				})
			}

			t.Run("unflagged string param stays a wrong type", func(t *testing.T) {
				_, err := p.Process(ctx, req(`{"scale":2,"tag":5}`))
				var ce *perr.CodedError
				if !stderrors.As(err, &ce) || ce.Details["reason"] != "wrong_type" {
					t.Fatalf("Process err = %v, want wrong_type", err)
				}
			})
		})
	}
}

// TestExtensions_ParamsExpressionManifest: the manifest marks an
// expression-capable param expression: true on every category's
// extension entry, and never on an unflagged one.
func TestExtensions_ParamsExpressionManifest(t *testing.T) {
	t.Parallel()

	p, _ := openE2E(t, exprExtensions(&paramCapture{}))
	m := p.Manifest(context.Background())
	raw, err := json.Marshal(m.Extensions)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for cat, list := range snap {
		var ops []struct {
			Name   string `json:"name"`
			Params []struct {
				Name       string `json:"name"`
				Expression bool   `json:"expression"`
			} `json:"params"`
		}
		if json.Unmarshal(list, &ops) != nil {
			continue
		}
		for _, op := range ops {
			if !strings.Contains(op.Name, "_ACME_STRICT") {
				continue
			}
			for _, pm := range op.Params {
				if pm.Expression != (pm.Name == "scale") {
					t.Fatalf("%s %s param %s expression = %v", cat, op.Name, pm.Name, pm.Expression)
				}
			}
			seen++
		}
	}
	if seen != 9 {
		t.Fatalf("saw %d extension entries with params, want 9 (one per category)", seen)
	}
}

// TestExtensions_ParamsExpressionRegistration: Expression on a param
// whose JSONType is not "number" is refused at pulse.New,
// PULSE_EXTENSION_PARAM_INVALID reason expression_not_number.
func TestExtensions_ParamsExpressionRegistration(t *testing.T) {
	t.Parallel()

	for _, jt := range []string{"string", "boolean", "array", "object"} {
		params := []pulse.ParamMeta{{Name: "scale", JSONType: jt, Expression: true}}
		_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: onlyCategory(withParams(strictExtensions(), params), "aggregator")})
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_PARAM_INVALID || ce.Details["reason"] != "expression_not_number" {
			t.Fatalf("JSONType %s: pulse.New err = %v, want expression_not_number", jt, err)
		}
	}
}

// TestExtensions_ParamsExpressionCompose: inside a Compose batch the
// slot's expression-capable extension param is judged per slot —
// predict lists a lookup() one at requests[i].<slot path>, a bad one is
// refused located at the request — and the batch runs on Compose and
// ComposeParallel with the factory receiving the number.
func TestExtensions_ParamsExpressionCompose(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, withRateTable(onlyCategory(exprExtensions(c), "aggregator")))
	ctx := context.Background()
	agg := strictCases()[0]
	batch := func(params string) *pulse.ComposedRequest {
		lit := withReqParams(agg.request(cohort, json.RawMessage(`{"scale":1}`)))
		ex := withReqParams(agg.request(cohort, json.RawMessage(params)))
		lit.Label, ex.Label = "lit", "ex"
		return &pulse.ComposedRequest{Requests: []*pulse.Request{lit, ex}}
	}
	env, err := p.PredictCompose(ctx, batch(`{"scale":"lookup(\"rate\", params.label)"}`))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictCompose: %v %v", err, jsonOf(env.Errors))
	}
	res, ok := env.Data.(*pulse.ComposePredictResult)
	if !ok {
		t.Fatalf("predict data %T", env.Data)
	}
	if len(res.UnresolvedScalars) != 1 || res.UnresolvedScalars[0].Path != "requests[1].aggregations[0].params.scale" {
		t.Fatalf("unresolved_scalars = %+v", res.UnresolvedScalars)
	}
	for name, run := range map[string]func(*pulse.ComposedRequest) (*pulse.ComposedResponse, error){
		"Compose": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) { return p.Compose(ctx, r) },
		"ComposeParallel": func(r *pulse.ComposedRequest) (*pulse.ComposedResponse, error) {
			return p.ComposeParallel(ctx, r, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		},
	} {
		c.reset()
		if _, err := run(batch(`{"scale":"params.k * 2"}`)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := c.scales(t, "aggregator")
		if !slices.Contains(got, "6") || slices.ContainsFunc(got, func(s string) bool { return s != "6" && s != "1" }) {
			t.Fatalf("%s: factory saw scale %v, want 1 and 6 only", name, got)
		}
		_, err := run(batch(`{"scale":"params.label"}`))
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != "param_not_numeric" {
			t.Fatalf("%s bad expression: err = %v, want param_not_numeric", name, err)
		}
	}
	env, err = p.PredictCompose(ctx, batch(`{"scale":"params.label"}`))
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Details["reason"] != "param_not_numeric" ||
		env.Errors[0].Details["path"] != "aggregations[0].params.scale" {
		t.Fatalf("PredictCompose bad expression: %v %+v", err, env.Errors)
	}
}

// TestExtensions_ParamsExpressionParallelReducers: the parallel
// reducers — the per-shard archive reducer and the single-file
// parallel decode, which build their operators off the request without
// a Processor — resolve the expression too: a Mergeable extension
// aggregator merges and its factory sees only the number; a bad
// expression is refused as on the serial arm.
func TestExtensions_ParamsExpressionParallelReducers(t *testing.T) {
	t.Parallel()

	arms := []struct {
		name, cohort string
		open         func(t *testing.T, ext pulse.Extensions) *pulse.Pulse
	}{
		{"shard_archive", "archive.pulse", openMergeArchive},
		{"parallel_decode", "large.pulse", func(t *testing.T, ext pulse.Extensions) *pulse.Pulse {
			dir := parityLargeCohort(t.TempDir())(t)
			p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: ext})
			if err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
			return p
		}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			probe := &parityProbe{}
			c := &paramCapture{}
			reg := paritySumRegistration(probe, "AGG_ACME_EXPRSUM", true)
			reg.Params = exprParams
			inner := reg.Factory
			reg.Factory = func(s *types.Aggregation, sc *encoding.Schema) (extend.Aggregator, error) {
				c.add("aggregator", s.Params)
				return inner(s, sc)
			}
			p := arm.open(t, pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}})
			req := func(params string) *types.Request {
				return withReqParams(&types.Request{
					Cohort:       &types.Cohort{Filename: arm.cohort},
					Aggregations: []*types.Aggregation{{Type: "AGG_ACME_EXPRSUM", Field: "score", Label: "total", Params: json.RawMessage(params)}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				})
			}
			c.reset()
			if _, err := p.Process(context.Background(), req(`{"scale":"params.k * 2"}`)); err != nil {
				t.Fatalf("Process: %v", err)
			}
			if probe.merges.Load() == 0 {
				t.Fatal("the reducer never merged: the test no longer reaches the parallel arm")
			}
			if got := c.scales(t, "aggregator"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "6" }) {
				t.Fatalf("factory saw scale %v, want only 6", got)
			}
			_, err := p.Process(context.Background(), req(`{"scale":"params.k / 0"}`))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != "param_not_finite" {
				t.Fatalf("bad expression: err = %v, want param_not_finite", err)
			}
		})
	}
}

// TestExtensions_ParamsExpressionFacet: a facet's extension filterer
// resolves its expression-capable param over the facet's params before
// the filterer is built; a bad one is refused identically by
// PredictFacet and FacetSchema.
func TestExtensions_ParamsExpressionFacet(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, onlyCategory(exprExtensions(c), "filterer"))
	ctx := context.Background()
	req := func(params string) *pulse.FacetRequest {
		return &pulse.FacetRequest{Cohort: cohort, Fields: []string{"score"}, Params: types.Params{"k": json.Number("3"), "label": "a"},
			Filterers: []*types.Filterer{{Type: "FILTER_ACME_STRICT", Field: "score", Params: json.RawMessage(params)}}}
	}
	c.reset()
	if _, err := p.FacetSchema(ctx, req(`{"scale":"params.k * 2"}`)); err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if got := c.scales(t, "filterer"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "6" }) {
		t.Fatalf("filterer saw scale %v, want only 6", got)
	}
	if env, err := p.PredictFacet(ctx, req(`{"scale":"params.k * 2"}`)); err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictFacet: %v %+v", err, env.Errors)
	}
	_, err := p.FacetSchema(ctx, req(`{"scale":"params.label"}`))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID {
		t.Fatalf("FacetSchema bad expression: err = %v", err)
	}
	env, err := p.PredictFacet(ctx, req(`{"scale":"params.label"}`))
	if err != nil || len(env.Errors) == 0 {
		t.Fatalf("PredictFacet bad expression: %v %+v", err, env)
	}
	got := env.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message || jsonOf(got.Details) != jsonOf(ce.Details) {
		t.Fatalf("predict %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
	}
}

// TestExtensions_ParamsExpressionCrosstabAndMaterialize: the crosstab
// (a cell aggregation) and materialize (a derived attribute column)
// engine entries resolve the expression before the factory, and refuse
// a bad one with the serial arm's code and reason.
func TestExtensions_ParamsExpressionCrosstabAndMaterialize(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	p, cohort := openE2E(t, exprExtensions(c))
	ctx := context.Background()
	crosstab := func(params string) *types.Request {
		return withReqParams(&types.Request{Cohort: cohort, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: "AGG_ACME_STRICT", Field: "score", Label: "v", Params: json.RawMessage(params)},
		}})
	}
	materialize := func(params string) *types.Request {
		return withReqParams(&types.Request{Cohort: cohort,
			Attributes: []*types.Attribute{{Type: "ATTR_ACME_STRICT", Field: "score", Label: "a", Params: json.RawMessage(params)}}})
	}
	runs := []struct {
		name, category string
		run            func(params string) error
	}{
		{"crosstab", "aggregator", func(params string) error { _, err := p.Process(ctx, crosstab(params)); return err }},
		{"materialize", "attribute", func(params string) error {
			_, err := p.Materialize(ctx, materialize(params), "m.pulse", pulse.MaterializeOptions{Overwrite: true})
			return err
		}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			c.reset()
			if err := r.run(`{"scale":"params.k * 2"}`); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got := c.scales(t, r.category); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "6" }) {
				t.Fatalf("factory saw scale %v, want only 6", got)
			}
			err := r.run(`{"scale":"params.label"}`)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != "param_not_numeric" {
				t.Fatalf("bad expression: err = %v, want param_not_numeric", err)
			}
		})
	}
}

// TestExtensions_ParamsExpressionCrosstabFused: a fusable extension
// cell (Mergeable, a summable margin class) takes the fused crosstab
// arm, which resolves the expression before the factory too.
func TestExtensions_ParamsExpressionCrosstabFused(t *testing.T) {
	t.Parallel()

	c := &paramCapture{}
	reg := paritySumRegistration(&parityProbe{}, "AGG_ACME_EXPRSUM", true)
	reg.Params = exprParams
	reg.MarginReducibility = types.MarginSummable
	inner := reg.Factory
	reg.Factory = func(s *types.Aggregation, sc *encoding.Schema) (extend.Aggregator, error) {
		c.add("aggregator", s.Params)
		return inner(s, sc)
	}
	p, cohort := openE2E(t, pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}})
	req := func(params string) *types.Request {
		return withReqParams(&types.Request{Cohort: cohort, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: "AGG_ACME_EXPRSUM", Field: "score", Label: "v", Params: json.RawMessage(params)},
		}})
	}
	ctx := context.Background()
	if res, err := p.Predict(ctx, req(`{"scale":"params.k * 2"}`)); err != nil || res.CrosstabFusable == nil || !*res.CrosstabFusable {
		t.Fatalf("Predict: %+v / %v — the test no longer reaches the fused arm", res, err)
	}
	c.reset()
	if _, err := p.Process(ctx, req(`{"scale":"params.k * 2"}`)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := c.scales(t, "aggregator"); len(got) == 0 || slices.ContainsFunc(got, func(s string) bool { return s != "6" }) {
		t.Fatalf("factory saw scale %v, want only 6", got)
	}
	_, err := p.Process(ctx, req(`{"scale":"params.label"}`))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID || ce.Details["reason"] != "param_not_numeric" {
		t.Fatalf("bad expression: err = %v, want param_not_numeric", err)
	}
}

// TestExtensions_ParamsExpressionReads: the request reads walker lists
// an expression-capable extension param written as an expression — its
// params and functions, no column — at its slot, and nothing for one
// written as a number.
func TestExtensions_ParamsExpressionReads(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, exprExtensions(&paramCapture{}))
	req := withReqParams(strictCases()[0].request(cohort, json.RawMessage(`{"scale":"params.k * lookup(\"rate\", params.label)"}`)))
	doc, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	reads, err := p.RequestReads(doc, descriptor.ReadsRootRequest)
	if err != nil {
		t.Fatal(err)
	}
	var hit *descriptor.SlotReads
	for i := range reads.Slots {
		if reads.Slots[i].Path == "aggregations[0]" && reads.Slots[i].Key == "params.scale" {
			hit = &reads.Slots[i]
		}
	}
	if hit == nil || !slices.Equal(hit.Params, []string{"k", "label"}) || len(hit.Columns) != 0 || !slices.Equal(hit.Funcs, []string{"lookup"}) {
		t.Fatalf("reads = %+v, want params.scale reading k, label through lookup", reads.Slots)
	}
	req.Aggregations[0].Params = json.RawMessage(`{"scale":2}`)
	doc, _ = json.Marshal(req)
	reads, err = p.RequestReads(doc, descriptor.ReadsRootRequest)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reads.Slots {
		if s.Key == "params.scale" {
			t.Fatalf("a number param is read as %+v", s)
		}
	}
}
