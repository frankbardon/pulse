package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// dictArchiveSchema: four f64 members x0..x3 then a categorical_u32
// tag whose dictionary holds `entries` labels of ~40 bytes. Every shard
// of an archive carries its own copy of that dictionary, so the
// archive's byte length is far above records x stride — the bytes the
// payload term alone used to count.
func dictArchiveSchema(entries int) *encoding.Schema {
	s := &encoding.Schema{}
	for j := range 4 {
		s.Fields = append(s.Fields, encoding.Field{Name: fmt.Sprintf("x%d", j), Type: encoding.FieldTypeF64, ByteOffset: 8 * j, CsvColumnIdx: j})
	}
	dict := encoding.NewDictionary()
	for i := range entries {
		if _, err := dict.Add(fmt.Sprintf("tag-%07d-abcdefghijklmnopqrstuvwxyz", i)); err != nil {
			panic(err)
		}
	}
	s.Fields = append(s.Fields, encoding.Field{Name: "tag", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 32, CsvColumnIdx: 4, Dictionary: dict})
	return s
}

// writeDictArchive writes an uncompressed shard archive of shards
// shards x rowsPerShard records over dictArchiveSchema(entries) to name
// on fs and returns the archive's byte length. Values are the matrix
// scale generator's hash (FMA-free), the tag cycles the dictionary.
func writeDictArchive(t testing.TB, fs afero.Fs, name string, shards, rowsPerShard, entries int) int64 {
	t.Helper()
	schema := dictArchiveSchema(entries)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entry := func(n string) *bufio.Writer {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		return bufio.NewWriter(w)
	}
	w := entry(encx.ReservedSchemaName)
	if err := encx.WriteSchemaDoc(w, schema, uint64(shards*rowsPerShard), uint16(shards)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, schema.RecordByteSize())
	g := uint64(0)
	for s := range shards {
		w := entry(fmt.Sprintf("s%02d.pulse", s))
		if err := encoding.WriteHeader(w); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(w, schema); err != nil {
			t.Fatal(err)
		}
		for range rowsPerShard {
			for j := range 4 {
				binary.LittleEndian.PutUint64(row[8*j:], math.Float64bits(matrixScaleUnit(matrixScaleHash(g, uint64(j)))))
			}
			binary.LittleEndian.PutUint32(row[32:], uint32(g%uint64(entries)))
			if _, err := w.Write(row); err != nil {
				t.Fatal(err)
			}
			g++
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, name, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return int64(buf.Len())
}

// archiveMemoryEstimate is the runtime's own MaxEstimatedMemory figure
// for req on svc — the observed detail of the pre-flight refusal under a
// 1-byte limit — with svc's limits restored afterwards.
func archiveMemoryEstimate(t *testing.T, svc *Service, req *types.Request) int64 {
	t.Helper()
	prev := svc.Limits()
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1
	svc.SetLimits(l)
	defer svc.SetLimits(prev)
	_, err := svc.Process(context.Background(), req)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("estimate probe: %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	return ce.Details["observed"].(int64)
}

// dictArchiveRequest: AGG_SUM over x0, grouped by the tag when grouped
// (mergeable either way, so ShardWorkers > 1 fans out).
func dictArchiveRequest(path string, grouped bool) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x0", Label: "s"}}}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}}
	}
	return req
}

// totalArchiveRequest is dictArchiveRequest with a total() formula sh
// summed beside x0.
func totalArchiveRequest(path string, grouped bool, expr string) *types.Request {
	req := dictArchiveRequest(path, grouped)
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: expr, Label: "sh"}}
	req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "sh", Label: "ssh"})
	return req
}

