package descriptor

import (
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

func memIn(records int64) LimitInputs {
	return LimitInputs{Records: records, JoinRightRows: -1}
}

func sumOf(field string) []*types.Aggregation {
	return []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: "s"}}
}

// TestMemoryArm: the arm the estimate sizes is the engine's dispatch —
// streaming iff streamable, buffered otherwise; a crosstab fused iff
// the shared fusion rule accepts and neither a join nor the instance
// switch declines it.
func TestMemoryArm(t *testing.T) {
	schema := groupsSchema(t)
	xt := &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "flag"}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "num", Label: "n"},
	}
	cases := []struct {
		name string
		req  *types.Request
		in   LimitInputs
		want limits.MemoryArm
	}{
		{"streamable", &types.Request{Aggregations: sumOf("num")}, memIn(10), limits.ArmStreaming},
		{"order statistic", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}}, memIn(10), limits.ArmBuffered},
		{"fused crosstab", &types.Request{Crosstab: xt}, memIn(10), limits.ArmFusedCrosstab},
		{"fusion disabled", &types.Request{Crosstab: xt}, LimitInputs{Records: 10, JoinRightRows: -1, FusionDisabled: true}, limits.ArmBuffered},
		{"joined crosstab", &types.Request{Crosstab: xt}, LimitInputs{Records: 10, Join: true, JoinRightRows: 5}, limits.ArmBuffered},
	}
	for _, c := range cases {
		if got := MemoryArm(c.req, schema, nil, c.in); got != c.want {
			t.Errorf("%s: arm %q, want %q", c.name, got, c.want)
		}
	}
}

// The bucket charges (limits.BucketBytes; U51 E2-S4, per-aggregator
// state since E5-S7) for one unweighted AGG_SUM: a merged bucket's
// state + finalize output (components on), and a parallel partition's
// state alone.
var (
	partitionBucket1, finalBucket1 = limits.BucketBytes(limits.MemoryInputs{
		Aggregations: 1, AggStateBytes: limits.AggregationStateBytes(types.AGG_SUM, false, 0)})
	// partitionBucketOf / finalBucketOf are those charges for one
	// unweighted slot of type at.
	partitionBucketOf = func(at types.AggregationType) int64 {
		p, _ := limits.BucketBytes(limits.MemoryInputs{Aggregations: 1, AggStateBytes: limits.AggregationStateBytes(at, false, 0)})
		return p
	}
	finalBucketOf = func(at types.AggregationType) int64 {
		_, f := limits.BucketBytes(limits.MemoryInputs{Aggregations: 1, AggStateBytes: limits.AggregationStateBytes(at, false, 0)})
		return f
	}
	// extraGrouperBytes is what each grouper past the first adds to a
	// bucket's state.
	extraGrouperBytes = func() int64 {
		a, _ := limits.BucketBytes(limits.MemoryInputs{ExtraGroupers: 1})
		b, _ := limits.BucketBytes(limits.MemoryInputs{})
		return a - b
	}()
)

// TestEstimateMemory_WeightAndSetTerms (U51 E2-S4): a weighted slot —
// its own weight, the request's, or the instance default reaching it
// through LimitInputs.DefaultWeight; never one opted out with `null` —
// costs its weighted table entry (AGG_SUM: 576 B of state, not 160), and an
// AGG_SET_FREQUENCY slot adds a counter and an output label per member
// of its set field (3 here: 64 + 128 B each) to every bucket.
func TestEstimateMemory_WeightAndSetTerms(t *testing.T) {
	schema := groupsSchema(t)
	est := func(req *types.Request, in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(req, schema, nil, in)
		if !ok {
			t.Fatalf("no estimate for %+v", in)
		}
		return n
	}
	byCat := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}} // 5 buckets
	sum := func(w types.SlotWeight) []*types.Aggregation {
		return []*types.Aggregation{{Type: types.AGG_SUM, Field: "num", Label: "s", Weight: w}}
	}
	unweighted := est(&types.Request{Groups: byCat, Aggregations: sum(types.SlotWeight{})}, memIn(1000))
	extra := 5 * (limits.AggregationStateBytes(types.AGG_SUM, true, 0) - limits.AggregationStateBytes(types.AGG_SUM, false, 0))
	cases := []struct {
		name string
		req  *types.Request
		def  *types.WeightSpec
		want int64
	}{
		{"slot weight", &types.Request{Groups: byCat, Aggregations: sum(types.SlotWeightField("num"))}, nil, extra},
		{"request weight", &types.Request{Groups: byCat, Aggregations: sum(types.SlotWeight{}), Weight: &types.WeightSpec{Field: "num"}}, nil, extra},
		{"instance default", &types.Request{Groups: byCat, Aggregations: sum(types.SlotWeight{})}, &types.WeightSpec{Field: "num"}, extra},
		{"null opts out", &types.Request{Groups: byCat, Aggregations: sum(types.NullSlotWeight()), Weight: &types.WeightSpec{Field: "num"}}, nil, 0},
	}
	for _, c := range cases {
		in := memIn(1000)
		in.DefaultWeight = c.def
		if d := est(c.req, in) - unweighted; d != c.want {
			t.Errorf("%s: adds %d bytes, want %d", c.name, d, c.want)
		}
	}
	setFreq := &types.Request{Groups: byCat, Aggregations: []*types.Aggregation{{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "f"}}}
	setCard := &types.Request{Groups: byCat, Aggregations: []*types.Aggregation{{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "f"}}}
	// Beside the two slots' own state entries.
	slots := 5 * (limits.AggregationStateBytes(types.AGG_SET_FREQUENCY, false, 0) - limits.AggregationStateBytes(types.AGG_SET_CARDINALITY_SUM, false, 0))
	if d := est(setFreq, memIn(1000)) - est(setCard, memIn(1000)) - slots; d != 5*3*(64+128) {
		t.Errorf("AGG_SET_FREQUENCY over a 3-member set adds %d bytes, want 5 buckets x 3 members x 192 (%d)", d, 5*3*(64+128))
	}
}

