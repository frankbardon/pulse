package model

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mmm_carry_test.go pins the static face of adstock carry-over: which
// specs carry, the order they read, the adstock state's outputs form,
// the kernel weights it reads off the coefficients, and every
// carry-over refusal of a scoring reference.

// TestMMMCarryover_Specs: REG_MMM_BAYES always adstocks in-model, a
// REG_MMM_HILL only under params.adstock; carryover is on unless
// params.carryover is false; the order is spec.Order, a REG_MMM_BAYES
// geo panel's series falling back to params.geo (params.time is
// removed: it is no order).
func TestMMMCarryover_Specs(t *testing.T) {
	t.Parallel()

	bayes := func(params string, order *types.RegressionOrder) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_MMM_BAYES, Params: json.RawMessage(params), Order: order}
	}
	for _, tc := range []struct {
		name          string
		spec          *types.RegressionSpec
		inModel, on   bool
		field, series string
	}{
		{"bayes_geo_partition", bayes(`{"channels":["tv"],"geo":"mkt"}`, &types.RegressionOrder{Field: "week"}), true, true, "week", "mkt"},
		{"bayes_removed_time_is_no_order", bayes(`{"channels":["tv"],"time":"week"}`, nil), true, true, "", ""},
		{"bayes_order", bayes(`{"channels":["tv"]}`, &types.RegressionOrder{Field: "wk", Partition: "geo"}), true, true, "wk", "geo"},
		{"bayes_off", bayes(`{"channels":["tv"],"carryover":false}`, &types.RegressionOrder{Field: "week"}), true, false, "week", ""},
		{"hill_adstock", hillAdstockSpec(`, "adstock": "geometric"`), true, true, "", ""},
		{"hill_off", hillAdstockSpec(`, "adstock": "geometric", "carryover": false`), true, false, "", ""},
		{"hill_plain", hillSpec(), false, false, "", ""},
		{"ols", &types.RegressionSpec{Type: types.REG_OLS}, false, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := MMMInModelAdstock(tc.spec); got != tc.inModel {
				t.Errorf("MMMInModelAdstock = %v, want %v", got, tc.inModel)
			}
			if got := MMMCarryover(tc.spec); got != tc.on {
				t.Errorf("MMMCarryover = %v, want %v", got, tc.on)
			}
			if f, p := MMMOrder(tc.spec); f != tc.field || p != tc.series {
				t.Errorf("MMMOrder = %q %q, want %q %q", f, p, tc.field, tc.series)
			}
		})
	}
}

// carryState is a two-series geometric state over channel tv.
func carryState() *AdstockState {
	return &AdstockState{Field: "week", Partition: "mkt", Kernel: "geometric", MaxLag: 2, Step: 1, Series: []AdstockSeries{
		{Key: "east", Order: []int64{9, 10}, Spend: map[string][]Float{"tv": {4, Float(math.NaN())}}},
		{Key: "west", Order: []int64{10}, Spend: map[string][]Float{"tv": {7}}},
	}}
}

// TestAdstockState_RoundTrip: the outputs form reads back to the same
// state (a null spend NaN), outputs without one read false, and a state
// that breaks its shape is an error.
func TestAdstockState_RoundTrip(t *testing.T) {
	t.Parallel()

	st := carryState()
	v, err := st.Value()
	if err != nil {
		t.Fatal(err)
	}
	if _, isMap := v.(map[string]any); !isMap {
		t.Fatalf("Value = %T, want the JSON object form", v)
	}
	got, ok, err := ReadAdstockState(map[string]any{MMMAdstockStateKey: v})
	if err != nil || !ok {
		t.Fatalf("ReadAdstockState = %v %v", ok, err)
	}
	if !math.IsNaN(float64(got.Series[0].Spend["tv"][1])) {
		t.Errorf("null spend read back %v, want NaN", got.Series[0].Spend["tv"][1])
	}
	got.Series[0].Spend["tv"][1], st.Series[0].Spend["tv"][1] = 0, 0
	if !reflect.DeepEqual(got, st) {
		t.Errorf("round trip %+v, want %+v", got, st)
	}
	if _, ok, err := ReadAdstockState(map[string]any{}); ok || err != nil {
		t.Errorf("absent state = %v %v", ok, err)
	}
	if _, ok, err := ReadAdstockState(map[string]any{MMMAdstockStateKey: map[string]any{"field": "week", "max_lag": 2, "step": 1,
		"series": []any{map[string]any{"order": []any{1.0, 2.0}, "spend": map[string]any{"tv": []any{1.0}}}}}}); !ok || err == nil {
		t.Errorf("ragged state = %v %v, want an error", ok, err)
	}
	if got := st.Channels(); !reflect.DeepEqual(got, []string{"tv"}) {
		t.Errorf("Channels = %v", got)
	}
}

