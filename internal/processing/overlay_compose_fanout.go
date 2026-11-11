package processing

import (
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// Compose fan-out (U66 #443). A single-target Compose kind once scored
// Targets[0] and silently dropped every later target. The fan-out kinds
// (overlayhost.ComposeFanOutKind) sit in composeOverlayMultiLayerHandlers
// beside OVERLAY_PANEL_INDEX_VS_REF and emit one layer per target, in
// Targets order: each layer is exactly the single-target handler's
// output for its (reference, target) pair. One target is byte-identical
// to the single-target layer (same Name, same payload, same warnings).

// composeFanOutLayers runs one (a single-target handler) once per
// target, against a copy of spec naming that target alone, with the
// shim's Kind set to shimKind (the panel scores its targets as
// OVERLAY_INDEX_VS_REF). Each layer is renamed name(label, n) — the
// MATRIX payload's CellLabel with it — and echoes spec.Kind. The third
// result is each warning's layer offset inside the returned layers, so
// the dispatcher routes it to its own target's layer.
func composeFanOutLayers(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int, one composeOverlayHandler, shimKind types.OverlayKind, name func(targetLabel string, n int) string) ([]types.OverlayLayer, []types.OverlayWarning, []int, error) {
	layers := make([]types.OverlayLayer, 0, len(targets))
	var (
		warnings []types.OverlayWarning
		at       []int
	)
	for i, target := range targets {
		targetLabel := ""
		if i < len(spec.Targets) {
			targetLabel = spec.Targets[i]
		}
		// The shim is the spec itself — Name, Scope, Level / Within,
		// Params and Options all carried — naming only this target, so
		// the single-target handler sees what one spec per target would
		// have handed it.
		shim := *spec
		shim.Kind = shimKind
		shim.Targets = []string{targetLabel}
		layer, ws, err := one(&shim, reference, []*types.Response{target}, refIdx, []int{composeTargetIdx(targetIdxs, i)})
		if err != nil {
			return nil, nil, nil, err
		}
		layer.Name = name(targetLabel, len(targets))
		layer.Kind = spec.Kind
		if layer.Payload.Matrix != nil {
			layer.Payload.Matrix.CellLabel = layer.Name
		}
		layers = append(layers, layer)
		for range ws {
			at = append(at, i)
		}
		warnings = append(warnings, ws...)
	}
	return layers, warnings, at, nil
}

// composeFanOut is the multi-layer table entry of a single-target
// fan-out kind: one layer per target, named
// overlayhost.ComposeFanOutLayerName (the spec's Name for one target,
// `<name>__<target label>` for two or more). The MaxPanelTargets cap is
// the dispatcher's (overlayhost.ComposeFanOutCapRefusal, the rule
// predict raises at its Gate 3), judged before any slot gate.
func composeFanOut(one composeOverlayHandler) composeOverlayMultiLayerHandler {
	return func(spec *types.ComposeOverlaySpec, reference *types.Response, targets []*types.Response, refIdx int, targetIdxs []int) ([]types.OverlayLayer, []types.OverlayWarning, []int, error) {
		return composeFanOutLayers(spec, reference, targets, refIdx, targetIdxs, one, spec.Kind, func(targetLabel string, n int) string {
			return overlayhost.ComposeFanOutLayerName(spec, targetLabel, n)
		})
	}
}