// TestEstimateMemory_Terms: each request-shaped figure reaches the
// leaf formula — the record-scaled payload, the group bound, a join's
// build side, the per-shard matrix blocks — and an unknown record
// count (or right count) yields no estimate.
func TestEstimateMemory_Terms(t *testing.T) {
	schema := groupsSchema(t)
	stride := int64(schema.RecordByteSize())
	rec := limits.RecordBytes(schema)
	est := func(req *types.Request, in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(req, schema, nil, in)
		if !ok {
			t.Fatalf("no estimate for %+v", in)
		}
		return n
	}
	plain := &types.Request{Aggregations: sumOf("num")}
	base := est(plain, memIn(1000))
	if d := est(plain, memIn(2000)) - base; d != 1000*3*stride {
		t.Errorf("1,000 more streamed records add %d bytes, want the payload %d", d, 1000*3*stride)
	}
	// Grouped by a 5-entry dictionary: four more buckets of one
	// aggregation each — a merged bucket's state (256 + 256) plus its
	// finalize output (1,024 + 512).
	grouped := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: sumOf("num")}
	if d := est(grouped, memIn(1000)) - base; d != 4*finalBucket1 {
		t.Errorf("grouping adds %d bytes, want 4 buckets (%d)", d, 4*finalBucket1)
	}
	// A grouper of unknown cardinality is bounded by the records.
	ranged := &types.Request{Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}}, Aggregations: sumOf("num")}
	if d := est(ranged, memIn(1000)) - base; d != 999*finalBucket1 {
		t.Errorf("unknown grouper adds %d bytes, want 999 buckets", d)
	}
	// A join adds its build side.
	joined := LimitInputs{Records: 1000, Join: true, JoinRightRows: 300}
	if d := est(plain, joined) - base; d != 300*(3*stride+2*rec) {
		t.Errorf("a 300-row build side adds %d bytes, want %d", d, 300*(3*stride+2*rec))
	}
	// Buffered: every record resident.
	median := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}}
	// (beside the median slot's bucket state in place of the sum's)
	if d := est(median, memIn(1000)) - base - (finalBucketOf(types.AGG_MEDIAN) - finalBucket1); d != 1000*(rec+16) {
		t.Errorf("the buffered arm adds %d bytes, want %d", d, 1000*(rec+16))
	}
	// Matrix: merge fragments x buckets x accumulator bytes (2 numeric
	// members, listwise: 40 + 8·(2+3) = 80).
	numeric := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}, {Name: "y", Type: encoding.FieldTypeF64}}}
	mat := &types.Request{Aggregations: sumOf("x"), Matrices: []types.MatrixSpec{{Name: "c", Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}}}
	matEst := func(in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(mat, numeric, nil, in)
		if !ok {
			t.Fatal("no matrix estimate")
		}
		return n
	}
	single := matEst(memIn(limits.MergeBlockSize + 6))
	// ShardWorkers 1: the serial archive arm, so no per-shard partials
	// (TestEstimateMemory_ArchiveTerms covers those).
	shardedIn := LimitInputs{Records: limits.MergeBlockSize + 6, ShardRecords: []int64{limits.MergeBlockSize + 1, 5}, JoinRightRows: -1, ShardWorkers: 1}
	sharded := matEst(shardedIn)
	// The archive terms (TestEstimateMemory_ArchiveTerms) move the
	// matrix-free request alike; only the matrix state differs.
	plainEst := func(in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(&types.Request{Aggregations: sumOf("x")}, numeric, nil, in)
		if !ok {
			t.Fatal("no estimate")
		}
		return n
	}
	archive := plainEst(shardedIn) - plainEst(memIn(limits.MergeBlockSize+6))
	// One more fragment (80) plus the 5 rows shard 1 buffers because it
	// starts inside block 1 (x, y and the weight: 24 bytes each).
	if d := sharded - single - archive; d != 80+5*24 {
		t.Errorf("4,097 + 5 sharded rows hold one more fragment and 5 buffered rows than 4,102 in one file: +%d bytes, want %d", d, 80+5*24)
	}

	// Fan-out (U35 #267): a GROUP_SET_PER_ELEMENT record reaches up to
	// 3 buckets (the set dictionary), so its distinct (bucket, value)
	// pairs reach 3 x records.
	distinct := []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "num", Label: "d"}}
	tags := []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}}
	if arm := MemoryArm(&types.Request{Groups: tags, Aggregations: distinct}, schema, nil, memIn(1000)); arm != limits.ArmStreaming {
		t.Fatalf("set-per-element distinct count runs %q", arm)
	}
	if d := est(&types.Request{Groups: tags, Aggregations: distinct}, memIn(1000)) - est(&types.Request{Aggregations: distinct}, memIn(1000)); d != 2*finalBucketOf(types.AGG_DISTINCT_COUNT)+2*1000*96 {
		t.Errorf("fan-out value state adds %d bytes, want 2 more buckets and 2 x 1,000 more value entries (%d)", d, 2*finalBucketOf(types.AGG_DISTINCT_COUNT)+2*1000*96)
	}
	// Tier-1 tests (U35 #437): an instance per test per bucket (5
	// categories), its split levels (packed_bool: 2 per bucket), and a
	// buffered rank test's values (num + the weight, per record).
	byCat := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	medianByCat := func(tests ...*types.Test) *types.Request {
		return &types.Request{Groups: byCat, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}, Tests: tests}
	}
	noTests := est(medianByCat(), memIn(1000))
	if d := est(medianByCat(&types.Test{Type: types.TEST_T, Field: "num", SplitBy: "flag"}), memIn(1000)) - noTests; d != 5*1024+10*256 {
		t.Errorf("a grouped TEST_T adds %d bytes, want 5 instances + 10 levels (%d)", d, 5*1024+10*256)
	}
	if d := est(medianByCat(&types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "num", SplitBy: "flag"}), memIn(1000)) - noTests; d != 5*1024+10*256+1000*2*32 {
		t.Errorf("a grouped TEST_MANN_WHITNEY_U adds %d bytes, want instances + levels + 2 kept values a record (%d)", d, 5*1024+10*256+1000*2*32)
	}
	// A split field the schema cannot bound: its levels are bounded by
	// the rows the test admits.
	if d := est(medianByCat(&types.Test{Type: types.TEST_T, Field: "num", SplitBy: "num"}), memIn(1000)) - noTests; d != 5*1024+1000*256 {
		t.Errorf("an unbounded split adds %d bytes, want 5 instances + 1,000 levels (%d)", d, 5*1024+1000*256)
	}
	// A buffered matrix under the fan-out grouper keeps one row copy
	// per bucket: 3 x records x 8·(p + 1), against the same request
	// grouped by a 3-entry categorical (equal buckets and merge state).
	fan := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64}, {Name: "y", Type: encoding.FieldTypeF64},
		schema.Fields[1], // tags, set_u8 over x/y/z
		{Name: "three", Type: encoding.FieldTypeCategoricalU8, Dictionary: schema.Fields[1].Dictionary},
	}}
	rho := func(g *types.Group) *types.Request {
		return &types.Request{Groups: []*types.Group{g}, Matrices: []types.MatrixSpec{{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x", "y"}, Params: []byte(`{"method": "spearman"}`)}}}
	}
	fanEst := func(req *types.Request) int64 {
		t.Helper()
		n, ok := EstimateMemory(req, fan, nil, memIn(1000))
		if !ok {
			t.Fatal("no fan-out matrix estimate")
		}
		return n
	}
	setReq, catReq := rho(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}), rho(&types.Group{Type: types.GROUP_CATEGORY, Field: "three"})
	if arm := MemoryArm(setReq, fan, nil, memIn(1000)); arm != limits.ArmBuffered {
		t.Fatalf("the rank matrix runs %q, want buffered", arm)
	}
	// Two more row copies a record (8·(2+1) bytes each, x10 for the
	// store's growth and the rank finalize: limits.MatrixRowWorkBytes),
	// plus the buffered arm's partition slot per extra bucket (24 bytes).
	if d, want := fanEst(setReq)-fanEst(catReq), int64(2*1000*24*10+2*1000*24); d != want {
		t.Errorf("the fan-out rank matrix adds %d bytes over the categorical one, want %d (two more row copies a record)", d, want)
	}

	if _, ok := EstimateMemory(plain, schema, nil, memIn(-1)); ok {
		t.Error("an unknown record count has an estimate")
	}
	if _, ok := EstimateMemory(plain, schema, nil, LimitInputs{Records: 10, Join: true, JoinRightRows: -1}); ok {
		t.Error("a join with an unknown build side has an estimate")
	}
}

// TestEstimateMemory_ArchiveTerms (E8-S3, re-pinned U51 E1-S2): over a
// shard archive the estimate charges what the runtime holds beyond its
// per-row state — the LARGEST shard's records x stride in place of the
// whole cohort's in the payload term (the archive streams one shard at a
// time, never resident), its central directory (8 x DirectoryBytes),
// the one schema block decoded with its dictionaries (the canonical
// entry, bounded by the canonical schema's decode; Open's record-count
// peek and the scan read every shard's layout-only, E8-S4 / E8-S5) —
// and, on the per-shard parallel arm only, each shard's partial buckets
// and per-value state. ShardWorkers decides that arm as the runtime
// does. An anchor-opened shard is a single file.
func TestEstimateMemory_ArchiveTerms(t *testing.T) {
	schema := groupsSchema(t)
	stride := int64(schema.RecordByteSize())
	decode := limits.SchemaDecodeBytes(schema)
	shards := []int64{600, 400}
	est := func(req *types.Request, in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(req, schema, nil, in)
		if !ok {
			t.Fatalf("no estimate for %+v", in)
		}
		return n
	}
	arch := func(dir int64, workers int) LimitInputs {
		return LimitInputs{Records: 1000, ShardRecords: shards, DirectoryBytes: dir, ShardWorkers: workers, JoinRightRows: -1}
	}
	plain := &types.Request{Aggregations: sumOf("num")}
	single := est(plain, memIn(1000))

	// Serial: the largest shard's payload (600 records) replaces the
	// cohort's (1,000), plus the directory and 1 decode (the canonical
	// entry; whatever the shard count).
	const dirBytes = 4096
	if d, want := est(plain, arch(dirBytes, 1))-single, -3*(1000-600)*stride+8*dirBytes+decode; d != want {
		t.Errorf("serial archive adds %d bytes, want 3 x (largest shard - cohort) payload + 8 x directory + 1 schema decode = %d", d, want)
	}
	// The shard count does not move the decode term, and more shards of
	// a smaller largest shard lower the payload term.
	many := arch(dirBytes, 1)
	many.ShardRecords = []int64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	if d, want := est(plain, many)-est(plain, arch(dirBytes, 1)), -3*(600-100)*stride; d != want {
		t.Errorf("10 shards of 100 vs 600 + 400: %d, want %d (the largest shard's payload only)", d, want)
	}
	// An anchor-opened shard (no shard counts): a single file — no
	// directory, no extra decode.
	anchor := LimitInputs{Records: 1000, DirectoryBytes: dirBytes, ShardWorkers: 1, JoinRightRows: -1}
	if d := est(plain, anchor) - single; d != 0 {
		t.Errorf("an anchor-opened shard adds %d bytes, want 0", d)
	}

	// Fan-out (2 workers over 2 shards, a mergeable request): one
	// bucket per shard partial ungrouped ...
	serial := est(plain, arch(dirBytes, 1))
	if d := est(plain, arch(dirBytes, 2)) - serial; d != 2*partitionBucket1 {
		t.Errorf("fan-out adds %d bytes, want 2 partial buckets (%d)", d, 2*partitionBucket1)
	}
	// ... each shard's own bound grouped (a 5-entry dictionary: 5 each)
	// ...
	grouped := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: sumOf("num")}
	if d := est(grouped, arch(dirBytes, 0)) - est(grouped, arch(dirBytes, 1)); runtime.NumCPU() >= 2 && d != 10*partitionBucket1 {
		t.Errorf("grouped fan-out adds %d bytes, want 10 partial buckets (%d)", d, 10*partitionBucket1)
	}
	// ... clamped by the shard's records for an unknown grouper ...
	ranged := &types.Request{Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}}, Aggregations: sumOf("num")}
	if d := est(ranged, arch(dirBytes, 2)) - est(ranged, arch(dirBytes, 1)); d != 1000*partitionBucket1 {
		t.Errorf("unknown-grouper fan-out adds %d bytes, want 600 + 400 partial buckets", d)
	}
	// ... and per-value state over every record once more.
	distinct := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "num", Label: "d"}}}
	if d := est(distinct, arch(dirBytes, 2)) - est(distinct, arch(dirBytes, 1)); d != 2*partitionBucketOf(types.AGG_DISTINCT_COUNT)+1000*96 {
		t.Errorf("value-state fan-out adds %d bytes, want 2 buckets + 1,000 x 96", d)
	}
	// U51 E2-S4: the incremental merge holds at most the merge window's
	// shards (limits.ShardMergeWindow: 2 x workers) beside the merged
	// state, so on 12 shards under 2 workers only the 4 LARGEST shards'
	// partials are charged — bucket bounds and per-value records alike
	// — never all 12.
	wide := LimitInputs{Records: 1000, ShardRecords: []int64{50, 300, 10, 90, 40, 200, 60, 70, 20, 80, 30, 50}, ShardWorkers: 2, JoinRightRows: -1}
	wideSerial := wide
	wideSerial.ShardWorkers = 1
	if d, want := est(ranged, wide)-est(ranged, wideSerial), int64((300+200+90+80)*partitionBucket1); d != want {
		t.Errorf("12 shards under 2 workers add %d bytes, want the window's 4 largest shards' buckets (%d)", d, want)
	}
	if d, want := est(distinct, wide)-est(distinct, wideSerial), 4*partitionBucketOf(types.AGG_DISTINCT_COUNT)+(300+200+90+80)*96; d != want {
		t.Errorf("12-shard value state adds %d bytes, want 4 partial buckets + the 4 largest shards' records x 96 (%d)", d, want)
	}
	// A request the merge gate refuses stays serial at any cap.
	median := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}}
	if a, b := est(median, arch(dirBytes, 2)), est(median, arch(dirBytes, 1)); a != b {
		t.Errorf("a non-mergeable request moved with ShardWorkers: %d vs %d", a, b)
	}

	// The rich facet charges the same archive terms.
	facet := &types.FacetRequest{Fields: []string{"cat"}}
	fs, _ := EstimateFacetMemory(facet, schema, memIn(1000))
	fa, _ := EstimateFacetMemory(facet, schema, arch(dirBytes, 1))
	if d, want := fa-fs, -3*(1000-600)*stride+8*dirBytes+decode; d != want {
		t.Errorf("facet over an archive adds %d bytes, want %d", d, want)
	}
}

