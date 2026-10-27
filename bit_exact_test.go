package pulse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestBitExact_* (U51 E2-S3, #357, FR-20) is the bit-exactness gate for
// the binned reproducible sums (internal/reprosum): AGG_SUM, AGG_AVERAGE
// (that sum over an exact integer n), AGG_RATIO, the weighted sums
// (weighted AGG_SUM / AGG_AVERAGE / AGG_COUNT, AGG_WEIGHTED_MEAN), the
// sum_weights / n_eff floor and ATTR_FORMULA total(). Over non-dyadic,
// mixed-magnitude floats (x spans 1e-3 .. 1e6; the probability weight
// and the ratio denominator are non-dyadic too) every arm must answer
// the SAME BITS: the serial single file, DecodeWorkers 2 / 3 / 7 on it,
// and ShardWorkers 1 / 2 / 3 / 8 on an archive of the same rows split
// into nine UNEVEN shards. Data and Components compare with
// reflect.DeepEqual; the fixture has no empty bucket, so no NaN reaches
// a compared figure. Welford figures (variance, stddev) are NOT here:
// they merge by Chan-Welford and stay within ULP, by contract.

const bitExactRows = 120_000 // above the parallel-decode threshold (100k)

// bitExactShardWeights is the uneven split: each row lands in shard i
// with probability proportional to weight i (tiny, huge and odd
// shards), by a hash of the row, so the archive holds the rows in a
// DIFFERENT order from the single file — a serial archive scan folds
// them in another order, and every partition differs.
var bitExactShardWeights = []uint64{1, 17_003, 3, 40_000, 9_999, 12, 30_000, 22_981, 2}

// bitExactShardOf is row r's shard: rows 0..8 seed one shard each (no
// shard is empty), the rest follow the weights.
func bitExactShardOf(r uint64) int {
	if r < uint64(len(bitExactShardWeights)) {
		return int(r)
	}
	var total uint64
	for _, w := range bitExactShardWeights {
		total += w
	}
	u := bitExactHash(r, 9) % total
	for i, w := range bitExactShardWeights {
		if u < w {
			return i
		}
		u -= w
	}
	return len(bitExactShardWeights) - 1
}

// bitExactHash is splitmix64 over (row, column).
func bitExactHash(row, col uint64) uint64 {
	z := row*0x9E3779B97F4A7C15 + col*0xD1B54A32D192ED03 + 0x632BE59BD9B4E019
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// bitExactUnit maps a hash to [0, 1).
func bitExactUnit(h uint64) float64 { return float64(h>>11) / (1 << 53) }

// bitExactStride is the fixed-width part of a row (the null bitmap follows).
const bitExactStride = 39

var bitExactScales = [...]float64{1e-3, 1e-2, 0.1, 1, 10, 100, 1e3, 1e4, 1e5, 1e6}

func bitExactSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east", "west", "central"} {
		if _, err := dict.Add(v); err != nil {
			panic(err)
		}
	}
	tags := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := tags.Add(v); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 2},
		{Name: "pw", Type: encoding.FieldTypeF64, ByteOffset: 17, CsvColumnIdx: 3},
		{Name: "fw", Type: encoding.FieldTypeU8, ByteOffset: 25, CsvColumnIdx: 4},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 26, CsvColumnIdx: 5, Dictionary: tags},
		{Name: "rid", Type: encoding.FieldTypeU32, ByteOffset: 27, CsvColumnIdx: 6},
		{Name: "rv", Type: encoding.FieldTypeF64, ByteOffset: 31, CsvColumnIdx: 7},
	}}
}

// bitExactRow encodes row r. Every product is an explicit float64
// barrier, so arm64 FMA fusion cannot move the inputs.
func bitExactRow(buf []byte, r uint64) {
	clear(buf[bitExactStride:])
	buf[0] = byte(r % 5)
	h := bitExactHash(r, 1)
	x := float64((2*bitExactUnit(h) - 1) * bitExactScales[h%uint64(len(bitExactScales))])
	putF64(buf[1:], x)
	putF64(buf[9:], 1+float64(bitExactUnit(bitExactHash(r, 2))*97.3))
	putF64(buf[17:], 0.05+float64(bitExactUnit(bitExactHash(r, 3))*2.7))
	buf[25] = byte(1 + r%4)
	buf[26] = byte(bitExactHash(r, 4) & 0x3f) // a set_u8 mask over six members
	// rid: a respondent spans three consecutive rows and carries ONE
	// non-dyadic value rv on all of them (AGG_DISTINCT_SUM's key).
	rid := r / 3
	buf[27], buf[28], buf[29], buf[30] = byte(rid), byte(rid>>8), byte(rid>>16), byte(rid>>24)
	putF64(buf[31:], float64(bitExactUnit(bitExactHash(rid, 5))*bitExactScales[rid%uint64(len(bitExactScales))]))
	if r%11 == 5 {
		encoding.BitmapSetNull(buf[bitExactStride:], 1)
	}
}

