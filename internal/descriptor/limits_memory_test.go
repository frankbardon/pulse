package descriptor

import (
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
	// Matrix: blocks per shard x buckets x accumulator bytes (2 numeric
	// members, listwise: 32 + 8·(2+3) = 72).
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
	sharded := matEst(LimitInputs{Records: limits.MergeBlockSize + 6, ShardRecords: []int64{limits.MergeBlockSize + 1, 5}, JoinRightRows: -1})
	if d := sharded - single; d != 72 {
		t.Errorf("4,097 + 5 sharded rows hold one more block than 4,102 in one file: +%d bytes, want 72", d)
	}

	if _, ok := EstimateMemory(plain, schema, nil, memIn(-1)); ok {
		t.Error("an unknown record count has an estimate")
	}
	if _, ok := EstimateMemory(plain, schema, nil, LimitInputs{Records: 10, Join: true, JoinRightRows: -1}); ok {
		t.Error("a join with an unknown build side has an estimate")
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
