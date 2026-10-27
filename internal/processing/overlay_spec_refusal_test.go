package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestApplyOverlays_CrosstabSpecRefusal: the MATRIX dispatcher raises the
// shared Ref / Scope refusal (overlayhost.CrosstabSpecRefusal) for every
// ruled kind before any handler runs — the refusal predict raises,
// carrying the spec's index — and a well-formed spec ahead of it in the
// same request does not mask it.
func TestApplyOverlays_CrosstabSpecRefusal(t *testing.T) {
	t.Parallel()

	host := symmetryHost([]string{"a", "b"}, []string{"a", "b"}, [][]float64{{3, 1}, {2, 4}})
	lead := types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}
	for _, kind := range overlayhost.CrosstabSpecKinds() {
		spec := types.OverlaySpec{Kind: kind, Scope: "nowhere"}
		if kind == types.OverlayKindDeltaVsMargin || kind == types.OverlayKindIndexVsMargin ||
			kind == types.OverlayKindShareOfRow || kind == types.OverlayKindShareOfCol ||
			kind == types.OverlayKindShareOfTotal || kind == types.OverlayKindZScoreVsMargin {
			spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
		}
		want := overlayhost.CrosstabSpecRefusal(&spec, kind, 1)
		if want == nil {
			t.Fatalf("%s: rule accepted scope nowhere", kind)
		}
		_, _, err := applyOverlays([]types.OverlaySpec{lead, spec}, host)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != want.Code || ce.Message != want.Message || ce.Details["index"] != 1 {
			t.Fatalf("%s: err %v, want %s %q at index 1", kind, err, want.Code, want.Message)
		}
	}
}
