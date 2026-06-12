package types

// ComposeOverlaySpec is the request-side definition of one Compose-only
// overlay layer attached to a ComposedRequest. Each spec produces one
// OverlayLayer in ComposedResponse.Overlays in matching index order. The
// shape mirrors OverlaySpec (Name / Kind / Scope / Level / Within /
// Params) but swaps the per-result OverlayRef discriminated union for a
// pair of caller-supplied slot-label strings — Reference (the baseline
// slot to compare against) and Targets (one or more slot labels whose
// results the comparison surface decorates). The slot labels resolve
// against the parent ComposedRequest's per-Request Label field; the
// E7-S1 auto-default rule means an empty caller-supplied Label
// resolves to `request_<index+1>` (1-based) before reference lookup, so
// every slot has a stable name to address.
//
// File scope (E7-S2): the struct stub lands here with the exact shape
// E7-S3 expects (see the E7-S3 acceptance list) so the canonical-hash
// walker can fold ComposedRequest.Overlays at this story without
// re-doing the walk at S3. E7-S3 polishes the JSON round-trip surface
// and adds the user-facing validation tests; subsequent E7 stories layer
// the Compose-only overlay catalog (E7-S4 through E7-S14) and the
// per-kind handlers.
//
// Field-order matters for the canonical hash from E7-S2 — keep Name,
// Kind, Scope, Reference, Targets, Level, Within, Params in that order
// across the Go struct and the JSON envelope. The canonical-hash walker
// is data-driven over json.Marshal output so re-ordering would not
// break the hash, but the field order is the documented contract for
// downstream parsers + visual diff stability.
type ComposeOverlaySpec struct {
	// Name is the renderer-facing label for this overlay. When empty,
	// the processing layer synthesises a deterministic default keyed
	// by Kind + Reference + Targets.
	Name string `json:"name,omitempty"`

	// Kind selects the Compose-only overlay catalog entry to execute.
	// Compose-only kinds (E7 catalog) consume Reference / Targets
	// instead of the in-Request OverlayRef discriminated union; the
	// universal OverlayKind enum still namespaces every kind.
	Kind OverlayKind `json:"kind"`

	// Scope declares where the overlay lands relative to the target
	// slot's result. Reuses the universal OverlayScope enum so
	// downstream renderers handle Compose layers and per-Request
	// layers with the same scope machinery.
	Scope OverlayScope `json:"scope,omitempty"`

	// Reference names the baseline slot to compare against. Resolves
	// against the parent ComposedRequest's per-Request Label field
	// (after the E7-S1 auto-default has filled empty Labels with
	// `request_<index+1>`). Non-empty is the structural requirement;
	// per-kind validation (E7-S3 onward) rejects references that do
	// not resolve to a known slot label with PULSE_OVERLAY_REF_UNKNOWN.
	Reference string `json:"reference"`

	// Targets names the slot label(s) whose result(s) the comparison
	// surface decorates. Length >= 1 for every kind today; multi-ref
	// kinds (E7-S15+) consume the full slice, single-target kinds
	// consume Targets[0]. Order is significant — the canonical-hash
	// walker preserves slice order via the data-driven JSON walk.
	Targets []string `json:"targets,omitempty"`

	// Level truncates the same axis the overlay scopes to a
	// parent-grouper prefix at the configured depth (mirrors the
	// per-Request OverlaySpec.Level semantics; see types/overlay.go
	// for the buffered-crosstab NormalizeLevel mapping). Default zero
	// is `omitempty` so the wire shape stays byte-identical to the
	// pre-Level handler output. Honoured only by matrix-shape Compose
	// kinds; non-matrix Compose kinds ignore the slot.
	Level int `json:"level,omitempty"`

	// Within fixes a prefix of the OPPOSITE axis at the configured
	// depth (mirrors the per-Request OverlaySpec.Within semantics).
	// Default zero is `omitempty`. Honoured only by matrix-shape
	// Compose kinds; non-matrix Compose kinds ignore the slot.
	Within int `json:"within,omitempty"`

	// Params holds kind-specific configuration as a free-form map.
	// The per-kind schema lands alongside each kind's processor; v1
	// uses: Params["population"] for OVERLAY_RANK (E7-S9),
	// Params["scale"] for share-vs-index variants, etc. Omitted on
	// the wire when nil.
	Params map[string]any `json:"params,omitempty"`
}
