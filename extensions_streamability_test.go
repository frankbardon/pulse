package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Runtime streamability of an extension operator is decided by its
// DECLARED registration flag — the same fact predict reads from the
// extensions snapshot — never by the built-in per-type Streamable()
// tables (which know no extension name) nor by whichever optional
// interface the value happens to carry. Each row below asserts three
// arms agree: the path the engine actually drove (observed through
// call counters on the extension values), PredictResult.Streamable,
// and processing.CanStreamRequestWithExtensions.

const (
	streamAggOn  types.AggregationType = "AGG_ACME_SM_SUM_ON"
	streamAggOff types.AggregationType = "AGG_ACME_SM_SUM_OFF"
	streamGrpOn  types.GroupType       = "GROUP_ACME_SM_KEY_ON"
	streamGrpOff types.GroupType       = "GROUP_ACME_SM_KEY_OFF"
	streamGrpFan types.GroupType       = "GROUP_ACME_SM_FAN_ON"
	streamFanOff types.GroupType       = "GROUP_ACME_SM_FAN_OFF"
	streamAttrRL types.AttributeType   = "ATTR_ACME_SM_ROW"
	streamAttrTP types.AttributeType   = "ATTR_ACME_SM_TWO"
	streamAttrBF types.AttributeType   = "ATTR_ACME_SM_BUF"
	streamTestOn types.TestType        = "TEST_ACME_SM_SUM_ON"
	streamTestNo types.TestType        = "TEST_ACME_SM_SUM_OFF"
)

// streamabilityExtensions registers one operator per (category,
// declared flag) pair. Every aggregator is the paritySum twin — it
// implements extend.OnlineAggregator whatever its declaration — so the
// probe counters reveal which path drove it.
func streamabilityExtensions(probe *parityProbe, grp *r6GrouperCalls, calls *e2eCalls) pulse.Extensions {
	sumFactory := func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
		return &paritySum{probe: probe, dec: encoding.ZeroDecimal128()}, nil
	}
	none := func(json.RawMessage) []string { return nil }
	keyed := func(*types.Group, *encoding.Schema) (extend.Grouper, error) { return r6Grouper{calls: grp}, nil }
	fan := func(*types.Group, *encoding.Schema) (extend.Grouper, error) { return r6FanGrouper{calls: grp}, nil }
	attr := func(mode pulse.AttributeMode) extend.AttributeFactory {
		return func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
			base := e2eAttr{calls: calls}
			switch mode {
			case pulse.AttributeModeRowLocal:
				return e2eAttrRow{base}, nil
			case pulse.AttributeModeTwoPass:
				return e2eAttrTwoPass{e2eAttrRow{base}, new(bool)}, nil
			}
			return base, nil
		}
	}
	rowTest := func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
		return e2eRowTest{calls: calls, field: spec.Field, sum: new(float64)}, nil
	}
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{Name: streamAggOn, Factory: sumFactory, Streamable: true, FieldInputs: none},
			{Name: streamAggOff, Factory: sumFactory, Streamable: false, FieldInputs: none},
		},
		Groupers: []pulse.GrouperRegistration{
			{Name: streamGrpOn, Factory: keyed, Streamable: true, FieldInputs: none},
			{Name: streamGrpOff, Factory: keyed, Streamable: false, FieldInputs: none},
			{Name: streamGrpFan, Factory: fan, Streamable: true, FansOut: true, FieldInputs: none},
			{Name: streamFanOff, Factory: fan, Streamable: false, FansOut: true, FieldInputs: none},
		},
		Attributes: []pulse.AttributeRegistration{
			{Name: streamAttrRL, Factory: attr(pulse.AttributeModeRowLocal), Mode: pulse.AttributeModeRowLocal},
			{Name: streamAttrTP, Factory: attr(pulse.AttributeModeTwoPass), Mode: pulse.AttributeModeTwoPass},
			{Name: streamAttrBF, Factory: attr(pulse.AttributeModeBuffered), Mode: pulse.AttributeModeBuffered},
		},
		Tests: []pulse.TestRegistration{
			{Name: streamTestOn, Tier: pulse.TestTierRow, RowFactory: rowTest, Streamable: true},
			{Name: streamTestNo, Tier: pulse.TestTierRow, RowFactory: rowTest, Streamable: false},
		},
	}
}

type streamabilityRow struct {
	name   string
	req    *types.Request
	stream bool
	// check asserts the operator under test was driven through the
	// path the row names (KeyForRow vs Group, Row vs Compute, …).
	check func(t *testing.T, grp *r6GrouperCalls, calls *e2eCalls)
}

