package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// TestProcessor_OLSStreams confirms the orchestrator routes an
// unpenalized REG_OLS request through the streaming path and emits a
// populated RegressionResult on the response. Mirrors the Phase 0
// not-implemented test, but for the live engine.
func TestProcessor_OLSStreams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}
	records := make([]*Record, 30)
	for i := range records {
		records[i] = NewRecord(schema, map[string]float64{
			"x": float64(i),
			"y": 1.0 + 2.0*float64(i),
		})
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y"}},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Name: "fit"},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Errorf("LastPath = %v, want PathStreaming (Phase 1 must take streaming route for unpenalized OLS)", proc.LastPath())
	}
	if len(resp.Regressions) != 1 {
		t.Fatalf("Regressions len = %d, want 1", len(resp.Regressions))
	}
	got := resp.Regressions[0]
	if got.Name != "fit" {
		t.Errorf("Regressions[0].Name = %q, want fit", got.Name)
	}
	if math.Abs(got.Coefficients[regression.InterceptKey]-1.0) > 1e-9 {
		t.Errorf("intercept = %v, want 1.0", got.Coefficients[regression.InterceptKey])
	}
	if math.Abs(got.Coefficients["x"]-2.0) > 1e-9 {
		t.Errorf("β_x = %v, want 2.0", got.Coefficients["x"])
	}
	if math.Abs(got.R2-1.0) > 1e-9 {
		t.Errorf("R² = %v, want 1.0 on noise-free data", got.R2)
	}
	if got.NObs != 30 {
		t.Errorf("NObs = %d, want 30", got.NObs)
	}
}

// TestProcessor_OLSBuffered confirms that a regression-only request
// (no aggregations) still routes through the buffered path (canStream
// requires ≥1 aggregation), and that the buffered orchestrator also
// fits the OLS model correctly via FitBuffered.
func TestProcessor_OLSBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}
	records := make([]*Record, 10)
	for i := range records {
		records[i] = NewRecord(schema, map[string]float64{
			"x": float64(i),
			"y": 3.0 + 0.5*float64(i),
		})
	}
	// No aggregations → buffered path. Pair with a regression slot only.
	req := &types.Request{
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathBuffered {
		t.Errorf("LastPath = %v, want PathBuffered (regression-only forces buffered)", proc.LastPath())
	}
	if len(resp.Regressions) != 1 {
		t.Fatalf("Regressions len = %d, want 1", len(resp.Regressions))
	}
	got := resp.Regressions[0]
	if math.Abs(got.Coefficients[regression.InterceptKey]-3.0) > 1e-9 {
		t.Errorf("intercept = %v, want 3.0", got.Coefficients[regression.InterceptKey])
	}
	if math.Abs(got.Coefficients["x"]-0.5) > 1e-9 {
		t.Errorf("β_x = %v, want 0.5", got.Coefficients["x"])
	}
}

// TestProcessor_OLSStreamingMatchesBuffered fits the same dataset
// through both orchestrator paths (streaming via the aggregation-paired
// request, buffered via the regression-only request) and asserts the
// emitted RegressionResult is identical to ~1e-10 across paths.
func TestProcessor_OLSStreamingMatchesBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x1", Type: encoding.FieldTypeF64},
			{Name: "x2", Type: encoding.FieldTypeF64},
		},
	}
	records := make([]*Record, 80)
	for i := range records {
		records[i] = NewRecord(schema, map[string]float64{
			"x1": float64(i) * 0.5,
			"x2": math.Sin(0.21 * float64(i)),
			"y":  1.2 + 0.7*float64(i)*0.5 - 1.1*math.Sin(0.21*float64(i)) + 0.05*math.Cos(float64(i)),
		})
	}
	specs := []*types.RegressionSpec{
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}},
	}

	streamingReq := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}},
		Regressions:  specs,
	}
	bufferedReq := &types.Request{Regressions: specs}

	streamProc := NewProcessor(schema)
	streamResp, err := streamProc.Process(context.Background(), streamingReq, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}
	if streamProc.LastPath() != PathStreaming {
		t.Fatalf("expected streaming path, got %v", streamProc.LastPath())
	}

	bufProc := NewProcessor(schema)
	bufResp, err := bufProc.Process(context.Background(), bufferedReq, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("buffered Process: %v", err)
	}
	if bufProc.LastPath() != PathBuffered {
		t.Fatalf("expected buffered path, got %v", bufProc.LastPath())
	}

	s := streamResp.Regressions[0]
	b := bufResp.Regressions[0]
	const tol = 1e-10
	for _, key := range []string{regression.InterceptKey, "x1", "x2"} {
		if math.Abs(s.Coefficients[key]-b.Coefficients[key]) > tol {
			t.Errorf("coef %s: streaming=%v buffered=%v Δ=%v", key, s.Coefficients[key], b.Coefficients[key], s.Coefficients[key]-b.Coefficients[key])
		}
	}
	if math.Abs(s.R2-b.R2) > tol {
		t.Errorf("R²: streaming=%v buffered=%v", s.R2, b.R2)
	}
}

// TestProcessor_BayesStreams confirms the orchestrator routes a
// REG_BAYES_LINEAR request paired with an online aggregation through
// the streaming path. Phase 4 lights up this code path; same shape as
// TestProcessor_OLSStreams but for the Bayes engine.
func TestProcessor_BayesStreams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}
	records := make([]*Record, 100)
	for i := range records {
		records[i] = NewRecord(schema, map[string]float64{
			"x": float64(i),
			"y": 1.0 + 2.0*float64(i),
		})
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Name: "bayes_fit"},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Errorf("LastPath = %v, want PathStreaming (Phase 4 must take streaming route for REG_BAYES_LINEAR)", proc.LastPath())
	}
	if len(resp.Regressions) != 1 {
		t.Fatalf("Regressions len = %d, want 1", len(resp.Regressions))
	}
	got := resp.Regressions[0]
	if got.Type != types.REG_BAYES_LINEAR {
		t.Errorf("Regressions[0].Type = %v, want REG_BAYES_LINEAR", got.Type)
	}
	if got.Prior != "nig" {
		t.Errorf("Regressions[0].Prior = %q, want \"nig\"", got.Prior)
	}
	// Default diffuse prior + clean line data: posterior mean matches OLS
	// to within ~1e-3 even on n=100 (the data Gram dominates ε=1e-3).
	if math.Abs(got.Coefficients[regression.InterceptKey]-1.0) > 1e-3 {
		t.Errorf("intercept = %v, want ~1.0", got.Coefficients[regression.InterceptKey])
	}
	if math.Abs(got.Coefficients["x"]-2.0) > 1e-3 {
		t.Errorf("β_x = %v, want ~2.0", got.Coefficients["x"])
	}
	if len(got.CredibleIntervals) != 2 {
		t.Errorf("CredibleIntervals len = %d, want 2 (intercept + x)", len(got.CredibleIntervals))
	}
	if len(got.PValues) != 0 {
		t.Errorf("Bayes emitted PValues = %v, want empty/nil", got.PValues)
	}
}
