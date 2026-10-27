package service

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// cellMemoryCase is one measured crosstab over aggMemorySchema: rows
// (g) x cols (h) keys, perCell records in every cell.
type cellMemoryCase struct {
	measures []*types.Aggregation // crosstab.cell when one, crosstab.cells when more
	aux      []*types.Aggregation // crosstab.margin_aggregations
	margins  bool                 // row, column and grand margins
	weighted bool
	off      bool // components disabled
	buffered bool // the buffered arm (else the fused one)
	rows     int
	cols     int
	perCell  int
	// shards splits the records into this many equal runs, each
	// starting where the last ended — a shard archive's merge positions
	// (a shard starting inside a merge block buffers that block's rows
	// in a non-head fragment); 0 or 1 is a single file.
	shards int
	// nested makes both axes two groupers deep — rows (i, g), columns
	// (k, h), i = g mod 5, k = h mod 4 — and normalizes by row at
	// normalize_level 0 within column prefix 0: the partial-depth and
	// cross-axis denominators.
	nested bool
	// diagonal keeps the axes unbounded — g and h stay aggMemorySchema's
	// u32 words, no dictionary, so each axis bound is the record count —
	// and lands record i in cell (i, i) of a rows x rows grid: rows
	// records over rows^2 dense positions, all but rows of them empty
	// (U51 E5-S11, the fused arm's dense grid).
	diagonal bool
}

func (c cellMemoryCase) records() int {
	if c.diagonal {
		return c.rows
	}
	return c.rows * c.cols * c.perCell
}

// schema is aggMemorySchema with the axis fields made dictionary
// categoricals of exactly the case's cardinalities — rows labels on g,
// cols on h, 5 on i and 4 on k (the nested outer positions) — so every
// axis bound is known, as on a categorical crosstab.
func (c cellMemoryCase) schema() *encoding.Schema {
	s := aggMemorySchema()
	s.Fields = append(s.Fields, encoding.Field{Name: "k"})
	dict := func(prefix string, n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := range n {
			if _, err := d.Add(fmt.Sprintf("%s%05d", prefix, i)); err != nil {
				panic(err)
			}
		}
		return d
	}
	for i := range s.Fields {
		f := &s.Fields[i]
		if c.diagonal && (f.Name == "g" || f.Name == "h") {
			continue
		}
		switch f.Name {
		case "g":
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU16, dict("row", max(c.rows, 1))
		case "h":
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU16, dict("col", max(c.cols, 1))
		case "i":
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU8, dict("outer", 5)
		case "k":
			f.Type, f.Dictionary = encoding.FieldTypeCategoricalU8, dict("band", 4)
		}
	}
	off := 0
	for i := range s.Fields {
		s.Fields[i].ByteOffset = off
		s.Fields[i].CsvColumnIdx = i
		off += s.Fields[i].Type.ByteSize()
	}
	return s
}

func (c cellMemoryCase) request() *types.Request {
	spec := &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
		Shape:   types.CrosstabShapeMatrix,
	}
	if c.nested {
		spec.Rows = append([]*types.Group{{Type: types.GROUP_CATEGORY, Field: "i"}}, spec.Rows...)
		spec.Columns = append([]*types.Group{{Type: types.GROUP_CATEGORY, Field: "k"}}, spec.Columns...)
		zero := 0
		spec.Normalize, spec.NormalizeLevel, spec.NormalizeWithin = types.CrosstabNormalizeRow, &zero, &zero
	}
	cp := func(a *types.Aggregation) *types.Aggregation { c := *a; return &c }
	if len(c.measures) == 1 {
		spec.Cell = cp(c.measures[0])
	} else {
		for _, m := range c.measures {
			spec.Cells = append(spec.Cells, cp(m))
		}
	}
	for _, a := range c.aux {
		spec.MarginAggregations = append(spec.MarginAggregations, cp(a))
	}
	if c.margins {
		spec.Margins = types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	}
	req := &types.Request{Crosstab: spec}
	if c.weighted {
		req.Weight = &types.WeightSpec{Field: "w"}
	}
	return req
}

// shardRecords is the case's per-shard record counts (nil: one file).
func (c cellMemoryCase) shardRecords() []int64 {
	if c.shards <= 1 {
		return nil
	}
	n, per := c.records(), (c.records()+c.shards-1)/c.shards
	var out []int64
	for start := 0; start < n; start += per {
		out = append(out, int64(min(per, n-start)))
	}
	return out
}

// cellSlot is aggMemorySlot made a valid crosstab cell: a vector
// aggregator selects one element (a crosstab cell holds one number).
func cellSlot(at types.AggregationType, label string, members int) *types.Aggregation {
	a := aggMemorySlot(at, label, members)
	if vectorAggregation(at) {
		a.Params = json.RawMessage(`{"element":"f00"}`)
	}
	return a
}