func streamabilityRows() []streamabilityRow {
	sumOn := []*types.Aggregation{{Type: streamAggOn, Field: "score", Label: "s"}}
	by := func(g types.GroupType) []*types.Group { return []*types.Group{{Type: g, Field: "score"}} }
	attrs := func(a types.AttributeType) []*types.Attribute {
		return []*types.Attribute{{Type: a, Field: "score", Label: "dbl"}}
	}
	tests := func(tt types.TestType) []*types.Test {
		return []*types.Test{{Type: tt, Field: "score", Label: "sum"}}
	}
	return []streamabilityRow{
		{name: "aggregator_declared_streamable", req: &types.Request{Aggregations: sumOn}, stream: true},
		{
			// Implements extend.OnlineAggregator but declares
			// Streamable=false: the declaration wins.
			name:   "aggregator_declared_buffered_with_online_value",
			req:    &types.Request{Aggregations: []*types.Aggregation{{Type: streamAggOff, Field: "score", Label: "s"}}},
			stream: false,
		},
		{
			name: "grouper_declared_streamable", req: &types.Request{Aggregations: sumOn, Groups: by(streamGrpOn)}, stream: true,
			check: func(t *testing.T, g *r6GrouperCalls, _ *e2eCalls) {
				if g.keyForRow.Load() == 0 || g.group.Load() != 0 {
					t.Errorf("grouper: KeyForRow=%d Group=%d, want KeyForRow only", g.keyForRow.Load(), g.group.Load())
				}
			},
		},
		{
			name: "grouper_declared_buffered", req: &types.Request{Aggregations: sumOn, Groups: by(streamGrpOff)}, stream: false,
			check: func(t *testing.T, g *r6GrouperCalls, _ *e2eCalls) {
				if g.group.Load() == 0 || g.keyForRow.Load() != 0 {
					t.Errorf("grouper: KeyForRow=%d Group=%d, want Group only", g.keyForRow.Load(), g.group.Load())
				}
			},
		},
		{
			name: "fanout_grouper_declared_streamable", req: &types.Request{Aggregations: sumOn, Groups: by(streamGrpFan)}, stream: true,
			check: func(t *testing.T, g *r6GrouperCalls, _ *e2eCalls) {
				if g.keysForRow.Load() == 0 || g.group.Load() != 0 {
					t.Errorf("grouper: KeysForRow=%d Group=%d, want KeysForRow only", g.keysForRow.Load(), g.group.Load())
				}
			},
		},
		{
			name: "attribute_row_local", req: &types.Request{Aggregations: sumOn, Attributes: attrs(streamAttrRL)}, stream: true,
			check: func(t *testing.T, _ *r6GrouperCalls, c *e2eCalls) {
				if c.row.Load() == 0 || c.compute.Load() != 0 {
					t.Errorf("attribute: Row=%d Compute=%d, want Row only", c.row.Load(), c.compute.Load())
				}
			},
		},
		{
			name: "attribute_two_pass", req: &types.Request{Aggregations: sumOn, Attributes: attrs(streamAttrTP)}, stream: true,
			check: func(t *testing.T, _ *r6GrouperCalls, c *e2eCalls) {
				if c.prePass.Load() == 0 || c.finalize.Load() == 0 || c.compute.Load() != 0 {
					t.Errorf("attribute: PrePass=%d Finalize=%d Compute=%d, want the two-pass drive only",
						c.prePass.Load(), c.finalize.Load(), c.compute.Load())
				}
			},
		},
		{
			// A two-pass attribute does not compose with grouped
			// streaming; the extension's declared Mode must trip the same
			// combination gate a built-in ATTR_ZSCORE does.
			name: "attribute_two_pass_with_groups",
			req:  &types.Request{Aggregations: sumOn, Attributes: attrs(streamAttrTP), Groups: by(streamGrpOn)}, stream: false,
		},
		{name: "attribute_buffered", req: &types.Request{Aggregations: sumOn, Attributes: attrs(streamAttrBF)}, stream: false},
		{
			name: "row_test_declared_streamable", req: &types.Request{Aggregations: sumOn, Tests: tests(streamTestOn)}, stream: true,
			check: func(t *testing.T, _ *r6GrouperCalls, c *e2eCalls) {
				if c.update.Load() == 0 {
					t.Error("row test never driven through UpdateRow")
				}
			},
		},
		{name: "row_test_declared_buffered", req: &types.Request{Aggregations: sumOn, Tests: tests(streamTestNo)}, stream: false},
	}
}

