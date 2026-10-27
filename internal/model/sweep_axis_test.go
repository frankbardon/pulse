package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheckSweepAxis: the static sweep_axis() rule — unknown fit, a fit
// not bound from a sweep, an unknown axis (only when the axes are
// known), in that order — over the static catalog and the runtime set
// alike.
func TestCheckSweepAxis(t *testing.T) {
	fits := []types.FitSpec{
		{Name: "plain", Request: &types.Request{Regressions: []*types.RegressionSpec{namedOLS("m", "x")}}},
		{Name: "best", FromSweep: &types.FitFromSweep{Regression: "m"}, Request: &types.Request{Regressions: []*types.RegressionSpec{namedOLS("m", "x")}}},
	}
	unknownAxes := CatalogFromFits(fits)
	known := CatalogFromFits(fits).WithSweepAxes([]string{"tv", "hi"})
	set := &Set{}
	if err := set.Add(&Bound{Name: "best", Type: types.REG_OLS, SweepBound: true, SweepAxes: map[string]any{"tv": json.Number("0.4"), "hi": 60}}); err != nil {
		t.Fatal(err)
	}
	if err := set.Add(&Bound{Name: "plain", Type: types.REG_OLS}); err != nil {
		t.Fatal(err)
	}
	for name, cat := range map[string]Catalog{"static": known, "runtime": set} {
		if cerr := CheckSweepAxis(cat, "nope", "tv"); cerr == nil || cerr.Code != errors.PULSE_MODEL_UNKNOWN {
			t.Errorf("%s unknown fit: %v", name, cerr)
		}
		if cerr := CheckSweepAxis(cat, "plain", "tv"); cerr == nil || cerr.Code != errors.PULSE_MODEL_TERM_UNKNOWN || cerr.Details["reason"] != ReasonNotSweepBound {
			t.Errorf("%s not sweep bound: %v", name, cerr)
		}
		cerr := CheckSweepAxis(cat, "best", "decay")
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_TERM_UNKNOWN || cerr.Details["reason"] != ReasonAxisUnknown ||
			!reflect.DeepEqual(cerr.Details["axes"], []string{"hi", "tv"}) {
			t.Errorf("%s unknown axis: %v", name, cerr)
		}
		if cerr := CheckSweepAxis(cat, "best", "tv"); cerr != nil {
			t.Errorf("%s known axis: %v", name, cerr)
		}
	}
	// Axes unknown statically: any axis passes, the runtime judges it.
	if cerr := CheckSweepAxis(unknownAxes, "best", "decay"); cerr != nil {
		t.Errorf("axes unknown: %v", cerr)
	}
	// Values: numbers in any form read as float64.
	for axis, want := range map[string]any{"tv": 0.4, "hi": 60.0} {
		if v, cerr := SweepAxis(set, "best", axis); cerr != nil || v != want {
			t.Errorf("SweepAxis(%s) = %v %v, want %v", axis, v, cerr, want)
		}
	}
	// A failed sweep-bound fit: the fit's own code, details.fit.
	failed := &Set{}
	sig := CatalogFromFits(fits).WithSweepAxes([]string{"tv"})["best"]
	if err := failed.AddFailed(sig, &types.SlotError{Code: string(errors.PULSE_SWEEP_RANK_PATH), Message: "empty"}); err != nil {
		t.Fatal(err)
	}
	if _, cerr := SweepAxis(failed, "best", "tv"); cerr == nil || cerr.Code != errors.PULSE_SWEEP_RANK_PATH || cerr.Details["fit"] != "best" {
		t.Errorf("failed fit: %v", cerr)
	}
}
