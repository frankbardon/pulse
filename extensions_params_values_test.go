package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Strict extension params (U53 E1-S3): an extension operator that
// declares its params has each declared value checked against its
// ParamMeta — the JSON type against JSONType, presence against
// Required — before any operator code runs, in every one of the nine
// extension categories. A wrong-typed value and a missing required
// param are each refused PULSE_REQUEST_PARAM_INVALID with the same
// details.path at predict and at runtime; a conforming params object
// runs.

// strictParams is every category's declaration: scale is a required
// number, tag an optional string.
var strictParams = []pulse.ParamMeta{
	{Name: "scale", JSONType: "number", Required: true, Description: "A required number."},
	{Name: "tag", JSONType: "string", Description: "An optional string."},
}

// strictExtensions registers one params-declaring operator per
// extension category, each built from a factory the e2e and stub suites
// already prove runs.
func strictExtensions() pulse.Extensions {
	calls := &e2eCalls{}
	reg := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		return &bufferedSlope{acc: newSlopeAcc(spec), calls: &regCalls{}}, nil
	}
	mat := validMatrixOp()
	mat.Name = "MAT_ACME_STRICT"
	mat.Params = strictParams
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{Name: "AGG_ACME_STRICT", Description: "strict params",
			Factory: stubAggregatorFactory, Streamable: true, Accepts: []encoding.FieldType{encoding.FieldTypeF64}, Params: strictParams}},
		Attributes: []pulse.AttributeRegistration{{Name: "ATTR_ACME_STRICT", Description: "strict params", Mode: pulse.AttributeModeBuffered,
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return e2eAttr{calls: calls}, nil
			},
			Params: strictParams}},
		Filterers: []pulse.FiltererRegistration{{Name: "FILTER_ACME_STRICT", Description: "strict params",
			Factory: func() extend.FiltererBuilder { return strictKeepAll{} }, Params: strictParams}},
		Groupers: []pulse.GrouperRegistration{{Name: "GROUP_ACME_STRICT", Description: "strict params",
			Factory: stubGrouperFactory, Params: strictParams}},
		Windows: []pulse.WindowRegistration{{Name: "WIN_ACME_STRICT", Description: "strict params",
			Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) { return e2eWindow{}, nil },
			Params:  strictParams}},
		Features: []pulse.FeatureRegistration{{Name: "FEAT_ACME_STRICT", Description: "strict params",
			Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) { return e2eFeat{calls}, nil },
			Params:  strictParams}},
		Tests: []pulse.TestRegistration{{Name: "TEST_ACME_STRICT", Description: "strict params", Tier: pulse.TestTierRow, Streamable: true,
			RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
				return e2eRowTest{calls: calls, field: spec.Field, sum: new(float64)}, nil
			},
			Params: strictParams}},
		MatrixOps: []pulse.MatrixOpRegistration{mat},
		Regressions: []pulse.RegressionRegistration{{Name: "REG_ACME_STRICT", Description: "strict params",
			Factory: reg, PValues: true, OutputSchema: slopeOutputs, Params: strictParams}},
	}
}

// strictKeepAll is a filterer that keeps every record.
type strictKeepAll struct{}

func (strictKeepAll) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

// strictCase builds one category's request carrying params on its
// extension slot at slot.
type strictCase struct {
	category string
	slot     string
	request  func(cohort *types.Cohort, params json.RawMessage) *types.Request
}

func strictCases() []strictCase {
	count := func() []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
	}
	return []strictCase{
		{"aggregator", "aggregations[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: []*types.Aggregation{{Type: "AGG_ACME_STRICT", Field: "score", Label: "v", Params: p}}}
		}},
		{"attribute", "attributes[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Attributes: []*types.Attribute{{Type: "ATTR_ACME_STRICT", Field: "score", Label: "a", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}
		}},
		{"filterer", "filterers[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Filterers: []*types.Filterer{{Type: "FILTER_ACME_STRICT", Field: "score", Params: p}}, Aggregations: count()}
		}},
		{"grouper", "groups[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{{Type: "GROUP_ACME_STRICT", Field: "region", Params: p}}, Aggregations: count()}
		}},
		{"window", "windows[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
				Windows:      []*types.Window{{Type: "WIN_ACME_STRICT", Field: "s", Label: "w", OrderBy: []types.OrderKey{{Field: "s"}}, Params: p}}}
		}},
		{"feature", "features[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Features: []*types.Feature{{Type: "FEAT_ACME_STRICT", Field: "score", Label: "f", Params: p}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}
		}},
		{"test", "tests[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Aggregations: count(), Tests: []*types.Test{{Type: "TEST_ACME_STRICT", Field: "score", Label: "t", Params: p}}}
		}},
		{"matrix", "matrices[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			return &types.Request{Cohort: c, Matrices: []types.MatrixSpec{{Type: "MAT_ACME_STRICT", Name: "m", Fields: []string{"score", "qty"}, Params: p}}}
		}},
		{"regression", "regressions[0]", func(c *types.Cohort, p json.RawMessage) *types.Request {
			s := slopeSpec("REG_ACME_STRICT")
			s.Params = p
			return regressionRequest(c, s)
		}},
	}
}

