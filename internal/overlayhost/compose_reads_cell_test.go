package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestComposeReadsCellValue pins the Compose half of the
// categorical-code rule's cell classification (U66 #445): every Compose
// kind is classified for both cell flavours, the moment cell kinds read
// the value only over a cell without moments, and no kind off the
// Compose host reads a cell there. Processing's
// TestComposeReadsCellValue_MatchesHandlers pins the table to what each
// handler reads.
func TestComposeReadsCellValue(t *testing.T) {
	t.Parallel()

	compose := map[types.OverlayKind][2]bool{ // {no moments, moments}
		types.OverlayKindIndexVsRef:      {true, true},
		types.OverlayKindDeltaVsRef:      {true, true},
		types.OverlayKindPropZCell:       {true, true},
		types.OverlayKindPropZPanel:      {true, true},
		types.OverlayKindChiSqVsRef:      {true, true},
		types.OverlayKindRank:            {true, true},
		types.OverlayKindPanelIndexVsRef: {true, true},
		types.OverlayKindFormula:         {true, true},
		types.OverlayKindTCell:           {true, false},
		types.OverlayKindZCell:           {true, false},
		types.OverlayKindTVsRef:          {false, false},
		types.OverlayKindZVsRef:          {false, false},
	}
	for _, k := range types.AllOverlayKinds() {
		want, onCompose := compose[k]
		if Accepts(Compose, k) != onCompose {
			t.Errorf("%s: Compose host %v, table %v — classify it", k, Accepts(Compose, k), onCompose)
		}
		if got := [2]bool{ComposeReadsCellValue(k, false), ComposeReadsCellValue(k, true)}; got != want {
			t.Errorf("ComposeReadsCellValue(%s) = %v, want %v", k, got, want)
		}
	}
}
