package overlayhost

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/extspec"
	"github.com/frankbardon/pulse/internal/seriespartition"
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
	// (types.OverlayShapeMatrix: a crosstab host; types.OverlayShapeSeries:
	// a grouped request with aggregations), one per Process handler the
	// registration sets. Empty: the kind does not run on
	// Request.Overlays at all.
	HostShapes []types.OverlayShape
	// Compose reports that the kind runs on ComposedRequest.Overlays
	// (the registration sets a Compose handler).
	Compose bool
	// Shapes are the payload shapes the handler may return.
	Shapes []types.OverlayShape
	// Scopes are the scopes a spec may author.
	Scopes []types.OverlayScope
	// Refs are the Ref arms a spec may set (RefNone: every arm empty).
	Refs []string
	// Validate is the registration's optional spec-only hook
	// (extend.SpecValidator) for a Request.Overlays spec; nil accepts
	// every spec.
	Validate func(*types.OverlaySpec) error
	// ValidateCompose is the same hook for a ComposedRequest.Overlays
	// spec; nil accepts every spec.
	ValidateCompose func(*types.ComposeOverlaySpec) error
	// WeightAware declares that the handler reads the host's weighted
	// figures: a spec of the kind runs under a weight in force on its
	// slot. Not WeightAware, the weight resolver refuses it under ANY
	// weight in force (PULSE_EXTENSION_NOT_WEIGHT_AWARE), like an
	// extension attribute or test.
	WeightAware bool
	// Inferential declares that the layer carries a test statistic /
	// p-value (its registration must then declare an Interpretation).
	Inferential bool
	// EmitsPValues declares that the layer carries RAW p-values at its
	// payload shape's p-site (ExtensionPSite); requires Inferential. A
	// member slot's p-values join the request's multiple-comparison
	// family like a built-in inferential kind's.
	EmitsPValues bool
	// ReadsCellCountsOnly declares that the handler reads only a
	// crosstab host's record counts and weight figures, never a cell or
	// margin VALUE: the categorical-code cell rule then leaves the kind
	// alone (ExtensionReadsCellValue). Unset, a kind with a MATRIX or
	// Compose handler reads values and is refused over a code cell.
	ReadsCellCountsOnly bool
	// HonoursField declares that the handler scores the aggregation
	// params.field names (internal/seriesfield) — on the SERIES host
	// and on each Compose series slot — instead of the first
	// aggregation (ExtensionHonoursField). Unset, field is not an
	// engine param of the kind.
	HonoursField bool
	// HonoursPartitionBy declares that the SERIES handler runs once per
	// params.partition_by partition of a grouped host
	// (internal/seriespartition; ExtensionHonoursPartitionBy). Request
	// host only: a Compose spec carrying partition_by is refused
	// (ExtensionComposeRefusal).
	HonoursPartitionBy bool
}

// ExtensionHonoursField reports whether the kind d declares scores the
// aggregation params.field names on host (U66 #713): Request iff it
// declares HonoursField and has a SERIES Process handler, Compose iff
// it declares HonoursField and has a Compose handler. The instance
// hooks of seriesfield.AppliesWith / AppliesComposeWith, consulted by
// predict through the snapshot's declaration and by the runtime
// through the registry's — both built from the same registration.
func ExtensionHonoursField(d *ExtensionDecl, host Host) bool {
	if d == nil || !d.HonoursField {
		return false
	}
	switch host {
	case Request:
		return slices.Contains(d.HostShapes, types.OverlayShapeSeries)
	case Compose:
		return d.Compose
	}
	return false
}

// ExtensionHonoursPartitionBy reports whether the kind d declares runs
// its SERIES handler once per params.partition_by partition (U66 #713):
// HonoursPartitionBy and a SERIES Process handler. The instance hook of
// seriespartition.AppliesWith on predict and runtime alike.
func ExtensionHonoursPartitionBy(d *ExtensionDecl) bool {
	return d != nil && d.HonoursPartitionBy && slices.Contains(d.HostShapes, types.OverlayShapeSeries)
}

// ExtensionReadsCellValue is ReadsCellValue / ComposeReadsCellValue for
// an extension kind (U66 #447): whether d's handler reads a crosstab
// cell's VALUE on host — Request iff the kind has a MATRIX Process
// handler, Compose iff it has a Compose handler (a crosstab slot's
// view) — unless the registration declares ReadsCellCountsOnly. No
// extension kind runs on Chain or Facet. Predict and the runtime both
// reach it through the instance snapshot's declaration, which
// pulse.New builds from the same registration as the runtime
// registry's.
func ExtensionReadsCellValue(d *ExtensionDecl, host Host) bool {
	if d == nil || d.ReadsCellCountsOnly {
		return false
	}
	switch host {
	case Request:
		return slices.Contains(d.HostShapes, types.OverlayShapeMatrix)
	case Compose:
		return d.Compose
	}
	return false
}

