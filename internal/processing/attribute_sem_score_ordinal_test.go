package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// semOrdinalTol holds an inline ordinal score to lavaan's mode
// (internal/sem's TestSEMScore_EBMMatchesLavaan: ~2e-6 at worst).
const semOrdinalTol = 1e-5

var brandOrdered = []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}

// brandOrderedMed are the observed-mediator fixtures' ordered ratings
// (brand_score_ordmed*: v3 numeric).
var brandOrderedMed = []string{"q1", "q2", "q3", "v1", "v2", "s1", "s2", "s3"}

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

// TestSEMScore_OrdinalInlineRefusals: on an inline ordinal fit Bartlett
// is bad_params (param method), and a refused two-step fit (a constant
// ordered member) nulls every score with the fit's
// PULSE_MATRIX_SINGULAR warning (reason constant).
func TestSEMScore_OrdinalInlineRefusals(t *testing.T) {
	schema, mk := brandSEMFixture(t, nil, nil)
	for _, c := range []struct {
		params map[string]any
		param  string
	}{
		{map[string]any{"model": brandModel(t, "brand_ms"), "latent": "service", "method": "bartlett"}, "method"},
	} {
		c.params["estimator"], c.params["ordered"] = "wlsmv", brandOrdered
		fields := brandMembers
		p := NewProcessor(schema)
		_, err := p.Process(t.Context(), &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: fields, Params: semScoreParamsJSON(t, c.params)}}}, NewSliceIterator(mk()))
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != c.param {
			t.Errorf("%s: %v, want SERVICE_VALIDATION bad_params (%s)", c.param, err, c.param)
		}
	}
	constant := func() []*Record {
		recs := mk()
		for _, r := range recs {
			r.Set("q2", 3)
		}
		return recs
	}
	params := map[string]any{"model": brandModel(t, "brand_ms"), "latent": "service", "estimator": "wlsmv", "ordered": brandOrdered}
	streamed, _, resp := runFittedArms(t, schema, constant, "score", nil, func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "score", Fields: brandMembers, Params: semScoreParamsJSON(t, params)}}}
	})
	for r, v := range streamed {
		if !math.IsNaN(v) {
			t.Fatalf("row %d scored %v under a refused fit", r, v)
		}
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_SINGULAR) || resp.Warnings[0].Details["reason"] != "constant" ||
		resp.Warnings[0].Details["variable"] != "q2" || resp.Warnings[0].Details["n"] != int64(1200) {
		wj, _ := json.Marshal(resp.Warnings)
		t.Fatalf("warnings %s", wj)
	}
}
