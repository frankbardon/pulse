package vectors

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Chain code columns — the one rule both sides of ProcessChain use to
// carry categorical-code-ness from a stage to the stages after it.
//
// A later chain stage reads the previous stage's Response.Data through
// a synthesised schema (descriptor synthChainSchema on predict,
// processing.ChainOutputSchema at runtime) that types every
// aggregation output column f64. An aggregation that writes a code
// (IsCode — AGG_MODE over a categorical_* / packed_bool field writes
// the dictionary index / truth value) therefore forwards a CODE in an
// f64 column: AGG_MODE over it at the next stage picks a code again,
// and an overlay scoring that is scoring codes as magnitudes. The
// synthesised schema cannot carry the fact (the column is a plain f64
// at runtime — retyping it categorical would re-dictionary its
// values), so it travels beside the schema as a code-column map: column
// name → the input field type the code is a code of.

// StageCodeColumns returns the output columns of the chain stage req
// that carry a categorical code, each mapped to the field type it is a
// code of: an aggregation k.IsCode over its input field's type as
// inputType reports it (nil: none), keyed by its output label
// (types.AggregationLabelOf, the column the stage writes). Nil when
// none does.
func (k AggregationKinds) StageCodeColumns(req *types.Request, inputType func(field string) (encoding.FieldType, bool)) map[string]encoding.FieldType {
	if req == nil || inputType == nil {
		return nil
	}
	var out map[string]encoding.FieldType
	for _, a := range req.Aggregations {
		if a == nil || a.Field == "" || !k.EmitsInputValue(a.Type) {
			continue
		}
		ft, ok := inputType(a.Field)
		if !ok || !k.IsCode(a.Type, ft) {
			continue
		}
		if out == nil {
			out = map[string]encoding.FieldType{}
		}
		out[types.AggregationLabelOf(a)] = ft
	}
	return out
}

// ChainCodeColumns returns the code columns of chain stage idx's INPUT:
// nil for stage 0 (it reads stage0, whose fields carry their own
// types); for idx >= 1 the columns stage idx-1 writes as a code, judged
// against that stage's own input (CodeFieldTypes of its schema and the
// code columns it inherited), so code-ness propagates through every
// stage. stages are the chain's requests as executed (smart defaults
// resolved), index-aligned; stage0 the schema stage 0 executes over
// (nil: no judgement); next the side's synthesised-schema builder for
// a stage's output over that stage's input schema (nil result: no
// judgement past it).
func (k AggregationKinds) ChainCodeColumns(stages []*types.Request, idx int, stage0 *encoding.Schema, next func(req *types.Request, in *encoding.Schema) *encoding.Schema) map[string]encoding.FieldType {
	if stage0 == nil || idx <= 0 || idx > len(stages) {
		return nil
	}
	schema := stage0
	var codes map[string]encoding.FieldType
	for s := 0; s < idx; s++ {
		if schema == nil || stages[s] == nil {
			return nil
		}
		codes = k.StageCodeColumns(stages[s], CodeFieldTypes(schema, codes))
		schema = next(stages[s], schema)
	}
	return codes
}

// CodeFieldTypes is the field-type lookup a code judgement reads over
// schema plus the code columns a chain stage inherited: a code column
// answers the type it is a code of, every other schema field its own
// type. Nil for a nil schema.
func CodeFieldTypes(schema *encoding.Schema, codes map[string]encoding.FieldType) func(field string) (encoding.FieldType, bool) {
	if schema == nil {
		return nil
	}
	return func(field string) (encoding.FieldType, bool) {
		f := schema.Field(field)
		if f == nil {
			return 0, false
		}
		if ft, ok := codes[field]; ok {
			return ft, true
		}
		return f.Type, true
	}
}
