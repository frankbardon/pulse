package descriptor

import "github.com/frankbardon/pulse/descriptor"

// joinCapability returns the canonical JoinCapability entry.
func joinCapability() descriptor.JoinCapability {
	return descriptor.JoinCapability{
		Name:               "hash_join",
		MaxJoinsPerRequest: 1,
		Kinds:              []string{"inner"},
		SpillBytes:         0,
		Limitations: []string{
			"At most one joins entry per request — PULSE_JOIN_TOO_MANY otherwise.",
			"Inner join only — any other kind is PULSE_JOIN_KIND_NOT_IMPLEMENTED.",
			"No spill — the right (build) side is held in memory in full.",
			"The build side is always the joins[].right cohort, whichever side is smaller.",
			"Key type compatibility per on[] pair: identical types match, categorical_* match each other, and the unsigned-integer + float + date family is interchangeable except u64 against f32 / f64 (PULSE_JOIN_TYPE_MISMATCH, details.reason \"u64_float_key\"). decimal128 matches only decimal128 and compares on its exact 128-bit mantissa, never the float64 echo.",
			"A set_* field cannot be a join key at any rung — PULSE_JOIN_TYPE_MISMATCH with details.reason \"set_key\", even when both sides carry the identical rung: a bitmask has no single equality value; use a FILTER_SET_* membership predicate. Carrying a set field THROUGH a join is unaffected.",
			"A vec_f32 / vec_f64 field cannot be a join key — PULSE_JOIN_TYPE_MISMATCH with details.reason \"vector_key\", even against an identical type and dim. Carrying a vector field THROUGH a join is unaffected.",
			"Joined records flow through the standard pipeline (filter / attribute / group / aggregate).",
			"A crosstab honours its request's join: rows, columns and cell resolve against the joined schema and every cell, margin and components count is over joined rows. It always runs the buffered crosstab arm.",
		},
	}
}
