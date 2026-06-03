package descriptor

import "encoding/json"

// Envelope is the standard JSON output wrapper for all descriptor operations.
// All --json output follows this shape.
//
// Request, when non-nil, carries the *normalized* request the operation
// executed — smart defaults resolved, label bindings expanded, per-stage
// requests captured for chains. Opt-in via pulse.Options.EchoRequest /
// CLI --echo-request; absent (and omitted from JSON) by default so the
// envelope shape is unchanged for callers that do not need it. The
// shape varies by operation: *types.Request for process/predict,
// *types.ComposedRequest for compose, *types.ChainRequest for
// process-chain, *types.FacetRequest for facet, *types.SampleRequest
// for sample.
type Envelope struct {
	FormatVersion string           `json:"format_version"`
	Data          any              `json:"data"`
	Request       any              `json:"request,omitempty"`
	Errors        []*EnvelopeEntry `json:"errors"`
	Warnings      []*EnvelopeEntry `json:"warnings"`
}

// EnvelopeEntry represents a single error or warning in the envelope.
type EnvelopeEntry struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// NewEnvelope creates an envelope with the given data and no errors/warnings.
func NewEnvelope(data any) *Envelope {
	return &Envelope{
		FormatVersion: "1.0",
		Data:          data,
		Errors:        []*EnvelopeEntry{},
		Warnings:      []*EnvelopeEntry{},
	}
}

// NewEnvelopeWithRequest creates an envelope with both data and the
// normalized request that produced it. Use when EchoRequest is enabled
// at the operation boundary. Passing a nil request degrades to
// NewEnvelope semantics (the field is omitted from JSON via omitempty).
func NewEnvelopeWithRequest(data, request any) *Envelope {
	env := NewEnvelope(data)
	env.Request = request
	return env
}

// WithRequest attaches a normalized request to an existing envelope.
// Returns the receiver for fluent use. Nil-safe.
func (e *Envelope) WithRequest(request any) *Envelope {
	if e != nil {
		e.Request = request
	}
	return e
}

// AddError appends a structured error to the envelope.
func (e *Envelope) AddError(code, message string, details map[string]any) {
	e.Errors = append(e.Errors, &EnvelopeEntry{
		Code:    code,
		Message: message,
		Details: details,
	})
}

// AddWarning appends a structured warning to the envelope.
func (e *Envelope) AddWarning(code, message string, details map[string]any) {
	e.Warnings = append(e.Warnings, &EnvelopeEntry{
		Code:    code,
		Message: message,
		Details: details,
	})
}

// MarshalJSON produces deterministic JSON output.
func (e *Envelope) MarshalJSON() ([]byte, error) {
	type alias Envelope
	return json.Marshal((*alias)(e))
}
