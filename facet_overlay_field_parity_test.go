package pulse

import (
	"context"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFacetOverlay_ParamsFieldRefusalsParity: on a facet host, a
// params.field naming none of the request's fields is
// PULSE_OVERLAY_FIELD_UNKNOWN (as on every other host), and an omitted params.field
// on a multi-field facet is PULSE_OVERLAY_PARAM_MISSING — predict and the
// run agree on code, message and details.
func TestFacetOverlay_ParamsFieldRefusalsParity(t *testing.T) {
	t.Parallel()
	p := intFacetPulse(t, Options{})
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		params string
		want   perrors.Code
	}{
		{"unlisted field", `{"field":"zz"}`, perrors.PULSE_OVERLAY_FIELD_UNKNOWN},
		{"omitted field", ``, perrors.PULSE_OVERLAY_PARAM_MISSING},
	} {
		req := func() *types.FacetRequest {
			r := intFacetReq("c.pulse", "a", nil)
			r.Fields = []string{"a", "b"}
			spec := types.OverlaySpec{
				Name:  "o",
				Kind:  types.OverlayKindIndexVsPop,
				Scope: types.OverlayScopeGroup,
				Ref:   types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "c.pulse"}},
			}
			if tc.params != "" {
				spec.Params = []byte(tc.params)
			}
			r.Overlays = []types.OverlaySpec{spec}
			return r
		}
		env, err := p.PredictFacet(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		_, rerr := p.FacetSchema(ctx, req())
		assertParity(t, tc.name, envFirst(env), rerr, tc.want)
	}
}
