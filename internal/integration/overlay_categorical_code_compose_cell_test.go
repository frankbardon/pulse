package integration_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// U66 #445. The categorical-code cell rule reaches the Compose and
// whole-chain hosts: a Compose crosstab slot, or a chain crosstab stage,
// whose CELL aggregation is a code (AGG_MODE over a categorical /
// packed_bool field — the modal dictionary index / truth value in every
// cell and margin) is refused for every kind that reads the cell value
// (overlayhost.ComposeReadsCellValue / ChainReadsCellValue), with the
// Process host's PROCESSING_CONFIG, reason categorical_code, on predict
// (first) and runtime alike. OVERLAY_T_CELL / OVERLAY_Z_CELL read the
// value only over a cell without moments.

// ccComposeCellKinds are the Compose kinds that read the cell value
// over a cell with no moments (every Compose kind a crosstab slot
// carries).
var ccComposeCellKinds = []types.OverlayKind{
	types.OverlayKindIndexVsRef, types.OverlayKindDeltaVsRef, types.OverlayKindPanelIndexVsRef,
	types.OverlayKindPropZCell, types.OverlayKindPropZPanel, types.OverlayKindChiSqVsRef,
	types.OverlayKindRank, types.OverlayKindFormula, types.OverlayKindTCell, types.OverlayKindZCell,
}

// ccComposeCellSpec is a valid spec of kind over crosstab slots a
// (reference) and b (target).
func ccComposeCellSpec(kind types.OverlayKind) types.ComposeOverlaySpec {
	spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}
	params := map[string]any{}
	switch kind {
	case types.OverlayKindChiSqVsRef:
		spec.Scope = types.OverlayScopeMatrix
	case types.OverlayKindFormula:
		params["formula"] = "cell - ref_cell"
	case types.OverlayKindTCell, types.OverlayKindZCell:
		// A scalar cell is the side's mean; its variance and n come
		// from params.
		params["variance"], params["sample_size"] = 4.0, 9.0
	}
	if len(params) > 0 {
		spec.Params = params
	}
	return spec
}

