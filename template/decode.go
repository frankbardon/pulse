package template

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Rendered is the result of a complete render: the substituted JSON plus
// the typed request it decoded into.
//
// Exactly one of the five typed pointers is non-nil, and which one is
// decided solely by Target. There are no generics and no per-target
// wrapper types — the caller reads the pointer its target names and hands
// it straight to the matching facade method (Process, Compose,
// ProcessChain, FacetSchema, SampleWithRequest). A caller that does not
// know the target ahead of time can switch on Target, or take the
// interface value from Typed.
//
// JSON is retained alongside the typed value because the two answer
// different questions. The typed request is what gets executed; the raw
// JSON is what a caller shows a human, diffs against an expectation, or
// stores as the record of what a render produced. Re-marshaling the typed
// value would not reproduce it — every request struct is dense with
// omitempty, so a slot that rendered to an explicit zero would silently
// vanish from a round trip.
type Rendered struct {
	// Target is the template's declared target — the discriminant that
	// says which typed pointer below is populated.
	Target Target

	// JSON is the rendered body exactly as RenderJSON produced it,
	// before strict decode. It is always populated.
	JSON json.RawMessage

	// Request is populated iff Target is TargetRequest.
	Request *types.Request

	// Composed is populated iff Target is TargetComposed.
	Composed *types.ComposedRequest

	// Chain is populated iff Target is TargetChain.
	Chain *types.ChainRequest

	// Facet is populated iff Target is TargetFacet.
	Facet *types.FacetRequest

	// Sample is populated iff Target is TargetSample.
	Sample *types.SampleRequest
}

// Typed returns the one populated request pointer as an interface value,
// selected by Target. It is the target-agnostic accessor — useful to a
// caller that dispatches on the concrete type rather than on Target.
//
// Nil-safe, and returns nil for a zero Rendered, so a nil result always
// means "nothing was decoded" rather than "the wrong arm was read".
func (r *Rendered) Typed() any {
	if r == nil {
		return nil
	}
	switch r.Target {
	case TargetRequest:
		if r.Request == nil {
			return nil
		}
		return r.Request
	case TargetComposed:
		if r.Composed == nil {
			return nil
		}
		return r.Composed
	case TargetChain:
		if r.Chain == nil {
			return nil
		}
		return r.Chain
	case TargetFacet:
		if r.Facet == nil {
			return nil
		}
		return r.Facet
	case TargetSample:
		if r.Sample == nil {
			return nil
		}
		return r.Sample
	default:
		return nil
	}
}

