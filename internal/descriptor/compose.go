package descriptor

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprbuiltins"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/formulaident"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// ValidateCompose is the no-execute descriptor mirror of the runtime
// COMPOSE-host overlay gates (strict key-set alignment, structural
// schema match, panel target cap). It walks
// ComposedRequest.Overlays and surfaces every per-spec failure that can
// be detected from header + schema alone — every other Compose overlay
// failure mode (key-set alignment, categorical dict-prefix drift)
// requires record-level visibility the no-execute path does not have.
//
// The validator stays inside descriptor/ — no service / processing
// import. The helpers that mirror internal/processing/-side normalisers
// (kindRequiresMatrix, ResolveComposeSlots's default-label rule, and
// the inferred per-slot shape) are duplicated here under the
// no-execute structural ban; the per-helper sync tests in
// compose_test.go lock the duplicates against the processing
// originals so the two surfaces cannot drift.
//
// Failure modes surfaced (one envelope error per spec failure plus
// one SlotPair entry on ComposeValidationResult.OverlaysSchemaDivergence):
//
//   - PULSE_OVERLAY_KIND_UNKNOWN          — spec.Kind absent from the catalog.
//   - PULSE_OVERLAY_REFERENCE_UNKNOWN     — Reference label does not resolve.
//   - PULSE_OVERLAY_TARGET_UNKNOWN        — Targets[j] does not resolve.
//   - PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT  — ref and target shape disagree.
//   - PULSE_OVERLAY_SLOT_NOT_CROSSTAB     — kind requires MATRIX but slot isn't.
//   - PULSE_OVERLAY_SCHEMA_DIVERGENT      — per-axis grouper-kind tuples disagree.
//   - PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP — multi-ref target count exceeds cap.
//
// composeDefaultPanelCap mirrors the runtime default from
// processing.applyComposeOverlay's OVERLAY_PROP_Z_PANEL /
// OVERLAY_PANEL_INDEX_VS_REF handler (16, per the interview "Multi-
// reference combinatorics" risk paragraph). When OverlaySpec.Options.
// MaxPanelTargets > 0 the per-spec value takes precedence; the
// `OverlayOptions.MaxPanelTargets` doc explicitly notes this is the
// per-request override.
const composeDefaultPanelCap = 16

// ComposeValidationResult is the structured output of ValidateCompose.
// Mirrors ChainValidationResult / FacetValidationResult: the input
// request echoes back so callers can forward it after inspection, and
// the per-spec divergence slice is populated alongside the envelope
// errors so renderers can read the rejected (reference, target) pairs
// without re-parsing envelope details.
type ComposeValidationResult struct {
	// Valid mirrors envelope.Errors emptiness.
	Valid bool `json:"valid"`

	// Request echoes the input composed request unchanged.
	Request *types.ComposedRequest `json:"request"`

	// OverlaysSchemaDivergence lists every (reference, target)
	// pair the overlay walk rejected per PRD §I-FR-I3. Populated
	// alongside the matching envelope error so MCP planners can
	// budget reshapes without re-parsing envelope details. Empty
	// (but non-nil) when every spec passes; never nil so JSON
	// renderers see an empty array, matching PredictResult's
	// contract.
	OverlaysSchemaDivergence []descriptor.SlotPair `json:"overlays_schema_divergence"`

	// OverlayCost maps each COMPOSE-host overlay-spec Name to a coarse
	// cost score the routing layer can consult before execution. Sibling
	// to PredictResult.OverlayCost / FacetValidationResult.OverlayCost —
	// streamable kinds carry overlayCostStreamable (~5% extra work) and
	// buffered kinds carry overlayCostBuffered (~one extra payload
	// traversal). The two multi-ref kinds (OVERLAY_PROP_Z_PANEL,
	// OVERLAY_PANEL_INDEX_VS_REF) scale the base cost by
	// `min(len(spec.Targets), MaxPanelTargets)` so renderers can budget
	// per-panel fan-out without re-deriving the cap. Single-target kinds
	// ignore the Targets slice and surface the raw kind cost. Per
	// kind-catalog-v1 PRD §I-FR-I3 the value is intentionally coarse —
	// callers budgeting cost across slots sum the map values. Empty (but
	// non-nil) when req.Overlays is empty; never nil in JSON output.
	OverlayCost map[string]float64 `json:"overlay_cost"`

	// PValues counts the inferential p-values the Compose-host overlays
	// emit and how many no multiplicity block corrects. Their extent
	// depends on slot results predict never sees, so each inferential
	// spec counts as one and the basis is always lower_bound. Omitted
	// when no host overlay is inferential, when the batch multiplicity is
	// refused, and when the instance hides capability:multiplicity.
	PValues *descriptor.PValueCount `json:"p_values,omitempty"`

	// Advisories carries the coded, non-blocking PULSE_ADVISORY_* notes:
	// each slot's Request advisories in slot order (details.request = the
	// slot index, every slot path prefixed "requests[i]."), then
	// PULSE_ADVISORY_MANY_TESTS over PValues. Never a warning; omitted
	// when none fires.
	Advisories []descriptor.Advisory `json:"advisories,omitempty"`

	// Sweep summarises what the request's `sweep` block expands to —
	// axes, resolved mode, expanded slot count and (once expanded) the
	// sweep slots' labels. Set whenever the sweep is structurally
	// valid, also on a refused batch; omitted without a sweep.
	Sweep *descriptor.SweepSummary `json:"sweep,omitempty"`

	// Fits lists the request's `fits` entries in declaration order —
	// name, regression type, target, the predictors as written and the
	// cohort — read statically, never fitted. Set once the fits pass
	// their structural rules (model.CheckFits); omitted without fits.
	Fits []descriptor.FitSummary `json:"fits,omitempty"`
}

// ValidateCompose runs the no-execute COMPOSE-host overlay walk over
// a *types.ComposedRequest. Returns an envelope whose Data carries a
// *ComposeValidationResult and whose Errors carry one entry per spec
// failure. The validator never reads record data; per-slot schema
// shape inference (MATRIX / SERIES / SCALAR) is derived from each
// slot's *types.Request alone via inferComposeSlotShape.
//
// The Request slot in the envelope is left nil — Compose has no
// single normalized form to echo back (per-slot normalisation is
// handled by service.applyComposeLabelDefaults at execution time and
// is not the descriptor surface's responsibility).
func ValidateCompose(req *types.ComposedRequest) *descriptor.Envelope {
	return ValidateComposeWithOptions(req, nil)
}

// ValidateComposeWithOptions is ValidateCompose with the predict
// options in reach — today that means PredictOptions.Extensions, the
// read-only snapshot through which an embedder-registered grouper's
// fan-out declaration reaches the panel's within-prefix slab gate.
// descriptor/ may not import internal/processing/, so the snapshot is the only
// route; the runtime twin reads the live registry instead and both
// hand their resolver to the same types-side predicate.
//
// Additive rather than a signature change on ValidateCompose: the
// no-options entry point shipped, and a host with no extensions gets
// the identical answer either way (a nil snapshot yields a nil
// resolver, which the gate reads as "no extension groupers").
func ValidateComposeWithOptions(req *types.ComposedRequest, opts *PredictOptions) *descriptor.Envelope {
	env, _ := ValidateComposeExpanded(req, opts)
	return env
}

