package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// Sorted-vs-scattered gates for run-skip (E2-S3).
//
// Both directions are asserted on one synthetic fixture PAIR — the
// join-shape cohort in its generated order (sorted by the parent key,
// high continuation) and a deterministic permutation of the same rows
// (scattered, low continuation):
//
//   - the WIN on sorted data: the decoder rewrites only the fields that
//     changed, so the fields it writes track 1 - continuation;
//   - the LOSS bound on scattered data: comparison is the only work
//     run-skip adds, and the adaptive backoff caps the rows it compares.
//     Writes never exceed a full decode's, so a scattered scan does at
//     most a full decode plus that bounded compare overhead.
//
// Everything here is gated on COUNTS (rows offered for comparison, rows
// kept, field writes) observed through a counting record, never on wall
// clock, so the gates are deterministic on any architecture. The
// wall-clock ratios live in BenchmarkJoinShape_RunSkip (-bench only).

// joinShapeGateParents sizes the count-gate cohort: 400 parents x 12.5 =
// 5,000 rows (~1.7 MB, generated in memory in a few ms) — large enough
// for several backoff cycles on the scattered copy, which the 400-row
// committed fixture is not.
const joinShapeGateParents = 400

// runSkipCounts is what a counting record observes over one scan.
type runSkipCounts struct {
	rows      int // rows decoded (the begin of the EOF attempt is not counted)
	offered   int // BeginRunRow(keep=true): the reader compared this row
	kept      int // BeginRunRow returned true: partial rewrite
	numeric   int // SetNumericAt calls: one per field value written
	other     int // SetNullFieldAt / SetWideFieldAt / Set*SetAt
	clearNull int // ClearNullAt: a kept row's null -> non-null flip

	lastOffered, lastKept bool // the most recent BeginRunRow, undone at EOF
}

// countingRecord forwards the index-keyed decoder contract to a
// production positional Record and counts every call. It does NOT
// implement encoding.RunSkipRecord, so the decoder clears and fully
// repopulates it each row: the no-skip arm.
type countingRecord struct {
	r *processing.Record
	c runSkipCounts
}

func (n *countingRecord) SetNumeric(name string, v float64) { n.r.SetNumeric(name, v) }
func (n *countingRecord) SetNullField(name string)          { n.r.SetNullField(name) }
func (n *countingRecord) SetWideField(name string, v any)   { n.r.SetWideField(name, v) }
func (n *countingRecord) SetNumericAt(i int, v float64)     { n.c.numeric++; n.r.SetNumericAt(i, v) }
func (n *countingRecord) SetNullFieldAt(i int)              { n.c.other++; n.r.SetNullFieldAt(i) }
func (n *countingRecord) SetWideFieldAt(i int, v any)       { n.c.other++; n.r.SetWideFieldAt(i, v) }
func (n *countingRecord) SetNarrowSetAt(i int, m uint64)    { n.c.other++; n.r.SetNarrowSetAt(i, m) }
func (n *countingRecord) SetWideSetAt(i int, m encoding.SetMask) {
	n.c.other++
	n.r.SetWideSetAt(i, m)
}
func (n *countingRecord) ClearForRow() {
	n.c.rows++
	n.c.lastOffered, n.c.lastKept = false, false
	n.r.ClearForRow()
}

// countingRunSkipRecord is countingRecord plus encoding.RunSkipRecord:
// the run-skip arm, on the same production record.
type countingRunSkipRecord struct{ *countingRecord }

func (n countingRunSkipRecord) BeginRunRow(token uint64, keep bool) bool {
	n.c.rows++
	ok := n.r.BeginRunRow(token, keep)
	n.c.lastOffered, n.c.lastKept = keep, ok
	if keep {
		n.c.offered++
	}
	if ok {
		n.c.kept++
	}
	return ok
}
func (n countingRunSkipRecord) ClearNullAt(i int) { n.c.clearNull++; n.r.ClearNullAt(i) }

var (
	_ encoding.TypedSetRecord = (*countingRecord)(nil)
	_ encoding.RunSkipRecord  = countingRunSkipRecord{}
)

