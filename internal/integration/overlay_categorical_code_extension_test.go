package integration_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// U66 #447. The categorical-code cell rule judges an EXTENSION overlay
// kind by its registration: a kind with a MATRIX handler (Process
// crosstab host) or a Compose handler (a Compose crosstab slot) reads
// the cell VALUES unless it declares ReadsCellCountsOnly, so over a
// code cell (AGG_MODE over a categorical / packed_bool field) it is
// refused exactly like a built-in value-reading kind — predict (first)
// and runtime alike, the declaration reaching predict through the
// instance snapshot and the runtime through the same registration. A
// counts-only kind is not judged and runs.

const (
	ccExtMatrixKind  = types.OverlayKind("OVERLAY_CCTEST_CELL_N")
	ccExtComposeKind = types.OverlayKind("OVERLAY_CCTEST_SLOT_N")
)

// ccExtCountGrid is a MATRIX layer of the host's per-cell record counts.
func ccExtCountGrid(h extend.MatrixOverlayHost) types.OverlayPayload {
	m := &types.MatrixPayload{RowHeader: h.RowHeader(), ColumnHeader: h.ColumnHeader(), CellLabel: "n"}
	for r := 0; r < h.Rows(); r++ {
		m.RowKeys = append(m.RowKeys, h.RowKey(r))
		row := make([]types.MatrixCell, h.Columns())
		for c := range row {
			if n, ok := h.CellN(r, c); ok {
				row[c] = types.MatrixCell{Value: float64(n), Present: true}
			}
		}
		m.Cells = append(m.Cells, row)
	}
	for c := 0; c < h.Columns(); c++ {
		m.ColumnKeys = append(m.ColumnKeys, h.ColumnKey(c))
	}
	return types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: m}
}

// ccExtRegistrations: a MATRIX-host kind and a Compose kind, both
// reading the cells' record counts, countsOnly as declared.
func ccExtRegistrations(countsOnly bool) []pulse.OverlayRegistration {
	return []pulse.OverlayRegistration{
		{
			Name: ccExtMatrixKind, Description: "per-cell record count",
			Handler: extend.OverlayHandler{Matrix: func(_ *types.OverlaySpec, h extend.MatrixOverlayHost) (extend.OverlayOutput, error) {
				return extend.OverlayOutput{Payload: ccExtCountGrid(h)}, nil
			}},
			Shapes: []types.OverlayShape{types.OverlayShapeMatrix}, Scopes: []types.OverlayScope{types.OverlayScopeCell},
			ReadsCellCountsOnly: countsOnly,
		},
		{
			Name: ccExtComposeKind, Description: "target per-cell record count",
			Handler: extend.OverlayHandler{Compose: func(_ *types.ComposeOverlaySpec, h extend.ComposeOverlayHost) (extend.OverlayOutput, error) {
				tgt := h.Target(0)
				if tgt == nil || tgt.Matrix() == nil {
					return extend.OverlayOutput{}, fmt.Errorf("no crosstab target")
				}
				return extend.OverlayOutput{Payload: ccExtCountGrid(tgt.Matrix())}, nil
			}},
			Shapes: []types.OverlayShape{types.OverlayShapeMatrix}, Scopes: []types.OverlayScope{types.OverlayScopeCell},
			ReadsCellCountsOnly: countsOnly,
		},
	}
}

