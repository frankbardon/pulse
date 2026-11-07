package processing

import (
	"bytes"
	"math"
	"slices"
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

// ExtensionSeriesOverlayFunc runs an extension kind over a SERIES
// host. It receives a copy of the spec and the read-only host view
// (SeriesHostView.ValueLabel names the scored column).
type ExtensionSeriesOverlayFunc func(spec *types.OverlaySpec, host *SeriesHostView) (ExtensionOverlayOutput, error)

// ExtensionComposeOverlayFunc runs an extension kind over a Compose
// host. It receives a copy of the spec and the resolved slots.
type ExtensionComposeOverlayFunc func(spec *types.ComposeOverlaySpec, host *ExtensionComposeHost) (ExtensionOverlayOutput, error)

// ExtensionOverlay is one registered extension overlay kind.
type ExtensionOverlay struct {
	// Decl is the declared contract both sides judge a spec by.
	Decl overlayhost.ExtensionDecl
	// Matrix is the crosstab-host handler (nil: the kind does not run
	// on a crosstab host).
	Matrix ExtensionMatrixOverlayFunc
	// Series is the SERIES-host handler (nil: the kind does not run on
	// a grouped request).
	Series ExtensionSeriesOverlayFunc
	// Compose is the ComposedRequest.Overlays handler (nil: the kind
	// does not run there).
	Compose ExtensionComposeOverlayFunc
}

// ExtensionComposeHost is the Compose host an extension handler reads:
// the reference slot, then each target in spec.Targets order.
type ExtensionComposeHost struct {
	Reference *ExtensionComposeSlot
	Targets   []*ExtensionComposeSlot
}

// ExtensionComposeSlot is one resolved Compose slot: its label, index
// and judged host shape, and the one view that shape offers (Matrix
// for a crosstab slot, Series for a grouped one, Scalar / ScalarOK for
// an ungrouped one).
type ExtensionComposeSlot struct {
	Label    string
	Index    int
	Shape    types.OverlayShape
	Matrix   *CrosstabHostView
	Series   *SeriesHostView
	Scalar   float64
	ScalarOK bool
}

// extensionSlotRefusal is the wrong-slot refusal for a spec of kind on
// host: the catalog's (overlayhost.Refusal), else an extension kind's
// declared slots (overlayhost.ExtensionSlotRefusal) — the pair predict
// raises at the same point. A hidden kind is neither (its route is
// never registered). Nil-receiver-safe.
func (r *ExtensionRegistry) extensionSlotRefusal(host overlayhost.Host, kind types.OverlayKind, index int) *errors.CodedError {
	route := r.overlayRoute(kind)
	if cerr := overlayhost.Refusal(host, kind, route, index); cerr != nil {
		return cerr
	}
	if x := r.extensionOverlay(route); x != nil {
		return overlayhost.ExtensionSlotRefusal(&x.Decl, host, index)
	}
	return nil
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
		return types.OverlayLayer{}, nil, extensionNoHandler(spec.Kind, "crosstab", index)
	}
	out, err := x.Matrix(cloneOverlaySpec(spec), host)
	return extensionLayer(x, out, err, spec.Name, spec.Kind, spec.Scope, spec.Ref, index)
}

// applyExtensionOverlaySeries is applyExtensionOverlayMatrix over a
// SERIES host.
func applyExtensionOverlaySeries(x *ExtensionOverlay, spec *types.OverlaySpec, host *SeriesHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if x.Series == nil {
		// Unreachable: the declaration lists the series host exactly
		// when Series is set.
		return types.OverlayLayer{}, nil, extensionNoHandler(spec.Kind, "series", index)
	}
	out, err := x.Series(cloneOverlaySpec(spec), host)
	return extensionLayer(x, out, err, spec.Name, spec.Kind, spec.Scope, spec.Ref, index)
}

// applyExtensionOverlayCompose is applyExtensionOverlayMatrix over a
// Compose host. The layer's Ref stays empty (a Compose spec names its
// slots by Reference / Targets, which no OverlayRef arm carries), like
// every built-in Compose kind's.
func applyExtensionOverlayCompose(x *ExtensionOverlay, spec *types.ComposeOverlaySpec, host *ExtensionComposeHost, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if x.Compose == nil {
		// Unreachable: ExtensionComposeRefusal refuses a kind without a
		// Compose handler.
		return types.OverlayLayer{}, nil, extensionNoHandler(spec.Kind, "compose", index)
	}
	out, err := x.Compose(cloneComposeOverlaySpec(spec), host)
	name := spec.Name
	if name == "" {
		name = ExtensionOverlayLayerName(&types.OverlaySpec{Kind: spec.Kind})
	}
	return extensionLayer(x, out, err, name, spec.Kind, spec.Scope, types.OverlayRef{}, index)
}