// ValidateComposeExpanded is ValidateComposeWithOptions also returning
// the effective request it judged — the sweep expanded into slots after
// the explicit ones (sweep.Expand) — so the facade can reach every slot
// cohort (the managed-import TTL) without expanding twice. The
// effective request is the input itself when it carries no sweep, and
// the input too when the sweep was refused before it expanded.
func ValidateComposeExpanded(req *types.ComposedRequest, opts *PredictOptions) (*descriptor.Envelope, *types.ComposedRequest) {
	result := &ComposeValidationResult{
		Valid:                    true,
		Request:                  req,
		OverlaysSchemaDivergence: []descriptor.SlotPair{},
		OverlayCost:              map[string]float64{},
	}
	env := descriptor.NewEnvelope(result)

	if req == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "compose request is required", nil)
		result.Valid = false
		return env, nil
	}
	written := req
	// The runtime's entry order (Service.expandCompose): a set sweep on
	// an instance that hides it is an unknown field before its contents
	// are judged; then the sweep expands — the runtime's shared
	// expansion, sweep.Expand — with the MaxComposeSlots preflight over
	// explicit + expanded slots before any sweep slot renders (a
	// sweep-free batch is counted too); then the slot gate over the
	// effective request. Every pass below judges the effective request:
	// explicit slots, then sweep slots, one label namespace.
	// result.Request keeps the request as written.
	if req.Sweep != nil {
		if serr := SlotRefusal(req, opts.instance()); serr != nil {
			addCodedError(env, serr)
			result.Valid = false
			return env, written
		}
		result.Sweep = sweepSummary(req.Sweep)
	}
	exp, xerr := sweep.Expand(req, composeSlotsPreflight(opts))
	if xerr != nil {
		addCodedError(env, xerr)
		result.Valid = false
		return env, written
	}
	req = exp.Composed
	if result.Sweep != nil {
		result.Sweep.Labels = exp.Labels
	}
	// A slot the instance hides is an unknown field, refused before
	// anything else past the expansion — the runtime's order. A sweep
	// slot carrying one is located by details.request like an explicit
	// slot.
	if serr := SlotRefusal(req, opts.instance()); serr != nil {
		addCodedError(env, serr)
		result.Valid = false
		return env, req
	}
	// Then the fits' structural rules — the runtime's model.CheckFits,
	// run right after its slot gate; nothing past it runs on a refusal.
	if ferr := model.CheckFits(req.Fits); ferr != nil {
		addCodedError(env, ferr)
		result.Valid = false
		return env, req
	}
	// Then the batch's saves and loads — the runtime's
	// model.CheckComposeBatch and its model-file reads, run before any
	// fit or slot; nothing past it runs on a refusal.
	loaded, berr := loadComposeModels(req, opts)
	if berr != nil {
		addCodedError(env, berr)
		result.Valid = false
		return env, req
	}
	// One label namespace across explicit and sweep slots, checked
	// whether or not the batch declares overlays or a sweep — the
	// runtime (applyComposeLabelDefaults) refuses a collision before any
	// slot runs. With overlays the walk below reports it beside the
	// overlay faults.
	if len(req.Overlays) == 0 {
		if _, collision := composeBuildLabelIndex(req); collision != "" {
			addLabelCollision(env, collision)
			result.Valid = false
			return env, req
		}
	}
	// Multiplicity over the whole batch — the pass Compose runs before
	// any slot starts (ResolveComposeMultiplicity).
	multPlan, merr := ResolveComposeMultiplicity(req, opts.defaultMultiplicity(), opts.instance())
	if merr != nil {
		addCodedError(env, merr)
		result.Valid = false
	}
	// The Compose-level `return` (top-level overlays) — the pass
	// Compose runs right after the batch multiplicity.
	if _, rerr := ResolveComposeReturn(req, opts.instance()); rerr != nil {
		addCodedError(env, rerr)
		result.Valid = false
	}
	// Zone resolution per slot — the pass Compose runs inside each
	// slot's Process — against the slot's cohort (or joined) schema
	// read through opts.SchemaLoader. A refusal carries the slot index
	// under details.request, as the runtime's does.
	// Each fit request, recursively: the slot checks plus the regression
	// rules, located under details.fit (the runtime's fit pre-pass). It
	// runs FIRST — a vector fit's resolved labels feed the slots' catalog
	// (step 12 aligns a slot's params.fit by them, as the runtime's bind
	// does) — but its refusals are reported after the slots', as before.
	fitEnv := descriptor.NewEnvelope(nil)
	result.Fits = validateComposeFits(fitEnv, req.Fits, opts, loaded)
	validateComposeSlots(env, req, opts, loaded, result.Fits)
	env.Errors = append(env.Errors, fitEnv.Errors...)
	env.Warnings = append(env.Warnings, fitEnv.Warnings...)

	// Fit-for-purpose advisories, per slot then over the Compose-host
	// overlays — computed whether or not the batch is valid, never a
	// warning, and dropped per Options.SuppressAdvisories.
	if merr == nil && opts.instance().Enabled(featMultiplicity) {
		result.PValues = countComposeHostPValues(req, multPlan, opts)
	}
	result.Advisories = composeAdvisories(req, multPlan, merr == nil, result.PValues, opts)

	if len(req.Overlays) == 0 {
		if len(env.Errors) > 0 {
			result.Valid = false
		}
		// Nothing to validate at the overlay surface; downstream
		// per-slot validation (the standard Predict on each Request)
		// runs through a separate entry point.
		return env, req
	}

	// Build slot label → (slot index, *Request) lookup. Mirrors the
	// runtime composeDefaultLabel rule in
	// internal/processing/compose_overlay_resolve.go: empty Label →
	// "request_<i+1>" (1-based). Collisions are reported the same
	// way the runtime applyComposeLabelDefaults does so the
	// descriptor surface stays parity-true.
	byLabel, labelCollision := composeBuildLabelIndex(req)
	if labelCollision != "" {
		addLabelCollision(env, labelCollision)
		// Continue — label collisions do not block the overlay
		// walk from surfacing other failures the caller would
		// otherwise have to round-trip to discover.
	}

	// Authored slot index per resolved label. The panel's
	// slab-partition diagnostic carries BOTH the panel index and the
	// authored Compose slot index, because panel 0 is the reference
	// and is rarely Compose slot 0 — a diagnostic naming only the
	// panel position sends the caller hunting through their own
	// request.
	byIndex := composeBuildLabelSlotIndex(req)

	for i, spec := range req.Overlays {
		validateComposeOverlaySpec(env, result, &spec, i, byLabel, byIndex, req, opts)
		// Populate the per-spec cost score in matching order — even when
		// the spec fails a downstream gate (the cost map is descriptive,
		// not gated by validity). The cost dispatcher reads the spec's
		// kind + Targets + Options so a rejected spec still surfaces its
		// budget. Mirrors the per-spec cost emission rule on
		// PredictResult.OverlayCost / FacetValidationResult.OverlayCost.
		name := composeOverlayDescriptorName(&spec)
		// Cost keys on the route, so a hidden kind is costed like a
		// kind not in the catalog; the name keeps the authored kind.
		costed := spec
		costed.Kind = opts.overlayRoute(spec.Kind)
		result.OverlayCost[name] = composeOverlayCostForSpec(&costed)
	}

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env, req
}

// composeSlotsPreflight is the MaxComposeSlots check sweep.Expand runs
// on explicit + expanded slots before rendering a sweep slot — the
// runtime's limits.CheckComposeSlots over the instance's limits
// (InstanceSnapshot.Limits, the resolved Options.Limits pulse.New
// installs; the built-in defaults without an instance), so predict and
// the runtime refuse the identical batch with the identical
// PULSE_LIMIT_EXCEEDED.
func composeSlotsPreflight(opts *PredictOptions) func(slots int) error {
	l := opts.instance().Limits()
	return func(slots int) error {
		if ce := limits.CheckComposeSlots(l, slots); ce != nil {
			return ce
		}
		return nil
	}
}