// TestAdstockState_Weights: the kernel at the fit's coefficients,
// unnormalised (decay^l over lags 0..max_lag); missing coefficients are
// an error.
func TestAdstockState_Weights(t *testing.T) {
	t.Parallel()

	w, err := carryState().Weights(map[string]float64{"decay:tv": 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if want := []float64{1, 0.5, 0.25}; !reflect.DeepEqual(w["tv"], want) {
		t.Errorf("weights %v, want %v", w["tv"], want)
	}
	if _, err := carryState().Weights(map[string]float64{}); err == nil {
		t.Error("no decay coefficient: want an error")
	}
}

// TestScoreCarries: only an ATTR_REG_PREDICT with params.order and
// carryover not false is two-pass.
func TestScoreCarries(t *testing.T) {
	t.Parallel()

	for params, want := range map[string]bool{
		`{"fit":"m","order":{"field":"week"}}`:                   true,
		`{"fit":"m","order":{"field":"week"},"carryover":true}`:  true,
		`{"fit":"m","order":{"field":"week"},"carryover":false}`: false,
		`{"fit":"m"}`:   false,
		`{"fit":"m",`:   false,
		`{"fit":"m",1}`: false,
	} {
		if got := ScoreCarries(scoreAttr(params)); got != want {
			t.Errorf("ScoreCarries(%s) = %v, want %v", params, got, want)
		}
	}
	if ScoreCarries(&types.Attribute{Type: types.ATTR_FORMULA, Params: json.RawMessage(`{"order":{"field":"w"}}`)}) {
		t.Error("ATTR_FORMULA carries")
	}
}

// TestCheckScore_CarryRefusals: every carry-over refusal of a scoring
// reference, against a static catalog (an in-model adstocked fit holds
// its state) and a bound fit without one.
func TestCheckScore_CarryRefusals(t *testing.T) {
	t.Parallel()

	cat := CatalogFromFits(append(bayesFits(), types.FitSpec{Name: "ols", Request: olsReq(&types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}})}))
	scorable := func(types.RegressionType) bool { return true }
	for _, tc := range []struct {
		params, reason string
	}{
		{`{"fit":"draws","order":{"field":"week","partition":"market"}}`, ""},
		{`{"fit":"draws","carryover":true,"order":{"field":"week"}}`, ""},
		{`{"fit":"draws","carryover":false}`, ""},
		{`{"fit":"draws"}`, ReasonCarryOrderRequired},
		{`{"fit":"draws","carryover":true}`, ReasonCarryOrderRequired},
		{`{"fit":"draws","order":{"partition":"market"}}`, ReasonCarryOrderFieldMissing},
		{`{"fit":"draws","carryover":false,"order":{"field":"week"}}`, ReasonCarryOrderUnused},
		{`{"fit":"ols","order":{"field":"week"}}`, ReasonCarryOrderUnused},
		{`{"fit":"ols","carryover":false}`, ReasonCarryoverUnused},
		{`{"fit":"hill","carryover":true}`, ReasonCarryoverUnused},
	} {
		_, _, err := CheckScoreFor(scoreAttr(tc.params), cat, scorable)
		if tc.reason == "" {
			if err != nil {
				t.Errorf("%s refused: %v", tc.params, err)
			}
			continue
		}
		if err == nil || err.Code != errors.PULSE_MODEL_SCORE_INVALID || err.Details["reason"] != tc.reason {
			t.Errorf("%s: %v, want reason %s", tc.params, err, tc.reason)
		}
	}

	// A bound in-model adstocked fit without its state (a file saved
	// before carry-over) carries nothing: carryover_state_missing.
	set := &Set{}
	res := &types.RegressionResult{Type: types.REG_MMM_BAYES, Coefficients: map[string]float64{"tv": 1, "k:tv": 2, "s:tv": 1, "decay:tv": 0.4},
		Outputs: map[string]any{MMMDrawsKey: map[string]any{}}}
	b, err := Bind("old", &types.RegressionSpec{Type: types.REG_MMM_BAYES, Target: "y", Predictors: []string{"tv"}}, res)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	if sig, _ := set.Signature("old"); !sig.Carries || sig.CarryState {
		t.Fatalf("stateless bound fit: Carries %v CarryState %v", sig.Carries, sig.CarryState)
	}
	_, _, cerr := CheckScoreFor(scoreAttr(`{"fit":"old","order":{"field":"week"}}`), set, scorable)
	if cerr == nil || cerr.Details["reason"] != ReasonCarryStateMissing {
		t.Errorf("stateless fit: %v, want %s", cerr, ReasonCarryStateMissing)
	}
	if _, _, cerr := CheckScoreFor(scoreAttr(`{"fit":"old","carryover":false}`), set, scorable); cerr != nil {
		t.Errorf("stateless fit under carryover false: %v", cerr)
	}
	// A REG_MMM_HILL whose table names a kernel parameter carries.
	if !boundCarries(types.REG_MMM_HILL, map[string]float64{"shape:tv": 1}) || boundCarries(types.REG_MMM_HILL, map[string]float64{"k:tv": 1}) {
		t.Error("boundCarries misreads a REG_MMM_HILL table")
	}
}

// TestCarryOrderFieldInvalid: the walk's refusal names the slot, the
// field and — a schema field of the wrong type — its type.
func TestCarryOrderFieldInvalid(t *testing.T) {
	t.Parallel()

	ce := CarryOrderFieldInvalid("m", "order.field", "week", "")
	if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Details["reason"] != ReasonCarryOrderField || ce.Details["slot"] != "order.field" || ce.Details["field"] != "week" {
		t.Errorf("missing: %v", ce)
	}
	if _, typed := ce.Details["field_type"]; typed {
		t.Errorf("missing field carries a type: %v", ce.Details)
	}
	ce = CarryOrderFieldInvalid("m", "order.partition", "x", "f64")
	if ce.Details["field_type"] != "f64" {
		t.Errorf("typed: %v", ce.Details)
	}
}
