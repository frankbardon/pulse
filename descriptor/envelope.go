package descriptor

import "encoding/json"

// Envelope is the standard JSON output wrapper for all descriptor operations.
// All --json output follows this shape.
type Envelope struct {
	FormatVersion string           `json:"format_version"`
	Data          any              `json:"data"`
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
