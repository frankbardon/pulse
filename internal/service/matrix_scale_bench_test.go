package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Matrix-at-scale benchmarks (matrix-extensions-hardening E8-S1, TODO
// #157). Never run in CI: CI runs no benchmarks.
//
//   - BenchmarkMatrixScale: MAT_COVARIANCE listwise and pairwise, plus
//     the vector aggregates AGG_VEC_MEAN / AGG_VEC_SD over the same
//     members, ungrouped. Reports ns/op, peak-heap-MB (measurePeakHeap,
//     an untimed extra pass) and, for a matrix, predict's
//     estimated_bytes as pred-state-MB beside it.
//   - BenchmarkMatrixScaleGrouped: the pairwise covariance per seg
//     bucket (4 buckets) — four times the merge-block state.
//   - BenchmarkMatrixScaleWorkers: the pairwise covariance swept over
//     DecodeWorkers on the single file and ShardWorkers on an 8-shard
//     archive of the same rows (the results are bit-identical across the
//     sweep — TestMatrixGrouped_WorkerInvariant — so only time moves).
//
// Tiers. `make bench` runs the default tier, 1M rows × p = 50. The
// opt-in `make bench-scale` adds 1M × p = 256, 10M × p = 50 and
// 10M × p = 256 (-matrix-scale; ~21 GB for the largest single file,
// ~55 GB for every tier with its 8-shard archive, and tens of minutes
// for the pairwise 10M × 256 cases).
//
// Cohorts are stream-generated row by row (never held in memory — 10M ×
// 256 f64 is ~20 GB) and cached under $PULSE_BENCH_DIR (default
// <os.TempDir()>/pulse-bench), keyed by rows, p, shard count and a
// generator version, so only the first run pays for the write. Values
// are a deterministic hash of (row, column); every fourth column is
// nullable and null on ~1/1024 of rows, so the listwise drop stays a few
// percent at p = 256 and pairwise has real per-pair counts.
//
// Baseline: docs/internals/matrix-bench-baseline.txt (benchstat input),
// indexed from docs/internals/BENCHMARKS_BASELINE.md.

// matrixScaleAll opts into the large tiers (make bench-scale passes it).
var matrixScaleAll = flag.Bool("matrix-scale", false, "run the 1M x p256 and 10M matrix scale tiers (make bench-scale)")

// matrixScaleGenVersion keys the cache: bump it when the generator's
// values or layout change so a stale cohort is never reused.
const matrixScaleGenVersion = 1

type matrixScaleTier struct {
	rows, p int
}

func (t matrixScaleTier) String() string {
	return fmt.Sprintf("rows=%dM/p=%d", t.rows/1_000_000, t.p)
}

func matrixScaleTiers() []matrixScaleTier {
	tiers := []matrixScaleTier{{1_000_000, 50}}
	if *matrixScaleAll {
		tiers = append(tiers, matrixScaleTier{1_000_000, 256}, matrixScaleTier{10_000_000, 50}, matrixScaleTier{10_000_000, 256})
	}
	return tiers
}

// matrixScaleDir is the cohort cache: $PULSE_BENCH_DIR, else a stable
// directory under the OS temp dir.
func matrixScaleDir(tb testing.TB) string {
	tb.Helper()
	dir := os.Getenv("PULSE_BENCH_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "pulse-bench")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatalf("bench dir %s: %v", dir, err)
	}
	return dir
}

// matrixScaleMembers names the p f64 members x000 … x(p-1).
func matrixScaleMembers(p int) []string {
	out := make([]string, p)
	for j := range out {
		out[j] = fmt.Sprintf("x%03d", j)
	}
	return out
}

// matrixScaleSchema: p f64 members (every fourth nullable) then a
// four-level categorical seg (the grouped bench's grouper).
func matrixScaleSchema(p int) *encoding.Schema {
	s := &encoding.Schema{}
	off := 0
	for j, name := range matrixScaleMembers(p) {
		s.Fields = append(s.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, ByteOffset: off, CsvColumnIdx: j, Nullable: j%4 == 0})
		off += 8
	}
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := dict.Add(v); err != nil {
			panic(err)
		}
	}
	s.Fields = append(s.Fields, encoding.Field{Name: "seg", Type: encoding.FieldTypeCategoricalU8, ByteOffset: off, CsvColumnIdx: p, Dictionary: dict})
	return s
}

