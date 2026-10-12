package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// readsCellHost is a 3×3 crosstab whose cells (and the row / column /
// grand margins summed from them) are cells, over the components block
// comps — shared between two hosts that differ only in their values.
func readsCellHost(cells [3][3]float64, comps *types.CrosstabComponents) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		RowKeys:      []types.AxisKey{{"a"}, {"b"}, {"c"}},
		ColumnKeys:   []types.AxisKey{{"x"}, {"y"}, {"z"}},
	}
	var cols [3]float64
	grand := 0.0
	for i := range cells {
		row := make([]types.MatrixCell, 3)
		sum := 0.0
		for j, v := range cells[i] {
			row[j] = types.MatrixCell{Value: v, Present: true}
			sum += v
			cols[j] += v
		}
		grand += sum
		mx.Cells = append(mx.Cells, row)
		mx.RowMargins = append(mx.RowMargins, types.MatrixCell{Value: sum, Present: true})
	}
	for _, v := range cols {
		mx.ColumnMargins = append(mx.ColumnMargins, types.MatrixCell{Value: v, Present: true})
	}
	mx.GrandTotal = types.MatrixCell{Value: grand, Present: true}
	return newCrosstabHostViewWithComponents(mx, comps)
}

// readsCellComps is a components block every cell of which carries m.
func readsCellComps(m func(i, j int) map[string]any) *types.CrosstabComponents {
	c := &types.CrosstabComponents{}
	for i := 0; i < 3; i++ {
		row := make([]map[string]any, 3)
		for j := range row {
			row[j] = m(i, j)
		}
		c.CellComponents = append(c.CellComponents, row)
	}
	return c
}

// TestOverlayReadsCellValue_MatchesHandlers pins
// overlayhost.ReadsCellValue — the categorical-code rule's MATRIX half —
// to what each MATRIX handler actually reads. Two hosts share one
// components block and differ only in their cell (and margin) values: a
// kind declared value-reading must answer differently over them, a kind
// declared not (the moment pairwise kinds) identically. A new MATRIX
// kind fails here until it is classified.
func TestOverlayReadsCellValue_MatchesHandlers(t *testing.T) {
	a := [3][3]float64{{12, 30, 8}, {25, 9, 41}, {6, 18, 27}}
	b := [3][3]float64{{40, 7, 22}, {11, 35, 14}, {28, 16, 5}}
	plain := readsCellComps(func(i, j int) map[string]any { return map[string]any{"n": 100 + 7*i + 3*j} })
	welford := readsCellComps(func(i, j int) map[string]any {
		return map[string]any{"mean": 10 + float64(i*3+j), "variance": 4 + float64(j), "n": 50 + 5*i}
	})
	weighted := readsCellComps(func(i, j int) map[string]any {
		return map[string]any{"weighted_mean": 10 + float64(i*3+j), "m2_weighted": 200 + 10*float64(j),
			"sum_weights": 60.0 + float64(i), "sum_weights_sq": 90.0 + float64(i), "n": 60}
	})
	judged := 0
	for kind, handler := range overlayHandlers {
		if !overlayhost.AcceptsShape(types.OverlayShapeMatrix, kind) || len(overlayhost.Shapes(kind)) == 0 {
			continue
		}
		t.Run(string(kind), func(t *testing.T) {
			spec := &types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell}
			comps := plain
			switch kind {
			case types.OverlayKindChiSqMatrix:
				spec.Scope = types.OverlayScopeMatrix
			case types.OverlayKindChiSqRow, types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT:
				spec.Scope = types.OverlayScopeRow
			case types.OverlayKindPairwiseWelchT, types.OverlayKindPairwiseTwoMeansZ:
				spec.Scope, comps = types.OverlayScopeRow, welford
			case types.OverlayKindPairwiseWeightedTwoMeansZ:
				spec.Scope, comps = types.OverlayScopeRow, weighted
				spec.Params = json.RawMessage(`{"n_basis":"kish"}`)
			case types.OverlayKindChiSqCol:
				spec.Scope = types.OverlayScopeColumn
			case types.OverlayKindIndexVsMargin, types.OverlayKindDeltaVsMargin, types.OverlayKindZScoreVsMargin, types.OverlayKindShareOfRow:
				spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
			case types.OverlayKindShareOfCol:
				spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}}
			case types.OverlayKindShareOfTotal:
				spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand}}
			case types.OverlayKindFormula:
				spec.Params = json.RawMessage(`{"formula":"cell / margin_grand"}`)
			}
			run := func(cells [3][3]float64) string {
				layer, _, err := handler(spec, readsCellHost(cells, comps))
				if err != nil {
					t.Fatalf("handler: %v", err)
				}
				out, err := json.Marshal(layer)
				if err != nil {
					t.Fatal(err)
				}
				return string(out)
			}
			la, lb := run(a), run(b)
			if !strings.Contains(la, `"present":true`) && !strings.Contains(la, `"statistic":`) {
				t.Fatalf("layer computed nothing (a vacuous comparison): %s", la)
			}
			if got, want := la != lb, overlayhost.ReadsCellValue(kind); got != want {
				t.Fatalf("answers differ with the cell values: %v; ReadsCellValue declares %v\nA %s\nB %s", got, want, la, lb)
			}
			judged++
		})
	}
	if judged < 16 {
		t.Fatalf("judged %d MATRIX kinds, want every MATRIX handler", judged)
	}
}
