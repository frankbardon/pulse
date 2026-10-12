package pulse_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// A Process overlay kind authored over the other Process host shape — a
// MATRIX-only kind (OVERLAY_SHARE_OF_COL, the χ² family, OVERLAY_FORMULA
// …) on a grouped request, or a SERIES-only kind (OVERLAY_YOY, the
// prior / rolling / baseline / sibling / _TOTAL kinds) on a crosstab —
// was refused by each kind's own predict validator with assorted
// Details while the runtime answered PULSE_OVERLAY_KIND_UNKNOWN (E4-S15).
// Both sides now raise the one shared shape refusal
// (overlayhost.ShapeRefusal): PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
// Details {index, kind, host, shape, shapes}, before any per-kind rule.
//
// A Compose OVERLAY_FORMULA once passed predict-compose and answered an
// empty stub layer; it now computes, and an identifier outside its
// namespace is refused identically on both sides.
//
// Every case runs PREDICT FIRST on a fresh request.

// TestOverlayWrongShape_ProcessParity: every Process kind over every
// Process shape it does not run on; the valid spec at index 0 makes the
// refused index 1.
func TestOverlayWrongShape_ProcessParity(t *testing.T) {
	ctx := context.Background()
	p, data := nsFixture(t, pulse.Options{})
	hosts := []struct {
		name  string
		shape types.OverlayShape
		index int
		build func(kind types.OverlayKind) *pulse.Request
	}{
		{"series", types.OverlayShapeSeries, 1, func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"}, Groups: nsGroups(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup},
					{Kind: kind, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}},
				}}
		}},
		{"matrix", types.OverlayShapeMatrix, 1, func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
					Shape:   types.CrosstabShapeMatrix,
				},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKindShareOfCol, Scope: types.OverlayScopeCell, Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}},
					{Kind: kind, Scope: types.OverlayScopeGroup},
				}}
		}},
		// A long-shape crosstab has no MATRIX payload to dispatch over:
		// the Process hook refuses from the request alone.
		{"long", types.OverlayShapeMatrix, 0, func(kind types.OverlayKind) *pulse.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "ns.pulse"},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
					Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
					Shape:   types.CrosstabShapeLong,
				},
				Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup}}}
		}},
	}
	for _, h := range hosts {
		if h.index == 1 {
			ctl := h.build("")
			ctl.Overlays = ctl.Overlays[:1]
			if pe := predictCodedError(t, p, data, ctl); pe != nil {
				t.Fatalf("%s control: predict = %+v", h.name, pe)
			}
			if _, err := p.Process(ctx, ctl); err != nil {
				t.Fatalf("%s control: Process: %v", h.name, err)
			}
		}
		n := 0
		for _, kind := range types.AllOverlayKinds() {
			if !overlayhost.Accepts(overlayhost.Request, kind) || overlayhost.AcceptsShape(h.shape, kind) {
				continue
			}
			n++
			t.Run(fmt.Sprintf("%s/%s", h.name, kind), func(t *testing.T) {
				want := overlayhost.ShapeRefusal(h.shape, kind, kind, h.index)
				pe := predictCodedError(t, p, data, h.build(kind))
				if pe == nil || pe.Code != string(want.Code) || pe.Message != want.Message ||
					nsJSON(t, pe.Details) != nsJSON(t, want.Details) {
					t.Fatalf("predict = %+v, want %s %s %q", pe, want.Code, nsJSON(t, want.Details), want.Message)
				}
				_, err := p.Process(ctx, h.build(kind))
				if err == nil {
					t.Fatal("runtime accepted the spec predict refuses")
				}
				ce := codeOf(t, err)
				if ce.Code != want.Code || ce.Message != want.Message || nsJSON(t, ce.Details) != nsJSON(t, want.Details) {
					t.Fatalf("runtime = %s %s %q, want predict's", ce.Code, nsJSON(t, ce.Details), ce.Message)
				}
			})
		}
		if n == 0 {
			t.Fatalf("vacuous: no kind refused over a %s host", h.name)
		}
	}
}

// cfSlots builds a two-slot Compose request (labels a, b) over one
// slot shape, a scoring AGG_SUM and b AGG_AVERAGE, with specs.
func cfSlots(shape types.OverlayShape, specs ...types.ComposeOverlaySpec) *pulse.ComposedRequest {
	slot := func(label string, agg types.AggregationType) *types.Request {
		r := &types.Request{Label: label, Cohort: &types.Cohort{Filename: "ns.pulse"}}
		switch shape {
		case types.OverlayShapeMatrix:
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}},
				Cell:    &types.Aggregation{Type: agg, Field: "score"},
				Shape:   types.CrosstabShapeMatrix,
			}
		case types.OverlayShapeSeries:
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}}
			r.Aggregations = []*types.Aggregation{{Type: agg, Field: "score"}}
		default:
			r.Aggregations = []*types.Aggregation{{Type: agg, Field: "score"}}
		}
		return r
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{slot("a", types.AGG_SUM), slot("b", types.AGG_AVERAGE)}, Overlays: specs}
}

