package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// An extension's spec-only Validate hook is handed the SAME spec at
// predict as at runtime (U67 E1-S3): the slot after the rewrites the
// engine applies before any factory — the expression-capable params
// resolved, the weight in force stamped, the vector inputs rewritten
// (members resolved, a vector aggregator's default label and
// params.labels, a test's member sets, a matrix's engine-owned params
// stripped). Every hook here echoes the whole spec it saw when
// params.echo is set, so predict and Process agree byte for byte only
// when both judged the same spec.

const (
	prepAgg      types.AggregationType = "AGG_ACME_PREP"
	prepAttr     types.AttributeType   = "ATTR_ACME_PREP"
	prepFilter   types.FiltererType    = "FILTER_ACME_PREP"
	prepGroup    types.GroupType       = "GROUP_ACME_PREP"
	prepWindow   types.WindowType      = "WIN_ACME_PREP"
	prepFeature  types.FeatureType     = "FEAT_ACME_PREP"
	prepRowTest  types.TestType        = "TEST_ACME_PREP_ROW"
	prepPostTest types.TestType        = "TEST_ACME_PREP_POST"
	prepMatrix   types.MatrixType      = "MAT_ACME_PREP"
	prepReg      types.RegressionType  = "REG_ACME_PREP"
)

// prepParams: params.echo turns the echo on; params.k is an
// expression-capable number.
var prepParams = []pulse.ParamMeta{
	{Name: "echo", JSONType: "boolean"},
	{Name: "k", JSONType: "number", Expression: true},
}

// echoValidator refuses a spec whose params set echo, the message the
// spec it saw as JSON; any other spec (the probe's) is accepted.
func echoValidator[S any](params func(*S) json.RawMessage) extend.SpecValidator[S] {
	return func(spec *S) error {
		var p struct {
			Echo bool `json:"echo"`
		}
		if raw := params(spec); len(raw) == 0 || json.Unmarshal(raw, &p) != nil || !p.Echo {
			return nil
		}
		b, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		return fmt.Errorf("saw %s", b)
	}
}

// prepAggregator counts rows (a scalar VectorInput aggregator).
type prepAggregator struct{}

func (prepAggregator) Aggregate(rows extend.Rows, _ string) (float64, error) {
	return float64(rows.Len()), nil
}

