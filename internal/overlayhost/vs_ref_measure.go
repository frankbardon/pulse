package overlayhost

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// The Compose two-sample series kinds (OVERLAY_T_VS_REF /
// OVERLAY_Z_VS_REF over grouped slots) read each side's mean per group
// row. Before U66 #444 they took ANY scored scalar as that mean — an
// AGG_SUM, an AGG_VARIANCE, an AGG_MEDIAN or an AGG_MODE code was
// tested as if it were one. The rule below is the one allowlist
// predict (internal/descriptor, gate 2e) and the runtime
// (internal/processing, the Compose dispatcher before the T / Z
// handlers) share, keyed on the measure params.field selects on each
// slot (else Aggregations[0]).

// VsRefRead is how a Compose two-sample series kind reads one slot's
// scored measure.
type VsRefRead int

const (
	// VsRefReadUnjudged: no type to judge (an untyped aggregation whose
	// smart default did not resolve, or a type the instance hides) —
	// left to the rule that owns it.
	VsRefReadUnjudged VsRefRead = iota
	// VsRefReadMean: the row value IS the side's mean; its variance
	// and n come from params (twosample.Inputs).
	VsRefReadMean
	// VsRefReadMoments: the side's (mean, variance, n) ride the slot's
	// per-group components (AGG_WELFORD, or a moment-carrying
	// AGG_VARIANCE / AGG_STDDEV / extension).
	VsRefReadMoments
	// VsRefReadRefused: the measure is not a mean and carries no
	// moments.
	VsRefReadRefused
)

func (r VsRefRead) String() string {
	switch r {
	case VsRefReadMean:
		return "mean"
	case VsRefReadMoments:
		return "moments"
	case VsRefReadRefused:
		return "refused"
	}
	return "unjudged"
}

// VsRefMeasureKind reports whether route is a Compose two-sample
// series kind the measure rule judges.
func VsRefMeasureKind(route types.OverlayKind) bool {
	return route == types.OverlayKindTVsRef || route == types.OverlayKindZVsRef
}

// VsRefMeanMeasure reports whether a built-in type's value is a mean
// the series arm reads as is: AGG_AVERAGE and AGG_WEIGHTED_MEAN.
func VsRefMeanMeasure(t types.AggregationType) bool {
	return t == types.AGG_AVERAGE || t == types.AGG_WEIGHTED_MEAN
}

// VsRefMeasureRead classifies a slot's ROUTED scored type: the mean
// built-ins (VsRefMeanMeasure) are read as the mean; a built-in whose
// ComponentSchema carries the moments (PriorWelfordMeasure: AGG_WELFORD,
// AGG_VARIANCE, AGG_STDDEV) through its components; an extension
// aggregator through its components when its declared ComponentSchema
// carries `mean` and `variance` (extMoments), refused otherwise. An
// expanded element column (element) is a mean only for AGG_VEC_MEAN.
// Every other built-in is refused.
func VsRefMeasureRead(measure types.AggregationType, element, extMoments bool) VsRefRead {
	switch {
	case measure == "":
		return VsRefReadUnjudged
	case element:
		if measure == types.AGG_VEC_MEAN {
			return VsRefReadMean
		}
		return VsRefReadRefused
	case VsRefMeanMeasure(measure):
		return VsRefReadMean
	case PriorWelfordMeasure(measure):
		return VsRefReadMoments
	case PriorMeasureBuiltin(measure):
		return VsRefReadRefused
	case extMoments:
		return VsRefReadMoments
	}
	return VsRefReadRefused
}

// VsRefSlot is one grouped slot a two-sample series kind reads, as the
// measure rule judges it.
type VsRefSlot struct {
	Label    string // the slot's label
	Host     string // requests[j].aggregations[ai] (the element column appended)
	AggLabel string // the scored column
	Measure  types.AggregationType
	Read     VsRefRead
}

// VsRefSlotOf resolves slot j (label) of a Compose spec to its
// VsRefSlot: the aggregation params.field names (seriesfield.
// ResolveComposeSlot over cols), else Aggregations[0], judged on its
// routed type. nil — not judged — for a crosstab or nil slot, a field
// the slot does not carry (PULSE_OVERLAY_FIELD_UNKNOWN owns it) or no
// aggregation.
func VsRefSlotOf(spec *types.ComposeOverlaySpec, index, j int, label string, req *types.Request, cols seriesfield.Columns, kinds vectors.AggregationKinds) *VsRefSlot {
	if req == nil || req.Crosstab != nil {
		return nil
	}
	sc, err := seriesfield.ResolveComposeSlot(req.Aggregations, spec, index, label, cols)
	if err != nil {
		return nil
	}
	ai := sc.Index
	if ai < 0 || ai >= len(req.Aggregations) || req.Aggregations[ai] == nil {
		return nil
	}
	agg := req.Aggregations[ai]
	element := sc.Element(req.Aggregations)
	routed := kinds.Routed(agg.Type)
	host := "requests[" + strconv.Itoa(j) + "].aggregations[" + strconv.Itoa(ai) + "]"
	column := types.AggregationLabelOf(agg)
	if element {
		column = sc.Column
	}
	return &VsRefSlot{Label: label, Host: host, AggLabel: column, Measure: agg.Type,
		Read: VsRefMeasureRead(routed, element, kinds.ExtensionCarriesMoments(agg.Type))}
}