// sweepSummary is ComposeValidationResult.Sweep for a sweep that passes
// the structural check (sweep.Validate), else nil. Labels are filled in
// once the expansion succeeds.
func sweepSummary(spec *types.SweepSpec) *descriptor.SweepSummary {
	if sweep.Validate(spec) != nil {
		return nil
	}
	mode := spec.Mode
	if mode == "" {
		mode = types.SweepModeGrid
	}
	axes := make([]descriptor.SweepAxisSummary, len(spec.Axes))
	for i, ax := range spec.Axes {
		axes[i] = descriptor.SweepAxisSummary{Name: ax.Name, Count: len(ax.Values)}
	}
	return &descriptor.SweepSummary{Axes: axes, Mode: mode, ExpandedCount: sweep.Count(spec)}
}

// addLabelCollision reports PULSE_COMPOSE_LABEL_COLLISION for label.
func addLabelCollision(env *descriptor.Envelope, label string) {
	env.AddError(string(errors.PULSE_COMPOSE_LABEL_COLLISION),
		"compose request has two slots resolving to the same label: "+label,
		map[string]any{"label": label})
}

// composeOverlayDescriptorName resolves the renderer-facing label for one
// ComposeOverlaySpec — spec.Name when set, otherwise the on-wire Kind
// string. Mirrors `processing.composeOverlayLayerName` (the runtime
// equivalent) under the no-execute structural ban so the descriptor
// surface and the runtime surface use byte-identical Name resolution
// for the OverlayCost / OverlayAppliedDescriptor key alignment.
func composeOverlayDescriptorName(spec *types.ComposeOverlaySpec) string {
	if spec == nil {
		return ""
	}
	if spec.Name != "" {
		return spec.Name
	}
	return string(spec.Kind)
}

// composeOverlayCostForSpec returns the per-spec cost score for one
// ComposeOverlaySpec. Single-target kinds delegate to the streamability-
// derived dispatch (overlayCostForKind) — streamable kinds report ~0.05x
// and buffered kinds report ~1.0x. Multi-reference kinds
// (OVERLAY_PROP_Z_PANEL, OVERLAY_PANEL_INDEX_VS_REF) scale the base cost
// by `min(len(spec.Targets), cap)` where cap = spec.Options.MaxPanelTargets
// when > 0, else composeDefaultPanelCap (16). The cap clamp is the same
// rule descriptor.validateComposeOverlaySpec uses to reject
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP — a spec that would be rejected
// at validate time gets a cost score reflecting the cap (not the
// over-cap actual), so renderers budgeting cost across slots see the
// stable post-cap fan-out.
//
// Zero / empty Targets on a multi-ref kind yields a multiplier of 1
// (single-target fallback) so the renderer sees a sensible baseline
// when the caller has not yet populated the panel — the per-spec
// validator surfaces PULSE_OVERLAY_TARGET_UNKNOWN separately for the
// missing target arm.
func composeOverlayCostForSpec(spec *types.ComposeOverlaySpec) float64 {
	if spec == nil {
		return 0
	}
	base := overlayCostForKind(spec.Kind)
	if !composeKindIsPanel(spec.Kind) {
		return base
	}
	targets := len(spec.Targets)
	if targets <= 0 {
		// No-target arm — the per-spec validator surfaces
		// PULSE_OVERLAY_TARGET_UNKNOWN; the cost map stays sensible by
		// reporting the single-target equivalent so renderers do not
		// see a zero-cost panel.
		return base
	}
	cap := composeDefaultPanelCap
	if spec.Options != nil && spec.Options.MaxPanelTargets > 0 {
		cap = spec.Options.MaxPanelTargets
	}
	if targets > cap {
		targets = cap
	}
	return base * float64(targets)
}