// prepExtensions registers one echoing operator per category, each
// WeightAware where the category has the declaration, the aggregator
// VectorInput.
func prepExtensions() pulse.Extensions {
	var built svBuilt
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name: prepAgg, Description: "echoing count", Params: prepParams, VectorInput: true, WeightAware: true,
			Validate: echoValidator(func(s *types.Aggregation) json.RawMessage { return s.Params }),
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return prepAggregator{}, nil
			},
		}},
		Attributes: []pulse.AttributeRegistration{{
			Name: prepAttr, Description: "echoing double", Params: prepParams, Mode: pulse.AttributeModeBuffered, WeightAware: true,
			Validate: echoValidator(func(s *types.Attribute) json.RawMessage { return s.Params }),
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return e2eAttr{calls: &e2eCalls{}}, nil
			},
		}},
		Filterers: []pulse.FiltererRegistration{{
			Name: prepFilter, Description: "echoing pass-through", Params: prepParams,
			Validate: echoValidator(func(s *types.Filterer) json.RawMessage { return s.Params }),
			Factory:  func() extend.FiltererBuilder { return svFilterBuilder{built: &built} },
		}},
		Groupers: []pulse.GrouperRegistration{{
			Name: prepGroup, Description: "echoing category", Params: prepParams,
			Validate: echoValidator(func(s *types.Group) json.RawMessage { return s.Params }),
			Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
				return svGrouper{}, nil
			},
		}},
		Windows: []pulse.WindowRegistration{{
			Name: prepWindow, Description: "echoing ordinal", Params: prepParams,
			Validate: echoValidator(func(s *types.Window) json.RawMessage { return s.Params }),
			Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) {
				return e2eWindow{}, nil
			},
		}},
		Features: []pulse.FeatureRegistration{{
			Name: prepFeature, Description: "echoing double", Params: prepParams,
			Validate: echoValidator(func(s *types.Feature) json.RawMessage { return s.Params }),
			Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) {
				return e2eFeat{&e2eCalls{}}, nil
			},
		}},
		Tests: []pulse.TestRegistration{
			{
				Name: prepRowTest, Description: "echoing sum", Tier: pulse.TestTierRow, Streamable: true, WeightAware: true, Params: prepParams,
				Validate: echoValidator(func(s *types.Test) json.RawMessage { return s.Params }),
				RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
					return e2eRowTest{calls: &e2eCalls{}, field: spec.Field, sum: new(float64)}, nil
				},
			},
			{
				Name: prepPostTest, Description: "echoing rows", Tier: pulse.TestTierPost, WeightAware: true, Params: prepParams,
				Validate: echoValidator(func(s *types.Test) json.RawMessage { return s.Params }),
				PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) {
					return e2ePostTest{&e2eCalls{}}, nil
				},
			},
		},
		MatrixOps: []pulse.MatrixOpRegistration{func() pulse.MatrixOpRegistration {
			r := validMatrixOp()
			r.Name, r.Params, r.WeightAware = prepMatrix, prepParams, true
			r.Validate = echoValidator(func(s *types.MatrixSpec) json.RawMessage { return s.Params })
			return r
		}()},
		Regressions: []pulse.RegressionRegistration{{
			Name: prepReg, Description: "echoing slope", Streamable: true, WeightAware: true, PValues: true,
			OutputSchema: slopeOutputs, Params: prepParams,
			Validate: echoValidator(func(s *types.RegressionSpec) json.RawMessage { return s.Params }),
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return &streamSlope{acc: newSlopeAcc(spec), calls: &regCalls{}}, nil
			},
		}},
	}
}

// prepCase is one category under one rewrite: the request (its
// validated slot carrying params p) and a fragment of the spec JSON
// only the rewritten slot carries.
type prepCase struct {
	name    string
	request func(c *types.Cohort, p json.RawMessage) *types.Request
	params  string
	want    string
}

// prepVectors are the request vectors every vector case reads.
func prepVectors() []types.VectorSpec {
	return []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}}
}

func prepCount() []*types.Aggregation {
	return []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
}

func prepRegion() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "region"} }

// prepVectorCases: a slot reading a vector has its members resolved;
// the engine-owned keys a test or matrix operator never sees
// (a post-test's partition_by, a matrix's missing) are stripped.
func prepVectorCases() []prepCase {
	const echo = `{"echo":true}`
	return []prepCase{
		{"aggregator", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(),
				Aggregations: []*types.Aggregation{{Type: prepAgg, Vector: "v", Params: p}}}
		}, echo, `"fields":["score","qty"]`},
		{"aggregator_default_label", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(),
				Aggregations: []*types.Aggregation{{Type: prepAgg, Vector: "v", Params: p}}}
		}, echo, `"labels":["s","q"]`},
		{"attribute", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Attributes: []*types.Attribute{{Type: prepAttr, Fields: []string{"s*", "qty"}, Params: p}},
				Aggregations: prepCount()}
		}, echo, `"fields":["score","qty"]`},
		{"grouper", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(),
				Groups: []*types.Group{{Type: prepGroup, Field: "region", Vector: "v", Params: p}}, Aggregations: prepCount()}
		}, echo, `"fields":["score","qty"]`},
		{"crosstab_axis", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(), Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: prepGroup, Field: "region", Vector: "v", Params: p}},
				Columns: []*types.Group{prepRegion()},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "score"},
			}}
		}, echo, `"fields":["score","qty"]`},
		{"row_test", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(), Aggregations: prepCount(),
				Tests: []*types.Test{{Type: prepRowTest, Vector: "v", Fields2: []string{"q*"}, Label: "t", Params: p}}}
		}, echo, `"fields2":["qty"]`},
		{"post_test_partition", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{prepRegion()}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
				PostTests: []*types.Test{{Type: prepPostTest, Field: "s", Label: "rows", Params: p}}}
		}, `{"echo":true,"partition_by":["region"]}`, `"params":{"echo":true}`},
		{"matrix", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Vectors: prepVectors(),
				Matrices: []types.MatrixSpec{{Name: "m", Type: prepMatrix, Vector: "v", Params: p}}}
		}, `{"echo":true,"missing":"listwise"}`, `"params":{"echo":true}`},
	}
}

