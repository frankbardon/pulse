package service

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// stdResidualRef is the slice of std_residual.json
// (scripts/reference/gen_std_residual.R) the multiplicity fold reads: R's
// two-sided p per cell and p.adjust over each row (nil = R NaN).
type stdResidualRef struct {
	Name     string       `json:"name"`
	StdRes   [][]*float64 `json:"stdres"`
	P        [][]*float64 `json:"p"`
	PRowHolm [][]*float64 `json:"p_row_holm"`
	PRowBH   [][]*float64 `json:"p_row_bh"`
}

func loadStdResidualRef(t *testing.T) []stdResidualRef {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "std_residual.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if i := strings.LastIndex(body, "\n// golden-hash: "); i >= 0 {
		body = body[:i]
	}
	var doc struct {
		Cases []stdResidualRef `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("std_residual.json holds no case")
	}
	return doc.Cases
}

// refMatrix lays an R matrix out as present float cells (null → NaN).
func refMatrix(m [][]*float64, label string) *types.MatrixPayload {
	out := &types.MatrixPayload{RowHeader: types.AxisHeader{Fields: []string{"r"}}, ColumnHeader: types.AxisHeader{Fields: []string{"c"}}, CellLabel: label}
	for i, row := range m {
		out.RowKeys = append(out.RowKeys, types.AxisKey{string(rune('a' + i))})
		cells := make([]types.MatrixCell, len(row))
		for j, v := range row {
			x := math.NaN()
			if v != nil {
				x = *v
			}
			cells[j] = types.MatrixCell{Value: x, Present: true}
		}
		out.Cells = append(out.Cells, cells)
	}
	for j := range m[0] {
		out.ColumnKeys = append(out.ColumnKeys, types.AxisKey{string(rune('u' + j))})
	}
	return out
}

// TestFoldOverlayMultiplicity_StdResidualRowMatchesPAdjust: a `row`
// family on an OVERLAY_STD_RESIDUAL layer reads its p part and adjusts
// each row as R's p.adjust does on the same row (NaN p-values left out
// of the family, as p.adjust leaves NA out of n); the adjusted figures
// ride payload.p_adjusted, while the residual matrix and the raw p part
// stay byte-identical.
func TestFoldOverlayMultiplicity_StdResidualRowMatchesPAdjust(t *testing.T) {
	t.Parallel()

	methods := map[types.MultiplicityMethod]func(stdResidualRef) [][]*float64{
		types.MultiplicityMethodHolm: func(c stdResidualRef) [][]*float64 { return c.PRowHolm },
		types.MultiplicityMethodBH:   func(c stdResidualRef) [][]*float64 { return c.PRowBH },
	}
	for _, c := range loadStdResidualRef(t) {
		for method, want := range methods {
			t.Run(c.Name+"/"+string(method), func(t *testing.T) {
				layer := types.OverlayLayer{Kind: types.OverlayKindStdResidual, Payload: types.OverlayPayload{
					Shape:  types.OverlayShapeMatrix,
					Matrix: refMatrix(c.StdRes, "std_residual"),
					Parts:  map[string]*types.OverlayPayload{processing.StdResidualPartP: {Shape: types.OverlayShapeMatrix, Matrix: refMatrix(c.P, "p")}},
				}}
				resp := &types.Response{Overlays: []types.OverlayLayer{layer}}
				beforeBase := mustMarshal(t, resp.Overlays[0].Payload.Matrix)
				beforeP := mustMarshal(t, resp.Overlays[0].Payload.Parts[processing.StdResidualPartP])
				plan := &descx.MultiplicityPlan{Overlays: []descx.ResolvedMultiplicity{
					overlayMember("overlays[0]", types.OverlayKindStdResidual, method, types.MultiplicityFamilyRow, 0.05),
				}}
				if err := foldRequestMultiplicity(plan, resp); err != nil {
					t.Fatal(err)
				}
				l := resp.Overlays[0]
				if l.Payload.PAdjusted == nil || l.Payload.SignificantAdjusted == nil {
					t.Fatal("no p_adjusted / significant_adjusted on the payload")
				}
				for i, row := range want(c) {
					for j, w := range row {
						got := l.Payload.PAdjusted.Cells[i][j]
						v, _ := got.Value.(float64)
						if w == nil {
							if got.Present && !math.IsNaN(v) {
								t.Errorf("p_adjusted[%d][%d] = %v, want undefined (R NA)", i, j, v)
							}
							continue
						}
						if !got.Present || math.Abs(v-*w) > 1e-12*math.Max(1, *w) {
							t.Errorf("p_adjusted[%d][%d] = %+v, want %.17g (R p.adjust)", i, j, got, *w)
						}
						sig, _ := l.Payload.SignificantAdjusted.Cells[i][j].Value.(bool)
						if sig != (*w < 0.05) {
							t.Errorf("significant_adjusted[%d][%d] = %v for adjusted p %v", i, j, sig, *w)
						}
					}
				}
				if l.Multiplicity == nil || l.Multiplicity.Family != types.MultiplicityFamilyRow || len(l.Multiplicity.MPer) != len(c.P) {
					t.Errorf("echo %+v, want a row echo with one m per row", l.Multiplicity)
				}
				if !bytes.Equal(beforeBase, mustMarshal(t, l.Payload.Matrix)) {
					t.Error("residual matrix changed under the correction")
				}
				if !bytes.Equal(beforeP, mustMarshal(t, l.Payload.Parts[processing.StdResidualPartP])) {
					t.Error("raw p part changed under the correction")
				}
			})
		}
	}
}

// TestMultiplicityFold_CrosstabStdResidual runs the fold over a real
// crosstab on both arms: `row` and `layer` families adjust the layer's
// p part exactly as the core adjusts the baseline's raw p-values; the
// baseline (no multiplicity) carries no adjusted slot; the raw p part
// and the residuals never move; fused and buffered agree byte for byte.
func TestMultiplicityFold_CrosstabStdResidual(t *testing.T) {
	t.Parallel()

	fused, buffered := overlayFoldService(t)
	ctx := context.Background()
	mk := func(m *types.Multiplicity) *types.Request {
		r := overlayCrosstabRequest(false)
		r.Overlays = []types.OverlaySpec{{Name: "sr", Kind: types.OverlayKindStdResidual, Scope: types.OverlayScopeCell, Multiplicity: m}}
		return r
	}
	base, err := buffered.Process(ctx, mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	bl := base.Overlays[0]
	if bl.Payload.PAdjusted != nil || bl.Multiplicity != nil {
		t.Fatal("baseline carries adjusted slots without a multiplicity block")
	}
	rawP := bl.Payload.Parts[processing.StdResidualPartP].Matrix
	for _, family := range []types.MultiplicityFamily{types.MultiplicityFamilyRow, types.MultiplicityFamilyLayer} {
		t.Run(string(family), func(t *testing.T) {
			m := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: family}
			got, err := buffered.Process(ctx, mk(m))
			if err != nil {
				t.Fatal(err)
			}
			l := got.Overlays[0]
			groups := [][][2]int{}
			if family == types.MultiplicityFamilyLayer {
				var all [][2]int
				for r := range rawP.Cells {
					for c := range rawP.Cells[r] {
						all = append(all, [2]int{r, c})
					}
				}
				groups = append(groups, all)
			} else {
				for r := range rawP.Cells {
					var row [][2]int
					for c := range rawP.Cells[r] {
						row = append(row, [2]int{r, c})
					}
					groups = append(groups, row)
				}
			}
			for _, g := range groups {
				ps := make([]float64, len(g))
				for k, rc := range g {
					ps[k] = rawP.Cells[rc[0]][rc[1]].Value.(float64)
				}
				adj, _ := multiplicity.Adjust(multiplicity.MethodHolm, ps)
				for k, rc := range g {
					if v := l.Payload.PAdjusted.Cells[rc[0]][rc[1]].Value.(float64); !sameFloat(v, adj[k]) {
						t.Errorf("(%d,%d) p_adjusted %v, want %v", rc[0], rc[1], v, adj[k])
					}
				}
			}
			if a, b := mustMarshal(t, rawP), mustMarshal(t, l.Payload.Parts[processing.StdResidualPartP].Matrix); !bytes.Equal(a, b) {
				t.Error("raw p part changed under the correction")
			}
			if a, b := mustMarshal(t, bl.Payload.Matrix), mustMarshal(t, l.Payload.Matrix); !bytes.Equal(a, b) {
				t.Error("residuals changed under the correction")
			}
			fu, err := fused.Process(ctx, mk(m))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mustMarshal(t, fu.Overlays), mustMarshal(t, got.Overlays)) {
				t.Error("fused and buffered layers differ")
			}
		})
	}
}
