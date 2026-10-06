package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TestTopPairs_Ranking: top_pairs ranks the strict upper triangle by
// |r| descending, breaks |r| ties by (row, col) axis order, skips the
// diagonal and every NaN pair, carries each pair's own n, and lists
// every candidate when k exceeds them.
func TestTopPairs_Ranking(t *testing.T) {
	nan := math.NaN()
	// Members a b c d e; d has no spread (its row / column NaN).
	rows := [][]float64{
		{1, 0.5, -0.8, nan, 0.2},
		{0.5, 1, 0.5, nan, -0.8},
		{-0.8, 0.5, 1, nan, -0.5},
		{nan, nan, nan, nan, nan},
		{0.2, -0.8, -0.5, nan, 1},
	}
	r, err := linalg.NewSymFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	members := []string{"a", "b", "c", "d", "e"}
	pairN := func(i, j int) int64 { return int64(100 + 10*i + j) }
	all := []types.MatrixPair{
		{Row: "a", Col: "c", R: -0.8, N: 102},
		{Row: "b", Col: "e", R: -0.8, N: 114},
		{Row: "a", Col: "b", R: 0.5, N: 101},
		{Row: "b", Col: "c", R: 0.5, N: 112},
		{Row: "c", Col: "e", R: -0.5, N: 124},
		{Row: "a", Col: "e", R: 0.2, N: 104},
	}
	cases := []struct {
		k    int
		want []types.MatrixPair
	}{
		{1, all[:1]},
		{3, all[:3]},
		{6, all},
		{50, all},
	}
	for _, c := range cases {
		got := topPairs(r, pairN, members, c.k)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("k=%d:\n got %+v\nwant %+v", c.k, got, c.want)
		}
	}
	if got := topPairs(r, pairN, []string{"x"}, 3); got == nil || len(got) != 0 {
		t.Errorf("one member: %v, want an empty (non-nil) list", got)
	}
}

// TestMatrixCorrelation_TopPairsResult: a MAT_CORRELATION slot with
// params.summary.top_pairs emits vectors.top_pairs read off its own
// primary matrix (r bit-equal to the cell) with the listwise N, or the
// pairwise per-pair N; without the param no vectors are emitted.
func TestMatrixCorrelation_TopPairsResult(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, Nullable: true},
		{Name: "z", Type: encoding.FieldTypeF64, ByteOffset: 16, Nullable: true},
	}}
	for _, c := range []struct {
		params   string
		pairwise bool
		wantLen  int
	}{
		{``, false, 0},
		{`{"summary": {"top_pairs": 2}}`, false, 2},
		{`{"missing": "pairwise", "summary": {"top_pairs": 9}}`, true, 3},
	} {
		t.Run(c.params, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(c.params)}}}
			slots, err := buildMatrixSlotsFor(req, schema, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 60; i++ {
				x := float64(i%13) + 0.5*float64(i%4)
				vals := map[string]float64{"x": x, "y": 3 - x + float64(i%5), "z": float64((i * 7) % 11)}
				var nulls map[string]bool
				if i%6 == 1 {
					nulls = map[string]bool{"z": true}
				}
				rec := NewRecordWithNulls(schema, vals, nulls)
				rec.SetMergePosition(0, i)
				if err := slots[0].UpdateRow(rec, ""); err != nil {
					t.Fatal(err)
				}
			}
			res, _, err := slots[0].result(false)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantLen == 0 {
				if res.Vectors != nil {
					t.Fatalf("vectors = %v, want none without params.summary", res.Vectors)
				}
				return
			}
			got, ok := res.Vectors["top_pairs"].([]types.MatrixPair)
			if !ok || len(got) != c.wantLen {
				t.Fatalf("top_pairs = %#v, want %d pairs", res.Vectors["top_pairs"], c.wantLen)
			}
			idx := map[string]int{"x": 0, "y": 1, "z": 2}
			for _, pr := range got {
				i, j := idx[pr.Row], idx[pr.Col]
				if math.Float64bits(pr.R) != math.Float64bits(res.Primary.Values[i][j]) {
					t.Errorf("%s/%s r = %v, primary cell %v", pr.Row, pr.Col, pr.R, res.Primary.Values[i][j])
				}
				wantN := 50 // listwise: rows with z present
				if c.pairwise {
					wantN = int(res.Auxiliary["n"].Values[i][j])
				}
				if pr.N != wantN {
					t.Errorf("%s/%s n = %d, want %d", pr.Row, pr.Col, pr.N, wantN)
				}
			}
			if c.pairwise {
				ns := map[int]bool{}
				for _, pr := range got {
					ns[pr.N] = true
				}
				if !ns[60] || !ns[50] {
					t.Errorf("pairwise pair Ns %v, want both 60 (x/y) and 50 (with z)", ns)
				}
			}
		})
	}
}
