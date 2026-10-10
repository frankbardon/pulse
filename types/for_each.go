package types

// ForEach fans ONE window, feature or attribute item out over parameter
// axes: before validation the item is expanded IN PLACE into one
// ordinary item per axis combination — the Cartesian product of the
// axes (Mode grid, the default; first axis slowest) or the axes walked
// in lockstep (Mode zip) — each rendered from the item's own body with
// the compose-sweep substitution: `{{axis}}` inside any string (a
// `label`, a `field`), `{"$var": "axis"}` as a whole JSON value where the
// item takes opaque JSON (`params`), type-preserving. Each rendered item
// is decoded strictly. Items without ForEach are untouched, so a list
// may mix plain and fanned-out items; the expanded list keeps declared
// order.
//
// It rides as the optional `for_each` key on types.Window,
// types.Feature and types.Attribute, gated by capability:for_each. A
// nil ForEach is absent: the key is `omitempty`, so a for_each-free
// request is byte-identical on the wire and under CanonicalHash.
type ForEach struct {
	// Axes are the fanned-out parameters, at least one: unique
	// identifier names, each with a non-empty list of scalar values —
	// the compose sweep's axis shape (SweepAxis).
	Axes []SweepAxis `json:"axes"`

	// Mode is how the axes combine (SweepMode*). Empty is grid.
	Mode SweepMode `json:"mode,omitempty"`
}
