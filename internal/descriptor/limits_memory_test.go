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
	// aggregation each.
	grouped := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: sumOf("num")}
	if d := est(grouped, memIn(1000)) - base; d != 4*(512+256) {
		t.Errorf("grouping adds %d bytes, want 4 buckets (%d)", d, 4*(512+256))
	}
	// A grouper of unknown cardinality is bounded by the records.
	ranged := &types.Request{Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}}, Aggregations: sumOf("num")}
	if d := est(ranged, memIn(1000)) - base; d != 999*(512+256) {
		t.Errorf("unknown grouper adds %d bytes, want 999 buckets", d)
	}
	// A join adds its build side.
	joined := LimitInputs{Records: 1000, Join: true, JoinRightRows: 300}
	if d := est(plain, joined) - base; d != 300*(3*stride+2*rec) {
		t.Errorf("a 300-row build side adds %d bytes, want %d", d, 300*(3*stride+2*rec))
	}
	// Buffered: every record resident.
	median := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "num", Label: "m"}}}
	if d := est(median, memIn(1000)) - base; d != 1000*(rec+16) {
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
	if d := est(&types.Request{Groups: tags, Aggregations: distinct}, memIn(1000)) - est(&types.Request{Aggregations: distinct}, memIn(1000)); d != 2*(512+256)+2*1000*96 {
		t.Errorf("fan-out value state adds %d bytes, want 2 more buckets and 2 x 1,000 more value entries (%d)", d, 2*(512+256)+2*1000*96)
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
	if d := est(plain, arch(dirBytes, 2)) - serial; d != 2*(512+256) {
		t.Errorf("fan-out adds %d bytes, want 2 partial buckets (%d)", d, 2*(512+256))
	}
	// ... each shard's own bound grouped (a 5-entry dictionary: 5 each)
	// ...
	grouped := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: sumOf("num")}
	if d := est(grouped, arch(dirBytes, 0)) - est(grouped, arch(dirBytes, 1)); runtime.NumCPU() >= 2 && d != 10*(512+256) {
		t.Errorf("grouped fan-out adds %d bytes, want 10 partial buckets (%d)", d, 10*(512+256))
	}
	// ... clamped by the shard's records for an unknown grouper ...
	ranged := &types.Request{Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "num", Interval: 10}}, Aggregations: sumOf("num")}
	if d := est(ranged, arch(dirBytes, 2)) - est(ranged, arch(dirBytes, 1)); d != 1000*(512+256) {
		t.Errorf("unknown-grouper fan-out adds %d bytes, want 600 + 400 partial buckets", d)
	}
	// ... and per-value state over every record once more.
	distinct := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "num", Label: "d"}}}
	if d := est(distinct, arch(dirBytes, 2)) - est(distinct, arch(dirBytes, 1)); d != 2*(512+256)+1000*96 {
		t.Errorf("value-state fan-out adds %d bytes, want 2 buckets + 1,000 x 96", d)
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