// TestShardFanOut_PredictMatchesRuntime: the memory estimate's arm
// decision (descx.ShardFanOut, which charges the per-shard partials) is
// the dispatch Service.shouldFanOut takes, for every ShardWorkers cap
// and request shape, on a multi-shard and a one-shard archive.
func TestShardFanOut_PredictMatchesRuntime(t *testing.T) {
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "multi.pulse", 3, 10, 4)
	writeDictArchive(t, mem, "one.pulse", 1, 10, 4)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	reqs := map[string]func(string) *types.Request{
		"sum":     func(p string) *types.Request { return dictArchiveRequest(p, false) },
		"grouped": func(p string) *types.Request { return dictArchiveRequest(p, true) },
		"median (not mergeable)": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p}, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x0", Label: "m"}}}
		},
		"matrix": func(p string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: p}, Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"x0", "x1"}}}}
		},
		// U51 E3-S1: an ungrouped one-layer total() fans out (two-phase);
		// grouped and multi-layer ones stay serial.
		"total": func(p string) *types.Request {
			return totalArchiveRequest(p, false, "x0 / total(x0)")
		},
		"grouped total": func(p string) *types.Request {
			return totalArchiveRequest(p, true, "x0 / total(x0)")
		},
		"multi-layer total": func(p string) *types.Request {
			req := totalArchiveRequest(p, false, "total(x0)")
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(x0 - sh)", Label: "t2"})
			return req
		},
	}
	ctx := context.Background()
	for _, path := range []string{"multi.pulse", "one.pulse"} {
		for name, mk := range reqs {
			for _, workers := range []int{0, 1, 2, 8} {
				svc := New(cfg)
				svc.SetShardWorkers(workers)
				l := limits.Defaults()
				l.MaxEstimatedMemory = 1 << 50
				svc.SetLimits(l)
				cohort, err := svc.Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				in, err := svc.limitInputs(ctx, cohort, path)
				if err != nil {
					t.Fatal(err)
				}
				req := mk(path)
				_, runtimeFans := svc.shouldFanOut(req, cohort)
				if got := descx.ShardFanOut(req, cohort.Schema(), svc.instance, in); got != runtimeFans {
					t.Errorf("%s / %s / ShardWorkers %d: predict fan-out %v, runtime %v", path, name, workers, got, runtimeFans)
				}
			}
		}
	}
}

// TestArchiveMemory_EstimatedBytesRatio (E8-S3): over a shard archive
// MaxEstimatedMemory's estimate bounds the peak heap a run really
// reaches — with ShardWorkers 1 (the serial archive iterator) and 4
// (the per-shard parallel reducer). Two archives: one whose shards each
// carry a 100,000-entry dictionary, so its bytes and schema decodes
// dwarf records x stride (without the archive terms the estimate was
// ~1/5 of its peak), and the matrix scale archive (~100K rows x p = 50, 8
// shards), where the payload dominates — streamed (a pairwise
// covariance) and buffered (an AGG_MEDIAN). The peak is the minimum of
// three sampled passes (measurePeakHeap); the band is loose — the
// estimate is an allocation upper bound, so GC timing moves only how
// far below it the peak lands — and catches a dropped term, not a few
// percent.
func TestArchiveMemory_EstimatedBytesRatio(t *testing.T) {
	if testing.Short() {
		t.Skip("peak-heap measurement")
	}
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "dict.pulse", 4, 4096, 100_000)
	memCfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	const p = 50
	dir := t.TempDir()
	scale := matrixScaleCohort(t, dir, 25*linalg.MergeBlockSize, p, 8)
	osCfg := matrixScaleOSConfig(t, dir)
	cases := []struct {
		name string
		cfg  *fs.Config
		req  func() *types.Request
	}{
		{"dictionary archive", memCfg, func() *types.Request { return dictArchiveRequest("dict.pulse", false) }},
		{"dictionary archive grouped", memCfg, func() *types.Request { return dictArchiveRequest("dict.pulse", true) }},
		{"matrix scale archive", osCfg, func() *types.Request { return matrixScaleRequest(scale, p, "pairwise", false) }},
		// The buffered arm (an order statistic): every record is
		// materialised, so the payload term's headroom over the largest
		// shard alone must still bound the peak.
		{"matrix scale archive buffered", osCfg, func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: scale}, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x001", Label: "m"}}}
		}},
	}
	for _, c := range cases {
		for _, workers := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/shards=%d", c.name, workers), func(t *testing.T) {
				svc := New(c.cfg)
				svc.SetShardWorkers(workers)
				estimate := float64(archiveMemoryEstimate(t, svc, c.req()))
				peak := -1.0
				for range 3 {
					got, err := measurePeakHeap(func() error {
						_, err := svc.Process(context.Background(), c.req())
						return err
					})
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					if peak < 0 || got < peak {
						peak = got
					}
				}
				ratio := peak / estimate
				t.Logf("measured peak %.1f MB, estimate %.1f MB, ratio %.2f", peak/(1<<20), estimate/(1<<20), ratio)
				if ratio < archiveRatioMin || ratio > archiveRatioMax {
					t.Errorf("measured / estimated = %.2f, want within [%.2f, %.2f]", ratio, archiveRatioMin, archiveRatioMax)
				}
			})
		}
	}
}

