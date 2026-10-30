package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Chain stages carry expanded vector element columns (U30 E4-S3): an
// expanding AGG_VEC_* slot (params.expand) on a non-final ProcessChain
// stage writes one f64 column per member, "<label>[<element label>]",
// and the next stage reads each as an ordinary nullable f64 field — a
// plain aggregation, an AGG_RATIO, a filter — predict and runtime
// alike. An element no row answered (NaN) is a null there. A
// non-expanding vector slot feeding a later stage, and a later stage
// naming the bare array label, stay refused on both sides.

// chainElemStage0 is stage 0: grouped by qty, the expanded mean of b1 /
// b2 (m[b1], m[b2]) beside a row count.
func chainElemStage0(expand bool) *types.Request {
	m := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m"}
	if expand {
		m.Params = vaggExpandParams()
	}
	return &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
		Aggregations: []*types.Aggregation{m, {Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
	}
}

func chainElemRequest(stage0, stage1 *types.Request) *pulse.ChainRequest {
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "e.pulse"}, Stages: []*types.ChainStage{
		{Name: "profile", Request: stage0},
		{Name: "rollup", Request: stage1},
	}}
}

// chainElemPredict runs PredictChain and returns its result, failing on
// any predict error.
func chainElemPredict(t *testing.T, p *pulse.Pulse, req *pulse.ChainRequest) *pulse.ChainPredictResult {
	t.Helper()
	env, err := p.PredictChain(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		for _, e := range env.Errors {
			t.Logf("predict error %s %s %v", e.Code, e.Message, e.Details)
		}
		t.Fatalf("PredictChain refused")
	}
	res, ok := env.Data.(*pulse.ChainPredictResult)
	if !ok {
		t.Fatalf("PredictChain data = %T", env.Data)
	}
	return res
}

// TestChain_ElementColumnsFeedNextStage: stage 1 sums an element
// column, ratios two of them and filters on one; every figure equals
// the oracle over stage 0's rows with the NaN element skipped as a null
// (n_null counts it), and predict names the element columns as stage
// 0's output and admits the stage-1 references.
func TestChain_ElementColumnsFeedNextStage(t *testing.T) {
	t.Parallel()

	p, _ := vaggElementCohort(t)
	ctx := context.Background()
	stage1 := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "m[b1]", Label: "s1"},
			{Type: types.AGG_SUM, Field: "m[b2]", Label: "s2"},
			{Type: types.AGG_RATIO, Field: "m[b1]", Label: "r",
				Params: json.RawMessage(`{"numerator_field": "m[b1]", "denominator_field": "m[b2]"}`)},
		}}
	}
	req := func() *pulse.ChainRequest { return chainElemRequest(chainElemStage0(true), stage1()) }

	pres := chainElemPredict(t, p, req())
	if len(pres.StageSchemas) != 2 || !slices.Equal(pres.StageSchemas[0], []string{"qty", "m[b1]", "m[b2]", "n"}) {
		t.Fatalf("stage_schemas = %v, want stage 0 [qty m[b1] m[b2] n]", pres.StageSchemas)
	}

	resp, err := p.ProcessChain(ctx, req())
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	// The oracle: stage 0's own element cells, a NaN one a null.
	var s1, s2, num, den float64
	nNull1 := 0
	for _, row := range resp.Stages[0].Data {
		b1, ok1 := row["m[b1]"].(float64)
		b2, ok2 := row["m[b2]"].(float64)
		if !ok1 || !ok2 {
			t.Fatalf("stage 0 row lacks an element column: %v", vaggKeys(row))
		}
		if math.IsNaN(b1) {
			nNull1++
		} else {
			s1 += b1
		}
		if !math.IsNaN(b2) {
			s2 += b2
		}
		if !math.IsNaN(b1) && !math.IsNaN(b2) {
			num += b1
			den += b2
		}
	}
	if nNull1 != 1 {
		t.Fatalf("fixture: %d NaN m[b1] cells, want 1", nNull1)
	}
	if len(resp.Final.Data) != 1 {
		t.Fatalf("final rows = %d, want 1", len(resp.Final.Data))
	}
	row := resp.Final.Data[0]
	for _, c := range []struct {
		col  string
		want float64
	}{{"s1", s1}, {"s2", s2}, {"r", num / den}} {
		got, ok := row[c.col].(float64)
		if !ok || !vexprClose(got, c.want) {
			t.Errorf("%s = %v, want %v", c.col, row[c.col], c.want)
		}
	}
	if comp := resp.Final.Components; comp == nil || len(comp.Aggregations) < 1 {
		t.Fatalf("final components = %+v", comp)
	} else if a := comp.Aggregations[0]; a.NNull != 1 || a.N != len(resp.Stages[0].Data)-1 {
		t.Errorf("s1 components n=%d n_null=%d, want n=%d n_null=1 (the NaN element is a null)", a.N, a.NNull, len(resp.Stages[0].Data)-1)
	}

	// A filter on an element column at stage 1: a range on m[b1].
	filtered := func() *pulse.ChainRequest {
		s := stage1()
		s.Aggregations = s.Aggregations[:1]
		s.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "m[b1]", Values: []string{"0", "1e300"}}}
		return chainElemRequest(chainElemStage0(true), s)
	}
	chainElemPredict(t, p, filtered())
	fresp, err := p.ProcessChain(ctx, filtered())
	if err != nil {
		t.Fatalf("ProcessChain with an element-column filter: %v", err)
	}
	if got, ok := fresp.Final.Data[0]["s1"].(float64); !ok || !vexprClose(got, s1) {
		t.Errorf("filtered s1 = %v, want %v (the null element filtered out)", fresp.Final.Data[0]["s1"], s1)
	}

	// A row-local formula reads an element column by its name through
	// $env (the bracket is no identifier); a null element coalesces.
	formula := func() *pulse.ChainRequest {
		return chainElemRequest(chainElemStage0(true), &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "twice", Expression: `($env["m[b1]"] ?? 0) * 2`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "twice", Label: "t"}}})
	}
	chainElemPredict(t, p, formula())
	tresp, err := p.ProcessChain(ctx, formula())
	if err != nil {
		t.Fatalf("ProcessChain with an element-column formula: %v", err)
	}
	if got, ok := tresp.Final.Data[0]["t"].(float64); !ok || !vexprClose(got, 2*s1) {
		t.Errorf("formula sum = %v, want %v", tresp.Final.Data[0]["t"], 2*s1)
	}
}

