//go:build heavy

package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestSEMScore_OrdinalInlineMatchesLavaan runs ATTR_SEM_SCORE over an
// inline WLSMV fit (the nine ratings ordered, recommend a numeric
// outcome) through the processor — streaming two-pass and buffered
// arms, bit-identical — and holds every recorded row's EBM score on
// each latent to lavaan's: one group, and the two waves under
// thresholds, loadings and intercepts (each row scored with its own
// wave's parameters). method "regression" is the EBM score. The
// observed-mediator model (recommend predicted by two factors and
// predicting service and its s3; v3 numeric) scores the same way, its
// mediator a dummy latent, one group and across the waves.
func TestSEMScore_OrdinalInlineMatchesLavaan(t *testing.T) {
	t.Parallel()

	schema, mk := brandSEMFixture(t, nil, nil)
	scalar := map[string]any{"group": "wave", "group_equal": []string{"thresholds", "loadings", "intercepts"}}
	for _, c := range []struct {
		ref, model string
		ordered    []string
		params     map[string]any
	}{
		{"brand_score_ordmx", "brand_ms", brandOrdered, nil},
		{"brand_score_ordmg", "brand_ms", brandOrdered, scalar},
		{"brand_score_ordmed", "brand_ordmed", brandOrderedMed, nil},
		{"brand_score_ordmedmg", "brand_ordmed", brandOrderedMed, scalar},
	} {
		ref := loadSEMScoreRef(t, c.ref)
		for l, lat := range ref.Latents {
			t.Run(c.ref+"/"+lat, func(t *testing.T) {
				params := map[string]any{"model": brandModel(t, c.model), "latent": lat, "estimator": "wlsmv", "ordered": c.ordered}
				if l == 1 {
					params["method"] = "regression"
				}
				for k, v := range c.params {
					params[k] = v
				}
				streamed, _, resp := runFittedArms(t, schema, mk, "score", nil, func() *types.Request {
					return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}}}
				})
				if len(resp.Warnings) != 0 {
					wj, _ := json.Marshal(resp.Warnings)
					t.Fatalf("warnings %s", wj)
				}
				for _, g := range ref.Groups {
					for k, r := range g.Rows {
						if d := math.Abs(streamed[r] - g.Regression[k][l]); !(d <= semOrdinalTol) {
							t.Errorf("row %d (wave %q): %s = %.10g, lavaan %.10g (diff %.2g)", r, g.Label, lat, streamed[r], g.Regression[k][l], d)
						}
					}
				}
			})
		}
	}
}