// prepWeightCases: a WeightAware slot has the weight in force stamped.
func prepWeightCases() []prepCase {
	const echo = `{"echo":true}`
	weighted := func(r *types.Request) *types.Request {
		r.Weight = &types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency}
		return r
	}
	return []prepCase{
		{"aggregator", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return weighted(&types.Request{Cohort: c, Aggregations: []*types.Aggregation{{Type: prepAgg, Fields: []string{"score", "qty"}, Label: "a", Params: p}}})
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
		{"attribute", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return weighted(&types.Request{Cohort: c, Attributes: []*types.Attribute{{Type: prepAttr, Field: "score", Label: "dbl", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dbl", Label: "s"}}})
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
		{"row_test", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return weighted(&types.Request{Cohort: c, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
				Tests: []*types.Test{{Type: prepRowTest, Field: "score", Label: "t", Params: p}}})
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
		{"post_test", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return weighted(&types.Request{Cohort: c, Groups: []*types.Group{prepRegion()}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
				PostTests: []*types.Test{{Type: prepPostTest, Field: "s", Label: "rows", Params: p}}})
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
		{"matrix", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return weighted(&types.Request{Cohort: c, Matrices: []types.MatrixSpec{{Name: "m", Type: prepMatrix, Fields: []string{"score", "qty"}, Params: p}}})
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
		{"regression", func(c *types.Cohort, p json.RawMessage) *types.Request {
			s := slopeSpec(prepReg)
			s.Params = p
			return weighted(regressionRequest(c, s))
		}, echo, `"weight":{"field":"qty","kind":"frequency"}`},
	}
}

// prepExpressionCases: an expression-valued param arrives resolved.
func prepExpressionCases() []prepCase {
	const echo = `{"echo":true,"k":"1 + 2"}`
	const want = `"k":3`
	sum := func() []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}
	}
	return []prepCase{
		{"aggregator", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: []*types.Aggregation{{Type: prepAgg, Fields: []string{"score", "qty"}, Label: "a", Params: p}}}
		}, echo, want},
		{"crosstab_cell", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{prepRegion()}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Cell: &types.Aggregation{Type: prepAgg, Fields: []string{"score", "qty"}, Params: p},
			}}
		}, echo, want},
		{"attribute", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Attributes: []*types.Attribute{{Type: prepAttr, Field: "score", Label: "dbl", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "dbl", Label: "s"}}}
		}, echo, want},
		{"filterer", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Filterers: []*types.Filterer{{Type: prepFilter, Field: "score", Params: p}}, Aggregations: sum()}
		}, echo, want},
		{"grouper", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{{Type: prepGroup, Field: "region", Params: p}}, Aggregations: sum()}
		}, echo, want},
		{"window", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{prepRegion()}, Aggregations: sum(),
				Windows: []*types.Window{{Type: prepWindow, Field: "s", Label: "ord", OrderBy: []types.OrderKey{{Field: "s"}}, Params: p}}}
		}, echo, want},
		{"feature", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Features: []*types.Feature{{Type: prepFeature, Field: "score", Label: "fdbl", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "fdbl", Label: "s"}}}
		}, echo, want},
		{"row_test", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: sum(), Tests: []*types.Test{{Type: prepRowTest, Field: "score", Label: "t", Params: p}}}
		}, echo, want},
		{"post_test", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{prepRegion()}, Aggregations: sum(),
				PostTests: []*types.Test{{Type: prepPostTest, Field: "s", Label: "rows", Params: p}}}
		}, echo, want},
		{"matrix", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Matrices: []types.MatrixSpec{{Name: "m", Type: prepMatrix, Fields: []string{"score", "qty"}, Params: p}}}
		}, echo, want},
		{"regression", func(c *types.Cohort, p json.RawMessage) *types.Request {
			s := slopeSpec(prepReg)
			s.Params = p
			return regressionRequest(c, s)
		}, echo, want},
	}
}

