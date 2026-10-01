package processing

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// regSchema returns a small schema with a target, two predictors, and
// a non-numeric field used by validation tests.
func regSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x1", Type: encoding.FieldTypeF64},
			{Name: "x2", Type: encoding.FieldTypeF64},
			{Name: "cat", Type: encoding.FieldTypeCategoricalU8},
		},
	}
}

// makeRegRecords builds n records where y = a + b·x1 + c·x2 (no noise).
// b, c chosen so OLS recovers them exactly modulo numeric error.
func makeRegRecords(schema *encoding.Schema, x1, x2, y []float64) []*Record {
	out := make([]*Record, len(y))
	for i := range y {
		out[i] = NewRecord(schema, map[string]float64{
			"x1": x1[i],
			"x2": x2[i],
			"y":  y[i],
		})
	}
	return out
}

// makeRegAttr builds a registry-instantiated attribute computer for one
// of the regression attributes. Drives the same path the orchestrator
// uses.
func makeRegAttr(t *testing.T, kind types.AttributeType, schema *encoding.Schema, spec *types.Attribute) AttributeComputer {
	t.Helper()
	factory, ok := attributeRegistry[kind]
	if !ok {
		t.Fatalf("attribute %s not registered", kind)
	}
	if spec.Type == "" {
		spec.Type = kind
	}
	c, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("factory %s: %v", kind, err)
	}
	return c
}

// TestAttrRegFitted_Numerical verifies that ATTR_REG_FITTED emits
// ŷᵢ = Xᵢ · β + β₀ per row to within 1e-10. Constructs a noise-free
// linear fixture so OLS recovers the coefficients exactly.
func TestAttrRegFitted_Numerical(t *testing.T) {
	schema := regSchema()
	// y = 2 + 3*x1 + 5*x2
	x1 := []float64{1, 2, 3, 4, 5, 6}
	x2 := []float64{0, 1, 1, 2, 3, 5}
	y := make([]float64, len(x1))
	for i := range x1 {
		y[i] = 2 + 3*x1[i] + 5*x2[i]
	}
	records := makeRegRecords(schema, x1, x2, y)

	c := makeRegAttr(t, types.ATTR_REG_FITTED, schema, &types.Attribute{
		Target:     "y",
		Predictors: []string{"x1", "x2"},
	})
	values, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(values) != len(records) {
		t.Fatalf("len(values) = %d, want %d", len(values), len(records))
	}
	for i, want := range y {
		if math.Abs(values[i]-want) > 1e-10 {
			t.Errorf("row %d: fitted=%v want=%v (diff=%g)", i, values[i], want, values[i]-want)
		}
	}
}

// TestAttrRegResidual_SumsToZero asserts that the per-row residuals
// sum to ≈ 0 over n=50 noisy points when the OLS fit includes the
// (implicit) intercept. Tolerance 1e-8 absorbs rounding.
func TestAttrRegResidual_SumsToZero(t *testing.T) {
	schema := regSchema()
	const n = 50
	x1 := make([]float64, n)
	x2 := make([]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x1[i] = float64(i) * 0.5
		x2[i] = math.Sin(float64(i))
		// Add deterministic non-zero noise.
		y[i] = 1.5 + 0.7*x1[i] - 0.4*x2[i] + 0.3*math.Cos(float64(i)*1.7)
	}
	records := makeRegRecords(schema, x1, x2, y)

	c := makeRegAttr(t, types.ATTR_REG_RESIDUAL, schema, &types.Attribute{
		Target:     "y",
		Predictors: []string{"x1", "x2"},
	})
	values, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	if math.Abs(sum) > 1e-8 {
		t.Errorf("Σ residuals = %g, want ≈ 0 (tol 1e-8)", sum)
	}
}

// TestAttrRegLeverage_TraceEqualsP asserts the well-known identity
// Σᵢ hᵢᵢ = p + 1 for OLS with an intercept, where p is the predictor
// count.
func TestAttrRegLeverage_TraceEqualsP(t *testing.T) {
	schema := regSchema()
	x1 := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	x2 := []float64{2, 1, 3, 2, 5, 4, 7, 6, 9, 8}
	y := make([]float64, len(x1))
	for i := range x1 {
		y[i] = 1.0 + 2.0*x1[i] - 0.5*x2[i]
	}
	records := makeRegRecords(schema, x1, x2, y)

	c := makeRegAttr(t, types.ATTR_REG_LEVERAGE, schema, &types.Attribute{
		Target:     "y",
		Predictors: []string{"x1", "x2"},
	})
	values, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	// p = 2 predictors, +1 for intercept → trace = 3.
	if math.Abs(sum-3.0) > 1e-10 {
		t.Errorf("Σ leverages = %g, want 3 (= p + 1 with p=2)", sum)
	}
}

