package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// penalized_leverage_bound_test.go pins regression-depth-2 E1-S4
// (#677): a penalized REG_OLS / REG_GLM fit carries its regularized
// inverse in memory and in a saved model file — never on the wire
// (RegressionResult.PenalizedInverse) — and ATTR_REG_LEVERAGE over
// it (params.fit) is the penalized refit's hat, predict judging it as
// the runtime runs it; a model file without the carriage is refused
// alike; the GLM refit takes a penalty; and an instance hiding REG_GLM
// refuses the refit's params.family / params.link as unknown keys.

// penLevCohort writes pen.pulse — id, x1, x2, y and a frequency weight
// w = 1 + i%3 over case c's rows — to a fresh instance and returns it
// with its filesystem.
func penLevCohort(t *testing.T, c glmInfluenceCase, opts Options) (*Pulse, afero.Fs) {
	t.Helper()
	p, fsys := matPulse(t, opts)
	var schema encoding.Schema
	for _, name := range []string{"id", "x1", "x2", "y", "w"} {
		schema.Fields = append(schema.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "penalized leverage column " + name})
	}
	b, err := p.NewCohortBuilder(context.Background(), "pen.pulse", schema, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range c.Rows {
		if err := b.Append(CohortRow{float64(i), r[0], r[1], r[2], float64(1 + i%3)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func penLevCase(t *testing.T, name string) glmInfluenceCase {
	t.Helper()
	for _, c := range loadGLMInfluence(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s case", name)
	return glmInfluenceCase{}
}

// penLevFits is one fit "m" of reg over pen.pulse.
func penLevFits(reg *types.RegressionSpec) []types.FitSpec {
	return []types.FitSpec{{Name: "m", Request: &Request{Cohort: &types.Cohort{Filename: "pen.pulse"}, Regressions: []*types.RegressionSpec{reg}}}}
}

// penLevKnobs sets penalty / alpha / l1_ratio on a spec or a refit slot.
func penLevKnobs(penalty string) (alpha, l1 float64) {
	switch penalty {
	case "l2":
		return 0.3, 0
	case "l1":
		return 0.02, 0
	}
	return 0.05, 0.5
}

// assertPredictMatches requires predict to carry the runtime refusal ce
// among its errors (same code and message) and to be invalid.
func assertPredictMatches(t *testing.T, errs []*descriptor.EnvelopeEntry, valid bool, ce *errors.CodedError) {
	t.Helper()
	matched := false
	for _, e := range errs {
		matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
	}
	if valid || !matched {
		t.Errorf("predict (valid %v) errors %+v do not carry the runtime refusal %s %q", valid, errs, ce.Code, ce.Message)
	}
}

// TestPenalizedBoundLeverage_EqualsRefit: ATTR_REG_LEVERAGE over a bound
// penalized REG_OLS fit — ridge, lasso, elastic net, unweighted and
// frequency-weighted — equals the same slot's penalized refit for one
// copy of the row (the refit's h / w) to 1e-12 relative; the fit carries
// penalized_inverse and no vcov is needed; PredictCompose judges the
// slot valid, as Compose runs it.
func TestPenalizedBoundLeverage_EqualsRefit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := penLevCase(t, "ols")
	p, _ := penLevCohort(t, c, Options{})
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	for _, pen := range []string{"l2", "l1", "elasticnet"} {
		for _, weighted := range []bool{false, true} {
			name := pen
			if weighted {
				name += "/frequency"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				alpha, l1 := penLevKnobs(pen)
				reg := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Penalty: pen, Alpha: alpha, L1Ratio: l1}
				if weighted {
					reg.Weight = types.SlotWeightOf(*freq)
				}
				composed := func() *ComposedRequest {
					return &ComposedRequest{Fits: penLevFits(reg), Requests: []*Request{fitRefSlot("pen.pulse", "id", fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`))}}
				}
				out, err := p.Compose(ctx, composed())
				if err != nil {
					t.Fatal(err)
				}
				if f := out.Fits["m"]; f == nil || f.RegressionResult == nil || f.PenalizedInverse == nil || f.Vcov != nil {
					t.Fatalf("fit %+v: want penalized_inverse and no vcov", f)
				}
				// The carriage is in memory only: the wire (the bound fit's
				// entry and a plain penalized fit) never shows it.
				plain, err := p.Process(ctx, &Request{Cohort: &types.Cohort{Filename: "pen.pulse"}, Regressions: []*types.RegressionSpec{reg}})
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range []any{out, plain} {
					if b, _ := json.Marshal(v); strings.Contains(string(b), "penalized_inverse") {
						t.Errorf("penalized_inverse reached the wire: %s", b)
					}
				}
				env, err := p.PredictCompose(ctx, composed())
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) > 0 {
					t.Errorf("PredictCompose errors %+v, want the runtime's acceptance", env.Errors)
				}
				refit := influenceAttr(types.ATTR_REG_LEVERAGE, "hat", "")
				refit.Penalty, refit.Alpha, refit.L1Ratio = pen, alpha, l1
				req := fitRefSlot("pen.pulse", "id", refit)
				if weighted {
					req.Weight = freq
					for _, a := range req.Aggregations {
						a.Weight = types.NullSlotWeight()
					}
				}
				ref, err := p.Process(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if len(ref.Data) != len(c.Rows) || len(out.Responses[0].Data) != len(c.Rows) {
					t.Fatalf("%d / %d rows, want %d", len(ref.Data), len(out.Responses[0].Data), len(c.Rows))
				}
				for i, row := range out.Responses[0].Data {
					w := 1.0
					if weighted {
						w = float64(1 + i%3)
					}
					hb, hr := row["hat"].(float64), ref.Data[i]["hat"].(float64)/w
					if e := relErr(hb, hr); e > 1e-12 {
						t.Errorf("row %d: bound h %.17g, refit h/w %.17g (rel %.3g)", i, hb, hr, e)
					}
				}
			})
		}
	}
}

// TestPenalizedBoundLeverage_GLMRefitBitEqual: a penalized REG_GLM fit
// (binomial logit and poisson log; l2, l1, elasticnet) bound by
// ATTR_REG_LEVERAGE params.fit and the slot's own GLM refit
// (params.family + the same penalty — no longer refused) read one design
// kernel over one regularized inverse: bit-equal on every row; predict
// judges both valid.
func TestPenalizedBoundLeverage_GLMRefitBitEqual(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, name := range []string{"binomial_logit", "poisson_log"} {
		c := penLevCase(t, name)
		p, _ := penLevCohort(t, c, Options{})
		for _, pen := range []string{"l2", "l1", "elasticnet"} {
			t.Run(name+"/"+pen, func(t *testing.T) {
				t.Parallel()
				alpha, l1 := penLevKnobs(pen)
				reg := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"}, Penalty: pen, Alpha: alpha, L1Ratio: l1}
				composed := func() *ComposedRequest {
					return &ComposedRequest{Fits: penLevFits(reg), Requests: []*Request{fitRefSlot("pen.pulse", "id", fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`))}}
				}
				out, err := p.Compose(ctx, composed())
				if err != nil {
					t.Fatal(err)
				}
				env, err := p.PredictCompose(ctx, composed())
				if err != nil {
					t.Fatal(err)
				}
				if len(env.Errors) > 0 {
					t.Errorf("PredictCompose errors %+v", env.Errors)
				}
				refit := influenceAttr(types.ATTR_REG_LEVERAGE, "hat", "")
				params, _ := json.Marshal(map[string]string{"family": c.Family, "link": c.Link})
				refit.Params, refit.Penalty, refit.Alpha, refit.L1Ratio = params, pen, alpha, l1
				ref, err := p.Process(ctx, fitRefSlot("pen.pulse", "id", refit))
				if err != nil {
					t.Fatal(err)
				}
				pr, err := p.Predict(ctx, fitRefSlot("pen.pulse", "id", refit))
				if err != nil {
					t.Fatal(err)
				}
				if !pr.Valid || len(pr.Errors) > 0 {
					t.Errorf("predict valid %v errors %+v on the penalized GLM refit", pr.Valid, pr.Errors)
				}
				for i, row := range out.Responses[0].Data {
					if b, r := row["hat"], ref.Data[i]["hat"]; b != r {
						t.Errorf("row %d: bound h %v ≠ refit h %v", i, b, r)
					}
				}
			})
		}
	}
}

// TestPenalizedBoundLeverage_ModelFile: a saved penalized fit lists
// feature penalized_inverse and its loaded model scores the leverage
// bit-equal to the in-request fit; the same file stripped of the section
// (what an earlier build wrote) is refused PULSE_MODEL_SCORE_INVALID
// requires penalized_inverse by Process and Predict alike, with a fixup.
func TestPenalizedBoundLeverage_ModelFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := penLevCase(t, "ols")
	p, fsys := penLevCohort(t, c, Options{})
	reg := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Penalty: "l1", Alpha: 0.02}
	fits := penLevFits(reg)
	fits[0].Save = "m.pulse-model.json"
	slot := func(path string) *Request {
		r := fitRefSlot("pen.pulse", "id", fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`))
		if path != "" {
			r.Models = []types.ModelRef{{Name: "m", Path: path}}
		}
		return r
	}
	in, err := p.Compose(ctx, &ComposedRequest{Fits: fits, Requests: []*Request{slot("")}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := afero.ReadFile(fsys, "m.pulse-model.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"penalized_inverse"`) {
		t.Fatalf("saved file lists no penalized_inverse:\n%s", raw)
	}
	out, err := p.Process(ctx, slot("m.pulse-model.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range out.Data {
		if g, w := row["hat"], in.Responses[0].Data[i]["hat"]; g != w {
			t.Errorf("row %d: loaded h %v, in-request %v", i, g, w)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "penalized_inverse")
	feats, _ := doc["features"].([]any)
	feats = slices.DeleteFunc(feats, func(f any) bool { return f == "penalized_inverse" })
	if len(feats) == 0 {
		delete(doc, "features")
	} else {
		doc["features"] = feats
	}
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "old.pulse-model.json", old, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = p.Process(ctx, slot("old.pulse-model.json"))
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Details["requires"] != "penalized_inverse" || !strings.Contains(ce.Message, "earlier build") {
		t.Fatalf("Process err = %v (details %v), want PULSE_MODEL_SCORE_INVALID requires penalized_inverse", err, ce.Details)
	}
	if md, ok := errors.MetadataFor(ce.Code); !ok || len(md.Fixups) == 0 {
		t.Error("the refusal's code carries no fixup")
	}
	pr, err := p.Predict(ctx, slot("old.pulse-model.json"))
	if err != nil {
		t.Fatal(err)
	}
	assertPredictMatches(t, pr.Errors, pr.Valid, ce)
}

// TestPenalizedBoundLeverage_InfluenceStillRefused: over a penalized
// bound fit the influence measures and the scaled residual kinds read a
// residual variance a penalized fit does not define — refused
// requires unpenalized by Compose and PredictCompose alike, while the
// leverage beside them runs.
func TestPenalizedBoundLeverage_InfluenceStillRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := penLevCase(t, "ols")
	p, _ := penLevCohort(t, c, Options{})
	reg := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Penalty: "l2", Alpha: 0.3, Vcov: true}
	for _, a := range []*types.Attribute{
		fitRefAttr(types.ATTR_REG_INFLUENCE, "d", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "s", `{"fit":"m","kind":"studentized"}`),
	} {
		t.Run(string(a.Type), func(t *testing.T) {
			req := func() *ComposedRequest {
				return &ComposedRequest{Fits: penLevFits(reg), Requests: []*Request{fitRefSlot("pen.pulse", "id", a)}}
			}
			_, err := p.Compose(ctx, req())
			ce := codedOf(t, err)
			if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Details["requires"] != "unpenalized" {
				t.Fatalf("Compose err = %v (details %v), want requires unpenalized", err, ce.Details)
			}
			env, err := p.PredictCompose(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			matched := false
			for _, e := range env.Errors {
				matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if !matched {
				t.Errorf("PredictCompose errors %+v lack the runtime refusal %q", env.Errors, ce.Message)
			}
		})
	}
}

// TestHiddenGLM_RefitFamilyIsUnknownKey: on an instance whose feature
// profile hides REG_GLM (keeping ATTR_REG_LEVERAGE and REG_OLS), the
// leverage refit's params.family / params.link are no keys at all —
// PULSE_REQUEST_UNKNOWN_FIELD from Process and Predict alike, the same
// code and message, the message naming no REG_GLM, details.valid_keys
// without them — and the instance's manifest declares neither param;
// the unprofiled control runs the GLM refit, and the hiding instance
// still runs the OLS refit.
func TestHiddenGLM_RefitFamilyIsUnknownKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := penLevCase(t, "poisson_log")
	control, fsys := penLevCohort(t, c, Options{})
	feats := []string{"capability:process", string(types.REG_OLS), string(types.ATTR_REG_LEVERAGE), string(types.GROUP_RANGE), string(types.AGG_MIN)}
	h, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Profile: "no-glm", Features: feats}})
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{`{"family":"poisson"}`, `{"family":"poisson","link":"log"}`, `{"link":"log"}`} {
		t.Run(params, func(t *testing.T) {
			req := func() *Request {
				return fitRefSlot("pen.pulse", "id", influenceAttr(types.ATTR_REG_LEVERAGE, "hat", params))
			}
			if params != `{"link":"log"}` {
				if _, err := control.Process(ctx, req()); err != nil {
					t.Fatalf("unprofiled control: %v", err)
				}
			}
			_, err := h.Process(ctx, req())
			ce := codedOf(t, err)
			if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || strings.Contains(ce.Message, "REG_GLM") || strings.Contains(ce.Message, "GLM") {
				t.Fatalf("hidden REG_GLM: Process err = %v, want PULSE_REQUEST_UNKNOWN_FIELD naming no GLM", err)
			}
			if vk, ok := ce.Details["valid_keys"].([]string); ok && (slices.Contains(vk, "family") || slices.Contains(vk, "link")) {
				t.Errorf("valid_keys %v offer a hidden key", vk)
			}
			pr, perr := h.Predict(ctx, req())
			if perr != nil {
				t.Fatal(perr)
			}
			assertPredictMatches(t, pr.Errors, pr.Valid, ce)
		})
	}
	if _, err := h.Process(ctx, fitRefSlot("pen.pulse", "id", influenceAttr(types.ATTR_REG_LEVERAGE, "hat", ""))); err != nil {
		t.Errorf("hiding instance refuses the OLS refit: %v", err)
	}
	m := h.Manifest(ctx)
	found := false
	for _, a := range m.Components.Attributes {
		if a.Name != string(types.ATTR_REG_LEVERAGE) {
			continue
		}
		found = true
		for _, prm := range a.Params {
			if prm.Name == "family" || prm.Name == "link" {
				t.Errorf("hiding instance's manifest declares ATTR_REG_LEVERAGE params.%s", prm.Name)
			}
		}
	}
	if !found {
		t.Fatal("vacuous: the hiding instance's manifest lists no ATTR_REG_LEVERAGE")
	}
	cm := control.Manifest(ctx)
	declared := 0
	for _, a := range cm.Components.Attributes {
		if a.Name == string(types.ATTR_REG_LEVERAGE) {
			for _, prm := range a.Params {
				if prm.Name == "family" || prm.Name == "link" {
					declared++
				}
			}
		}
	}
	if declared != 2 {
		t.Errorf("control manifest declares %d of family / link, want 2", declared)
	}
}