// countRunSkipScan decodes every row of payload through one reader and
// one counting record (run-skip arm when skip, else no-skip) and
// returns the counts.
func countRunSkipScan(t *testing.T, schema *encoding.Schema, payload []byte, keep encoding.FieldFilter, plan *encoding.DecodePlan, skip bool) runSkipCounts {
	t.Helper()
	rr := encoding.NewRecordReader(bytes.NewReader(payload), schema)
	cr := &countingRecord{r: processing.NewReusableRecord(schema)}
	var rec encoding.ReusableRecord = cr
	if skip {
		rec = countingRunSkipRecord{cr}
	}
	for {
		err := rr.ReadRecordReusedWithPlan(rec, keep, plan)
		if err == io.EOF {
			// The reader begins a row before it finds the payload
			// exhausted; that attempt decoded nothing.
			c := cr.c
			c.rows--
			if c.lastOffered {
				c.offered--
			}
			if c.lastKept {
				c.kept--
			}
			return c
		}
		if err != nil {
			t.Fatalf("decode row %d: %v", cr.c.rows, err)
		}
	}
}

// joinShapeFieldContinuation is joinShapeContinuation restricted to the
// fields keep retains (nil = every field): the run-skip hit rate of a
// scan that decodes only those fields.
func joinShapeFieldContinuation(payload []byte, schema *encoding.Schema, rows int, keep encoding.FieldFilter) float64 {
	stride := schema.RecordByteSize()
	bm := schema.BitmapByteSize()
	same, total := 0, 0
	for k := 1; k < rows; k++ {
		cur, prev := payload[k*stride:(k+1)*stride], payload[(k-1)*stride:k*stride]
		for i := range schema.Fields {
			f := &schema.Fields[i]
			if keep != nil && !keep(f.Name) {
				continue
			}
			w := 1
			if !f.Type.IsBitPacked() {
				w = f.Type.ByteSize()
			}
			eq := bytes.Equal(cur[f.ByteOffset:f.ByteOffset+w], prev[f.ByteOffset:f.ByteOffset+w])
			if f.Nullable {
				eq = eq && encoding.BitmapIsNull(cur[stride-bm:], i) == encoding.BitmapIsNull(prev[stride-bm:], i)
			}
			if eq {
				same++
			}
			total++
		}
	}
	return float64(same) / float64(total)
}

// joinShapeGatePair generates the count-gate cohort and returns the
// payloads (record region only) of its sorted and scattered orderings.
func joinShapeGatePair(t *testing.T) (*encoding.Schema, int, map[string][]byte) {
	t.Helper()
	schema, data, err := buildJoinShapeCohort(joinShapeGateParents)
	if err != nil {
		t.Fatal(err)
	}
	rows := joinShapeRows(joinShapeGateParents)
	scattered := scatterJoinShapeCohort(data, schema, rows)
	n := rows * schema.RecordByteSize()
	return schema, rows, map[string][]byte{
		"sorted":    data[len(data)-n:],
		"scattered": scattered[len(scattered)-n:],
	}
}

