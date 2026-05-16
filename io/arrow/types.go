package arrow

import (
	"fmt"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"

	"github.com/frankbardon/pulse/encoding"
)

// Pulse field metadata key carried as an Arrow Field.Metadata pair so a
// reader can recover the original Pulse type for fields that map to a
// generic Arrow type. Decimal128 carries its precision/scale natively in
// arrow.Decimal128Type and does not need an extension marker.
const (
	PulseTypeMetadataKey = "pulse:type"
)

// TypeToPulse maps an Arrow data type (with its nullability flag) to a Pulse
// FieldType. Lifted from the original io/parquet implementation so both the
// Arrow IPC reader and the Parquet reader share a single source of truth.
//
// Mapping rules:
//   - Signed and unsigned integers of the same width collapse to Pulse's
//     unsigned type at that width (Pulse has no signed integer types).
//   - Nullable variants are chosen for u8/u16 widths and for bool when the
//     Arrow field is marked nullable.
//   - Both Date32 and Date64 collapse to FieldTypeDate; Pulse stores dates as
//     days-since-epoch and discards the sub-day precision Date64 carries.
//   - String and binary (in both standard and large variants), and any
//     dictionary-encoded type, collapse to FieldTypeCategoricalU8. The import
//     pipeline upgrades to wider categorical widths when the dictionary
//     overflows.
//   - Anything unrecognized falls back to FieldTypeF64. This is a deliberate
//     conservative default: it lets unknown numeric Arrow types load without
//     error, at the cost of precision for unusual cases.
func TypeToPulse(dt arrow.DataType, nullable bool) encoding.FieldType {
	switch dt.ID() {
	case arrow.UINT8:
		if nullable {
			return encoding.FieldTypeNullableU8
		}
		return encoding.FieldTypeU8
	case arrow.UINT16:
		if nullable {
			return encoding.FieldTypeNullableU16
		}
		return encoding.FieldTypeU16
	case arrow.UINT32:
		return encoding.FieldTypeU32
	case arrow.UINT64:
		return encoding.FieldTypeU64
	case arrow.INT8:
		if nullable {
			return encoding.FieldTypeNullableU8
		}
		return encoding.FieldTypeU8
	case arrow.INT16:
		if nullable {
			return encoding.FieldTypeNullableU16
		}
		return encoding.FieldTypeU16
	case arrow.INT32:
		return encoding.FieldTypeU32
	case arrow.INT64:
		return encoding.FieldTypeU64
	case arrow.FLOAT32:
		return encoding.FieldTypeF32
	case arrow.FLOAT64:
		return encoding.FieldTypeF64
	case arrow.BOOL:
		if nullable {
			return encoding.FieldTypeNullableBool
		}
		return encoding.FieldTypePackedBool
	case arrow.DATE32:
		return encoding.FieldTypeDate
	case arrow.DATE64:
		return encoding.FieldTypeDate
	case arrow.STRING, arrow.LARGE_STRING, arrow.BINARY, arrow.LARGE_BINARY:
		return encoding.FieldTypeCategoricalU8
	case arrow.DICTIONARY:
		// Dictionary-encoded -> categorical.
		return encoding.FieldTypeCategoricalU8
	case arrow.DECIMAL128:
		if nullable {
			return encoding.FieldTypeNullableDecimal128
		}
		return encoding.FieldTypeDecimal128
	default:
		// Default to f64 for unknown types.
		return encoding.FieldTypeF64
	}
}