// setCellRecord makes rec the case's i-th record: cell i / perCell in
// row-major order, a distinct x per record, its merge position under
// the case's shard split.
func setCellRecord(c cellMemoryCase, rec *processing.Record, i int) {
	cell := i / max(c.perCell, 1)
	row, col := cell/max(c.cols, 1), cell%max(c.cols, 1)
	if c.diagonal {
		row, col = i, i
	}
	rec.Set("g", float64(row))
	rec.Set("h", float64(col))
	rec.Set("x", float64(i%97)+float64(i)*0.25)
	rec.Set("y", float64(i)+0.75)
	if c.nested {
		rec.Set("i", float64(row%5))
		rec.Set("k", float64(col%4))
	}
	per := (c.records() + max(c.shards, 1) - 1) / max(c.shards, 1)
	shard := i / per
	rec.SetMergePosition(shard, shard*per, i-shard*per)
}

func newCellRecord(schema *encoding.Schema) *processing.Record {
	vals := map[string]float64{"g": 0, "h": 0, "i": 0, "k": 0, "x": 1.5, "y": 2.25, "z": 1, "w": 1.25}
	for i := range aggMemoryMaxMembers {
		vals[fmt.Sprintf("f%02d", i)] = float64(i) + 0.5
	}
	return processing.NewRecordWithWide(schema, vals, nil, map[string]any{"tags": uint64(0b1011)})
}

// cellMemoryIter generates the case's records into one reused record
// (the fused arm keeps no record past its update) and reads the live
// heap once the fold has seen them all: the folded state.
type cellMemoryIter struct {
	c        cellMemoryCase
	rec      *processing.Record
	i        int
	folded   uint64
	measured bool
}

func (it *cellMemoryIter) Next() bool {
	if it.i >= it.c.records() {
		if !it.measured {
			it.measured = true
			it.folded = liveHeap()
		}
		return false
	}
	setCellRecord(it.c, it.rec, it.i)
	it.i++
	return true
}
func (it *cellMemoryIter) Record() *processing.Record { return it.rec }
func (it *cellMemoryIter) Reset()                     { it.i = 0 }

