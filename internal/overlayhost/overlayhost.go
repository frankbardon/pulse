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
// Within a Process host (Request.Overlays) it also declares the host
// SHAPE(s) each Process kind runs on — MATRIX (a crosstab), SERIES (a
// grouped request with aggregations) or both — and owns the one
// refusal a kind authored over the other shape gets (ShapeRefusal),
// raised right after the slot refusal and before any per-kind rule, on
// predict and runtime alike. The processing-side drift test pins the
// shape table to the MATRIX and SERIES handler tables.
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
	// Process (MATRIX only — processShapes) and Compose.
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

var (
	matrixOnly  = []types.OverlayShape{types.OverlayShapeMatrix}
	seriesOnly  = []types.OverlayShape{types.OverlayShapeSeries}
	bothProcess = []types.OverlayShape{types.OverlayShapeMatrix, types.OverlayShapeSeries}
)

// processShapes maps every kind that runs on Request.Overlays to the
// Process host shapes it runs on, MATRIX first. OVERLAY_SHARE_OF_TOTAL
// is the one kind with both. OVERLAY_FORMULA runs on a MATRIX Process
// host only: its SERIES and SCALAR namespaces execute on a Compose host.
var processShapes = map[types.OverlayKind][]types.OverlayShape{
	types.OverlayKindChiSqCol:                  matrixOnly,
	types.OverlayKindChiSqMatrix:               matrixOnly,
	types.OverlayKindChiSqRow:                  matrixOnly,
	types.OverlayKindDeltaVsMargin:             matrixOnly,
	types.OverlayKindFisherExactCell:           matrixOnly,
	types.OverlayKindFormula:                   matrixOnly,
	types.OverlayKindIndexVsMargin:             matrixOnly,
	types.OverlayKindPairwiseProbitT:           matrixOnly,
	types.OverlayKindPairwisePropZ:             matrixOnly,
	types.OverlayKindPairwiseTwoMeansZ:         matrixOnly,
	types.OverlayKindPairwiseWeightedTwoMeansZ: matrixOnly,
	types.OverlayKindPairwiseWelchT:            matrixOnly,
	types.OverlayKindShareOfCol:                matrixOnly,
	types.OverlayKindShareOfRow:                matrixOnly,
	types.OverlayKindZScoreVsMargin:            matrixOnly,
	types.OverlayKindDeltaVsBaseline:           seriesOnly,
	types.OverlayKindDeltaVsPrior:              seriesOnly,
	types.OverlayKindDeltaVsSibling:            seriesOnly,
	types.OverlayKindIndexVsBaseline:           seriesOnly,
	types.OverlayKindIndexVsPrior:              seriesOnly,
	types.OverlayKindIndexVsRollingMean:        seriesOnly,
	types.OverlayKindIndexVsSibling:            seriesOnly,
	types.OverlayKindIndexVsTotal:              seriesOnly,
	types.OverlayKindYoY:                       seriesOnly,
	types.OverlayKindZScoreVsRolling:           seriesOnly,
	types.OverlayKindZScoreVsTotal:             seriesOnly,
	types.OverlayKindShareOfTotal:              bothProcess,
}

// Shapes returns the Process host shapes kind runs on, or nil for a
// kind that does not run on Request.Overlays (or is outside the
// catalog).
func Shapes(kind types.OverlayKind) []types.OverlayShape {
	return append([]types.OverlayShape(nil), processShapes[kind]...)
}

// AcceptsShape reports whether kind runs on a Process host of shape.
// Only MATRIX and SERIES are judged: a SCALAR (ungrouped) host, a kind
// outside the Process table (the slot refusal owns it) and a kind
// outside the catalog are accepted here.
func AcceptsShape(shape types.OverlayShape, kind types.OverlayKind) bool {
	if shape != types.OverlayShapeMatrix && shape != types.OverlayShapeSeries {
		return true
	}
	ss, known := processShapes[kind]
	if !known {
		return true
	}
	for _, s := range ss {
		if s == shape {
			return true
		}
	}
	return false
}

// shapeHost names the request slot that makes a Process host shape.
func shapeHost(shape types.OverlayShape) string {
	if shape == types.OverlayShapeMatrix {
		return "a matrix host (Request.Crosstab)"
	}
	return "a series host (Request.Groups + Request.Aggregations, no Request.Crosstab)"
}

// ShapeRefusal returns the wrong-shape refusal for the Request.Overlays
// spec at index over a Process host of shape, or nil when kind runs on
// it. route and kind follow Refusal.
//
// Code PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
// {index, kind, host, shape, shapes}: host is always "request", shape
// the host shape authored, shapes every Process shape the kind runs on.
func ShapeRefusal(shape types.OverlayShape, kind, route types.OverlayKind, index int) *errors.CodedError {
	if AcceptsShape(shape, route) {
		return nil
	}
	ss := processShapes[route]
	names := make([]string, len(ss))
	runs := ""
	for i, s := range ss {
		names[i] = string(s)
		if i > 0 {
			runs += " or "
		}
		runs += shapeHost(s)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
		"overlay "+string(kind)+" does not run on a "+string(shape)+"-shape Request.Overlays host; it runs on "+runs,
		map[string]any{
			"index":  index,
			"kind":   string(kind),
			"host":   string(Request),
			"shape":  string(shape),
			"shapes": names,
		})
}

// ReadsCellValue reports whether kind, on a MATRIX Process host, reads
// the crosstab cell aggregation's VALUE — the one number per cell and
// per margin (CellAt / MarginFor): the share / index / delta / z-score
// vs-margin kinds, the χ² and Fisher kinds (cell values as observed
// counts), the proportion pairwise kinds (OVERLAY_PAIRWISE_PROP_Z /
// _PROBIT_T: the cell value as a proportion) and OVERLAY_FORMULA
// (`cell`, the margins and their SDs). The moment pairwise kinds
// (types.PairwiseKindUsesWelford, PairwiseKindUsesWeightedMoments)
// read only the cell's component moments and counts, never its value,
// and are false; so is every kind that does not run on a MATRIX host.
// The categorical-code overlay rule refuses a value-reading kind over
// a crosstab whose cell is a code; processing's
// TestOverlayReadsCellValue_MatchesHandlers pins this to what each
// MATRIX handler reads.
func ReadsCellValue(kind types.OverlayKind) bool {
	matrix := false
	for _, s := range processShapes[kind] {
		if s == types.OverlayShapeMatrix {
			matrix = true
		}
	}
	return matrix && !types.PairwiseKindUsesWelford(kind) && !types.PairwiseKindUsesWeightedMoments(kind)
}
