package io

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// rowConverter turns one source row into encoded field values: the ONE
// per-cell conversion Run writes and the detecting import predict
// (ImportJob.Predict with Groups / SuggestGroups / ElideConstants)
// measures, so a predicted dictionary or ratio is computed over exactly
// the bytes the import would write. It owns reusable per-row scratch;
// a single goroutine drives it.
type rowConverter struct {
	schema *encoding.Schema
	// inferred tolerates an out-of-sample null in a non-nullable field
	// by promoting the field (see Run); otherwise such a null fails the
	// row.
	inferred bool
	dicts    map[int]*encoding.Dictionary
	delimFor func(string) string

	// Narrow types share vals; wide types (decimal128, set_u128,
	// set_u256) write raw bytes via wide, sized for the widest of them
	// and sliced down to the field's own ByteSize on write.
	vals     []uint64
	wide     []wideFieldBytes
	wideUsed []bool
	null     []bool
	// promoted[i]: field i was widened to nullable by an out-of-sample
	// null during this pass.
	promoted []bool

	// widenable[i]: field i's type is inference-originated and may be
	// promoted to a wider type when a value outgrows it (see
	// import_widen.go). widened records every rung step taken, in pass
	// order; pending holds the steps the caller has not yet applied to
	// the rows it already holds (takePending).
	widenable []bool
	widened   []widening
	pending   []widening
}

func newRowConverter(schema *encoding.Schema, inferred bool, dicts map[int]*encoding.Dictionary, delimFor func(string) string, widenable []bool) *rowConverter {
	n := len(schema.Fields)
	if widenable == nil {
		widenable = make([]bool, n)
	}
	return &rowConverter{
		schema:    schema,
		inferred:  inferred,
		dicts:     dicts,
		delimFor:  delimFor,
		vals:      make([]uint64, n),
		wide:      make([]wideFieldBytes, n),
		wideUsed:  make([]bool, n),
		null:      make([]bool, n),
		promoted:  make([]bool, n),
		widenable: widenable,
	}
}

// takePending returns the rung steps taken since the last call, in
// order, and forgets them. A caller holding converted rows re-strides
// them for each step (widenBufferedColumn) before writing the next row.
func (c *rowConverter) takePending() []widening {
	p := c.pending
	c.pending = nil
	return p
}

// convert converts row (1-based source data row rowNum) into the
// scratch slots. A nil return means the row imports; a non-nil
// RowError means it is skipped, exactly as Run skips it. An inferred
// schema promotes a non-nullable field on a null cell (mutating
// schema.Fields[i].Nullable and recording promoted[i]).
func (c *rowConverter) convert(rowNum int, row []string, declaredNulls []bool) *RowError {
	for i := range c.wideUsed {
		c.wideUsed[i] = false
		c.null[i] = false
	}
	rowErr := func(f encoding.Field, msg string) *RowError {
		return &RowError{
			Row: rowNum,
			Err: errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_ROW_ERROR,
				fmt.Sprintf("row %d, column %q: %s", rowNum, f.Name, msg),
				map[string]any{"row": rowNum, "column": f.Name},
			),
		}
	}
	for i, f := range c.schema.Fields {
		colIdx := f.CsvColumnIdx
		var raw string
		if colIdx < len(row) {
			raw = strings.TrimSpace(row[colIdx])
		}

		if isNullCell(raw, f.Type, declaredNulls, colIdx) {
			if !f.Nullable {
				if !c.inferred {
					// Explicit schema declared this field non-nullable —
					// a null here is a contract violation, not a guess.
					return rowErr(f, "null value in non-nullable field")
				}
				// Inferred schema: the bounded sample missed this null.
				// Promote the field to nullable and carry on. The field
				// stride is unchanged and every record already reserves a
				// bitmap byte for index i (Run writes a bitmap for every
				// inferred import), so prior records — whose bit i is 0
				// because they were non-null — stay valid.
				c.schema.Fields[i].Nullable = true
				c.promoted[i] = true
			}
			c.null[i] = true
			if isWideFieldType(f.Type) {
				// Null rides the per-record bitmap; the payload is a
				// zeroed placeholder of the field's FULL width. For a
				// set that zero is also the empty mask, which is only
				// reachable as a VALUE on a non-null cell.
				c.wide[i] = wideFieldBytes{}
				if f.Type == encoding.FieldTypeDecimal128 {
					enc := encoding.EncodeDecimal128(encoding.ZeroDecimal128())
					copy(c.wide[i][:], enc[:])
				}
				c.wideUsed[i] = true
			} else {
				c.vals[i] = 0
			}
			continue
		}

		if isWideFieldType(f.Type) {
			wb, err := convertValueWide(raw, f, c.dicts[i], c.delimFor(f.Name))
			if err != nil {
				return rowErr(f, err.Error())
			}
			c.wide[i] = wb
			c.wideUsed[i] = true
			continue
		}

		v, err := convertValue(raw, f.Type, c.dicts[i], c.delimFor(f.Name))
		if err != nil && c.widenable[i] {
			// A sample-inferred width this value outgrows is promoted,
			// not refused. The step sticks even if a later column fails
			// this row: a categorical's dictionary already holds more
			// entries than the old rung addresses, and Predict's
			// measured pass takes the same steps in the same order.
			for err != nil {
				to, ok := widenTarget(f.Type, raw, c.dicts[i])
				if !ok {
					break
				}
				step := widening{field: i, from: f.Type, to: to, row: rowNum}
				c.widened = append(c.widened, step)
				c.pending = append(c.pending, step)
				c.schema.Fields[i].Type = to
				f = c.schema.Fields[i]
				v, err = convertValue(raw, f.Type, c.dicts[i], c.delimFor(f.Name))
			}
		}
		if err != nil {
			return rowErr(f, err.Error())
		}
		c.vals[i] = v
	}
	return nil
}

// writeFields appends the converted row's field bytes — every field in
// schema order, bit-packed types as one whole byte — to buf.
func (c *rowConverter) writeFields(buf *bytes.Buffer) error {
	for i, f := range c.schema.Fields {
		if c.wideUsed[i] {
			// Slice to the field's own width: 16 for decimal128 and
			// set_u128, 32 for set_u256. Writing the whole scratch
			// array would pad every decimal by 16 zero bytes and
			// desynchronize the stride for the rest of the file.
			if _, err := buf.Write(c.wide[i][:f.Type.ByteSize()]); err != nil {
				return err
			}
			continue
		}
		if f.Type.IsBitPacked() {
			// Bit-packed types: write as single byte for simplicity.
			buf.WriteByte(byte(c.vals[i]))
			continue
		}
		if err := encoding.WriteFieldValue(buf, f.Type, c.vals[i]); err != nil {
			return err
		}
	}
	return nil
}

// fillBitmap sets bit i of bm (a zeroed full-width ceil(field_count/8)
// bitmap) for every null cell of the converted row. A cell is only ever
// null-marked for a nullable field (an explicit non-nullable null fails
// the row), so this is safe.
func (c *rowConverter) fillBitmap(bm []byte) {
	for i := range c.null {
		if c.null[i] {
			encoding.BitmapSetNull(bm, i)
		}
	}
}

// promotedNames lists the promoted fields in schema order.
func (c *rowConverter) promotedNames() []string {
	var out []string
	for i := range c.schema.Fields {
		if c.promoted[i] {
			out = append(out, c.schema.Fields[i].Name)
		}
	}
	return out
}
