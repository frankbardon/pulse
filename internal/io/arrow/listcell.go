package arrow

import (
	"math"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"

	"github.com/frankbardon/pulse/internal/iocore"
)

// ListCellAt reads row idx of arr as a typed numeric list
// (iocore.ListAwareReader): a FixedSizeList, List or LargeList whose
// child holds integers or floats. A null row is iocore.ListNull; a
// non-list column is iocore.ListNotList; a list whose child type is
// not numeric is iocore.ListNonNumeric at its first non-null element
// (an all-null list of any child type reads as a list of nulls). The
// elements come off the child array's typed values — the pipe-joined
// row text is never parsed back.
func ListCellAt(arr arrow.Array, idx int, dst *iocore.ListCell) {
	if arr.IsNull(idx) {
		dst.Reset(iocore.ListNull)
		return
	}
	var (
		child      arrow.Array
		start, end int
	)
	switch a := arr.(type) {
	case *array.FixedSizeList:
		s, e := a.ValueOffsets(idx)
		start, end, child = int(s), int(e), a.ListValues()
	case *array.List:
		s, e := a.ValueOffsets(idx)
		start, end, child = int(s), int(e), a.ListValues()
	case *array.LargeList:
		s, e := a.ValueOffsets(idx)
		start, end, child = int(s), int(e), a.ListValues()
	default:
		dst.Reset(iocore.ListNotList)
		dst.BadValue = FormatValue(arr, idx)
		return
	}
	dst.Reset(iocore.ListValues)
	for i := start; i < end; i++ {
		if child.IsNull(i) {
			dst.Append(math.NaN(), true)
			continue
		}
		v, ok := numericAt(child, i)
		if !ok {
			dst.State = iocore.ListNonNumeric
			dst.BadIndex = i - start
			dst.BadValue = FormatValue(child, i)
			return
		}
		dst.Append(v, false)
	}
}

// numericAt is element i of a numeric Arrow array as a float64.
func numericAt(a arrow.Array, i int) (float64, bool) {
	switch c := a.(type) {
	case *array.Float64:
		return c.Value(i), true
	case *array.Float32:
		return float64(c.Value(i)), true
	case *array.Float16:
		return float64(c.Value(i).Float32()), true
	case *array.Int8:
		return float64(c.Value(i)), true
	case *array.Int16:
		return float64(c.Value(i)), true
	case *array.Int32:
		return float64(c.Value(i)), true
	case *array.Int64:
		return float64(c.Value(i)), true
	case *array.Uint8:
		return float64(c.Value(i)), true
	case *array.Uint16:
		return float64(c.Value(i)), true
	case *array.Uint32:
		return float64(c.Value(i)), true
	case *array.Uint64:
		return float64(c.Value(i)), true
	}
	return 0, false
}
