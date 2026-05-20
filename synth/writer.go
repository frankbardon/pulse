package synth

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand/v2"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// fieldTypeFromName maps the spec's string type name to a FieldType.
// The mapping mirrors encoding.FieldType.String() — keep them in sync.
func fieldTypeFromName(name string) (encoding.FieldType, bool) {
	switch name {
	case "u4":
		return encoding.FieldTypeU4, true
	case "u8":
		return encoding.FieldTypeU8, true
	case "u16":
		return encoding.FieldTypeU16, true
	case "u32":
		return encoding.FieldTypeU32, true
	case "u64":
		return encoding.FieldTypeU64, true
	case "f32":
		return encoding.FieldTypeF32, true
	case "f64":
		return encoding.FieldTypeF64, true
	case "date":
		return encoding.FieldTypeDate, true
	case "packed_bool":
		return encoding.FieldTypePackedBool, true
	case "categorical_u8":
		return encoding.FieldTypeCategoricalU8, true
	case "categorical_u16":
		return encoding.FieldTypeCategoricalU16, true
	case "categorical_u32":
		return encoding.FieldTypeCategoricalU32, true
	case "decimal128":
		return encoding.FieldTypeDecimal128, true
	}
	return 0, false
}

// fieldOf builds an encoding.Field for a FieldSpec. Computes byte/bit
// offsets via cursor state in buildSchema.
type writerField struct {
	spec    FieldSpec
	field   *encoding.Field
	sampler sampler
	dict    *encoding.Dictionary
}

// buildSchema returns the encoding.Schema and per-field samplers laid
// out in record order. Byte offsets / bit positions are populated.
func buildSchema(s *Spec) (*encoding.Schema, []*writerField, error) {
	fields := make([]encoding.Field, len(s.Fields))
	wfs := make([]*writerField, len(s.Fields))

	byteOffset := 0
	bitCursor := 8 // bit offsets advance from the high bit of a fresh byte
	for i, fs := range s.Fields {
		ft, ok := fieldTypeFromName(fs.Type)
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("unknown field type %q for field %q", fs.Type, fs.Name),
				map[string]any{"type": fs.Type, "field": fs.Name})
		}
		field := encoding.Field{
			Name:         fs.Name,
			Type:         ft,
			Nullable:     fs.Nullable,
			Description:  fs.Description,
			CsvColumnIdx: i,
		}
		// Bit-packed fields each consume one byte in the importer's writer
		// pattern (see io/import.go: WriteByte path); replicate here so
		// bytes laid down match what RecordReader expects.
		if ft.IsCategorical() {
			field.Dictionary = encoding.NewDictionary()
		}
		if ft.IsDecimal() {
			prec := fs.Precision
			scale := fs.Scale
			if prec == 0 {
				prec = encoding.MaxDecimalPrecision
			}
			if scale > prec {
				return nil, nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
					fmt.Sprintf("field %q: scale > precision", fs.Name),
					map[string]any{"precision": prec, "scale": scale})
			}
			field.Precision = prec
			field.Scale = scale
		}
		field.ByteOffset = byteOffset
		// Bit-packed types use the bit position field; consume a fresh
		// byte for simplicity (matches io/import.go).
		if ft.IsBitPacked() {
			field.BitPosition = bitCursor % 8
			byteOffset += 1
			bitCursor = 8
		} else {
			byteOffset += ft.ByteSize()
		}

		smp, err := buildSampler(fs)
		if err != nil {
			return nil, nil, err
		}

		fields[i] = field
		wfs[i] = &writerField{spec: fs, field: &fields[i], sampler: smp}
	}

	schema := &encoding.Schema{Fields: fields}
	for i := range wfs {
		wfs[i].field = &schema.Fields[i]
		if schema.Fields[i].Dictionary != nil {
			wfs[i].dict = schema.Fields[i].Dictionary
		}
	}
	return schema, wfs, nil
}

