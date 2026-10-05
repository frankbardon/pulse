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
	// and sliced down to the field's own ByteSize on write. The cells
	// and their byte layout are shared with the cohort builder
	// (cell_encode.go).
	rowCells
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

	// forced[i]: field i's type was fixed by ColumnTypeOverrides. A
	// present value that does not convert at that type is the fatal
	// PULSE_IMPORT_OVERRIDE_INVALID, not a skipped row (see
	// import_override.go). Nil when no override is in force.
	forced []bool

	// zones is the pass's source-zone context (import_zone.go); nil
	// when no field reads in a source zone.
	zones *sourceZones
}

// force marks the fields overrides names (by field name) as forced.
func (c *rowConverter) force(overrides map[string]encoding.FieldType) {
	if len(overrides) == 0 {
		return
	}
	c.forced = make([]bool, len(c.schema.Fields))
	for i := range c.schema.Fields {
		_, c.forced[i] = overrides[c.schema.Fields[i].Name]
	}
}

// isForced reports whether field i carries a column type override.
func (c *rowConverter) isForced(i int) bool {
	return c.forced != nil && c.forced[i]
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
		rowCells:  newRowCells(n),
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
	c.reset()
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
			c.setNull(i, f.Type)
			continue
		}

		if c.isForced(i) && f.Type == encoding.FieldTypeDecimal128 {
			// The forced scale is the sample's largest; a wider value
			// would be silently rounded by the rescale below.
			if err := convertForced(raw, f, nil, ""); err != nil {
				return &RowError{Row: rowNum, Err: overrideRefusal(f.Name, f.Type, raw, rowNum, err)}
			}
		}
		if isWideFieldType(f.Type) {
			wb, err := convertValueWide(raw, f, c.dicts[i], c.delimFor(f.Name))
			if err != nil {
				if c.isForced(i) {
					return &RowError{Row: rowNum, Err: overrideRefusal(f.Name, f.Type, raw, rowNum, err)}
				}
				return rowErr(f, err.Error())
			}
			c.setWide(i, wb)
			continue
		}

		// A sample-inferred width this value outgrows is promoted, not
		// refused (convertOrWiden). The step sticks even if a later
		// column fails this row: a categorical's dictionary already
		// holds more entries than the old rung addresses, and Predict's
		// measured pass takes the same steps in the same order.
		v, steps, err := convertOrWiden(c.schema, i, raw, c.dicts[i], c.delimFor(f.Name), c.widenable[i], rowNum, c.zones)
		if len(steps) > 0 {
			c.widened = append(c.widened, steps...)
			c.pending = append(c.pending, steps...)
			f = c.schema.Fields[i]
		}
		if err != nil {
			if isDSTRefusal(err) {
				// Already names row, column, value and zone; fatal,
				// never a skipped row (the caller checks).
				return &RowError{Row: rowNum, Err: err}
			}
			if c.isForced(i) {
				return &RowError{Row: rowNum, Err: overrideRefusal(f.Name, f.Type, raw, rowNum, err)}
			}
			return rowErr(f, err.Error())
		}
		c.vals[i] = v
	}
	return nil
}

// writeFields appends the converted row's field bytes to buf (the
// shared row layout, rowCells.writeFields).
func (c *rowConverter) writeFields(buf *bytes.Buffer) error {
	return c.rowCells.writeFields(buf, c.schema)
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