// requirePrepParity runs c through Process and Predict: Process must
// refuse with the hook's echo, carrying c.want (the rewritten spec), and
// Predict must report exactly that refusal, byte for byte.
func requirePrepParity(t *testing.T, p *pulse.Pulse, cohort *types.Cohort, c prepCase) {
	t.Helper()
	req := c.request(cohort, json.RawMessage(c.params))
	_, err := p.Process(context.Background(), req)
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
	if !strings.Contains(ce.Message, c.want) {
		t.Fatalf("runtime hook saw %q, want the rewritten %s", ce.Message, c.want)
	}
	pr, perrv := p.Predict(context.Background(), req)
	if perrv != nil {
		t.Fatalf("Predict: %v", perrv)
	}
	if len(pr.Errors) != 1 {
		t.Fatalf("predict errors = %+v, want exactly the runtime's Validate refusal %q", pr.Errors, ce.Message)
	}
	if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
		t.Errorf("predict = %s %q %v\nruntime = %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
	}
}

// TestExtensions_SpecValidateSeesRewrittenVectors: a slot reading a
// vector is judged with its members resolved, a vector aggregator with
// its default label and params.labels, a tier-1 test with both member
// sets, an extension matrix operator with the engine-owned params
// stripped — at predict as at runtime.
func TestExtensions_SpecValidateSeesRewrittenVectors(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, prepExtensions())
	for _, c := range prepVectorCases() {
		t.Run(c.name, func(t *testing.T) { requirePrepParity(t, p, cohort, c) })
	}
}

// TestExtensions_SpecValidateSeesStampedWeights: a WeightAware slot is
// judged with the weight in force stamped on it, at predict as at
// runtime.
func TestExtensions_SpecValidateSeesStampedWeights(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, prepExtensions())
	for _, c := range prepWeightCases() {
		t.Run(c.name, func(t *testing.T) { requirePrepParity(t, p, cohort, c) })
	}
}

// TestExtensions_SpecValidateSeesResolvedExpressions: an
// expression-valued param is judged as the number it resolves to, at
// predict as at runtime, on every category.
func TestExtensions_SpecValidateSeesResolvedExpressions(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, prepExtensions())
	for _, c := range prepExpressionCases() {
		t.Run(c.name, func(t *testing.T) { requirePrepParity(t, p, cohort, c) })
	}
}

// TestExtensions_SpecValidateFacetSeesResolvedExpressions: a facet's
// extension filterer is judged with its expression param resolved over
// the facet's params, at PredictFacet as at FacetSchema.
func TestExtensions_SpecValidateFacetSeesResolvedExpressions(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, prepExtensions())
	req := &types.FacetRequest{Cohort: cohort, Fields: []string{"region"}, Params: map[string]any{"base": 4},
		Filterers: []*types.Filterer{{Type: prepFilter, Field: "score", Params: json.RawMessage(`{"echo":true,"k":"params.base * 2"}`)}}}
	_, err := p.FacetSchema(context.Background(), req)
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_SPEC_REFUSED)
	if !strings.Contains(ce.Message, `"k":8`) {
		t.Fatalf("runtime hook saw %q, want k resolved to 8", ce.Message)
	}
	env, err := p.PredictFacet(context.Background(), req)
	if err != nil {
		t.Fatalf("PredictFacet: %v", err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("PredictFacet errors = %+v, want exactly the runtime's refusal", env.Errors)
	}
	if got := env.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
		t.Errorf("PredictFacet = %s %q %v\nFacetSchema = %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
	}
}
