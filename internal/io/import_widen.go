package io

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
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
//	f32 → f64                   (a number outside f32's range)
//	set_u8 → set_u16 → set_u32 → set_u64 → set_u128 → set_u256
//	                            (a cell whose tokens outgrow the rung)
//
// The f64 arm is lossless for every value already imported: each fits
// 32 bits, and f64 holds every integer up to 2^53 and every f32 value
// exactly. From u64 it would not be, so a u64 field never leaves the
// integer ladder and such a value stays a row error, as does anything
// that is not a number, a categorical past categorical_u32, a set past
// set_u256 (PULSE_IMPORT_SET_OVERFLOW, the reason of the row's
// PULSE_IMPORT_ROW_ERROR) and a packed_bool cell that is not a boolean (no wider type holds both a
// boolean and an arbitrary value losslessly).
//
// The f32 trigger is RANGE, the same test inference used to choose f32
// (f32Holds, shared with fitsF32): a magnitude past math.MaxFloat32, or
// a non-zero one below math.SmallestNonzeroFloat32 that f32 would flush
// to zero. A value inside the range that f32 merely rounds (0.1, or an
// integer past 2^24) does not promote — inference accepted exactly such
// values into the sample, and treating rounding as overflow would
// promote nearly every decimal column.
//
// Only inference-originated fields promote. An authoritative schema
// (SchemaAwareReader), a user-authored ImportJob.Schema, every
// ColumnTypeOverrides column and every source-declared Arrow / Parquet
// pin (DeclaredTypeReader) keep their declared type, and an overflow
// there stays the row error it has always been — a declared width is a
// contract, not a guess.
//
// Promotion never changes a value: dictionary IDs are assigned in the
// same order at any rung (a set's token -> bit assignment too),
// integers and set masks zero-extend — the low bytes of a wider set
// rung are byte-identical to the narrower rung's mask, set_u64 →
// set_u128 included (byte-layout.md, Wide-set word order) — and every
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
// type the caller fixed, and the source-declared pins (import_pins.go),
// whose type the source fixed. The type itself decides whether a
// widenable field has anywhere to go (widenTarget).
func widenableFields(schema *encoding.Schema, inferred bool, overrides map[string]encoding.FieldType, pins map[string]encoding.Field) []bool {
	out := make([]bool, len(schema.Fields))
	if !inferred {
		return out
	}
	for i := range schema.Fields {
		name := schema.Fields[i].Name
		_, forced := overrides[name]
		_, pinned := pins[name]
		out[i] = !forced && !pinned
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

	case encoding.FieldTypeF32:
		if _, err := strconv.ParseFloat(raw, 64); err == nil && !f32Holds(raw) {
			return encoding.FieldTypeF64, true
		}

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

// f32Holds reports whether raw, a number, lies inside f32's range: zero,
// or a magnitude in [math.SmallestNonzeroFloat32, math.MaxFloat32]. It
// is the per-value test fitsF32 applies to the inference sample, so a
// field is promoted off f32 by exactly the values that would have kept
// inference from choosing it.
func f32Holds(raw string) bool {
	f, _ := strconv.ParseFloat(raw, 64)
	// Phrased as the negation of "out of range" so a NaN — which
	// compares false to everything — holds, exactly as fitsF32 has
	// always let it through.
	return !(f != 0 && (math.Abs(f) < math.SmallestNonzeroFloat32 || math.Abs(f) > math.MaxFloat32))
}

// f32AtRangeEdge reports whether bits, an f32 convertValue produced, is
// one a value outside f32's range can round to: zero or the smallest
// subnormal (from an underflow), ±Inf or ±MaxFloat32 (from an overflow).
// Any other f32 came from an in-range value, so f32Holds — a second
// parse of the cell — only runs for these.
func f32AtRangeEdge(bits uint64) bool {
	r := math.Abs(float64(math.Float32frombits(uint32(bits))))
	return r == 0 || r == math.SmallestNonzeroFloat32 || r >= math.MaxFloat32
}

// convertOrWiden converts raw, a present cell of schema field i, at the
// field's type. When the value does not fit and the field is widenable
// it steps the field up its ladder (widenTarget) until the value
// converts or no step applies, writing each step to
// schema.Fields[i].Type and returning the steps in order; the error is
// the last conversion's. An f32 cell outside f32's range converts
// without error at f32 (to ±Inf or zero), so it is tested explicitly.
//
// This is the ONE promotion decision: the import row pass
// (rowConverter), the measured import predict and ConvertJob.Run all
// call it, so a field widens at the same row to the same type whichever
// verb reads the source.
//
// It is also the ONE source-zone decision: a datetime field sz zones
// (ImportJob.SourceTZ / ColumnSourceTZ) parses through sz.parse —
// temporal.ParseLocal under the job's DST policy — and every other cell,
// including every cell of a job without a source zone (sz nil), takes
// convertValue exactly as before. A datetime never widens, so the zoned
// arm returns before the ladder.
func convertOrWiden(schema *encoding.Schema, i int, raw string, dict *encoding.Dictionary, delim string, widenable bool, rowNum int, sz *sourceZones) (uint64, []widening, error) {
	if sz.zoned(i) {
		v, err := sz.parse(i, raw, schema.Fields[i].Name, rowNum)
		return v, nil, err
	}
	ft := schema.Fields[i].Type
	v, err := convertValue(raw, ft, dict, delim)
	if !widenable || err == nil && (ft != encoding.FieldTypeF32 || !f32AtRangeEdge(v) || f32Holds(raw)) {
		return v, nil, err
	}
	var steps []widening
	for {
		to, ok := widenTarget(ft, raw, dict)
		if !ok {
			return v, steps, err
		}
		steps = append(steps, widening{field: i, from: ft, to: to, row: rowNum})
		schema.Fields[i].Type = to
		ft = to
		if v, err = convertValue(raw, ft, dict, delim); err == nil {
			return v, steps, nil
		}
	}
}

// nextSetRung returns the set rung one step wider than ft on the shared
// ladder (encx.SetLadder), or ok=false for set_u256 and every non-set
// type.
func nextSetRung(ft encoding.FieldType) (encoding.FieldType, bool) {
	ladder := encx.SetLadder()
	for k := 0; k+1 < len(ladder); k++ {
		if ladder[k] == ft {
			return ladder[k+1], true
		}
	}
	return 0, false
}

// internSetOrWiden is convertOrWiden's set arm: it interns the tokens of
// raw, a present cell of set field i, and returns the membership mask.
// A set mask does not fit convertOrWiden's uint64 once the rung is wide,
// so the set ladder has its own entry point; the rule is the same. When
// the tokens outgrow the rung (PULSE_IMPORT_SET_OVERFLOW) and the field
// is widenable, the field steps up the set ladder one rung at a time,
// writing each step to schema.Fields[i].Type, until the cell interns or
// set_u256 still overflows — the row error that stands. Tokens interned
// before a failed attempt stay in the dictionary and keep their IDs, so
// the retry assigns every token the bit a full-sample import gives it.
//
// The import row pass (rowConverter), the measured import predict and
// ConvertJob.Run all call it, so a set field widens at the same row to
// the same rung whichever verb reads the source.
func internSetOrWiden(schema *encoding.Schema, i int, raw string, dict *encoding.Dictionary, delim string, widenable bool, rowNum int) (encoding.SetMask, []widening, error) {
	ft := schema.Fields[i].Type
	mask, err := setMaskFromCell(raw, ft, dict, delim)
	var steps []widening
	for widenable && isSetOverflow(err) {
		to, ok := nextSetRung(ft)
		if !ok {
			break
		}
		steps = append(steps, widening{field: i, from: ft, to: to, row: rowNum})
		schema.Fields[i].Type = to
		ft = to
		mask, err = setMaskFromCell(raw, ft, dict, delim)
	}
	return mask, steps, err
}

// isSetOverflow reports whether err is the set rung ceiling
// (internSetTokens' PULSE_IMPORT_SET_OVERFLOW).
func isSetOverflow(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == errors.PULSE_IMPORT_SET_OVERFLOW
}

// widenBufferedColumn re-strides rows import rows held in buf, laid out
// field by field per held (Schema.FieldWidth each, bit-packed = one
// byte, no bitmap), so field fi is stored as to instead of its held type.
// It returns the layout the rows now have — held with field fi retyped —
// for the next promotion to start from; held itself is not modified. It
// works in place, last row first: a row's new position is never before
// its old one, so no row is overwritten before it has been moved. Values
// are preserved — integers, dictionary IDs and set masks (to set_u256's
// 32 bytes) zero-extend, and an
// integer bound for f64 is written as the exactly-equal float, an f32 as
// the exactly-equal f64.
func widenBufferedColumn(buf *bytes.Buffer, rows int, held *encoding.Schema, fi int, to encoding.FieldType) (*encoding.Schema, error) {
	next := *held
	next.Fields = append([]encoding.Field(nil), held.Fields...)
	next.Fields[fi].Type = to
	from := held.Fields[fi].Type
	off, oldStride := 0, 0
	for i := range held.Fields {
		if i == fi {
			off = oldStride
		}
		oldStride += held.FieldWidth(i)
	}
	wOld, wNew := held.FieldWidth(fi), next.FieldWidth(fi)
	newStride := oldStride - wOld + wNew
	if rows == 0 || wNew == wOld && to != encoding.FieldTypeF64 {
		return &next, nil
	}
	if buf.Len() != rows*oldStride {
		return nil, fmt.Errorf("import width promotion: %d buffered bytes are not %d rows of %d", buf.Len(), rows, oldStride)
	}
	var cell [32]byte // set_u256, the widest promoted width
	if wNew > len(cell) || wOld > wNew {
		return nil, fmt.Errorf("import width promotion: %s (%d bytes) to %s (%d bytes) is not a widening", from, wOld, to, wNew)
	}
	buf.Write(make([]byte, rows*(newStride-oldStride)))
	b := buf.Bytes()
	for k := rows - 1; k >= 0; k-- {
		src, dst := k*oldStride, k*newStride
		cell = [32]byte{}
		copy(cell[:wOld], b[src+off:src+off+wOld])
		copy(b[dst+off+wNew:dst+newStride], b[src+off+wOld:src+oldStride])
		copy(b[dst:dst+off], b[src:src+off])
		if to == encoding.FieldTypeF64 {
			v := binary.LittleEndian.Uint64(cell[:8])
			if from == encoding.FieldTypeF32 {
				v = math.Float64bits(float64(math.Float32frombits(uint32(v))))
			} else {
				v = math.Float64bits(float64(v))
			}
			binary.LittleEndian.PutUint64(cell[:8], v)
		}
		copy(b[dst+off:dst+off+wNew], cell[:wNew])
	}
	return &next, nil
}

// relayoutOffsets recomputes every field's ByteOffset from the field
// types through the canonical layout (encx.LayoutFields), after a
// promotion changed a width or when a caller-authored schema's own
// offsets must not be trusted (the cohort builder).
func relayoutOffsets(schema *encoding.Schema) {
	encx.LayoutFields(schema)
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
