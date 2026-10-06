package service

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_merge_invariance_test.go is the U16 E2 gate: the REAL
// MAT_COVARIANCE registration (no test vehicle) returns the same bits
// serially and under every DecodeWorkers / ShardWorkers count — the
// TestCoMomentMergeTree_WorkerInvariant contract carried through the
// production matrix slot. It reuses that test's cohort fixtures
// (coMomentSchema / coMomentRows / coMomentArchive).
//
// Engagement proof: the "with_vehicle" variant carries the vehicle
// aggregator beside the matrix, whose instance count shows the parallel
// arm really ran; the matrix-only variant (no aggregation at all) is
// asserted admitted by the merge gate and bit-equal to it.

// matrixInvarianceVariant is one request shape of the gate.
type matrixInvarianceVariant struct {
	weighted    bool
	withVehicle bool
}

var matrixInvarianceVariants = []matrixInvarianceVariant{
	{false, false}, {true, false}, {false, true}, {true, true},
}

func (v matrixInvarianceVariant) String() string {
	return fmt.Sprintf("weighted_%v_vehicle_%v", v.weighted, v.withVehicle)
}

func matrixInvarianceRequest(path string, v matrixInvarianceVariant) *types.Request {
	spec := types.MatrixSpec{Name: "cov", Type: types.MAT_COVARIANCE, Fields: []string{"x1", "x2", "x3"}}
	if v.weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: path},
		Matrices: []types.MatrixSpec{spec},
	}
	if v.withVehicle {
		req.Aggregations = coMomentRequest(path, "listwise", false).Aggregations
	}
	return req
}

// matrixRun is everything a parallel arm could get wrong: the matrix
// bits (every cell and the determinant), the warnings, and the run
// counters.
type matrixRun struct {
	words     []uint64
	warnings  []*types.ResponseWarning
	run       types.RunComponents
	instances int64
}

func runMatrixInvariance(t *testing.T, cfg *fs.Config, req *types.Request, decodeWorkers, shardWorkers int) matrixRun {
	t.Helper()
	stats := &vehicleStats{}
	svc := New(cfg)
	svc.SetExtensions(coMomentVehicleRegistry(stats))
	svc.SetDecodeWorkers(decodeWorkers)
	svc.SetShardWorkers(shardWorkers)
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process(decode=%d, shard=%d): %v", decodeWorkers, shardWorkers, err)
	}
	if len(resp.Matrices) != 1 || resp.Matrices[0].Primary == nil {
		t.Fatalf("Process(decode=%d, shard=%d): %d matrices, want 1", decodeWorkers, shardWorkers, len(resp.Matrices))
	}
	m := resp.Matrices[0]
	var words []uint64
	for _, row := range m.Primary.Values {
		for _, v := range row {
			words = append(words, math.Float64bits(v))
		}
	}
	words = append(words, math.Float64bits(m.Scalars["determinant"]))
	out := matrixRun{words: words, warnings: resp.Warnings, instances: stats.instancesWithRows.Load()}
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatalf("Process(decode=%d, shard=%d): no Components.Run", decodeWorkers, shardWorkers)
	}
	out.run = *resp.Components.Run
	if len(req.Aggregations) == 0 && len(resp.Data) != 0 {
		t.Fatalf("matrix-only request emitted %d data rows, want 0", len(resp.Data))
	}
	return out
}

// assertMatrixRunsEqual: matrix bits exact, warnings and run counters
// equal.
func assertMatrixRunsEqual(t *testing.T, label string, got, want matrixRun) {
	t.Helper()
	if i := firstWordDiff(got.words, want.words); i != -1 {
		t.Errorf("%s: matrix differs from serial at word %d", label, i)
	}
	if !reflect.DeepEqual(got.warnings, want.warnings) {
		t.Errorf("%s: warnings %v, serial %v", label, got.warnings, want.warnings)
	}
	if got.run != want.run {
		t.Errorf("%s: Components.Run %+v, serial %+v", label, got.run, want.run)
	}
}