// MatrixOnly reports whether every payload shape the kind may return
// is MATRIX — the shape rule the `row` / `column` multiplicity
// families need.
func (d *ExtensionDecl) MatrixOnly() bool {
	if d == nil || len(d.Shapes) == 0 {
		return false
	}
	for _, s := range d.Shapes {
		if s != types.OverlayShapeMatrix {
			return false
		}
	}
	return true
}

// Slots returns the overlay slots the kind runs on, in slot order:
// Request when it runs on a Process host shape, Compose when it runs on
// ComposedRequest.Overlays. An extension kind never runs on
// ChainRequest.Overlays or FacetRequest.Overlays.
func (d *ExtensionDecl) Slots() []Host {
	var out []Host
	if len(d.HostShapes) > 0 {
		out = append(out, Request)
	}
	if d.Compose {
		out = append(out, Compose)
	}
	return out
}

// ExtensionSlotRefusal is the wrong-slot refusal for a spec of an
// extension kind authored on host, nil when the kind runs there — the
// shared Refusal wording and details {index, kind, host, hosts}, so an
// extension kind on a slot it does not declare reads exactly like a
// built-in on the wrong slot. Predict and every runtime dispatcher
// (Request, Compose, chain, facet) raise it at the point they raise
// Refusal.
func ExtensionSlotRefusal(d *ExtensionDecl, host Host, index int) *errors.CodedError {
	if d == nil {
		return nil
	}
	hs := d.Slots()
	if slices.Contains(hs, host) {
		return nil
	}
	names := make([]string, len(hs))
	slots := ""
	for i, h := range hs {
		names[i] = string(h)
		if i > 0 {
			slots += " or "
		}
		slots += h.Slot()
	}
	kind := string(d.Kind)
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+kind+" does not run on "+host.Slot()+"; attach it to "+slots,
		map[string]any{"index": index, "kind": kind, "host": string(host), "hosts": names})
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
//  0. the slot: the kind runs on a Process host shape at all — else
//     ExtensionSlotRefusal (a Compose-only kind);
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
	if cerr := ExtensionDeclRefusal(d, judged, measures, spec, index); cerr != nil {
		return cerr
	}
	if d == nil || spec == nil {
		return nil
	}
	return extspec.Refusal(d.Validate, spec, extspec.Site{Category: "overlay", Type: string(spec.Kind), Slot: "overlays", Name: spec.Name})
}

// ExtensionDeclRefusal is ExtensionRequestRefusal without its Validate
// hook: rules 0-5, pure functions of the declaration and the spec. A
// runtime host dispatcher (the MATRIX and SERIES loops) re-judges a
// spec by it after the Process hook (refuseWrongHostOverlays) already
// ran the full rule, so the embedder's hook runs once per spec.
func ExtensionDeclRefusal(d *ExtensionDecl, judged types.OverlayShape, measures bool, spec *types.OverlaySpec, index int) *errors.CodedError {
	if d == nil || spec == nil {
		return nil
	}
	kind := string(spec.Kind)
	if cerr := ExtensionSlotRefusal(d, Request, index); cerr != nil {
		return cerr
	}
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
	return extensionScopeLevelRefusal(d, spec.Kind, spec.Scope, spec.Level, spec.Within, index)
}

// extensionScopeLevelRefusal is the Scope then Level / Within rule
// every slot's extension refusal shares.
func extensionScopeLevelRefusal(d *ExtensionDecl, k types.OverlayKind, scope types.OverlayScope, level, within, index int) *errors.CodedError {
	kind := string(k)
	if !slices.Contains(d.Scopes, scope) {
		names := make([]string, len(d.Scopes))
		for i, s := range d.Scopes {
			names[i] = string(s)
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED,
			"overlay "+kind+" does not support scope "+string(scope)+" (supports: "+strings.Join(names, ", ")+")",
			map[string]any{"index": index, "kind": kind, "scope": string(scope)})
	}
	if level != 0 || within != 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE,
			"overlay "+kind+" does not support Level / Within (an extension overlay reads the leaf axes)",
			map[string]any{"index": index, "kind": kind, "level": level, "within": within})
	}
	return nil
}

