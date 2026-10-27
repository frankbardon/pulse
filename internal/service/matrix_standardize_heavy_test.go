//go:build heavy

package service

import (
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMatrixStandardize_PathInvariant: the standardized state is a
// function of the merged co-moment, so the streamed, buffered,
// decode-parallel and sharded runs emit the same bits.
func TestMatrixStandardize_PathInvariant(t *testing.T) {
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
	path := dir + "/std.pulse"
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

	for _, typ := range []types.MatrixType{types.MAT_COVARIANCE, types.MAT_SEM} {
		t.Run(string(typ), func(t *testing.T) {
			req := standardizeInvarianceRequest(path, typ)
			if !processing.CanMergeRequest(req, schema) || !processing.CanStreamRequest(req, schema) {
				t.Fatal("a standardized matrix-only request must stream and merge")
			}
			serial := runMatrixInvariance(t, cfg, req, 1, 0)
			for _, workers := range []int{2, 3, 8} {
				assertMatrixRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), runMatrixInvariance(t, cfg, req, workers, 0), serial)
			}
			buf := standardizeInvarianceRequest(path, typ)
			buf.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
			if processing.CanStreamRequest(buf, schema) {
				t.Fatal("AGG_MEDIAN request streams; the buffered arm is not exercised")
			}
			got := processMatrices(t, cfg, buf).Matrices[0]
			for r, row := range got.Primary.Values {
				for c, v := range row {
					if math.Float64bits(v) != serial.words[r*len(row)+c] {
						t.Errorf("buffered [%d][%d] = %.17g differs from streamed", r, c, v)
					}
				}
			}
			areq := standardizeInvarianceRequest("archive.pulse", typ)
			aserial := runMatrixInvariance(t, mem, areq, 0, 1)
			for _, workers := range []int{2, 8} {
				assertMatrixRunsEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), runMatrixInvariance(t, mem, areq, 0, workers), aserial)
			}
			single := runMatrixInvariance(t, mem, standardizeInvarianceRequest("single.pulse", typ), 0, 0)
			if i := firstWordDiff(single.words, aserial.words); i != -1 {
				t.Errorf("archive differs from its single-file twin at word %d", i)
			}
		})
	}
}