// validateComposeOverlaySpec walks a single ComposeOverlaySpec against
// the resolved byLabel lookup and emits per-failure envelope errors +
// SlotPair entries. The helper is split out of ValidateCompose so the
// per-spec coverage can grow kind-by-kind without expanding the
// outer-loop signature.
func validateComposeOverlaySpec(env *descriptor.Envelope, result *ComposeValidationResult, spec *types.ComposeOverlaySpec, specIdx int, byLabel map[string]*types.Request, byIndex map[string]int, req *types.ComposedRequest, opts *PredictOptions) {
	// Gate 0: unknown kind. The catalog lookup runs against
	// types.AllOverlayKinds() so a new kind shows up here automatically
	// once it's appended to the catalog.
	if !composeOverlayKindKnown(opts.overlayRoute(spec.Kind)) {
		env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
			"compose overlay spec carries unknown kind: "+string(spec.Kind),
			map[string]any{"index": specIdx, "kind": string(spec.Kind)})
		appendComposeSlotPair(result, spec.Reference, "", "kind-unknown")
		return
	}
	// Gate 0a: a catalog kind that does not run on
	// ComposedRequest.Overlays (a Process, whole-chain or FACET kind) —
	// the shared overlayhost refusal the runtime dispatcher
	// (processing.ApplyComposeOverlaysWithRequests) raises first, same
	// code and Details. Before, the runtime answered an empty stub layer.
	if cerr := overlayhost.Refusal(overlayhost.Compose, spec.Kind, opts.overlayRoute(spec.Kind), specIdx); cerr != nil {
		addCodedError(env, cerr)
		appendComposeSlotPair(result, spec.Reference, "", "host-incompatible")
		return
	}

	// Gate 0b: a Compose-host OVERLAY_FORMULA may not call total() —
	// the cohort-total term is ATTR_FORMULA's alone. The runtime twin
	// (processing.ApplyComposeOverlaysWithRequests) refuses at the same
	// point, before reference resolution, under the same code.
	if cerr := composeFormulaTotalRefusal(spec, specIdx, opts.overlayRoute(spec.Kind)); cerr != nil {
		addCodedError(env, cerr)
		return
	}
	// Gate 0c: a Compose-host OVERLAY_FORMULA may not call a vector
	// built-in (exprbuiltins.OverlayRefused) — the request-host gate's
	// callee rule. The runtime compile refuses the same formula under
	// the same code.
	if cerr := composeFormulaBuiltinRefusal(spec, specIdx, opts.overlayRoute(spec.Kind)); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Gate 1: reference label resolution.
	refReq, ok := byLabel[spec.Reference]
	if !ok {
		env.AddError(string(errors.PULSE_OVERLAY_REFERENCE_UNKNOWN),
			"compose overlay reference label does not resolve to a slot: "+spec.Reference,
			map[string]any{
				"index":     specIdx,
				"kind":      string(spec.Kind),
				"reference": spec.Reference,
			})
		appendComposeSlotPair(result, spec.Reference, "", "reference-unknown")
		return
	}

	// Gate 2: per-target label resolution. We collect resolved
	// requests so we can fan the per-target shape / schema checks
	// out without re-walking the map.
	type resolvedTarget struct {
		label string
		req   *types.Request
	}
	resolved := make([]resolvedTarget, 0, len(spec.Targets))
	for j, label := range spec.Targets {
		tReq, ok := byLabel[label]
		if !ok {
			env.AddError(string(errors.PULSE_OVERLAY_TARGET_UNKNOWN),
				"compose overlay target label does not resolve to a slot: "+label,
				map[string]any{
					"index":        specIdx,
					"kind":         string(spec.Kind),
					"target_index": j,
					"target_label": label,
				})
			appendComposeSlotPair(result, spec.Reference, label, "target-unknown")
			// Continue scanning so the caller sees every unknown
			// target in one envelope — mirrors how the runtime
			// orchestrator surfaces every per-target failure.
			continue
		}
		resolved = append(resolved, resolvedTarget{label: label, req: tReq})
	}

	// Gate 2b: an Inferential kind reading a slot weighted by a
	// request or instance default weight has no weighted form yet
	// (PULSE_WEIGHT_UNSUPPORTED) — the runtime's check in
	// Service.applyComposeOverlays, over the same raw slots.
	labels := make([]string, len(req.Requests))
	for j, r := range req.Requests {
		if r != nil {
			labels[j] = r.Label
			if labels[j] == "" {
				labels[j] = composeDescriptorDefaultLabel(j)
			}
		}
	}
	var defaultWeight *types.WeightSpec
	if opts != nil {
		defaultWeight = opts.DefaultWeight
	}
	if werr := composeOverlayWeightRefusal(specIdx, spec, req.Requests, labels, defaultWeight, opts.instance()); werr != nil {
		addCodedError(env, werr)
	}
	// Gate 2c: a kind that scales a probability-weighted slot's Σw by
	// the n_eff on its floor, over a slot built with components
	// disabled (PROCESSING_CONFIG) — the runtime's check in
	// Service.applyComposeOverlays.
	if ferr := composeOverlayHiddenFloorRefusal(specIdx, spec, req.Requests, labels, defaultWeight, opts.instance(), opts.componentsDisabled); ferr != nil {
		addCodedError(env, ferr)
	}
	// Gate 2d: a slot the overlay reads scores an array-valued (vector),
	// non-scalar or categorical-code aggregation (PROCESSING_CONFIG,
	// reason array_valued / non_scalar / categorical_code) —
	// the runtime's check in Service.applyComposeOverlays.
	if aerr := composeOverlayArrayValuedRefusal(specIdx, spec, req.Requests, labels, opts.instance(), composeSlotInputSchema(req.Requests, opts)); aerr != nil {
		addCodedError(env, aerr)
	}
	// Gate 2e: a two-sample kind (OVERLAY_T_VS_REF / _Z_VS_REF /
	// _T_CELL / _Z_CELL) on a route that yields no mean, variance or n
	// for a side — AGG_WELFORD moments with components disabled
	// (PULSE_OVERLAY_COMPONENTS_REQUIRED), a scalar mean without the
	// side's variance / sample-size params (PULSE_OVERLAY_PARAM_MISSING),
	// a vs-ref kind over a crosstab slot
	// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) — the runtime's check
	// in Service.applyComposeOverlays.
	if werr := composeOverlayTwoSampleInputsRefusal(specIdx, spec, req.Requests, labels, opts.instance(), opts.componentsDisabled); werr != nil {
		addCodedError(env, werr)
	}

	// Gate 3: multi-reference panel target cap. Fires before the
	// per-target shape walk so a wildly over-cap spec does not also
	// emit N shape-divergence pairs. The cap honours
	// spec.Options.MaxPanelTargets when > 0, otherwise falls back to
	// composeDefaultPanelCap (16). Only the two multi-reference
	// kinds enforce the cap today — other kinds ignore the slot per
	// OverlayOptions.MaxPanelTargets doc.
	if composeKindIsPanel(spec.Kind) {
		cap := composeDefaultPanelCap
		if spec.Options != nil && spec.Options.MaxPanelTargets > 0 {
			cap = spec.Options.MaxPanelTargets
		}
		if len(spec.Targets) > cap {
			env.AddError(string(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP),
				"compose overlay panel target count exceeds the cap",
				map[string]any{
					"index":         specIdx,
					"kind":          string(spec.Kind),
					"targets_count": len(spec.Targets),
					"max_targets":   cap,
				})
			appendComposeSlotPair(result, spec.Reference, "", "panel-targets-over-cap")
			// Stop processing this spec — the cap is a structural
			// gate, the per-target schema walk below would just
			// surface noise.
			return
		}
	}

	// Gate 3b: per-kind params. OVERLAY_PROP_Z_PANEL decodes its
	// Params slot into types.PanelOverlayParams; a blob that cannot
	// decode — or that names an n_source mode this binary does not
	// know — fires PULSE_OVERLAY_PARAM_MISSING rather than being
	// dropped on the floor.
	//
	// Runs AFTER the cap gate on purpose. MaxPanelTargets rides
	// Options, not Params, and an over-cap panel is refused before any
	// params work — so a spec that is both over-cap and malformed
	// reports the cap, which is the structural failure the caller must
	// fix first. Runs BEFORE the per-target shape / schema walk so the
	// spec's own configuration is judged before its slots are.
	//
	// Non-fatal: the walk continues so one envelope still carries every
	// offending slot alongside the params complaint.
	if types.IsPanelOverlayParamsKind(spec.Kind) {
		params, err := types.DecodePanelParamsMap(spec.Params)
		// The per-slot ROW AXES the within-prefix slab gate needs.
		// Built in PANEL order — panel[0] is the reference, panel[i]
		// is Targets[i-1] — so the offender the gate names is the
		// offender the runtime twin would name. A target label that
		// did not resolve has already been reported by Gate 2 and
		// simply contributes no axis: it cannot be judged, and
		// judging the rest is still strictly more useful than
		// saying nothing.
		slots := make([]types.PanelSlabPartitionSlot, 0, 1+len(resolved))
		slots = append(slots, types.PanelSlabPartitionSlot{
			Rows:       composeSlotRowAxis(refReq),
			PanelIndex: 0,
			SlotIndex:  byIndex[spec.Reference],
			Label:      spec.Reference,
		})
		for j, label := range spec.Targets {
			tReq, ok := byLabel[label]
			if !ok {
				continue
			}
			slots = append(slots, types.PanelSlabPartitionSlot{
				Rows:       composeSlotRowAxis(tReq),
				PanelIndex: j + 1,
				SlotIndex:  byIndex[label],
				Label:      label,
			})
		}
		validateOverlayPanel(env, spec.Kind, params, err, specIdx, slots,
			snapshotGroupFanOut(extensionsFromOpts(opts)))
		// Weighted-slot n_source rule (weighting-inferential E3-S2):
		// an unweighted-count mode on a panel with a weighted slot (its
		// crosstab cell resolves an applied weight) is PROCESSING_CONFIG
		// with the runtime's message (processing.applyPropZPanel).
		if err == nil && types.PanelNSourceReadsComponents(params.NSource) {
			basis := crosstabCellWeightBasis(refReq, opts)
			for _, label := range spec.Targets {
				if tReq, ok := byLabel[label]; ok && !basis.Weighted() {
					basis = crosstabCellWeightBasis(tReq, opts)
				}
			}
			if reason := weighting.NSourceRefusal(params.NSource, basis); reason != "" {
				env.AddError(string(errors.PROCESSING_CONFIG),
					"overlay "+string(spec.Kind)+" n_source "+params.NSource+": "+reason,
					map[string]any{"index": specIdx, "kind": string(spec.Kind), "param": "n_source",
						"n_source": params.NSource})
			}
		}
	}

	// Gate 4: per-target shape + schema match. The reference shape
	// is the per-slot Request shape, NOT the overlay's scope; the
	// scope discriminates within the host result, the shape is the
	// host itself. The kindRequiresMatrix table mirrors the runtime
	// processing.kindRequiresMatrix (see compose_test.go for the
	// lock-step sync test).
	//
	// Gate 4a, the SERIES value + key rule (U46): a non-crosstab slot is
	// scored on the aggregation params.field names (else
	// Aggregations[0]) and keyed on its GROUP columns alone — the
	// runtime's processing.projectComposeSeriesSlots. An unknown label
	// is PULSE_OVERLAY_FIELD_UNKNOWN (reference first, then targets),
	// and two SERIES slots whose group key columns differ by name can
	// never align: PULSE_OVERLAY_KEY_SET_DIVERGENT, the code the
	// runtime key-set gate raises.
	if seriesfield.AppliesCompose(opts.overlayRoute(spec.Kind)) {
		slots := append([]resolvedTarget{{label: spec.Reference, req: refReq}}, resolved...)
		for _, s := range slots {
			if s.req == nil || s.req.Crosstab != nil {
				continue
			}
			// An aggregation authored without a Type takes its smart
			// default (and so its TYPE_field label) from the slot's
			// schema — the joined one for a joined slot — read header
			// only (composeSlotAsExecuted). Only a slot whose schema
			// does not load, or whose defaults are off, stays unknown
			// here; the runtime refuses its untyped aggregation first.
			sreq := composeSlotAsExecuted(s.req, opts)
			if requestLabelsDeferred(sreq) {
				continue
			}
			if _, ferr := seriesfield.ResolveComposeSlot(sreq.Aggregations, spec, specIdx, s.label); ferr != nil {
				addCodedError(env, ferr)
				appendComposeSlotPair(result, spec.Reference, s.label, "field-unknown")
			}
		}
	}
	refShape := inferComposeSlotShape(refReq)
	if refShape == types.OverlayShapeSeries {
		refKeys := composeSeriesKeyFields(refReq)
		for _, target := range resolved {
			if inferComposeSlotShape(target.req) != types.OverlayShapeSeries {
				continue
			}
			if tKeys := composeSeriesKeyFields(target.req); !composeStringTuplesEqual(refKeys, tKeys) {
				env.AddError(string(errors.PULSE_OVERLAY_KEY_SET_DIVERGENT),
					"compose overlay key set divergent: reference and target SERIES slots key on differently named group columns",
					map[string]any{
						"index":            specIdx,
						"reference":        spec.Reference,
						"target":           target.label,
						"reference_fields": refKeys,
						"target_fields":    tKeys,
					})
				appendComposeSlotPair(result, spec.Reference, target.label, "key-set-divergent")
			}
		}
	}
	if kindRequiresMatrixCompose(spec.Kind) && refShape != types.OverlayShapeMatrix {
		env.AddError(string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
			"compose overlay kind requires a MATRIX-shape host but the reference slot is not a crosstab",
			map[string]any{
				"index":          specIdx,
				"kind":           string(spec.Kind),
				"required_shape": "MATRIX",
				"target_label":   "reference",
				"observed_shape": string(refShape),
			})
		appendComposeSlotPair(result, spec.Reference, "", "slot-not-crosstab")
		// Continue checking per-target failures so the caller sees
		// every offending slot in one envelope.
	}

	for _, target := range resolved {
		tShape := inferComposeSlotShape(target.req)
		// Per-target shape divergence (ref vs target).
		if refShape != tShape {
			env.AddError(string(errors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT),
				"compose overlay reference and target slots produce different host result shapes",
				map[string]any{
					"index":           specIdx,
					"kind":            string(spec.Kind),
					"reference":       spec.Reference,
					"target_label":    target.label,
					"reference_shape": string(refShape),
					"target_shape":    string(tShape),
				})
			appendComposeSlotPair(result, spec.Reference, target.label, "slot-shape-divergent")
			continue
		}
		// Per-target matrix-required gate (kind requires MATRIX but
		// the target is non-MATRIX; the reference arm above already
		// flagged the symmetric case).
		if kindRequiresMatrixCompose(spec.Kind) && tShape != types.OverlayShapeMatrix {
			env.AddError(string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"compose overlay kind requires a MATRIX-shape host but a target slot is not a crosstab",
				map[string]any{
					"index":          specIdx,
					"kind":           string(spec.Kind),
					"required_shape": "MATRIX",
					"target_label":   target.label,
					"observed_shape": string(tShape),
				})
			appendComposeSlotPair(result, spec.Reference, target.label, "slot-not-crosstab")
			continue
		}
		// Per-target structural schema match. Compares the row /
		// column axis grouper-kind tuples drawn from each slot's
		// *types.Request directly — descriptor.ValidateCompose runs
		// before any execution so the post-execution
		// extractSchemaShape (internal/processing/) would have no Response
		// to read; the per-Request inference reads the same per-
		// grouper Kind tuples the matrix RowHeader / ColumnHeader
		// types would have echoed.
		if !composeAxisKindsEqual(refReq, target.req) {
			env.AddError(string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT),
				"compose overlay reference and target slots produce structurally different axis schemas",
				map[string]any{
					"index":            specIdx,
					"kind":             string(spec.Kind),
					"reference":        spec.Reference,
					"target_label":     target.label,
					"reference_schema": composeRequestSchemaCanonical(refReq),
					"target_schema":    composeRequestSchemaCanonical(target.req),
				})
			appendComposeSlotPair(result, spec.Reference, target.label, "schema-divergent")
		}
	}

	// Gate 5: a Compose OVERLAY_FORMULA reads only its namespace — the
	// reference slot's shape variables, its ref variable and the
	// `slot.<label>.<field>` paths of the slots it names. The runtime
	// dispatcher (processing.applyComposeFormula) raises the same
	// formulaident.ComposeRefusal before it compiles the formula.
	if opts.overlayRoute(spec.Kind) == types.OverlayKindFormula {
		slots := make([]formulaident.Slot, 0, 1+len(resolved))
		slots = append(slots, formulaident.Slot{Label: spec.Reference, Shape: refShape})
		for _, target := range resolved {
			slots = append(slots, formulaident.Slot{Label: target.label, Shape: inferComposeSlotShape(target.req)})
		}
		if cerr := formulaident.ComposeRefusal(spec.Kind, spec.Params, specIdx, refShape, slots, buildFormulaAllowedFuncs(opts)); cerr != nil {
			addCodedError(env, cerr)
		}
	}

	_ = req // reserved for future per-spec walks that need siblings
}

