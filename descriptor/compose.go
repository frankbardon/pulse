package descriptor

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E7-S14: predict-time validator for ComposedRequest.Overlays.
//
// ValidateCompose is the no-execute descriptor mirror of the runtime
// COMPOSE-host overlay gates (E7-S6 strict key-set alignment, E7-S7
// structural schema match, E7-S11 panel target cap). It walks
// ComposedRequest.Overlays and surfaces every per-spec failure that can
// be detected from header + schema alone — every other Compose overlay
// failure mode (key-set alignment, categorical dict-prefix drift) is
// out of scope per the story acceptance list because it requires
// record-level visibility the no-execute path does not have.
//
// The validator stays inside descriptor/ — no service / processing
// import. The helpers that mirror processing/-side normalisers
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
	OverlaysSchemaDivergence []SlotPair `json:"overlays_schema_divergence"`

	// OverlayCost maps each COMPOSE-host overlay-spec Name to a coarse
	// cost score the routing layer can consult before execution. Sibling
	// to PredictResult.OverlayCost / FacetValidationResult.OverlayCost —
	// streamable kinds carry overlayCostStreamable (~5% extra work) and
	// buffered kinds carry overlayCostBuffered (~one extra payload
	// traversal). E10-S3 lands this slot alongside multi-reference cost
	// scaling: the two multi-ref kinds (OVERLAY_PROP_Z_PANEL,
	// OVERLAY_PANEL_INDEX_VS_REF) scale the base cost by
	// `min(len(spec.Targets), MaxPanelTargets)` so renderers can budget
	// per-panel fan-out without re-deriving the cap. Single-target kinds
	// ignore the Targets slice and surface the raw kind cost. Per
	// kind-catalog-v1 PRD §I-FR-I3 the value is intentionally coarse —
	// callers budgeting cost across slots sum the map values. Empty (but
	// non-nil) when req.Overlays is empty; never nil in JSON output.
	OverlayCost map[string]float64 `json:"overlay_cost"`
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
func ValidateCompose(req *types.ComposedRequest) *Envelope {
	result := &ComposeValidationResult{
		Valid:                    true,
		Request:                  req,
		OverlaysSchemaDivergence: []SlotPair{},
		OverlayCost:              map[string]float64{},
	}
	env := NewEnvelope(result)

	if req == nil {
		env.AddError(string(errors.SERVICE_VALIDATION), "compose request is required", nil)
		result.Valid = false
		return env
	}
	if len(req.Overlays) == 0 {
		// Nothing to validate at the overlay surface; downstream
		// per-slot validation (the standard Predict on each Request)
		// runs through a separate entry point.
		return env
	}

	// Build slot label → (slot index, *Request) lookup. Mirrors the
	// runtime composeDefaultLabel rule in
	// processing/compose_overlay_resolve.go: empty Label →
	// "request_<i+1>" (1-based). Collisions are reported the same
	// way the runtime applyComposeLabelDefaults does so the
	// descriptor surface stays parity-true.
	byLabel, labelCollision := composeBuildLabelIndex(req)
	if labelCollision != "" {
		env.AddError(string(errors.PULSE_COMPOSE_LABEL_COLLISION),
			"compose request has two slots resolving to the same label: "+labelCollision,
			map[string]any{"label": labelCollision})
		// Continue — label collisions do not block the overlay
		// walk from surfacing other failures the caller would
		// otherwise have to round-trip to discover.
	}

	for i, spec := range req.Overlays {
		validateComposeOverlaySpec(env, result, &spec, i, byLabel, req)
		// Populate the per-spec cost score in matching order — even when
		// the spec fails a downstream gate (the cost map is descriptive,
		// not gated by validity). The cost dispatcher reads the spec's
		// kind + Targets + Options so a rejected spec still surfaces its
		// budget. Mirrors the per-spec cost emission rule on
		// PredictResult.OverlayCost / FacetValidationResult.OverlayCost.
		name := composeOverlayDescriptorName(&spec)
		result.OverlayCost[name] = composeOverlayCostForSpec(&spec)
	}

	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return env
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
func validateComposeOverlaySpec(env *Envelope, result *ComposeValidationResult, spec *types.ComposeOverlaySpec, specIdx int, byLabel map[string]*types.Request, req *types.ComposedRequest) {
	// Gate 0: unknown kind. The catalog lookup runs against
	// types.AllOverlayKinds() so a new kind shows up here automatically
	// once it's appended to the catalog.
	if !composeOverlayKindKnown(spec.Kind) {
		env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
			"compose overlay spec carries unknown kind: "+string(spec.Kind),
			map[string]any{"index": specIdx, "kind": string(spec.Kind)})
		appendComposeSlotPair(result, spec.Reference, "", "kind-unknown")
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

	// Gate 4: per-target shape + schema match. The reference shape
	// is the per-slot Request shape, NOT the overlay's scope; the
	// scope discriminates within the host result, the shape is the
	// host itself. The kindRequiresMatrix table mirrors the runtime
	// processing.kindRequiresMatrix (see compose_test.go for the
	// lock-step sync test).
	refShape := inferComposeSlotShape(refReq)
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
		// extractSchemaShape (processing/) would have no Response
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

	_ = req // reserved for future per-spec walks that need siblings
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
// for the slot at index i. Mirrors processing/compose_overlay_resolve.go's
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
// processing/compose_overlay_schemamatch.go. The catalog row is
// duplicated under the no-execute structural ban; the
// TestKindRequiresMatrixCompose_MatchesProcessing sync test in
// compose_test.go pins the two surfaces in lockstep so a new
// matrix-required kind cannot land in processing/ without updating
// this row too.
func kindRequiresMatrixCompose(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindPropZCell,
		types.OverlayKindPropZPanel,
		types.OverlayKindTCell,
		types.OverlayKindChiSqVsRef,
		types.OverlayKindRank:
		return true
	}
	return false
}

// inferComposeSlotShape returns the OverlayShape a Compose slot's
// *types.Request slot will produce after execution. Mirrors
// inferChainStageShape and the runtime processing/extractSchemaShape
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
// compared (mirrors processing/axisKindsEqual + extractSchemaShape).
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
// mirrors processing/composeOverlaySchemaShape.canonical:
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
	result.OverlaysSchemaDivergence = append(result.OverlaysSchemaDivergence, SlotPair{
		ReferenceLabel: ref,
		TargetLabel:    target,
		Reason:         reason,
	})
}
