package pulse_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Extension regression params and outputs (U53 E1-S2): a REG_* slot
// carries its model's own `params` object, checked against the
// registration's declared keys at predict and at runtime alike, and the
// result carries the model's declared `outputs`, which no components
// opt-out and no return preset strips.

const regParamsName types.RegressionType = "REG_ACME_SCALED"

// scaledSlope is the buffered slope model reading params.scale: every
// coefficient is multiplied by it and the scale is echoed in outputs.
type scaledSlope struct {
	acc   *slopeAcc
	scale float64
	label string
}

func (m *scaledSlope) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	calls := &regCalls{}
	for i := 0; i < rows.Len(); i++ {
		m.acc.add(rows.At(i), calls)
	}
	out, err := m.acc.output(true)
	if err != nil {
		return out, err
	}
	for i := range out.Coefficients {
		out.Coefficients[i].Estimate *= m.scale
	}
	out.Vcov = nil
	out.Outputs = map[string]any{"scale": m.scale, "label": m.label}
	return out, nil
}

func scaledExtensions() pulse.Extensions {
	factory := func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		p := struct {
			Scale *float64 `json:"scale"`
			Label string   `json:"label"`
		}{}
		if len(spec.Params) > 0 {
			if err := json.Unmarshal(spec.Params, &p); err != nil {
				return nil, perr.NewCodedError(perr.PROCESSING_CONFIG, "params: "+err.Error())
			}
		}
		scale := 1.0
		if p.Scale != nil {
			scale = *p.Scale
		}
		return &scaledSlope{acc: newSlopeAcc(spec), scale: scale, label: p.Label}, nil
	}
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name:        regParamsName,
		Description: "slope scaled by params.scale",
		Factory:     factory,
		Params: []pulse.ParamMeta{
			{Name: "scale", JSONType: "number", Description: "Coefficient multiplier.", Default: 1.0},
			{Name: "label", JSONType: "string", Description: "Echoed in outputs."},
		},
		OutputSchema: []pulse.RegressionOutputMeta{
			{Key: "scale", JSONType: "number", Description: "The scale applied."},
			{Key: "label", JSONType: "string", Description: "The label echoed."},
		},
	}}}
}