// TestJoinShapeRunSkip_WorkCounts gates both directions of run-skip on
// counts. MEASURED values (5,000-row gate cohort, any architecture —
// the counts are deterministic) are recorded next to each bound.
func TestJoinShapeRunSkip_WorkCounts(t *testing.T) {
	schema, rows, payloads := joinShapeGatePair(t)
	for _, tc := range []struct {
		order string
		shape string
		keep  encoding.FieldFilter
		// sorted: continuation over the decoded fields must be at least
		// minCont, and kept rows must write within writeSlack of
		// (1 - continuation) of a full decode's field values.
		minCont float64
		// scattered: continuation at most maxCont; the fraction of rows
		// offered for comparison at most maxOffered.
		maxCont    float64
		maxOffered float64
	}{
		// MEASURED sorted/full: continuation 0.7087, write fraction
		// 0.2925 (140,700 of 480,961 values), offered = kept = 4,999.
		{order: "sorted", shape: "full", minCont: 0.65},
		// MEASURED sorted/projected4: continuation 0.5536, write
		// fraction 0.4465 (8,930 of 20,000), offered = kept = 4,999.
		// Two of the four retained fields are child fields that change
		// almost every row, so this sits 5 points under the backoff's
		// 50%-rewritten trip point; a projection retaining more child
		// fields would back off even on sorted data (by design: there
		// the comparison no longer pays).
		{order: "sorted", shape: "projected4", keep: joinShapeKeep4, minCont: 0.50},
		// MEASURED scattered/full: continuation 0.1930, offered 160 of
		// 5,000 rows (0.0320 — five 32-row probe windows, one per
		// 1,024-row backoff), write fraction 0.9940.
		{order: "scattered", shape: "full", maxCont: 0.30, maxOffered: 0.05},
		// MEASURED scattered/projected4: continuation 0.2070, offered
		// 160 of 5,000 rows (0.0320), write fraction 0.9940.
		{order: "scattered", shape: "projected4", keep: joinShapeKeep4, maxCont: 0.30, maxOffered: 0.05},
	} {
		t.Run(tc.order+"/"+tc.shape, func(t *testing.T) {
			var plan *encoding.DecodePlan
			if tc.keep != nil {
				plan = baselinePlan(t, schema, tc.keep)
			}
			payload := payloads[tc.order]
			cont := joinShapeFieldContinuation(payload, schema, rows, tc.keep)
			skip := countRunSkipScan(t, schema, payload, tc.keep, plan, true)
			full := countRunSkipScan(t, schema, payload, tc.keep, plan, false)
			writeFrac := float64(skip.numeric) / float64(full.numeric)
			offeredFrac := float64(skip.offered) / float64(rows)
			t.Logf("continuation %.4f; run-skip: offered %d kept %d numeric writes %d other %d clear-null %d; full: numeric %d other %d; write fraction %.4f, offered fraction %.4f",
				cont, skip.offered, skip.kept, skip.numeric, skip.other, skip.clearNull, full.numeric, full.other, writeFrac, offeredFrac)

			if skip.rows != rows || full.rows != rows {
				t.Fatalf("rows begun: run-skip %d, full %d; want %d", skip.rows, full.rows, rows)
			}
			// Structural net-loss bound, both orders: run-skip never
			// writes more values than a full decode. Its only extra
			// write is ClearNullAt, at most one per null -> non-null
			// flip, which a full decode pays for in its row clear.
			if skip.numeric > full.numeric || skip.other > full.other {
				t.Fatalf("run-skip wrote more than a full decode: numeric %d > %d or other %d > %d",
					skip.numeric, full.numeric, skip.other, full.other)
			}
			if full.offered != 0 || full.kept != 0 {
				t.Fatalf("no-skip arm was offered/kept rows: %+v", full)
			}

			if tc.order == "sorted" {
				if cont < tc.minCont {
					t.Fatalf("sorted continuation %.4f below %.2f: the fixture no longer holds a sorted parent block", cont, tc.minCont)
				}
				// Every row after the first compares and keeps: the probe
				// never trips on sorted data.
				if skip.offered != rows-1 || skip.kept != rows-1 {
					t.Fatalf("sorted: offered %d kept %d, want %d each (backoff must not engage)", skip.offered, skip.kept, rows-1)
				}
				// The win: written values track the changed fraction.
				const writeSlack = 0.03
				if want := 1 - cont; writeFrac > want+writeSlack || writeFrac < want-writeSlack {
					t.Fatalf("sorted: wrote %.4f of a full decode's values, want 1-continuation = %.4f +/- %.2f", writeFrac, want, writeSlack)
				}
				return
			}
			if cont > tc.maxCont {
				t.Fatalf("scattered continuation %.4f above %.2f: the scatter no longer breaks the parent runs", cont, tc.maxCont)
			}
			// The loss bound: the backoff caps compared rows to about one
			// probe window per backoff period.
			if offeredFrac > tc.maxOffered {
				t.Fatalf("scattered: %d of %d rows (%.4f) compared, above %.2f — the adaptive backoff is not engaging",
					skip.offered, rows, offeredFrac, tc.maxOffered)
			}
			if skip.offered == 0 {
				t.Fatalf("scattered: no row was compared — the probe never ran")
			}
		})
	}
}