// TestAttrRegLeverage_RangeInUnit asserts every hᵢᵢ lands in [0, 1].
// (Strictly positive given the n > p+1 fixture and bounded predictors,
// but the closed identity guarantees the unit interval.)
func TestAttrRegLeverage_RangeInUnit(t *testing.T) {
	schema := regSchema()
	x1 := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	x2 := []float64{0.1, 0.3, 0.7, 1.2, 1.5, 2.1, 2.5, 3.0, 3.5, 4.0}
	y := make([]float64, len(x1))
	for i := range x1 {
		y[i] = 2.0 + 1.5*x1[i] + 0.5*x2[i]
	}
	records := makeRegRecords(schema, x1, x2, y)

	c := makeRegAttr(t, types.ATTR_REG_LEVERAGE, schema, &types.Attribute{
		Target:     "y",
		Predictors: []string{"x1", "x2"},
	})
	values, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for i, v := range values {
		if v < -1e-12 || v > 1.0+1e-12 {
			t.Errorf("row %d: leverage=%g out of [0, 1]", i, v)
		}
	}
}

// TestAttrRegLeverage_PenaltyRejected confirms any non-empty Penalty
// surfaces PROCESSING_CONFIG. Penalized leverage uses a different
// formula and is deferred.
func TestAttrRegLeverage_PenaltyRejected(t *testing.T) {
	schema := regSchema()
	for _, pen := range []string{"l1", "l2", "elasticnet"} {
		spec := &types.Attribute{
			Type:       types.ATTR_REG_LEVERAGE,
			Target:     "y",
			Predictors: []string{"x1"},
			Penalty:    pen,
			Alpha:      0.5,
			L1Ratio:    0.5,
		}
		_, err := attributeRegistry[types.ATTR_REG_LEVERAGE](spec, schema)
		if err == nil {
			t.Errorf("Penalty=%q: expected PROCESSING_CONFIG, got nil", pen)
			continue
		}
		if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
			t.Errorf("Penalty=%q: error %v does not carry PROCESSING_CONFIG", pen, err)
			continue
		}
		if !strings.Contains(err.Error(), "ATTR_REG_LEVERAGE") {
			t.Errorf("Penalty=%q: error = %q, want mention of ATTR_REG_LEVERAGE", pen, err.Error())
		}
	}
}

