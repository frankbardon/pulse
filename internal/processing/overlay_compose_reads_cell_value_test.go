package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// composeReadsCellSlot is a 3×3 crosstab slot response (cells and the
// row / column / grand margins summed from them) over components comps.
func composeReadsCellSlot(cells [3][3]float64, comps *types.CrosstabComponents) *types.Response {
	return &types.Response{
		Crosstab:   &types.CrosstabResult{Matrix: readsCellHost(cells, nil).payload},
		Components: &types.ResponseComponents{Crosstab: comps},
	}
}

// TestComposeReadsCellValue_MatchesHandlers pins
// overlayhost.ComposeReadsCellValue — the categorical-code rule's
// Compose half (U66 #445) — to what each Compose handler reads over
// crosstab slots. Two runs share one components block per slot and
// differ only in the cell (and margin) values: a kind declared
// value-reading must answer differently, a kind declared not
// identically. OVERLAY_T_CELL / OVERLAY_Z_CELL are judged over a cell
// without moments (the params carry variance and n) and over one whose
// components carry mean and variance. A new Compose kind fails here
// until it is classified.
func TestComposeReadsCellValue_MatchesHandlers(t *testing.T) {
	t.Parallel()

	ref := [2][3][3]float64{
		{{12, 30, 8}, {25, 9, 41}, {6, 18, 27}},
		{{40, 7, 22}, {11, 35, 14}, {28, 16, 5}},
	}
	tgt := [2][3][3]float64{
		{{15, 21, 9}, {33, 12, 30}, {8, 26, 19}},
		{{19, 31, 6}, {14, 28, 23}, {37, 9, 17}},
	}
	plain := readsCellComps(func(i, j int) map[string]any { return map[string]any{"n": 100 + 7*i + 3*j} })
	moments := func(shift float64) *types.CrosstabComponents {
		return readsCellComps(func(i, j int) map[string]any {
			return map[string]any{"mean": shift + float64(i*3+j), "variance": 4 + float64(j), "n": 50 + 5*i}
		})
	}
	type arm struct {
		name      string
		momentful bool
	}
	judged := 0
	for _, kind := range types.AllOverlayKinds() {
		if !overlayhost.Accepts(overlayhost.Compose, kind) {
			continue
		}
		arms := []arm{{"plain", false}}
		if kind == types.OverlayKindTCell || kind == types.OverlayKindZCell {
			arms = append(arms, arm{"moments", true})
		}
		for _, a := range arms {
			t.Run(string(kind)+"/"+a.name, func(t *testing.T) {
				spec := types.ComposeOverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Reference: "ref", Targets: []string{"t"}}
				switch kind {
				case types.OverlayKindChiSqVsRef:
					spec.Scope = types.OverlayScopeMatrix
				case types.OverlayKindFormula:
					spec.Params = map[string]any{"formula": "cell - ref_cell"}
				case types.OverlayKindTCell, types.OverlayKindZCell, types.OverlayKindTVsRef, types.OverlayKindZVsRef:
					spec.Params = map[string]any{"variance": 4.0, "sample_size": 9.0}
				}
				refComps, tgtComps := plain, plain
				if a.momentful {
					refComps, tgtComps = moments(10), moments(13)
				}
				run := func(k int) string {
					layers, _, err := ApplyComposeOverlays([]types.ComposeOverlaySpec{spec},
						[]*types.Response{composeReadsCellSlot(ref[k], refComps), composeReadsCellSlot(tgt[k], tgtComps)}, []string{"ref", "t"})
					if err != nil {
						// A kind with no crosstab arm (the two-sample
						// vs-ref kinds read grouped rows) refuses both
						// runs alike: it reads no cell.
						return "error: " + err.Error()
					}
					out, err := json.Marshal(layers)
					if err != nil {
						t.Fatal(err)
					}
					return string(out)
				}
				la, lb := run(0), run(1)
				want := overlayhost.ComposeReadsCellValue(kind, a.momentful)
				computed := strings.Contains(la, `"present":true`) || strings.Contains(la, `"statistic":`) || strings.Contains(la, `"scalar":`)
				if seriesOnly := kind == types.OverlayKindTVsRef || kind == types.OverlayKindZVsRef; computed == seriesOnly {
					// Every crosstab arm must compute something (else the
					// comparison is vacuous); a series-only kind must not.
					t.Fatalf("layer computed %v over crosstab slots: %s", computed, la)
				}
				if got := la != lb; got != want {
					t.Fatalf("answers differ with the cell values: %v; ComposeReadsCellValue declares %v\nA %s\nB %s", got, want, la, lb)
				}
				judged++
			})
		}
	}
	if judged < 14 {
		t.Fatalf("judged %d Compose arms, want every Compose kind (plus the moment arms)", judged)
	}
}