// generate writes synthetic records to a bytes.Buffer, respecting any
// declared constraints via rejection sampling.
func generate(s *Spec, schema *encoding.Schema, wfs []*writerField, recordsBuf *bytes.Buffer, rng *mrand.Rand) (rowsGenerated, rowsRejected int, warnings []string, err error) {
	maxRej := s.MaxRejectionRate
	if maxRej == 0 {
		maxRej = 0.5
	}

	row := make(map[string]any, len(wfs))
	rowNullMask := make(map[string]bool, len(wfs))
	bitmapSize := schema.BitmapByteSize()

	cons, err := compileConstraints(s.Constraints, wfs)
	if err != nil {
		return 0, 0, nil, err
	}

	corr, err := buildCorrelator(s, wfs)
	if err != nil {
		return 0, 0, nil, err
	}

	for rowsGenerated < s.RowCount {
		if err := drawRow(rng, wfs, row, rowNullMask, corr); err != nil {
			return rowsGenerated, rowsRejected, warnings, err
		}
		ok, evalErr := cons.evaluate(row)
		if evalErr != nil {
			return rowsGenerated, rowsRejected, warnings, evalErr
		}
		if !ok {
			rowsRejected++
			total := rowsGenerated + rowsRejected
			// Enforce only after a sane sample so a few unlucky early
			// rejections don't trip the threshold.
			if total >= 64 && float64(rowsRejected)/float64(total) > maxRej {
				return rowsGenerated, rowsRejected, warnings,
					errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_CONSTRAINT_INFEASIBLE,
						"rejection rate exceeded threshold; relax constraint or change distribution",
						map[string]any{
							"rows_accepted": rowsGenerated,
							"rows_rejected": rowsRejected,
							"threshold":     maxRej,
						})
			}
			continue
		}
		if encErr := encodeRow(recordsBuf, wfs, row, rowNullMask, bitmapSize); encErr != nil {
			return rowsGenerated, rowsRejected, warnings, encErr
		}
		rowsGenerated++
	}
	return rowsGenerated, rowsRejected, warnings, nil
}

func drawRow(rng *mrand.Rand, wfs []*writerField, row map[string]any, nullMask map[string]bool, corr *correlator) error {
	for k := range row {
		delete(row, k)
	}
	for k := range nullMask {
		delete(nullMask, k)
	}
	for _, wf := range wfs {
		v, isNull := wf.sampler.next(rng)
		row[wf.spec.Name] = v
		if isNull {
			nullMask[wf.spec.Name] = true
		}
	}
	if corr != nil {
		corr.transform(rng, row)
	}
	return nil
}

func encodeRow(buf *bytes.Buffer, wfs []*writerField, row map[string]any, nullMask map[string]bool, bitmapSize int) error {
	for _, wf := range wfs {
		val := row[wf.spec.Name]
		isNull := nullMask[wf.spec.Name]
		if err := writeFieldValue(buf, wf, val, isNull); err != nil {
			return err
		}
	}
	if bitmapSize > 0 {
		bitmap := make([]byte, bitmapSize)
		for i, wf := range wfs {
			if !wf.field.Nullable {
				continue
			}
			if nullMask[wf.spec.Name] {
				encoding.BitmapSetNull(bitmap, i)
			}
		}
		if err := encoding.WriteBitmap(buf, bitmap); err != nil {
			return err
		}
	}
	return nil
}