// composeSlotRowAxis returns one Compose slot's crosstab ROW axis, or
// nil when the slot declares no crosstab. Nil is the right answer for
// the panel's slab gate: a non-MATRIX slot is refused by the shape
// walk, and an empty axis has no dim to fan out at.
func composeSlotRowAxis(req *types.Request) []*types.Group {
	if req == nil || req.Crosstab == nil {
		return nil
	}
	return req.Crosstab.Rows
}

// composeBuildLabelSlotIndex maps each slot's resolved label to its
// authored ComposedRequest.Requests position. Same label rule
// composeBuildLabelIndex uses; on a collision the FIRST slot wins,
// matching that helper's map-fill order.
func composeBuildLabelSlotIndex(req *types.ComposedRequest) map[string]int {
	out := make(map[string]int, len(req.Requests))
	for i, src := range req.Requests {
		if src == nil {
			continue
		}
		label := src.Label
		if label == "" {
			label = composeDescriptorDefaultLabel(i)
		}
		if _, dup := out[label]; dup {
			continue
		}
		out[label] = i
	}
	return out
}

// composeBuildLabelIndex walks ComposedRequest.Requests and produces a
// final-label → *types.Request lookup. Mirrors the runtime
// composeDefaultLabel rule (`request_<i+1>` for empty Label, 1-based)
// so a Compose request that round-trips through Predict before
// service.applyComposeLabelDefaults sees the same slot labels the
// runtime would.
//
// Returns the second arg as the first colliding label when two slots
// resolve to the same final label; the caller emits
// PULSE_COMPOSE_LABEL_COLLISION and continues so the rest of the
// overlay walk can still surface per-spec failures.
func composeBuildLabelIndex(req *types.ComposedRequest) (map[string]*types.Request, string) {
	out := make(map[string]*types.Request, len(req.Requests))
	for i, src := range req.Requests {
		if src == nil {
			continue
		}
		label := src.Label
		if label == "" {
			label = composeDescriptorDefaultLabel(i)
		}
		if _, dup := out[label]; dup {
			return out, label
		}
		out[label] = src
	}
	return out, ""
}