func scaledSpec(params string) *types.RegressionSpec {
	s := slopeSpec(regParamsName)
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

// TestExtensions_RegressionParamsReachModelOutputsSurvive: params reach
// the factory through Process (the coefficients move with
// params.scale), the declared outputs come back on the result and on
// the wire, and they survive the per-request and the engine-level
// components opt-out and every return preset.
func TestExtensions_RegressionParamsReachModelOutputsSurvive(t *testing.T) {
	p, cohort := openE2E(t, scaledExtensions())
	base := processE2E(t, p, regressionRequest(cohort, scaledSpec(""))).Regressions[0]
	got := processE2E(t, p, regressionRequest(cohort, scaledSpec(`{"scale":2,"label":"x2"}`))).Regressions[0]
	if base.Coefficients["qty"] == 0 || got.Coefficients["qty"] != 2*base.Coefficients["qty"] {
		t.Fatalf("slope with scale 2 = %v, without = %v: params did not reach the model", got.Coefficients["qty"], base.Coefficients["qty"])
	}
	if got.Outputs["scale"] != 2.0 || got.Outputs["label"] != "x2" {
		t.Fatalf("outputs = %v, want scale 2 and label x2", got.Outputs)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Outputs map[string]any `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Outputs["label"] != "x2" {
		t.Fatalf("wire outputs = %v (%v): %s", wire.Outputs, err, raw)
	}

	hasOutputs := func(t *testing.T, resp *types.Response) {
		t.Helper()
		if len(resp.Regressions) != 1 || resp.Regressions[0] == nil || resp.Regressions[0].Outputs["label"] != "x2" {
			t.Fatalf("outputs stripped: %+v", resp.Regressions)
		}
	}
	off := true
	t.Run("request disable_components", func(t *testing.T) {
		req := regressionRequest(cohort, scaledSpec(`{"scale":2,"label":"x2"}`))
		req.DisableComponents = &off
		resp := processE2E(t, p, req)
		if resp.Components != nil {
			t.Fatalf("components not disabled")
		}
		hasOutputs(t, resp)
	})
	t.Run("engine DisableComponents", func(t *testing.T) {
		fsys := afero.NewMemMapFs()
		writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
		q, err := pulse.New(pulse.Options{FS: fsys, Extensions: scaledExtensions(), DisableComponents: true})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		resp := processE2E(t, q, regressionRequest(cohort, scaledSpec(`{"scale":2,"label":"x2"}`)))
		if resp.Components != nil {
			t.Fatalf("components not disabled")
		}
		hasOutputs(t, resp)
	})
	for _, preset := range types.AllReturnPresets() {
		t.Run("return "+string(preset), func(t *testing.T) {
			req := regressionRequest(cohort, scaledSpec(`{"scale":2,"label":"x2"}`))
			req.Return = &types.Return{Preset: preset}
			hasOutputs(t, processE2E(t, p, req))
		})
	}
}

// TestExtensions_RegressionParamsRefusedPredictMatchesRuntime: an
// undeclared params key on an extension regression, and any params key
// on a built-in regression (an empty allowlist), is refused
// PULSE_REQUEST_UNKNOWN_FIELD at regressions[i].params.<key> with the
// same code, path and message at predict and at runtime.
func TestExtensions_RegressionParamsRefusedPredictMatchesRuntime(t *testing.T) {
	p, cohort := openE2E(t, scaledExtensions())
	ctx := context.Background()
	builtin := func(rt types.RegressionType, params string) *types.RegressionSpec {
		s := slopeSpec(rt)
		if rt == types.REG_GLM {
			s.Family = "poisson"
		}
		s.Params = json.RawMessage(params)
		return s
	}
	for _, tc := range []struct {
		name    string
		specs   []*types.RegressionSpec
		path    string
		suggest string
	}{
		{"extension unknown key", []*types.RegressionSpec{scaledSpec(`{"scael":2}`)}, "regressions[0].params.scael", "scale"},
		{"second slot", []*types.RegressionSpec{scaledSpec(`{"scale":2}`), scaledSpec(`{"labl":"a"}`)}, "regressions[1].params.labl", "label"},
		{"REG_OLS", []*types.RegressionSpec{builtin(types.REG_OLS, `{"alpha":1}`)}, "regressions[0].params.alpha", ""},
		{"REG_GLM", []*types.RegressionSpec{builtin(types.REG_GLM, `{"family":"poisson"}`)}, "regressions[0].params.family", ""},
		{"REG_BAYES_LINEAR", []*types.RegressionSpec{builtin(types.REG_BAYES_LINEAR, `{"prior":"nig"}`)}, "regressions[0].params.prior", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(ctx, regressionRequest(cohort, tc.specs...))
			ce := unknownParam(t, err, tc.path, tc.suggest)
			res, perr2 := p.Predict(ctx, regressionRequest(cohort, tc.specs...))
			if perr2 != nil || res.Valid || len(res.Errors) == 0 {
				t.Fatalf("Predict = %+v / %v, want the refusal", res, perr2)
			}
			e := res.Errors[0]
			if e.Code != string(ce.Code) || e.Message != ce.Message || e.Details["path"] != tc.path {
				t.Fatalf("predict %s %q (path %v), runtime %s %q (path %s)", e.Code, e.Message, e.Details["path"], ce.Code, ce.Message, tc.path)
			}
		})
	}
	t.Run("declared keys pass both sides", func(t *testing.T) {
		req := regressionRequest(cohort, scaledSpec(`{"scale":3,"label":"ok"}`))
		if res, err := p.Predict(ctx, req); err != nil || !res.Valid {
			t.Fatalf("Predict = %+v / %v, want valid", res, err)
		}
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatalf("Process: %v", err)
		}
	})
}

// TestExtensions_RegressionManifestCarriesParamsAndOutputSchema: the
// extension regression's manifest row, in the built-in RegressionMeta
// shape, lists the shared slot keys (marked slot) then the model's own
// params and the declared output schema.
func TestExtensions_RegressionManifestCarriesParamsAndOutputSchema(t *testing.T) {
	p, _ := openE2E(t, scaledExtensions())
	m := p.Manifest(context.Background())
	if len(m.Extensions.Regressions) != 1 {
		t.Fatalf("extensions.regressions = %+v, want one row", m.Extensions.Regressions)
	}
	row := m.Extensions.Regressions[0]
	if row.Name != string(regParamsName) || row.Streamable || row.Description == "" || len(row.AcceptsTypes) == 0 {
		t.Fatalf("row = %+v", row)
	}
	var slot, own []string
	for _, prm := range row.Params {
		if prm.Slot {
			slot = append(slot, prm.Name)
		} else {
			own = append(own, prm.Name+":"+prm.Type)
		}
	}
	if !slices.Equal(slot, []string{"target", "predictors"}) || !slices.Equal(own, []string{"scale:float", "label:string"}) {
		t.Fatalf("params slot=%v own=%v", slot, own)
	}
	if len(row.OutputSchema) != 2 || row.OutputSchema[0].Key != "scale" || row.OutputSchema[0].JSONType != "number" ||
		row.OutputSchema[1].Key != "label" || row.OutputSchema[1].Description != "The label echoed." {
		t.Fatalf("output_schema = %+v", row.OutputSchema)
	}
	for _, r := range m.Regressions {
		if r.Name == string(regParamsName) {
			t.Fatalf("extension listed among the built-in regressions")
		}
	}
}
