//go:build heavy

package service

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMatrixCovariance_WorkerInvariant is the matrix worker-invariance
// gate, for every built-in operator (MAT_COVARIANCE, MAT_CORRELATION): serial == DecodeWorkers 2/3/7/8 ==
// ShardWorkers 2/3/8 bitwise (every cell and the determinant), on
// cohorts ending mid-block and on a block boundary, weighted and
// unweighted, matrix-only and beside an aggregation; a one-shard
// archive and a multi-shard one (shard edges inside merge blocks) equal
// their single-file twin bitwise (blocks key by the global record
// ordinal — .claude/reference/matrix-and-vectors/linalg.md, Blocked
// merge-tree contract). Listwise and pairwise (params.missing),
// the pairwise N (auxiliary.n) and the matrix warnings included.
func TestMatrixCovariance_WorkerInvariant(t *testing.T) {
	schema := coMomentSchema()
	B := linalg.MergeBlockSize

	for _, v := range matrixInvarianceVariants {
		if !v.withVehicle && !processing.CanMergeRequest(matrixInvarianceRequest("x.pulse", v), schema) {
			t.Fatalf("%s: the merge gate refuses a matrix-only request", v)
		}
	}

	t.Run("DecodeWorkers", func(t *testing.T) {
		dir := t.TempDir()
		osFs := afero.NewOsFs()
		cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
		if err != nil {
			t.Fatalf("fs.New: %v", err)
		}
		for _, n := range []int{25 * B, 25*B + 2777} {
			if n < parallelDecodeRecordThreshold {
				t.Fatalf("cohort of %d records is below the parallel-decode threshold", n)
			}
			recs, nullAt := coMomentRows(n, 0)
			path := fmt.Sprintf("%s/matrix_%d.pulse", dir, n)
			if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			for _, v := range matrixInvarianceVariants {
				t.Run(fmt.Sprintf("n%d_%s", n, v), func(t *testing.T) {
					req := matrixInvarianceRequest(path, v)
					serial := runMatrixInvariance(t, cfg, req, 1, 0)
					assertMatrixCloseToReference(t, serial, referenceMatrixWords(t, n, v))
					for _, workers := range []int{2, 3, 7, 8} {
						got := runMatrixInvariance(t, cfg, req, workers, 0)
						if v.withVehicle && got.instances != int64(workers) {
							t.Fatalf("DecodeWorkers=%d fed %d instances — the parallel reducer did not engage", workers, got.instances)
						}
						assertMatrixRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), got, serial)
					}
				})
			}
		}
	})

	t.Run("ShardWorkers", func(t *testing.T) {
		shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
		total := 0
		for _, n := range shardRows {
			total += n
		}
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		recs, nullAt := coMomentRows(total, 0)
		if err := afero.WriteFile(cfg.Fs(), "single.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range matrixInvarianceVariants {
			t.Run(v.String(), func(t *testing.T) {
				req := matrixInvarianceRequest("archive.pulse", v)
				serial := runMatrixInvariance(t, cfg, req, 0, 1)
				assertMatrixCloseToReference(t, serial, referenceMatrixWords(t, total, v))
				for _, workers := range []int{2, 3, 8} {
					got := runMatrixInvariance(t, cfg, req, 0, workers)
					if v.withVehicle && got.instances != int64(len(shardRows)) {
						t.Fatalf("ShardWorkers=%d fed %d instances — the shard reducer did not engage", workers, got.instances)
					}
					assertMatrixRunsEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), got, serial)
				}

				// Multi-shard archive vs single-file twin: blocks key by
				// the global record ordinal, so the same bits.
				single := runMatrixInvariance(t, cfg, matrixInvarianceRequest("single.pulse", v), 0, 0)
				if i := firstWordDiff(single.words, serial.words); i != -1 {
					t.Errorf("archive differs from its single-file twin at word %d", i)
				}
			})
		}
	})

	t.Run("OneShardArchiveMatchesSingleFile", func(t *testing.T) {
		n := 3*B + 55
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "one.pulse", coMomentArchive(t, schema, []int{n}), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		recs, nullAt := coMomentRows(n, 0)
		if err := afero.WriteFile(cfg.Fs(), "twin.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range matrixInvarianceVariants {
			a := runMatrixInvariance(t, cfg, matrixInvarianceRequest("one.pulse", v), 0, 0)
			b := runMatrixInvariance(t, cfg, matrixInvarianceRequest("twin.pulse", v), 0, 0)
			if i := firstWordDiff(a.words, b.words); i != -1 {
				t.Errorf("%s: one-shard archive differs from its single file at word %d", v, i)
			}
		}
	})

	t.Run("MatrixOnlyEqualsBesideAggregation", func(t *testing.T) {
		// The matrix does not depend on what else the request carries.
		cfg := fs.NewMemMap()
		n := 2*B + 9
		recs, nullAt := coMomentRows(n, 0)
		if err := afero.WriteFile(cfg.Fs(), "m.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, typ := range types.AllMatrixTypes() {
			for _, weighted := range []bool{false, true} {
				if weighted && typ == types.MAT_SEM {
					continue // frequency only; these weights are fractional
				}
				alone := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, false, typ, false}), 0, 0)
				beside := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, true, typ, false}), 0, 0)
				if i := firstWordDiff(alone.words, beside.words); i != -1 {
					t.Errorf("%s weighted=%v: matrix-only differs from matrix-beside-aggregation at word %d", typ, weighted, i)
				}
			}
		}
	})
}