func putF64(b []byte, v float64) {
	u := math.Float64bits(v)
	for i := range 8 {
		b[i] = byte(u >> (8 * i))
	}
}

// writeBitExactFile writes the rows keep accepts, in row order, as one
// .pulse file.
func writeBitExactFile(t *testing.T, path string, schema *encoding.Schema, keep func(r uint64) bool) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, schema); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, bitExactStride+schema.BitmapByteSize())
	n := 0
	for r := uint64(0); r < bitExactRows; r++ {
		if !keep(r) {
			continue
		}
		bitExactRow(row, r)
		if _, err := w.Write(row); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return n
}

// bitExactCohorts writes the single file flat.pulse and the nine-shard
// archive arch.pulse of the same rows (hash-scattered, unevenly) under
// a fresh OS directory (the parallel-decode arm needs a real, mappable
// file).
func bitExactCohorts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	schema := bitExactSchema()
	writeBitExactFile(t, filepath.Join(dir, "flat.pulse"), schema, func(uint64) bool { return true })
	var shards []string
	total := 0
	for i := range bitExactShardWeights {
		name := fmt.Sprintf("part%d.pulse", i)
		total += writeBitExactFile(t, filepath.Join(dir, name), schema, func(r uint64) bool { return bitExactShardOf(r) == i })
		shards = append(shards, name)
	}
	if total != bitExactRows {
		t.Fatalf("shards hold %d rows, want %d", total, bitExactRows)
	}
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return dir
}

// bitExactArm is one execution configuration over one cohort.
type bitExactArm struct {
	name    string
	cohort  string
	opts    pulse.Options
	arm     observe.Arm // the arm the run must take ("" = unchecked)
	workers int         // the worker count it must report (0 = unchecked)
}

// bitExactArms: the serial single file, DecodeWorkers 2 / 3 / 7 on it,
// and ShardWorkers 1 / 2 / 3 / 8 on the archive. parallel says whether
// the fanned-out arms must really fan out (a request the merge gate
// keeps sequential sets it false).
func bitExactArms(parallel bool) []bitExactArm {
	arms := []bitExactArm{
		{name: "flat/serial", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 1, ShardWorkers: 1}},
	}
	for _, w := range []int{2, 3, 7} {
		a := bitExactArm{name: fmt.Sprintf("flat/decode=%d", w), cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: w, ShardWorkers: 1}}
		if parallel {
			a.arm, a.workers = observe.ArmParallelDecode, w
		}
		arms = append(arms, a)
	}
	for _, w := range []int{1, 2, 3, 8} {
		a := bitExactArm{name: fmt.Sprintf("archive/shards=%d", w), cohort: "arch.pulse", opts: pulse.Options{ShardWorkers: w, DecodeWorkers: 1}}
		if parallel && w > 1 {
			a.arm, a.workers = observe.ArmShardParallel, w
		}
		arms = append(arms, a)
	}
	return arms
}

// bitExactRun runs req (rebuilt per call: Process normalises in place)
// on every arm and returns the responses in arm order, asserting each
// arm the case pins really ran.
func bitExactRun(t *testing.T, dir string, arms []bitExactArm, mk func(cohort string) *types.Request) []*types.Response {
	t.Helper()
	var out []*types.Response
	for _, a := range arms {
		rec := &execRecorder{}
		opts := a.opts
		opts.DataDir = dir
		p := execNew(t, opts, rec)
		resp, err := p.Process(context.Background(), mk(a.cohort))
		if err != nil {
			t.Fatalf("%s: Process: %v", a.name, err)
		}
		rec.mu.Lock()
		end := rec.ends[len(rec.ends)-1]
		rec.mu.Unlock()
		if a.arm != "" && (end.Arm != a.arm || (a.workers != 0 && end.Workers != a.workers)) {
			t.Fatalf("%s: ran arm %q with %d workers, want %q with %d — the arm is untested", a.name, end.Arm, end.Workers, a.arm, a.workers)
		}
		out = append(out, resp)
	}
	return out
}