// ExtensionComposeRefusal is the one rule a ComposedRequest.Overlays
// spec of an extension kind meets, on predict (descriptor
// validateComposeOverlaySpec) and on the runtime Compose dispatcher
// (processing.ApplyComposeOverlaysWithSchemas) alike, at the point both
// raise the wrong-slot Refusal, in this order:
//
//  1. the slot: the kind declares a Compose handler — else
//     ExtensionSlotRefusal;
//  2. Scope one of Scopes — PULSE_OVERLAY_SCOPE_UNSUPPORTED;
//  3. Level / Within zero — PULSE_OVERLAY_LEVEL_OUT_OF_RANGE;
//     3a. no params.partition_by on a kind declaring HonoursPartitionBy
//     (extensionComposePartitionRefusal, reason compose_host);
//  4. the ValidateCompose hook (extspec.Refusal, site {overlay, kind,
//     overlays, spec.Name}).
//
// Reference / Targets resolution, the slot gates (key set, shape and
// schema match) and the params allowlist are the ones every Compose
// kind meets, after it. nil when the spec passes.
func ExtensionComposeRefusal(d *ExtensionDecl, spec *types.ComposeOverlaySpec, index int) *errors.CodedError {
	if d == nil || spec == nil {
		return nil
	}
	if cerr := ExtensionSlotRefusal(d, Compose, index); cerr != nil {
		return cerr
	}
	if cerr := extensionScopeLevelRefusal(d, spec.Kind, spec.Scope, spec.Level, spec.Within, index); cerr != nil {
		return cerr
	}
	if cerr := extensionComposePartitionRefusal(d, spec, index); cerr != nil {
		return cerr
	}
	return extspec.Refusal(d.ValidateCompose, spec, extspec.Site{Category: "overlay", Type: string(spec.Kind), Slot: "overlays", Name: spec.Name})
}

// extensionComposePartitionRefusal refuses a Compose spec carrying a
// non-null params.partition_by for a kind that declares
// HonoursPartitionBy: the engine partitions only the Request SERIES
// host for an extension kind, so the param would be accepted (the
// kind's allowlist lists it) and silently ignored on a Compose host.
// PULSE_OVERLAY_PARTITION_INVALID {index, kind, partition_by, reason:
// "compose_host"}.
func extensionComposePartitionRefusal(d *ExtensionDecl, spec *types.ComposeOverlaySpec, index int) *errors.CodedError {
	if !d.HonoursPartitionBy {
		return nil
	}
	given, ok := spec.Params[seriespartition.Param]
	if !ok || given == nil {
		return nil
	}
	kind := string(spec.Kind)
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARTITION_INVALID,
		"overlay "+kind+" params.partition_by partitions a Request.Overlays SERIES host only; an extension kind's Compose handler reads every slot whole — omit partition_by on ComposedRequest.Overlays",
		map[string]any{"index": index, "kind": kind, "partition_by": given, "reason": "compose_host"})
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
//     SignificantAdjusted on the payload, the summary or a series
//     entry's summary (reason engine_owned);
//   - p-values only as EmitsPValues declares them, at the shape's
//     p-site (extensionPValueRefusal: reasons undeclared_p_values,
//     misplaced_p_values, invalid_p_value, and missing_p_values at the
//     probe);
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
	if payload.PAdjusted != nil || payload.SignificantAdjusted != nil || adjustedSummary(summary) || seriesAdjusted(payload.Series) {
		return fail("engine_owned", "p_adjusted / significant_adjusted are the engine's multiple-comparison figures")
	}
	if reason, why := extensionPValueRefusal(d, payload, summary, index < 0); reason != "" {
		return fail(reason, why)
	}
	for _, w := range warnings {
		if w.Code == "" || w.Message == "" {
			return fail("invalid_warning", "every warning needs a Code and a Message")
		}
	}
	return nil
}

// ExtensionPartitionOutputRefusal holds one partition's output of a
// kind declaring HonoursPartitionBy, run over a sub-host of rows group
// rows (two or more partitions): the engine lays each partition's
// entries back onto the host rows they came from, so the payload must
// be SERIES with exactly one entry per partition row — else
// PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH, reason
// partition_misaligned, details as ExtensionOutputRefusal's plus
// {rows, entries} (entries -1 for a non-SERIES payload). Runtime only:
// the partitions are data. nil when it holds.
func ExtensionPartitionOutputRefusal(d *ExtensionDecl, payload *types.OverlayPayload, rows, index int) *errors.CodedError {
	if d == nil || payload == nil {
		return nil
	}
	entries := -1
	if payload.Shape == types.OverlayShapeSeries && payload.Series != nil {
		entries = len(payload.Series.Entries)
	}
	if entries == rows {
		return nil
	}
	shapes := make([]string, len(d.Shapes))
	for i, s := range d.Shapes {
		shapes[i] = string(s)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_OVERLAY_OUTPUT_MISMATCH,
		"extension overlay "+string(d.Kind)+" returned a payload its registration does not declare: a partitioned SERIES host (HonoursPartitionBy) needs a series payload with one entry per partition row ("+strconv.Itoa(rows)+")",
		map[string]any{"category": "overlay", "name": string(d.Kind), "reason": "partition_misaligned",
			"shape": string(payload.Shape), "shapes": shapes, "index": index, "rows": rows, "entries": entries})
}

