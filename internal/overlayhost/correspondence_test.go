package overlayhost

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParseCorrespondenceParams: the strict params decode shared by
// predict and runtime — defaults, the three normalizations, and every
// malformed shape refused PULSE_OVERLAY_PARAM_MISSING with the index
// only when one is known.
func TestParseCorrespondenceParams(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, raw string
		dims      int
		norm      string
		refused   bool
	}{
		{"absent", "", 0, CorrespondenceSymmetric, false},
		{"null", "null", 0, CorrespondenceSymmetric, false},
		{"empty object", "{}", 0, CorrespondenceSymmetric, false},
		{"dimensions", `{"dimensions":3}`, 3, CorrespondenceSymmetric, false},
		{"row principal", `{"normalization":"row_principal"}`, 0, CorrespondenceRowPrincipal, false},
		{"column principal", `{"normalization":"column_principal","dimensions":1}`, 1, CorrespondenceColumnPrincipal, false},
		{"zero", `{"dimensions":0}`, 0, "", true},
		{"fraction", `{"dimensions":2.5}`, 0, "", true},
		{"string", `{"dimensions":"2"}`, 0, "", true},
		{"null dimensions", `{"dimensions":null}`, 0, "", true},
		{"unknown normalization", `{"normalization":"symmetrical"}`, 0, "", true},
		{"null normalization", `{"normalization":null}`, 0, CorrespondenceSymmetric, false},
		{"unknown key", `{"dimension":2}`, 0, "", true},
		{"array", `[1]`, 0, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			spec := &types.OverlaySpec{Kind: types.OverlayKindCorrespondence, Params: json.RawMessage(c.raw)}
			for _, index := range []int{-1, 4} {
				p, cerr := ParseCorrespondenceParams(spec, index)
				if c.refused {
					if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING {
						t.Fatalf("index %d: %v, want PULSE_OVERLAY_PARAM_MISSING", index, cerr)
					}
					if _, has := cerr.Details["index"]; has != (index >= 0) {
						t.Errorf("index %d: details %v", index, cerr.Details)
					}
					continue
				}
				if cerr != nil || p.Dimensions != c.dims || p.Normalization != c.norm {
					t.Fatalf("index %d: %+v %v, want dims %d norm %q", index, p, cerr, c.dims, c.norm)
				}
			}
		})
	}
}

// TestCorrespondenceDimensions: K = min(r, c) − 1; the default 2 is
// capped at K, an explicit value above K is refused, and a table below
// 2 × 2 is left to the shape refusal.
func TestCorrespondenceDimensions(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name               string
		explicit, rows, cs int
		resolved           int
		refused            bool
	}{
		{"default 5x4", 0, 5, 4, 2, false},
		{"default 2x7", 0, 2, 7, 1, false},
		{"explicit at cap", 3, 5, 4, 3, false},
		{"explicit over cap", 4, 5, 4, 0, true},
		{"explicit over cap 2x2", 2, 2, 2, 0, true},
		{"below 2x2 not judged", 3, 1, 4, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p := CorrespondenceParams{Dimensions: c.explicit, Normalization: CorrespondenceSymmetric}
			cerr := CorrespondenceDimensionsRefusal(types.OverlayKindCorrespondence, p, c.rows, c.cs, 0)
			if (cerr != nil) != c.refused {
				t.Fatalf("refusal %v, want refused %v", cerr, c.refused)
			}
			if cerr != nil {
				if cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["max_dimensions"] != MaxCorrespondenceDimensions(c.rows, c.cs) {
					t.Errorf("refusal %+v", cerr)
				}
				return
			}
			if c.rows >= 2 && c.cs >= 2 {
				if got := p.ResolveDimensions(MaxCorrespondenceDimensions(c.rows, c.cs)); got != c.resolved {
					t.Errorf("resolved %d, want %d", got, c.resolved)
				}
			}
		})
	}
}

// TestNonnegativeRefusals: only AGG_COUNT / AGG_SUM cells (or an
// unknown type) pass; only finite non-negative values pass; both raise
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED with their own details.
func TestNonnegativeRefusals(t *testing.T) {
	t.Parallel()

	k := types.OverlayKindCorrespondence
	for typ, refused := range map[types.AggregationType]bool{
		"": false, types.AGG_COUNT: false, types.AGG_SUM: false,
		types.AGG_AVERAGE: true, types.AGG_MODE: true, "AGG_X_CUSTOM": true,
	} {
		cerr := NonnegativeCellRefusal(k, typ, 2)
		if (cerr != nil) != refused {
			t.Errorf("%q: %v, want refused %v", typ, cerr, refused)
		}
		if cerr != nil && (cerr.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || cerr.Details["slot"] != "crosstab.cell" || cerr.Details["index"] != 2) {
			t.Errorf("%q: %+v", typ, cerr)
		}
	}
	row, col := types.AxisKey{"a"}, types.AxisKey{"x"}
	for _, v := range []float64{0, 1, 1e300} {
		if cerr := NonnegativeValueRefusal(k, row, col, v); cerr != nil {
			t.Errorf("%v refused: %v", v, cerr)
		}
	}
	for _, v := range []float64{-1e-300, -2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cerr := NonnegativeValueRefusal(k, row, col, v)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED {
			t.Fatalf("%v: %v, want PULSE_OVERLAY_NONNEGATIVE_REQUIRED", v, cerr)
		}
		if finite := !math.IsNaN(v) && !math.IsInf(v, 0); (cerr.Details["value"] == nil) == finite {
			t.Errorf("%v: details value %v", v, cerr.Details["value"])
		}
	}
}
