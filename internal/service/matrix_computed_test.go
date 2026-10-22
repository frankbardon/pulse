package service

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_computed_test.go: a matrix over a computed vector member (an
// ATTR_FORMULA label of the same request, sem-contribution E2-S2) folds
// the value the attribute writes on the record, so the streamed,
// buffered, decode-parallel and sharded runs emit the same bits — and a
// standardized vector over it z-scores the computed values over the
// listwise rows.

// computedInvarianceRequest is a MAT_COVARIANCE (probability-weighted,
// optionally standardized) or MAT_SEM over x1, a nullable formula of
// x3, and x3.
func computedInvarianceRequest(path string, typ types.MatrixType, std types.VectorStandardize) *types.Request {
	spec := types.MatrixSpec{Name: "mat", Type: typ, Vector: "z", Params: json.RawMessage(`{"max_drop_share": 0}`),
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})}
	if typ == types.MAT_SEM {
		spec.Params = json.RawMessage(`{"max_drop_share": 0, "model": "x1 ~ f + x3"}`)
		spec.Weight = types.SlotWeight{}
	}
	return &types.Request{
		Cohort:     &types.Cohort{Filename: path},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "x3 > 9.9e5 ? nil : x3 / 1e3 + x3 * x3 / 1e9"}},
		Vectors:    []types.VectorSpec{{Name: "z", Fields: []string{"x1", "f", "x3"}, Standardize: std}},
		Matrices:   []types.MatrixSpec{spec},
	}
}

// TestMatrixComputedMember_PathInvariant: a row-local computed member
// keeps the request streamable and mergeable, and every path emits the
// same bits, standardized or not.
func TestMatrixComputedMember_PathInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record computed-member invariance gate in -short mode")
	}
	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	n := 25*B + 2777
	recs, nullAt := coMomentRows(n, 0)
	path := dir + "/computed.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
	total := 0
	for _, s := range shardRows {
		total += s
	}
	mem := fs.NewMemMap()
	if err := afero.WriteFile(mem.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
		t.Fatal(err)
	}
	srecs, snull := coMomentRows(total, 0)
	if err := afero.WriteFile(mem.Fs(), "single.pulse", writeNullablePulse(t, schema, srecs, snull), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		typ types.MatrixType
		std types.VectorStandardize
	}{
		{types.MAT_COVARIANCE, ""},
		{types.MAT_COVARIANCE, types.VectorStandardizeZScore},
		{types.MAT_SEM, types.VectorStandardizeZScore},
	} {
		t.Run(fmt.Sprintf("%s/%s", c.typ, c.std), func(t *testing.T) {
			req := computedInvarianceRequest(path, c.typ, c.std)
			if !processing.CanMergeRequest(req, schema) || !processing.CanStreamRequest(req, schema) {
				t.Fatal("a row-local computed member must keep the request streamable and mergeable")
			}
			serial := runMatrixInvariance(t, cfg, req, 1, 0)
			for _, workers := range []int{2, 3, 8} {
				assertMatrixRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), runMatrixInvariance(t, cfg, req, workers, 0), serial)
			}
			buf := computedInvarianceRequest(path, c.typ, c.std)
			buf.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
			if processing.CanStreamRequest(buf, schema) {
				t.Fatal("AGG_MEDIAN request streams; the buffered arm is not exercised")
			}
			got := processMatrices(t, cfg, buf).Matrices[0]
			for r, row := range got.Primary.Values {
				for k, v := range row {
					if math.Float64bits(v) != serial.words[r*len(row)+k] {
						t.Errorf("buffered [%d][%d] = %.17g differs from streamed", r, k, v)
					}
				}
			}
			areq := computedInvarianceRequest("archive.pulse", c.typ, c.std)
			aserial := runMatrixInvariance(t, mem, areq, 0, 1)
			for _, workers := range []int{2, 8} {
				assertMatrixRunsEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), runMatrixInvariance(t, mem, areq, 0, workers), aserial)
			}
			single := runMatrixInvariance(t, mem, computedInvarianceRequest("single.pulse", c.typ, c.std), 0, 0)
			if i := firstWordDiff(single.words, aserial.words); i != -1 {
				t.Errorf("archive differs from its single-file twin at word %d", i)
			}
		})
	}
}

// TestMatrixComputedMember_StandardizedZScoresComputedValues: under
// standardize a computed member is z-scored over the listwise rows like
// any member — the ddof-0 covariance is the raw run's correlation, the
// computed member's variance exactly 1 — and its null rows (the formula
// returns nil) are dropped before the z-score.
func TestMatrixComputedMember_StandardizedZScoresComputedValues(t *testing.T) {
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
