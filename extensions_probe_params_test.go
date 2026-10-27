package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Probe params (U53 E4-S1 follow-up): since E1-S3 a request reaches an
// extension factory only with every Required param present, so the
// registration probe at pulse.New hands each factory a params object
// that satisfies the same declaration. A factory that refuses an absent
// Required param — the sensible thing once the request path guarantees
// it — now registers and runs in every probed category.

// requireScale is a factory-side check of strictParams' Required
// "scale": absent or non-numeric is an error. want, when non-nil, is the
// one value accepted (a factory stricter than the placeholder).
func requireScale(raw json.RawMessage, want *float64) error {
	var p struct {
		Scale *float64 `json:"scale"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("params: %w", err)
		}
	}
	if p.Scale == nil {
		return stderrors.New("params.scale is required")
	}
	if want != nil && *p.Scale != *want {
		return fmt.Errorf("params.scale must be %v", *want)
	}
	return nil
}

// guardedMatrixOp checks MatrixInput.Params in Finalize as well.
type guardedMatrixOp struct {
	inner extend.MatrixOperator
	want  *float64
}

func (g guardedMatrixOp) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	if err := requireScale(in.Params, g.want); err != nil {
		return extend.MatrixOutput{}, err
	}
	return g.inner.Finalize(in)
}

// guardedFilterer checks the spec's params when the builder builds.
type guardedFilterer struct{ want *float64 }

func (g guardedFilterer) Build(spec *types.Filterer, schema *encoding.Schema) (extend.FilterFunc, error) {
	if err := requireScale(spec.Params, g.want); err != nil {
		return nil, err
	}
	return strictKeepAll{}.Build(spec, schema)
}

// guardedExtensions is strictExtensions with every factory refusing an
// absent (or, with want, a different) Required scale. The test is
// declared WeightAware so the probe constructs it.
func guardedExtensions(want *float64) pulse.Extensions {
	ext := strictExtensions()
	agg := ext.Aggregators[0].Factory
	ext.Aggregators[0].Factory = func(s *types.Aggregation, sc *encoding.Schema) (extend.Aggregator, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return agg(s, sc)
	}
	attr := ext.Attributes[0].Factory
	ext.Attributes[0].Factory = func(s *types.Attribute, sc *encoding.Schema) (extend.AttributeComputer, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return attr(s, sc)
	}
	ext.Filterers[0].Factory = func() extend.FiltererBuilder { return guardedFilterer{want} }
	grp := ext.Groupers[0].Factory
	ext.Groupers[0].Factory = func(s *types.Group, sc *encoding.Schema) (extend.Grouper, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return grp(s, sc)
	}
	win := ext.Windows[0].Factory
	ext.Windows[0].Factory = func(s *types.Window, o extend.WindowOptions) (extend.WindowComputer, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return win(s, o)
	}
	feat := ext.Features[0].Factory
	ext.Features[0].Factory = func(s *types.Feature, sc *encoding.Schema) (extend.FeatureComputer, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return feat(s, sc)
	}
	test := ext.Tests[0].RowFactory
	ext.Tests[0].WeightAware = true
	ext.Tests[0].RowFactory = func(s *types.Test, sc *encoding.Schema) (extend.RowTest, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return test(s, sc)
	}
	mat := ext.MatrixOps[0].Factory
	ext.MatrixOps[0].Factory = func(s *types.MatrixSpec, sc *encoding.Schema) (extend.MatrixOperator, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		op, err := mat(s, sc)
		if err != nil {
			return nil, err
		}
		return guardedMatrixOp{inner: op, want: want}, nil
	}
	reg := ext.Regressions[0].Factory
	ext.Regressions[0].Factory = func(s *types.RegressionSpec, sc *encoding.Schema) (extend.Regression, error) {
		if err := requireScale(s.Params, want); err != nil {
			return nil, err
		}
		return reg(s, sc)
	}
	return ext
}

// onlyCategory keeps one category's registration of ext.
func onlyCategory(ext pulse.Extensions, category string) pulse.Extensions {
	var out pulse.Extensions
	switch category {
	case "aggregator":
		out.Aggregators = ext.Aggregators
	case "attribute":
		out.Attributes = ext.Attributes
	case "filterer":
		out.Filterers = ext.Filterers
	case "grouper":
		out.Groupers = ext.Groupers
	case "window":
		out.Windows = ext.Windows
	case "feature":
		out.Features = ext.Features
	case "test":
		out.Tests = ext.Tests
	case "matrix":
		out.MatrixOps = ext.MatrixOps
	case "regression":
		out.Regressions = ext.Regressions
	}
	return out
}

// TestExtensions_ProbeParamsSatisfyRequired: per category, a
// registration whose factory refuses an absent Required param passes
// pulse.New (the probe hands it the declaration's placeholder) and runs
// a conforming request.
func TestExtensions_ProbeParamsSatisfyRequired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			p, cohort := openE2E(t, onlyCategory(guardedExtensions(nil), tc.category))
			if _, err := p.Process(ctx, tc.request(cohort, json.RawMessage(`{"scale":2}`))); err != nil {
				t.Fatalf("Process: %v", err)
			}
		})
	}
}

// TestExtensions_ProbeParamsRefusalNamesThem: a factory that refuses
// the placeholder itself fails pulse.New with
// PULSE_EXTENSION_FACTORY_PANIC, and details.probe_params shows the
// object it was handed — in every category the probe constructs
// (filterers and windows are never constructed by the probe).
func TestExtensions_ProbeParamsRefusalNamesThem(t *testing.T) {
	t.Parallel()

	two := 2.0
	want := map[string]any{"scale": float64(1)}
	for _, category := range []string{"aggregator", "attribute", "grouper", "feature", "test", "matrix", "regression"} {
		t.Run(category, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(onlyCategory(guardedExtensions(&two), category)))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_FACTORY_PANIC {
				t.Fatalf("pulse.New err = %v, want PULSE_EXTENSION_FACTORY_PANIC", err)
			}
			if got := ce.Details["probe_params"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("details.probe_params = %#v, want %#v (details %v)", got, want, ce.Details)
			}
		})
	}
	for _, category := range []string{"filterer", "window"} {
		t.Run(category+" not probed", func(t *testing.T) {
			if _, err := pulse.New(newTestOptions(onlyCategory(guardedExtensions(&two), category))); err != nil {
				t.Fatalf("pulse.New: %v", err)
			}
		})
	}
}
