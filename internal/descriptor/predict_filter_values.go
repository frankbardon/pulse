package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/valuematch"
	"github.com/frankbardon/pulse/types"
)

// validateFilterValues reports the FILTER_INCLUDE / FILTER_EXCLUDE
// values refusal the runtime constructor raises — valuematch.FilterKeys,
// the one rule both sides call: a label the categorical field's
// dictionary does not hold, or a value that does not parse as a number
// on any other field type. The dictionary is the schema block's full
// one (never inspect's truncated view; on a shard archive the canonical
// schema's, the union of every shard's), so no record is read.
//
// Only a field the schema carries is judged: an unknown name is the
// field-reference rule's, and a set / vector / table-refused type is
// refused before the constructor reads a value (rejectSetFieldForNumericFilter
// and the accepted-types table), so its values are never judged.
func validateFilterValues(env *descriptor.Envelope, filterers []*types.Filterer, schema *encoding.Schema, inst *InstanceSnapshot) {
	if schema == nil {
		return
	}
	for _, f := range filterers {
		if f == nil || f.Field == "" {
			continue
		}
		route := opRoute(inst, f.Type)
		if route != types.FILTER_INCLUDE && route != types.FILTER_EXCLUDE {
			continue
		}
		field := schema.Field(f.Field)
		if field == nil || field.Type.IsSet() || field.Type.IsVector() {
			continue
		}
		if field.Type.IsCategorical() && (field.Dictionary == nil || field.Dictionary.Count() == 0) {
			// A chain stage's synthesised categorical column carries an
			// empty dictionary the runtime fills as the previous stage's
			// rows materialise (vectors.ChainStageSchema), so its labels
			// are unknowable here; leave them to the runtime.
			continue
		}
		if fieldaccept.Refusal(string(route), fieldaccept.SlotField, f.Field, field.Type) != nil {
			continue
		}
		if _, err := valuematch.FilterKeys(route, f.Field, field, f.Values); err != nil {
			addCodedError(env, err)
		}
	}
}