// TestShardFanOut: the per-shard parallel arm needs two or more shards,
// a worker cap resolving to two or more (0 = NumCPU, clamped by the
// shard count) and a request the merge gate admits; a crosstab or a
// join never reaches that dispatch.
func TestShardFanOut(t *testing.T) {
	schema := groupsSchema(t)
	plain := &types.Request{Aggregations: sumOf("num")}
	in := func(workers int, shards ...int64) LimitInputs {
		return LimitInputs{Records: 10, ShardRecords: shards, ShardWorkers: workers, JoinRightRows: -1}
	}
	cases := []struct {
		name string
		req  *types.Request
		in   LimitInputs
		want bool
	}{
		{"two workers, two shards", plain, in(2, 5, 5), true},
		{"serial cap", plain, in(1, 5, 5), false},
		{"one shard", plain, in(4, 10), false},
		{"single file", plain, in(4), false},
		{"default cap", plain, in(0, 5, 5), runtime.NumCPU() >= 2},
		{"non-mergeable", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}}, in(2, 5, 5), false},
		{"crosstab", &types.Request{Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "flag"}}}}, in(2, 5, 5), false},
		{"join", plain, LimitInputs{Records: 10, ShardRecords: []int64{5, 5}, ShardWorkers: 2, Join: true, JoinRightRows: 3}, false},
	}
	for _, c := range cases {
		if got := ShardFanOut(c.req, schema, nil, c.in); got != c.want {
			t.Errorf("%s: fan-out %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRequestLimitFindings_MaxEstimatedMemoryCertain: a set
// MaxEstimatedMemory below the estimate is a CERTAIN finding (and the
// pre-flight refusal); at the estimate, under the default (Unlimited)
// and without a record count there is none.
func TestRequestLimitFindings_MaxEstimatedMemoryCertain(t *testing.T) {
	schema := groupsSchema(t)
	req := &types.Request{Aggregations: sumOf("num")}
	n, ok := EstimateMemory(req, schema, nil, memIn(1000))
	if !ok {
		t.Fatal("no estimate")
	}
	l := limits.Defaults()
	if fs := RequestLimitFindings(req, schema, nil, l, memIn(1000)); len(fs) != 0 {
		t.Fatalf("default (unlimited) memory limit: findings %+v", fs)
	}
	l.MaxEstimatedMemory = n - 1
	want := limits.Finding{Limit: limits.MaxEstimatedMemory, Configured: n - 1, Estimated: n, Grade: limits.Certain}
	if fs := RequestLimitFindings(req, schema, nil, l, memIn(1000)); len(fs) != 1 || fs[0] != want {
		t.Fatalf("findings %+v, want [%+v]", fs, want)
	}
	err := LimitRefusal(req, schema, nil, l, memIn(1000))
	if err == nil || err.Details["limit"] != "max_estimated_memory" || err.Details["observed"] != n {
		t.Fatalf("pre-flight refusal = %v", err)
	}
	if fs := RequestLimitFindings(req, schema, nil, l, memIn(-1)); len(fs) != 0 {
		t.Fatalf("unknown record count: findings %+v", fs)
	}
	l.MaxEstimatedMemory = n
	if fs := RequestLimitFindings(req, schema, nil, l, memIn(1000)); len(fs) != 0 {
		t.Fatalf("at the estimate: findings %+v", fs)
	}
}

// TestPredict_MaxEstimatedMemory: predict reads the header count and
// reports the certain finding with the PULSE_LIMIT_EXCEEDED error, so
// Valid is false; under the default there is no finding.
func TestPredict_MaxEstimatedMemory(t *testing.T) {
	data := vectorPredictCohort(t, 500)
	req := func() *types.Request { return &types.Request{Aggregations: sumOf("q_1")} }
	pr := func(l limits.Limits) (*descriptor.PredictResult, *descriptor.Envelope) {
		env := predictFromBytes(data, req(), &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithLimits(l)})
		return env.Data.(*descriptor.PredictResult), env
	}
	res, _ := pr(limits.Defaults())
	if !res.Valid || res.LimitFindings != nil {
		t.Fatalf("defaults: valid=%v findings=%+v", res.Valid, res.LimitFindings)
	}
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1
	res, env := pr(l)
	if res.Valid || len(res.LimitFindings) != 1 {
		t.Fatalf("limit 1: valid=%v findings=%+v", res.Valid, res.LimitFindings)
	}
	f := res.LimitFindings[0]
	if f.Limit != "max_estimated_memory" || f.Grade != descriptor.LimitGradeCertain || f.Configured != 1 {
		t.Fatalf("finding %+v", f)
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != "PULSE_LIMIT_EXCEEDED" {
		t.Fatalf("errors %+v", env.Errors)
	}
	// The estimate scales with the header count the file length gives.
	big := vectorPredictCohort(t, 1500)
	env = predictFromBytes(big, req(), &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithLimits(l)})
	if g := env.Data.(*descriptor.PredictResult).LimitFindings[0].Estimated; g <= f.Estimated {
		t.Fatalf("1,500 records estimate %d, 500 records %d", g, f.Estimated)
	}
}

// TestFacetLimitRefusal: a rich facet's memory estimate — one bucket
// per faceted field, per-value state for a dictionary-less field (twice
// with percentiles) — refuses only under a set limit.
func TestFacetLimitRefusal(t *testing.T) {
	schema := groupsSchema(t)
	req := &types.FacetRequest{Fields: []string{"cat", "num"}}
	n, ok := EstimateFacetMemory(req, schema, memIn(1000))
	if !ok {
		t.Fatal("no estimate")
	}
	pct := &types.FacetRequest{Fields: []string{"cat", "num"}, NumericPercentiles: []float64{0.5}}
	if m, _ := EstimateFacetMemory(pct, schema, memIn(1000)); m-n != 1000*96 {
		t.Errorf("percentiles add %d bytes, want one more value slot (%d)", m-n, 1000*96)
	}
	catOnly := &types.FacetRequest{Fields: []string{"cat"}, AdditiveFields: []string{"flag"}}
	if m, _ := EstimateFacetMemory(catOnly, schema, memIn(1000)); n-m != 1000*96 {
		t.Errorf("a dictionary field swapped for a numeric differs by %d, want %d", n-m, 1000*96)
	}
	l := limits.Defaults()
	if err := FacetLimitRefusal(req, schema, l, memIn(1000)); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
	l.MaxEstimatedMemory = n - 1
	if err := FacetLimitRefusal(req, schema, l, memIn(1000)); err == nil || err.Details["observed"] != n {
		t.Fatalf("refusal = %v, want observed %d", err, n)
	}
	l.MaxEstimatedMemory = n
	if err := FacetLimitRefusal(req, schema, l, memIn(1000)); err != nil {
		t.Fatalf("at the estimate: %v", err)
	}
	if err := FacetLimitRefusal(req, schema, limits.Limits{MaxEstimatedMemory: 1}, memIn(-1)); err != nil {
		t.Fatalf("unknown record count refused: %v", err)
	}
}

// TestFacetGroupBound: a rich facet counts each faceted field's
// distinct values against MaxGroups separately, so the bound is the
// largest per-field one — a categorical field's dictionary size, 2 for
// packed_bool, clamped by a known record count — and a numeric field
// mints none. FacetLimitFindings grades it possible, beside the certain
// memory estimate.
func TestFacetGroupBound(t *testing.T) {
	schema := groupsSchema(t)
	cases := []struct {
		name     string
		req      *types.FacetRequest
		records  int64
		want     int64
		wantSome bool
	}{
		{"categorical", &types.FacetRequest{Fields: []string{"cat"}}, -1, 5, true},
		{"largest field wins", &types.FacetRequest{Fields: []string{"flag"}, AdditiveFields: []string{"cat"}}, -1, 5, true},
		{"packed_bool", &types.FacetRequest{Fields: []string{"flag", "num"}}, -1, 2, true},
		{"clamped by records", &types.FacetRequest{Fields: []string{"cat"}}, 3, 3, true},
		{"numeric mints none", &types.FacetRequest{Fields: []string{"num", "day"}}, -1, 0, false},
		{"unknown field", &types.FacetRequest{Fields: []string{"nope"}}, -1, 0, false},
	}
	for _, c := range cases {
		got, ok := FacetGroupBound(c.req, schema, c.records)
		if ok != c.wantSome || got != c.want {
			t.Errorf("%s: bound = %d, %v; want %d, %v", c.name, got, ok, c.want, c.wantSome)
		}
	}

	req := &types.FacetRequest{Fields: []string{"cat", "num"}}
	l := limits.Defaults()
	l.MaxGroups = 4
	fs := FacetLimitFindings(req, schema, l, memIn(1000))
	if len(fs) != 1 || fs[0].Limit != limits.MaxGroups || fs[0].Grade != limits.Possible || fs[0].Estimated != 5 {
		t.Fatalf("findings %+v, want one possible MaxGroups of 5", fs)
	}
	if err := FacetLimitRefusal(req, schema, l, memIn(1000)); err != nil {
		t.Fatalf("a possible finding refused: %v", err)
	}
	l.MaxEstimatedMemory = 1
	fs = FacetLimitFindings(req, schema, l, memIn(1000))
	if len(fs) != 2 || fs[1].Limit != limits.MaxEstimatedMemory || fs[1].Grade != limits.Certain {
		t.Fatalf("findings %+v, want MaxGroups then a certain memory finding", fs)
	}
}

// TestEstimateMemory_ValueWidthBound (U51 E5-S6): a grouper of
// data-dependent cardinality whose key is a function of one integer or
// set word is charged at most 2^bits buckets (valueKeyBound), never
// more than the records; a float, date or 64-bit field, and a fan-out
// run, keep the record bound — times the fan-out multiplicity, the
// composite keys one record can mint.
func TestEstimateMemory_ValueWidthBound(t *testing.T) {
	schema := groupsSchema(t)
	schema.Fields = append(schema.Fields,
		encoding.Field{Name: "nib", Type: encoding.FieldTypeU4},
		encoding.Field{Name: "small", Type: encoding.FieldTypeU8},
		encoding.Field{Name: "mid", Type: encoding.FieldTypeU16},
		encoding.Field{Name: "big", Type: encoding.FieldTypeU32},
		encoding.Field{Name: "huge", Type: encoding.FieldTypeU64},
		encoding.Field{Name: "mask", Type: encoding.FieldTypeSetU8},
	)
	sum := sumOf("num")
	buckets := func(records int64, groups ...*types.Group) int64 {
		t.Helper()
		base, ok := EstimateMemory(&types.Request{Aggregations: sum}, schema, nil, memIn(records))
		if !ok {
			t.Fatal("no estimate")
		}
		n, ok := EstimateMemory(&types.Request{Groups: groups, Aggregations: sum}, schema, nil, memIn(records))
		if !ok {
			t.Fatal("no estimate")
		}
		// base holds one ungrouped bucket; each grouped bucket also
		// carries the composite key's extra groupers.
		final := finalBucket1 + int64(len(groups)-1)*extraGrouperBytes
		d := n - base + finalBucket1
		if d%final != 0 {
			t.Fatalf("%v: grouped delta %d is not whole buckets (arm %s)", groups[0].Type, d, MemoryArm(&types.Request{Groups: groups, Aggregations: sum}, schema, nil, memIn(records)))
		}
		return d / final
	}
	g := func(typ types.GroupType, field string) *types.Group {
		return &types.Group{Type: typ, Field: field, Interval: 10}
	}
	cases := []struct {
		name    string
		records int64
		groups  []*types.Group
		want    int64
	}{
		{"category over u4", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "nib")}, 16},
		{"category over u8", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "small")}, 256},
		{"category over u8, fewer records", 100, []*types.Group{g(types.GROUP_CATEGORY, "small")}, 100},
		{"category over u16", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "mid")}, 65_536},
		{"category over u32", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "big")}, 100_000},
		{"category over u64", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "huge")}, 100_000},
		{"category over f64", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "num")}, 100_000},
		{"rounded over u8", 100_000, []*types.Group{g(types.GROUP_ROUNDED, "small")}, 256},
		{"range over u8", 100_000, []*types.Group{g(types.GROUP_RANGE, "small")}, 256},
		{"category over date", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "day")}, 100_000},
		{"set value over set_u8", 100_000, []*types.Group{g(types.GROUP_SET_VALUE, "mask")}, 256},
		// A known dictionary times a width bound: 5 x 256.
		{"dictionary x u8", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "cat"), g(types.GROUP_CATEGORY, "small")}, 5 * 256},
		{"u8 x f64", 100_000, []*types.Group{g(types.GROUP_CATEGORY, "small"), g(types.GROUP_RANGE, "num")}, 100_000},
		// Fan-out: 3 members x 256 values bound it below 3 x records…
		{"fan-out x u8", 100_000, []*types.Group{g(types.GROUP_SET_PER_ELEMENT, "tags"), g(types.GROUP_CATEGORY, "small")}, 3 * 256},
		// …and with an unbounded position each record still mints up to
		// 3 composite keys.
		{"fan-out x f64", 1_000, []*types.Group{g(types.GROUP_SET_PER_ELEMENT, "tags"), g(types.GROUP_RANGE, "num")}, 3 * 1_000},
	}
	for _, c := range cases {
		if got := buckets(c.records, c.groups...); got != c.want {
			t.Errorf("%s: charged %d buckets, want %d", c.name, got, c.want)
		}
	}
}

