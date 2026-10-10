package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_Matrices: predict reports each matrix's shape, axis,
// labels, missing mode, encoding, accumulator estimate, streamability,
// mergeability, a buffered spec's row store and pairwise PSD risk, in
// request order, off the runtime's own resolver.
func TestPredict_Matrices(t *testing.T) {
	pairwise := json.RawMessage(`{"missing": "pairwise"}`)
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_2", "q_1", "q_3"}, Labels: []string{"Two", "One", "Three"}}},
		Matrices: []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Vector: "v"},
			{Name: "rp", Type: types.MAT_CORRELATION, Vector: "v", Params: pairwise, Encoding: types.MatrixEncodingUpper},
			{Name: "r2", Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_3"}, Params: pairwise},
			{Name: "c2", Type: types.MAT_COVARIANCE, Fields: []string{"q_1", "q_3"}, Params: pairwise},
			{Name: "rho", Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"method": "spearman"}`)},
			{Name: "tau", Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_3"}, Params: json.RawMessage(`{"method": "kendall", "missing": "pairwise"}`)},
			{Name: "pear", Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_3"}, Params: json.RawMessage(`{"method": "pearson"}`)},
			{Name: "pv", Type: types.MAT_PARTIAL_CORRELATION, Vector: "v", Params: json.RawMessage(`{"control": ["q_1"]}`)},
			{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"q_2", "q_3"}, Params: json.RawMessage(`{"control": ["q_1"], "missing": "pairwise"}`)},
			{Name: "rel", Type: types.MAT_RELIABILITY, Vector: "v", Params: json.RawMessage(`{"reverse": ["q_1"], "scale_min": 0, "scale_max": 5, "missing": "pairwise"}`)},
			{Name: "rel2", Type: types.MAT_RELIABILITY, Fields: []string{"q_1", "q_3"}, Params: pairwise},
			{Name: "pca", Type: types.MAT_PCA, Vector: "v", Params: json.RawMessage(`{"components": 2, "missing": "pairwise"}`)},
			{Name: "pcak", Type: types.MAT_PCA, Fields: []string{"q_1", "q_3"}, Params: pairwise},
			{Name: "pcac", Type: types.MAT_PCA, Fields: []string{"q_1", "q_3"}, Params: json.RawMessage(`{"on": "covariance", "components": {"variance": 0.9}, "missing": "pairwise"}`)},
			{Name: "coll", Type: types.MAT_COLLINEARITY, Vector: "v", Params: json.RawMessage(`{"missing": "pairwise"}`)},
			{Name: "coll2", Type: types.MAT_COLLINEARITY, Fields: []string{"q_1", "q_3"}, Params: json.RawMessage(`{"center": true, "missing": "pairwise"}`)},
		},
	}
	env := predictFromBytes(vectorPredictCohort(t, matrixFixtureRecords), req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", *env.Errors[0])
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
		// A rank method is buffered and not mergeable, and keeps its
		// admitted rows: 8·(p + 1) bytes per record.
		{Name: "rho", Type: types.MAT_CORRELATION, Shape: [2]int{3, 3}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "listwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 8*(3+6), RowBufferBytes: i64(matrixFixtureRecords * 8 * 4)},
		{Name: "tau", Type: types.MAT_CORRELATION, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, RowBufferBytes: i64(matrixFixtureRecords * 8 * 3)},
		// An explicit "pearson" is the default: streamable, mergeable.
		{Name: "pear", Type: types.MAT_CORRELATION, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "listwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 8*(2+3), Streamable: true},
		// A partial correlation's axis drops a member control (q_1) and
		// its label; an outside control (q_1 beside inline q_2, q_3)
		// joins the fold, so the state and the PSD risk count three
		// columns on a 2 × 2 result.
		{Name: "pv", Type: types.MAT_PARTIAL_CORRELATION, Shape: [2]int{2, 2}, AxisKeys: []string{"q_2", "q_3"},
			Labels: []string{"Two", "Three"}, Missing: "listwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 8*(3+6), Streamable: true},
		{Name: "pc", Type: types.MAT_PARTIAL_CORRELATION, Shape: [2]int{2, 2}, AxisKeys: []string{"q_2", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*6, Streamable: true, PairwisePSDRisk: true},
		// Reliability reverses in the fold, so it stays streamable and
		// mergeable; omega's decomposition carries the PSD risk at p ≥ 3
		// only (a 2-item battery has no omega and an always-PSD r).
		{Name: "rel", Type: types.MAT_RELIABILITY, Shape: [2]int{3, 3}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "pairwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 56*6, Streamable: true, PairwisePSDRisk: true},
		{Name: "rel2", Type: types.MAT_RELIABILITY, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true},
		// PCA's primary is the p × k loadings: [p, k] for an integer k,
		// the [p, p] bound when kaiser or a share picks k from the data.
		// Its PSD risk follows the analysed matrix: a 2 × 2 pairwise
		// correlation is always PSD, a 2 × 2 pairwise covariance need not be.
		{Name: "pca", Type: types.MAT_PCA, Shape: [2]int{3, 2}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "pairwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 56*6, Streamable: true, PairwisePSDRisk: true},
		{Name: "pcak", Type: types.MAT_PCA, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true},
		{Name: "pcac", Type: types.MAT_PCA, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true, PairwisePSDRisk: true},
		// Collinearity's primary is the predictors' correlation [p, p];
		// its PSD risk is the correlation rule (p >= 3).
		{Name: "coll", Type: types.MAT_COLLINEARITY, Shape: [2]int{3, 3}, AxisKeys: []string{"q_2", "q_1", "q_3"},
			Labels: []string{"Two", "One", "Three"}, Missing: "pairwise", Encoding: types.MatrixEncodingFull,
			AccumulatorBytes: 32 + 56*6, Streamable: true, PairwisePSDRisk: true},
		{Name: "coll2", Type: types.MAT_COLLINEARITY, Shape: [2]int{2, 2}, AxisKeys: []string{"q_1", "q_3"},
			Missing: "pairwise", Encoding: types.MatrixEncodingFull, AccumulatorBytes: 32 + 56*3, Streamable: true},
	}
	// Ungrouped: one bucket, p² cells, one accumulator per merge block.
	for i := range want {
		p := int64(want[i].Shape[0])
		want[i].BucketBasis = "ungrouped"
		bytes := matrixFixtureBlocks * want[i].AccumulatorBytes
		if rb := want[i].RowBufferBytes; rb != nil {
			bytes += *rb
		}
		want[i].EstimatedBuckets, want[i].EstimatedCells, want[i].EstimatedBytes = i64(1), i64(p*p), i64(bytes)
		want[i].Mergeable = want[i].Streamable
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Errorf("matrices\n got %s\nwant %s", g, w)
	}
}

func i64(v int64) *int64 { return &v }

// matrixFixtureRecords spans two merge blocks (one full, one holding a
// single record), so every estimated_bytes expectation pins the blocks
// multiplier: matrixFixtureBlocks x buckets x accumulator_bytes.
const (
	matrixFixtureRecords = linalg.MergeBlockSize + 1
	matrixFixtureBlocks  = 2
)

// vectorPredictCohort is vectorPredictSchema carrying n zero-valued
// records — predict derives the record count from the file length.
func vectorPredictCohort(t *testing.T, n int) []byte {
	t.Helper()
	data := vectorPredictSchema(t)
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, make([]byte, n*schema.RecordByteSize())...)
}

// TestPredict_MatrixEstimatedBytesCountsBlocks pins
// MatrixPredict.EstimatedBytes as the real state — merge blocks x
// buckets x accumulator bytes, blocks counted PER SHARD on an archive
// (block numbering restarts per shard).
func TestPredict_MatrixEstimatedBytesCountsBlocks(t *testing.T) {
	req := func() *types.Request {
		return &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
			Matrices:     []types.MatrixSpec{{Name: "c", Type: types.MAT_COVARIANCE, Fields: []string{"id", "score"}}},
		}
	}
	bytesOf := func(t *testing.T, data []byte) *int64 {
		t.Helper()
		env := predictFromBytes(data, req(), nil)
		if len(env.Errors) != 0 {
			t.Fatalf("unexpected errors: %+v", *env.Errors[0])
		}
		ms := env.Data.(*descriptor.PredictResult).Matrices
		if len(ms) != 1 {
			t.Fatalf("matrices = %d", len(ms))
		}
		return ms[0].EstimatedBytes
	}
	acc := int64(32 + 8*(2+3))
	shards := func(counts ...int) []byte {
		specs := make([]struct {
			Name    string
			NRecord int
		}, len(counts))
		for i, n := range counts {
			specs[i].Name, specs[i].NRecord = "p"+string(rune('1'+i))+".pulse", n
		}
		return buildShardArchiveBytes(t, twoShardInspectSchema(t), specs)
	}
	cases := []struct {
		name string
		data []byte
		want int64
	}{
		// 4,097 + 5 records: two blocks in the first shard, one in the
		// second — three, where the same 4,102 rows in one file hold two.
		{"archive blocks per shard", shards(linalg.MergeBlockSize+1, 5), 3 * acc},
		{"one shard", shards(linalg.MergeBlockSize + 5), 2 * acc},
		{"exactly one block", shards(linalg.MergeBlockSize), acc},
		{"no records", shards(0), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bytesOf(t, c.data); got == nil || *got != c.want {
				t.Fatalf("estimated_bytes = %v, want %d", got, c.want)
			}
		})
	}
}

// TestPredict_MatrixBucketEstimate: a grouped request reports each
// spec's estimated buckets (from Groups[0] and the schema), cells
// (buckets × p²) and bytes (buckets × accumulator_bytes); a grouper
// whose keys depend on the data reports basis "unknown" and omits the
// three figures — never a guess, never a refusal (no guard: U19).
func TestPredict_MatrixBucketEstimate(t *testing.T) {
	pairwise := json.RawMessage(`{"missing": "pairwise"}`)
	specs := []types.MatrixSpec{
		{Name: "c", Type: types.MAT_COVARIANCE, Fields: []string{"q_1", "q_2", "q_3"}},
		{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_3"}, Params: pairwise},
	}
	cases := []struct {
		name   string
		groups []*types.Group
		basis  string
		want   int64 // buckets; 0 = omitted
	}{
		{"dictionary", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, "dictionary", 2},
		{"include", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"S", "X", "S"}}}, "include", 1},
		{"quantile", []*types.Group{{Type: types.GROUP_QUANTILE, Field: "q_1", Interval: 5}}, "quantile_bins", 5},
		{"range", []*types.Group{{Type: types.GROUP_RANGE, Field: "q_1", Interval: 10}}, "unknown", 0},
		{"multi-entry keys off Groups[0]", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_RANGE, Field: "q_1", Interval: 10}}, "dictionary", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Groups: c.groups, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}}, Matrices: specs}
			env := predictFromBytes(vectorPredictCohort(t, matrixFixtureRecords), req, nil)
			if len(env.Errors) != 0 {
				t.Fatalf("unexpected errors: %+v", *env.Errors[0])
			}
			got := env.Data.(*descriptor.PredictResult).Matrices
			if len(got) != len(specs) {
				t.Fatalf("%d matrices, want %d", len(got), len(specs))
			}
			for _, mp := range got {
				if mp.BucketBasis != c.basis {
					t.Errorf("%s: bucket_basis %q, want %q", mp.Name, mp.BucketBasis, c.basis)
				}
				if c.want == 0 {
					if mp.EstimatedBuckets != nil || mp.EstimatedCells != nil || mp.EstimatedBytes != nil {
						t.Errorf("%s: unknown basis carries estimates %v %v %v", mp.Name, mp.EstimatedBuckets, mp.EstimatedCells, mp.EstimatedBytes)
					}
					b, _ := json.Marshal(mp)
					if strings.Contains(string(b), "estimated_") {
						t.Errorf("%s: unknown basis renders estimates: %s", mp.Name, b)
					}
					continue
				}
				p := int64(mp.Shape[0])
				if mp.EstimatedBuckets == nil || *mp.EstimatedBuckets != c.want {
					t.Fatalf("%s: estimated_buckets %v, want %d", mp.Name, mp.EstimatedBuckets, c.want)
				}
				if mp.EstimatedCells == nil || *mp.EstimatedCells != c.want*p*p {
					t.Errorf("%s: estimated_cells %v, want %d", mp.Name, mp.EstimatedCells, c.want*p*p)
				}
				if want := matrixFixtureBlocks * c.want * mp.AccumulatorBytes; mp.EstimatedBytes == nil || *mp.EstimatedBytes != want {
					t.Errorf("%s: estimated_bytes %v, want %d", mp.Name, mp.EstimatedBytes, want)
				}
			}
		})
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
