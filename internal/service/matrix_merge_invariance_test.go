package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_merge_invariance_test.go is the U16 E2 gate: the REAL
// MAT_COVARIANCE and MAT_CORRELATION registrations (no test vehicle)
// return the same bits
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
// typ is the operator (MAT_COVARIANCE when empty); pairwise selects
// params.missing "pairwise".
type matrixInvarianceVariant struct {
	weighted    bool
	withVehicle bool
	typ         types.MatrixType
	pairwise    bool
}

// matrixInvarianceVariants: every request shape for every built-in
// matrix operator, listwise and pairwise.
var matrixInvarianceVariants = func() []matrixInvarianceVariant {
	var out []matrixInvarianceVariant
	for _, typ := range types.AllMatrixTypes() {
		for _, pairwise := range []bool{false, true} {
			for _, v := range []matrixInvarianceVariant{{false, false, "", false}, {true, false, "", false}, {false, true, "", false}, {true, true, "", false}} {
				v.typ = typ
				v.pairwise = pairwise
				out = append(out, v)
			}
		}
	}
	return out
}()

func (v matrixInvarianceVariant) matrixType() types.MatrixType {
	if v.typ == "" {
		return types.MAT_COVARIANCE
	}
	return v.typ
}

func (v matrixInvarianceVariant) String() string {
	return fmt.Sprintf("%s_%s_weighted_%v_vehicle_%v", v.matrixType(), v.mode(), v.weighted, v.withVehicle)
}

func (v matrixInvarianceVariant) mode() string {
	if v.pairwise {
		return "pairwise"
	}
	return "listwise"
}

func matrixInvarianceRequest(path string, v matrixInvarianceVariant) *types.Request {
	spec := types.MatrixSpec{Name: "mat", Type: v.matrixType(), Fields: []string{"x1", "x2", "x3"}}
	// Listwise carries max_drop_share 0, so its integer drop count —
	// summed across partitions — rides the compared warnings.
	spec.Params = json.RawMessage(`{"max_drop_share": 0}`)
	if v.pairwise {
		spec.Params = json.RawMessage(`{"missing": "pairwise"}`)
	}
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
// bits (every cell and the determinant), the pairwise N (auxiliary.n),
// the response and matrix warnings, and the run counters.
type matrixRun struct {
	words     []uint64
	auxN      []uint64
	warnings  []*types.ResponseWarning
	matWarns  []*types.ResponseWarning
	run       types.RunComponents
	comps     []types.MatrixComponents
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
	var auxN []uint64
	if n := m.Auxiliary["n"]; n != nil {
		for _, row := range n.Values {
			for _, v := range row {
				auxN = append(auxN, math.Float64bits(v))
			}
		}
	}
	pairwise := strings.Contains(string(req.Matrices[0].Params), "pairwise")
	if (len(auxN) > 0) != pairwise {
		t.Fatalf("auxiliary.n present = %v, pairwise = %v", len(auxN) > 0, pairwise)
	}
	if !pairwise && findWarning(m, errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP) == nil {
		t.Fatalf("listwise variant carries no PULSE_MATRIX_LISTWISE_HEAVY_DROP (warnings %v)", matWarningCodes(m))
	}
	out := matrixRun{words: words, auxN: auxN, warnings: resp.Warnings, matWarns: m.Warnings, instances: stats.instancesWithRows.Load()}
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatalf("Process(decode=%d, shard=%d): no Components.Run", decodeWorkers, shardWorkers)
	}
	out.run = *resp.Components.Run
	out.comps = resp.Components.Matrices
	if len(out.comps) != 1 {
		t.Fatalf("Process(decode=%d, shard=%d): %d Components.Matrices entries, want 1", decodeWorkers, shardWorkers, len(out.comps))
	}
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
	if i := firstWordDiff(got.auxN, want.auxN); i != -1 || len(got.auxN) != len(want.auxN) {
		t.Errorf("%s: auxiliary.n differs from serial at word %d", label, i)
	}
	if !reflect.DeepEqual(got.warnings, want.warnings) {
		t.Errorf("%s: warnings %v, serial %v", label, got.warnings, want.warnings)
	}
	if !reflect.DeepEqual(got.matWarns, want.matWarns) {
		t.Errorf("%s: matrix warnings %v, serial %v", label, got.matWarns, want.matWarns)
	}
	if got.run != want.run {
		t.Errorf("%s: Components.Run %+v, serial %+v", label, got.run, want.run)
	}
	if !reflect.DeepEqual(got.comps, want.comps) {
		t.Errorf("%s: Components.Matrices %s, serial %s", label, mustJSON(t, got.comps), mustJSON(t, want.comps))
	}
}

// referenceMatrixWords folds rows 0..n-1 straight into one listwise
// CoMoment and renders the variant's operator (Cov(1) or Corr) as the
// gate renders the matrix — the correctness anchor (wrong member, wrong
// weight, wrong operator) the engine's blocked result must agree with
// to rounding.
func referenceMatrixWords(t *testing.T, n int, v matrixInvarianceVariant) []uint64 {
	weighted := v.weighted
	t.Helper()
	mode := linalg.Listwise
	if v.pairwise {
		mode = linalg.Pairwise
	}
	c, err := linalg.NewCoMoment(3, mode)
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
	if v.matrixType() == types.MAT_CORRELATION {
		cov = c.Corr()
	}
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

// TestMatrixCovariance_WorkerInvariant is the matrix worker-invariance
// gate, for every built-in operator (MAT_COVARIANCE, MAT_CORRELATION): serial == DecodeWorkers 2/3/7/8 ==
// ShardWorkers 2/3/8 bitwise (every cell and the determinant), on
// cohorts ending mid-block and on a block boundary, weighted and
// unweighted, matrix-only and beside an aggregation; a one-shard
// archive equals its single-file twin bitwise; a multi-shard archive
// equals its single-file twin to tolerance only (block numbering
// restarts per shard — .claude/reference/matrix-and-vectors.md,
// Blocked merge-tree contract). Listwise and pairwise (params.missing),
// the pairwise N (auxiliary.n) and the matrix warnings included.
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

				// Multi-shard archive vs single-file twin: tolerance only
				// (blocks are cut at different places); the bit
				// difference, if any, is logged.
				single := runMatrixInvariance(t, cfg, matrixInvarianceRequest("single.pulse", v), 0, 0)
				assertMatrixCloseToReference(t, single, referenceMatrixWords(t, total, v))
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
		for _, typ := range types.AllMatrixTypes() {
			for _, weighted := range []bool{false, true} {
				alone := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, false, typ, false}), 0, 0)
				beside := runMatrixInvariance(t, cfg, matrixInvarianceRequest("m.pulse", matrixInvarianceVariant{weighted, true, typ, false}), 0, 0)
				if i := firstWordDiff(alone.words, beside.words); i != -1 {
					t.Errorf("%s weighted=%v: matrix-only differs from matrix-beside-aggregation at word %d", typ, weighted, i)
				}
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
