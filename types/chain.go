package types

// ChainRequest bundles a source-rooted linear chain of Requests. Each
// stage after the first feeds off the previous stage's output rows
// rather than re-opening the cohort file. Mergeable-only at v1: see
// processing.CanChainRequest for the gate; non-mergeable stages
// surface PULSE_CHAIN_NOT_MERGEABLE with the offending index so the
// caller can fall back to per-stage Process calls.
//
// Cohort identifies the source for the FIRST stage. Stages after the
// first ignore their inner Request.Cohort; the chain executor wires
// the prior stage's output as the next stage's input automatically.
type ChainRequest struct {
	// Cohort identifies the source .pulse file (single file or shard
	// archive) for stage 0.
	Cohort *Cohort `json:"cohort"`

	// Stages is the ordered list of pipeline stages. Length must be
	// >= 1. Each stage's Request is run as a normal Process call
	// against the synthesized iterator from the prior stage (or
	// against Cohort for stage 0).
	Stages []*ChainStage `json:"stages"`

	// Overlays is the optional list of whole-chain overlay layer
	// specifications. Whole-chain overlays operate AFTER every stage
	// in Stages finishes (NOT between stages — per-stage overlays
	// ride the individual ChainStage.Request.Overlays slot) and emit
	// one ChainResponse.Overlays entry per spec in matching index
	// order. The dual-slot design (per-stage on Stages[i].Request,
	// whole-chain here) means callers can decorate any individual
	// stage's result OR the post-finalize chain result with the same
	// universal OverlayKind catalog; the StageRef discriminated
	// reference (see below) is what the whole-chain kinds
	// (OVERLAY_INDEX_VS_STAGE, OVERLAY_DELTA_VS_STAGE) consume to
	// identify which stage's result is the comparison baseline and
	// which stage's result is the comparison target.
	//
	// Forward-compat: a ChainRequest without whole-chain overlays
	// produces byte-identical JSON to the same request with
	// `Overlays: nil` because the slot is `omitempty` — nil / empty
	// slices marshal to no key at all. The canonical-hash routine
	// inherits that contract via the data-driven JSON walk in
	// types/hash.go (see TestChainCanonicalHash_OverlayFreeByteIdentity).
	Overlays []*ChainOverlaySpec `json:"overlays,omitempty"`
}

// ChainStage wraps a single Request with optional diagnostic name.
// The name is surfaced verbatim in ChainResponse.Stages and in
// PULSE_CHAIN_NOT_MERGEABLE error details when a stage rejects the
// chain gate.
type ChainStage struct {
	// Name is an optional diagnostic label (e.g. "filter_active",
	// "group_by_region"). Echoed in chain envelope.
	Name string `json:"name,omitempty"`

	// Request is the per-stage processing request. Cohort field is
	// ignored for stages > 0. Per-stage overlays ride
	// Request.Overlays — whole-chain overlays ride the parent
	// ChainRequest.Overlays slot. The dual-slot design is
	// intentional: per-stage overlays decorate an intermediate
	// result, whole-chain overlays decorate the post-finalize chain
	// result.
	Request *Request `json:"request"`
}

// ChainResponse is the result of a ChainRequest. Final is the last
// stage's response; Stages carries per-stage responses for callers
// that want to inspect intermediate outputs (matches Compose's
// per-request response shape).
type ChainResponse struct {
	// Stages holds per-stage responses in input order. The last
	// entry matches Final.
	Stages []*Response `json:"stages"`

	// Final aliases the last entry in Stages for ergonomic access.
	Final *Response `json:"final"`

	// NormalizedRequest is the chain request the engine actually
	// executed — each stage's Request reflects smart-defaults
	// resolution against its per-stage schema (the original cohort
	// for stage 0, the synthesised stage-output schemas for stages
	// 1+). Populated only when pulse.Options.EchoRequest is true at
	// service-construction time; nil otherwise so the wire size is
	// unchanged for callers that do not need it. Serialised under
	// the omitempty rule.
	NormalizedRequest *ChainRequest `json:"normalized_request,omitempty"`

	// Overlays carries the executed whole-chain overlay layers, one
	// entry per ChainRequest.Overlays spec in matching index order.
	// Reuses the OverlayLayer wrapper from types/overlay.go so
	// downstream renderers handle a per-stage layer and a
	// whole-chain layer with the same payload / summary machinery.
	//
	// Empty (or nil) when the request carried no whole-chain
	// overlays — the slot is `omitempty` so the wire form stays
	// byte-identical to the overlay-free ChainResponse for overlay-free
	// chains.
	Overlays []*OverlayLayer `json:"overlays,omitempty"`
}

