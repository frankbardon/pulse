package service

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// U16 E3-S3: Response.Components.Matrices — one entry per
// Response.Matrices result: the floor {n, n_null, n_listwise_dropped},
// pairwise {min_pair_n, max_pair_n}, the weighted floor keys and the
// operator map ({ddof} / none).

// componentRows: [x1, x2, x3, f, p]. Row 2 carries weight 0 under both
// kinds (counts toward n, adds no mass); row 3 has one null member,
// row 4 every member null; row 5 a null probability weight and row 6 a
// negative frequency weight (both invalid).
func componentRows() [][5]float64 {
	nan := math.NaN()
	return [][5]float64{
		{1, 2, 3, 1, 1},
		{2, 4, 1, 2, 0.5},
		{3, 1, 2, 0, 0},
		{nan, 5, 6, 1, 1},
		{nan, nan, nan, 1, 1},
		{4, 3, 5, 1, nan},
		{5, 6, 7, -1, 2},
	}
}

func floatp(v float64) *float64 { return &v }

func TestMatrixComponents_Floor(t *testing.T) {
	cases := []struct {
		name   string
		typ    types.MatrixType
		weight string
		params string
		want   types.MatrixComponents
	}{
		{
			name: "covariance listwise", typ: types.MAT_COVARIANCE,
			want: types.MatrixComponents{N: 5, NNull: 2, NListwiseDropped: 2, Operator: map[string]any{"ddof": 1}},
		},
		{
			name: "covariance listwise ddof 0", typ: types.MAT_COVARIANCE, params: `{"ddof": 0}`,
			want: types.MatrixComponents{N: 5, NNull: 2, NListwiseDropped: 2, Operator: map[string]any{"ddof": 0}},
		},
		{
			name: "correlation listwise", typ: types.MAT_CORRELATION,
			want: types.MatrixComponents{N: 5, NNull: 2, NListwiseDropped: 2},
		},
		{
			// Row 4 is the only row pairwise admits nowhere; x1's own
			// rows (5) are the thinnest pair, x2 / x3 (6) the widest.
			name: "correlation pairwise", typ: types.MAT_CORRELATION, params: `{"missing": "pairwise"}`,
			want: types.MatrixComponents{N: 6, NNull: 1, MinPairN: intp(5), MaxPairN: intp(6)},
		},
		{
			// Weight 0 (row 2) counts toward n; the null weight (row 5)
			// is invalid. Σw = 1 + .5 + 0 + 2, n_eff = 3.5² / 5.25.
			name: "covariance listwise probability", typ: types.MAT_COVARIANCE, weight: "p",
			want: types.MatrixComponents{N: 4, NNull: 2, NListwiseDropped: 2,
				SumWeights: floatp(3.5), NEff: floatp(3.5 * 3.5 / 5.25), NWeightInvalid: intp(1),
				Operator: map[string]any{"ddof": 1}},
		},
		{
			// Frequency: no n_eff; the negative weight (row 6) is invalid.
			name: "correlation listwise frequency", typ: types.MAT_CORRELATION, weight: "f",
			want: types.MatrixComponents{N: 4, NNull: 2, NListwiseDropped: 2,
				SumWeights: floatp(4), NWeightInvalid: intp(1)},
		},
		{
			name: "covariance pairwise frequency", typ: types.MAT_COVARIANCE, weight: "f", params: `{"missing": "pairwise"}`,
			// Row 3 (one null member) adds its weight here.
			want: types.MatrixComponents{N: 5, NNull: 1, MinPairN: intp(4), MaxPairN: intp(5),
				SumWeights: floatp(5), NWeightInvalid: intp(1), Operator: map[string]any{"ddof": 1}},
		},
	}
	cfg := writeMatrixCohort(t, "c.pulse", componentRows())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := missingSpec(c.typ, x123, c.weight, c.params)
			resp := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: "c.pulse"},
				Matrices: []types.MatrixSpec{spec},
			})
			if resp.Components == nil || len(resp.Components.Matrices) != 1 {
				t.Fatalf("Components.Matrices = %+v, want one entry", resp.Components)
			}
			want := c.want
			want.Name, want.Type = "m", c.typ
			got := resp.Components.Matrices[0]
			if !reflect.DeepEqual(mustJSON(t, got), mustJSON(t, want)) {
				t.Errorf("Components.Matrices[0]\n got %s\nwant %s", mustJSON(t, got), mustJSON(t, want))
			}
		})
	}
}

