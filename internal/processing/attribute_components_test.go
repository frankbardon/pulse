package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// scaleEntry is the Components.Attributes entry ATTR_SCALE_SCORE "s"
// must carry for scores want (NaN = null) and an out-of-range tally
// (op nil: no range declared, so no operator keys).
func scaleEntry(want []float64, op map[string]any) types.AttributeComponents {
	e := types.AttributeComponents{Label: "s", Type: types.ATTR_SCALE_SCORE, Operator: op}
	for _, v := range want {
		if math.IsNaN(v) {
			e.NNull++
		} else {
			e.N++
		}
	}
	return e
}

// TestAttributeComponents_ScaleScoreEveryArm: the ATTR_SCALE_SCORE slot
// carries one Components.Attributes entry — floor by output presence,
// n_out_of_range / n_rows_out_of_range only under a declared range —
// identically on the streaming, grouped streaming and buffered arms,
// and the warning keeps its counts beside it. ATTR_PERCENTILE (the
// buffered arm's forcing attribute) declares no ComponentSchema and so
// adds no entry.
func TestAttributeComponents_ScaleScoreEveryArm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		vecs []types.VectorSpec
		attr types.Attribute
		want types.AttributeComponents
	}{
		{name: "no range: floor only",
			attr: types.Attribute{Fields: []string{"q1", "q2", "q3", "q4"}},
			want: scaleEntry([]float64{2.5, nan, nan, 3.75, 1}, nil)},
		{name: "kind scale bounds: one value out of range",
			vecs: scaleVector(true, 1, 5),
			attr: types.Attribute{Vector: "bat", Params: json.RawMessage(`{"min_valid": 1}`)},
			want: scaleEntry([]float64{1, 1, 1, 1, 1}, map[string]any{"n_out_of_range": 1, "n_rows_out_of_range": 1})},
		{name: "range holds every value: zero counters present",
			vecs: scaleVector(true, 0, 10),
			attr: types.Attribute{Vector: "bat"},
			want: scaleEntry([]float64{2.5, nan, nan, 3.75, 1}, map[string]any{"n_out_of_range": 0, "n_rows_out_of_range": 0})},
		{name: "out of range drops a row below min_valid",
			vecs: scaleVector(true, 1, 5),
			attr: types.Attribute{Vector: "bat"},
			want: scaleEntry([]float64{2.5, nan, nan, nan, 1}, map[string]any{"n_out_of_range": 1, "n_rows_out_of_range": 1})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, resps := runScaleArms(t, func() *types.Request {
				a := c.attr
				a.Type, a.Label = types.ATTR_SCALE_SCORE, "s"
				return &types.Request{Vectors: c.vecs, Attributes: []*types.Attribute{&a}}
			})
			for arm, resp := range resps {
				if resp.Components == nil {
					t.Fatalf("arm %d: no Components", arm)
				}
				got := resp.Components.Attributes
				if len(got) != 1 || !reflect.DeepEqual(got[0], c.want) {
					t.Errorf("arm %d: Components.Attributes = %+v, want [%+v]", arm, got, c.want)
				}
				if ws := scaleWarnings(resp); c.want.Operator != nil && c.want.Operator["n_out_of_range"] != 0 {
					if len(ws) != 1 || ws[0].Details["n_out_of_range"] != int64(1) {
						t.Errorf("arm %d: the warning lost its counts: %+v", arm, ws)
					}
				}
			}
		})
	}
}

// TestAttributeComponents_ScaleScoreFusedMatchesBuffered: the fused
// crosstab arm tallies the entry inline; it equals the buffered
// RunCrosstab's.
func TestAttributeComponents_ScaleScoreFusedMatchesBuffered(t *testing.T) {
	t.Parallel()

	schema := scaleSchema()
	build := func() *types.Request {
		return &types.Request{
			Vectors:    scaleVector(true, 1, 5),
			Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s", Params: json.RawMessage(`{"min_valid": 2}`)}},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s"},
			},
		}
	}
	fused, err := NewProcessor(schema).RunCrosstabFused(context.Background(), build(), NewSliceIterator(scaleRecords(schema)))
	if err != nil {
		t.Fatal(err)
	}
	buffered, err := NewProcessor(schema).RunCrosstab(context.Background(), build(), scaleRecords(schema))
	if err != nil {
		t.Fatal(err)
	}
	// min_valid 2: r2 (one valid item) is null; r3 keeps three.
	want := []types.AttributeComponents{scaleEntry([]float64{1, 1, nan, 1, 1},
		map[string]any{"n_out_of_range": 1, "n_rows_out_of_range": 1})}
	for name, resp := range map[string]*types.Response{"fused": fused, "buffered": buffered} {
		if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
			t.Errorf("%s: Components.Attributes = %+v, want %+v", name, resp.Components, want)
		}
	}
}

