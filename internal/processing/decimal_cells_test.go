package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestDecimalCells_EveryReaderReadsTheValue (E4-S8): each numeric
// reader of a row / crosstab cell reads a decimal cell — a decimal
// aggregate result and a record-row decimal — bit for bit as the
// float64 cell of equal value, and an undefined decimal figure as NaN,
// as it reads a NaN float64 cell. Before, each type switch fell through
// on the opaque struct (0, NaN or not-ok: a silent null).
func TestDecimalCells_EveryReaderReadsTheValue(t *testing.T) {
	t.Parallel()
	readers := map[string]func(any) (float64, bool){
		"toFloat64 (group / series overlays)": func(v any) (float64, bool) {
			f := toFloat64(v)
			return f, !math.IsNaN(f) || isNaNCell(v)
		},
		"floatValue (post-tests)": floatValue,
		"scalarFromCell (crosstab-host overlays)": func(v any) (float64, bool) {
			return scalarFromCell(types.MatrixCell{Value: v, Present: true})
		},
		"coerceFloat64 (crosstab normalize)": func(v any) (float64, bool) {
			f := coerceFloat64(v)
			return f, f != 0 || isNaNCell(v)
		},
		"coerceFloat (chain hand-off)":                 coerceFloat,
		"coerceNumericValue (stage / compose readers)": coerceNumericValue,
		"MatrixCell.Scalar (public)": func(v any) (float64, bool) {
			f := types.MatrixCell{Value: v, Present: true}.Scalar()
			return f, f != 0 || isNaNCell(v)
		},
	}
	for _, m := range []int64{1050, -325, 4775, 1} {
		d := encoding.NewDecimal128FromInt(m)
		want := d.Float64(2)
		cells := map[string]any{
			"aggregate":  decimalAggResult{Value: d, Scale: 2, Precision: 18},
			"record row": decimalCell{value: d, scale: 2},
		}
		for rname, read := range readers {
			fv, fok := read(want)
			if !fok || math.Float64bits(fv) != math.Float64bits(want) {
				t.Fatalf("%s reads the float64 cell %v as (%v, %v)", rname, want, fv, fok)
			}
			for cname, c := range cells {
				got, ok := read(c)
				if !ok || math.Float64bits(got) != math.Float64bits(want) {
					t.Errorf("%s: %s decimal %d/100 reads (%v, %v), float64 cell %v", rname, cname, m, got, ok, want)
				}
			}
		}
	}
	for rname, read := range readers {
		got, _ := read(undefinedDecimal())
		if !math.IsNaN(got) {
			t.Errorf("%s: undefined decimal figure reads %v, want NaN (as a NaN float64 cell)", rname, got)
		}
		if got, ok := read(decimalCount(7)); !ok || got != 7 {
			t.Errorf("%s: decimal count 7 reads (%v, %v)", rname, got, ok)
		}
		if got, ok := read(decimalAggResult{FellBack: true, Float: 2.5}); !ok || got != 2.5 {
			t.Errorf("%s: fallen-back decimal 2.5 reads (%v, %v)", rname, got, ok)
		}
	}
}

func isNaNCell(v any) bool {
	if r, ok := v.(decimalAggResult); ok {
		return r.Undefined
	}
	return false
}