func writeFieldValue(buf *bytes.Buffer, wf *writerField, val any, isNull bool) error {
	ft := wf.field.Type
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		if f < 0 {
			f = 0
		}
		if math.IsNaN(f) {
			f = 0
		}
		raw := uint64(math.Floor(f + 0.5))
		raw = clampUnsigned(raw, ft)
		return encoding.WriteFieldValue(buf, ft, raw)
	case encoding.FieldTypeF32:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat32(val)
		return encoding.WriteFieldValue(buf, ft, uint64(math.Float32bits(f)))
	case encoding.FieldTypeF64:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		return encoding.WriteFieldValue(buf, ft, math.Float64bits(f))
	case encoding.FieldTypeDate:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		f := toFloat64(val)
		days := uint32(int64(math.Floor(f + 0.5)))
		return encoding.WriteFieldValue(buf, ft, uint64(days))
	case encoding.FieldTypePackedBool:
		// Single byte holding the flag in bit 0. Matches reader's ReadBit.
		var b byte
		if !isNull && toBool(val) {
			b = 1
		}
		_, err := buf.Write([]byte{b})
		return err
	case encoding.FieldTypeU4:
		var v uint8
		if !isNull {
			f := toFloat64(val)
			if math.IsNaN(f) {
				f = 0
			}
			v = uint8(math.Floor(f+0.5)) & 0x0F
		}
		_, err := buf.Write([]byte{v})
		return err
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		if isNull {
			return encoding.WriteFieldValue(buf, ft, 0)
		}
		s, ok := val.(string)
		if !ok {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
				fmt.Sprintf("field %q: categorical sampler must return string", wf.spec.Name), nil)
		}
		id, err := wf.dict.AddWithLimit(s, ft.MaxCategoricalEntries())
		if err != nil {
			return err
		}
		return encoding.WriteFieldValue(buf, ft, uint64(id))
	case encoding.FieldTypeDecimal128:
		if isNull {
			return encoding.WriteDecimal128(buf, encoding.ZeroDecimal128())
		}
		dec, err := decimalFromValue(val, wf.field.Scale)
		if err != nil {
			return errors.WrapCodedError(err, errors.PULSE_DECIMAL_OVERFLOW,
				fmt.Sprintf("field %q: encoding decimal", wf.spec.Name))
		}
		return encoding.WriteDecimal128(buf, dec)
	}
	return errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
		fmt.Sprintf("field %q: cannot encode type %s", wf.spec.Name, ft), nil)
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		return 0
	}
	return 0
}

func toFloat32(v any) float32 {
	return float32(toFloat64(v))
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "true" || s == "1" || s == "yes" || s == "y" || s == "t"
	}
	return false
}

func clampUnsigned(v uint64, ft encoding.FieldType) uint64 {
	switch ft {
	case encoding.FieldTypeU8:
		if v > 0xFF {
			return 0xFF
		}
	case encoding.FieldTypeU16:
		if v > 0xFFFF {
			return 0xFFFF
		}
	case encoding.FieldTypeU32:
		if v > 0xFFFFFFFF {
			return 0xFFFFFFFF
		}
	}
	return v
}

func decimalFromValue(v any, scale uint8) (encoding.Decimal128, error) {
	switch x := v.(type) {
	case string:
		dec, srcScale, err := encoding.ParseDecimal128(x)
		if err != nil {
			return encoding.Decimal128{}, err
		}
		if srcScale != scale {
			return dec.Rescale(srcScale, scale)
		}
		return dec, nil
	case float64:
		return decimalFromFloat(x, scale)
	case int:
		return decimalFromFloat(float64(x), scale)
	case int64:
		return decimalFromFloat(float64(x), scale)
	}
	return encoding.Decimal128{}, errors.NewCodedError(errors.ENCODING_TYPE_MISMATCH,
		"unsupported value type for decimal128")
}

func decimalFromFloat(f float64, scale uint8) (encoding.Decimal128, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return encoding.Decimal128{}, errors.NewCodedError(errors.PULSE_DECIMAL_OVERFLOW,
			"cannot encode NaN/Inf as decimal128")
	}
	mult := math.Pow10(int(scale))
	scaled := f * mult
	if scaled < 0 {
		scaled = math.Ceil(scaled - 0.5)
	} else {
		scaled = math.Floor(scaled + 0.5)
	}
	bf := new(big.Float).SetFloat64(scaled)
	m := new(big.Int)
	bf.Int(m)
	return encoding.NewDecimal128FromBigInt(m)
}
