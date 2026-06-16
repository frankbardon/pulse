package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// E7-S12 runtime handler for OVERLAY_PANEL_INDEX_VS_REF — the
// multi-reference DESCRIPTIVE COMPOSE-host ratio index that emits ONE
// OverlayLayer per target slot against a single shared reference slot.
// Each emitted layer is mathematically equivalent to
// OVERLAY_INDEX_VS_REF for that single (reference, target[i]) pair —
// the handler reuses the single-target `applyIndexVsRef` math verbatim
// per target, then renames the resulting layer per the
// `<spec.Name>__<target_label>` template documented on the kind's
// catalog row.
//
// Multi-layer chassis surface: PANEL_INDEX_VS_REF is the first kind
// to consume the `composeOverlayMultiLayerHandler` signature added in
// E7-S12 — the chassis dispatch in ApplyComposeOverlays checks the
// multi-layer dispatch table FIRST and appends every emitted layer to
// the parent layers slice in (spec order × target order). The
// PROP_Z_PANEL pattern from E7-S11 encoded its N-slot output inside a
// single layer's per-cell vector; PANEL_INDEX_VS_REF emits N layers
// instead so renderers see the panel as N parallel index strips
// sharing one coord canvas.
//
// Cap enforcement: identical to OVERLAY_PROP_Z_PANEL — the catalog
// standardises on `OverlayOptions.MaxPanelTargets` (default 16) as the
// single panel-cap surface so renderers can budget panel-layout state
// against one knob. `len(spec.Targets) > MaxPanelTargets` fires
// PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry. The cap
// counts target slots only — the reference is always present and does
// not count against the cap (so an authoring shape with 16 targets is
// valid against the default 16; 17 targets fail).
//
// Shared coord space: the schema-match (E7-S7) and key-alignment
// (E7-S6) gates already enforce coord-space identity across every
// (reference, target) pair BEFORE this handler dispatches, so every
// emitted layer's row/col axis descriptors are byte-identical (matrix
// host) or share the same group key set (series host) by construction.
// The handler relies on that invariant rather than re-validating per
// emission.

// panelLayerName builds the renderer-facing label for one emitted
// PANEL_INDEX_VS_REF layer. Template: `<spec.Name>__<target_label>`.
// When `spec.Name == ""` the chassis fallback rule (mirrors
// `composeOverlayLayerName`) degenerates to
// `<kind>__<target_label>` so the panel still has a stable handle
// per target against an unnamed authoring shape.
func panelLayerName(spec *types.ComposeOverlaySpec, targetLabel string) string {
	base := spec.Name
	if base == "" {
		base = string(spec.Kind)
	}
	return base + "__" + targetLabel
}

// applyPanelIndexVsRef is the COMPOSE-host multi-layer runtime handler
// for OVERLAY_PANEL_INDEX_VS_REF. Emits `len(targets)` layers — one per
// (reference, target[i]) pair — each layer reusing the single-target
// `applyIndexVsRef` math arm for that pair and then renamed per the
// `<spec.Name>__<target_label>` template.
//
// Shape resolution: each emitted layer's shape follows the host-shape
// disambiguator the single-target sibling uses — MATRIX host
// (reference + target carry crosstab matrices) emits MATRIX layers;
// SERIES host (reference + target carry Response.Data row slices)
// emits SERIES layers. Dispatch via `composeHostIsSeries` per target,
// the same per-target shape gate that drives `applyIndexVsRef`.
//
// Layer slice order: spec order then target order — `layers[i]`
// corresponds to `spec.Targets[i]` so renderers can address the
// emitted panel by target offset without any auxiliary index map.
// Stable across re-runs of the same spec.
func applyPanelIndexVsRef(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) ([]types.OverlayLayer, []types.OverlayWarning, error) {
	// Cap enforcement runs FIRST — an over-cap panel fails loud before
	// any per-target work happens. The cap counts target slots only; the
	// reference is always present and does not count.
	cap := resolveMaxPanelTargets(spec.Options)
	if len(targets) > cap {
		return nil, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay "+string(spec.Kind)+" exceeded the per-spec MaxPanelTargets cap",
			map[string]any{
				"code":     string(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP),
				"kind":     string(spec.Kind),
				"observed": len(targets),
				"cap":      cap,
			})
	}

	layers := make([]types.OverlayLayer, 0, len(targets))
	var warnings []types.OverlayWarning

	for i, target := range targets {
		// Build a single-target ComposeOverlaySpec shim so the reused
		// applyIndexVsRef handler sees the same authoring shape it would
		// see if the caller had supplied one INDEX_VS_REF spec per
		// target. The shim carries the same Reference, the same Scope,
		// the same Params (so caller-supplied `Params["scale"]`
		// propagates through every emitted layer), and a Targets slice
		// containing only the current target's label. spec.Options does
		// NOT propagate — the cap check above already gated the panel
		// shape and propagating MaxPanelTargets to the single-target
		// sibling would be misleading.
		targetLabel := ""
		if i < len(spec.Targets) {
			targetLabel = spec.Targets[i]
		}
		targetIdx := -1
		if i < len(targetIdxs) {
			targetIdx = targetIdxs[i]
		}
		shim := types.ComposeOverlaySpec{
			Name:      spec.Name,
			Kind:      types.OverlayKindIndexVsRef,
			Scope:     spec.Scope,
			Reference: spec.Reference,
			Targets:   []string{targetLabel},
			Params:    spec.Params,
		}
		layer, ws, err := applyIndexVsRef(&shim, reference, []*types.Response{target}, refIdx, []int{targetIdx})
		if err != nil {
			return nil, nil, err
		}
		// Rename the layer per the panel naming convention. The reused
		// applyIndexVsRef stamped the layer's Name + CellLabel from the
		// shim's spec.Name (or kind fallback) via composeOverlayLayerName
		// — overwrite both slots so the rendered panel surface carries
		// the per-target handle the kind's catalog row documents.
		panelName := panelLayerName(spec, targetLabel)
		layer.Name = panelName
		// Echo the panel kind on the emitted layer so the renderer sees
		// the parent kind (PANEL_INDEX_VS_REF), not the underlying
		// single-target sibling (INDEX_VS_REF).
		layer.Kind = spec.Kind
		// MATRIX payload carries a CellLabel slot — keep it aligned with
		// the renderer-facing Name so the matrix grid's per-cell header
		// reflects the panel target. Nil-safe (SERIES emissions leave
		// the matrix slot empty).
		if layer.Payload.Matrix != nil {
			layer.Payload.Matrix.CellLabel = panelName
		}
		layers = append(layers, layer)
		if len(ws) > 0 {
			warnings = append(warnings, ws...)
		}
	}

	return layers, warnings, nil
}