// bitExactWelfordKeys are the component keys a weighted AGG_AVERAGE /
// AGG_WEIGHTED_MEAN reports from its Welford moments. They merge by
// Chan-Welford and are within ULP across arms, not bit-identical (the
// documented contract), so the gate lifts them out, checks them within
// bitExactWelfordTol, and compares everything else bit for bit.
var bitExactWelfordKeys = []string{"m2_weighted", "weighted_variance"}

const bitExactWelfordTol = 1e-12

// liftWelford removes the Welford keys from every component map of r
// and returns them by path.
func liftWelford(r *types.Response) map[string]float64 {
	out := map[string]float64{}
	lift := func(path string, m map[string]any) {
		for _, k := range bitExactWelfordKeys {
			if v, ok := m[k].(float64); ok {
				out[path+"."+k] = v
				delete(m, k)
			}
		}
	}
	c := r.Components
	if c == nil {
		return out
	}
	for i, e := range c.Aggregations {
		lift(fmt.Sprintf("agg[%d]", i), e.Operator)
		for j, g := range e.Groups {
			lift(fmt.Sprintf("agg[%d].group[%d]", i, j), g.Operator)
		}
	}
	if x := c.Crosstab; x != nil {
		for i, row := range x.CellComponents {
			for j, m := range row {
				lift(fmt.Sprintf("cell[%d][%d]", i, j), m)
			}
		}
		for i, m := range x.RowMarginComponents {
			lift(fmt.Sprintf("row_margin[%d]", i), m)
		}
		for i, m := range x.ColumnMarginComponents {
			lift(fmt.Sprintf("col_margin[%d]", i), m)
		}
		lift("grand", x.GrandTotalComponents)
	}
	return out
}

// bitExactCompare asserts every response equals the first (the serial
// single file): Data and Crosstab always, the aggregation, grouper and
// crosstab components always, and the whole Components between runs
// over the same cohort (Run.shard_count tells the cohorts apart).
func bitExactCompare(t *testing.T, arms []bitExactArm, resps []*types.Response) {
	t.Helper()
	ref := resps[0]
	if len(ref.Data) == 0 && ref.Crosstab == nil || ref.Components == nil {
		t.Fatal("reference run has no data or components: the gate proves nothing")
	}
	welford := make([]map[string]float64, len(resps))
	for i, r := range resps {
		welford[i] = liftWelford(r)
	}
	sameCohort := map[string]*types.Response{}
	for i, r := range resps {
		a := arms[i]
		if !reflect.DeepEqual(r.Data, ref.Data) {
			t.Errorf("%s: Data differs from %s\n got %s\nwant %s", a.name, arms[0].name, mustBitExactJSON(t, r.Data), mustBitExactJSON(t, ref.Data))
		}
		if !reflect.DeepEqual(r.Crosstab, ref.Crosstab) {
			t.Errorf("%s: Crosstab differs from %s\n got %s\nwant %s", a.name, arms[0].name, mustBitExactJSON(t, r.Crosstab), mustBitExactJSON(t, ref.Crosstab))
		}
		if r.Components == nil {
			t.Fatalf("%s: no components", a.name)
		}
		if !reflect.DeepEqual(r.Components.Aggregations, ref.Components.Aggregations) {
			t.Errorf("%s: Components.Aggregations differ\n got %s\nwant %s", a.name,
				mustBitExactJSON(t, r.Components.Aggregations), mustBitExactJSON(t, ref.Components.Aggregations))
		}
		if !reflect.DeepEqual(r.Components.Groupers, ref.Components.Groupers) {
			t.Errorf("%s: Components.Groupers differ", a.name)
		}
		if !reflect.DeepEqual(r.Components.Crosstab, ref.Components.Crosstab) {
			t.Errorf("%s: Components.Crosstab differ\n got %s\nwant %s", a.name,
				mustBitExactJSON(t, r.Components.Crosstab), mustBitExactJSON(t, ref.Components.Crosstab))
		}
		if same, ok := sameCohort[a.cohort]; !ok {
			sameCohort[a.cohort] = r
		} else if !reflect.DeepEqual(r.Components, same.Components) {
			t.Errorf("%s: Components differ from the same cohort's first run\n got %s\nwant %s", a.name,
				mustBitExactJSON(t, r.Components), mustBitExactJSON(t, same.Components))
		}
		if len(welford[i]) != len(welford[0]) {
			t.Errorf("%s: %d Welford keys, reference %d", a.name, len(welford[i]), len(welford[0]))
		}
		for k, w := range welford[0] {
			if g := welford[i][k]; math.Abs(g-w) > bitExactWelfordTol*math.Max(1, math.Abs(w)) {
				t.Errorf("%s: Welford %s = %v, reference %v (beyond %g relative)", a.name, k, g, w, bitExactWelfordTol)
			}
		}
	}
}

func mustBitExactJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bitExactPW is the non-dyadic probability weight.
func bitExactPW() types.SlotWeight {
	return types.SlotWeightOf(types.WeightSpec{Field: "pw", Kind: types.WeightKindProbability})
}

// bitExactAggs is every reproducible-sum slot: unweighted, and weighted
// by the non-dyadic probability weight pw.
func bitExactAggs() []*types.Aggregation {
	ratio, _ := json.Marshal(map[string]string{"numerator_field": "x", "denominator_field": "y"})
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "x", Label: "s"},
		{Type: types.AGG_AVERAGE, Field: "x", Label: "a"},
		{Type: types.AGG_RATIO, Field: "x", Label: "r", Params: ratio},
		{Type: types.AGG_SUM, Field: "x", Label: "ws", Weight: bitExactPW()},
		{Type: types.AGG_AVERAGE, Field: "x", Label: "wa", Weight: bitExactPW()},
		{Type: types.AGG_COUNT, Field: "x", Label: "wn", Weight: bitExactPW()},
		{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Label: "wm", Weight: bitExactPW()},
	}
}

// TestBitExact_ScalarSums: the reproducible-sum slots, ungrouped and
// grouped, answer identical bits on every worker count and shard split.
func TestBitExact_ScalarSums(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := bitExactArms(true)
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: bitExactAggs()}
				if grouped {
					req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				}
				return req
			})
			// The figures are real: a non-dyadic, non-integral sum, and
			// the probability floor's sum_weights / n_eff present.
			if s, _ := resps[0].Data[0]["s"].(float64); s == 0 || s == math.Trunc(s) {
				t.Fatalf("sum %v is zero or integral: the fixture is not mixed-magnitude", s)
			}
			for _, e := range resps[0].Components.Aggregations {
				if e.Label == "ws" && (e.SumWeights == nil || e.NEff == nil) {
					t.Fatalf("weighted slot %s carries no sum_weights / n_eff floor", e.Label)
				}
			}
			bitExactCompare(t, arms, resps)
		})
	}
}

// TestBitExact_StreamingVsBuffered: the reproducible-sum slots on the
// buffered arm (an AGG_MEDIAN beside them forces it) equal the
// streaming arm's figures bit for bit, on the single file and the
// archive.
func TestBitExact_StreamingVsBuffered(t *testing.T) {
	t.Parallel()

	dir := bitExactCohorts(t)
	arms := []bitExactArm{
		{name: "flat/streaming", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 1, ShardWorkers: 1}, arm: observe.ArmStreaming},
		{name: "flat/buffered", cohort: "flat.pulse", opts: pulse.Options{DecodeWorkers: 1, ShardWorkers: 1}, arm: observe.ArmBuffered},
		{name: "archive/streaming", cohort: "arch.pulse", opts: pulse.Options{DecodeWorkers: 1, ShardWorkers: 1}, arm: observe.ArmStreaming},
		{name: "archive/buffered", cohort: "arch.pulse", opts: pulse.Options{DecodeWorkers: 1, ShardWorkers: 1}, arm: observe.ArmBuffered},
	}
	call := 0
	resps := bitExactRun(t, dir, arms, func(cohort string) *types.Request {
		req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: bitExactAggs(),
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}}
		if call%2 == 1 {
			req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "x", Label: "med"})
		}
		call++
		return req
	})
	for i, r := range resps {
		if i%2 == 0 {
			continue
		}
		// Drop the median (it exists only to force the buffered arm).
		for _, row := range r.Data {
			delete(row, "med")
		}
		aggs := r.Components.Aggregations
		r.Components.Aggregations = aggs[:len(aggs)-1]
	}
	bitExactCompare(t, arms, resps)
}
