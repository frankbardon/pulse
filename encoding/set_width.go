package encoding

// SetWidthHeadroom reports how much of one set field's bitmask capacity
// the canonical dictionary has consumed, and which rung it would be
// widened to next.
//
// It exists so an impending archive-wide widen is FORESEEABLE. The
// widen itself is correct but expensive (every record of every shard is
// re-laid-out), and without this a caller learns the field was one entry
// from its ceiling only by paying for the rewrite.
type SetWidthHeadroom struct {
	Field string `json:"field"`
	// Type is the rung the field currently declares.
	Type string `json:"type"`
	// Used is the number of dictionary entries in the canonical schema.
	Used int `json:"used"`
	// Capacity is the rung's bitmask width.
	Capacity int `json:"capacity"`
	// Headroom is Capacity - Used: how many more distinct members the
	// archive can absorb before the next `shard add` widens it.
	Headroom int `json:"headroom"`
	// NextType is the rung a widen would promote to, or "" when the
	// field is already at the widest rung — at which point Headroom is
	// the hard remainder and an overflow is fatal, not widenable.
	NextType string `json:"next_type,omitempty"`
}
