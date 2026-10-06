package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// This file is the proof vehicle for processing.BlockMerger: a
// CoMoment-wrapping reducer that exists ONLY here — no registry,
// manifest, skill or feature entry. It is wired through the service's
// own extension registry (Service.SetExtensions), the internal hook the
// root facade fills from pulse.Options.Extensions; the public extend
// adapter cannot carry the opt-in without growing the frozen extend
// surface, which this unit must not do.

const coMomentVehicle types.AggregationType = "AGG_TEST_BLOCK_COMOMENT"

type coMomentVehicleParams struct {
	Fields []string `json:"fields"`
	Mode   string   `json:"mode"`
	Weight string   `json:"weight"`
}

// vehicleStats counts the aggregator instances that saw at least one
// row: 1 on a serial run, one per non-empty segment / shard on a
// parallel one — the proof the parallel arm actually engaged.
type vehicleStats struct{ instancesWithRows atomic.Int64 }

type coMomentAgg struct {
	params coMomentVehicleParams
	blocks *processing.BlockCoMoments
	x      []float64
	rows   int
	root   *linalg.CoMoment
	stats  *vehicleStats
}

func (a *coMomentAgg) BlockMoments() *processing.BlockCoMoments { return a.blocks }

func (a *coMomentAgg) UpdateRow(rec *processing.Record, _ string) error {
	if a.rows == 0 {
		a.stats.instancesWithRows.Add(1)
	}
	a.rows++
	for i, f := range a.params.Fields {
		v, ok := rec.NumericValue(f)
		if !ok {
			v = math.NaN()
		}
		a.x[i] = v
	}
	w := 1.0
	if a.params.Weight != "" {
		v, ok := rec.NumericValue(a.params.Weight)
		if !ok {
			v = math.NaN()
		}
		w = v
	}
	return a.blocks.Add(rec, a.x, w)
}

func (a *coMomentAgg) Finalize() (float64, error) {
	root, err := a.blocks.Tree()
	if err != nil {
		return 0, err
	}
	a.root = root
	return float64(root.N()), nil
}

func (a *coMomentAgg) Aggregate(records []*processing.Record, field string) (float64, error) {
	for _, r := range records {
		if err := a.UpdateRow(r, field); err != nil {
			return 0, err
		}
	}
	return a.Finalize()
}

// Rich surfaces every bit of the merged accumulator into Response.Data.
func (a *coMomentAgg) Rich() (any, error) { return coMomentResultWords(a.root), nil }

// coMomentResultWords renders N, the invalid-weight count, W, the
// means, and per upper-triangle pair its count, weight and M2 (read as
// Cov(0) = M2 / W, plus Cov(1)) as raw words.
func coMomentResultWords(c *linalg.CoMoment) []uint64 {
	out := []uint64{uint64(c.N()), uint64(c.NWeightInvalid()), math.Float64bits(c.W())}
	m := c.Mean()
	cov0, cov1 := c.Cov(0), c.Cov(1)
	for i := 0; i < c.P(); i++ {
		out = append(out, math.Float64bits(m.At(i)))
		for j := i; j < c.P(); j++ {
			out = append(out, uint64(c.PairN(i, j)), math.Float64bits(c.PairW(i, j)),
				math.Float64bits(cov0.At(i, j)), math.Float64bits(cov1.At(i, j)))
		}
	}
	return out
}

func coMomentVehicleRegistry(stats *vehicleStats) *processing.ExtensionRegistry {
	key := processing.StreamabilityKey("aggregator", string(coMomentVehicle))
	factory := func(agg *types.Aggregation, _ *encoding.Schema) (processing.Aggregator, error) {
		var p coMomentVehicleParams
		if err := json.Unmarshal(agg.Params, &p); err != nil {
			return nil, err
		}
		mode := linalg.Listwise
		if p.Mode == "pairwise" {
			mode = linalg.Pairwise
		}
		blocks, err := processing.NewBlockCoMoments(len(p.Fields), mode)
		if err != nil {
			return nil, err
		}
		return &coMomentAgg{params: p, blocks: blocks, x: make([]float64, len(p.Fields)), stats: stats}, nil
	}
	return &processing.ExtensionRegistry{
		Aggregators: map[types.AggregationType]processing.AggregatorFactory{coMomentVehicle: factory},
		Streamable:  map[string]bool{key: true},
		Mergeable:   map[string]bool{key: true},
	}
}

// coMomentSchema: two nullable predictors (missing values exercise the
// listwise / pairwise split), a never-null one, and a nullable weight.
func coMomentSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
		{Name: "x2", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1, Nullable: true},
		{Name: "x3", Type: encoding.FieldTypeF64, ByteOffset: 16, CsvColumnIdx: 2},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 24, CsvColumnIdx: 3, Nullable: true},
	}}
}