// StageRef is the discriminated reference one ChainOverlaySpec uses
// to identify a specific stage in the parent ChainRequest.Stages
// slice. Exactly one of Index / Name is populated per StageRef value
// — the XOR is validated downstream by the chain-overlay validator
// and the per-kind runtime handler:
//
//   - Index is a pointer so the zero value (`Index: ptr(0)`,
//     meaningfully "stage 0") is distinguishable from "no index
//     supplied" (`Index: nil`). A literal `int` field would collapse
//     those two cases onto the same wire shape.
//   - Name matches against ChainStage.Name verbatim. Empty Name +
//     nil Index is rejected as malformed; both set is also rejected
//     (XOR is "exactly one").
//
// The StageRef family is the canonical entry point for whole-chain
// overlay reference resolution: the whole-chain kinds
// (OVERLAY_INDEX_VS_STAGE, OVERLAY_DELTA_VS_STAGE) populate both Ref
// (the baseline stage to compare against) and Target (the stage
// whose result the comparison surface decorates) with StageRef
// instances. OverlayRef.Stage in types/overlay.go references the
// same StageRef struct — there is exactly one StageRef declaration
// in the codebase.
type StageRef struct {
	// Index is the zero-based stage index into ChainRequest.Stages.
	// Pointer so `0` is meaningful — the canonical "stage 0" call
	// site sets Index = &zero, NOT Index unset.
	Index *int `json:"index,omitempty"`

	// Name matches against ChainStage.Name. Empty when Index is set.
	Name string `json:"name,omitempty"`
}

// ChainOverlaySpec is the request-side definition of one whole-chain
// overlay layer. Each spec produces one OverlayLayer in
// ChainResponse.Overlays in matching index order. The shape mirrors
// OverlaySpec (Name / Kind / Scope / Params) but swaps the
// per-result OverlayRef discriminated union for the StageRef pair
// (Ref baseline + Target) so the whole-chain kinds can address two
// distinct stage results inside the same ChainRequest without
// re-opening OverlayRef.
//
// Validation rules (enforced in descriptor + processing layers, not
// this file — land in descriptor/chain_overlay.go +
// processing/overlay_chain_dispatch.go):
//   - Kind is required and must be a known OverlayKind whose
//     whole-chain catalog entry exists (OVERLAY_INDEX_VS_STAGE,
//     OVERLAY_DELTA_VS_STAGE).
//   - Scope is required.
//   - Ref and Target each populate exactly one of (Index, Name)
//     (XOR validated by the per-kind validator).
//   - Name (when set) becomes the renderer-facing label; when empty
//     the processing layer synthesises a deterministic default
//     keyed by Kind + Ref + Target.
//   - Params carries kind-specific configuration; the per-kind
//     schema lives alongside the kind's processor.
type ChainOverlaySpec struct {
	// Name is the renderer-facing label for this overlay. When
	// empty, the processing layer synthesises a deterministic
	// default.
	Name string `json:"name,omitempty"`

	// Kind selects the whole-chain overlay catalog entry to
	// execute.
	Kind OverlayKind `json:"kind"`

	// Ref names the baseline stage to compare against. Exactly one
	// of Ref.Index / Ref.Name must be populated.
	Ref StageRef `json:"ref"`

	// Target names the stage whose result the comparison surface
	// decorates. Exactly one of Target.Index / Target.Name must be
	// populated.
	Target StageRef `json:"target"`

	// Scope declares where the overlay lands relative to the
	// target stage's result. Reuses the universal OverlayScope
	// enum.
	Scope OverlayScope `json:"scope"`

	// Params holds kind-specific configuration as a free-form map.
	// The per-kind schema is documented alongside the kind's
	// processor. Omitted on the wire when nil.
	Params map[string]any `json:"params,omitempty"`
}