// Render turns a template plus a caller-supplied variable map into a
// typed, executable request. It is the package's terminal operation and
// the only one most callers need.
//
// It is RenderJSON followed by a strict decode into the type Target
// names; variables are resolved exactly once, by RenderJSON. Every fault
// the render walk can raise (PULSE_TEMPLATE_INVALID,
// PULSE_TEMPLATE_TARGET_UNKNOWN, PULSE_TEMPLATE_VAR_*,
// PULSE_TEMPLATE_UNRESOLVED) surfaces from here unchanged — Render adds
// exactly one new failure mode, PULSE_TEMPLATE_RENDER_INVALID, for
// substituted JSON that does not fit the target request type.
//
// The decode is STRICT: json.Decoder with DisallowUnknownFields. That is
// deliberately harsher than the rest of Pulse, which tolerates unknown
// fields — that tolerance is exactly how the examples/ `_meta` sidecar
// block survives at execution. Here a typo in a stored template must fail
// loudly at render rather than silently drop a request slot, so an
// unrecognised key is a hard error and the message names it.
//
// Render never opens a cohort file, and the package it lives in cannot:
// nothing on the render path imports a filesystem API. Field existence,
// operator/field type compatibility, and streamability stay Predict's
// job, unchanged. A template that renders is well-formed against the
// request SHAPE; whether it is executable against a particular cohort is
// a separate question with a separate answer.
func Render(t *Template, supplied map[string]any) (*Rendered, error) {
	raw, err := RenderJSON(t, supplied)
	if err != nil {
		return nil, err
	}

	out := &Rendered{Target: t.Target, JSON: raw}
	switch t.Target {
	case TargetRequest:
		out.Request = new(types.Request)
		err = strictDecode(t, raw, out.Request)
	case TargetComposed:
		out.Composed = new(types.ComposedRequest)
		err = strictDecode(t, raw, out.Composed)
	case TargetChain:
		out.Chain = new(types.ChainRequest)
		err = strictDecode(t, raw, out.Chain)
	case TargetFacet:
		out.Facet = new(types.FacetRequest)
		err = strictDecode(t, raw, out.Facet)
	case TargetSample:
		out.Sample = new(types.SampleRequest)
		err = strictDecode(t, raw, out.Sample)
	default:
		// Unreachable in practice: Validate (via RenderJSON) rejects an
		// absent or unrecognised target before any substitution runs.
		// Kept as a hard failure anyway so a sixth Target added without
		// an arm here cannot silently return an empty Rendered.
		return nil, unknownTargetAtDecode(t)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// unknownFieldPrefix is the stable prefix encoding/json uses to report a
// DisallowUnknownFields violation. There is no dedicated error type to
// assert on, so the prefix is the only handle — and the field name it
// carries is the whole reason this decode is tolerable, so it is worth
// parsing rather than passing through verbatim.
const unknownFieldPrefix = "json: unknown field "

// strictDecode decodes the rendered JSON into out, rejecting any key that
// is not a field of the target type. Faults are translated into
// PULSE_TEMPLATE_RENDER_INVALID carrying the template, the target, and —
// when the fault is an unknown field — the offending field name.
//
// There is no shared strict-decode helper in the repo; this is the local
// one, and it is deliberately local. Strictness is a template-package
// policy, not a Pulse-wide one.
func strictDecode(t *Template, raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return renderInvalid(t, err)
	}
	return nil
}

// renderInvalid builds the strict-decode fault, choosing between the
// unknown-field message and the structural-mismatch message.
func renderInvalid(t *Template, cause error) error {
	target := t.Target
	details := map[string]any{
		errors.DetailTemplate: t.Name,
		"target":              target.String(),
	}

	var msg string
	if field, ok := unknownFieldName(cause); ok {
		details["field"] = field
		msg = "rendered template body carries the unrecognised field " + strconv.Quote(field) +
			", which is not a slot on the " + strconv.Quote(target.String()) + " target (" +
			targetGoType(target) + "). A rendered template is decoded STRICTLY — unlike a request " +
			"handed to Process directly, an unknown key is an error rather than being ignored, so a " +
			"typo cannot silently drop a request slot. Remove " + strconv.Quote(field) +
			", correct its spelling, or move it under the slot it belongs to. If the body was copied " +
			"from an examples/ file, drop the `_meta` block — it is an example sidecar, not a request field."
	} else {
		msg = "rendered template body does not fit the " + strconv.Quote(target.String()) +
			" target (" + targetGoType(target) + "): " + cause.Error() +
			". A substituted value's JSON type must match the request slot it lands in — check the " +
			"declared type of the variable feeding that slot."
	}

	fault := errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_RENDER_INVALID, msg, details)
	fault.Cause = cause
	return fault
}

// unknownFieldName extracts the offending key from a
// DisallowUnknownFields decoder fault. The second result reports whether
// the error was that fault at all.
func unknownFieldName(err error) (string, bool) {
	msg := err.Error()
	if !strings.HasPrefix(msg, unknownFieldPrefix) {
		return "", false
	}
	quoted := strings.TrimSpace(strings.TrimPrefix(msg, unknownFieldPrefix))
	name, uerr := strconv.Unquote(quoted)
	if uerr != nil {
		// The decoder always quotes the name; if that ever changes, the
		// raw remainder is still more useful than nothing.
		return quoted, true
	}
	return name, true
}

// unknownTargetAtDecode builds the defensive fault for a target that
// reached the decode switch without an arm.
func unknownTargetAtDecode(t *Template) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_TARGET_UNKNOWN,
		"template target "+strconv.Quote(t.Target.String())+
			" has no decode target; valid targets are "+targetList(),
		map[string]any{
			errors.DetailTemplate: t.Name,
			"target":              t.Target.String(),
		})
}

// targetGoType names the Go request type a target decodes into, for error
// prose. It is the one place the wire spelling and the type it selects are
// stated together, which is what makes a decode fault actionable: a caller
// reading "not a slot on the "facet" target (types.FacetRequest)" knows
// exactly which struct to check.
func targetGoType(target Target) string {
	switch target {
	case TargetRequest:
		return "types.Request"
	case TargetComposed:
		return "types.ComposedRequest"
	case TargetChain:
		return "types.ChainRequest"
	case TargetFacet:
		return "types.FacetRequest"
	case TargetSample:
		return "types.SampleRequest"
	default:
		return "no request type"
	}
}
