package overlayhost

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParseRakeParams: the strict decode — defaults, every accepted
// form, and one row per refusal with its code and param.
func TestParseRakeParams(t *testing.T) {
	const both = `"row_targets":{"a":1,"b":3},"col_targets":{"x":2,"y":2}`
	cases := []struct {
		name      string
		raw       string
		want      errors.Code // "" = accepted
		param     string
		targetsAs string
		maxIter   int
		tol       float64
	}{
		{"defaults", `{` + both + `}`, "", "", RakeTargetsShares, RakeDefaultMaxIter, RakeDefaultTolerance},
		{"every param", `{` + both + `,"targets_as":"counts","max_iter":7,"tolerance":0.001}`, "", "", RakeTargetsCounts, 7, 0.001},
		{"counts totals within tolerance", `{"row_targets":{"a":1,"b":3},"col_targets":{"x":2,"y":2.0000000001},"targets_as":"counts","tolerance":1e-6}`, "", "", RakeTargetsCounts, RakeDefaultMaxIter, 1e-6},
		{"no params", ``, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"row targets missing", `{"col_targets":{"x":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"col targets missing", `{"row_targets":{"a":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "col_targets", "", 0, 0},
		{"targets empty", `{"row_targets":{},"col_targets":{"x":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"target negative", `{"row_targets":{"a":-1,"b":2},"col_targets":{"x":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"target not a number", `{"row_targets":{"a":"1"},"col_targets":{"x":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"targets sum zero", `{"row_targets":{"a":0},"col_targets":{"x":1}}`, errors.PULSE_OVERLAY_PARAM_MISSING, "row_targets", "", 0, 0},
		{"targets_as unknown", `{` + both + `,"targets_as":"weights"}`, errors.PULSE_OVERLAY_PARAM_MISSING, "targets_as", "", 0, 0},
		{"max_iter zero", `{` + both + `,"max_iter":0}`, errors.PULSE_OVERLAY_PARAM_MISSING, "max_iter", "", 0, 0},
		{"max_iter fraction", `{` + both + `,"max_iter":2.5}`, errors.PULSE_OVERLAY_PARAM_MISSING, "max_iter", "", 0, 0},
		{"tolerance zero", `{` + both + `,"tolerance":0}`, errors.PULSE_OVERLAY_PARAM_MISSING, "tolerance", "", 0, 0},
		{"tolerance one", `{` + both + `,"tolerance":1}`, errors.PULSE_OVERLAY_PARAM_MISSING, "tolerance", "", 0, 0},
		{"unknown param", `{` + both + `,"weights":true}`, errors.PULSE_OVERLAY_PARAM_MISSING, "", "", 0, 0},
		{"counts totals differ", `{"row_targets":{"a":1,"b":3},"col_targets":{"x":2,"y":3},"targets_as":"counts"}`, errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH, "", "", 0, 0},
		{"shares totals may differ", `{"row_targets":{"a":1,"b":3},"col_targets":{"x":2,"y":3}}`, "", "", RakeTargetsShares, RakeDefaultMaxIter, RakeDefaultTolerance},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.OverlaySpec{Kind: types.OverlayKindRake, Params: json.RawMessage(c.raw)}
			got, cerr := ParseRakeParams(spec, 2)
			if c.want == "" {
				if cerr != nil {
					t.Fatalf("refused: %v", cerr)
				}
				if got.TargetsAs != c.targetsAs || got.MaxIter != c.maxIter || got.Tolerance != c.tol {
					t.Fatalf("got %+v", got)
				}
				if len(got.RowTargets) == 0 || len(got.ColTargets) == 0 {
					t.Fatalf("targets not decoded: %+v", got)
				}
				return
			}
			if cerr == nil || cerr.Code != c.want {
				t.Fatalf("got %v, want %s", cerr, c.want)
			}
			if cerr.Details["index"] != 2 || cerr.Details["kind"] != string(types.OverlayKindRake) {
				t.Fatalf("details %v", cerr.Details)
			}
			if c.param != "" && cerr.Details["param"] != c.param {
				t.Fatalf("param %v, want %s", cerr.Details["param"], c.param)
			}
			if c.want == errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH && cerr.Details["reason"] != "totals_differ" {
				t.Fatalf("details %v", cerr.Details)
			}
		})
	}
}

// TestRakeKeyRefusal: nil when both sides match; otherwise sorted,
// never-null lists under reason "keys".
func TestRakeKeyRefusal(t *testing.T) {
	if cerr := RakeKeyRefusal(types.OverlayKindRake, RakeAxisRow, nil, nil, 0); cerr != nil {
		t.Fatalf("matching keys refused: %v", cerr)
	}
	cerr := RakeKeyRefusal(types.OverlayKindRake, RakeAxisColumn, []string{"z", "b"}, nil, -1)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_RAKE_TARGET_MISMATCH {
		t.Fatalf("got %v", cerr)
	}
	d := cerr.Details
	if d["axis"] != "column" || d["reason"] != "keys" {
		t.Fatalf("details %v", d)
	}
	if got := d["target_only"].([]string); len(got) != 2 || got[0] != "b" {
		t.Fatalf("target_only %v not sorted", got)
	}
	if got := d["host_only"].([]string); got == nil || len(got) != 0 {
		t.Fatalf("host_only %v, want an empty list", got)
	}
	if _, ok := d["index"]; ok {
		t.Fatalf("index -1 leaked into details %v", d)
	}
}

// TestRakeTotalsRefusal: the gap is judged relative to the larger
// total.
func TestRakeTotalsRefusal(t *testing.T) {
	for _, c := range []struct {
		rows, cols, tol float64
		refuse          bool
	}{
		{100, 100, 1e-8, false},
		{100, 100.0000001, 1e-8, false}, // gap 1e-7, relative 1e-9
		{100, 100.0000001, 1e-10, true},
		{100, 101, 0.01, false},
		{100, 102, 0.01, true},
	} {
		got := RakeTotalsRefusal(types.OverlayKindRake, c.rows, c.cols, c.tol, 0) != nil
		if got != c.refuse {
			t.Errorf("rows %v cols %v tol %v: refused %v, want %v", c.rows, c.cols, c.tol, got, c.refuse)
		}
	}
}