// TypeFromPulse maps a Pulse FieldType to an Arrow DataType for use when
// constructing an Arrow schema for export. Lifted from the original
// io/parquet implementation.
//
// Mapping rules:
//   - Unsigned integer widths map to the matching Arrow primitive.
//   - Float widths map to the matching Arrow primitive.
//   - Date maps to Arrow Date32 (days-since-epoch), the more compact of the
//     two Arrow date types and the natural fit for Pulse's storage.
//   - All bool variants (packed and nullable) collapse to Arrow Boolean;
//     nullability is carried by the field's Nullable flag, not the data type.
//   - All categorical widths collapse to Arrow String. Writers that want
//     dictionary encoding configure that as a column-encoding hint at write
//     time, not as a data-type choice.
//   - Anything unrecognized falls back to Float64.
func TypeFromPulse(ft encoding.FieldType) arrow.DataType {
	switch ft {
	case encoding.FieldTypeU8:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeU16:
		return arrow.PrimitiveTypes.Uint16
	case encoding.FieldTypeU32:
		return arrow.PrimitiveTypes.Uint32
	case encoding.FieldTypeU64:
		return arrow.PrimitiveTypes.Uint64
	case encoding.FieldTypeF32:
		return arrow.PrimitiveTypes.Float32
	case encoding.FieldTypeF64:
		return arrow.PrimitiveTypes.Float64
	case encoding.FieldTypeDate:
		return arrow.FixedWidthTypes.Date32
	case encoding.FieldTypePackedBool:
		return arrow.FixedWidthTypes.Boolean
	case encoding.FieldTypeNullableBool:
		return arrow.FixedWidthTypes.Boolean
	case encoding.FieldTypeNullableU4:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeNullableU8:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeNullableU16:
		return arrow.PrimitiveTypes.Uint16
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return arrow.BinaryTypes.String
	case encoding.FieldTypeDecimal128, encoding.FieldTypeNullableDecimal128:
		// Caller-resolved precision/scale; default 38/0 when not provided.
		return &arrow.Decimal128Type{Precision: int32(encoding.MaxDecimalPrecision), Scale: 0}
	default:
		return arrow.PrimitiveTypes.Float64
	}
}

// FieldFromPulse builds an Arrow Field for a Pulse schema entry, including
// per-type details (decimal128 precision/scale).
func FieldFromPulse(f encoding.Field) arrow.Field {
	nullable := f.Type == encoding.FieldTypeNullableDecimal128 ||
		f.Type == encoding.FieldTypeNullableBool ||
		f.Type == encoding.FieldTypeNullableU4 ||
		f.Type == encoding.FieldTypeNullableU8 ||
		f.Type == encoding.FieldTypeNullableU16
	switch f.Type {
	case encoding.FieldTypeDecimal128, encoding.FieldTypeNullableDecimal128:
		precision := int32(f.Precision)
		if precision == 0 {
			precision = int32(encoding.MaxDecimalPrecision)
		}
		return arrow.Field{
			Name:     f.Name,
			Type:     &arrow.Decimal128Type{Precision: precision, Scale: int32(f.Scale)},
			Nullable: nullable,
		}
	default:
		return arrow.Field{Name: f.Name, Type: TypeFromPulse(f.Type), Nullable: nullable}
	}
}

// PulseFieldFromArrow inverts FieldFromPulse for decimal128 columns.
// Returns (field, true) if a Pulse decimal type was identified;
// (zero, false) otherwise — the caller should fall back to TypeToPulse.
func PulseFieldFromArrow(af arrow.Field) (encoding.Field, bool) {
	if dt, ok := af.Type.(*arrow.Decimal128Type); ok {
		ft := encoding.FieldTypeDecimal128
		if af.Nullable {
			ft = encoding.FieldTypeNullableDecimal128
		}
		return encoding.Field{
			Name:      af.Name,
			Type:      ft,
			Precision: uint8(dt.Precision),
			Scale:     uint8(dt.Scale),
		}, true
	}
	return encoding.Field{}, false
}

// AppendDecimal128 appends a decimal value to an Arrow Decimal128
// builder, accepting either an encoding.Decimal128 typed value or a
// canonical decimal string at the field's scale. nil and "" append a
// null. Used by the Arrow IPC and Parquet writers.
func AppendDecimal128(b array.Builder, f encoding.Field, v any) error {
	bldr, ok := b.(*array.Decimal128Builder)
	if !ok {
		return fmt.Errorf("arrow: column expected Decimal128 builder, got %T", b)
	}
	switch x := v.(type) {
	case nil:
		bldr.AppendNull()
	case string:
		if x == "" {
			bldr.AppendNull()
			return nil
		}
		d, parsedScale, err := encoding.ParseDecimal128(x)
		if err != nil {
			return err
		}
		if parsedScale != f.Scale {
			d, err = d.Rescale(parsedScale, f.Scale)
			if err != nil {
				return err
			}
		}
		bldr.Append(decimal128FromPulse(d))
	case encoding.Decimal128:
		bldr.Append(decimal128FromPulse(x))
	default:
		return fmt.Errorf("arrow: decimal column expected encoding.Decimal128 or string, got %T", v)
	}
	return nil
}

