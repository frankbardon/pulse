package arrow

import (
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/iocore"
)

// VectorArrowField is the Arrow field of a native vector column
// (iocore.ListColumn): FixedSizeList<float32, dim> for vec_f32,
// FixedSizeList<float64, dim> for vec_f64. It is the type ListCellAt
// reads back as the same vector, so an export re-imports under
// `--vector NAME` element for element.
//
// fixed=false gives LIST<float32|float64> instead, for the one writer
// that needs it: Parquet (pqarrow) cannot write a null FixedSizeList
// between non-null ones — a null slot still owns dim child elements, and
// the encoder refuses a list column whose null entries have non-zero
// length — so a NULLABLE vector goes to Parquet as a plain list, whose
// null entry is empty. The import side reads either shape, and a list
// of constant length is still a vector of that dim.
func VectorArrowField(col iocore.ListColumn, fixed bool) arrow.Field {
	var elem arrow.DataType = arrow.PrimitiveTypes.Float64
	if col.Type == encoding.FieldTypeVecF32 {
		elem = arrow.PrimitiveTypes.Float32
	}
	var typ arrow.DataType = arrow.FixedSizeListOf(int32(col.Dim), elem)
	if !fixed {
		typ = arrow.ListOf(elem)
	}
	return arrow.Field{Name: col.Name, Type: typ, Nullable: col.Nullable}
}

// AppendVectorList appends one vector cell — a []float64 of exactly dim
// elements, or nil for a null vector — to a FixedSizeList or List
// builder built from VectorArrowField. A NaN or ±Inf element is stored
// as the value it is: Arrow floats carry it, and ListCellAt reads it
// back verbatim. A cell of any other shape is an error (the row is
// reported, never written short).
func AppendVectorList(b array.Builder, dim int, v any) error {
	type listBuilder interface {
		array.Builder
		ValueBuilder() array.Builder
	}
	var (
		lb  listBuilder
		app func()
	)
	switch x := b.(type) {
	case *array.FixedSizeListBuilder:
		lb, app = x, func() { x.Append(true) }
	case *array.ListBuilder:
		lb, app = x, func() { x.Append(true) }
	default:
		return fmt.Errorf("arrow: vector column is not a list builder (got %T)", b)
	}
	if v == nil {
		lb.AppendNull()
		return nil
	}
	vec, ok := v.([]float64)
	if !ok {
		return fmt.Errorf("arrow: vector cell is %T, want []float64", v)
	}
	if len(vec) != dim {
		return fmt.Errorf("arrow: vector cell has %d elements, the column's dim is %d", len(vec), dim)
	}
	app()
	switch eb := lb.ValueBuilder().(type) {
	case *array.Float32Builder:
		for _, e := range vec {
			eb.Append(float32(e))
		}
	case *array.Float64Builder:
		for _, e := range vec {
			eb.Append(e)
		}
	default:
		return fmt.Errorf("arrow: vector element builder is %T", eb)
	}
	return nil
}

// ListColumnIndex indexes an export's native list columns by header
// position.
func ListColumnIndex(cols []iocore.ListColumn) map[int]iocore.ListColumn {
	if len(cols) == 0 {
		return nil
	}
	out := make(map[int]iocore.ListColumn, len(cols))
	for _, c := range cols {
		out[c.Index] = c
	}
	return out
}
