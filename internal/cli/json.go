package cli

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
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

// writeEnvelopeWithWarnings wraps data in a descriptor.Envelope and
// lifts a slice of coded source-parse diagnostics onto the envelope's
// `warnings` array, where the --json contract says warnings live.
//
// It exists because an adapter's non-fatal diagnostics are not part of
// the report payload's meaning — a PULSE_SPSS_CARDINALITY_HIGH is not a
// property of "6 rows imported", it is a caveat about the cohort — and
// burying them inside `data` would leave every generic envelope consumer
// blind to them. An empty / nil slice leaves the envelope byte-identical
// to writeEnvelope, so no existing --json output shape moves.
func writeEnvelopeWithWarnings(w io.Writer, data any, warnings []*perrors.CodedError) error {
	env := descriptor.NewEnvelope(data)
	for _, warn := range warnings {
		if warn == nil {
			continue
		}
		env.AddWarning(string(warn.Code), warn.Message, warn.Details)
	}
	return writeJSON(w, env)
}

// writeSourceWarnings prints one line per coded source-parse diagnostic
// on the human-readable (non-JSON) path. Silent for an empty slice.
func writeSourceWarnings(w io.Writer, warnings []*perrors.CodedError) {
	for _, warn := range warnings {
		if warn == nil {
			continue
		}
		writeText(w, "Warning [%s]: %s\n", warn.Code, warn.Message)
	}
}

func writeErrorEnvelope(w io.Writer, code, message string) error {
	env := descriptor.NewEnvelope(nil)
	env.AddError(code, message, nil)
	return writeJSON(w, env)
}

// writeCodedErrorEnvelope emits a fatal error into the --json envelope,
// preserving the error's own code and details when it carries them.
//
// It exists because the obvious spelling loses exactly the thing the
// envelope's `errors[0].code` is for. `writeErrorEnvelope(w,
// "IMPORT_ERROR", err.Error())` stringifies a *errors.CodedError, so a
// PULSE_SPSS_SIDECAR_STALE — a code with a message and fixups reachable
// through `pulse errors lookup` — arrives at the consumer as the
// placeholder "IMPORT_ERROR" with the real code buried in prose. Non-fatal
// warnings on the same paths have always carried real codes
// (writeEnvelopeWithWarnings), so the fatal path was the odd one out.
//
// fallback is the placeholder used when the error is NOT coded — an
// ordinary fmt.Errorf from the shared jobs, which has no code to preserve
// — so every existing non-coded failure keeps the envelope it had.
//
// It follows E2-S7's makeImportReader precedent: pass a *CodedError
// through verbatim rather than re-wrapping it. errors.As rather than a
// type assertion, so a code wrapped with %w on the way up still surfaces.
func writeCodedErrorEnvelope(w io.Writer, fallback string, err error) error {
	env := descriptor.NewEnvelope(nil)
	var ce *perrors.CodedError
	if stderrors.As(err, &ce) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	} else {
		env.AddError(fallback, err.Error(), nil)
	}
	return writeJSON(w, env)
}

func writeText(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}

// WriteJSONPublic is the exported version of writeJSON for use from main.
func WriteJSONPublic(w io.Writer, data any) error {
	return writeJSON(w, data)
}