// composeDescriptorDefaultLabel returns the synthesised default label
// for the slot at index i. Mirrors internal/processing/compose_overlay_resolve.go's
// composeDefaultLabel — both produce "request_<i+1>" (1-based). The two
// implementations are intentionally duplicated under the no-execute
// structural ban; TestComposeDescriptorDefaultLabel_MatchesProcessingHelper
// pins them in lockstep.
func composeDescriptorDefaultLabel(i int) string {
	return "request_" + composeDescriptorItoa(i+1)
}

// composeDescriptorItoa converts a non-negative integer to decimal
// without fmt.Sprintf, mirroring processing.composeItoa. Keeps the
// descriptor's JSON-bearing path grep-clean against the
// no-fmt.Sprintf-in-descriptor ban.
func composeDescriptorItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// composeOverlayKindKnown reports whether kind is in the catalog
// types.AllOverlayKinds() emits. The slot is iterated rather than
// table-lookup'd because the catalog is small (~34 entries) and the
// linear walk avoids a global package-level map allocation.
func composeOverlayKindKnown(kind types.OverlayKind) bool {
	for _, k := range types.AllOverlayKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// composeKindIsPanel reports whether kind is one of the two multi-
// reference panel kinds that enforce the MaxPanelTargets cap. Mirrors
// the runtime check inside the per-kind handlers — single-target
// kinds ignore the cap per OverlayOptions.MaxPanelTargets doc.
func composeKindIsPanel(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindPropZPanel, types.OverlayKindPanelIndexVsRef:
		return true
	}
	return false
}

// kindRequiresMatrixCompose mirrors processing.kindRequiresMatrix from
// internal/processing/compose_overlay_schemamatch.go. The catalog row is
// duplicated under the no-execute structural ban; the
// TestKindRequiresMatrixCompose_MatchesProcessing sync test in
// compose_test.go pins the two surfaces in lockstep so a new
// matrix-required kind cannot land in internal/processing/ without updating
// this row too.
func kindRequiresMatrixCompose(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindPropZCell,
		types.OverlayKindPropZPanel,
		types.OverlayKindTCell,
		types.OverlayKindZCell,
		types.OverlayKindChiSqVsRef,
		types.OverlayKindRank:
		return true
	}
	return false
}

// inferComposeSlotShape returns the OverlayShape a Compose slot's
// *types.Request slot will produce after execution. Mirrors
// inferChainStageShape and the runtime internal/processing/extractSchemaShape
// heuristic: MATRIX when Crosstab is set, SERIES when grouped, SCALAR
// otherwise (or for nil/empty requests).
//
// The helper is total over every *types.Request value — predicate-
// negative inputs (nil req, no aggregations) fall through to
// OverlayShapeScalar so the per-spec divergence walk does not panic
// on edge fixtures.
func inferComposeSlotShape(req *types.Request) types.OverlayShape {
	if req == nil {
		return types.OverlayShapeScalar
	}
	if req.Crosstab != nil {
		return types.OverlayShapeMatrix
	}
	if len(req.Aggregations) == 0 {
		return types.OverlayShapeScalar
	}
	if len(req.Groups) > 0 {
		return types.OverlayShapeSeries
	}
	return types.OverlayShapeScalar
}

// composeAxisKindsEqual reports whether the row and column grouper-
// kind tuples of two slots agree structurally. Field names are
// allowed to differ across slots — only the per-axis kind tuple is
// compared (mirrors internal/processing/axisKindsEqual + extractSchemaShape).
//
// Matrix arm: row axis = Crosstab.Rows[].Type; col axis =
// Crosstab.Columns[].Type. Both tuples come from the request
// directly; the runtime extractSchemaShape reads them from
// MatrixPayload.{RowHeader,ColumnHeader}.Types post-execution and
// the descriptor side reads them from the request pre-execution.
// Both surfaces yield identical strings because the matrix payload
// echoes the per-grouper Type verbatim.
//
// Series arm: row axis = Groups[].Field (NOT Type), aligned with the
// runtime extractSeriesAxisFields heuristic that drops field names
// from the Data row keys. Sorted to mirror the runtime sort. The
// column axis is empty for SERIES.
//
// Scalar arm: both axes empty.
func composeAxisKindsEqual(a, b *types.Request) bool {
	aRow, aCol := composeRequestAxisTuples(a)
	bRow, bCol := composeRequestAxisTuples(b)
	if !composeStringTuplesEqual(aRow, bRow) {
		return false
	}
	return composeStringTuplesEqual(aCol, bCol)
}

// composeRequestAxisTuples extracts the row and column grouper-kind
// tuples from a *types.Request. See composeAxisKindsEqual for the
// shape contract. Matrix arm reads Crosstab.{Rows,Columns}.Type;
// series arm reads sorted Groups[].Field (mirrors the runtime
// extractSeriesAxisFields rule); scalar arm returns empty tuples.
func composeRequestAxisTuples(req *types.Request) ([]string, []string) {
	if req == nil {
		return nil, nil
	}
	if req.Crosstab != nil {
		row := make([]string, 0, len(req.Crosstab.Rows))
		for _, g := range req.Crosstab.Rows {
			if g == nil {
				continue
			}
			row = append(row, string(g.Type))
		}
		col := make([]string, 0, len(req.Crosstab.Columns))
		for _, g := range req.Crosstab.Columns {
			if g == nil {
				continue
			}
			col = append(col, string(g.Type))
		}
		return row, col
	}
	if len(req.Groups) > 0 {
		row := make([]string, 0, len(req.Groups))
		for _, g := range req.Groups {
			if g == nil {
				continue
			}
			row = append(row, string(g.Type))
		}
		return row, nil
	}
	return nil, nil
}

// requestLabelsDeferred reports whether a Compose slot or chain stage
// Request carries an aggregation with neither a Label nor a Type: its
// output label is the smart default's TYPE_field, which only its
// (post-join) schema decides. Callers resolve defaults first
// (composeSlotAsExecuted, the chain's staged stages), so it is true
// only when that schema is unknown or defaults are off.
func requestLabelsDeferred(req *types.Request) bool {
	for _, a := range req.Aggregations {
		if a != nil && a.Label == "" && a.Type == "" {
			return true
		}
	}
	return false
}

// composeSlotInputSchema returns the schema Compose slot j executes
// over — its cohort's, or the joined one for a joined slot
// (joinedDefaultsSchema) — read through opts.SchemaLoader; nil when it
// cannot be read (no judgement). The runtime's twin is
// Service.requestInputSchema.
func composeSlotInputSchema(requests []*types.Request, opts *PredictOptions) func(j int) *encoding.Schema {
	return func(j int) *encoding.Schema {
		if j < 0 || j >= len(requests) || requests[j] == nil || requests[j].Cohort == nil {
			return nil
		}
		return joinedDefaultsSchema(requests[j], cohortSchemaFor(requests[j].Cohort, opts), opts)
	}
}

// composeSlotAsExecuted is a Compose slot as the runtime executes it
// for label resolution: when an aggregation is untyped and unlabelled,
// a clone with smart defaults resolved against the slot's cohort
// schema, or the joined schema for a joined slot (joinedDefaultsSchema
// — processWithJoin's preflight); otherwise the slot itself. Header and
// schema reads only.
func composeSlotAsExecuted(req *types.Request, opts *PredictOptions) *types.Request {
	if !requestLabelsDeferred(req) || opts == nil || opts.DisableDefaults {
		return req
	}
	schema := joinedDefaultsSchema(req, cohortSchemaFor(req.Cohort, opts), opts)
	if schema == nil {
		return req
	}
	return defaultedForValidation(req, schema, opts)
}