// TestAttributeComponents_TwoPassCountsOnce: a two-pass attribute that
// reads the scale score makes the two-pass arm score the row-local
// stage on the prepass AND the emission scan; the tally (entry and
// warning) still counts each row once — the other arms' figures.
func TestAttributeComponents_TwoPassCountsOnce(t *testing.T) {
	t.Parallel()

	schema := scaleSchema()
	req := &types.Request{
		Vectors: scaleVector(true, 1, 5),
		Attributes: []*types.Attribute{
			{Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s", Params: json.RawMessage(`{"min_valid": 1}`)},
			{Type: types.ATTR_ZSCORE, Field: "s", Label: "z"},
		},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "z", Label: "sum_z"}},
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), req, NewSliceIterator(scaleRecords(schema)))
	if err != nil {
		t.Fatal(err)
	}
	if p.LastPath() != PathStreaming {
		t.Fatalf("ran %v, want the two-pass streaming arm", p.LastPath())
	}
	want := []types.AttributeComponents{scaleEntry([]float64{1, 1, 1, 1, 1},
		map[string]any{"n_out_of_range": 1, "n_rows_out_of_range": 1})}
	if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
		t.Errorf("Components.Attributes = %+v, want %+v", resp.Components, want)
	}
	if ws := scaleWarnings(resp); len(ws) != 1 || ws[0].Details["n_out_of_range"] != int64(1) || ws[0].Details["n_rows"] != int64(1) {
		t.Errorf("warning counts a re-scored row twice: %+v", ws)
	}
}

// TestAttributeComponents_ComputeGate: a closed Components gate or a
// plan without components.attributes builds no entry (and counts no
// build) on every arm, while the rest of the block is untouched; an
// attribute that declares no ComponentSchema never emits one.
func TestAttributeComponents_ComputeGate(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump

	schema := scaleSchema()
	noAttrs := FullComputePlan()
	noAttrs.Attributes = false
	type arm struct {
		name string
		run  func(p *Processor, req *types.Request) (*types.Response, error)
		req  func() *types.Request
	}
	scale := func() *types.Attribute {
		return &types.Attribute{Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s", Params: json.RawMessage(`{"min_valid": 1}`)}
	}
	base := func(extra ...*types.Attribute) func() *types.Request {
		return func() *types.Request {
			return &types.Request{
				Vectors:      scaleVector(true, 1, 5),
				Attributes:   append([]*types.Attribute{scale()}, extra...),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "sum_s"}},
			}
		}
	}
	process := func(p *Processor, req *types.Request) (*types.Response, error) {
		return p.Process(context.Background(), req, NewSliceIterator(scaleRecords(schema)))
	}
	crosstab := func() *types.Request {
		r := base()()
		r.Aggregations = nil
		r.Crosstab = &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s"},
		}
		return r
	}
	arms := []arm{
		{"streaming", process, base()},
		{"buffered", process, base(&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "q4", Label: "pct"})},
		{"two_pass", process, base(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "s", Label: "z"})},
		{"fused_crosstab", func(p *Processor, req *types.Request) (*types.Response, error) {
			return p.RunCrosstabFused(context.Background(), req, NewSliceIterator(scaleRecords(schema)))
		}, crosstab},
		{"buffered_crosstab", func(p *Processor, req *types.Request) (*types.Response, error) {
			return p.RunCrosstab(context.Background(), req, scaleRecords(schema))
		}, crosstab},
	}
	for _, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			open, err := a.run(NewProcessor(schema), a.req())
			if err != nil {
				t.Fatal(err)
			}
			if open.Components == nil || len(open.Components.Attributes) != 1 {
				t.Fatalf("open gate: Components.Attributes = %+v, want one entry", open.Components)
			}
			p := NewProcessor(schema)
			p.SetDisableComponents(true)
			closed, err := a.run(p, a.req())
			if err != nil {
				t.Fatal(err)
			}
			if closed.Components != nil {
				t.Errorf("closed gate: Components = %+v, want nil", closed.Components)
			}
			before := WorkStats()
			p = NewProcessor(schema)
			p.SetComputePlan(noAttrs)
			planned, err := a.run(p, a.req())
			if err != nil {
				t.Fatal(err)
			}
			if d := WorkStats().Sub(before).AttributeComponentBuilds; d != 0 {
				t.Errorf("plan without components.attributes built %d entries", d)
			}
			if planned.Components == nil || planned.Components.Attributes != nil {
				t.Errorf("planned: Components = %+v, want the block without attributes", planned.Components)
			}
		})
	}
	t.Run("undeclared_attribute_emits_none", func(t *testing.T) {
		resp, err := process(NewProcessor(schema), &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "q1", Label: "z"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "z", Label: "sum_z"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Components == nil || resp.Components.Attributes != nil {
			t.Errorf("ATTR_ZSCORE emitted %+v", resp.Components)
		}
	})
}
