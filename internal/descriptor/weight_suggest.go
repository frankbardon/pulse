package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// SuggestWeight is the one rule inspect and predict share for the row
// weight a cohort's SPSS metadata sidecar proposes. variable is the
// sidecar's recorded weighting variable, read by the caller.
//
// It returns nil — silently, never a diagnostic — when variable is
// empty, the instance hides capability:weighting, the schema has no
// such column (a stale sidecar), or the column's type cannot carry a
// weight (IsWeightFieldType): a suggestion that would be refused if
// applied is not offered. Kind is always "probability", the request
// default; SPSS `WEIGHT BY` is frequency-like, which the documentation
// says wherever the suggestion appears. Nothing applies the result.
func SuggestWeight(schema *encoding.Schema, variable string, inst *InstanceSnapshot) *descriptor.SuggestedWeight {
	if variable == "" || schema == nil || !inst.Enabled(featWeighting) {
		return nil
	}
	f := schema.Field(variable)
	if f == nil || !IsWeightFieldType(f.Type) {
		return nil
	}
	return &descriptor.SuggestedWeight{
		Field:  variable,
		Source: descriptor.WeightSuggestionSourceSPSSSidecar,
		Kind:   string(types.WeightKindProbability),
	}
}

// anyWeightResolves reports whether some slot resolved a weight field
// (applied or skipped as not weight-aware): predict's suggestion echo
// is offered only when none did.
func anyWeightResolves(weights []descriptor.ResolvedWeight) bool {
	for _, w := range weights {
		if w.Field != "" {
			return true
		}
	}
	return false
}