// composeSeriesKeyFields returns the sorted group key columns a SERIES
// slot's Data rows key on (vectors.GroupKeyField of every Groups
// entry) — the column names the runtime row key encodes, in its sorted
// order.
func composeSeriesKeyFields(req *types.Request) []string {
	out := make([]string, 0, len(req.Groups))
	for _, g := range req.Groups {
		if g != nil {
			out = append(out, vectors.GroupKeyField(g.Type, g))
		}
	}
	sort.Strings(out)
	return out
}

// composeStringTuplesEqual reports byte-equality between two string
// slices. Nil and empty slices compare equal — both are the "no axis"
// shape. Order-sensitive per the runtime axisKindsEqual contract.
func composeStringTuplesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// composeRequestSchemaCanonical renders the per-slot axis tuples as a
// deterministic string for the envelope Details payload. Format
// mirrors internal/processing/composeOverlaySchemaShape.canonical:
// "<rowKinds>/<colKinds>" with each half joined by "|". Empty axis
// halves render as "" so the slash is always present, making the
// two-axis nature visually unambiguous in MCP error payloads.
func composeRequestSchemaCanonical(req *types.Request) string {
	row, col := composeRequestAxisTuples(req)
	return composeJoinKindTuple(row) + "/" + composeJoinKindTuple(col)
}

// composeJoinKindTuple joins a string slice with "|" without going
// through strings.Join (kept inline to mirror the runtime helper that
// avoids the import on the hot path). Empty slices render as "".
func composeJoinKindTuple(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "|" + parts[i]
	}
	return out
}

// appendComposeSlotPair pushes one SlotPair entry onto the result's
// divergence slice. No-op when result is nil. Centralises the slice
// append so the per-spec validator does not sprinkle the empty
// reference / target normalization across every emission site.
func appendComposeSlotPair(result *ComposeValidationResult, ref, target, reason string) {
	if result == nil {
		return
	}
	result.OverlaysSchemaDivergence = append(result.OverlaysSchemaDivergence, descriptor.SlotPair{
		ReferenceLabel: ref,
		TargetLabel:    target,
		Reason:         reason,
	})
}

// validateComposeSlots runs each slot's runtime-entry checks in the
// runtime's order — the join-count rule (JoinCountRefusal), the
// join-key rule for a join, zones the way the runtime resolves them
// (defaults, then ResolveZones), then the field-reference rule
// (FieldRefRefusals) on the defaulted slot, then weight resolution
// (ResolveWeights) when the references pass — and records each refusal
// tagged with its slot index. Without a SchemaLoader (or for a cohort it
// cannot read) the slot resolves schema-less: the field-independent
// refusals still apply, the field-dependent ones are left to the
// runtime.
func validateComposeSlots(env *descriptor.Envelope, req *types.ComposedRequest, opts *PredictOptions, loaded *composeLoaded, summaries []descriptor.FitSummary) {
	// The names every slot's ATTR_REG_PREDICT resolves: the runtime's
	// fit pre-pass binds each `fits` entry before any slot runs, over
	// the registered populations, beside the slot's own loaded entries.
	fits := model.WithinCatalog(opts.populations(), composeFitCatalog(req.Fits, summaries))
	for i, slot := range req.Requests {
		if slot == nil {
			continue
		}
		validateComposeRequest(env, slot, opts, "request", i, loadedCatalog(fits, model.RequestRefs(slot), loaded.slot(i)))
	}
}

// composeFitCatalog is model.CatalogFromFits with each vector fit's
// member labels filled in from its resolved battery (summaries, the
// validateComposeFits result, index-aligned with fits): the labels the
// runtime's fit captures, so field-reference step 12 aligns a slot's
// params.fit to them (vectors.AlignFit) exactly as the bind does. A
// battery that did not resolve keeps no labels (its fit's own refusal
// is reported, and the slot's alignment is not judged).
func composeFitCatalog(fits []types.FitSpec, summaries []descriptor.FitSummary) model.FitCatalog {
	cat := model.CatalogFromFits(fits)
	for i, f := range fits {
		if i >= len(summaries) || summaries[i].Vector == nil || summaries[i].Vector.Labels == nil {
			continue
		}
		if sig, ok := cat[f.Name]; ok && sig.Kind == types.FitKindVector {
			sig.Members = append([]string(nil), summaries[i].Vector.Labels...)
			cat[f.Name] = sig
		}
	}
	return cat
}

// composeLoaded is predict's Compose pre-pass result: each fit
// request's and each slot's loaded model and population files,
// index-aligned.
type composeLoaded struct {
	fits  []*model.Loaded
	slots []*model.Loaded
}

func (l *composeLoaded) fit(i int) *model.Loaded {
	if l == nil || i >= len(l.fits) {
		return nil
	}
	return l.fits[i]
}

func (l *composeLoaded) slot(i int) *model.Loaded {
	if l == nil || i >= len(l.slots) {
		return nil
	}
	return l.slots[i]
}

// loadComposeModels is the runtime's Compose pre-pass
// (Service.composeModelsPrepass): model.CheckComposeBatch, then every
// fit request's `models` and `populations` files read, then every
// slot's, each refusal
// located by details.fit / details.request.
func loadComposeModels(req *types.ComposedRequest, opts *PredictOptions) (*composeLoaded, error) {
	if cerr := model.CheckComposeBatch(req.Fits, req.Requests, opts.populations()); cerr != nil {
		return nil, cerr
	}
	out := &composeLoaded{fits: make([]*model.Loaded, len(req.Fits)), slots: make([]*model.Loaded, len(req.Requests))}
	for i, f := range req.Fits {
		if f.Request == nil {
			continue
		}
		files, cerr := opts.loadModelFiles(model.RequestRefs(f.Request))
		if cerr != nil {
			return nil, RefusalAt(cerr, "fit", i)
		}
		out.fits[i] = files
	}
	for k, slot := range req.Requests {
		if slot == nil {
			continue
		}
		files, cerr := opts.loadModelFiles(model.RequestRefs(slot))
		if cerr != nil {
			return nil, RefusalAt(cerr, "request", k)
		}
		out.slots[k] = files
	}
	return out, nil
}

// validateComposeRequest runs one compose-batch request's runtime-entry
// checks (validateComposeSlots' order) and records each refusal located
// under key = idx — details.request for a slot, details.fit for a fit.
// models are the names its ATTR_REG_PREDICT slots resolve (nil: none).
// It returns the schema the request executes over (nil when
// unresolvable) and whether every check passed.
func validateComposeRequest(env *descriptor.Envelope, slot *types.Request, opts *PredictOptions, key string, idx int, models model.Catalog) (*encoding.Schema, bool) {
	l := opts.instance().Limits()
	// The request's Process expands its item for_each first (right
	// after the slot gate the whole batch already passed).
	expanded, _, _, xerr := ExpandRequestItems(slot, l)
	if xerr != nil {
		addCodedError(env, RefusalAt(xerr, key, idx))
		return nil, false
	}
	slot = expanded
	// The request's Process refuses a second JoinSpec before it
	// resolves a zone; Compose locates it like a zone refusal.
	if jerr := JoinCountRefusal(slot); jerr != nil {
		addCodedError(env, RefusalAt(jerr, key, idx))
		return nil, false
	}
	// Then the matrix-host rule (matrices with joins or a crosstab).
	if merr := mergegate.MatrixRefusal(slot); merr != nil {
		addCodedError(env, RefusalAt(merr, key, idx))
		return nil, false
	}
	// Then the request's `return` block (ResolveReturn — the pass its
	// Process runs before dispatch).
	if _, rerr := ResolveReturn(slot, opts.instance()); rerr != nil {
		addCodedError(env, RefusalAt(rerr, key, idx))
		return nil, false
	}
	schema, keyRefusals := validatorRequestSchema(slot, cohortSchemaFor(slot.Cohort, opts), opts)
	if len(keyRefusals) > 0 {
		for _, ce := range keyRefusals {
			addCodedError(env, RefusalAt(ce, key, idx))
		}
		return schema, false
	}
	if _, err := resolveRequestZones(slot, schema, opts); err != nil {
		addCodedError(env, RefusalAt(err, key, idx))
		return schema, false
	}
	defaulted := defaultedForValidation(slot, schema, opts)
	refs := fieldRefRefusalsWithModels(defaulted, schema, extensionsFromOpts(opts), opts.instance(), models)
	for _, ce := range refs {
		addCodedError(env, RefusalAt(ce, key, idx))
	}
	if len(refs) > 0 {
		return schema, false
	}
	// `return` data columns, right after the field references (the
	// runtime's checkFieldRefs order).
	if cerr := ReturnColumnRefusal(defaulted, schema, opts.instance()); cerr != nil {
		addCodedError(env, RefusalAt(cerr, key, idx))
		return schema, false
	}
	// The request's Process resolves its weights right after its field
	// references pass (ResolveWeights).
	if _, werr := resolveRequestWeights(slot, schema, opts); werr != nil {
		addCodedError(env, RefusalAt(werr, key, idx))
		return schema, false
	}
	return schema, true
}

