package cli

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// writeJSON writes data as indented JSON. When data is an envelope
// carrying a fatal error it returns errEnvelopeFatal after the write, so
// the process exits ExitFatal with the envelope bytes unchanged and
// nothing added to stderr.
func writeJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := encodeFinite(enc, data); err != nil {
		return err
	}
	if envelopeIsFatal(data) {
		return errEnvelopeFatal
	}
	return nil
}

// encodeFinite writes v through enc under the wire rule for undefined
// figures: a non-finite float is JSON null (types.MarshalFinite), never
// a `json: unsupported value` failure. Output holding no non-finite
// float is byte-identical to enc.Encode(v).
func encodeFinite(enc *json.Encoder, v any) error {
	body, err := types.MarshalFinite(v)
	if err != nil {
		return err
	}
	return enc.Encode(json.RawMessage(body))
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
//
// It returns the envelope-fatal exit wrapping err (see reportedFatal), so
// the process exits ExitFatal and a caller can still errors.As the code.
func writeCodedErrorEnvelope(w io.Writer, fallback string, err error) error {
	return reportedFatal(writeJSON(w, codedErrorEnvelope(nil, fallback, err)), err)
}

// codedErrorEnvelope builds writeCodedErrorEnvelope's envelope around
// data, so a leaf that still has a partial report (and its warnings) for
// a fatal error can carry them beside errors[0] with the same code rule.
func codedErrorEnvelope(data any, fallback string, err error) *descriptor.Envelope {
	env := descriptor.NewEnvelope(data)
	var ce *perrors.CodedError
	if stderrors.As(err, &ce) {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
	} else {
		env.AddError(fallback, err.Error(), nil)
	}
	return env
}

func writeText(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format, args...)
}

// WriteJSONPublic is the exported version of writeJSON for use from main.
func WriteJSONPublic(w io.Writer, data any) error {
	return writeJSON(w, data)
}

// runtimeWarnings is the CLI's single reading of every run-time warning
// a result carries — Response.Warnings (one Response, every Compose
// slot, every chain stage), ComposedResponse.Warnings and
// FacetResult.Warnings — as the {code, message, details} entries the
// --json envelope's top-level `warnings` and the --stream trailing
// warnings record both carry. A Compose slot's entry gains
// details.request (the slot index) and a chain stage's details.stage,
// unless the warning already locates itself; the result's own entries
// are never mutated (data keeps them unchanged). Overlay-layer
// warnings stay on their layer. Nil for a warning-free result, so a
// clean run's envelope and stream are byte-identical.
func runtimeWarnings(result any) []*descriptor.EnvelopeEntry {
	var out []*descriptor.EnvelopeEntry
	add := func(ws []*types.ResponseWarning, key string, at int) {
		for _, w := range ws {
			if w == nil {
				continue
			}
			details := w.Details
			if key != "" {
				if _, set := details[key]; !set {
					details = make(map[string]any, len(w.Details)+1)
					for k, v := range w.Details {
						details[k] = v
					}
					details[key] = at
				}
			}
			out = append(out, &descriptor.EnvelopeEntry{Code: w.Code, Message: w.Message, Details: details})
		}
	}
	switch r := result.(type) {
	case *types.Response:
		if r != nil {
			add(r.Warnings, "", 0)
		}
	case *types.ComposedResponse:
		if r != nil {
			for i, sub := range r.Responses {
				if sub != nil {
					add(sub.Warnings, "request", i)
				}
			}
			add(r.Warnings, "", 0)
		}
	case *types.ChainResponse:
		if r != nil {
			// Final aliases the last stage: walk Stages only.
			for i, st := range r.Stages {
				if st != nil {
					add(st.Warnings, "stage", i)
				}
			}
		}
	case *types.FacetResult:
		if r != nil {
			add(r.Warnings, "", 0)
		}
	}
	return out
}

// withRuntimeWarnings promotes result's run-time warnings
// (runtimeWarnings) onto env's top-level `warnings`, where every
// generic envelope consumer looks; data keeps its own copy.
func withRuntimeWarnings(env *descriptor.Envelope, result any) *descriptor.Envelope {
	for _, w := range runtimeWarnings(result) {
		env.AddWarning(w.Code, w.Message, w.Details)
	}
	return env
}

// writeResultEnvelope is writeEnvelopeWithRequest for a result that
// carries run-time warnings: they are promoted onto the envelope's
// top-level `warnings` (withRuntimeWarnings).
func writeResultEnvelope(w io.Writer, result, request any) error {
	return writeJSON(w, withRuntimeWarnings(descriptor.NewEnvelopeWithRequest(result, request), result))
}

// writeStreamWarnings ends an NDJSON stream with one
// {"warnings":[...]} record carrying ws (runtimeWarnings' entries) when
// the run warned; nothing for a warning-free run.
func writeStreamWarnings(enc *json.Encoder, ws []*descriptor.EnvelopeEntry) error {
	if len(ws) == 0 {
		return nil
	}
	return encodeFinite(enc, map[string]any{"warnings": ws})
}
