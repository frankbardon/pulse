package processing

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/types"
)

// constantAggregator is AGG_CONSTANT: one typed constant, read from the
// slot's params by internal/aggconst (the reading predict shares) and
// emitted unchanged in every row, group, crosstab cell and margin. It
// reads no column and keeps no state, so it streams, merges (a merge of
// two constants is the constant) and emits from the first chunk.
//
// The row value is the one the existing aggregators write for the type
// (aggconst package doc): a numeric-family constant rides the float64
// scalar (Rich returns nil, so the scalar path writes it, NaN — null on
// the wire — for `value: null`); decimal128 rides the decimal arm's
// result (a decimal string at the literal's scale); categorical_* the
// label; set_* the label list; vec_* the element array.
//
// Its params.expr is resolved before the factory runs (the request's
// scalar pre-resolution pass, ResolveExtensionScalars); the factory
// reads only the literal.
type constantAggregator struct {
	c aggconst.Constant
}

var (
	_ MergeableAggregator = (*constantAggregator)(nil)
	_ RichAggregator      = (*constantAggregator)(nil)
	_ MetaAggregator      = (*constantAggregator)(nil)
)

func newConstantAggregator(agg *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	if agg == nil {
		return &constantAggregator{c: aggconst.Constant{Type: encoding.FieldTypeF64, Null: true}}, nil
	}
	if r := aggconst.CheckSlot(agg); r != nil {
		return nil, r.Coded()
	}
	c, r := aggconst.Read(agg.Params)
	if r != nil {
		return nil, r.Coded()
	}
	return &constantAggregator{c: c}, nil
}

// scalar is the float64 the scalar path carries: the number of a
// numeric-family constant, the decimal's float echo, a set's member
// count; NaN for null and for a value with no number (a label, a
// vector).
func (a *constantAggregator) scalar() float64 {
	c := a.c
	switch {
	case c.Null:
		return math.NaN()
	case aggconst.Numeric(c.Type):
		return c.Number
	case c.Type.IsDecimal():
		return c.Decimal.Float64(c.Scale)
	case c.Type.IsSet():
		return float64(len(c.Labels))
	}
	return math.NaN()
}

func (a *constantAggregator) Aggregate([]*Record, string) (float64, error) { return a.scalar(), nil }

func (a *constantAggregator) UpdateRow(*Record, string) error { return nil }

func (a *constantAggregator) Finalize() (float64, error) { return a.scalar(), nil }

func (a *constantAggregator) MergeOnline(other OnlineAggregator) error {
	if _, ok := other.(*constantAggregator); !ok {
		return mergeTypeMismatch(string(types.AGG_CONSTANT))
	}
	return nil
}

// Rich is the typed value of a non-numeric constant; nil (the scalar
// stands) for a numeric-family or null one.
func (a *constantAggregator) Rich() (any, error) {
	c := a.c
	switch {
	case c.Null || aggconst.Numeric(c.Type):
		return nil, nil
	case c.Type.IsDecimal():
		return decimalAggResult{Value: c.Decimal, Scale: c.Scale, Precision: c.Precision}, nil
	case c.Type.IsCategorical():
		return c.Label, nil
	case c.Type.IsSet():
		return slices.Clone(c.Labels), nil
	case c.Type.IsVector():
		return slices.Clone(c.Vector), nil
	}
	return nil, nil
}

// Components: no operator-specific keys — the universal floor {n,
// n_null} is the whole payload.
func (a *constantAggregator) Components() (map[string]any, error) { return nil, nil }

// normalizeRefusedWhat names why a crosstab normalize refuses cell:
// a map-valued type, or an AGG_CONSTANT of a non-numeric type (no
// number to divide by a margin).
func normalizeRefusedWhat(cell *types.Aggregation) string {
	if aggconst.NonNumeric(cell) {
		return "non-numeric (AGG_CONSTANT params.type)"
	}
	return "map-valued"
}