// joinShapePlainIter hides ReusableIterator, so processing.EnableReuse is
// a no-op and every row is decoded into a FRESH record: nothing is ever
// kept across rows — the pre-run-skip output, from the same bytes.
type joinShapePlainIter struct{ it *streamingIterator }

func (p joinShapePlainIter) Next() bool                 { return p.it.Next() }
func (p joinShapePlainIter) Record() *processing.Record { return p.it.Record() }
func (p joinShapePlainIter) Reset()                     { p.it.Reset() }

// TestJoinShapeRunSkip_ProcessOutputOrderIndependent runs order-free
// streaming requests (grouped and ungrouped, over nullable fields from
// both blocks, full and projected) over the sorted and scattered
// fixtures, each through the reuse iterator (run-skip) and through a
// fresh-record iterator (no run-skip), and requires all four responses
// to be byte-identical JSON. Correctness before performance: row order
// and run-skip are both invisible in the output.
func TestJoinShapeRunSkip_ProcessOutputOrderIndependent(t *testing.T) {
	fsys, schema, _, paths := joinShapeOrders(t)
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "c_u64_01", Label: "n"},
			{Type: types.AGG_SUM, Field: "c_u64_01", Label: "sum_c"},
			{Type: types.AGG_MIN, Field: "p_u64_04", Label: "min_p"},
			{Type: types.AGG_MAX, Field: "p_u64_04", Label: "max_p"},
			{Type: types.AGG_FREQUENCY, Field: "c_cat8_02", Label: "freq_c"},
			{Type: types.AGG_FREQUENCY, Field: "p_cat8_03", Label: "freq_p"},
		}
	}
	reqFields := map[string]bool{"c_u64_01": true, "p_u64_04": true, "c_cat8_02": true, "p_cat8_03": true, "p_cat16_01": true}
	for _, tc := range []struct {
		name    string
		req     func() *types.Request
		project bool
	}{
		{name: "ungrouped/full", req: func() *types.Request { return &types.Request{Aggregations: aggs()} }},
		{name: "grouped/full", req: func() *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "p_cat16_01"}},
				Aggregations: aggs(),
				Sort:         []types.OrderKey{{Field: "p_cat16_01"}},
			}
		}},
		{name: "grouped/projected", project: true, req: func() *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "p_cat16_01"}},
				Aggregations: aggs(),
				Sort:         []types.OrderKey{{Field: "p_cat16_01"}},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var want []byte
			for _, order := range []string{"sorted", "scattered"} {
				for _, reuse := range []bool{true, false} {
					it := newStreamingIterator(fsys, paths[order], schema)
					if tc.project {
						it.SetProjection(func(n string) bool { return reqFields[n] }, len(reqFields))
					}
					var iter processing.RecordIterator = it
					if !reuse {
						iter = joinShapePlainIter{it}
					}
					proc := processing.NewProcessor(schema)
					resp, err := proc.Process(context.Background(), tc.req(), iter)
					_ = it.Close()
					if err != nil {
						t.Fatalf("%s reuse=%v: %v", order, reuse, err)
					}
					if proc.LastPath() != processing.PathStreaming {
						t.Fatalf("%s reuse=%v: path %v, want streaming (run-skip is a reuse-path optimisation)", order, reuse, proc.LastPath())
					}
					got, err := json.Marshal(resp)
					if err != nil {
						t.Fatal(err)
					}
					if want == nil {
						want = got
						continue
					}
					if !bytes.Equal(got, want) {
						t.Fatalf("%s reuse=%v: response differs from sorted/run-skip:\n got  %s\n want %s", order, reuse, got, want)
					}
				}
			}
		})
	}
}
