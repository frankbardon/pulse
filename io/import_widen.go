package io

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Full-pass width promotion for sample-inferred fields.
//
// Inference sizes a categorical_* field's rung and an integer field's
// width from the bounded sample (defaultSampleRows). A later value the
// sample never saw can outgrow that rung: the 257th distinct value of a
// categorical_u8, or 70000 in a column the sample saw only as u8.
// Refusing such a row drops real data for a guess the import made
// itself, so an INFERRED field is instead promoted to the narrowest
// rung that holds it and the row imports:
//
//	categorical_u8 → categorical_u16 → categorical_u32
//	u4 → u8 → u16 → u32 → u64   (a non-negative integer past the rung)
//	u4 | u8 | u16 | u32 → f64   (any other number: negative, fractional,
//	                             signed, past uint64)
//
// The f64 arm is lossless for every value already imported: each fits
// 32 bits, and f64 holds every integer up to 2^53 exactly. From u64 it
// would not be, so a u64 field never leaves the integer ladder and such
// a value stays a row error, as does anything that is not a number, and
// a categorical past categorical_u32.
//
// Only inference-originated fields promote. An authoritative schema
// (SchemaAwareReader), a user-authored ImportJob.Schema and every
// ColumnTypeOverrides column keep their declared type, and an overflow
// there stays the row error it has always been — a declared width is a
// contract, not a guess.
//
// Promotion never changes a value: dictionary IDs are assigned in the
// same order at any rung, integers zero-extend, and every
// u32-or-narrower integer is exact in f64. The rows already converted
// are re-strided in memory (widenBufferedColumn), so the written cohort
// is byte-identical to one imported with the final types from the
// start. A cohort that never overflows is byte-identical to before.
// Each promoted field carries one PULSE_IMPORT_WIDTH_PROMOTED warning.

// widening is one rung step taken by one field during the row pass.
type widening struct {
	field int
	from  encoding.FieldType
	to    encoding.FieldType
	row   int // 1-based source data row that forced the step
}

// widenableFields marks the fields the row pass may promote. Nothing
// is widenable unless the schema is inference-originated (inferred);
// then every field is, except the ColumnTypeOverrides columns, whose
// type the caller fixed. The type itself decides whether a widenable
// field has anywhere to go (widenTarget).
func widenableFields(schema *encoding.Schema, inferred bool, overrides map[string]encoding.FieldType) []bool {
	out := make([]bool, len(schema.Fields))
	if !inferred {
		return out
	}
	for i := range schema.Fields {
		_, forced := overrides[schema.Fields[i].Name]
		out[i] = !forced
	}
	return out
}

// intRungs is the unsigned integer ladder, narrowest first.
var intRungs = []encoding.FieldType{
	encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16,
	encoding.FieldTypeU32, encoding.FieldTypeU64,
}

// intRungMax is the largest value each rung of intRungs holds.
func intRungMax(ft encoding.FieldType) uint64 {
	switch ft {
	case encoding.FieldTypeU4:
		return 0x0F
	case encoding.FieldTypeU8:
		return math.MaxUint8
	case encoding.FieldTypeU16:
		return math.MaxUint16
	case encoding.FieldTypeU32:
		return math.MaxUint32
	}
	return math.MaxUint64
}

// widenTarget returns the type a widenable field of type ft must move to
// so that raw — a present cell that failed to convert at ft — converts,
// or ok=false when no promotion applies (the row error stands). dict is
// the field's dictionary (categorical only).
func widenTarget(ft encoding.FieldType, raw string, dict *encoding.Dictionary) (encoding.FieldType, bool) {
	switch ft {
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16:
		// Only a full dictionary is a width problem.
		if dict == nil || uint32(dict.Count()) < ft.MaxCategoricalEntries() {
			return 0, false
		}
		if ft == encoding.FieldTypeCategoricalU8 {
			return encoding.FieldTypeCategoricalU16, true
		}
		return encoding.FieldTypeCategoricalU32, true

	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32:
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			for _, r := range intRungs {
				if intRungMax(r) > intRungMax(ft) && v <= intRungMax(r) {
					return r, true
				}
			}
			return 0, false
		}
		if _, err := strconv.ParseFloat(raw, 64); err == nil {
			return encoding.FieldTypeF64, true
		}
	}
	return 0, false
}