// matrixScaleHash is splitmix64 over (row, column): the generator's one
// source of values, so any tier is reproducible from its row index.
func matrixScaleHash(row, col uint64) uint64 {
	z := row*0x9E3779B97F4A7C15 + col*0xD1B54A32D192ED03 + 0x632BE59BD9B4E019
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// matrixScaleUnit maps a hash to [-1, 1).
func matrixScaleUnit(h uint64) float64 {
	return float64(h>>11)/(1<<52) - 1
}

// matrixScaleRow encodes global row g into buf (the schema's stride:
// p × 8 value bytes, the seg byte, the null bitmap). A shared factor
// gives the members a real correlation; the products are explicit
// float64 barriers so arm64 FMA fusion cannot move the inputs.
func matrixScaleRow(buf []byte, g uint64, p, bitmapOff int) {
	clear(buf[bitmapOff:])
	f := matrixScaleUnit(matrixScaleHash(g, math.MaxUint32))
	for j := range p {
		h := matrixScaleHash(g, uint64(j))
		v := float64(0.6*f) + float64(0.8*matrixScaleUnit(h))
		binary.LittleEndian.PutUint64(buf[8*j:], math.Float64bits(v))
		if j%4 == 0 && h>>54 == 0 { // ~1/1024 of rows
			encoding.BitmapSetNull(buf[bitmapOff:], j)
		}
	}
	buf[8*p] = byte(g % 4)
}

// writeMatrixScaleShard streams header + schema + rows [from, from+n).
func writeMatrixScaleShard(w *bufio.Writer, schema *encoding.Schema, p int, from, n uint64) error {
	if err := encoding.WriteHeader(w); err != nil {
		return err
	}
	if err := encoding.WriteSchema(w, schema); err != nil {
		return err
	}
	bitmapOff := 8*p + 1
	row := make([]byte, bitmapOff+schema.BitmapByteSize())
	for g := from; g < from+n; g++ {
		matrixScaleRow(row, g, p, bitmapOff)
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Flush()
}

// matrixScaleCohort returns the cached cohort for (rows, p, shards) —
// shards 0 is a single file, else an uncompressed shard archive of the
// same rows split evenly — generating it first if absent. The write
// goes to a temp name and is renamed into place, so an interrupted run
// never leaves a truncated cohort behind.
func matrixScaleCohort(tb testing.TB, dir string, rows, p, shards int) string {
	tb.Helper()
	path := filepath.Join(dir, fmt.Sprintf("matrix_scale_v%d_r%d_p%d_s%d.pulse", matrixScaleGenVersion, rows, p, shards))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer os.Remove(tmp.Name())
	schema := matrixScaleSchema(p)
	bw := bufio.NewWriterSize(tmp, 4<<20)
	if shards == 0 {
		err = writeMatrixScaleShard(bw, schema, p, 0, uint64(rows))
	} else {
		err = writeMatrixScaleArchive(bw, schema, p, rows, shards)
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		tb.Fatalf("generate %s: %v", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		tb.Fatalf("rename %s: %v", path, err)
	}
	return path
}

func writeMatrixScaleArchive(bw *bufio.Writer, schema *encoding.Schema, p, rows, shards int) error {
	zw := zip.NewWriter(bw)
	entry := func(name string) (*bufio.Writer, error) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		return bufio.NewWriterSize(w, 4<<20), nil
	}
	w, err := entry(encx.ReservedSchemaName)
	if err != nil {
		return err
	}
	if err := encx.WriteSchemaDoc(w, schema, uint64(rows), uint16(shards)); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	from := uint64(0)
	for i := range shards {
		n := uint64(rows / shards)
		if i < rows%shards {
			n++
		}
		if w, err = entry(fmt.Sprintf("s%02d.pulse", i)); err != nil {
			return err
		}
		if err := writeMatrixScaleShard(w, schema, p, from, n); err != nil {
			return err
		}
		from += n
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return bw.Flush()
}

// matrixScaleOSConfig mounts the cache directory read-through.
func matrixScaleOSConfig(tb testing.TB, dir string) *fs.Config {
	tb.Helper()
	cfg, err := fs.New(fs.WithFs(afero.NewOsFs()), fs.WithDataDir(dir))
	if err != nil {
		tb.Fatalf("fs.New: %v", err)
	}
	return cfg
}

// matrixScaleRequest is one benchmarked shape over the cohort at path:
// a MAT_COVARIANCE (missing "listwise" / "pairwise") or a vector
// aggregate (AGG_VEC_MEAN / AGG_VEC_SD) over every member, grouped by
// seg when grouped. Rebuilt per call: Process normalises in place.
func matrixScaleRequest(path string, p int, op string, grouped bool) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: path}}
	members := matrixScaleMembers(p)
	switch op {
	case "listwise", "pairwise":
		req.Matrices = []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Fields: members,
			Params: json.RawMessage(fmt.Sprintf(`{"missing": %q}`, op))}}
	default:
		req.Aggregations = []*types.Aggregation{{Type: types.AggregationType(op), Fields: members, Label: "vec"}}
	}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}}
	}
	return req
}