// TestExtensions_ParamsTypeAndRequiredEveryCategory: per category, a
// wrong-typed value and a missing required param (in a params object,
// and with no params object at all) are refused at predict AND at
// runtime with the identical code, message, details.path and reason;
// the conforming object runs on both sides.
func TestExtensions_ParamsTypeAndRequiredEveryCategory(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, strictExtensions())
	ctx := context.Background()
	refusals := []struct {
		name, params, key, reason string
	}{
		{"wrong type", `{"scale":2,"tag":5}`, "tag", "wrong_type"},
		{"string for number", `{"scale":"2"}`, "scale", "wrong_type"},
		{"null value", `{"scale":null}`, "scale", "wrong_type"},
		{"missing required", `{"tag":"x"}`, "scale", "missing_required"},
		{"empty object", `{}`, "scale", "missing_required"},
		{"no params", ``, "scale", "missing_required"},
	}
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			for _, r := range refusals {
				t.Run(r.name, func(t *testing.T) {
					var raw json.RawMessage
					if r.params != "" {
						raw = json.RawMessage(r.params)
					}
					path := tc.slot + ".params." + r.key
					_, err := p.Process(ctx, tc.request(cohort, raw))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_REQUEST_PARAM_INVALID {
						t.Fatalf("Process err = %v, want PULSE_REQUEST_PARAM_INVALID", err)
					}
					if ce.Details["path"] != path || ce.Details["reason"] != r.reason || ce.Details["param"] != r.key {
						t.Fatalf("runtime details = %v, want path %s reason %s", ce.Details, path, r.reason)
					}
					res, perr2 := p.Predict(ctx, tc.request(cohort, raw))
					if perr2 != nil {
						t.Fatalf("Predict: %v", perr2)
					}
					if res.Valid || len(res.Errors) == 0 {
						t.Fatalf("predict valid, runtime refused %s", ce.Message)
					}
					got := res.Errors[0]
					if got.Code != string(ce.Code) || got.Message != ce.Message || got.Details["path"] != path || got.Details["reason"] != r.reason {
						t.Fatalf("predict %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
					}
				})
			}
			t.Run("conforming params run", func(t *testing.T) {
				for _, params := range []string{`{"scale":2}`, `{"scale":2.5,"tag":"x"}`} {
					req := tc.request(cohort, json.RawMessage(params))
					if res, err := p.Predict(ctx, req); err != nil || !res.Valid {
						t.Fatalf("Predict(%s) = %+v / %v", params, res, err)
					}
					if _, err := p.Process(ctx, tc.request(cohort, json.RawMessage(params))); err != nil {
						t.Fatalf("Process(%s): %v", params, err)
					}
				}
			})
		})
	}
}

// TestExtensions_ParamsValueCheckLeavesUndeclaredAndBuiltins: the value
// check adds no refusal where none applied before — an operator that
// declares no params still passes any params through, a built-in keeps
// its own decode path, and an unknown key is still
// PULSE_REQUEST_UNKNOWN_FIELD (it wins over a type fault elsewhere in
// the same object).
func TestExtensions_ParamsValueCheckLeavesUndeclaredAndBuiltins(t *testing.T) {
	t.Parallel()

	ext := strictExtensions()
	ext.Aggregators = append(ext.Aggregators, pulse.AggregatorRegistration{Name: "AGG_ACME_LOOSE", Description: "declares nothing",
		Factory: stubAggregatorFactory, Streamable: true, Accepts: []encoding.FieldType{encoding.FieldTypeF64}})
	p, cohort := openE2E(t, ext)
	ctx := context.Background()
	agg := func(op types.AggregationType, params string) *types.Request {
		a := &types.Aggregation{Type: op, Field: "score", Label: "v"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{a}}
	}
	for _, req := range []*types.Request{agg("AGG_ACME_LOOSE", `{"scale":"not a number"}`), agg("AGG_ACME_LOOSE", ""), agg(types.AGG_SUM, "")} {
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatalf("Process(%s %s): %v", req.Aggregations[0].Type, req.Aggregations[0].Params, err)
		}
	}
	_, err := p.Process(ctx, agg("AGG_ACME_STRICT", `{"scael":2,"tag":5}`))
	unknownParam(t, err, "aggregations[0].params.scael", "scale")
}
