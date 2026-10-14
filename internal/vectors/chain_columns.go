package vectors

import (
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ChainStageSchema is the one synthesised schema a later ProcessChain
// stage reads the stage req's output rows through — the runtime's
// processing.ChainOutputSchema and predict's synthChainSchema both
// build it here, so the next stage's field references, defaults and
// gate see the same columns on both sides. in is the schema req itself
// executes over (its INPUT: the cohort's or joined schema for stage 0,
// else the one synthesised from the stage before it); kinds is the
// instance's aggregation view.
//
// Layout, in order:
//   - One categorical_u32 field per Group entry, named by its key
//     column (GroupKeyField — the Data row key the stage writes), with
//     an empty Dictionary the runtime fills as rows materialise; a
//     repeated key is listed once.
//   - One f64 field per aggregation output column
//     (AggregationOutputColumns): the label, or — on an expanding
//     vector aggregation — one "<label>[<element label>]" column per
//     member in member order, the label itself absent (the row writer
//     writes no array). An element column is an ordinary nullable f64
//     column there: an element no row answered (NaN) is a null.
//
// A non-expanding array-valued or non-scalar aggregation never feeds a
// later stage (mergegate.StageArrayValuedRefusal), so its label is
// listed but never read.
//
// The error is the first aggregation column colliding with an earlier
// column (a group key or another aggregation's column):
// PROCESSING_CONFIG {column, aggregation_index, aggregation}. The
// schema is still complete (the colliding column listed once) so a
// caller that only reports — predict — keeps judging the next stage.
func ChainStageSchema(req *types.Request, in *encoding.Schema, kinds AggregationKinds) (*encoding.Schema, *errors.CodedError) {
	if req == nil {
		return &encoding.Schema{}, nil
	}
	fields := make([]encoding.Field, 0, len(req.Groups)+len(req.Aggregations))
	seen := make(map[string]struct{}, len(req.Groups)+len(req.Aggregations))
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		key := GroupKeyField(g.Type, g)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, encoding.Field{
			Name:       key,
			Type:       encoding.FieldTypeCategoricalU32,
			Dictionary: encoding.NewDictionary(),
		})
	}
	var first *errors.CodedError
	for i, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		cols, _ := AggregationOutputColumns(req, agg, in, kinds)
		for _, col := range cols {
			if _, dup := seen[col]; dup {
				if first == nil {
					first = errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
						"chain output column "+strconv.Quote(col)+" (aggregations["+strconv.Itoa(i)+"], "+string(agg.Type)+
							") collides with another column of the stage; give the aggregation a distinct label",
						map[string]any{"column": col, "aggregation_index": i, "aggregation": string(agg.Type)})
				}
				continue
			}
			seen[col] = struct{}{}
			fields = append(fields, encoding.Field{Name: col, Type: encoding.FieldTypeF64})
		}
	}
	return &encoding.Schema{Fields: fields}, first
}