func cfSpec(name string, kind types.OverlayKind, scope types.OverlayScope, formula string) types.ComposeOverlaySpec {
	s := types.ComposeOverlaySpec{Name: name, Kind: kind, Scope: scope, Reference: "a", Targets: []string{"b"}}
	if kind == types.OverlayKindFormula {
		s.Params = map[string]any{"formula": formula}
	}
	return s
}

// cfRun predicts then runs req, both clean.
func cfRun(t *testing.T, p *pulse.Pulse, req func() *pulse.ComposedRequest) *types.ComposedResponse {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, req())
	if pe := whFirstError(t, env, err); pe != nil {
		t.Fatalf("predict = %+v", pe)
	}
	out, err := p.Compose(ctx, req())
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return out
}

func cfSame(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }

// TestComposeFormula_ComputesParity: a Compose OVERLAY_FORMULA computes
// what its namespace promises — `value / ref_value * 100` (and the
// matrix twin) equal OVERLAY_INDEX_VS_REF bit for bit, the slot paths
// read the same figures as value / ref, and prior / total follow the
// target series.
func TestComposeFormula_ComputesParity(t *testing.T) {
	p, _ := nsFixture(t, pulse.Options{})

	t.Run("series", func(t *testing.T) {
		out := cfRun(t, p, func() *pulse.ComposedRequest {
			return cfSlots(types.OverlayShapeSeries,
				cfSpec("idx", types.OverlayKindIndexVsRef, types.OverlayScopeGroup, ""),
				cfSpec("f", types.OverlayKindFormula, types.OverlayScopeGroup, "value / ref_value * 100"),
				cfSpec("slots", types.OverlayKindFormula, types.OverlayScopeGroup, "slot.b.value - slot.a.value - (value - ref_value)"),
				cfSpec("pt", types.OverlayKindFormula, types.OverlayScopeGroup, "value / total + (prior > 0 ? 1 : 0)"))
		})
		idx, f := out.Overlays[0].Payload.Series, out.Overlays[1].Payload.Series
		if f == nil || len(f.Entries) == 0 || len(f.Entries) != len(idx.Entries) {
			t.Fatalf("formula layer = %+v, want %d entries", out.Overlays[1], len(idx.Entries))
		}
		for i := range idx.Entries {
			if nsJSON(t, f.Entries[i].Key) != nsJSON(t, idx.Entries[i].Key) || *f.Entries[i].Summary.Statistic != *idx.Entries[i].Summary.Statistic {
				t.Errorf("entry %d: formula %v %v, index %v %v", i, f.Entries[i].Key, *f.Entries[i].Summary.Statistic, idx.Entries[i].Key, *idx.Entries[i].Summary.Statistic)
			}
		}
		for _, e := range out.Overlays[2].Payload.Series.Entries {
			if *e.Summary.Statistic != 0 {
				t.Errorf("slot paths disagree with value / ref_value at %v: %v", e.Key, *e.Summary.Statistic)
			}
		}
		var total float64
		var vals []float64
		for _, row := range out.Responses[1].Data {
			v := row[types.AggregationLabelOf(&types.Aggregation{Type: types.AGG_AVERAGE, Field: "score"})].(float64)
			vals = append(vals, v)
			total += v
		}
		pt := out.Overlays[3].Payload.Series.Entries
		if len(pt) != len(vals) {
			t.Fatalf("prior/total entries = %d, want %d", len(pt), len(vals))
		}
		for i, v := range vals {
			want := v / total
			if i > 0 {
				want++
			}
			if !cfSame(*pt[i].Summary.Statistic, want) {
				t.Errorf("entry %d: %v, want %v", i, *pt[i].Summary.Statistic, want)
			}
		}
		if out.Overlays[1].Name != "f" || out.Overlays[1].Kind != types.OverlayKindFormula || out.Overlays[1].Summary == nil || *out.Overlays[1].Summary.Count != len(idx.Entries) {
			t.Errorf("formula layer metadata = %+v", out.Overlays[1])
		}
	})

	t.Run("matrix", func(t *testing.T) {
		out := cfRun(t, p, func() *pulse.ComposedRequest {
			return cfSlots(types.OverlayShapeMatrix,
				cfSpec("idx", types.OverlayKindIndexVsRef, types.OverlayScopeCell, ""),
				cfSpec("f", types.OverlayKindFormula, types.OverlayScopeCell, "cell / ref_cell * 100"),
				cfSpec("slots", types.OverlayKindFormula, types.OverlayScopeCell, "slot.b.cell - cell + slot.a.cell - ref_cell"))
		})
		idx, f := out.Overlays[0].Payload.Matrix, out.Overlays[1].Payload.Matrix
		if f == nil || len(f.Cells) == 0 || len(f.Cells) != len(idx.Cells) {
			t.Fatalf("formula layer = %+v", out.Overlays[1])
		}
		seen := 0
		for r := range idx.Cells {
			for c := range idx.Cells[r] {
				if idx.Cells[r][c].Present != f.Cells[r][c].Present || !cfSame(idx.Cells[r][c].Value.(float64), f.Cells[r][c].Value.(float64)) {
					t.Errorf("cell %d,%d: formula %+v, index %+v", r, c, f.Cells[r][c], idx.Cells[r][c])
				}
				if f.Cells[r][c].Present {
					seen++
				}
				if s := out.Overlays[2].Payload.Matrix.Cells[r][c]; s.Present && s.Value.(float64) != 0 {
					t.Errorf("slot paths disagree at %d,%d: %v", r, c, s.Value)
				}
			}
		}
		if seen == 0 {
			t.Fatal("vacuous: no present cell")
		}
	})

	t.Run("scalar", func(t *testing.T) {
		out := cfRun(t, p, func() *pulse.ComposedRequest {
			return cfSlots(types.OverlayShapeScalar,
				cfSpec("f", types.OverlayKindFormula, types.OverlayScopeTotal, "value - ref + slot.a.value - slot.b.value"),
				cfSpec("d", types.OverlayKindFormula, types.OverlayScopeTotal, "value - ref"))
		})
		a := out.Responses[0].Data[0][types.AggregationLabelOf(&types.Aggregation{Type: types.AGG_SUM, Field: "score"})].(float64)
		b := out.Responses[1].Data[0][types.AggregationLabelOf(&types.Aggregation{Type: types.AGG_AVERAGE, Field: "score"})].(float64)
		if got := out.Overlays[1].Payload.Scalar; got == nil || *got != b-a {
			t.Fatalf("value - ref = %v, want %v", got, b-a)
		}
		if got := out.Overlays[0].Payload.Scalar; got == nil || *got != 0 {
			t.Errorf("slot paths disagree with value / ref: %v", got)
		}
	})
}