// TestExtensions_StreamabilityFollowsDeclaration is the runtime ==
// predict == CanStreamRequestWithExtensions matrix over every
// extension category with a streaming tier.
func TestExtensions_StreamabilityFollowsDeclaration(t *testing.T) {
	for _, row := range streamabilityRows() {
		t.Run(row.name, func(t *testing.T) {
			probe, grp, calls := &parityProbe{}, &r6GrouperCalls{}, &e2eCalls{}
			p, cohort := openE2E(t, streamabilityExtensions(probe, grp, calls))
			req := *row.req
			req.Cohort = cohort

			pr, err := p.Predict(context.Background(), &req)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			if !pr.Valid {
				t.Fatalf("Predict invalid: %+v", pr)
			}
			if pr.Streamable != row.stream {
				t.Errorf("PredictResult.Streamable = %v (reasons %v), want %v", pr.Streamable, pr.StreamableReasons, row.stream)
			}
			reg := pulse.ServiceForTest(p).Extensions()
			if got := processing.CanStreamRequestWithExtensions(&req, paritySchema(t), reg); got != row.stream {
				t.Errorf("CanStreamRequestWithExtensions = %v, want %v", got, row.stream)
			}

			for _, arm := range []string{"Process", "ProcessStream"} {
				probe.reset()
				*grp = r6GrouperCalls{}
				*calls = e2eCalls{}
				if arm == "Process" {
					processE2E(t, p, &req)
				} else {
					streamE2E(t, p, &req)
				}
				online, buffered := probe.online.Load(), probe.buffered.Load()
				if row.stream && (online == 0 || buffered != 0) {
					t.Errorf("%s: aggregator online=%d buffered=%d, want the streaming path", arm, online, buffered)
				}
				if !row.stream && (buffered == 0 || online != 0) {
					t.Errorf("%s: aggregator online=%d buffered=%d, want the buffered path", arm, online, buffered)
				}
				if row.check != nil {
					row.check(t, grp, calls)
				}
			}
		})
	}
}

// TestExtensions_StreamingGrouperMatchesBuffered pins that the newly
// reachable streaming arm for an extension grouper produces the same
// payload — Data and the Components.Groupers universal floor — as the
// buffered arm of an identical registration declared non-streamable,
// for a single-key and a fan-out grouper. The floor is the trap: an
// extension grouper carries no "buckets" payload, so the streaming arm
// must count its own (record, bucket) assignments.
func TestExtensions_StreamingGrouperMatchesBuffered(t *testing.T) {
	probe, grp, calls := &parityProbe{}, &r6GrouperCalls{}, &e2eCalls{}
	p, cohort := openE2E(t, streamabilityExtensions(probe, grp, calls))
	run := func(g types.GroupType) string {
		req := &types.Request{
			Cohort:       cohort,
			Groups:       []*types.Group{{Type: g, Field: "score"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}, {Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}
		resp := processE2E(t, p, req)
		return mustJSON(t, resp.Data) + mustJSON(t, stripGroupedAggComponents(req, resp.Components))
	}
	for _, pair := range [][2]types.GroupType{{streamGrpOn, streamGrpOff}, {streamGrpFan, streamFanOff}} {
		if s, b := run(pair[0]), run(pair[1]); s != b {
			t.Errorf("%s (streaming) vs %s (buffered) payload differs\nstreaming: %s\nbuffered:  %s", pair[0], pair[1], s, b)
		}
	}
}

// TestStreamability_TwoPassCombinationPredictMatchesRuntime pins the
// built-in half of the two-pass combination gate predict gained with
// the extension arm: a two-pass attribute alongside a grouper runs
// buffered at runtime, and predict now says so (it previously reported
// Streamable=true for this shape).
func TestStreamability_TwoPassCombinationPredictMatchesRuntime(t *testing.T) {
	p, cohort := openE2E(t, pulse.Extensions{})
	req := &types.Request{
		Cohort:       cohort,
		Attributes:   []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "z", Label: "s"}},
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	runtime := processing.CanStreamRequest(req, paritySchema(t))
	if runtime {
		t.Fatal("CanStreamRequest = true; the two-pass + grouper shape is expected to run buffered")
	}
	if pr.Streamable != runtime {
		t.Errorf("PredictResult.Streamable = %v (reasons %v), runtime = %v", pr.Streamable, pr.StreamableReasons, runtime)
	}
}