// decimal128FromPulse converts a Pulse Decimal128 mantissa into the
// arrow/decimal128.Num expected by the Arrow Decimal128 builder. Both
// representations are 128-bit two's complement; we transit through the
// canonical 16-byte little-endian Pulse encoding.
func decimal128FromPulse(d encoding.Decimal128) decimal128.Num {
	le := encoding.EncodeDecimal128(d)
	// little-endian -> big-endian, then split into hi/lo uint64.
	var be [16]byte
	for i := 0; i < 16; i++ {
		be[i] = le[15-i]
	}
	var hi int64
	var lo uint64
	for i := 0; i < 8; i++ {
		hi = (hi << 8) | int64(be[i])
		lo = (lo << 8) | uint64(be[8+i])
	}
	return decimal128.New(hi, lo)
}

// FormatValue renders a single element of an Arrow array as a string suitable
// for emission through the pio.Reader interface. Lifted from the original
// io/parquet implementation. Null handling is the caller's responsibility:
// FormatValue assumes idx is a non-null position.
//
// Formatting rules:
//   - Integers: base-10, unsigned where possible.
//   - Float32 / Float64: shortest round-trippable decimal at the given
//     precision (strconv 'f', precision -1).
//   - Boolean: lowercase "true" / "false".
//   - String: the raw value verbatim.
//   - Date32: rendered as YYYY-MM-DD using days-since-epoch.
//   - Date64: rendered as YYYY-MM-DD; the sub-day milliseconds are dropped.
//   - Dictionary with a string value type: the resolved string label.
//   - Anything else: falls back to fmt-formatting GetOneForMarshal, which
//     yields the Arrow library's canonical text representation.
func FormatValue(arr arrow.Array, idx int) string {
	switch a := arr.(type) {
	case *array.Uint8:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint16:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint32:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint64:
		return strconv.FormatUint(a.Value(idx), 10)
	case *array.Int8:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int16:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int32:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int64:
		return strconv.FormatInt(a.Value(idx), 10)
	case *array.Float32:
		return strconv.FormatFloat(float64(a.Value(idx)), 'f', -1, 32)
	case *array.Float64:
		return strconv.FormatFloat(a.Value(idx), 'f', -1, 64)
	case *array.Boolean:
		if a.Value(idx) {
			return "true"
		}
		return "false"
	case *array.String:
		return a.Value(idx)
	case *array.Date32:
		days := int64(a.Value(idx))
		t := time.Unix(days*86400, 0).UTC()
		return t.Format("2006-01-02")
	case *array.Date64:
		ms := int64(a.Value(idx))
		t := time.Unix(ms/1000, (ms%1000)*1e6).UTC()
		return t.Format("2006-01-02")
	case *array.Dictionary:
		// Dictionary-encoded: resolve to the string value.
		dict := a.Dictionary()
		index := a.GetValueIndex(idx)
		if strDict, ok := dict.(*array.String); ok {
			return strDict.Value(index)
		}
		return fmt.Sprintf("%v", index)
	case *array.Decimal128:
		// Render at the array's declared scale.
		dt := a.DataType().(*arrow.Decimal128Type)
		v := a.Value(idx)
		// Reconstruct the canonical decimal string at the declared scale
		// using Pulse's encoding to avoid round-tripping through float64.
		return decimal128ArrayValueToString(v, uint8(dt.Scale))
	default:
		// Fallback: use the String representation from the array.
		return fmt.Sprintf("%v", a.GetOneForMarshal(idx))
	}
}

// decimal128ArrayValueToString renders an arrow.decimal128 value at the
// given scale using Pulse's encoding. Avoids float64 round-tripping that
// would lose precision.
func decimal128ArrayValueToString(v decimal128.Num, scale uint8) string {
	hi := v.HighBits()
	lo := v.LowBits()
	var be [16]byte
	for i := 0; i < 8; i++ {
		be[i] = byte(uint64(hi) >> (56 - 8*i))
		be[8+i] = byte(lo >> (56 - 8*i))
	}
	var le [16]byte
	for i := 0; i < 16; i++ {
		le[i] = be[15-i]
	}
	d, _ := encoding.DecodeDecimal128(le)
	return d.String(scale)
}