// coMomentRows builds rows offset..offset+n-1 of one global sequence.
// Values mix magnitudes so a different merge order shows in the low
// bits; weights include null, negative (invalid) and zero rows.
func coMomentRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs := make([][]uint64, n)
	for i := range recs {
		g := uint64(offset + i)
		h := g*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019
		u := func(k uint64) float64 {
			v := (h ^ (h >> 29) ^ k*0xBF58476D1CE4E5B9) * 0x94D049BB133111EB
			return float64(v>>11) / (1 << 53)
		}
		x1 := 1e4 + 37*u(1)
		x2 := 0.5*x1 + 1e-3*u(2) - 3e3
		x3 := u(3) * 1e6
		w := 0.1 + 4*u(4)
		switch {
		case g%17 == 2:
			w = -1
		case g%19 == 4:
			w = 0
		}
		recs[i] = []uint64{math.Float64bits(x1), math.Float64bits(x2), math.Float64bits(x3), math.Float64bits(w)}
	}
	return recs, func(r, f int) bool {
		g := offset + r
		switch f {
		case 0:
			return g%7 == 0
		case 1:
			return g%11 == 3
		case 3:
			return g%13 == 5
		}
		return false
	}
}

func coMomentRequest(path, mode string, weighted bool) *types.Request {
	p := coMomentVehicleParams{Fields: []string{"x1", "x2", "x3"}, Mode: mode}
	if weighted {
		p.Weight = "w"
	}
	raw, _ := json.Marshal(p)
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: coMomentVehicle, Field: "x1", Label: "cm", Params: raw},
		},
	}
}

type coMomentRun struct {
	words     []uint64
	instances int64
}

func runCoMomentVehicle(t *testing.T, cfg *fs.Config, req *types.Request, decodeWorkers, shardWorkers int) coMomentRun {
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
	if len(resp.Data) != 1 {
		t.Fatalf("Process: %d data rows, want 1", len(resp.Data))
	}
	words, ok := resp.Data[0]["cm"].([]uint64)
	if !ok {
		t.Fatalf("cm = %T, want the vehicle's rich []uint64", resp.Data[0]["cm"])
	}
	return coMomentRun{words: words, instances: stats.instancesWithRows.Load()}
}

func firstWordDiff(a, b []uint64) int {
	if len(a) != len(b) {
		return -2
	}
	for i := range a {
		if a[i] != b[i] {
			return i
		}
	}
	return -1
}

