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
	// ignored for stages > 0.
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
}
