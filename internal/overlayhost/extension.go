package overlayhost

import (
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/extspec"
	"github.com/frankbardon/pulse/types"
)

// ExtensionDecl is the declared contract of one embedder-registered
// overlay kind (pulse.OverlayRegistration): what predict and every
// runtime hook judge a spec of the kind by. Predict reads it off the
// instance snapshot, the runtime off the extension registry; both are
// built from the same registration by the same root function, so the
// two sides judge one declaration.
type ExtensionDecl struct {
	// Kind is the registered OVERLAY_<NAMESPACE>_<NAME>.
	Kind types.OverlayKind
	// HostShapes are the Request.Overlays host shapes the kind runs on
	// (types.OverlayShapeMatrix: a crosstab host), one per handler the
	// registration sets.
	HostShapes []types.OverlayShape
	// Shapes are the payload shapes the handler may return.
	Shapes []types.OverlayShape
	// Scopes are the scopes a spec may author.
	Scopes []types.OverlayScope
	// Refs are the Ref arms a spec may set (RefNone: every arm empty).
	Refs []string
	// Validate is the registration's optional spec-only hook
	// (extend.SpecValidator); nil accepts every spec.
	Validate func(*types.OverlaySpec) error
}

// RefNone and RefMargin are the ExtensionDecl.Refs spellings
// (extend.OverlayRefNone / extend.OverlayRefMargin).
const (
	RefNone   = "none"
	RefMargin = "margin"
)

// ExtensionRefArms is every OverlayRef arm, in OverlayRef field order,
// as its JSON key spells it.
func ExtensionRefArms(ref types.OverlayRef) []string {
	var arms []string
	add := func(set bool, name string) {
		if set {
			arms = append(arms, name)
		}
	}
	add(ref.Margin != nil, "margin")
	add(ref.Sibling != nil, "sibling")
	add(ref.BaselineIndex != nil, "baseline_index")
	add(ref.Prior != nil, "prior")
	add(ref.RollingMean != nil, "rolling_mean")
	add(ref.YoY != nil, "yoy")
	add(ref.Population != nil, "population")
	add(ref.Stage != nil, "stage")
	add(ref.Slot != nil, "slot")
	add(ref.Matrix != nil, "matrix")
	return arms
}

// ExtensionRequestRefusal is the one rule a Request.Overlays spec of an
// extension kind meets, on predict (descriptor.ValidateOverlays) and on
// every runtime Process hook (processing.refuseWrongHostOverlays) alike,
// in this order — the first failing rule wins:
//
//  1. host shape: judged (overlayhost.JudgedShape) must be one of
//     HostShapes — else PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
//     {index, kind, host, shape, shapes}, ShapeRefusal's wording;
//  2. a multi-measure crosstab (measures) — MeasureHostRefusal;
//  3. Ref: at most one arm, one the declaration lists (no arm needs
//     RefNone); a margin's axis a known MarginAxis —
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {index, kind, …};
//  4. Scope one of Scopes — PULSE_OVERLAY_SCOPE_UNSUPPORTED
//     {index, kind, scope}, CrosstabSpecRefusal's wording;
//  5. Level / Within zero — PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//     {index, kind, level, within};
//  6. the Validate hook (extspec.Refusal, site {overlay, kind,
//     overlays, spec.Name}).
//
// Params are judged before it, by the shared params allowlist
// (descriptor.ParamsRefusal) every funnel runs first. nil when the spec
// passes.
func ExtensionRequestRefusal(d *ExtensionDecl, judged types.OverlayShape, measures bool, spec *types.OverlaySpec, index int) *errors.CodedError {
	if d == nil || spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	if !slices.Contains(d.HostShapes, judged) {
		names := make([]string, len(d.HostShapes))
		runs := ""
		for i, s := range d.HostShapes {
			names[i] = string(s)
			if i > 0 {
				runs += " or "
			}
			runs += shapeHost(s)
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" does not run on a "+string(judged)+"-shape Request.Overlays host; it runs on "+runs,
			map[string]any{"index": index, "kind": kind, "host": string(Request), "shape": string(judged), "shapes": names})
	}
	if measures {
		if cerr := MeasureHostRefusal(spec.Kind, spec.Kind, index); cerr != nil {
			return cerr
		}
	}
	if cerr := extensionRefRefusal(d, spec, index); cerr != nil {
		return cerr
	}
	if !slices.Contains(d.Scopes, spec.Scope) {
		names := make([]string, len(d.Scopes))
		for i, s := range d.Scopes {
			names[i] = string(s)
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+kind+" does not support scope "+string(spec.Scope)+" (supports: "+strings.Join(names, ", ")+")",
			map[string]any{"index": index, "kind": kind, "scope": string(spec.Scope)})
	}
	if spec.Level != 0 || spec.Within != 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
			"overlay "+kind+" does not support Level / Within (an extension overlay reads the leaf axes)",
			map[string]any{"index": index, "kind": kind, "level": spec.Level, "within": spec.Within})
	}
	return extspec.Refusal(d.Validate, spec, extspec.Site{Category: "overlay", Type: kind, Slot: "overlays", Name: spec.Name})
}

