// Package overlayhost is the one declaration of WHICH overlay slot each
// catalog kind runs on, and the one refusal a kind authored on any other
// slot gets.
//
// Four slots carry overlay specs: Request.Overlays (a Process host —
// crosstab MATRIX or grouped SERIES), ComposedRequest.Overlays,
// ChainRequest.Overlays and FacetRequest.Overlays. Each runtime
// dispatcher (internal/processing) and each predict validator
// (internal/descriptor) calls Refusal for every spec before any per-kind
// rule, so a kind on the wrong slot is refused identically — code,
// message and Details — on both sides, instead of predict accepting it
// and the runtime answering PULSE_OVERLAY_KIND_UNKNOWN (or an empty stub
// layer). The processing-side test pins this table to the four runtime
// handler tables, so it cannot drift from what actually executes.
//
// Within a Process host the MATRIX-versus-SERIES split stays with the
// per-kind validators; this package judges the slot only.
//
// Leaf: stdlib + types + errors. No-execute.
package overlayhost

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Host names an overlay slot.
type Host string

const (
	// Request is Request.Overlays: a Process host (crosstab MATRIX or
	// grouped SERIES).
	Request Host = "request"
	// Compose is ComposedRequest.Overlays.
	Compose Host = "compose"
	// Chain is ChainRequest.Overlays.
	Chain Host = "chain"
	// Facet is FacetRequest.Overlays.
	Facet Host = "facet"
)

// Slot returns the request field the host's overlay specs live on.
func (h Host) Slot() string {
	switch h {
	case Request:
		return "Request.Overlays"
	case Compose:
		return "ComposedRequest.Overlays"
	case Chain:
		return "ChainRequest.Overlays"
	case Facet:
		return "FacetRequest.Overlays"
	}
	return string(h)
}

var (
	requestOnly = []Host{Request}
	composeOnly = []Host{Compose}
	chainOnly   = []Host{Chain}
	facetOnly   = []Host{Facet}
)

// hosts maps every catalog kind to the slots it runs on, in slot order.
// OVERLAY_FORMULA is the one kind with two: Request.Overlays and
// ComposedRequest.Overlays.
var hosts = map[types.OverlayKind][]Host{
	// Process MATRIX (crosstab) kinds.
	types.OverlayKindChiSqCol:                  requestOnly,
	types.OverlayKindChiSqMatrix:               requestOnly,
	types.OverlayKindChiSqRow:                  requestOnly,
	types.OverlayKindDeltaVsMargin:             requestOnly,
	types.OverlayKindFisherExactCell:           requestOnly,
	types.OverlayKindIndexVsMargin:             requestOnly,
	types.OverlayKindPairwiseProbitT:           requestOnly,
	types.OverlayKindPairwisePropZ:             requestOnly,
	types.OverlayKindPairwiseTwoMeansZ:         requestOnly,
	types.OverlayKindPairwiseWeightedTwoMeansZ: requestOnly,
	types.OverlayKindPairwiseWelchT:            requestOnly,
	types.OverlayKindShareOfCol:                requestOnly,
	types.OverlayKindShareOfRow:                requestOnly,
	types.OverlayKindZScoreVsMargin:            requestOnly,
	// Process SERIES (grouped) kinds; SHARE_OF_TOTAL runs on both
	// Process shapes.
	types.OverlayKindDeltaVsBaseline:    requestOnly,
	types.OverlayKindDeltaVsPrior:       requestOnly,
	types.OverlayKindDeltaVsSibling:     requestOnly,
	types.OverlayKindIndexVsBaseline:    requestOnly,
	types.OverlayKindIndexVsPrior:       requestOnly,
	types.OverlayKindIndexVsRollingMean: requestOnly,
	types.OverlayKindIndexVsSibling:     requestOnly,
	types.OverlayKindIndexVsTotal:       requestOnly,
	types.OverlayKindShareOfTotal:       requestOnly,
	types.OverlayKindYoY:                requestOnly,
	types.OverlayKindZScoreVsRolling:    requestOnly,
	types.OverlayKindZScoreVsTotal:      requestOnly,
	// Both Process shapes and Compose.
	types.OverlayKindFormula: {Request, Compose},
	// Compose kinds.
	types.OverlayKindChiSqVsRef:      composeOnly,
	types.OverlayKindDeltaVsRef:      composeOnly,
	types.OverlayKindIndexVsRef:      composeOnly,
	types.OverlayKindPanelIndexVsRef: composeOnly,
	types.OverlayKindPropZCell:       composeOnly,
	types.OverlayKindPropZPanel:      composeOnly,
	types.OverlayKindRank:            composeOnly,
	types.OverlayKindTCell:           composeOnly,
	types.OverlayKindTVsRef:          composeOnly,
	types.OverlayKindZCell:           composeOnly,
	types.OverlayKindZVsRef:          composeOnly,
	// Whole-chain kinds.
	types.OverlayKindDeltaVsStage: chainOnly,
	types.OverlayKindIndexVsStage: chainOnly,
	// FACET kinds.
	types.OverlayKindChiSqVsPop:  facetOnly,
	types.OverlayKindIndexVsPop:  facetOnly,
	types.OverlayKindKSVsPop:     facetOnly,
	types.OverlayKindZScoreVsPop: facetOnly,
}

// Hosts returns the slots kind runs on, or nil for a kind outside the
// catalog (the empty route of a hidden kind included).
func Hosts(kind types.OverlayKind) []Host {
	return append([]Host(nil), hosts[kind]...)
}

// Accepts reports whether kind runs on host. A kind outside the catalog
// is accepted: the slot's own unknown-kind gate owns it.
func Accepts(host Host, kind types.OverlayKind) bool {
	hs, known := hosts[kind]
	if !known {
		return true
	}
	for _, h := range hs {
		if h == host {
			return true
		}
	}
	return false
}

// Refusal returns the wrong-slot refusal for the spec at index on host,
// or nil when the slot runs it. route is the kind every decision keys on
// (the authored kind, or the empty route of a hidden kind, which is
// never refused here); kind is the authored kind the message and Details
// name.
//
// Code PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
// {index, kind, host, hosts}: host is the slot it was authored on,
// hosts every slot it runs on.
func Refusal(host Host, kind, route types.OverlayKind, index int) *errors.CodedError {
	if Accepts(host, route) {
		return nil
	}
	hs := hosts[route]
	names := make([]string, len(hs))
	slots := ""
	for i, h := range hs {
		names[i] = string(h)
		if i > 0 {
			slots += " or "
		}
		slots += h.Slot()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" does not run on "+host.Slot()+"; attach it to "+slots,
		map[string]any{
			"index": index,
			"kind":  string(kind),
			"host":  string(host),
			"hosts": names,
		})
}