func extensionNoHandler(kind types.OverlayKind, host string, index int) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"extension overlay "+string(kind)+" has no "+host+" handler", map[string]any{"index": index, "kind": string(kind)})
}

// extensionLayer holds a handler's output to the declaration and
// stamps the layer's engine-owned identity.
func extensionLayer(x *ExtensionOverlay, out ExtensionOverlayOutput, err error, name string, kind types.OverlayKind, scope types.OverlayScope, ref types.OverlayRef, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}
	if cerr := overlayhost.ExtensionOutputRefusal(&x.Decl, &out.Payload, out.Summary, out.Warnings, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if name == "" {
		name = ExtensionOverlayLayerName(&types.OverlaySpec{Kind: kind})
	}
	layer := types.OverlayLayer{
		Name:    name,
		Kind:    kind,
		Scope:   scope,
		Ref:     ref,
		Payload: out.Payload,
		Summary: out.Summary,
	}
	return layer, out.Warnings, nil
}

// cloneComposeOverlaySpec is a copy of spec a handler may not reach the
// request through: its Targets, Options and (deeply) its Params.
func cloneComposeOverlaySpec(spec *types.ComposeOverlaySpec) *types.ComposeOverlaySpec {
	c := *spec
	c.Targets = slices.Clone(spec.Targets)
	if spec.Params != nil {
		c.Params, _ = cloneJSONValue(spec.Params).(map[string]any)
	}
	if spec.Options != nil {
		o := *spec.Options
		c.Options = &o
	}
	return &c
}

// cloneJSONValue deep-copies a decoded JSON value (maps and slices; the
// leaves are immutable).
func cloneJSONValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = cloneJSONValue(e)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneJSONValue(e)
		}
		return out
	}
	return v
}

// extensionComposeSlot builds the extension view of one resolved
// Compose slot from its (projected) response and authored request.
func extensionComposeSlot(label string, index int, shape types.OverlayShape, resp *types.Response, req *types.Request) *ExtensionComposeSlot {
	s := &ExtensionComposeSlot{Label: label, Index: index, Shape: shape}
	if resp == nil {
		return s
	}
	switch shape {
	case types.OverlayShapeMatrix:
		if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
			var comps *types.CrosstabComponents
			if resp.Components != nil {
				comps = resp.Components.Crosstab
			}
			s.Matrix = newCrosstabHostViewWithComponents(resp.Crosstab.Matrix, comps)
		}
	case types.OverlayShapeSeries:
		if req != nil && len(req.Aggregations) > 0 {
			s.Series = buildSeriesHostFromGroupedResponse(req, resp, 0, "")
		}
	case types.OverlayShapeScalar:
		if req != nil && len(req.Aggregations) > 0 && req.Aggregations[0] != nil && len(resp.Data) > 0 {
			if raw, ok := resp.Data[0][AggregationLabel(req.Aggregations[0])]; ok {
				if v := toFloat64(raw); !math.IsNaN(v) && !math.IsInf(v, 0) {
					s.Scalar, s.ScalarOK = v, true
				}
			}
		}
	}
	return s
}

// NewCrosstabHostViewWithComponents wraps a crosstab payload and its
// components block (nil: components disabled) as a host view — the
// pulse.New overlay probe's synthetic host. Neither pointer is copied;
// the caller must not mutate them while the view is in use.
func NewCrosstabHostViewWithComponents(payload *types.MatrixPayload, comps *types.CrosstabComponents) *CrosstabHostView {
	return newCrosstabHostViewWithComponents(payload, comps)
}

// NewExtensionSeriesHostView is a SERIES host view over keys (one per
// group), a value resolver, the grouper key columns and types and the
// scored column's label — the pulse.New overlay probe's synthetic
// series.
func NewExtensionSeriesHostView(keys []types.AxisKey, resolver func(i int) (float64, bool), groupFields []string, grouperKinds []types.GroupType, valueLabel string) *SeriesHostView {
	h := NewSeriesHostViewWithGrouper(keys, resolver, groupFields, grouperKinds)
	h.valueLabel = valueLabel
	return h
}