// peakLive runs fn under GOGC=1 — a collection every 1% of heap growth
// — sampling the heap the last collection MARKED live throughout
// (runtime/metrics /gc/heap/live:bytes: no stop-the-world), then once
// more after a collection at fn's return (what the run retains): the
// run's peak live heap, garbage excluded. Not the heap's object bytes
// (/memory/classes/heap/objects:bytes): those count every object not
// yet swept and every object allocated since the last mark, garbage
// that grows when the collector shares the CPU — under go test ./...
// on a 4-core runner the dense 60 x 60 cases measured within 1% of
// their charge (over it on some runs), against 1.15x alone, the same
// figures before and after a change that touched no crosstab path. The
// marked heap measures 1.20x contended and 1.24x alone.
func peakLive(fn func() error) (uint64, error) {
	old := debug.SetGCPercent(1)
	defer debug.SetGCPercent(old)
	var peak atomic.Uint64
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sample := []metrics.Sample{{Name: peakLiveMetric}}
		for {
			metrics.Read(sample)
			if v := sample[0].Value.Uint64(); v > peak.Load() {
				peak.Store(v)
			}
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	err := fn()
	close(done)
	wg.Wait()
	runtime.GC()
	sample := []metrics.Sample{{Name: peakLiveMetric}}
	metrics.Read(sample)
	return max(peak.Load(), sample[0].Value.Uint64()), err
}

// peakLiveMetric is the marked live heap peakLive samples.
const peakLiveMetric = "/gc/heap/live:bytes"

// cellMemoryFigures are a case's measured bytes beyond the heap before
// the run: the run's peak live heap (the buffered arm's materialised
// records included — the estimate charges them), and on the fused arm
// the folded state alone.
type cellMemoryFigures struct {
	peak, folded float64
}

// measureCellMemory runs the case on its arm as the service does
// (newProcessor under the run's compute plan; RunCrosstabFused over a
// record iterator, or RunCrosstab over the materialised records).
func measureCellMemory(t *testing.T, c cellMemoryCase) cellMemoryFigures {
	t.Helper()
	svc := New(nil)
	svc.SetDisableComponents(c.off)
	schema := c.schema()
	req := c.request()
	ctx := context.Background()
	if fused, why := processing.CanFuseCrosstab(req, schema, svc.extensions); !fused && !c.buffered {
		t.Fatalf("fused case declined by the fusion gate: %s", why)
	}
	proc := svc.newProcessor(ctx, schema, req)
	it := &cellMemoryIter{c: c, rec: newCellRecord(schema)}
	before := liveHeap()
	var records []*processing.Record
	var resp *types.Response
	peak, err := peakLive(func() error {
		var err error
		if c.buffered {
			records = make([]*processing.Record, c.records())
			for i := range records {
				records[i] = newCellRecord(schema)
				setCellRecord(c, records[i], i)
			}
			resp, err = proc.RunCrosstab(ctx, req, records)
		} else {
			resp, err = proc.RunCrosstabFused(ctx, req, it)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(records)
	runtime.KeepAlive(resp)
	f := cellMemoryFigures{peak: float64(peak) - float64(before)}
	if !c.buffered {
		f.folded = float64(it.folded) - float64(before)
	}
	return f
}

// cellMemoryCharge is the memory estimate's charge for what the case
// measures: descx.EstimateMemory on the case's arm, less the run base
// and the payload (limits.EstimateMemory of the bare scan) — the
// crosstab's cells, axis keys and margins, its per-value state and
// vector fragments, and on the buffered arm the materialised records
// and its one bucket.
func cellMemoryCharge(t *testing.T, c cellMemoryCase) float64 {
	t.Helper()
	schema := c.schema()
	in := descx.LimitInputs{Records: int64(c.records()), ShardRecords: c.shardRecords(), DisableComponents: c.off, FusionDisabled: c.buffered}
	req := processing.StampWeightsWith(c.request(), nil, New(nil).extensions)
	arm := descx.MemoryArm(req, schema, nil, in)
	if want := map[bool]limits.MemoryArm{false: limits.ArmFusedCrosstab, true: limits.ArmBuffered}[c.buffered]; arm != want {
		t.Fatalf("predict picks the %s arm, the case runs %s", arm, want)
	}
	est, ok := descx.EstimateMemory(req, schema, nil, in)
	if !ok {
		t.Fatal("estimate unknown")
	}
	bare := limits.MemoryInputs{Arm: limits.ArmStreaming, Records: in.Records, Stride: int64(schema.RecordByteSize())}
	if len(in.ShardRecords) > 0 {
		bare.MaxShardRecords = in.ShardRecords[0]
		bare.SchemaDecodes, bare.SchemaBytes = 1, limits.SchemaDecodeBytes(schema)
	}
	return float64(est - limits.EstimateMemory(bare))
}

// crosstabMemoryAttempts bounds the re-measures of a case whose peak
// exceeds its charge. The sampled peak's noise is one-sided — garbage
// the GOGC=1 collector has not yet reclaimed only inflates it, never
// deflates it — and grows when the run shares the CPU (go test ./...
// runs packages concurrently; peakLive samples the marked heap to keep
// that garbage out), so the lowest of a few measures is the honest
// figure. A charge that misses a term misses on every attempt.
const crosstabMemoryAttempts = 3

// crosstabMemoryCheck measures c and fails when its peak — the lowest
// of up to crosstabMemoryAttempts measures — exceeds the charge.
func crosstabMemoryCheck(t *testing.T, name string, c cellMemoryCase) {
	t.Helper()
	f := measureCellMemory(t, c)
	charge := cellMemoryCharge(t, c)
	for i := 1; i < crosstabMemoryAttempts && f.peak > charge; i++ {
		if g := measureCellMemory(t, c); g.peak < f.peak {
			f = g
		}
	}
	cells := float64(c.rows * c.cols)
	t.Logf("%-44s buffered=%-5v off=%-5v weighted=%-5v %dx%dx%d: folded %7.1f peak %7.1f charge %7.1f B a cell (%.2f)",
		name, c.buffered, c.off, c.weighted, c.rows, c.cols, c.perCell, f.folded/cells, f.peak/cells, charge/cells, charge/f.peak)
	if f.peak > charge {
		t.Errorf("%s %+v: peak %.0f B (%.1f a cell) over the estimate's %.0f (%.1f a cell)", name, c, f.peak, f.peak/cells, charge, charge/cells)
	}
}

// TestCrosstabMemory_GridPositionBytes (U51 E5-S11) pins
// limits.CrosstabGridPositionBytes to the Go layout it charges: per
// empty position the emitted types.MatrixCell, with components on a
// CellCounts int and a CellComponents map pointer, and on the fused arm
// the fold's dense slots — a processing.OnlineAggregator interface, its
// count and null count ints, and weighted a *processing.WeightFloor —
// at twice their size (append growth), all set 25% over (rounded up).
// A layout change moves the expected figure and fails here; the heap
// gate is TestCrosstabMemory_CellChargeTable's sparse cases.
func TestCrosstabMemory_GridPositionBytes(t *testing.T) {
	var (
		cell   types.MatrixCell
		count  int
		comps  map[string]any
		agg    processing.OnlineAggregator
		weight *processing.WeightFloor
	)
	for _, fused := range []bool{false, true} {
		for _, weighted := range []bool{false, true} {
			for _, off := range []bool{false, true} {
				layout := int64(unsafe.Sizeof(cell))
				if !off {
					layout += int64(unsafe.Sizeof(count) + unsafe.Sizeof(comps))
				}
				if fused {
					fold := int64(unsafe.Sizeof(agg) + 2*unsafe.Sizeof(count))
					if weighted {
						fold += int64(unsafe.Sizeof(weight))
					}
					layout += 2 * fold
				}
				if got, want := limits.CrosstabGridPositionBytes(fused, weighted, off), (layout*5+3)/4; got != want {
					t.Errorf("fused=%v weighted=%v off=%v: charged %d B a position, layout %d B wants %d", fused, weighted, off, got, layout, want)
				}
			}
		}
	}
}