// referenceCovarianceWords folds rows 0..n-1 straight into one listwise
// CoMoment and renders Cov(1) as the gate renders the matrix — the
// correctness anchor (wrong member, wrong weight) the engine's blocked
// result must agree with to rounding.
func referenceCovarianceWords(t *testing.T, n int, weighted bool) []uint64 {
	t.Helper()
	c, err := linalg.NewCoMoment(3, linalg.Listwise)
	if err != nil {
		t.Fatal(err)
	}
	recs, nullAt := coMomentRows(n, 0)
	read := func(r, f int) float64 {
		if nullAt(r, f) {
			return math.NaN()
		}
		return math.Float64frombits(recs[r][f])
	}
	for r := range recs {
		w := 1.0
		if weighted {
			w = read(r, 3)
		}
		c.Add([]float64{read(r, 0), read(r, 1), read(r, 2)}, w)
	}
	cov := c.Cov(1)
	var words []uint64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			words = append(words, math.Float64bits(cov.At(i, j)))
		}
	}
	return words
}

func assertMatrixCloseToReference(t *testing.T, got matrixRun, ref []uint64) {
	t.Helper()
	cells := got.words[:len(got.words)-1] // drop the determinant
	if len(cells) != len(ref) {
		t.Fatalf("matrix has %d cells, reference %d", len(cells), len(ref))
	}
	for i := range ref {
		x, y := math.Float64frombits(cells[i]), math.Float64frombits(ref[i])
		if d := math.Abs(x - y); !(d <= 1e-9*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))) {
			t.Errorf("cell %d: %v, reference %v", i, x, y)
		}
	}
}

// TestMatrixCovariance_WorkerInvariant is the MAT_COVARIANCE
// worker-invariance gate: serial == DecodeWorkers 2/3/7/8 ==
// ShardWorkers 2/3/8 bitwise (every cell and the determinant), on
// cohorts ending mid-block and on a block boundary, weighted and
// unweighted, matrix-only and beside an aggregation; a one-shard
// archive equals its single-file twin bitwise; a multi-shard archive
// equals its single-file twin to tolerance only (block numbering
// restarts per shard — .claude/reference/matrix-and-vectors.md,
// Blocked merge-tree contract). Listwise only: pairwise lands with
// E3-S2 and extends this gate.
func TestMatrixCovariance_WorkerInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record matrix worker-invariance gate in -short mode")
	}
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
					assertMatrixCloseToReference(t, serial, referenceCovarianceWords(t, n, v.weighted))
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
				assertMatrixCloseToReference(t, serial, referenceCovarianceWords(t, total, v.weighted))
				for _, workers := range []int{2, 3, 8} {
					got := runMatrixInvariance(t, cfg, req, 0, workers)
					if v.withVehicle && got.instances != int64(len(shardRows)) {
						t.Fatalf("ShardWorkers=%d fed %d instances — the shard reducer did not engage", workers, got.instances)
					}
					assertMatrixRunsEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), got, serial)
				}

				// Multi-shard archive vs single-file twin: tolerance only
				// (blocks are cut at different places); the bit
				// difference, if any, is logged.
				single := runMatrixInvariance(t, cfg, matrixInvarianceRequest("single.pulse", v), 0, 0)
				assertMatrixCloseToReference(t, single, referenceCovarianceWords(t, total, v.weighted))
				if i := firstWordDiff(single.words, serial.words); i != -1 {
					t.Logf("caveat observed: archive vs single file differ from word %d", i)
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
		for _, weighted := range []bool{false, true} {
			alone := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, false}), 0, 0)
			beside := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, true}), 0, 0)
			if i := firstWordDiff(alone.words, beside.words); i != -1 {
				t.Errorf("weighted=%v: matrix-only differs from matrix-beside-aggregation at word %d", weighted, i)
			}
		}
	})
}

// TestMatrixOnly_RunComponents: a matrix-only request (no aggregation,
// no grouper) has no primary null field, so Run.NullRecords is 0 on the
// serial and both parallel arms alike, and the record counters are the
// cohort's — the parallel reducers must not assume a first aggregator.
func TestMatrixOnly_RunComponents(t *testing.T) {
	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	shardRows := []int{B + 3, 700, 2 * B}
	total := 0
	for _, n := range shardRows {
		total += n
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := matrixInvarianceRequest("archive.pulse", matrixInvarianceVariant{})
	want := types.RunComponents{TotalRecords: int64(total), FilteredRecords: int64(total), ShardCount: len(shardRows)}
	for _, workers := range []int{1, 3} {
		got := runMatrixInvariance(t, cfg, req, 0, workers)
		if got.run != want {
			t.Errorf("ShardWorkers=%d: Components.Run %+v, want %+v", workers, got.run, want)
		}
	}
}
