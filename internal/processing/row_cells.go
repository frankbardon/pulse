package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing/window"
)

// Record.AllValues is the engine's internal row form: numbers as
// float64, categorical cells as labels, set cells as label slices — all
// JSON-native — but a decimal128 cell as the raw encoding.Decimal128,
// whose fields are unexported, so it marshals as `{}`. A decimal's
// value is unreadable without its column scale, which the Decimal128
// does not carry.
//
// Every path that hands record values to a caller as a row renders a
// decimal cell the way the rest of the response and export already do:
// a decimal STRING at the column's scale (decimalAggResult.MarshalJSON,
// internal/io export's d.String(f.Scale)). Two arms:
//
//   - record rows (recordsToRows → windows → Request.Sort → post-tests)
//     carry a decimalCell, which keeps ordering by value (SortValuer)
//     and carries its scale through a window that copies it (WIN_LAG /
//     WIN_LEAD); finalizeRowCells turns it into the string at the end;
//   - Sample and Lookup, which do not order, render straight through
//     RenderRecordCells.

// decimalCell is a record-row decimal128 cell with its column scale.
type decimalCell struct {
	value encoding.Decimal128
	scale uint8
}

// SortValue orders the cell by value (window.SortValuer).
func (c decimalCell) SortValue() any {
	return window.ScaledDecimal{Value: c.value, Scale: c.scale}
}

// String renders the decimal at its column scale.
func (c decimalCell) String() string { return c.value.String(c.scale) }

// MarshalJSON renders the decimal string, so a row that escapes
// finalizeRowCells still never marshals as `{}`.
func (c decimalCell) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// rowCell lifts one AllValues cell into its record-row form: a decimal
// gains its column scale (f may be nil for an off-schema name, whose
// scale is then 0 — the unscaled mantissa, never `{}`).
func rowCell(f *encoding.Field, v any) any {
	d, ok := v.(encoding.Decimal128)
	if !ok {
		return v
	}
	var scale uint8
	if f != nil {
		scale = f.Scale
	}
	return decimalCell{value: d, scale: scale}
}

// finalizeRowCells replaces every decimalCell in rows — schema columns
// and any window output that copied one — with its decimal string.
// Runs once, after the last stage that orders rows.
func finalizeRowCells(rows []map[string]any) {
	for _, row := range rows {
		for k, v := range row {
			if c, ok := v.(decimalCell); ok {
				row[k] = c.String()
			}
		}
	}
}

// RenderRecordCells returns rec's values in the row form a caller
// receives (Sample, Lookup): AllValues with every decimal128 cell
// rendered as its decimal string at the column scale. The returned map
// is the caller's to keep; AllValues' cached map is never mutated.
func RenderRecordCells(rec *Record) map[string]any {
	src := rec.AllValues()
	out := make(map[string]any, len(src))
	for k, v := range src {
		if d, ok := v.(encoding.Decimal128); ok {
			out[k] = rowCell(rec.fieldNamed(k), d).(decimalCell).String()
			continue
		}
		out[k] = v
	}
	return out
}

// fieldNamed resolves name against the record's schema (nil when the
// record has none or the name is off-schema).
func (r *Record) fieldNamed(name string) *encoding.Field {
	if r.schema == nil {
		return nil
	}
	return r.schema.Field(name)
}
