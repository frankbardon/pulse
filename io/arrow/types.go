package arrow

import (
	"fmt"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"

	"github.com/frankbardon/pulse/encoding"
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
	default:
		return arrow.PrimitiveTypes.Float64
	}
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
	default:
		// Fallback: use the String representation from the array.
		return fmt.Sprintf("%v", a.GetOneForMarshal(idx))
	}
}