// TestAttrReg_PredictorValidation covers the spec-level errors:
// empty Predictors, missing Target, non-numeric predictor.
func TestAttrReg_PredictorValidation(t *testing.T) {
	schema := regSchema()

	cases := []struct {
		name    string
		spec    *types.Attribute
		wantSub string // substring expected in error message
	}{
		{
			name:    "empty predictors",
			spec:    &types.Attribute{Type: types.ATTR_REG_FITTED, Target: "y"},
			wantSub: "predictor",
		},
		{
			name:    "missing target",
			spec:    &types.Attribute{Type: types.ATTR_REG_FITTED, Predictors: []string{"x1"}},
			wantSub: "Target",
		},
		{
			name:    "categorical predictor",
			spec:    &types.Attribute{Type: types.ATTR_REG_FITTED, Target: "y", Predictors: []string{"cat"}},
			wantSub: "numeric",
		},
		{
			name:    "categorical target",
			spec:    &types.Attribute{Type: types.ATTR_REG_FITTED, Target: "cat", Predictors: []string{"x1"}},
			wantSub: "numeric",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := attributeRegistry[types.ATTR_REG_FITTED](tc.spec, schema)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("err=%v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

// TestAttrReg_LabelDefaults asserts the synthesized default label
// substitutes Target for the missing Field. Mirrors the rule in
// processing.defaultAttributeLabel / descriptor.attributeDefaultLabel.
func TestAttrReg_LabelDefaults(t *testing.T) {
	schema := regSchema()
	// x1 / x2 not collinear so OLS has a unique solution.
	x1 := []float64{1, 2, 3, 4, 5}
	x2 := []float64{0, 1, 4, 9, 16}
	y := []float64{1, 2, 3, 4, 5}
	records := makeRegRecords(schema, x1, x2, y)

	req := &types.Request{
		Attributes: []*types.Attribute{
			// Empty Label — should default to ATTR_REG_FITTED_y.
			{Type: types.ATTR_REG_FITTED, Target: "y", Predictors: []string{"x1", "x2"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "ATTR_REG_FITTED_y", Label: "avg_fitted"},
		},
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	avg, ok := resp.Data[0]["avg_fitted"].(float64)
	if !ok {
		t.Fatalf("avg_fitted not a float: %T", resp.Data[0]["avg_fitted"])
	}
	if math.Abs(avg-3.0) > 1e-10 {
		t.Errorf("avg_fitted = %v, want 3.0 (mean of y)", avg)
	}
}

// TestAttrReg_TwoPassStreamingParity drives the same request through
// the streaming two-pass path and the buffered path; outputs must
// match to 1e-10.
func TestAttrReg_TwoPassStreamingParity(t *testing.T) {
	schema := regSchema()
	const n = 25
	x1 := make([]float64, n)
	x2 := make([]float64, n)
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		x1[i] = float64(i)
		x2[i] = math.Sin(float64(i) * 0.4)
		y[i] = 2.5 + 1.2*x1[i] + 3.5*x2[i] + 0.2*math.Cos(float64(i))
	}
	records := makeRegRecords(schema, x1, x2, y)

	cases := []struct {
		name string
		typ  types.AttributeType
	}{
		{"fitted", types.ATTR_REG_FITTED},
		{"residual", types.ATTR_REG_RESIDUAL},
		{"leverage", types.ATTR_REG_LEVERAGE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				Attributes: []*types.Attribute{{
					Type:       tc.typ,
					Target:     "y",
					Predictors: []string{"x1", "x2"},
					Label:      "out",
				}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "out", Label: "sum_out"},
					{Type: types.AGG_AVERAGE, Field: "out", Label: "avg_out"},
				},
			}

			streamResp, bufResp := runStreamingAndBuffered(t, schema, records, req)
			for _, label := range []string{"sum_out", "avg_out"} {
				sv, _ := streamResp.Data[0][label].(float64)
				bv, _ := bufResp.Data[0][label].(float64)
				if math.Abs(sv-bv) > 1e-10 {
					t.Errorf("%s: streaming=%v buffered=%v diff=%g", label, sv, bv, sv-bv)
				}
			}
		})
	}
}

// TestAttrRegFitted_PenalizedAccepted asserts ATTR_REG_FITTED accepts
// the OLS penalized variants (l1, l2, elasticnet) — the validation
// path delegates to regression.ValidateRegression so the same rules
// apply to penalized fits inside the attribute as to a REG_OLS slot.
func TestAttrRegFitted_PenalizedAccepted(t *testing.T) {
	schema := regSchema()
	cases := []struct {
		name string
		spec *types.Attribute
	}{
		{
			name: "l2/ridge",
			spec: &types.Attribute{
				Type:       types.ATTR_REG_FITTED,
				Target:     "y",
				Predictors: []string{"x1"},
				Penalty:    "l2",
				Alpha:      0.5,
			},
		},
		{
			name: "l1/lasso",
			spec: &types.Attribute{
				Type:       types.ATTR_REG_FITTED,
				Target:     "y",
				Predictors: []string{"x1"},
				Penalty:    "l1",
				Alpha:      0.1,
			},
		},
		{
			name: "elasticnet",
			spec: &types.Attribute{
				Type:       types.ATTR_REG_FITTED,
				Target:     "y",
				Predictors: []string{"x1"},
				Penalty:    "elasticnet",
				Alpha:      0.1,
				L1Ratio:    0.5,
			},
		},
	}
	x1 := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	y := []float64{1.1, 2.2, 3.3, 4.4, 5.5, 6.6, 7.7, 8.8}
	x2 := make([]float64, len(x1))
	records := makeRegRecords(schema, x1, x2, y)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := attributeRegistry[types.ATTR_REG_FITTED](tc.spec, schema)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			vals, err := c.Compute(records, "")
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if len(vals) != len(records) {
				t.Errorf("len(vals)=%d, want %d", len(vals), len(records))
			}
		})
	}
}

// TestAttrRegResidual_PenalizedAccepted mirrors the FITTED case for
// residuals.
func TestAttrRegResidual_PenalizedAccepted(t *testing.T) {
	schema := regSchema()
	x1 := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	y := []float64{1.1, 2.2, 3.3, 4.4, 5.5, 6.6, 7.7, 8.8}
	x2 := make([]float64, len(x1))
	records := makeRegRecords(schema, x1, x2, y)
	spec := &types.Attribute{
		Type:       types.ATTR_REG_RESIDUAL,
		Target:     "y",
		Predictors: []string{"x1"},
		Penalty:    "l2",
		Alpha:      0.3,
	}
	c, err := attributeRegistry[types.ATTR_REG_RESIDUAL](spec, schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	vals, err := c.Compute(records, "")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(vals) != len(records) {
		t.Errorf("len(vals)=%d, want %d", len(vals), len(records))
	}
}
