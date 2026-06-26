package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/descriptor"
)

func writeJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func writeEnvelope(w io.Writer, data any) error {
	env := descriptor.NewEnvelope(data)
	return writeJSON(w, env)
}

// writeEnvelopeWithRequest wraps data + the normalized request in a
// descriptor.Envelope and writes JSON to w. Pass nil for request to fall
// back to the writeEnvelope shape (the field is omitted via omitempty).
func writeEnvelopeWithRequest(w io.Writer, data, request any) error {
	env := descriptor.NewEnvelopeWithRequest(data, request)
	return writeJSON(w, env)
}

func writeErrorEnvelope(w io.Writer, code, message string) error {
	env := descriptor.NewEnvelope(nil)
	env.AddError(code, message, nil)
	return writeJSON(w, env)
}

func writeText(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}

// WriteJSONPublic is the exported version of writeJSON for use from main.
func WriteJSONPublic(w io.Writer, data any) error {
	return writeJSON(w, data)
}
