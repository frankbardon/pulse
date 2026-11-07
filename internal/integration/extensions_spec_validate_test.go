package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Every extension category's opt-in, spec-only Validate hook
// (extend.SpecValidator on each registration) is judged at predict
// exactly as at runtime, where it runs before the factory (U67 E1-S2;
// regressions landed in E1-S1): a coded error keeps its code, an
// uncoded one or a panic is PULSE_EXTENSION_SPEC_REFUSED {category,
// type, slot[, name][, panic]}, byte-identical on both sides.

// The validated operators, one per category (two for tests: a tier-1
// and a tier-2 one).
const (
	svAgg      types.AggregationType = "AGG_ACME_SV"
	svAttr     types.AttributeType   = "ATTR_ACME_SV"
	svFilter   types.FiltererType    = "FILTER_ACME_SV"
	svGroup    types.GroupType       = "GROUP_ACME_SV"
	svWindow   types.WindowType      = "WIN_ACME_SV"
	svFeature  types.FeatureType     = "FEAT_ACME_SV"
	svRowTest  types.TestType        = "TEST_ACME_SV_ROW"
	svPostTest types.TestType        = "TEST_ACME_SV_POST"
	svMatrix   types.MatrixType      = "MAT_ACME_SV"
)

// svParams is the optional params.k every validated registration
// declares (optional, so the probe spec carries none).
var svParams = []pulse.ParamMeta{{Name: "k", JSONType: "number"}}

