package processing

import (
	"encoding/json"
	"reflect"

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
//     spells every date / datetime column as its calendar literal, and
//     swaps every u64 column's float64 echo for the record's exact
//     uint64 word (u64RowWords);
//   - Sample and Lookup, which do not order, render straight through
//     RenderRecordCells — which also spells a u64 cell as its exact
//     uint64 word.

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
//   - every u64 schema column, and every WIN_LAG / WIN_LEAD output
//     copying one, becomes its record's exact uint64 word
//     (u64RowWords.render).
func finalizeRowCells(rows []map[string]any, schema *encoding.Schema, windows []*types.Window, words *u64RowWords) {
	temporalCols := temporalColumns(schema, windows)
	for _, row := range rows {
		words.render(row)
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

// u64RowWords keeps each record row's exact u64 words beside the row.
// The row itself carries the float64 echo through every stage that reads
// or orders it — window operators (built-in and extension: the extend
// WindowComputer sees the row map, so its cells keep today's Go types),
// Request.Sort and post-tests — so 2^53 still ties 2^53+1 in an order
// and a window computes exactly as before. Request.Sort reorders the row
// slice, never the maps, so the words are keyed by row map identity.
//
// A built-in copy window (WIN_LAG / WIN_LEAD) writing a verbatim copy
// of a u64 cell — a schema column or an earlier copy's output, so a lag
// of a lag follows — reports the copy (copyObserver), and the source
// row's exact word is kept for the output column too (copies), so the
// copy renders as the word it copied, not its echo.
type u64RowWords struct {
	cols   []string
	words  map[uintptr][]uint64
	copies map[uintptr]map[string]uint64
}

// rowKey is a row map's identity.
func rowKey(row map[string]any) uintptr { return reflect.ValueOf(row).Pointer() }

// copyObserver returns the window.CopyObserver that keeps copy-window
// outputs' exact words, or nil (observe nothing) for a nil table.
func (w *u64RowWords) copyObserver() window.CopyObserver {
	if w == nil {
		return nil
	}
	return w.observeCopy
}

// observeCopy keeps dst's label word: src's exact word for field when
// src holds one (its cell still that word's echo), else none — an
// out-of-partition default / null, or a non-u64 source.
func (w *u64RowWords) observeCopy(dst, src map[string]any, label, field string) {
	k := rowKey(dst)
	if src != nil {
		if word, ok := w.wordOf(src, field); ok {
			if w.copies == nil {
				w.copies = map[uintptr]map[string]uint64{}
			}
			m := w.copies[k]
			if m == nil {
				m = map[string]uint64{}
				w.copies[k] = m
			}
			m[label] = word
			return
		}
	}
	delete(w.copies[k], label)
}

// wordOf returns row's exact word for col — a u64 schema column or a
// copy window's output — when the cell is still that word's echo.
func (w *u64RowWords) wordOf(row map[string]any, col string) (uint64, bool) {
	f, ok := row[col].(float64)
	if !ok {
		return 0, false
	}
	k := rowKey(row)
	for j, c := range w.cols {
		if c != col {
			continue
		}
		if words, ok := w.words[k]; ok && f == float64(words[j]) {
			return words[j], true
		}
		return 0, false
	}
	if word, ok := w.copies[k][col]; ok && f == float64(word) {
		return word, true
	}
	return 0, false
}

// newU64RowWords returns the side table for schema's u64 fields, or nil
// (every method a no-op) when it has none.
func newU64RowWords(schema *encoding.Schema, n int) *u64RowWords {
	if schema == nil {
		return nil
	}
	var cols []string
	for i := range schema.Fields {
		if schema.Fields[i].Type == encoding.FieldTypeU64 {
			cols = append(cols, schema.Fields[i].Name)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	return &u64RowWords{cols: cols, words: make(map[uintptr][]uint64, n)}
}

// capture records rec's exact word for every u64 column row holds.
func (w *u64RowWords) capture(row map[string]any, rec *Record) {
	if w == nil {
		return
	}
	words := make([]uint64, len(w.cols))
	for j, c := range w.cols {
		if _, ok := row[c]; !ok {
			continue
		}
		words[j], _ = rec.Uint64Value(c)
	}
	w.words[rowKey(row)] = words
}

// render swaps each u64 column's float64 echo for its exact word. A
// cell that is no longer that word's echo (absent, null, or rewritten by
// a stage) is left as it is.
func (w *u64RowWords) render(row map[string]any) {
	if w == nil {
		return
	}
	k := rowKey(row)
	for label, word := range w.copies[k] {
		if f, ok := row[label].(float64); ok && f == float64(word) {
			row[label] = word
		}
	}
	words, ok := w.words[k]
	if !ok {
		return
	}
	for j, c := range w.cols {
		if f, ok := row[c].(float64); ok && f == float64(words[j]) {
			row[c] = words[j]
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
// A categorical / set cell whose code addresses no dictionary entry is
// refused with AllValuesChecked's ENCODING_INVALID (U51 E5-S8), never
// rendered as "".
func RenderRecordCells(rec *Record) (map[string]any, error) {
	src, err := rec.AllValuesChecked()
	if err != nil {
		return nil, err
	}
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
	return out, nil
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