// TestEstimateMemory_ComponentsOffOutput (U51 E5-S7): a run with
// components off is charged the components-off finalize output — the
// row alone, 512 + each slot's off column — in place of 1024 + 576 a
// slot, per merged bucket. The flag resolves exactly as the runtime's
// components gate does (EffectiveDisableComponents): the request's
// disable_components when set, else the instance default
// LimitInputs.DisableComponents. A partition's state is unchanged.
func TestEstimateMemory_ComponentsOffOutput(t *testing.T) {
	schema := groupsSchema(t)
	req := func(disable *bool) *types.Request {
		return &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, // 5 buckets
			Aggregations: sumOf("num"), DisableComponents: disable}
	}
	est := func(r *types.Request, engineOff bool) int64 {
		t.Helper()
		in := memIn(1000)
		in.DisableComponents = engineOff
		n, ok := EstimateMemory(r, schema, nil, in)
		if !ok {
			t.Fatal("no estimate")
		}
		return n
	}
	_, on := limits.BucketBytes(limits.MemoryInputs{Aggregations: 1, AggStateBytes: limits.AggregationStateBytes(types.AGG_SUM, false, 0)})
	_, off := limits.BucketBytes(limits.MemoryInputs{Aggregations: 1, AggStateBytes: limits.AggregationStateBytes(types.AGG_SUM, false, 0),
		ComponentsOff: true, AggOutputOffBytes: limits.AggregationOutputOffBytes(types.AGG_SUM, 0)})
	if on-off != (1024+576)-(512+96) {
		t.Fatalf("a bucket's components share is %d, want %d", on-off, (1024+576)-(512+96))
	}
	yes, no := true, false
	want := est(req(nil), false) - 5*(on-off)
	for _, c := range []struct {
		name      string
		disable   *bool
		engineOff bool
		off       bool
	}{
		{"engine on", nil, false, false},
		{"engine off", nil, true, true},
		{"request off", &yes, false, true},
		{"request on over engine off", &no, true, false},
	} {
		got := est(req(c.disable), c.engineOff)
		if c.off && got != want {
			t.Errorf("%s: estimate %d, want the components-off %d", c.name, got, want)
		}
		if !c.off && got != want+5*(on-off) {
			t.Errorf("%s: estimate %d, want the components-on %d", c.name, got, want+5*(on-off))
		}
	}
}

