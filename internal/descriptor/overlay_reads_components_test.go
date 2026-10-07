package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// OverlayReadsHostComponents is the rule a `return` skip obeys (the
// service's compute-plan veto): the pairwise family, every
// ScalesByHostFloor kind (so validateOverlayHiddenFloor's refusal set is
// a subset of it) and Fisher's weighted floor stamp read the host's
// components; a payload-only kind does not.
func TestOverlayReadsHostComponents(t *testing.T) {
	for _, kind := range types.AllOverlayKinds() {
		reads := OverlayReadsHostComponents(kind)
		if types.IsPairwiseOverlayKind(kind) && !reads {
			t.Errorf("%s: pairwise kind reads per-cell floors, yet not flagged", kind)
		}
		if weighting.ScalesByHostFloor(kind) && !reads {
			t.Errorf("%s: scales by the host floor, yet not flagged", kind)
		}
	}
	if !OverlayReadsHostComponents(types.OverlayKindFisherExactCell) {
		t.Error("OVERLAY_FISHER_EXACT_CELL stamps the weighted floor, yet not flagged")
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindShareOfRow, types.OverlayKindIndexVsMargin, types.OverlayKindFormula} {
		if OverlayReadsHostComponents(kind) {
			t.Errorf("%s: payload-only kind flagged", kind)
		}
	}
}