// TestMatrixComponents_StreamingEqualsBuffered: the entry is a function
// of the filtered rows, whichever path folded them, and its order
// follows Response.Matrices.
func TestMatrixComponents_StreamingEqualsBuffered(t *testing.T) {
	cfg := writeMatrixCohort(t, "big.pulse", matrixRows(2*4096+77))
	specs := []types.MatrixSpec{
		missingSpec(types.MAT_CORRELATION, x123, "p", `{"missing": "pairwise"}`),
		missingSpec(types.MAT_COVARIANCE, x123, "f", ""),
	}
	specs[0].Name, specs[1].Name = "a", "b"
	base := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "big.pulse"}, Matrices: specs}
	}
	streamReq, bufReq := base(), base()
	bufReq.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
	if !processing.CanStreamRequest(streamReq, matrixSchema()) || processing.CanStreamRequest(bufReq, matrixSchema()) {
		t.Fatal("fixture does not exercise both arms")
	}
	a := processMatrices(t, cfg, streamReq).Components.Matrices
	b := processMatrices(t, cfg, bufReq).Components.Matrices
	if len(a) != 2 || a[0].Name != "a" || a[1].Name != "b" {
		t.Fatalf("streamed entries %s, want a then b", mustJSON(t, a))
	}
	if mustJSON(t, a) != mustJSON(t, b) {
		t.Errorf("streamed %s\nbuffered %s", mustJSON(t, a), mustJSON(t, b))
	}
}

// TestMatrixComponents_OptOut: Request.DisableComponents leaves
// Components nil on both arms, and the response is otherwise
// byte-identical to the enabled one with its Components removed.
func TestMatrixComponents_OptOut(t *testing.T) {
	cfg := writeMatrixCohort(t, "c.pulse", componentRows())
	for _, buffered := range []bool{false, true} {
		mk := func(disable bool) *types.Request {
			req := &types.Request{
				Cohort:            &types.Cohort{Filename: "c.pulse"},
				Matrices:          []types.MatrixSpec{missingSpec(types.MAT_CORRELATION, x123, "p", `{"missing": "pairwise"}`)},
				DisableComponents: &disable,
			}
			if buffered {
				req.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
			}
			return req
		}
		on := processMatrices(t, cfg, mk(false))
		off := processMatrices(t, cfg, mk(true))
		if off.Components != nil {
			t.Fatalf("buffered=%v: disabled run carries Components %s", buffered, mustJSON(t, off.Components))
		}
		if on.Components == nil || len(on.Components.Matrices) != 1 {
			t.Fatalf("buffered=%v: enabled run carries no Components.Matrices", buffered)
		}
		on.Components = nil
		a, _ := json.Marshal(on)
		b, _ := json.Marshal(off)
		if string(a) != string(b) {
			t.Errorf("buffered=%v: disabled wire form differs beyond Components\n on %s\noff %s", buffered, a, b)
		}
	}
}

// TestMatrixComponents_ShardWorkerInvariant: the integer tallies behind
// the entry (listwise drops, pairwise all-null rows) are summed across
// partitions, so every shard-worker count reports the serial entry.
// Members x1 / x2 are both null on some rows, so n_null is non-zero on
// both modes.
func TestMatrixComponents_ShardWorkerInvariant(t *testing.T) {
	schema := coMomentSchema()
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, []int{900, 1300, 77, 2000}), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, mode := range []string{"listwise", "pairwise"} {
		spec := types.MatrixSpec{Name: "m", Type: types.MAT_COVARIANCE, Fields: []string{"x1", "x2"},
			Params: json.RawMessage(`{"missing": "` + mode + `"}`),
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})}
		req := &types.Request{Cohort: &types.Cohort{Filename: "archive.pulse"}, Matrices: []types.MatrixSpec{spec}}
		var serial string
		for _, workers := range []int{1, 2, 4} {
			svc := New(cfg)
			svc.SetShardWorkers(workers)
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("%s ShardWorkers=%d: %v", mode, workers, err)
			}
			if resp.Components == nil || len(resp.Components.Matrices) != 1 {
				t.Fatalf("%s ShardWorkers=%d: no Components.Matrices entry", mode, workers)
			}
			c := resp.Components.Matrices[0]
			if c.NNull == 0 {
				t.Fatalf("%s: fixture has no null rows", mode)
			}
			got := mustJSON(t, c)
			if workers == 1 {
				serial = got
			} else if got != serial {
				t.Errorf("%s ShardWorkers=%d: %s, serial %s", mode, workers, got, serial)
			}
		}
	}
}