// TestEstimateMemory_VectorAggregationTerms (U51 E5-S7): a vector
// aggregator slot is charged its fixed state per bucket (its table entry
// plus a member slope) and, once per run, a fragment per merge block it
// can hold — min(merge blocks x buckets, rows) — plus the rows a shard
// starting inside a block buffers.
func TestEstimateMemory_VectorAggregationTerms(t *testing.T) {
	schema := groupsSchema(t)
	schema.Fields = append(schema.Fields, encoding.Field{Name: "num2", Type: encoding.FieldTypeF64})
	vec := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Fields: []string{"num", "num2"}, Label: "v"}}}
	sum := &types.Request{Aggregations: sumOf("num")}
	est := func(r *types.Request, in LimitInputs) int64 {
		t.Helper()
		n, ok := EstimateMemory(r, schema, nil, in)
		if !ok {
			t.Fatal("no estimate")
		}
		return n
	}
	state := limits.AggregationStateBytes(types.AGG_VEC_MEAN, false, 2) - limits.AggregationStateBytes(types.AGG_SUM, false, 0)
	// 10,000 records: 3 merge blocks, one bucket.
	if d, want := est(vec, memIn(10_000))-est(sum, memIn(10_000)), state+limits.VectorAggregationBytes(types.AGG_VEC_MEAN, 2, 3, 0); d != want {
		t.Errorf("ungrouped vector slot adds %d bytes, want %d", d, want)
	}
	// Two records: never more fragments than rows.
	if d, want := est(vec, memIn(2))-est(sum, memIn(2)), state+limits.VectorAggregationBytes(types.AGG_VEC_MEAN, 2, 1, 0); d != want {
		t.Errorf("a one-block run adds %d bytes, want %d", d, want)
	}
	// An archive whose second shard starts inside a block buffers that
	// block's rows (4,096 - 100) and holds one more fragment.
	arch := LimitInputs{Records: 4200, ShardRecords: []int64{100, 4100}, ShardWorkers: 1, JoinRightRows: -1}
	blocks := limits.MergeBlocks(arch.ShardRecords...)
	if d, want := est(vec, arch)-est(sum, arch), state+limits.VectorAggregationBytes(types.AGG_VEC_MEAN, 2, blocks, 4096-100); d != want {
		t.Errorf("archive vector slot adds %d bytes, want %d (%d fragments, %d buffered rows)", d, want, blocks, 4096-100)
	}
}
