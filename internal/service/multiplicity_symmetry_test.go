package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// TestMultiplicityFold_CrosstabSymmetry: OVERLAY_SYMMETRY's one p-value
// (summary.p_value, the pSiteSummaryPValue site) joins a `request`
// family with OVERLAY_CHISQ_MATRIX's and is adjusted exactly as the core
// adjusts the two raw p-values; a `layer` family of one leaves p_adjusted
// = p; the raw p never moves; fused and buffered arms agree.
func TestMultiplicityFold_CrosstabSymmetry(t *testing.T) {
	fused, buffered := overlayFoldService(t)
	ctx := context.Background()
	mk := func(m *types.Multiplicity) *types.Request {
		r := overlayCrosstabRequest(false)
		r.Overlays = []types.OverlaySpec{
			{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix, Multiplicity: m},
			{Name: "s", Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix, Multiplicity: m},
		}
		return r
	}
	base, err := buffered.Process(ctx, mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	pm, ps := *base.Overlays[0].Summary.PValue, *base.Overlays[1].Summary.PValue
	if base.Overlays[1].Summary.PAdjusted != nil {
		t.Fatal("baseline carries p_adjusted without a multiplicity block")
	}
	pool, _ := multiplicity.Adjust(multiplicity.MethodHolm, []float64{pm, ps})
	for _, c := range []struct {
		family types.MultiplicityFamily
		want   float64
	}{
		{types.MultiplicityFamilyRequest, pool[1]},
		{types.MultiplicityFamilyLayer, ps},
	} {
		t.Run(string(c.family), func(t *testing.T) {
			m := &types.Multiplicity{Method: types.MultiplicityMethodHolm, Family: c.family}
			var got [2]*types.Response
			for i, svc := range []*Service{buffered, fused} {
				resp, err := svc.Process(ctx, mk(m))
				if err != nil {
					t.Fatal(err)
				}
				got[i] = resp
			}
			s := got[0].Overlays[1].Summary
			if s.PAdjusted == nil || !sameFloat(*s.PAdjusted, c.want) || *s.PValue != ps {
				t.Fatalf("summary %+v, want p_adjusted %v beside the raw p %v", s, c.want, ps)
			}
			if a, b := mustMarshal(t, got[0].Overlays), mustMarshal(t, got[1].Overlays); string(a) != string(b) {
				t.Errorf("fused and buffered layers differ:\n%s\n%s", b, a)
			}
		})
	}
}