// TestComposeFormula_RefusalParity: a Compose FORMULA reading outside
// its namespace — another shape's variable, an unknown function, a slot
// the spec does not name, a slot field of the wrong shape, no formula —
// is refused with the same code, message and Details on both sides.
func TestComposeFormula_RefusalParity(t *testing.T) {
	ctx := context.Background()
	p, _ := nsFixture(t, pulse.Options{})
	slots := []formulaident.Slot{{Label: "a", Shape: types.OverlayShapeSeries}, {Label: "b", Shape: types.OverlayShapeSeries}}
	for _, c := range []struct {
		name    string
		formula any
	}{
		{"matrix var on a series host", "cell + value"},
		{"unknown function", "pct(value)"},
		{"slot not named", "value - slot.c.value"},
		{"wrong slot field", "slot.a.cell"},
		{"parse", "(value"},
		{"missing", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			build := func() *pulse.ComposedRequest {
				req := cfSlots(types.OverlayShapeSeries,
					cfSpec("ok", types.OverlayKindIndexVsRef, types.OverlayScopeGroup, ""),
					cfSpec("f", types.OverlayKindFormula, types.OverlayScopeGroup, ""))
				req.Overlays[1].Params = map[string]any{}
				if c.formula != nil {
					req.Overlays[1].Params["formula"] = c.formula
				}
				return req
			}
			params := map[string]any{}
			if c.formula != nil {
				params["formula"] = c.formula
			}
			want := formulaident.ComposeRefusal(types.OverlayKindFormula, params, 1, types.OverlayShapeSeries, slots, formulaident.AllowedFuncs(nil, false))
			if want == nil {
				t.Fatal("vacuous: the formula is not refused")
			}
			env, err := p.PredictCompose(ctx, build())
			pe := whFirstError(t, env, err)
			if pe == nil || pe.Code != string(want.Code) || pe.Message != want.Message || nsJSON(t, pe.Details) != nsJSON(t, want.Details) {
				t.Fatalf("predict = %+v, want %s %s", pe, want.Code, nsJSON(t, want.Details))
			}
			_, runErr := p.Compose(ctx, build())
			if runErr == nil {
				t.Fatal("runtime accepted the formula predict refuses")
			}
			ce := codeOf(t, runErr)
			if ce.Code != want.Code || ce.Message != want.Message || nsJSON(t, ce.Details) != nsJSON(t, want.Details) {
				t.Fatalf("runtime = %s %s %q, want predict's", ce.Code, nsJSON(t, ce.Details), ce.Message)
			}
			if c.name == "parse" && ce.Code != perr.PULSE_OVERLAY_FORMULA_PARSE_ERROR {
				t.Errorf("parse code = %s", ce.Code)
			}
		})
	}
}