// The archive ratio band. Measured on darwin/arm64 (go1.26), with the
// schema-decode term at 1 (E8-S5: the scan, like Open's record-count
// peek since E8-S4, reads each shard's schema layout-only, so only the
// canonical entry is decoded with its dictionaries): 0.53-0.57 on the
// dictionary archive, 0.68-0.74 on the matrix scale archive;
// linux/amd64 under docker 0.53-0.59 and 0.65-0.74 (min of 3 each).
// Earlier calibrations: E8-S4 at shards + 1, 0.35-0.49 / 0.66-0.74;
// E8-S3 at 2 x shards + 1, 0.31-0.44 / 0.67-0.74. The estimate bounds
// the peak; the ceiling's 20% over 1 absorbs a sampled overshoot under
// another runner's GC timing. Without the archive terms (descx
// archiveTerms) the dictionary archive measured 3.1-5.4; without the one
// remaining schema decode it measures 0.69-0.76 (the leaf and descriptor
// pins catch that drop exactly); the floor fails a term that
// over-counts by a whole factor.
//
// U51 E1-S1 made every archive read header-only (Open reads the central
// directory, the scan reads each shard through a section — no
// whole-archive copy), while the estimate still charged the archive
// bytes x fileCopyFactor the slurp needed: the matrix scale archive
// measured 0.14 serially (0.23 at 4 workers), the dictionary archive
// 0.17-0.36 — the floor that run lowered to 0.08. U51 E1-S2 re-pinned
// the archive term to the central directory (x 8) plus the LARGEST
// shard's payload x fileCopyFactor (archiveTerms) and added the buffered
// arm (an AGG_MEDIAN over the matrix scale archive): darwin/arm64
// (go1.26, min of 3) 0.42-0.45 dictionary, 0.71-0.80 dictionary
// grouped, 0.50 / 0.82 matrix scale serial / 4 workers, 0.58 buffered.
// Restoring the whole-archive term fails the floor (0.14 matrix scale
// serial).
const (
	archiveRatioMin = 0.3
	archiveRatioMax = 1.2
)

// TestArchiveMemory_DirectoryTermBoundsOpen (U51 E1-S2): the memory
// estimate's directory term bounds what a run over a shard archive
// holds for its central directory — the opened cohort's shard list plus
// the parsed directory the scan keeps (openArchiveFile) — measured as
// the live heap after a GC, on archives of 500 and 4,000 one-record
// shards (the directory dominates; the canonical schema is tiny). The
// ratio band pins directoryCopyFactor: the floor fails a factor
// inflated by 2x, the ceiling one that no longer covers the
// directory.
func TestArchiveMemory_DirectoryTermBoundsOpen(t *testing.T) {
	for _, shards := range []int{500, 4000} {
		t.Run(fmt.Sprintf("shards=%d", shards), func(t *testing.T) {
			mem := afero.NewMemMapFs()
			writeDictArchive(t, mem, "wide.pulse", shards, 1, 2)
			cfg, err := fs.New(fs.WithFs(mem))
			if err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			ctx := context.Background()
			// Two collections each side: a sync.Pool's victim cache
			// (an earlier test's buffers) survives one.
			var before, after runtime.MemStats
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&before)
			cohort, err := svc.Open(ctx, "wide.pulse")
			if err != nil {
				t.Fatal(err)
			}
			a, err := openArchiveFile(mem, "wide.pulse")
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&after)
			resident := float64(int64(after.HeapAlloc) - int64(before.HeapAlloc))
			runtime.KeepAlive(cohort)
			_ = a.Close()
			if cohort.directoryBytes != a.arch.DirectoryBytes() || cohort.directoryBytes <= 0 {
				t.Fatalf("cohort directory bytes %d, archive %d", cohort.directoryBytes, a.arch.DirectoryBytes())
			}
			charged := float64(limits.EstimateMemory(limits.MemoryInputs{DirectoryBytes: cohort.directoryBytes}) - limits.EstimateMemory(limits.MemoryInputs{}))
			ratio := resident / charged
			t.Logf("%d shards: directory %d bytes, resident %.0f, charged %.0f, ratio %.2f", shards, cohort.directoryBytes, resident, charged, ratio)
			if ratio < directoryRatioMin || ratio > directoryRatioMax {
				t.Errorf("resident / charged = %.2f, want within [%.2f, %.2f]", ratio, directoryRatioMin, directoryRatioMax)
			}
		})
	}
}

