package processing

import (
	"bytes"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// overlay_extension.go is the engine half of an embedder-registered
// OVERLAY_<NS>_<NAME> kind (pulse.OverlayRegistration). The root adapter
// (extensions_overlay.go) lifts the public extend.OverlayHandler onto
// ExtensionOverlay; this package never imports extend. The kind's
// declared contract (ExtensionOverlay.Decl) is the one predict reads
// off the snapshot: refuseWrongHostOverlays judges every spec of the
// kind by overlayhost.ExtensionRequestRefusal before any payload guard,
// and the MATRIX dispatcher (applyOverlaysPlanned) runs its handler and
// holds the output to the declaration (overlayhost.ExtensionOutputRefusal).

// ExtensionOverlayOutput is an extension overlay handler's result
// (extend.OverlayOutput's engine twin).
type ExtensionOverlayOutput struct {
	Payload  types.OverlayPayload
	Summary  *types.OverlaySummary
	Warnings []types.OverlayWarning
}

// ExtensionMatrixOverlayFunc runs an extension kind over a crosstab
// host. It receives a copy of the spec and the read-only host view.
type ExtensionMatrixOverlayFunc func(spec *types.OverlaySpec, host *CrosstabHostView) (ExtensionOverlayOutput, error)

// ExtensionOverlay is one registered extension overlay kind.
type ExtensionOverlay struct {
	// Decl is the declared contract both sides judge a spec by.
	Decl overlayhost.ExtensionDecl
	// Matrix is the crosstab-host handler (nil: the kind does not run
	// on a crosstab host).
	Matrix ExtensionMatrixOverlayFunc
}

// extensionOverlay returns the extension overlay kind route names, nil
// for a built-in, unknown or hidden kind (a hidden kind's route is
// hiddenOverlayRoute, which no registration carries). Nil-receiver-safe.
func (r *ExtensionRegistry) extensionOverlay(route types.OverlayKind) *ExtensionOverlay {
	if r == nil || route == hiddenOverlayRoute {
		return nil
	}
	return r.Overlays[route]
}

// ExtensionOverlayLayerName is an extension layer's renderer-facing
// label: spec.Name, else the kind lower-cased without its OVERLAY_
// prefix (OVERLAY_ACME_HEAT → acme_heat) — the built-ins' bare-kind
// convention.
func ExtensionOverlayLayerName(spec *types.OverlaySpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	return strings.ToLower(strings.TrimPrefix(string(spec.Kind), "OVERLAY_"))
}

// cloneOverlaySpec is a copy of spec a handler may not reach the
// request through: its Params bytes, Ref.Margin and Multiplicity are
// copied too (the other Ref arms are refused on an extension kind
// before a handler runs).
func cloneOverlaySpec(spec *types.OverlaySpec) *types.OverlaySpec {
	c := *spec
	if spec.Params != nil {
		c.Params = bytes.Clone(spec.Params)
	}
	if spec.Ref.Margin != nil {
		m := *spec.Ref.Margin
		c.Ref.Margin = &m
	}
	if spec.Multiplicity != nil {
		m := *spec.Multiplicity
		c.Multiplicity = &m
	}
	return &c
}

// applyExtensionOverlayMatrix runs extension kind x over the crosstab
// host for the spec at index: the handler (on a copy of the spec), then
// the declaration's output rule, then the engine-stamped layer. The
// host payload is never handed out — the handler reads it through the
// view's copying accessors, so it cannot mutate the response.
func applyExtensionOverlayMatrix(x *ExtensionOverlay, spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if x.Matrix == nil {
		// Unreachable: ExtensionRequestRefusal refuses a crosstab host
		// the declaration does not list, and the declaration lists the
		// matrix host exactly when Matrix is set.
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"extension overlay "+string(spec.Kind)+" has no crosstab handler", map[string]any{"index": index, "kind": string(spec.Kind)})
	}
	out, err := x.Matrix(cloneOverlaySpec(spec), host)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	if cerr := overlayhost.ExtensionOutputRefusal(&x.Decl, &out.Payload, out.Summary, out.Warnings, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	layer := types.OverlayLayer{
		Name:    ExtensionOverlayLayerName(spec),
		Kind:    spec.Kind,
		Scope:   spec.Scope,
		Ref:     spec.Ref,
		Payload: out.Payload,
		Summary: out.Summary,
	}
	return layer, out.Warnings, nil
}

// NewCrosstabHostViewWithComponents wraps a crosstab payload and its
// components block (nil: components disabled) as a host view — the
// pulse.New overlay probe's synthetic host. Neither pointer is copied;
// the caller must not mutate them while the view is in use.
func NewCrosstabHostViewWithComponents(payload *types.MatrixPayload, comps *types.CrosstabComponents) *CrosstabHostView {
	return newCrosstabHostViewWithComponents(payload, comps)
}