// matrixScalePredictedBytes is predict's estimated_bytes for the
// request's one matrix over a single-file cohort (header + schema only;
// predict never reads a record).
func matrixScalePredictedBytes(tb testing.TB, req *types.Request) int64 {
	tb.Helper()
	f, err := os.Open(req.Cohort.Filename)
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	defer f.Close()
	env := descx.Predict(f, req, &descx.PredictOptions{})
	if len(env.Errors) != 0 {
		tb.Fatalf("predict errors: %+v", *env.Errors[0])
	}
	ms := env.Data.(*descriptor.PredictResult).Matrices
	if len(ms) != 1 || ms[0].EstimatedBytes == nil {
		tb.Fatalf("predict reports no matrix estimate: %+v", ms)
	}
	return *ms[0].EstimatedBytes
}

// runMatrixScale times Process over b.Loop, then reports the peak heap
// of one untimed pass and, for a matrix, the predicted state.
func runMatrixScale(b *testing.B, svc *Service, single bool, mk func() *types.Request) {
	b.Helper()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := svc.Process(ctx, mk()); err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
	b.StopTimer()
	reportPeakHeap(b, func() error {
		_, err := svc.Process(ctx, mk())
		return err
	})
	// Predict reads an archive whole (sniffArchive), so the predicted
	// figure is reported for single-file runs only.
	if req := mk(); len(req.Matrices) == 1 && single {
		b.ReportMetric(float64(matrixScalePredictedBytes(b, req))/(1024*1024), "pred-state-MB")
	}
}

// BenchmarkMatrixScale: ungrouped covariance (listwise, pairwise) and
// the vector aggregates over p members, per tier.
func BenchmarkMatrixScale(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	for _, tier := range matrixScaleTiers() {
		path := matrixScaleCohort(b, dir, tier.rows, tier.p, 0)
		for _, op := range []string{"listwise", "pairwise", string(types.AGG_VEC_MEAN), string(types.AGG_VEC_SD)} {
			b.Run(tier.String()+"/"+op, func(b *testing.B) {
				runMatrixScale(b, New(cfg), true, func() *types.Request { return matrixScaleRequest(path, tier.p, op, false) })
			})
		}
	}
}

// BenchmarkMatrixScaleGrouped: the pairwise covariance per seg bucket.
func BenchmarkMatrixScaleGrouped(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	for _, tier := range matrixScaleTiers() {
		path := matrixScaleCohort(b, dir, tier.rows, tier.p, 0)
		b.Run(tier.String()+"/pairwise_by_seg", func(b *testing.B) {
			runMatrixScale(b, New(cfg), true, func() *types.Request { return matrixScaleRequest(path, tier.p, "pairwise", true) })
		})
	}
}

// matrixScaleShards is the archive the ShardWorkers sweep reads.
const matrixScaleShards = 8

// BenchmarkMatrixScaleWorkers: the pairwise covariance swept over
// DecodeWorkers (single file) and ShardWorkers (8-shard archive).
func BenchmarkMatrixScaleWorkers(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	for _, tier := range matrixScaleTiers() {
		single := matrixScaleCohort(b, dir, tier.rows, tier.p, 0)
		archive := matrixScaleCohort(b, dir, tier.rows, tier.p, matrixScaleShards)
		for _, workers := range []int{1, 2, 4, 8} {
			b.Run(fmt.Sprintf("%s/decode=%d", tier, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetDecodeWorkers(workers)
				runMatrixScale(b, svc, true, func() *types.Request { return matrixScaleRequest(single, tier.p, "pairwise", false) })
			})
			b.Run(fmt.Sprintf("%s/shards=%d", tier, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetShardWorkers(workers)
				runMatrixScale(b, svc, false, func() *types.Request { return matrixScaleRequest(archive, tier.p, "pairwise", false) })
			})
		}
	}
}

