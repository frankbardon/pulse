package overlayhost

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Compose fan-out (U66 #443). A Compose kind that compares ONE target
// slot with the reference once scored Targets[0] alone and dropped every
// later target without a word. The fan-out kinds below emit one
// OverlayLayer per target instead, in Targets order, through the
// runtime's multi-layer handler table (the OVERLAY_PANEL_INDEX_VS_REF
// precedent). Predict (internal/descriptor: the cap at Gate 3, the
// p-value count, the overlay cost) and the runtime (the Compose
// dispatcher's cap, the warning routing and the multiplicity fold's
// layer → spec map) read the same rules here.

// DefaultComposeTargetCap is OverlayOptions.MaxPanelTargets' default:
// the most targets one multi-target Compose spec may name.
const DefaultComposeTargetCap = 16

// ComposeTargetCap is the active target cap of a Compose spec's
// Options: MaxPanelTargets when > 0, else DefaultComposeTargetCap.
func ComposeTargetCap(opts *types.OverlayOptions) int {
	if opts == nil || opts.MaxPanelTargets <= 0 {
		return DefaultComposeTargetCap
	}
	return opts.MaxPanelTargets
}

// composeFanOutKinds are the single-target Compose kinds that fan out
// to one layer per target (U66 #443): the series pair kinds and the
// matrix cell / whole-matrix kinds. Every other Compose kind reads all
// its targets at once — OVERLAY_FORMULA (host = the first target, the
// others as `slot.<label>`) and the two panel kinds.
var composeFanOutKinds = map[types.OverlayKind]bool{
	types.OverlayKindIndexVsRef: true,
	types.OverlayKindDeltaVsRef: true,
	types.OverlayKindTVsRef:     true,
	types.OverlayKindZVsRef:     true,
	types.OverlayKindTCell:      true,
	types.OverlayKindZCell:      true,
	types.OverlayKindPropZCell:  true,
	types.OverlayKindChiSqVsRef: true,
	types.OverlayKindRank:       true,
}

// ComposeFanOutKind reports whether kind is a single-target Compose
// kind that emits one layer per target. The panel kinds are not: they
// were multi-target from the start and keep their own cap arms.
func ComposeFanOutKind(kind types.OverlayKind) bool {
	return composeFanOutKinds[kind]
}

// ComposeFanOutKinds lists the fan-out kinds, in catalog order.
func ComposeFanOutKinds() []types.OverlayKind {
	out := make([]types.OverlayKind, 0, len(composeFanOutKinds))
	for _, k := range types.AllOverlayKinds() {
		if composeFanOutKinds[k] {
			out = append(out, k)
		}
	}
	return out
}

// ComposeMultiLayerKind reports whether a Compose spec of kind emits
// one OverlayLayer per target: a fan-out kind or
// OVERLAY_PANEL_INDEX_VS_REF. Every other kind emits one layer.
func ComposeMultiLayerKind(kind types.OverlayKind) bool {
	return kind == types.OverlayKindPanelIndexVsRef || composeFanOutKinds[kind]
}

// ComposeLayerCount is how many OverlayLayer entries a Compose spec of
// route emits on success: len(Targets) for a multi-layer kind, else 1.
// The runtime appends them in spec order, then target order, so layer
// positions follow from the specs alone.
func ComposeLayerCount(spec *types.ComposeOverlaySpec, route types.OverlayKind) int {
	if spec != nil && ComposeMultiLayerKind(route) && len(spec.Targets) > 1 {
		return len(spec.Targets)
	}
	return 1
}

// ComposeLayerSpecs maps each OverlayLayer position of a successful
// Compose fold to the index of the spec that emitted it, and each
// position to its target offset inside that spec. route resolves a
// spec's kind (nil = the authored kind).
func ComposeLayerSpecs(specs []types.ComposeOverlaySpec, route func(types.OverlayKind) types.OverlayKind) (spec, target []int) {
	for i := range specs {
		r := specs[i].Kind
		if route != nil {
			r = route(r)
		}
		for t := 0; t < ComposeLayerCount(&specs[i], r); t++ {
			spec = append(spec, i)
			target = append(target, t)
		}
	}
	return spec, target
}

// ComposeFanOutLayerName is the Name of a fan-out layer for target
// label among n targets: the spec's Name (else its kind) for one
// target — byte-identical to the single-target layer — and
// `<name>__<target label>` (the panel convention) for two or more.
func ComposeFanOutLayerName(spec *types.ComposeOverlaySpec, targetLabel string, n int) string {
	base := spec.Name
	if base == "" {
		base = string(spec.Kind)
	}
	if n <= 1 {
		return base
	}
	return base + "__" + targetLabel
}

// ComposeFanOutCapRefusal refuses a fan-out spec naming more targets
// than its MaxPanelTargets cap with PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP,
// Details {index, kind, observed, cap}; nil for any other spec. Predict
// raises it at Gate 3, the runtime dispatcher right after target
// resolution — same code, message and Details.
func ComposeFanOutCapRefusal(spec *types.ComposeOverlaySpec, route types.OverlayKind, index int) *errors.CodedError {
	if spec == nil || !ComposeFanOutKind(route) {
		return nil
	}
	limit := ComposeTargetCap(spec.Options)
	if len(spec.Targets) <= limit {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP,
		"compose overlay "+string(spec.Kind)+" names "+strconv.Itoa(len(spec.Targets))+
			" targets and emits one layer per target; the per-spec MaxPanelTargets cap is "+strconv.Itoa(limit),
		map[string]any{
			"index":    index,
			"kind":     string(spec.Kind),
			"observed": len(spec.Targets),
			"cap":      limit,
		})
}