func coMomentArchive(t *testing.T, schema *encoding.Schema, shardRows []int) []byte {
	t.Helper()
	var doc bytes.Buffer
	total, offset := 0, 0
	for _, n := range shardRows {
		total += n
	}
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(total), uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for i, n := range shardRows {
		recs, nullAt := coMomentRows(n, offset)
		write(fmt.Sprintf("s%d.pulse", i), writeNullablePulse(t, schema, recs, nullAt))
		offset += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

var coMomentVariants = []struct {
	mode     string
	weighted bool
}{
	{"listwise", false}, {"listwise", true}, {"pairwise", false}, {"pairwise", true},
}

// TestCoMomentMergeTree_WorkerInvariant is the BlockMerger gate: the
// full CoMoment state (N, invalid-weight count, W, means and every
// pair's count, weight and M2) is BIT-identical across serial and every
// worker count, through the real parallel-decode and shard dispatch,
// listwise and pairwise, weighted and unweighted.
func TestCoMomentMergeTree_WorkerInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record worker-invariance gate in -short mode")
	}
	schema := coMomentSchema()
	B := linalg.MergeBlockSize

	t.Run("DecodeWorkers", func(t *testing.T) {
		// Parallel decode needs mmap, so these cohorts live on a real
		// (temporary) directory; the shard arm below is hermetic.
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
			path := fmt.Sprintf("%s/comoment_%d.pulse", dir, n)
			if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			for _, v := range coMomentVariants {
				t.Run(fmt.Sprintf("n%d_%s_weighted_%v", n, v.mode, v.weighted), func(t *testing.T) {
					req := coMomentRequest(path, v.mode, v.weighted)
					serial := runCoMomentVehicle(t, cfg, req, 1, 0)
					if serial.instances != 1 {
						t.Fatalf("serial run fed %d instances, want 1", serial.instances)
					}
					assertWordsClose(t, serial.words, referenceCoMomentWords(t, n, v.mode, v.weighted))
					for _, workers := range []int{2, 3, 7, 8} {
						got := runCoMomentVehicle(t, cfg, req, workers, 0)
						if got.instances != int64(workers) {
							t.Fatalf("DecodeWorkers=%d fed %d instances — the parallel reducer did not engage", workers, got.instances)
						}
						if i := firstWordDiff(got.words, serial.words); i != -1 {
							t.Errorf("DecodeWorkers=%d differs from serial at word %d", workers, i)
						}
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
		// The single-file twin holds the same rows in the same order,
		// for the documented caveat below.
		recs, nullAt := coMomentRows(total, 0)
		if err := afero.WriteFile(cfg.Fs(), "single.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range coMomentVariants {
			t.Run(fmt.Sprintf("%s_weighted_%v", v.mode, v.weighted), func(t *testing.T) {
				req := coMomentRequest("archive.pulse", v.mode, v.weighted)
				serial := runCoMomentVehicle(t, cfg, req, 0, 1)
				if serial.instances != 1 {
					t.Fatalf("serial run fed %d instances, want 1", serial.instances)
				}
				assertWordsClose(t, serial.words, referenceCoMomentWords(t, total, v.mode, v.weighted))
				for _, workers := range []int{2, 3, 8} {
					got := runCoMomentVehicle(t, cfg, req, 0, workers)
					if got.instances != int64(len(shardRows)) {
						t.Fatalf("ShardWorkers=%d fed %d instances — the shard reducer did not engage", workers, got.instances)
					}
					if i := firstWordDiff(got.words, serial.words); i != -1 {
						t.Errorf("ShardWorkers=%d differs from serial at word %d", workers, i)
					}
				}

				// Documented caveat: an archive and its single-file twin
				// cut blocks at different places, so they agree to
				// rounding but may differ in the last bits. Asserted to
				// a tolerance only; the bit difference is logged.
				single := runCoMomentVehicle(t, cfg, coMomentRequest("single.pulse", v.mode, v.weighted), 0, 0)
				assertWordsClose(t, single.words, serial.words)
				if i := firstWordDiff(single.words, serial.words); i != -1 {
					t.Logf("caveat observed: archive vs single file differ from word %d", i)
				}
			})
		}
	})

	t.Run("OneShardArchiveMatchesSingleFile", func(t *testing.T) {
		// One shard is block-for-block its single-file twin: same bits.
		n := 3*B + 55
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "one.pulse", coMomentArchive(t, schema, []int{n}), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		recs, nullAt := coMomentRows(n, 0)
		if err := afero.WriteFile(cfg.Fs(), "twin.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range coMomentVariants {
			a := runCoMomentVehicle(t, cfg, coMomentRequest("one.pulse", v.mode, v.weighted), 0, 0)
			b := runCoMomentVehicle(t, cfg, coMomentRequest("twin.pulse", v.mode, v.weighted), 0, 0)
			if i := firstWordDiff(a.words, b.words); i != -1 {
				t.Errorf("%s weighted=%v: one-shard archive differs from its single file at word %d", v.mode, v.weighted, i)
			}
		}
	})
}

// referenceCoMomentWords folds rows 0..n-1 straight into one CoMoment —
// the correctness anchor the engine's blocked result must agree with to
// rounding (it guards the vehicle reading the wrong field or weight).
func referenceCoMomentWords(t *testing.T, n int, mode string, weighted bool) []uint64 {
	t.Helper()
	m := linalg.Listwise
	if mode == "pairwise" {
		m = linalg.Pairwise
	}
	c, err := linalg.NewCoMoment(3, m)
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
	if c.N() == 0 || c.N() == int64(n) && mode == "listwise" {
		t.Fatalf("degenerate fixture: N = %d of %d rows — missing values must thin the listwise count", c.N(), n)
	}
	return coMomentResultWords(c)
}

// assertWordsClose compares two result word vectors: the integer words
// (counts) exactly, the float words to a relative 1e-9.
func assertWordsClose(t *testing.T, a, b []uint64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("result shapes differ: %d vs %d words", len(a), len(b))
	}
	// Layout: N, nInvalid, W, then per i: mean, per j>=i: pairN, pairW, cov0, cov1.
	isCount := map[int]bool{0: true, 1: true}
	k := 3
	const p = 3
	for i := 0; i < p; i++ {
		k++
		for j := i; j < p; j++ {
			isCount[k] = true
			k += 4
		}
	}
	for i := range a {
		if isCount[i] {
			if a[i] != b[i] {
				t.Errorf("count word %d: %d vs %d", i, a[i], b[i])
			}
			continue
		}
		x, y := math.Float64frombits(a[i]), math.Float64frombits(b[i])
		if math.IsNaN(x) && math.IsNaN(y) {
			continue
		}
		if d := math.Abs(x - y); d > 1e-9*math.Max(1, math.Max(math.Abs(x), math.Abs(y))) {
			t.Errorf("word %d: %v vs %v", i, x, y)
		}
	}
}