// ccCellSlot is one region × col crosstab slot scoring typ over field.
func ccCellSlot(label, col string, typ types.AggregationType, field string) *types.Request {
	return &types.Request{Label: label, Cohort: &types.Cohort{Filename: "cc.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: col}},
			Cell:    &types.Aggregation{Type: typ, Field: field, Label: "c"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		}}
}

// ccCellCompose: reference slot a scores a count cell (refCode false)
// or the code cell, target b the code cell; specs follow.
func ccCellCompose(col string, typ types.AggregationType, field string, refCode bool, specs ...types.ComposeOverlaySpec) *pulse.ComposedRequest {
	refTyp, refField := types.AGG_COUNT, "x"
	if refCode {
		refTyp, refField = typ, field
	}
	return &pulse.ComposedRequest{Requests: []*types.Request{ccCellSlot("a", col, refTyp, refField), ccCellSlot("b", col, typ, field)},
		Overlays: specs}
}

// ccComposeRefused asserts predict (first) and runtime refuse req with
// PROCESSING_CONFIG wantDetails, messages equal; it returns the message.
func ccComposeRefused(t *testing.T, p *pulse.Pulse, req func() *pulse.ComposedRequest, wantDetails map[string]any) string {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(env.Errors[0].Details, wantDetails) {
		t.Fatalf("PredictCompose = %+v, want PROCESSING_CONFIG %v first", env.Errors, wantDetails)
	}
	_, err = p.Compose(ctx, req())
	ce := codeOf(t, err)
	if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, wantDetails) {
		t.Fatalf("Compose = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, wantDetails)
	}
	if ce.Message != env.Errors[0].Message {
		t.Errorf("message drift: runtime %q, predict %q", ce.Message, env.Errors[0].Message)
	}
	return ce.Message
}

// ccComposeNotCoded asserts neither predict nor runtime refuses req
// with reason categorical_code.
func ccComposeNotCoded(t *testing.T, p *pulse.Pulse, req func() *pulse.ComposedRequest) {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictCompose(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range env.Errors {
		if e.Details["reason"] == "categorical_code" {
			t.Fatalf("PredictCompose = %+v, want no categorical_code refusal", env.Errors)
		}
	}
	if _, err := p.Compose(ctx, req()); err != nil && codeOf(t, err).Details["reason"] == "categorical_code" {
		t.Fatalf("Compose: %v, want no categorical_code refusal", err)
	}
}

// TestComposeOverlay_CategoricalCodeCellRefused: every Compose kind
// that reads cell values runs over count cells and is refused over a
// code cell on the target — or the reference — slot, predict first,
// messages equal; AGG_MODE over the numeric x still runs.
func TestComposeOverlay_CategoricalCodeCellRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := ccFixture(t, pulse.Options{})
	for _, kind := range ccComposeCellKinds {
		t.Run(string(kind)+"/count_cells_run", func(t *testing.T) {
			req := func() *pulse.ComposedRequest {
				return ccCellCompose("flag", types.AGG_COUNT, "x", false, ccComposeCellSpec(kind))
			}
			env, err := p.PredictCompose(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("PredictCompose errors = %s, want none", nsJSON(t, env.Errors))
			}
			out, err := p.Compose(ctx, req())
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(out.Overlays) == 0 {
				t.Fatal("no overlay layer")
			}
		})
		t.Run(string(kind)+"/mode_numeric_cell_runs", func(t *testing.T) {
			ccComposeNotCoded(t, p, func() *pulse.ComposedRequest {
				return ccCellCompose("flag", types.AGG_MODE, "x", true, ccComposeCellSpec(kind))
			})
		})
		for _, c := range []struct{ col, field, ft string }{{"flag", "color", "categorical_u8"}, {"color", "flag", "packed_bool"}} {
			for _, refCode := range []bool{false, true} {
				// The reference slot is judged first: a code reference
				// is named, else the code target.
				slot, j := "b", 1
				if refCode {
					slot, j = "a", 0
				}
				t.Run(fmt.Sprintf("%s/mode_%s_cell_refused/slot_%s", kind, c.ft, slot), func(t *testing.T) {
					req := func() *pulse.ComposedRequest {
						return ccCellCompose(c.col, types.AGG_MODE, c.field, refCode, ccComposeCellSpec(kind))
					}
					host := fmt.Sprintf("requests[%d].crosstab.cell", j)
					msg := ccComposeRefused(t, p, req, map[string]any{"overlay_index": 0, "kind": string(kind), "slot": slot, "host": host,
						"aggregation": "AGG_MODE", "label": "c", "reason": "categorical_code", "field": c.field, "field_type": c.ft})
					if want := "overlays[0] (" + string(kind) + ") reads slot \"" + slot + "\"'s crosstab cell values"; !strings.HasPrefix(msg, want) {
						t.Errorf("message = %q, want it to lead with %q", msg, want)
					}
				})
			}
		}
	}
	// The two-sample vs-ref kinds read grouped rows only; a crosstab
	// slot is their own input rule's, never the code rule's.
	for _, kind := range []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef} {
		t.Run(string(kind)+"/not_judged", func(t *testing.T) {
			ccComposeNotCoded(t, p, func() *pulse.ComposedRequest {
				return ccCellCompose("flag", types.AGG_MODE, "color", true, ccComposeCellSpec(kind))
			})
		})
	}
	// No overlay, no judgement.
	if _, err := p.Compose(ctx, ccCellCompose("flag", types.AGG_MODE, "color", true)); err != nil {
		t.Fatalf("overlay-free code crosstabs: %v", err)
	}
}

// ccMomentPick is an extension aggregator picking its field's first
// value (EmitsInputValue) whose components carry mean and variance —
// the one shape on which OVERLAY_T_CELL / OVERLAY_Z_CELL's reading
// depends on the components gate.
type ccMomentPick struct {
	field string
	last  float64
}

func (a *ccMomentPick) Aggregate(rows extend.Rows, _ string) (float64, error) {
	if rows.Len() == 0 {
		return 0, nil
	}
	a.last, _ = rows.At(0).NumericValue(a.field)
	return a.last, nil
}