// extensionRefRefusal is ExtensionRequestRefusal's Ref rule.
func extensionRefRefusal(d *ExtensionDecl, spec *types.OverlaySpec, index int) *errors.CodedError {
	kind := string(spec.Kind)
	accepts := strings.Join(d.Refs, ", ")
	arms := ExtensionRefArms(spec.Ref)
	switch {
	case len(arms) == 0:
		if !slices.Contains(d.Refs, RefNone) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+kind+" requires a Ref (accepts: "+accepts+")",
				map[string]any{"index": index, "kind": kind, "refs": slices.Clone(d.Refs)})
		}
		return nil
	case len(arms) > 1:
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" sets more than one Ref arm ("+strings.Join(arms, ", ")+"); set at most one (accepts: "+accepts+")",
			map[string]any{"index": index, "kind": kind, "ref_arms": arms, "refs": slices.Clone(d.Refs)})
	case !slices.Contains(d.Refs, arms[0]):
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" does not read Ref."+arms[0]+" (accepts: "+accepts+")",
			map[string]any{"index": index, "kind": kind, "ref": arms[0], "refs": slices.Clone(d.Refs)})
	}
	if spec.Ref.Margin != nil && !knownMarginAxes[spec.Ref.Margin.Axis] {
		axis := string(spec.Ref.Margin.Axis)
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+kind+" Ref.Margin.Axis is not a known MarginAxis: "+axis,
			map[string]any{"index": index, "kind": kind, "axis": axis})
	}
	return nil
}

// ExtensionOutputRefusal holds an extension handler's output to its
// declaration, on every run (index = the spec index) and at pulse.New's
// probe (index < 0: no index in details):
//
//   - payload.Shape is one of d.Shapes (reason undeclared_shape);
//   - exactly that shape's arm is set (reason shape_arm_mismatch);
//   - no Parts (reason undeclared_parts);
//   - none of the engine's multiple-comparison figures — PAdjusted /
//     SignificantAdjusted on the payload or the summary (reason
//     engine_owned);
//   - every warning carries a Code and a Message (reason
//     invalid_warning).
//
// Code PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH, details {category:
// "overlay", name, reason, shape, shapes[, index]}. nil when it holds.
func ExtensionOutputRefusal(d *ExtensionDecl, payload *types.OverlayPayload, summary *types.OverlaySummary, warnings []types.OverlayWarning, index int) *errors.CodedError {
	if d == nil || payload == nil {
		return nil
	}
	fail := func(reason, why string) *errors.CodedError {
		shapes := make([]string, len(d.Shapes))
		for i, s := range d.Shapes {
			shapes[i] = string(s)
		}
		details := map[string]any{"category": "overlay", "name": string(d.Kind), "reason": reason,
			"shape": string(payload.Shape), "shapes": shapes}
		if index >= 0 {
			details["index"] = index
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
			"extension overlay "+string(d.Kind)+" returned a payload its registration does not declare: "+why, details)
	}
	if !slices.Contains(d.Shapes, payload.Shape) {
		return fail("undeclared_shape", "shape "+strconv.Quote(string(payload.Shape))+" is not one of its Shapes")
	}
	scalar, series, matrix := payload.Scalar != nil, payload.Series != nil, payload.Matrix != nil
	want := map[types.OverlayShape][3]bool{
		types.OverlayShapeScalar: {true, false, false},
		types.OverlayShapeSeries: {false, true, false},
		types.OverlayShapeMatrix: {false, false, true},
	}[payload.Shape]
	if [3]bool{scalar, series, matrix} != want {
		return fail("shape_arm_mismatch", "a "+string(payload.Shape)+" payload sets exactly its own arm ("+string(payload.Shape)+") and leaves the others nil")
	}
	if len(payload.Parts) > 0 {
		return fail("undeclared_parts", "an extension overlay returns no Parts")
	}
	if payload.PAdjusted != nil || payload.SignificantAdjusted != nil ||
		(summary != nil && (summary.PAdjusted != nil || summary.SignificantAdjusted != nil)) {
		return fail("engine_owned", "p_adjusted / significant_adjusted are the engine's multiple-comparison figures")
	}
	for _, w := range warnings {
		if w.Code == "" || w.Message == "" {
			return fail("invalid_warning", "every warning needs a Code and a Message")
		}
	}
	return nil
}
