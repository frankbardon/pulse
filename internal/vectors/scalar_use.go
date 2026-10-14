package vectors

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Vector fields in scalar slots.
//
// A native vec_f32 / vec_f64 field carries dim values per row, so a slot
// that reads ONE value — a scalar aggregation, grouper, attribute,
// feature, filter, test or regression input, a window field / order_by
// / partition_by, a sort key, a weight, a date / zone slot or a
// point-lookup key — has nothing to read. Without a refusal the slot
// would see every row null (Record.NumericValue is false for a vector)
// and answer with a plausible empty or zero figure. Every such site
// refuses with FieldNotScalar, the one PULSE_VECTOR_FIELD_NOT_SCALAR
// constructor, so predict and the runtime return the same code,
// message and details.

// Suggestions FieldNotScalar names in details.suggest: the vector
// operator that replaces a scalar aggregation or grouper slot.
const (
	SuggestAggregation = "AGG_VEC_MEAN"
	SuggestGrouper     = "GROUP_KMEANS"
)

// VectorField returns the schema field name names when it is a native
// vector field, with its dim; ok is false for any other (or unknown)
// name and for a nil schema.
func VectorField(schema *encoding.Schema, name string) (f *encoding.Field, dim int, ok bool) {
	if schema == nil || name == "" || !schema.HasVectors() {
		return nil, 0, false
	}
	for i := range schema.Fields {
		if schema.Fields[i].Name == name {
			if !schema.Fields[i].Type.IsVector() {
				return nil, 0, false
			}
			return &schema.Fields[i], schema.VectorDim(i), true
		}
	}
	return nil, 0, false
}

// FieldNotScalar is the PULSE_VECTOR_FIELD_NOT_SCALAR refusal of vector
// field f (dim dim) read as a single value at slot (a request path such
// as "aggregations[0] (AGG_SUM) field"). suggest, when non-empty, names
// the vector operator replacing the slot (SuggestAggregation,
// SuggestGrouper). extra details are merged in.
func FieldNotScalar(f *encoding.Field, dim int, slot, suggest string, extra map[string]any) *errors.CodedError {
	msg := slot + ": field " + strconv.Quote(f.Name) + " is a " + f.Type.String() + " vector field (dim " + strconv.Itoa(dim) +
		") and has no single value to read"
	switch suggest {
	case SuggestAggregation:
		msg += "; aggregate the whole vector with " + vectorAggregationList(", ", " or ") + " naming it under `vector`"
	case SuggestGrouper:
		msg += "; group by the vector with GROUP_KMEANS naming it under `vector`"
	default:
		msg += "; name it under `vector` in a vector slot (MAT_*, a vector test, attribute, grouper or aggregator), or read one element as `" +
			ElementMember(f.Name, 0) + "` inside a virtual vector"
	}
	details := map[string]any{
		"field":      f.Name,
		"field_type": f.Type.String(),
		"dim":        dim,
		"slot":       slot,
	}
	if suggest != "" {
		details["suggest"] = suggest
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_FIELD_NOT_SCALAR, msg, details)
}

// ScalarRefusal is FieldNotScalar for name when it is a vector field of
// schema, else nil.
func ScalarRefusal(schema *encoding.Schema, name, slot, suggest string, extra map[string]any) *errors.CodedError {
	f, dim, ok := VectorField(schema, name)
	if !ok {
		return nil
	}
	return FieldNotScalar(f, dim, slot, suggest, extra)
}
