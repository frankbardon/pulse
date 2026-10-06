package descriptor

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_Matrices: predict reports each matrix's shape, axis,
// labels, missing mode, encoding, accumulator estimate, streamability
// and pairwise PSD risk, in request order, off the runtime's own
// resolver.
func TestPredict_Matrices(t *testing.T) {
	pairwise := json.RawMessage(`{"missing": "pairwise"}`)
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_2", "q_1", "q_3"}, Labels: []string{"Two", "One", "Three"}}},
		Matrices: []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Vector: "v"},
			{Name: "rp", Type: types.MAT_CORRELATION, Vector: "v", Params: pairwise, Encoding: types.MatrixEncodingUpper},
			{Name: "r2", Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_3"}, Params: pairwise},
			{Name: "c2", Type: types.MAT_COVARIANCE, Fields: []string{"q_1", "q_3"}, Params: pairwise},
		},
	}
	env := predictFromBytes(vectorPredictSchema(t), req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}
	got := env.Data.(*descriptor.PredictResult).Matrices
	want := []descriptor.MatrixPredict{
		{Name: "MAT_COVARIANCE_v", Type: types.MAT_COVARIANCE, Shape: [2]int{3, 3}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "listwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 8*(3+6), Streamable: true},
		{Name: "rp", Type: types.MAT_CORRELATION, Shape: [2]int{3, 3}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "pairwise", Encoding: types.MatrixEncodingUpper,
			AccumulatorBytes: 32 + 56*6, Streamable: true, PairwisePSDRisk: true},
		// A 2 × 2 pairwise correlation is always PSD; a 2 × 2 pairwise
		// covariance need not be.
		{Name: "r2", Type: types.MAT_CORRELATION, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true},
		{Name: "c2", Type: types.MAT_COVARIANCE, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true, PairwisePSDRisk: true},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Errorf("matrices\n got %s\nwant %s", g, w)
	}
}

// TestPredict_MatricesOmitted: a matrix-free request and a refused spec
// carry no matrices key.
func TestPredict_MatricesOmitted(t *testing.T) {
	for name, req := range map[string]*types.Request{
		"none":    {Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}}},
		"refused": {Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "missing"}}},
	} {
		env := predictFromBytes(vectorPredictSchema(t), req, nil)
		res := env.Data.(*descriptor.PredictResult)
		if res.Matrices != nil {
			t.Errorf("%s: matrices = %v, want omitted", name, res.Matrices)
		}
		if name == "refused" && len(env.Errors) == 0 {
			t.Error("refused: no predict error")
		}
	}
}