// importCellWidth is the bytes one field occupies in an import row: its
// ByteSize, or one whole byte for a bit-packed type.
func importCellWidth(ft encoding.FieldType) int {
	if ft.IsBitPacked() {
		return 1
	}
	return ft.ByteSize()
}

// widenBufferedColumn re-strides rows import rows held in buf, laid out
// field by field per types (bit-packed = one byte), so field fi is
// stored as to instead of types[fi]. It works in place, last row first:
// a row's new position is never before its old one, so no row is
// overwritten before it has been moved. Values are preserved — integers
// and dictionary IDs zero-extend, and an integer bound for f64 is
// written as the exactly-equal float.
func widenBufferedColumn(buf *bytes.Buffer, rows int, types []encoding.FieldType, fi int, to encoding.FieldType) error {
	off, oldStride := 0, 0
	for i, ft := range types {
		if i == fi {
			off = oldStride
		}
		oldStride += importCellWidth(ft)
	}
	wOld, wNew := importCellWidth(types[fi]), importCellWidth(to)
	newStride := oldStride - wOld + wNew
	if rows == 0 || wNew == wOld && to != encoding.FieldTypeF64 {
		return nil
	}
	if buf.Len() != rows*oldStride {
		return fmt.Errorf("import width promotion: %d buffered bytes are not %d rows of %d", buf.Len(), rows, oldStride)
	}
	buf.Write(make([]byte, rows*(newStride-oldStride)))
	b := buf.Bytes()
	var cell [8]byte
	for k := rows - 1; k >= 0; k-- {
		src, dst := k*oldStride, k*newStride
		cell = [8]byte{}
		copy(cell[:wOld], b[src+off:src+off+wOld])
		v := binary.LittleEndian.Uint64(cell[:])
		copy(b[dst+off+wNew:dst+newStride], b[src+off+wOld:src+oldStride])
		copy(b[dst:dst+off], b[src:src+off])
		if to == encoding.FieldTypeF64 {
			v = math.Float64bits(float64(v))
		}
		binary.LittleEndian.PutUint64(cell[:], v)
		copy(b[dst+off:dst+off+wNew], cell[:wNew])
	}
	return nil
}

// relayoutOffsets recomputes every field's ByteOffset from the field
// types, exactly as inference lays them out, after a promotion changed a
// width.
func relayoutOffsets(schema *encoding.Schema) {
	off := 0
	for i := range schema.Fields {
		schema.Fields[i].ByteOffset = off
		off += importCellWidth(schema.Fields[i].Type)
	}
}

// widthWarnings folds the rung steps into one PULSE_IMPORT_WIDTH_PROMOTED
// warning per promoted field, in schema order: the inferred type, the
// final type, and the first source row that forced a step.
func widthWarnings(schema *encoding.Schema, steps []widening) []*errors.CodedError {
	if len(steps) == 0 {
		return nil
	}
	first := make(map[int]widening, len(steps))
	for _, s := range steps {
		if _, ok := first[s.field]; !ok {
			first[s.field] = s
		}
	}
	var out []*errors.CodedError
	for i := range schema.Fields {
		s, ok := first[i]
		if !ok {
			continue
		}
		f := schema.Fields[i]
		out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_WIDTH_PROMOTED,
			fmt.Sprintf("field %q promoted from %s to %s: source row %d holds a value the width inferred from the sample cannot", f.Name, s.from, f.Type, s.row),
			map[string]any{"field": f.Name, "from": s.from.String(), "to": f.Type.String(), "source_row": s.row}))
	}
	return out
}
