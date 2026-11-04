package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// file_sweep_axes_test.go pins the sweep_axes model-file feature: a
// sweep-bound fit's winning axis values round-trip exactly (numbers,
// strings, bools), the loaded model binds sweep-bound so sweep_axis()
// reads them, predict's catalog knows the axis names (axis_unknown
// judged statically, as the runtime does), and a file without the
// section keeps the not_sweep_bound refusal.

// sweepAxesFile is a plain OLS fit's file carrying axes, round-tripped
// through its bytes.
func sweepAxesFile(t *testing.T, axes map[string]any) (*File, []byte) {
	t.Helper()
	spec, res := fullFit()
	f, err := NewFile("m", spec, res, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetSweepAxes(axes); err != nil {
		t.Fatal(err)
	}
	return roundTrip(t, f)
}

// TestModelFile_SweepAxesReadBackExactly: every axis value reads back
// through sweep_axis() on the loaded model exactly as the in-request
// sweep-bound fit reads it — a number (json.Number, float64 or Go
// integer) as the same float64, a string or bool as is.
func TestModelFile_SweepAxesReadBackExactly(t *testing.T) {
	t.Parallel()

	axes := map[string]any{
		"decay": json.Number("0.30000000000000004"), "sum": 0.1 + 0.2, "lags": 3,
		"wide": json.Number("12345678901234567890"), "tiny": 5e-324,
		"penalty": "l2", "lagged": false,
	}
	got, data := sweepAxesFile(t, axes)
	if !strings.Contains(string(data), `"decay": 0.30000000000000004`) || !strings.Contains(string(data), `"penalty": "l2"`) {
		t.Fatalf("sweep_axes not written as raw JSON:\n%s", data)
	}
	refs := []types.ModelRef{{Name: "m", Path: "m" + FileSuffix}}
	loaded, err := (*Set)(nil).WithModels(refs, []*File{got})
	if err != nil {
		t.Fatal(err)
	}
	inRequest := &Set{byName: map[string]*Bound{}}
	b, err := Bind("m", fullFitSpec(), fullFitResult())
	if err != nil {
		t.Fatal(err)
	}
	b.SweepBound, b.SweepAxes = true, axes
	if err := inRequest.Add(b); err != nil {
		t.Fatal(err)
	}
	for axis := range axes {
		want, werr := SweepAxis(inRequest, "m", axis)
		have, herr := SweepAxis(loaded, "m", axis)
		if werr != nil || herr != nil {
			t.Fatalf("%s: %v / %v", axis, werr, herr)
		}
		if !reflect.DeepEqual(have, want) {
			t.Errorf("%s: loaded %#v, in-request %#v", axis, have, want)
		}
	}
}

// TestModelFile_SweepAxesCatalogParity: predict's catalog of a loaded
// sweep-bound file (CatalogWithModels) carries the same sweep face as
// the runtime's bound model — so CheckSweepAxis refuses an unknown axis
// (axis_unknown, details.axes) and passes a known one identically; a
// file without sweep_axes is not_sweep_bound on both.
func TestModelFile_SweepAxesCatalogParity(t *testing.T) {
	t.Parallel()

	refs := []types.ModelRef{{Name: "m", Path: "m" + FileSuffix}}
	for _, tc := range []struct {
		name   string
		axes   map[string]any
		axis   string
		reason string
	}{
		{"known axis", map[string]any{"tv": json.Number("0.4"), "hi": json.Number("60")}, "tv", ""},
		{"unknown axis", map[string]any{"tv": json.Number("0.4"), "hi": json.Number("60")}, "decay", ReasonAxisUnknown},
		{"no sweep_axes", nil, "tv", ReasonNotSweepBound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := sweepAxesFile(t, tc.axes)
			cat := CatalogWithModels(nil, refs, []*File{got})
			set, err := (*Set)(nil).WithModels(refs, []*File{got})
			if err != nil {
				t.Fatal(err)
			}
			if rt, _ := set.Signature("m"); !reflect.DeepEqual([]any{cat["m"].SweepBound, cat["m"].SweepAxes, cat["m"].SweepAxesKnown}, []any{rt.SweepBound, rt.SweepAxes, rt.SweepAxesKnown}) {
				t.Fatalf("catalog sweep face %+v, runtime %+v", cat["m"], rt)
			}
			static, runtime := CheckSweepAxis(cat, "m", tc.axis), CheckSweepAxis(set, "m", tc.axis)
			if tc.reason == "" {
				if static != nil || runtime != nil {
					t.Fatalf("static %v runtime %v, want none", static, runtime)
				}
				return
			}
			if static == nil || runtime == nil || static.Code != errors.PULSE_MODEL_TERM_UNKNOWN || static.Details["reason"] != tc.reason ||
				static.Message != runtime.Message || !reflect.DeepEqual(static.Details, runtime.Details) {
				t.Fatalf("static %v, runtime %v, want reason %s on both", static, runtime, tc.reason)
			}
			if tc.reason == ReasonAxisUnknown && !reflect.DeepEqual(static.Details["axes"], []string{"hi", "tv"}) {
				t.Fatalf("details.axes %v", static.Details["axes"])
			}
		})
	}
}

// TestModelFile_SweepAxesStructure: the sweep_axes section obeys the
// features rule — listed without the section, present unlisted, or
// empty is PULSE_MODEL_FILE_INVALID reason schema.
func TestModelFile_SweepAxesStructure(t *testing.T) {
	t.Parallel()

	_, good := sweepAxesFile(t, map[string]any{"tv": json.Number("0.4")})
	var doc map[string]any
	if err := json.Unmarshal(good, &doc); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(m map[string]any){
		"listed without section": func(m map[string]any) { delete(m, "sweep_axes") },
		"present unlisted":       func(m map[string]any) { delete(m, "features") },
		"empty":                  func(m map[string]any) { m["sweep_axes"] = map[string]any{} },
	} {
		m := map[string]any{}
		for k, v := range doc {
			m[k] = v
		}
		edit(m)
		data, _ := json.Marshal(m)
		_, err := DecodeFile("m"+FileSuffix, data)
		ce := coded(t, err)
		if ce.Code != errors.PULSE_MODEL_FILE_INVALID || ce.Details["reason"] != ReasonFileSchema || !strings.Contains(ce.Message, "sweep_axes") {
			t.Errorf("%s: %s %v (%s)", name, ce.Code, ce.Details, ce.Message)
		}
	}
}

func fullFitSpec() *types.RegressionSpec     { s, _ := fullFit(); return s }
func fullFitResult() *types.RegressionResult { _, r := fullFit(); return r }