// TestExtensions_OverlayCodeCellJudgedByDeclaration: over a code cell,
// an extension MATRIX kind and an extension Compose kind are refused
// (predict first, runtime equal, messages equal) unless they declare
// ReadsCellCountsOnly, in which case both sides accept and the layer
// lands; over a count cell both run either way.
func TestExtensions_OverlayCodeCellJudgedByDeclaration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, countsOnly := range []bool{false, true} {
		p, data := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Overlays: ccExtRegistrations(countsOnly)}})
		matrixSpec := types.OverlaySpec{Name: "o", Kind: ccExtMatrixKind, Scope: types.OverlayScopeCell}
		composeSpec := types.ComposeOverlaySpec{Name: "o", Kind: ccExtComposeKind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}
		for _, c := range []struct{ col, field, ft string }{{"flag", "color", "categorical_u8"}, {"color", "flag", "packed_bool"}} {
			t.Run(fmt.Sprintf("process/%s/counts_only=%v", c.ft, countsOnly), func(t *testing.T) {
				req := func() *pulse.Request { return ccCrosstab(c.col, types.AGG_MODE, c.field, matrixSpec) }
				pe := predictCodedError(t, p, data, req())
				out, err := p.Process(ctx, req())
				if countsOnly {
					if pe != nil {
						t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
					}
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					if len(out.Overlays) != 1 {
						t.Fatalf("overlays = %s, want one layer", nsJSON(t, out.Overlays))
					}
					return
				}
				want := map[string]any{"aggregation": "AGG_MODE", "label": "c", "slot": "crosstab.cell",
					"reason": "categorical_code", "field": c.field, "field_type": c.ft}
				if pe == nil || pe.Code != string(perr.PROCESSING_CONFIG) || !reflect.DeepEqual(pe.Details, want) {
					t.Fatalf("Predict = %+v, want PROCESSING_CONFIG %v first", pe, want)
				}
				ce := codeOf(t, err)
				if ce.Code != perr.PROCESSING_CONFIG || !reflect.DeepEqual(ce.Details, want) {
					t.Fatalf("Process = %s %v, want PROCESSING_CONFIG %v", ce.Code, ce.Details, want)
				}
				if ce.Message != pe.Message {
					t.Errorf("message drift: runtime %q, predict %q", ce.Message, pe.Message)
				}
				if lead := "overlays[0] (" + string(ccExtMatrixKind) + ") reads the crosstab cell values"; !strings.HasPrefix(ce.Message, lead) {
					t.Errorf("message = %q, want it to lead with %q", ce.Message, lead)
				}
			})
			t.Run(fmt.Sprintf("compose/%s/counts_only=%v", c.ft, countsOnly), func(t *testing.T) {
				req := func() *pulse.ComposedRequest {
					return ccCellCompose(c.col, types.AGG_MODE, c.field, false, composeSpec)
				}
				if countsOnly {
					ccComposeNotCoded(t, p, req)
					out, err := p.Compose(ctx, req())
					if err != nil {
						t.Fatalf("Compose: %v", err)
					}
					if len(out.Overlays) != 1 {
						t.Fatalf("overlays = %s, want one layer", nsJSON(t, out.Overlays))
					}
					return
				}
				ccComposeRefused(t, p, req, map[string]any{"overlay_index": 0, "kind": string(ccExtComposeKind), "slot": "b",
					"host": "requests[1].crosstab.cell", "aggregation": "AGG_MODE", "label": "c", "reason": "categorical_code",
					"field": c.field, "field_type": c.ft})
			})
		}
		t.Run(fmt.Sprintf("count_cells_run/counts_only=%v", countsOnly), func(t *testing.T) {
			req := func() *pulse.Request { return ccCrosstab("flag", types.AGG_COUNT, "x", matrixSpec) }
			if pe := predictCodedError(t, p, data, req()); pe != nil {
				t.Fatalf("Predict = %s %v, want clean", pe.Code, pe.Details)
			}
			if _, err := p.Process(ctx, req()); err != nil {
				t.Fatalf("Process: %v", err)
			}
			creq := func() *pulse.ComposedRequest { return ccCellCompose("flag", types.AGG_COUNT, "x", false, composeSpec) }
			env, err := p.PredictCompose(ctx, creq())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("PredictCompose errors = %s, want none", nsJSON(t, env.Errors))
			}
			if _, err := p.Compose(ctx, creq()); err != nil {
				t.Fatalf("Compose: %v", err)
			}
		})
	}
}
