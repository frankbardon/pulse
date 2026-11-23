package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glm_refit_leverage_test.go pins ATTR_REG_LEVERAGE's GLM refit
// (params.family / params.link, regression-depth-2 E1-S2, #677): the
// slot refits a REG_GLM on its own target and predictors and reads the
// final IRLS working weights' hat, R 4.6.1 hatvalues(glm) on the
// glm_influence.json rows (gen_glm_influence.R).

func glmRefitLeverageAttr(c glmInfluenceCase) *types.Attribute {
	a := &types.Attribute{Type: types.ATTR_REG_LEVERAGE, Label: "hat", Target: "y", Predictors: []string{"x1", "x2"}}
	params, _ := json.Marshal(map[string]string{"family": c.Family, "link": c.Link})
	a.Params = params
	return a
}

// TestGLMRefitLeverage_MatchesR: on the binomial-logit, poisson-log and
// gamma-inverse cases every row's refit leverage agrees with R's
// hatvalues to 1e-7 relative (the refit runs the default IRLS
// tolerance, R the fit polished to 1e-14), and is bit-equal to the
// leverage of a bound REG_GLM fit of the same rows at the same default
// tolerance (one design kernel); predict judges the slot valid.
func TestGLMRefitLeverage_MatchesR(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	n := 0
	for _, c := range loadGLMInfluence(t) {
		if c.Family == "gaussian" {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := glmInfluencePulse(t, c)
			req := fitRefSlot("glm.pulse", "id", glmRefitLeverageAttr(c))
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			pr, err := p.Predict(ctx, fitRefSlot("glm.pulse", "id", glmRefitLeverageAttr(c)))
			if err != nil {
				t.Fatal(err)
			}
			if !pr.Valid || len(pr.Errors) > 0 {
				t.Errorf("predict valid %v errors %+v", pr.Valid, pr.Errors)
			}
			bound := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}
			out, err := p.Compose(ctx, &ComposedRequest{Fits: glmInfluenceFits(bound),
				Requests: []*Request{fitRefSlot("glm.pulse", "id", fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`))}})
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Data) != len(c.Rows) || len(out.Responses[0].Data) != len(c.Rows) {
				t.Fatalf("%d / %d rows, want %d", len(resp.Data), len(out.Responses[0].Data), len(c.Rows))
			}
			sum := 0.0
			for i, row := range resp.Data {
				h, ok := row["hat"].(float64)
				if !ok {
					t.Fatalf("row %d hat = %v", i, row["hat"])
				}
				sum += h
				if e := relErr(h, c.Hat[i]); e > 1e-7 {
					t.Errorf("row %d hat = %.17g, R %.17g (rel %.3g)", i, h, c.Hat[i], e)
				}
				if b := out.Responses[0].Data[i]["hat"]; b != h {
					t.Errorf("row %d refit hat %v ≠ bound-fit hat %v", i, h, b)
				}
			}
			if math.Abs(sum-3) > 1e-6 {
				t.Errorf("Σh = %v, want p′ = 3", sum)
			}
		})
	}
	if n != 3 {
		t.Fatalf("%d GLM cases, want 3", n)
	}
}

// TestGLMRefitLeverage_FrequencyWeightIsExpansion: under a frequency
// weight a record's GLM refit leverage is the sum of its w copies'
// leverages in the rep()-expanded unweighted refit (the hat is w·the
// one-copy hat), to the IRLS tolerance; a probability weight's is the
// frequency weight's.
func TestGLMRefitLeverage_FrequencyWeightIsExpansion(t *testing.T) {
	t.Parallel()

	var c glmInfluenceCase
	for _, k := range loadGLMInfluence(t) {
		if k.Family == "poisson" {
			c = k
		}
	}
	ctx := context.Background()
	rows := make([][]any, 0, 2*len(c.Rows))
	expanded := make([][]any, 0, 2*len(c.Rows))
	id := 0
	for i, r := range c.Rows {
		w := float64(1 + i%2)
		rows = append(rows, []any{float64(i), r[0], r[1], r[2], w})
		for k := 0; k < int(w); k++ {
			expanded = append(expanded, []any{float64(id), r[0], r[1], r[2], 1.0})
			id++
		}
	}
	cols := []string{"id", "x1", "x2", "y", "w"}
	weighted := strdPulse(t, "w.pulse", cols, rows, nil)
	plain := strdPulse(t, "e.pulse", cols, expanded, nil)
	attr := glmRefitLeverageAttr(c)
	req := fitRefSlot("w.pulse", "id", attr)
	req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	for _, a := range req.Aggregations {
		a.Weight = types.NullSlotWeight()
	}
	wr, err := weighted.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	er, err := plain.Process(ctx, fitRefSlot("e.pulse", "id", glmRefitLeverageAttr(c)))
	if err != nil {
		t.Fatal(err)
	}
	// A probability weight fits on w* = c·w: the same β and, the hat
	// being invariant to the weights' scale, the same leverage.
	preq := fitRefSlot("w.pulse", "id", glmRefitLeverageAttr(c))
	preq.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	for _, a := range preq.Aggregations {
		a.Weight = types.NullSlotWeight()
	}
	pres, err := weighted.Process(ctx, preq)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range pres.Data {
		if e := relErr(row["hat"].(float64), wr.Data[i]["hat"].(float64)); e > 1e-9 {
			t.Errorf("row %d probability hat %v, frequency hat %v", i, row["hat"], wr.Data[i]["hat"])
		}
	}
	id = 0
	for i, row := range wr.Data {
		w := 1 + i%2
		want := 0.0
		for k := 0; k < w; k++ {
			want += er.Data[id]["hat"].(float64)
			id++
		}
		if e := relErr(row["hat"].(float64), want); e > 1e-6 {
			t.Errorf("row %d weighted hat %v, expanded copies sum %v (rel %.3g)", i, row["hat"], want, e)
		}
	}
}

// TestGLMRefitLeverage_RefusalsMatchPredict: params.family / params.link
// refusals are the same code and message from Process and Predict — an
// unknown family (REG_GLM's PROCESSING_REGRESSION_INVALID_FAMILY), a link
// outside the family's (PROCESSING_REGRESSION_INVALID_LINK), a link
// without a family and a negative_binomial family beside a penalty
// (PROCESSING_CONFIG, as the fit itself),
// params.family beside params.fit (PULSE_MODEL_SCORE_INVALID
// slot_fields) — and a family on another refit slot is an unknown key
// (PULSE_REQUEST_UNKNOWN_FIELD).
func TestGLMRefitLeverage_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	c := loadGLMInfluence(t)[2]
	p := glmInfluencePulse(t, c)
	ctx := context.Background()
	lev := func(params string, edit func(*types.Attribute)) *types.Attribute {
		a := &types.Attribute{Type: types.ATTR_REG_LEVERAGE, Label: "a", Target: "y", Predictors: []string{"x1", "x2"}, Params: json.RawMessage(params)}
		if edit != nil {
			edit(a)
		}
		return a
	}
	for _, tc := range []struct {
		name string
		attr *types.Attribute
		code errors.Code
	}{
		{"unknown family", lev(`{"family":"tweedie"}`, nil), errors.PROCESSING_REGRESSION_INVALID_FAMILY},
		{"foreign link", lev(`{"family":"poisson","link":"logit"}`, nil), errors.PROCESSING_REGRESSION_INVALID_LINK},
		{"link alone", lev(`{"link":"log"}`, nil), errors.PROCESSING_CONFIG},
		// Any other penalty is taken (E1-S4, penalized_leverage_bound_test.go).
		{"penalized negative_binomial", lev(`{"family":"negative_binomial"}`, func(a *types.Attribute) { a.Penalty, a.Alpha = "l2", 0.1 }), errors.PROCESSING_CONFIG},
		{"family on fitted", &types.Attribute{Type: types.ATTR_REG_FITTED, Label: "a", Target: "y", Predictors: []string{"x1", "x2"}, Params: json.RawMessage(`{"family":"poisson"}`)}, errors.PULSE_REQUEST_UNKNOWN_FIELD},
		{"family with fit", fitRefAttr(types.ATTR_REG_LEVERAGE, "a", `{"fit":"m","family":"poisson"}`), errors.PULSE_MODEL_SCORE_INVALID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(ctx, fitRefSlot("glm.pulse", "id", tc.attr))
			ce := codedOf(t, err)
			if ce.Code != tc.code {
				t.Fatalf("Process err = %v, want %s", err, tc.code)
			}
			pr, perr := p.Predict(ctx, fitRefSlot("glm.pulse", "id", tc.attr))
			if perr != nil {
				t.Fatal(perr)
			}
			matched := false
			for _, e := range pr.Errors {
				matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if pr.Valid || !matched {
				t.Errorf("predict (valid %v) errors %+v do not carry the runtime refusal %s %q", pr.Valid, pr.Errors, ce.Code, ce.Message)
			}
		})
	}
}