// The directory band. Measured on darwin/arm64 (go1.26): 0.79 at 500
// shards, 0.76 at 4,000 (~6.1 resident bytes per directory byte: a
// zip.File per entry with its name, the entry table, the by-name map
// and the cohort's shard list). The ceiling is the bound itself; the
// floor fails a directoryCopyFactor doubled (~0.38).
const (
	directoryRatioMin = 0.45
	directoryRatioMax = 1.0
)

// TestMaxEstimatedMemory_RefusesArchiveOverCap (E8-S3, re-pinned U51
// E1-S2): over a shard archive the pre-flight refuses a
// MaxEstimatedMemory one byte under the estimate — on the serial and
// the per-shard parallel arm, with the estimate as observed — and the
// run proceeds at it. The estimate no longer scales with the archive's
// bytes (the archive streams one shard at a time): doubling the shard
// count — every shard carrying its own 20,000-entry dictionary, so the
// archive nearly doubles — moves it only by the larger central
// directory (8 x its extra bytes) plus, on the parallel arm, the extra
// shard partials' buckets.
func TestMaxEstimatedMemory_RefusesArchiveOverCap(t *testing.T) {
	mem := afero.NewMemMapFs()
	small := writeDictArchive(t, mem, "dict.pulse", 4, 512, 20_000)
	large := writeDictArchive(t, mem, "dict8.pulse", 8, 512, 20_000)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	dirBytes := func(name string) int64 {
		a, err := openArchiveFile(mem, name)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		return a.arch.DirectoryBytes()
	}
	dirTerm := limits.EstimateMemory(limits.MemoryInputs{DirectoryBytes: dirBytes("dict8.pulse") - dirBytes("dict.pulse")}) - limits.EstimateMemory(limits.MemoryInputs{})
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("shards=%d", workers), func(t *testing.T) {
			svc := New(cfg)
			svc.SetShardWorkers(workers)
			est := archiveMemoryEstimate(t, svc, dictArchiveRequest("dict.pulse", false))
			l := limits.Defaults()
			l.MaxEstimatedMemory = est - 1
			svc.SetLimits(l)
			_, err := svc.Process(context.Background(), dictArchiveRequest("dict.pulse", false))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("cap %d under the estimate: err = %v, want PULSE_LIMIT_EXCEEDED", l.MaxEstimatedMemory, err)
			}
			if ce.Details["limit"] != "max_estimated_memory" || ce.Details["configured"] != l.MaxEstimatedMemory || ce.Details["observed"] != est {
				t.Fatalf("details = %v", ce.Details)
			}
			l.MaxEstimatedMemory = est
			svc.SetLimits(l)
			if _, err := svc.Process(context.Background(), dictArchiveRequest("dict.pulse", false)); err != nil {
				t.Fatalf("at the estimate (%d): %v", est, err)
			}

			svc.SetLimits(limits.Defaults())
			want := dirTerm
			if workers > 1 {
				// Four more ungrouped shard partials (8 shards, all inside
				// the 4-worker merge window of 8), each a bucket's state:
				// 256 + 256 (U51 E2-S4).
				want += 4 * (256 + 256)
			}
			if d := archiveMemoryEstimate(t, svc, dictArchiveRequest("dict8.pulse", false)) - est; d != want {
				t.Fatalf("8 shards (%d bytes) estimate %d above 4 shards (%d bytes), want %d — the directory term only", large, d, small, want)
			}
		})
	}
}