// validateComposeFits is the fit half of PredictCompose, run once the
// structural rules (model.CheckFits) passed: each fit request gets the
// slot checks (validateComposeRequest, details.fit) and then, for a
// regression fit, predict's regression rules over the schema it executes
// over, located the same way. It returns the static per-fit summary in
// declaration order (nil without fits): a regression fit's name,
// regression type, target, the predictors as written and the cohort; a
// vector fit's name, cohort and battery (vectorFitSummary).
func validateComposeFits(env *descriptor.Envelope, fits []types.FitSpec, opts *PredictOptions, loaded *composeLoaded) []descriptor.FitSummary {
	if len(fits) == 0 {
		return nil
	}
	out := make([]descriptor.FitSummary, len(fits))
	for i, f := range fits {
		req := model.FitRequest(f)
		vector := model.FitKindOf(req) == types.FitKindVector
		out[i] = descriptor.FitSummary{Name: f.Name}
		if vector {
			out[i].Vector = &descriptor.VectorFitSummary{Operator: vectorFitOperator(req)}
		} else {
			reg := req.Regressions[0]
			out[i].Type, out[i].Target = reg.Type, reg.Target
			out[i].Predictors = append([]string{}, reg.Predictors...)
		}
		if req.Cohort != nil {
			out[i].Cohort = cohortPathFor(req.Cohort)
		}
		// A fit request runs before the batch binds anything: it sees
		// only the registered populations and its own loaded entries.
		schema, ok := validateComposeRequest(env, req, opts, "fit", i, loadedCatalog(opts.populations(), model.RequestRefs(req), loaded.fit(i)))
		if !ok || schema == nil {
			continue
		}
		if vector {
			out[i].Vector.Labels, out[i].Vector.Members = vectorFitAxis(req, schema, opts)
			continue
		}
		scratch := descriptor.NewEnvelope(nil)
		validateRegressions(scratch, req, schema, nil, opts.instance())
		for _, e := range scratch.Errors {
			details := make(map[string]any, len(e.Details)+1)
			for k, v := range e.Details {
				details[k] = v
			}
			details["fit"] = i
			env.AddError(e.Code, e.Message, details)
		}
	}
	return out
}

// vectorFitOperator is a vector fit request's battery type: its one
// matrix's, else its one fitted attribute's (model.VectorBatteries).
func vectorFitOperator(req *types.Request) string {
	if len(req.Matrices) == 1 {
		return string(req.Matrices[0].Type)
	}
	for _, a := range req.Attributes {
		if a != nil && model.IsVectorFitAttribute(a.Type) {
			return string(a.Type)
		}
	}
	return ""
}

// vectorFitAxis resolves a vector fit request's battery against schema
// and returns the labels a consuming slot aligns to and the member
// columns, in axis order — what the runtime captures (a matrix's
// FitLabels / Columns; a fitted attribute's FittedLabels / members). Nil
// when the battery does not resolve (the walk already refused it).
func vectorFitAxis(req *types.Request, schema *encoding.Schema, opts *PredictOptions) (labels, members []string) {
	if len(req.Matrices) == 1 {
		mats, err := resolveMatricesFor(req, schema, nil, opts.instance())
		if err != nil || len(mats) != 1 {
			return nil, nil
		}
		return mats[0].FitLabels(), append([]string(nil), mats[0].Columns()...)
	}
	inputs, err := vectors.ResolveAttributes(req, schema, func(t types.AttributeType) types.AttributeType {
		return opRoute(opts.instance(), t)
	}, nil)
	if err != nil {
		return nil, nil
	}
	for i, a := range req.Attributes {
		if a == nil || !model.IsVectorFitAttribute(a.Type) || i >= len(inputs) || !inputs[i].Has() {
			continue
		}
		m := inputs[i].Members
		l, lerr := vectors.FittedLabels("", a.Type, a.Params, m)
		if lerr != nil {
			return nil, nil
		}
		return l, append([]string(nil), m.Members...)
	}
	return nil, nil
}

// composeFormulaBuiltinRefusal is PULSE_OVERLAY_FORMULA_INVALID_IDENT
// for a Compose-host OVERLAY_FORMULA whose Params["formula"] calls a
// reserved built-in outside the overlay scalar-maths subset (the first
// such callee, internal/exprident.FirstCallee with
// exprbuiltins.OverlayRefused — the runtime compile's own rule). nil
// for any other kind, a missing / non-string / unparsable formula (the
// parse error surfaces elsewhere) or a formula calling none. route is
// the instance's route of spec.Kind.
func composeFormulaBuiltinRefusal(spec *types.ComposeOverlaySpec, specIdx int, route types.OverlayKind) *errors.CodedError {
	if route != types.OverlayKindFormula {
		return nil
	}
	formula, _ := spec.Params["formula"].(string)
	refused := exprident.FirstCallee(formula, exprbuiltins.OverlayRefused)
	if refused == "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT,
		"overlay "+string(spec.Kind)+" formula calls "+refused+", a vector built-in OVERLAY_FORMULA does not accept (scalar maths only: "+strings.Join(exprbuiltins.OverlayNames(), ", ")+")",
		map[string]any{
			"index":              specIdx,
			"kind":               string(spec.Kind),
			"unknown_identifier": refused,
			"allowed_builtins":   exprbuiltins.OverlayNames(),
			"formula":            formula,
		})
}

// composeFormulaTotalRefusal is PULSE_FORMULA_TOTAL_SITE for a
// Compose-host OVERLAY_FORMULA whose Params["formula"] calls total()
// (internal/totalcall.Refusal at the OVERLAY_FORMULA site), details
// carrying the spec index and authored kind beside the refusal's own.
// nil for any other kind, a missing or non-string formula, or a formula
// without total(). route is the instance's route of spec.Kind.
func composeFormulaTotalRefusal(spec *types.ComposeOverlaySpec, specIdx int, route types.OverlayKind) *errors.CodedError {
	if route != types.OverlayKindFormula {
		return nil
	}
	formula, _ := spec.Params["formula"].(string)
	cerr := totalcall.Refusal(formula, string(types.OverlayKindFormula))
	if cerr == nil {
		return nil
	}
	details := map[string]any{"index": specIdx, "kind": string(spec.Kind)}
	for k, v := range cerr.Details {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(cerr.Code, cerr.Message, details)
}
