package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
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
//     WIN_LEAD); finalizeRowCells turns it into the string at the end,
//     and spells every date / datetime column as its calendar literal;
//   - Sample and Lookup, which do not order, render straight through
//     RenderRecordCells — which also spells a u64 cell as its exact
//     uint64 word (record rows still carry the float64 echo).

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

// finalizeRowCells renders rows' cells into the form a caller
// receives. Runs once, after the last stage that reads or orders cell
// values (windows, Request.Sort, post-tests) — every one of those sees
// the numeric epoch day / second and the scale-carrying decimal:
//
//   - every decimalCell — schema columns and any window output that
//     copied one — becomes its decimal string;
//   - every temporal column (temporalColumns) becomes its calendar
//     literal: `date` as YYYY-MM-DD, `datetime` as RFC 3339 UTC (`…Z`),
//     the spelling Sample, Lookup and export use. No request zone
//     applies: a record row spells the stored value.
func finalizeRowCells(rows []map[string]any, schema *encoding.Schema, windows []*types.Window) {
	temporalCols := temporalColumns(schema, windows)
	for _, row := range rows {
		for k, v := range row {
			if c, ok := v.(decimalCell); ok {
				row[k] = c.String()
				continue
			}
			if ft, ok := temporalCols[k]; ok {
				row[k] = temporalCell(&encoding.Field{Type: ft}, v)
			}
		}
	}
}

// temporalColumns maps every record-row column holding a date /
// datetime value to its type: the schema's date and datetime fields,
// plus each WIN_LAG / WIN_LEAD output copying one (in window order, so
// a lag of a lag resolves). Any other window output — a running sum or
// delta of days, a rank, an extension window — is a number. A window
// label never shadows a column (SERVICE_VALIDATION), so a name keeps
// one type.
func temporalColumns(schema *encoding.Schema, windows []*types.Window) map[string]encoding.FieldType {
	cols := map[string]encoding.FieldType{}
	if schema != nil {
		for i := range schema.Fields {
			if ft := schema.Fields[i].Type; ft == encoding.FieldTypeDate || ft == encoding.FieldTypeDateTime {
				cols[schema.Fields[i].Name] = ft
			}
		}
	}
	for _, w := range windows {
		if w == nil || w.Label == "" {
			continue
		}
		if src, ok := cols[w.Field]; ok && (w.Type == types.WIN_LAG || w.Type == types.WIN_LEAD) {
			cols[w.Label] = src
		}
	}
	return cols
}

// RenderRecordCells returns rec's values in the row form a caller
// receives (Sample, Lookup): AllValues with every decimal128 cell
// rendered as its decimal string at the column scale, every `date`
// cell as its calendar day (YYYY-MM-DD) and every `datetime` cell as
// RFC 3339 UTC (`…Z`) — the text export writes — and every `u64` cell
// as the record's EXACT stored word (Go uint64, Record.Uint64Value), so
// a value at or above 2^53 prints its own digits instead of its float64
// echo's; below 2^53 the JSON digits are the ones the echo printed. No
// request zone applies: a raw row spells the stored value. The returned
// map is the caller's to keep; AllValues' cached map is never mutated.
func RenderRecordCells(rec *Record) map[string]any {
	src := rec.AllValues()
	out := make(map[string]any, len(src))
	for k, v := range src {
		if d, ok := v.(encoding.Decimal128); ok {
			out[k] = rowCell(rec.fieldNamed(k), d).(decimalCell).String()
			continue
		}
		f := rec.fieldNamed(k)
		if f != nil && f.Type == encoding.FieldTypeU64 {
			if u, ok := rec.Uint64Value(k); ok {
				out[k] = u
				continue
			}
		}
		out[k] = temporalCell(f, v)
	}
	return out
}

// temporalCell renders a date / datetime cell (AllValues' signed epoch
// days / seconds) as its calendar literal; any other cell passes
// through.
func temporalCell(f *encoding.Field, v any) any {
	n, ok := v.(float64)
	if f == nil || !ok {
		return v
	}
	switch f.Type {
	case encoding.FieldTypeDate:
		return temporal.FormatDay(int64(n))
	case encoding.FieldTypeDateTime:
		return temporal.FormatUTC(int64(n))
	}
	return v
}

// fieldNamed resolves name against the record's schema (nil when the
// record has none or the name is off-schema).
func (r *Record) fieldNamed(name string) *encoding.Field {
	if r.schema == nil {
		return nil
	}
	return r.schema.Field(name)
}