// TestShardParallel_DecodeAllocsPerRow (E8-S3): the per-shard parallel
// reducer decodes each row into one fresh positional Record (the
// parallel-decode reducer's decode), not three maps and a converted
// Record — ~58 allocations a row at p = 50 before, ~3 after. The bound
// leaves room for the run's fixed allocations and the runtime's own.
func TestShardParallel_DecodeAllocsPerRow(t *testing.T) {
	const p, rows = 50, 4 * linalg.MergeBlockSize
	dir := t.TempDir()
	path := matrixScaleCohort(t, dir, rows, p, 4)
	svc := New(matrixScaleOSConfig(t, dir))
	svc.SetShardWorkers(4)
	req := &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x000", Label: "s"}}}
	if _, ok := svc.shouldFanOut(req, mustOpen(t, svc, path)); !ok {
		t.Fatal("the request does not take the per-shard parallel arm")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := svc.Process(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	perRow := float64(after.Mallocs-before.Mallocs) / rows
	t.Logf("%.1f allocations a row", perRow)
	if perRow > 10 {
		t.Fatalf("%.1f allocations a row on the per-shard parallel arm, want <= 10 (a positional Record per row)", perRow)
	}
}

func mustOpen(t *testing.T, svc *Service, path string) *Cohort {
	t.Helper()
	c, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestShardScan_SkipsShardDictionaries (E8-S5): the scan reads each
// shard's schema block only to reach its first record — the canonical
// schema decodes every row — so it reads that block LAYOUT-ONLY
// (encx.ReadSchemaLayout), never allocating the shard's dictionaries,
// on the serial archive iterator (shardIter.openShard) and the
// per-shard parallel reducer (processOneShard) alike. Over an archive
// whose four shards each carry a 100,000-entry dictionary a run then
// allocates about one dictionary decode (Open's canonical entry) plus
// the archive bytes Open and the scan read, where a full decode per
// shard added four more.
func TestShardScan_SkipsShardDictionaries(t *testing.T) {
	const shards, entries = 4, 100_000
	mem := afero.NewMemMapFs()
	size := writeDictArchive(t, mem, "dict.pulse", shards, 256, entries)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	var block bytes.Buffer
	if err := encoding.WriteSchema(&block, dictArchiveSchema(entries)); err != nil {
		t.Fatal(err)
	}
	decode := totalAlloc(t, func() error {
		_, err := encoding.ReadSchema(bytes.NewReader(block.Bytes()), encoding.FormatVersion)
		return err
	})
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("shards=%d", workers), func(t *testing.T) {
			svc := New(cfg)
			svc.SetShardWorkers(workers)
			req := dictArchiveRequest("dict.pulse", false)
			if _, fan := svc.shouldFanOut(req, mustOpen(t, svc, "dict.pulse")); fan != (workers > 1) {
				t.Fatalf("ShardWorkers %d: per-shard parallel arm = %v", workers, fan)
			}
			got := totalAlloc(t, func() error {
				_, err := svc.Process(context.Background(), req)
				return err
			})
			bound := 2*decode + 3*uint64(size)
			t.Logf("run allocates %.1f MB; one dictionary decode %.1f MB, archive %.1f MB", float64(got)/(1<<20), float64(decode)/(1<<20), float64(size)/(1<<20))
			if got > bound {
				t.Fatalf("run allocates %d bytes, want <= 2 dictionary decodes + 3 x archive = %d (a shard schema decoded with its dictionary)", got, bound)
			}
		})
	}
}

// totalAlloc is the bytes fn allocates (TotalAlloc delta), failing t
// on fn's error.
func totalAlloc(t *testing.T, fn func() error) uint64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
