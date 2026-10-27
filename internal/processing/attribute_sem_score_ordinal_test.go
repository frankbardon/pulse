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

// TestSEMScore_OrdinalInlineRefusals: on an inline ordinal fit Bartlett
// is bad_params (param method), and a refused two-step fit (a constant
// ordered member) nulls every score with the fit's
// PULSE_MATRIX_SINGULAR warning (reason constant).
func TestSEMScore_OrdinalInlineRefusals(t *testing.T) {
	t.Parallel()

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
