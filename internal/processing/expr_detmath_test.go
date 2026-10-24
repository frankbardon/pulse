package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// BenchmarkFormulaTranscendental is the hot ATTR_FORMULA path through
// the scalar transcendental built-ins (exp, log, pow), evaluated per
// record by a compiled program: the guard that moving them from the
// standard library onto internal/detmath (portable, FMA-barriered
// kernels) is no gross regression.
func BenchmarkFormulaTranscendental(b *testing.B) {
	s := mkSchema("x", "y")
	r := NewRecord(s, map[string]float64{"x": 1.75, "y": 42.5})
	for _, bc := range []struct{ name, src string }{
		{"exp_log", "exp(x) + log(y)"},
		{"pow", "pow(y, x)"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			prog, err := newExprProgram(bc.src, "formula", s, nil, nil, exprenv.Options())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := prog.runRecord(r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestFeatLog_MatchesFormulaLog: FEAT_LOG (log(x + 1), computed as
// log1p) and the formula built-in log1p(x) run the same kernel —
// internal/detmath's portable Log1p through vecmath — so on every row
// the feature column and the formula column are bit-equal, on every
// architecture. (The formula spelled `log(x + 1)` is NOT bit-equal: the
// rounded x + 1 loses the precision log1p exists to keep.) The grid is
// dense enough that a FEAT_LOG reverted to math.Log1p differs on some
// row where the compiler fuses multiply-adds (arm64).
func TestFeatLog_MatchesFormulaLog(t *testing.T) {
	s := mkSchema("x")
	var recs []*Record
	for i := -1000; i <= 5000; i++ {
		recs = append(recs, NewRecord(s, map[string]float64{"x": float64(i) * 0.000731}))
	}
	resp, err := NewProcessor(s).Process(context.Background(), &types.Request{
		Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "x"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "f", Expression: "log1p(x)"},
			{Type: types.ATTR_FORMULA, Label: "same", Expression: "LOG_x == f ? 1 : 0"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "same", Label: "n_same"},
			{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		},
	}, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	if got, n := toFloat(resp.Data[0]["n_same"]), toFloat(resp.Data[0]["n"]); got != n || n != float64(len(recs)) {
		t.Fatalf("FEAT_LOG equals log1p(x) on %v of %v rows (%d records), want every row", got, n, len(recs))
	}
}