// ExtensionPSite is where an extension layer of payload shape carries
// its p-values when its registration declares EmitsPValues — the
// multiple-comparison fold reads them there, and nowhere else:
//
//   - MATRIX: every present cell's Value (a float64 p, NaN for an
//     untestable cell) — the layer IS the raw p grid;
//   - SERIES: every entry's Summary.PValue;
//   - SCALAR: the layer's Summary.PValue.
func ExtensionPSite(shape types.OverlayShape) string {
	switch shape {
	case types.OverlayShapeMatrix:
		return "matrix cells"
	case types.OverlayShapeSeries:
		return "series entries' Summary.PValue"
	case types.OverlayShapeScalar:
		return "Summary.PValue"
	}
	return ""
}

// extensionPValueRefusal is the output rule's p-value half: the reason
// and prose of the first breach, "" when the output holds.
//
//   - not EmitsPValues: no PValue anywhere (the layer Summary, any
//     series entry) — reason undeclared_p_values;
//   - EmitsPValues: no PValue off the shape's site (ExtensionPSite: a
//     MATRIX or SERIES layer's own Summary.PValue would ride uncorrected
//     beside its corrected family) — reason misplaced_p_values; every p
//     at the site a float64 in [0, 1] or NaN (a MATRIX layer's present
//     cells included) — reason invalid_p_value; and, at pulse.New's
//     probe only (probe), at least one p at the site — reason
//     missing_p_values (a run over real data may test nothing).
func extensionPValueRefusal(d *ExtensionDecl, payload *types.OverlayPayload, summary *types.OverlaySummary, probe bool) (string, string) {
	summaryP := summary != nil && summary.PValue != nil
	seriesP := false
	if payload.Series != nil {
		for i := range payload.Series.Entries {
			if payload.Series.Entries[i].Summary.PValue != nil {
				seriesP = true
				break
			}
		}
	}
	if !d.EmitsPValues {
		if summaryP || seriesP {
			return "undeclared_p_values", "a p-value needs EmitsPValues (and Inferential) on the registration, so the engine can correct it"
		}
		return "", ""
	}
	site := ExtensionPSite(payload.Shape)
	if summaryP && payload.Shape != types.OverlayShapeScalar {
		return "misplaced_p_values", "a " + string(payload.Shape) + " layer carries its p-values in its " + site + ", not in its Summary.PValue"
	}
	found := false
	check := func(p float64) bool {
		found = true
		return math.IsNaN(p) || (p >= 0 && p <= 1)
	}
	invalid := func() (string, string) {
		return "invalid_p_value", "every p-value in the " + site + " is a float64 in [0, 1] or NaN"
	}
	switch payload.Shape {
	case types.OverlayShapeMatrix:
		for _, row := range payload.Matrix.Cells {
			for _, cell := range row {
				if !cell.Present {
					continue
				}
				p, ok := cell.Value.(float64)
				if !ok || !check(p) {
					return invalid()
				}
			}
		}
	case types.OverlayShapeSeries:
		for i := range payload.Series.Entries {
			if p := payload.Series.Entries[i].Summary.PValue; p != nil && !check(*p) {
				return invalid()
			}
		}
	case types.OverlayShapeScalar:
		if summaryP && !check(*summary.PValue) {
			return invalid()
		}
	}
	if probe && !found {
		return "missing_p_values", "a registration declaring EmitsPValues returns at least one p-value in the " + site + " over the probe host"
	}
	return "", ""
}

// adjustedSummary reports whether s carries an engine-owned adjusted
// figure.
func adjustedSummary(s *types.OverlaySummary) bool {
	return s != nil && (s.PAdjusted != nil || s.SignificantAdjusted != nil)
}

// seriesAdjusted reports whether any series entry carries an
// engine-owned adjusted figure.
func seriesAdjusted(s *types.SeriesPayload) bool {
	if s == nil {
		return false
	}
	for i := range s.Entries {
		if adjustedSummary(&s.Entries[i].Summary) {
			return true
		}
	}
	return false
}
