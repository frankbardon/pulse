package types

// JoinSpec describes a single hash-join leg attached to a Request.
// Each spec joins the request's primary cohort against a named right-
// side cohort path. The first cut implements inner join only;
// "left"/"outer"/"anti" kinds are reserved and return
// PULSE_JOIN_KIND_NOT_IMPLEMENTED.
//
// Cardinality: at most one JoinSpec per Request in v1. Multi-join
// chains will land when the orchestrator gains a per-join intermediate
// state machine.
type JoinSpec struct {
	// Right is the cohort path for the right side. Single-file `.pulse`,
	// shard archive, or `archive#shard.pulse` anchor — same dispatch
	// rules as the primary cohort path.
	Right string `json:"right"`

	// Kind selects the join semantic. "inner" (the only kind in v1)
	// emits one joined record per matched (left, right) pair. The
	// reserved kinds "left", "outer", "anti" will land once the null
	// bitmap correctness path is fully wired.
	Kind string `json:"kind,omitempty"`

	// On lists the equi-join key pairs. At least one entry is required.
	// Multiple entries are AND-ed (composite-key join). Field types
	// must match between left and right; mismatches surface
	// PULSE_JOIN_TYPE_MISMATCH.
	On []OnPair `json:"on"`

	// As is an optional prefix prepended to every right-side field's
	// name in the joined schema. Lets callers disambiguate columns
	// when both sides carry the same field name. Empty == no prefix.
	As string `json:"as,omitempty"`
}

// OnPair describes one equi-join key relationship: left.LeftField
// equals right.RightField.
type OnPair struct {
	LeftField  string `json:"left_field"`
	RightField string `json:"right_field"`
}
