package types

// Return is a response-shaping block: which parts of a Response the
// caller wants back, and how many significant digits a wire float
// carries. It rides as the optional `return` slot on Request.
//
// Resolution runs Preset, then Include adds, then Exclude removes —
// Exclude wins. Include without a Preset is an allowlist over an EMPTY
// base; Exclude or Precision alone shape the full response. Top-level
// `warnings` (and every nested `*.warnings` whose parent object is
// emitted) is kept unless a path explicitly excludes it.
//
// Paths are the JSON names of the Response as published by the payload
// schema, at any depth: `.` separates object keys, `[*]` steps into
// every element of an array, and a trailing `*` on a map-key segment is
// a prefix glob (`tests[*].details.effect_*`). A nil block is absent:
// the slot is `omitempty`, so a request without one is byte-identical on
// the wire and under CanonicalHash.
type Return struct {
	// Preset names a predefined selection (ReturnPreset*). Empty means
	// none: an Include list then starts from an empty base.
	Preset ReturnPreset `json:"preset,omitempty"`

	// Include adds the named paths (and everything beneath them).
	Include []string `json:"include,omitempty"`

	// Exclude removes the named paths (and everything beneath them).
	// An excluded path is absent on the wire, never null.
	Exclude []string `json:"exclude,omitempty"`

	// Precision is the number of significant digits every wire float
	// carries, 1–17. Zero means unlimited (the shortest round-trip
	// form). Wire-only: the Go struct keeps full float64 precision.
	Precision int `json:"precision,omitempty"`
}

// ReturnPreset names one predefined response selection.
type ReturnPreset string

const (
	// ReturnPresetFull selects the whole Response (identity).
	ReturnPresetFull ReturnPreset = "full"
	// ReturnPresetStandard selects the primary result of every slot plus
	// the figures most callers read beside it.
	ReturnPresetStandard ReturnPreset = "standard"
	// ReturnPresetMinimal selects the primary result of every slot.
	ReturnPresetMinimal ReturnPreset = "minimal"
)

// AllReturnPresets returns every preset name, in a stable order.
func AllReturnPresets() []ReturnPreset {
	return []ReturnPreset{ReturnPresetFull, ReturnPresetStandard, ReturnPresetMinimal}
}