// readK reads params.k off raw (absent: ok false).
func readK(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var p struct {
		K *int `json:"k"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.K == nil {
		return 0, false
	}
	return *p.K, true
}

// svValidator refuses params.k by value, the same rule
// validateSlopeK applies to a regression: negative — an uncoded error;
// zero — a wrapped coded one; 99 — a panic; anything else (and no k)
// is accepted. Every call bumps calls.
func svValidator[S any](params func(*S) json.RawMessage, typ func(*S) string, calls *atomic.Int64) extend.SpecValidator[S] {
	return func(spec *S) error {
		calls.Add(1)
		k, ok := readK(params(spec))
		switch {
		case !ok:
			return nil
		case k < 0:
			return fmt.Errorf("params.k must be non-negative, got %d", k)
		case k == 0:
			return fmt.Errorf("wrapped: %w", perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
				typ(spec)+" params.k must not be zero", map[string]any{"operator": typ(spec), "key": "k"}))
		case k == 99:
			panic("k is 99")
		}
		return nil
	}
}

// svBuilt counts factory calls per category, so a case can prove the
// runtime never reached the factory past a refusal.
type svBuilt struct{ n, regression atomic.Int64 }

type svAggregator struct{}

func (svAggregator) Aggregate(rows extend.Rows, _ string) (float64, error) {
	return float64(rows.Len()), nil
}

type svGrouper struct{}

func (svGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		k, _ := rows.At(i).StringValue(field)
		out[k] = append(out[k], i)
	}
	return out, nil
}

type svFilterBuilder struct{ built *svBuilt }

func (b svFilterBuilder) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	b.built.n.Add(1)
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

// svExtensions registers one validated operator per category, each
// hook rejecting params.k per svValidator, plus the E1-S1 validated
// regression.
func svExtensions(built *svBuilt, calls *atomic.Int64) pulse.Extensions {
	count := func() { built.n.Add(1) }
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: svAgg, Description: "validated count", Params: svParams,
			Validate: svValidator(func(s *types.Aggregation) json.RawMessage { return s.Params }, func(s *types.Aggregation) string { return string(s.Type) }, calls),
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				count()
				return svAggregator{}, nil
			},
		}},
		Attributes: []pulse.AttributeRegistration{{
			Name: svAttr, Description: "validated double", Params: svParams, Mode: pulse.AttributeModeBuffered,
			Validate: svValidator(func(s *types.Attribute) json.RawMessage { return s.Params }, func(s *types.Attribute) string { return string(s.Type) }, calls),
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				count()
				return e2eAttr{calls: &e2eCalls{}}, nil
			},
		}},
		Filterers: []pulse.FiltererRegistration{{
			Name: svFilter, Description: "validated pass-through", Params: svParams,
			Validate: svValidator(func(s *types.Filterer) json.RawMessage { return s.Params }, func(s *types.Filterer) string { return string(s.Type) }, calls),
			Factory:  func() extend.FiltererBuilder { return svFilterBuilder{built: built} },
		}},
		Groupers: []pulse.GrouperRegistration{{
			Name: svGroup, Description: "validated category", Params: svParams,
			Validate: svValidator(func(s *types.Group) json.RawMessage { return s.Params }, func(s *types.Group) string { return string(s.Type) }, calls),
			Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
				count()
				return svGrouper{}, nil
			},
		}},
		Windows: []pulse.WindowRegistration{{
			Name: svWindow, Description: "validated ordinal", Params: svParams,
			Validate: svValidator(func(s *types.Window) json.RawMessage { return s.Params }, func(s *types.Window) string { return string(s.Type) }, calls),
			Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) {
				count()
				return e2eWindow{}, nil
			},
		}},
		Features: []pulse.FeatureRegistration{{
			Name: svFeature, Description: "validated double", Params: svParams,
			Validate: svValidator(func(s *types.Feature) json.RawMessage { return s.Params }, func(s *types.Feature) string { return string(s.Type) }, calls),
			Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) {
				count()
				return e2eFeat{&e2eCalls{}}, nil
			},
		}},
		Tests: []pulse.TestRegistration{
			{
				Name: svRowTest, Description: "validated sum", Tier: pulse.TestTierRow, Streamable: true, Params: svParams,
				Validate: svValidator(func(s *types.Test) json.RawMessage { return s.Params }, func(s *types.Test) string { return string(s.Type) }, calls),
				RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
					count()
					return e2eRowTest{calls: &e2eCalls{}, field: spec.Field, sum: new(float64)}, nil
				},
			},
			{
				Name: svPostTest, Description: "validated rows", Tier: pulse.TestTierPost, Params: svParams,
				Validate: svValidator(func(s *types.Test) json.RawMessage { return s.Params }, func(s *types.Test) string { return string(s.Type) }, calls),
				PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) {
					count()
					return e2ePostTest{&e2eCalls{}}, nil
				},
			},
		},
		MatrixOps: []pulse.MatrixOpRegistration{func() pulse.MatrixOpRegistration {
			r := validMatrixOp()
			r.Name, r.Params = svMatrix, svParams
			r.Validate = svValidator(func(s *types.MatrixSpec) json.RawMessage { return s.Params }, func(s *types.MatrixSpec) string { return string(s.Type) }, calls)
			r.Factory = func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
				count()
				return stubMatrixOp{}, nil
			}
			return r
		}()},
		Regressions: validatedRegression(validateSlopeK, &built.regression).Regressions,
	}
}

// svCategory is one category's request shape: the request carrying
// the validated slot with params k, the category, type, slot key and
// slot name its refusal details carry.
type svCategory struct {
	category, typ, slot, name string
	request                   func(cohort *types.Cohort, params json.RawMessage) *types.Request
}

func svCategories() []svCategory {
	sum := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "s"} }
	region := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "region"} }
	return []svCategory{
		{"aggregator", string(svAgg), "aggregations", "v", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: []*types.Aggregation{{Type: svAgg, Field: "score", Label: "v", Params: p}}}
		}},
		{"attribute", string(svAttr), "attributes", "dbl", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Attributes: []*types.Attribute{{Type: svAttr, Field: "score", Label: "dbl", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dbl", Label: "s"}}}
		}},
		{"filterer", string(svFilter), "filterers", "", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Filterers: []*types.Filterer{{Type: svFilter, Field: "score", Params: p}},
				Aggregations: []*types.Aggregation{sum()}}
		}},
		{"grouper", string(svGroup), "groups", "", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{{Type: svGroup, Field: "region", Params: p}},
				Aggregations: []*types.Aggregation{sum()}}
		}},
		{"window", string(svWindow), "windows", "ord", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{region()}, Aggregations: []*types.Aggregation{sum()},
				Windows: []*types.Window{{Type: svWindow, Field: "s", Label: "ord", OrderBy: []types.OrderKey{{Field: "s"}}, Params: p}}}
		}},
		{"feature", string(svFeature), "features", "fdbl", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Features: []*types.Feature{{Type: svFeature, Field: "score", Label: "fdbl", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "fdbl", Label: "s"}}}
		}},
		{"test", string(svRowTest), "tests", "sum", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: []*types.Aggregation{sum()},
				Tests: []*types.Test{{Type: svRowTest, Field: "score", Label: "sum", Params: p}}}
		}},
		{"test", string(svPostTest), "post_tests", "rows", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{region()}, Aggregations: []*types.Aggregation{sum()},
				PostTests: []*types.Test{{Type: svPostTest, Field: "s", Label: "rows", Params: p}}}
		}},
		{"matrix", string(svMatrix), "matrices", "m", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Matrices: []types.MatrixSpec{{Name: "m", Type: svMatrix, Fields: []string{"score", "qty"}, Params: p}}}
		}},
		{"regression", string(regValidatedName), "regressions", "fit", func(c *types.Cohort, p json.RawMessage) *types.Request {
			s := slopeSpec(regValidatedName)
			s.Params = p
			return regressionRequest(c, s)
		}},
	}
}

// svRefusal is the refusal svValidator raises for k (a regression's
// coded refusal is validateSlopeK's own wording).
func svRefusal(c svCategory, k int) (perr.Code, string, map[string]any) {
	site := map[string]any{"category": c.category, "type": c.typ, "slot": c.slot}
	if c.name != "" {
		site["name"] = c.name
	}
	switch k {
	case 0:
		if c.category == "regression" {
			return perr.PROCESSING_CONFIG, "regression " + c.typ + " params.k must not be zero", map[string]any{"regression": c.typ, "key": "k"}
		}
		return perr.PROCESSING_CONFIG, c.typ + " params.k must not be zero", map[string]any{"operator": c.typ, "key": "k"}
	case 99:
		site["panic"] = "k is 99"
		return perr.PULSE_EXTENSION_SPEC_REFUSED, c.category + " " + c.typ + " Validate panicked: k is 99", site
	}
	return perr.PULSE_EXTENSION_SPEC_REFUSED, fmt.Sprintf("%s %s refused its spec: params.k must be non-negative, got %d", c.category, c.typ, k), site
}

// TestExtensions_SpecValidatePredictMatchesRuntime is the nine-category
// parity gate: for every category, a Validate refusal — uncoded, coded
// (wrapped) and a panic — is predict's only error and Process's error,
// code, message and details identical, and the runtime never reaches
// the category's factory; an accepted spec runs on both sides.
func TestExtensions_SpecValidatePredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	var built svBuilt
	var calls atomic.Int64
	p, cohort := openE2E(t, svExtensions(&built, &calls))
	for _, c := range svCategories() {
		t.Run(c.slot, func(t *testing.T) {
			for _, k := range []int{-2, 0, 99} {
				req := c.request(cohort, json.RawMessage(fmt.Sprintf(`{"k":%d}`, k)))
				code, message, details := svRefusal(c, k)
				before, regBefore := built.n.Load(), built.regression.Load()
				_, err := p.Process(context.Background(), req)
				ce := requireCodedError(t, err, code)
				if ce.Message != message || !reflect.DeepEqual(ce.Details, details) {
					t.Errorf("k=%d runtime = %q %v, want %q %v", k, ce.Message, ce.Details, message, details)
				}
				if n := built.n.Load() - before + built.regression.Load() - regBefore; n != 0 {
					t.Errorf("k=%d runtime built the operator %d times past a Validate refusal, want 0", k, n)
				}
				pr, perrv := p.Predict(context.Background(), req)
				if perrv != nil {
					t.Fatalf("Predict: %v", perrv)
				}
				if len(pr.Errors) != 1 {
					t.Fatalf("k=%d predict errors = %+v, want exactly the Validate refusal", k, pr.Errors)
				}
				if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
					t.Errorf("k=%d predict = %+v, runtime %s %q %v", k, got, ce.Code, ce.Message, ce.Details)
				}
			}
			req := c.request(cohort, json.RawMessage(`{"k":4}`))
			before := calls.Load()
			processE2E(t, p, req)
			pr, err := p.Predict(context.Background(), req)
			if err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict refused an accepted spec: %v %+v", err, pr.Errors)
			}
			if calls.Load() == before && c.category != "regression" {
				t.Error("an accepted spec never reached the Validate hook")
			}
		})
	}
}

// TestExtensions_SpecValidateAbsentIsIdentity: the same registrations
// without a Validate hook run the refused specs (k=-2) through Process
// and Predict without error — the field is opt-in.
func TestExtensions_SpecValidateAbsentIsIdentity(t *testing.T) {
	t.Parallel()

	var built svBuilt
	var calls atomic.Int64
	ext := svExtensions(&built, &calls)
	for i := range ext.Aggregators {
		ext.Aggregators[i].Validate = nil
	}
	for i := range ext.Attributes {
		ext.Attributes[i].Validate = nil
	}
	for i := range ext.Filterers {
		ext.Filterers[i].Validate = nil
	}
	for i := range ext.Groupers {
		ext.Groupers[i].Validate = nil
	}
	for i := range ext.Windows {
		ext.Windows[i].Validate = nil
	}
	for i := range ext.Features {
		ext.Features[i].Validate = nil
	}
	for i := range ext.Tests {
		ext.Tests[i].Validate = nil
	}
	for i := range ext.MatrixOps {
		ext.MatrixOps[i].Validate = nil
	}
	for i := range ext.Regressions {
		ext.Regressions[i].Validate = nil
	}
	p, cohort := openE2E(t, ext)
	for _, c := range svCategories() {
		req := c.request(cohort, json.RawMessage(`{"k":-2}`))
		if _, err := p.Process(context.Background(), req); err != nil {
			t.Errorf("%s: Process without Validate: %v", c.slot, err)
		}
		pr, err := p.Predict(context.Background(), req)
		if err != nil || len(pr.Errors) != 0 {
			t.Errorf("%s: Predict without Validate: %v %+v", c.slot, err, pr.Errors)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("a nil Validate field was called %d times", calls.Load())
	}
}

// TestExtensions_SpecValidateEngineRuleFirst: a spec an engine rule
// refuses (an unknown field) is that rule's refusal at predict, the
// hook not consulted — the runtime judges its own rules before any
// factory.
func TestExtensions_SpecValidateEngineRuleFirst(t *testing.T) {
	t.Parallel()

	var built svBuilt
	var calls atomic.Int64
	p, cohort := openE2E(t, svExtensions(&built, &calls))
	req := &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "no_such_field", Label: "bad"},
		{Type: svAgg, Field: "score", Label: "v", Params: json.RawMessage(`{"k":-1}`)},
	}}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	for _, e := range pr.Errors {
		if e.Code == string(perr.PULSE_EXTENSION_SPEC_REFUSED) {
			t.Errorf("predict consulted Validate past an engine refusal: %+v", pr.Errors)
		}
	}
	if len(pr.Errors) == 0 {
		t.Fatal("predict accepted an unknown field")
	}
	if _, err := p.Process(context.Background(), req); err == nil {
		t.Fatal("Process accepted an unknown field")
	}
}

// TestExtensions_SpecValidateComposeFit: a Compose fit's regression
// Validate refusal is PredictCompose's error (located under
// details.fit) and Compose's, code and message identical.
func TestExtensions_SpecValidateComposeFit(t *testing.T) {
	t.Parallel()

	var built atomic.Int64
	p, cohort := openE2E(t, validatedRegression(validateSlopeK, &built))
	spec := slopeSpec(regValidatedName)
	spec.Params = json.RawMessage(`{"k":-5}`)
	batch := func() *types.ComposedRequest {
		return &types.ComposedRequest{
			Fits:     []types.FitSpec{{Name: "m", Request: regressionRequest(cohort, spec)}},
			Requests: []*types.Request{{Cohort: cohort, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}}},
		}
	}
	probed := built.Load()
	_, err := p.Compose(context.Background(), batch())
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
	want := "regression REG_ACME_SLOPE_VALID refused its spec: params.k must be non-negative, got -5"
	if ce.Message != want {
		t.Errorf("Compose = %q, want %q", ce.Message, want)
	}
	if n := built.Load() - probed; n != 0 {
		t.Errorf("Compose built the fit's model %d times past a Validate refusal", n)
	}
	env, err := p.PredictCompose(context.Background(), batch())
	if err != nil {
		t.Fatalf("PredictCompose: %v", err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("PredictCompose errors = %+v, want exactly the fit's Validate refusal", env.Errors)
	}
	got := env.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message || got.Details["fit"] != 0 {
		t.Errorf("PredictCompose = %+v, Compose %s %q %v", got, ce.Code, ce.Message, ce.Details)
	}
	for _, key := range []string{"category", "type", "slot", "name"} {
		if got.Details[key] != ce.Details[key] {
			t.Errorf("details.%s: predict %v, runtime %v", key, got.Details[key], ce.Details[key])
		}
	}
}

// TestExtensions_SpecValidateProbed: pulse.New runs every category's
// Validate on its probe spec under recover() — a hook that panics there
// or refuses it fails registration with PULSE_EXTENSION_FACTORY_PANIC
// (details.stage validate, the hook's verdict under validate_code /
// validate_error, details.panic on a panic) before any factory is
// probed. Windows and non-WeightAware tests, whose factories are not
// probed, have their hook probed all the same.
func TestExtensions_SpecValidateProbed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		category, name string
		set            func(ext *pulse.Extensions, fail func() error)
	}{
		{"aggregator", string(svAgg), func(e *pulse.Extensions, f func() error) {
			e.Aggregators[0].Validate = func(*types.Aggregation) error { return f() }
		}},
		{"attribute", string(svAttr), func(e *pulse.Extensions, f func() error) {
			e.Attributes[0].Validate = func(*types.Attribute) error { return f() }
		}},
		{"filterer", string(svFilter), func(e *pulse.Extensions, f func() error) {
			e.Filterers[0].Validate = func(*types.Filterer) error { return f() }
		}},
		{"grouper", string(svGroup), func(e *pulse.Extensions, f func() error) {
			e.Groupers[0].Validate = func(*types.Group) error { return f() }
		}},
		{"window", string(svWindow), func(e *pulse.Extensions, f func() error) {
			e.Windows[0].Validate = func(*types.Window) error { return f() }
		}},
		{"feature", string(svFeature), func(e *pulse.Extensions, f func() error) {
			e.Features[0].Validate = func(*types.Feature) error { return f() }
		}},
		{"test", string(svRowTest), func(e *pulse.Extensions, f func() error) {
			e.Tests[0].Validate = func(*types.Test) error { return f() }
		}},
		{"test", string(svPostTest), func(e *pulse.Extensions, f func() error) {
			e.Tests[1].Validate = func(*types.Test) error { return f() }
		}},
		{"matrix", string(svMatrix), func(e *pulse.Extensions, f func() error) {
			e.MatrixOps[0].Validate = func(*types.MatrixSpec) error { return f() }
		}},
	}
	for _, tc := range cases {
		for _, mode := range []string{"refuses", "panics"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				var built svBuilt
				var calls atomic.Int64
				ext := svExtensions(&built, &calls)
				ext.Regressions = nil
				fail := func() error { return fmt.Errorf("never") }
				msg := tc.category + " " + tc.name + " refused its spec: never"
				want := map[string]any{"category": tc.category, "name": tc.name, "stage": "validate",
					"validate_code": string(perr.PULSE_EXTENSION_SPEC_REFUSED), "validate_error": msg}
				if mode == "panics" {
					fail = func() error { panic("always") }
					msg = tc.category + " " + tc.name + " Validate panicked: always"
					want["validate_error"], want["panic"] = msg, "always"
				}
				tc.set(&ext, fail)
				_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
				ce := requireCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
				if !reflect.DeepEqual(ce.Details, want) {
					t.Errorf("details = %v, want %v", ce.Details, want)
				}
			})
		}
	}
	t.Run("accepts", func(t *testing.T) {
		t.Parallel()
		var built svBuilt
		var calls atomic.Int64
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: svExtensions(&built, &calls)}); err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		// Nine hooks (one per category, two tests; the regression's own
		// probe is E1-S1's gate) each judged at least once on the probe.
		if calls.Load() < 9 {
			t.Errorf("probe consulted Validate %d times, want >= 9", calls.Load())
		}
	})
}

// TestExtensions_SpecValidateFacet: an extension filterer's Validate
// refusal on a facet request is PredictFacet's only error and
// FacetSchema's, code, message and details identical; an accepted spec
// runs on both.
func TestExtensions_SpecValidateFacet(t *testing.T) {
	t.Parallel()

	var built svBuilt
	var calls atomic.Int64
	p, cohort := openE2E(t, svExtensions(&built, &calls))
	req := func(params string) *types.FacetRequest {
		return &types.FacetRequest{Cohort: cohort, Fields: []string{"region"},
			Filterers: []*types.Filterer{{Type: svFilter, Field: "score", Params: json.RawMessage(params)}}}
	}
	_, err := p.FacetSchema(context.Background(), req(`{"k":-3}`))
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
	want := map[string]any{"category": "filterer", "type": string(svFilter), "slot": "filterers"}
	if ce.Message != "filterer FILTER_ACME_SV refused its spec: params.k must be non-negative, got -3" || !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("FacetSchema = %q %v", ce.Message, ce.Details)
	}
	env, err := p.PredictFacet(context.Background(), req(`{"k":-3}`))
	if err != nil {
		t.Fatalf("PredictFacet: %v", err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("PredictFacet errors = %+v, want exactly the Validate refusal", env.Errors)
	}
	if got := env.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
		t.Errorf("PredictFacet = %+v, FacetSchema %s %q %v", got, ce.Code, ce.Message, ce.Details)
	}
	if _, err := p.FacetSchema(context.Background(), req(`{"k":4}`)); err != nil {
		t.Errorf("FacetSchema refused an accepted spec: %v", err)
	}
	if env, err := p.PredictFacet(context.Background(), req(`{"k":4}`)); err != nil || len(env.Errors) != 0 {
		t.Errorf("PredictFacet refused an accepted spec: %v %+v", err, env.Errors)
	}
}