// TestComposeOverlay_CategoricalCodeCellMoments: over a code cell whose
// components carry mean and variance, OVERLAY_T_CELL / OVERLAY_Z_CELL
// read those moments (not judged) while the components are on, and the
// cell value — refused — once the slot is built with components
// disabled; a value-reading kind is refused either way.
func TestComposeOverlay_CategoricalCodeCellMoments(t *testing.T) {
	t.Parallel()

	const typ = types.AggregationType("AGG_CCTEST_MOMENT_PICK")
	reg := pulse.AggregatorRegistration{
		Name:            typ,
		Description:     "Test aggregator returning the first value of its field, with mean and variance components.",
		EmitsInputValue: true,
		ComponentSchema: welfordExtSchema(),
		ComponentsFunc: func(instance extend.Aggregator) (map[string]any, error) {
			return map[string]any{"mean": instance.(*ccMomentPick).last, "variance": 1.0}, nil
		},
		Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
			return &ccMomentPick{field: spec.Field}, nil
		},
	}
	p, _ := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}})
	off := true
	for _, kind := range []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell, types.OverlayKindIndexVsRef} {
		value := kind == types.OverlayKindIndexVsRef
		for _, disabled := range []bool{false, true} {
			req := func() *pulse.ComposedRequest {
				r := ccCellCompose("flag", typ, "color", true, ccComposeCellSpec(kind))
				if disabled {
					r.Requests[1].DisableComponents = &off
				}
				return r
			}
			t.Run(fmt.Sprintf("%s/components_disabled=%v", kind, disabled), func(t *testing.T) {
				if !value && !disabled {
					ccComposeNotCoded(t, p, req)
					return
				}
				// The reference slot keeps its moments: only a value
				// reader is refused there; the moment kinds reach the
				// target slot, whose components are off.
				slot, j := "a", 0
				if !value {
					slot, j = "b", 1
				}
				ccComposeRefused(t, p, req, map[string]any{"overlay_index": 0, "kind": string(kind), "slot": slot,
					"host": fmt.Sprintf("requests[%d].crosstab.cell", j), "aggregation": string(typ), "label": "c",
					"reason": "categorical_code", "field": "color", "field_type": "categorical_u8"})
			})
		}
	}
}

// ccCellChain is a one-stage chain whose stage 0 is a region × flag
// crosstab with an AGG_MODE-over-color (code) cell, kind comparing it
// with itself.
func ccCellChain(kind types.OverlayKind) *pulse.ChainRequest {
	zero := 0
	stage := ccCellSlot("", "flag", types.AGG_MODE, "color")
	stage.Cohort = nil
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "cc.pulse"},
		Stages: []*types.ChainStage{{Name: "s0", Request: stage}},
		Overlays: []*types.ChainOverlaySpec{{Name: "o", Kind: kind, Scope: types.OverlayScopeCell,
			Ref: types.StageRef{Index: &zero}, Target: types.StageRef{Index: &zero}}}}
}

// TestChainOverlay_CrosstabStageNeverScored: the chain half of #445 has
// no host to judge — a crosstab stage is refused
// PULSE_CHAIN_NOT_MERGEABLE (mergegate.ChainRefusal) on predict (first,
// and only) and runtime alike, so a whole-chain overlay never folds a
// code cell.
func TestChainOverlay_CrosstabStageNeverScored(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := ccFixture(t, pulse.Options{})
	for _, kind := range []types.OverlayKind{types.OverlayKindIndexVsStage, types.OverlayKindDeltaVsStage} {
		t.Run(string(kind), func(t *testing.T) {
			want := map[string]any{"stage_index": 0, "stage_name": "s0"}
			env, err := p.PredictChain(ctx, ccCellChain(kind))
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_CHAIN_NOT_MERGEABLE) || !reflect.DeepEqual(env.Errors[0].Details, want) {
				t.Fatalf("PredictChain = %s, want PULSE_CHAIN_NOT_MERGEABLE %v alone", nsJSON(t, env.Errors), want)
			}
			_, err = p.ProcessChain(ctx, ccCellChain(kind))
			if ce := codeOf(t, err); ce.Code != perr.PULSE_CHAIN_NOT_MERGEABLE || !reflect.DeepEqual(ce.Details, want) {
				t.Fatalf("ProcessChain = %s %v, want PULSE_CHAIN_NOT_MERGEABLE %v", ce.Code, ce.Details, want)
			}
		})
	}
}
