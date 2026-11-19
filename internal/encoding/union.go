package encoding

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// MergeUnionSchema folds incoming into canonical under the rules a shard
// archive applies to an arriving shard — the schema half only, so a
// request union (types.Request.Union) can be judged by predict (which
// reads no record) and executed by the runtime through one rule:
//
//  1. Both sides are read LOGICALLY (Schema.Logical): a parent-grouped
//     member merges as its ungrouped twin, as the archive merge flattens
//     a grouped arrival before reconciling it.
//  2. Set rungs are planned FIRST (PlanSetWidening) and both sides are
//     widened to the planned rung at the schema level
//     (WidenSchemaSetField) — the relaxation the archive applies before
//     its strict pass. A union past the widest rung is
//     PULSE_SHARD_DICT_WIDTH_OVERFLOW; a plan refused for any other
//     reason defers to the structural validator's diagnostic, as the
//     archive's reconcileFlat does.
//  3. Strict structural cohesion over the reconciled schemas
//     (ValidateStructuralCohesion): names, types, bit positions, vector
//     dims, categorical widths, the default zone field and the
//     one-directional nullability rule — every refusal
//     PULSE_SHARD_SCHEMA_MISMATCH with the archive's details.
//  4. Dictionaries union canonical-first (MergeDictUnion); a categorical
//     past its width is PULSE_SHARD_DICT_WIDTH_OVERFLOW.
//
// The returned schema is canonical's layout with the merged
// dictionaries and widened rungs; warnings are the description-class
// divergences the archive reports too. Neither input is modified.
func MergeUnionSchema(canonical, incoming *encoding.Schema) (*encoding.Schema, []CohesionWarning, error) {
	if canonical == nil || incoming == nil {
		return nil, nil, errors.NewCodedError(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"union merge requires non-nil schemas")
	}
	canon, inc := canonical.Logical(), incoming.Logical()
	plans, err := PlanSetWidening(canon, inc)
	if err != nil {
		if !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
			if _, cerr := ValidateStructuralCohesion(canon, inc); cerr != nil {
				return nil, nil, cerr
			}
		}
		return nil, nil, err
	}
	for _, p := range plans {
		if canon.Fields[p.FieldIndex].Type != p.To {
			if canon, err = WidenSchemaSetField(canon, p.Field, p.To); err != nil {
				return nil, nil, err
			}
		}
		if inc.Fields[p.FieldIndex].Type != p.To {
			if inc, err = WidenSchemaSetField(inc, p.Field, p.To); err != nil {
				return nil, nil, err
			}
		}
	}
	warnings, err := ValidateStructuralCohesion(canon, inc)
	if err != nil {
		return nil, warnings, err
	}
	merged, _, err := MergeDictUnion(canon, inc)
	if err != nil {
		return nil, warnings, err
	}
	return merged, warnings, nil
}