// BenchmarkArchiveWorkerScaling (U51 E2-S2, FR-22): a projected scalar
// request — AGG_SUM and AGG_AVERAGE over two of the p members, grouped
// by seg — swept over ShardWorkers 1/2/4/8 on the 8-shard archive and
// DecodeWorkers 1/2/4/8 on the single file of the same rows. Unlike the
// pairwise covariance above (every member), it decodes three of the
// p + 1 fields, so it measures the per-shard arm's projected decode and
// worker scaling rather than the co-moment fold. Reports ns/op and
// peak-heap-MB; figures in docs/internals/BENCHMARKS_BASELINE.md and
// docs/src/library/tuning-limits.md.
func BenchmarkArchiveWorkerScaling(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	for _, tier := range matrixScaleTiers() {
		single := matrixScaleCohort(b, dir, tier.rows, tier.p, 0)
		archive := matrixScaleCohort(b, dir, tier.rows, tier.p, matrixScaleShards)
		members := matrixScaleMembers(tier.p)
		mk := func(path string) func() *types.Request {
			return func() *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: path},
					Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "seg"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: members[0], Label: "s"},
						{Type: types.AGG_AVERAGE, Field: members[1], Label: "a"},
					}}
			}
		}
		for _, workers := range []int{1, 2, 4, 8} {
			b.Run(fmt.Sprintf("%s/shards=%d", tier, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetProjectBufferedFields(true)
				svc.SetShardWorkers(workers)
				runMatrixScale(b, svc, false, mk(archive))
			})
			b.Run(fmt.Sprintf("%s/decode=%d", tier, workers), func(b *testing.B) {
				svc := New(cfg)
				svc.SetProjectBufferedFields(true)
				svc.SetDecodeWorkers(workers)
				runMatrixScale(b, svc, true, mk(single))
			})
		}
	}
}

// TestMatrixScaleCohort_MatchesReferenceWriter: the streaming generator
// lays rows down exactly as the reference writer (writeNullablePulse)
// does, and its archive holds the same rows — so the benchmarks read
// real, decodable cohorts.
func TestMatrixScaleCohort_MatchesReferenceWriter(t *testing.T) {
	t.Parallel()

	const rows, p = 37, 9
	dir := t.TempDir()
	got, err := os.ReadFile(matrixScaleCohort(t, dir, rows, p, 0))
	if err != nil {
		t.Fatal(err)
	}
	schema := matrixScaleSchema(p)
	recs := make([][]uint64, rows)
	nulls := map[[2]int]bool{}
	bitmapOff := 8*p + 1
	buf := make([]byte, bitmapOff+schema.BitmapByteSize())
	for r := range recs {
		matrixScaleRow(buf, uint64(r), p, bitmapOff)
		for j := range p {
			recs[r] = append(recs[r], binary.LittleEndian.Uint64(buf[8*j:]))
			nulls[[2]int{r, j}] = buf[bitmapOff+j/8]&(1<<(j%8)) != 0
		}
		recs[r] = append(recs[r], uint64(r%4))
	}
	want := writeNullablePulse(t, schema, recs, func(r, f int) bool { return nulls[[2]int{r, f}] })
	if !bytes.Equal(got, want) {
		t.Fatalf("generated cohort (%d bytes) differs from the reference writer (%d bytes)", len(got), len(want))
	}

	// The archive is the same rows: a pairwise covariance over it equals
	// the single file's bit for bit.
	cfg := matrixScaleOSConfig(t, dir)
	archive := matrixScaleCohort(t, dir, rows, p, 3)
	one := processMatrices2(t, cfg, matrixScaleRequest(filepath.Join(dir, fmt.Sprintf("matrix_scale_v%d_r%d_p%d_s0.pulse", matrixScaleGenVersion, rows, p)), p, "pairwise", false))
	sharded := processMatrices2(t, cfg, matrixScaleRequest(archive, p, "pairwise", false))
	a, b := mustJSON(t, one.Matrices), mustJSON(t, sharded.Matrices)
	if a != b {
		t.Fatalf("archive covariance %s, single file %s", b, a)
	}
}

// The ratio bands, by missing mode. Measured on darwin/arm64 (go1.26,
// DecodeWorkers 1): pairwise peak / predicted ≈ 1.5 (8.8 MB of state,
// decode buffers and garbage on top); listwise ≈ 3.4, its 1 MB of state
// small beside the same fixed overhead. The floor is below 1 because a
// sampled peak can only under-read the live state, never invent it; the
// ceilings leave GC-timing headroom yet fail an estimate that drops the
// 25 merge blocks (ratio ≈ 37) or the 4 buckets (≈ 6 pairwise).
var (
	matrixScaleRatioMin = map[string]float64{"listwise": 0.8, "pairwise": 0.8}
	matrixScaleRatioMax = map[string]float64{"listwise": 8, "pairwise": 3}
)