// VsRefMeasureRefusal is the measure rule of a Compose two-sample
// series kind, judged on the reference slot then each target slot in
// spec order (nil: not judged):
//
//   - a slot whose measure is VsRefReadRefused:
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details
//     {overlay_index, kind, slot, host, label, measure_type,
//     reason: measure_not_mean} — the PriorMeasureRefusal precedent;
//   - then a target read differently from the reference (one a bare
//     mean, the other moments): PULSE_OVERLAY_SCHEMA_DIVERGENT, Details
//     {overlay_index, kind, reference, target, reference_read,
//     target_read, reason: mixed_measure_read} — the handler cannot
//     pair a components leg with a params leg.
//
// nil for any other route.
func VsRefMeasureRefusal(kind, route types.OverlayKind, index int, ref *VsRefSlot, targets []*VsRefSlot) *errors.CodedError {
	if !VsRefMeasureKind(route) {
		return nil
	}
	for _, s := range append([]*VsRefSlot{ref}, targets...) {
		if s == nil || s.Read != VsRefReadRefused {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlays["+strconv.Itoa(index)+"] ("+string(kind)+") reads slot "+strconv.Quote(s.Label)+"'s "+s.Host+" "+s.AggLabel+
				" as each group's mean, and its measure "+measureName(s.Measure)+" is not a mean and carries no moments; "+
				"score AGG_AVERAGE or AGG_WEIGHTED_MEAN (the mean; variance and n from params), AGG_WELFORD, or AGG_VARIANCE / AGG_STDDEV "+
				"(mean, variance and n read from the slot's components)",
			map[string]any{"overlay_index": index, "kind": string(kind), "slot": s.Label, "host": s.Host,
				"label": s.AggLabel, "measure_type": string(s.Measure), "reason": "measure_not_mean"})
	}
	if ref == nil || ref.Read == VsRefReadUnjudged {
		return nil
	}
	for _, t := range targets {
		if t == nil || t.Read == VsRefReadUnjudged || t.Read == ref.Read {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT,
			"overlays["+strconv.Itoa(index)+"] ("+string(kind)+") reads reference slot "+strconv.Quote(ref.Label)+" through its "+ref.Read.String()+
				" and target slot "+strconv.Quote(t.Label)+" through its "+t.Read.String()+
				"; both sides must be bare means (variance and n from params) or both moment-carrying measures (components)",
			map[string]any{"overlay_index": index, "kind": string(kind), "reference": ref.Label, "target": t.Label,
				"reference_read": ref.Read.String(), "target_read": t.Read.String(), "reason": "mixed_measure_read"})
	}
	return nil
}

// ComposeVsRefMeasureRefusal resolves the reference and every target
// of spec (slotIndex maps a label to its slot, -1 when none) through
// VsRefSlotOf and applies VsRefMeasureRefusal. cols(j) lists slot j's
// columns. Predict and the runtime dispatcher both call it.
func ComposeVsRefMeasureRefusal(spec *types.ComposeOverlaySpec, route types.OverlayKind, index int, requests []*types.Request, slotIndex func(label string) int, cols func(j int) seriesfield.Columns, kinds vectors.AggregationKinds) *errors.CodedError {
	if spec == nil || !VsRefMeasureKind(route) || len(spec.Targets) == 0 {
		return nil // no target: PULSE_OVERLAY_TARGET_UNKNOWN owns the spec
	}
	at := func(label string) *VsRefSlot {
		j := slotIndex(label)
		if j < 0 || j >= len(requests) {
			return nil
		}
		return VsRefSlotOf(spec, index, j, label, requests[j], cols(j), kinds)
	}
	ref := at(spec.Reference)
	targets := make([]*VsRefSlot, len(spec.Targets))
	for t, label := range spec.Targets {
		targets[t] = at(label)
	}
	return VsRefMeasureRefusal(spec.Kind, route, index, ref, targets)
}
