package descriptor

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// windows_first_extension_test.go (U67 E3-S1): predict and the engine
// read ONE windows-first decision over the same extension FieldInputs
// hooks. A request windows-first only through an extension
// aggregator's hook admits a regression, a holdout and a vector member
// reading the window label — the walk, the vector echo, streamability
// and the post-window plan all over the snapshot's hooks — and the
// engine runs it over the registry's.

const aggExtOf types.AggregationType = "AGG_EXT_OF"

// extOfInputs is the hook: params.of names the column the slot reads.
func extOfInputs(raw json.RawMessage) []string {
	var p struct {
		Of string `json:"of"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Of == "" {
		return nil
	}
	return []string{p.Of}
}

// extOfSnapshot is predict's view of the registration.
func extOfSnapshot() *ExtensionsSnapshot {
	return &ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: string(aggExtOf)}},
		FieldInputs: map[string]func(json.RawMessage) []string{"aggregator|" + string(aggExtOf): extOfInputs},
	}
}

// extOfSum sums the record column of names.
type extOfSum struct{ of string }

func (a extOfSum) Aggregate(records []*processing.Record, _ string) (float64, error) {
	var s float64
	for _, r := range records {
		if v, ok := r.NumericValue(a.of); ok {
			s += v
		}
	}
	return s, nil
}

// extOfRegistry is the runtime's view of the same registration.
func extOfRegistry() *processing.ExtensionRegistry {
	return &processing.ExtensionRegistry{
		Aggregators: map[types.AggregationType]processing.AggregatorFactory{
			aggExtOf: func(agg *types.Aggregation, _ *encoding.Schema) (processing.Aggregator, error) {
				ins := extOfInputs(agg.Params)
				if len(ins) == 0 {
					return extOfSum{}, nil
				}
				return extOfSum{of: ins[0]}, nil
			},
		},
		FieldInputs: map[string]processing.FieldInputsFunc{processing.StreamabilityKey("aggregator", string(aggExtOf)): extOfInputs},
	}
}

// extTriggeredRequest: only the extension aggregation reads lag_n
// (through its hook), turning the mode on; the regression, the holdout
// and vector v read lag_n as a record column.
func extTriggeredRequest() *types.Request {
	return &types.Request{
		Windows:      []*types.Window{lagWindow("lag_n")},
		Aggregations: []*types.Aggregation{{Type: aggExtOf, Field: "n", Label: "s", Params: json.RawMessage(`{"of":"lag_n"}`)}},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Target: "m", Predictors: []string{"lag_n"}},
			{Type: types.REG_OLS, Target: "m", Predictors: []string{"n"}, Holdout: "lag_n > 3"},
		},
		Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"n", "lag_n"}}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Vector: "v"}},
	}
}

// TestWindowsFirst_ExtensionTriggeredPredictMatchesRuntime: with the
// hook in hand predict admits the request windows-first at every site
// that reads the decision; the engine (the registry's hook) runs it, and
// the extension sums the lagged column exactly as AGG_SUM over lag_n.
func TestWindowsFirst_ExtensionTriggeredPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	schema := fieldRefSchema()
	snap := extOfSnapshot()
	req := extTriggeredRequest()

	if got := FieldRefRefusals(req, schema, snap); len(got) != 0 {
		t.Fatalf("walk over the snapshot refused: %v", got)
	}
	if got := FieldRefRefusals(req, schema, nil); len(got) == 0 {
		t.Fatal("walk without the hook admitted the window read")
	}
	opts := &PredictOptions{Extensions: snap}
	env := descriptor.NewEnvelope(nil)
	result := &descriptor.PredictResult{}
	predictVectors(env, result, req, schema, false, opts.classifier(nil))
	if !reflect.DeepEqual(result.ResolvedVectors["v"], []string{"n", "lag_n"}) {
		t.Fatalf("resolved vectors %v, want v = [n lag_n]", result.ResolvedVectors)
	}
	ok, reasons := computeStreamable(req, schema, opts)
	if ok || !strings.Contains(strings.Join(reasons, "|"), "windows-first") {
		t.Fatalf("streamable %v, reasons %v; want buffered, windows-first", ok, reasons)
	}
	if plan := classifyPostWindow(req, schema, UnscopedInstanceSnapshot(snap), nil); plan == nil || !plan.WindowsFirst {
		t.Fatalf("post-window plan %+v, want windows-first", plan)
	}
	if plan := classifyPostWindow(req, schema, nil, nil); plan == nil || plan.WindowsFirst {
		t.Fatalf("hook-free post-window plan %+v, want the historical order", plan)
	}

	recs := make([]*processing.Record, 20)
	for i := range recs {
		recs[i] = processing.NewRecord(schema, map[string]float64{"n": float64(i%6) + 0.3*float64(i), "m": float64(i*i%7) + float64(i), "cat": 0})
		recs[i].SetMergePosition(0, 0, i)
	}
	run := func(r *types.Request) *types.Response {
		t.Helper()
		resp, err := processing.NewProcessorWithExtensions(schema, extOfRegistry()).Process(context.Background(), r, processing.NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("runtime: %v", err)
		}
		return resp
	}
	resp := run(req)
	if len(resp.Regressions) != 2 || resp.Regressions[0].Error != nil || resp.Regressions[1].Error != nil || len(resp.Matrices) != 1 {
		t.Fatalf("runtime regressions %+v, matrices %+v", resp.Regressions, resp.Matrices)
	}
	twin := extTriggeredRequest()
	twin.Aggregations = []*types.Aggregation{sumField("lag_n", "s")}
	// AGG_SUM folds compensated; the plain loop may differ in the last
	// ulp, never by a missing (all-null) window column.
	got, want := resp.Data, run(twin).Data
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("extension rows %v, want the AGG_SUM twin's %v", got, want)
	}
	g, _ := got[0]["s"].(float64)
	w, _ := want[0]["s"].(float64)
	if w == 0 || math.Abs(g-w) > 1e-9*math.Abs(w) {
		t.Fatalf("extension sum %v, want the AGG_SUM twin's %v", g, w)
	}
}
