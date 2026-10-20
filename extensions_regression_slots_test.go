package pulse_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Extension regressions declare or refuse every shared RegressionSpec
// slot (U53 E1-S4): an undeclared slot set on the spec is
// PROCESSING_CONFIG {regression, slot} at predict and at runtime alike,
// and a declared one reaches the model on its spec.

// regSlotValue is a non-zero value for the RegressionSpec field whose
// JSON key is key — valid where the value is judged before the
// extension slot rule (save's path, a field name), else by kind.
func regSlotValue(t *testing.T, key string, ft reflect.Type) reflect.Value {
	t.Helper()
	overrides := map[string]any{
		"save":         "fits/m.pulse-model.json",
		"resample":     "bootstrap",
		"selection":    "forward",
		"criterion":    "aic",
		"penalty":      "l2",
		"family":       "binomial",
		"link":         "logit",
		"prior":        "nig",
		"jackknife_by": "day",
		"covariance":   &types.RegressionCovariance{Type: types.CovarianceHC1},
		"weight":       types.SlotWeightField("qty"),
		"multiplicity": &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm},
		"params":       json.RawMessage(`{"k":1}`),
		"prior_mu":     []float64{0, 0},
		// prior_precision: the per-coefficient vector form (the scalar
		// form governs the same slot).
		"prior_precision": types.PrecisionVector([]float64{0, 1}),
		"unpenalized":     []string{"x"},
		"bounds":          &types.RegressionBounds{Nonnegative: []string{"x"}},
	}
	if v, ok := overrides[key]; ok {
		return reflect.ValueOf(v)
	}
	switch ft.Kind() {
	case reflect.String:
		return reflect.ValueOf("x").Convert(ft)
	case reflect.Bool:
		return reflect.ValueOf(true)
	case reflect.Int, reflect.Int64:
		return reflect.ValueOf(3).Convert(ft)
	case reflect.Float64:
		return reflect.ValueOf(0.5)
	}
	t.Fatalf("RegressionSpec.%s (%s): no test value; extend regSlotValue", key, ft)
	return reflect.Value{}
}

// regressionSpecKeys returns each RegressionSpec field's JSON key and
// its Go field index.
func regressionSpecKeys() map[string]int {
	out := map[string]int{}
	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		out[key] = i
	}
	return out
}

