package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ComposeKindUnknownRefusal is the PULSE_OVERLAY_KIND_UNKNOWN a
// ComposedRequest.Overlays spec gets when its route names no catalog
// kind (types.AllOverlayKinds) and no extension kind (extension: the
// instance registers the route as one): a never-registered kind and a
// kind the instance feature set hides (route "") alike. Predict-compose
// raises it at Gate 0 and the runtime Compose dispatcher before any
// other rule, so both refuse with one message and details {index,
// kind} — the runtime once answered an empty stub layer. Nil for a
// known kind.
func ComposeKindUnknownRefusal(kind, route types.OverlayKind, extension bool, index int) *errors.CodedError {
	if extension || catalogKind(route) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_KIND_UNKNOWN,
		"compose overlay spec carries unknown kind: "+string(kind),
		map[string]any{"index": index, "kind": string(kind)})
}

// catalogKind reports whether kind is a built-in catalog kind.
func catalogKind(kind types.OverlayKind) bool {
	if kind == "" {
		return false
	}
	for _, k := range types.AllOverlayKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// KindUnknownRefusal is the PULSE_OVERLAY_KIND_UNKNOWN an overlay spec
// on Request.Overlays, ChainRequest.Overlays or FacetRequest.Overlays
// gets when its route names no kind the slot can run — a
// never-registered kind, or one the instance feature set hides: the
// predict validators' catalog probe and each runtime dispatcher's
// missing-handler arm raise this one refusal, details {index, kind[,
// host]}: host names the dispatcher — "series" (a grouped request's
// SERIES host), "chain" or "facet" — and is absent on a crosstab or
// matrix-result host.
func KindUnknownRefusal(kind types.OverlayKind, index int, host string) *errors.CodedError {
	details := map[string]any{"index": index, "kind": string(kind)}
	if host != "" {
		details["host"] = host
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_KIND_UNKNOWN,
		"overlay kind is not in the catalog: "+string(kind), details)
}

// RequestKindUnknownRefusal is KindUnknownRefusal for the
// Request.Overlays spec at index, judged up front on every Process
// shape: nil when route is a built-in catalog kind or extension (the
// instance registers route as one); otherwise the refusal with the host
// of the dispatcher that would have run it — "series" on a grouped
// request with no crosstab, none on a crosstab, a matrix-only or an
// ungrouped request. Predict (validateOverlaySpec) and the runtime's
// host pre-pass (refuseWrongHostOverlays) both call it, so an ungrouped
// request — where no dispatcher runs — refuses an unknown kind as a
// grouped one does instead of ignoring it.
func RequestKindUnknownRefusal(req *types.Request, kind, route types.OverlayKind, extension bool, index int) *errors.CodedError {
	if extension || catalogKind(route) {
		return nil
	}
	host := ""
	if req != nil && req.Crosstab == nil && len(req.Groups) > 0 {
		host = "series"
	}
	return KindUnknownRefusal(kind, index, host)
}
