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
			"v1: exactly one JoinSpec per Request — PULSE_JOIN_TOO_MANY otherwise.",
			"v1: inner join only — left/outer/anti return PULSE_JOIN_KIND_NOT_IMPLEMENTED.",
			"v1: no spill — the right (build) side is fully materialised in memory.",
			"v1: build side is always the spec.Right cohort; smaller-side detection arrives in a follow-up.",
			"Per-OnPair type compatibility: identical types match, categorical_* match each other, the unsigned-int + float + date family is interchangeable. decimal128 keys reject cross-type comparisons and compare on their exact 128-bit mantissa, never the float64 echo.",
			"set_* columns cannot be a join key at any rung — PULSE_JOIN_TYPE_MISMATCH with details.reason=\"set_key\", even when both sides carry the identical rung. A multi-select bitmask has no unambiguous equality value; use a FILTER_SET_* membership predicate. Carrying a set column THROUGH a join is unaffected.",
			"Joined records flow through the standard processor pipeline (filter / attribute / group / aggregator).",
			"A crosstab (Request.Crosstab) honours its one JoinSpec: rows, columns and cell resolve against the joined schema and every cell / margin / Components count is over joined rows. It always runs the buffered crosstab arm — the fused walk declines a joined request.",
		},
	}
}