// TestChain_ElementColumnRefusalsParity: the cases that stay refused,
// predict ≡ runtime — a non-expanding vector slot feeding a later stage
// (PROCESSING_CONFIG array_valued), a later stage naming the bare
// label of an expanding slot (no such column), and an aggregation
// labelled like a group key column of the stage feeding the next.
func TestChain_ElementColumnRefusalsParity(t *testing.T) {
	t.Parallel()

	p, _ := vaggElementCohort(t)
	ctx := context.Background()
	sum := func(field string) *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: "s"}}}
	}
	collide := chainElemStage0(true)
	collide.Aggregations = append(collide.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "b1", Label: "qty"})
	for _, c := range []struct {
		name   string
		req    *pulse.ChainRequest
		code   perr.Code
		reason string
	}{
		{"plain vector feeds", chainElemRequest(chainElemStage0(false), sum("n")), perr.PROCESSING_CONFIG, "array_valued"},
		{"bare label of an expanding slot", chainElemRequest(chainElemStage0(true), sum("m")), "", ""},
		{"output column collision", chainElemRequest(collide, sum("n")), perr.SERVICE_VALIDATION, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.ProcessChain(ctx, c.req)
			if err == nil {
				t.Fatal("ProcessChain ran, want a refusal")
			}
			ce := codeOf(t, err)
			if c.code != "" && ce.Code != c.code {
				t.Fatalf("ProcessChain = %s %v, want %s", ce.Code, ce.Details, c.code)
			}
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Fatalf("ProcessChain reason = %v, want %s", ce.Details["reason"], c.reason)
			}
			env, err := p.PredictChain(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 {
				t.Fatalf("PredictChain admitted it; runtime refused %s %v", ce.Code, ce.Details)
			}
			if got := env.Errors[0]; got.Code != string(ce.Code) || got.Details["reason"] != ce.Details["reason"] {
				t.Fatalf("PredictChain first error %s %v, runtime %s %v", got.Code, got.Details, ce.Code, ce.Details)
			}
		})
	}
}

// TestChain_ElementColumnsThroughThreeStages: a later stage may expand
// a vector over the element columns it received, and the stage after
// it reads those columns in turn — each stage's element columns resolve
// against the schema that stage itself ran over, on both sides.
func TestChain_ElementColumnsThroughThreeStages(t *testing.T) {
	t.Parallel()

	p, _ := vaggElementCohort(t)
	req := func() *pulse.ChainRequest {
		r := chainElemRequest(chainElemStage0(true), &types.Request{Aggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_SUM, Fields: []string{"m[b1]", "m[b2]"}, Label: "v", Params: vaggExpandParams()}}})
		r.Stages = append(r.Stages, &types.ChainStage{Name: "final", Request: &types.Request{Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "v[m[b2]]", Label: "t"}}}})
		return r
	}
	pres := chainElemPredict(t, p, req())
	if len(pres.StageSchemas) != 3 || !slices.Equal(pres.StageSchemas[1], []string{"v[m[b1]]", "v[m[b2]]"}) {
		t.Fatalf("stage_schemas = %v, want stage 1 [v[m[b1]] v[m[b2]]]", pres.StageSchemas)
	}
	resp, err := p.ProcessChain(context.Background(), req())
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	want, ok := resp.Stages[1].Data[0]["v[m[b2]]"].(float64)
	if !ok || math.IsNaN(want) {
		t.Fatalf("stage 1 v[m[b2]] = %v", resp.Stages[1].Data[0]["v[m[b2]]"])
	}
	if got, ok := resp.Final.Data[0]["t"].(float64); !ok || got != want {
		t.Errorf("t = %v, want %v", resp.Final.Data[0]["t"], want)
	}
}