// TestExtensions_RegressionSharedSlotsRefused is the every-slot gate:
// every types.RegressionSpec field is classified for extension models
// (descx.RegressionFieldClassOf), and every field a declared slot
// governs, set alone on a registration that declares nothing, is
// refused with PROCESSING_CONFIG {regression, slot[, key], name} — the
// runtime's error and predict's first error identical. A new
// RegressionSpec field fails here until it has a declare-or-refuse
// rule.
func TestExtensions_RegressionSharedSlotsRefused(t *testing.T) {
	calls := &regCalls{}
	p, cohort := openE2E(t, regressionExtensions(calls))
	for key, idx := range regressionSpecKeys() {
		class, slot, ok := descx.RegressionFieldClassOf(key)
		if !ok {
			t.Errorf("RegressionSpec key %q has no declare-or-refuse rule for extension models (internal/descriptor/extensions_regression_slots.go)", key)
			continue
		}
		if class != descx.RegressionFieldDeclared && class != descx.RegressionFieldWeightClass {
			continue
		}
		t.Run(key, func(t *testing.T) {
			spec := slopeSpec(regStreamName)
			f := reflect.ValueOf(spec).Elem().Field(idx)
			f.Set(regSlotValue(t, key, f.Type()))
			req := regressionRequest(cohort, spec)
			_, err := p.Process(context.Background(), req)
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) == 0 {
				t.Fatalf("predict accepted %s", key)
			}
			if class == descx.RegressionFieldWeightClass {
				requireCodedError(t, err, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
				if pr.Errors[0].Code != string(perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE) {
					t.Errorf("predict = %+v, want PULSE_EXTENSION_NOT_WEIGHT_AWARE", pr.Errors[0])
				}
				return
			}
			ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
			want := map[string]any{"regression": string(regStreamName), "slot": slot, "name": "fit"}
			if key != slot {
				want["key"] = key
			}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details = %v, want %v", ce.Details, want)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict first error = %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}
}

// specRecorder is a streaming slope model that records the spec its
// factory was handed.
func specRecorder(seen **types.RegressionSpec) extend.RegressionFactory {
	return func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
		*seen = spec
		return &streamSlope{acc: newSlopeAcc(spec), calls: &regCalls{}}, nil
	}
}

const regDeclaredName types.RegressionType = "REG_ACME_SLOPE_DECL"

// TestExtensions_RegressionDeclaredSlotsReachModel: a registration
// declaring Penalty and CovarianceTypes gets penalty / alpha / l1_ratio
// and a listed covariance type on the spec its factory sees, predict
// accepting the same request; an unlisted type is refused under slot
// covariance and a malformed declared block by the shared shape rule —
// each identically at predict and runtime. The manifest row lists the
// declared slots and no others.
func TestExtensions_RegressionDeclaredSlotsReachModel(t *testing.T) {
	var seen *types.RegressionSpec
	ext := pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name: regDeclaredName, Description: "declares penalty and covariance", Factory: specRecorder(&seen),
		Streamable: true, Penalty: true,
		CovarianceTypes: []types.CovarianceType{types.CovarianceClassical, types.CovarianceHC1, types.CovarianceCluster},
		OutputSchema:    slopeOutputs,
	}}}
	p, cohort := openE2E(t, ext)

	spec := slopeSpec(regDeclaredName)
	spec.Penalty, spec.Alpha, spec.L1Ratio = "ridge-ish", 0.25, 0.5
	cov := slopeSpec(regDeclaredName)
	cov.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
	for _, s := range []*types.RegressionSpec{spec, cov} {
		seen = nil
		req := regressionRequest(cohort, s)
		processE2E(t, p, req)
		if seen == nil || seen.Penalty != s.Penalty || seen.Alpha != s.Alpha || seen.L1Ratio != s.L1Ratio ||
			!reflect.DeepEqual(seen.Covariance, s.Covariance) {
			t.Fatalf("model saw %+v, want the declared slots of %+v", seen, s)
		}
		pr, err := p.Predict(context.Background(), req)
		if err != nil || len(pr.Errors) != 0 {
			t.Fatalf("predict refused declared slots: %v %+v", err, pr.Errors)
		}
	}

	refusals := []struct {
		name string
		mut  func(*types.RegressionSpec)
		code perr.Code
		slot string
	}{
		{"undeclared_type", func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} }, perr.PROCESSING_CONFIG, "covariance"},
		{"declared_type_malformed", func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster}
		}, perr.PROCESSING_CONFIG, ""},
		{"declared_robust_with_penalty", func(s *types.RegressionSpec) {
			s.Penalty, s.Covariance = "l2", &types.RegressionCovariance{Type: types.CovarianceHC1}
		}, perr.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, ""},
		{"selection_undeclared", func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }, perr.PROCESSING_CONFIG, "selection"},
	}
	for _, rc := range refusals {
		t.Run(rc.name, func(t *testing.T) {
			s := slopeSpec(regDeclaredName)
			rc.mut(s)
			r := regressionRequest(cohort, s)
			_, err := p.Process(context.Background(), r)
			ce := requireCodedError(t, err, rc.code)
			if ce.Details["slot"] != nilIfEmpty(rc.slot) {
				t.Errorf("details.slot = %v, want %q", ce.Details["slot"], rc.slot)
			}
			pr, err := p.Predict(context.Background(), r)
			if err != nil || len(pr.Errors) == 0 {
				t.Fatalf("predict: %v %+v", err, pr)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict first error = %+v, runtime %s %v", got, ce.Code, ce.Details)
			}
		})
	}

	m := p.Manifest(context.Background())
	var row *descriptor.RegressionMeta
	for i := range m.Extensions.Regressions {
		if m.Extensions.Regressions[i].Name == string(regDeclaredName) {
			row = &m.Extensions.Regressions[i]
		}
	}
	if row == nil {
		t.Fatalf("manifest lacks %s", regDeclaredName)
	}
	var slots []string
	for _, prm := range row.Params {
		if prm.Slot {
			slots = append(slots, prm.Name)
			if prm.Name == "covariance" && !strings.Contains(prm.Description, "classical, hc1, cluster") {
				t.Errorf("covariance description %q lacks the declared types", prm.Description)
			}
		}
	}
	if want := []string{"target", "predictors", "penalty", "alpha", "l1_ratio", "resample", "covariance"}; !reflect.DeepEqual(slots, want) {
		t.Errorf("manifest slot params = %v, want %v", slots, want)
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TestExtensions_RegressionCovarianceTypesValidated: a CovarianceTypes
// entry outside the covariance types, or listed twice, fails pulse.New
// with PULSE_EXTENSION_PARAM_INVALID reason invalid_covariance_type.
func TestExtensions_RegressionCovarianceTypesValidated(t *testing.T) {
	for _, list := range [][]types.CovarianceType{{"hc9"}, {types.CovarianceHC0, types.CovarianceHC0}} {
		var seen *types.RegressionSpec
		_, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
			Name: regDeclaredName, Factory: specRecorder(&seen), Streamable: true, CovarianceTypes: list,
		}}}))
		ce := requireCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
		if ce.Details["reason"] != "invalid_covariance_type" {
			t.Errorf("%v: reason = %v, want invalid_covariance_type", list, ce.Details["reason"])
		}
	}
}
