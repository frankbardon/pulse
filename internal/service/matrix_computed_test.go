package service

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
)

// matrix_computed_test.go: a matrix over a computed vector member (an
// ATTR_FORMULA label of the same request, sem-contribution E2-S2) folds
// the value the attribute writes on the record, so the streamed,
// buffered, decode-parallel and sharded runs emit the same bits — and a
// standardized vector over it z-scores the computed values over the
// listwise rows.

// TestMatrixComputedMember_StandardizedZScoresComputedValues: under
// standardize a computed member is z-scored over the listwise rows like
// any member — the ddof-0 covariance is the raw run's correlation, the
// computed member's variance exactly 1 — and its null rows (the formula
// returns nil) are dropped before the z-score.
func TestMatrixComputedMember_StandardizedZScoresComputedValues(t *testing.T) {
	t.Parallel()

	var rows [][]float64
	for i := 0; i < 60; i++ {
		x := float64(i)
		rows = append(rows, []float64{1.5 + math.Mod(x*7, 11), 2.25*math.Mod(x, 5) + 0.5*x, math.Mod(x*13, 17) - 4})
	}
	cfg := fs.NewMemMap()
	writeF64Cohort(t, cfg, "raw.pulse", []string{"a", "b", "c"}, rows, nil)
	run := func(std types.VectorStandardize, typ types.MatrixType, params string) *types.Response {
		return processMatrices(t, cfg, &types.Request{
			Cohort:     &types.Cohort{Filename: "raw.pulse"},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "c > 10 ? nil : a * b - c"}},
			Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"a", "f", "b"}, Standardize: std}},
			Matrices:   []types.MatrixSpec{{Name: "m", Type: typ, Vector: "v", Params: json.RawMessage(params)}},
		})
	}
	std := run(types.VectorStandardizeZScore, types.MAT_COVARIANCE, `{"ddof": 0}`)
	raw := run("", types.MAT_CORRELATION, `{}`)
	assertValuesClose(t, "standardized computed member", std.Matrices[0].Primary, raw.Matrices[0].Primary, 1e-14)
	if d := std.Matrices[0].Primary.Values[1][1]; semRel(d, 1) > 1e-15 {
		t.Errorf("computed member variance = %.17g, want 1", d)
	}
	dropped := 0
	for i := 0; i < 60; i++ {
		if math.Mod(float64(i)*13, 17)-4 > 10 {
			dropped++
		}
	}
	if c := std.Components.Matrices[0]; dropped == 0 || c.NListwiseDropped != dropped || c.N != 60-dropped {
		t.Errorf("components n %d dropped %d, want %d / %d", c.N, c.NListwiseDropped, 60-dropped, dropped)
	}
}
