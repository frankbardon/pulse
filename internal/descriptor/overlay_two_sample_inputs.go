package descriptor

import (
	"strconv"
	"strings"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/twosample"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// ComposeOverlayTwoSampleInputsRefusal is the one rule for WHERE a
// Compose two-sample overlay (OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over
// grouped slots, OVERLAY_T_CELL / OVERLAY_Z_CELL over crosstab slots)
// gets each side's mean, variance and n, and the refusal of every
// route on which it gets none. Per slot the overlay reads (reference
// and targets; every slot when it names no target, for the components
// leg):
//
//   - grouped slot, scored aggregation AGG_WELFORD (a payload the kind
//     reads, overlayReadsPayload): the moments ride the slot's
//     per-group components (processing.narrowSeriesHost). Components
//     disabled — disabled(slot) is the runtime's effective gate
//     (EffectiveDisableComponents; a `return` never removes them, the
//     Compose veto keeps every sub-part of a slot an overlay names) —
//     left the layer EMPTY with no error (U46 E4-S13):
//     PULSE_OVERLAY_COMPONENTS_REQUIRED, reason components_disabled.
//   - grouped slot, scalar scored aggregation: the row value is the
//     side's mean; its variance and n come from params
//     (twosample.Inputs). Absent, they were variance 1, n 2 (U46
//     E4-S16): PULSE_OVERLAY_PARAM_MISSING, reason params_missing,
//     Details.side + Details.params naming the side and its keys.
//   - crosstab slot under a cell kind: a cell whose components carry
//     mean and variance (cellCarriesMoments: AGG_WELFORD, AGG_VARIANCE,
//     AGG_STDDEV, or an extension declaring both keys) with components
//     enabled reads them; any other cell is a scalar mean and needs the
//     side's params, as above.
//   - crosstab slot under a vs-ref kind: the handler reads grouped rows
//     and a crosstab has none — the layer came back empty (U46 E4-S16):
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, Details {shape: matrix,
//     shapes: [series]}.
//   - grouped slot under a cell kind: PULSE_OVERLAY_SLOT_NOT_CROSSTAB
//     (kindRequiresMatrixCompose) owns it.
//
// The side a slot plays is the reference for spec.Reference and the
// target for every label in spec.Targets. A spec with no targets is
// judged for the components leg only (on every slot); its params are
// left to the target rule. The
// scored aggregation resolves as ComposeOverlayArrayValuedRefusal's
// (params.field, else Aggregations[0]); an array-valued or other
// non-scalar one is that rule's.
//
// Details carry {overlay_index, kind, slot, host, aggregation, label}
// plus the route's reason (and side / params). The runtime
// (Service.applyComposeOverlays) and ValidateComposeWithOptions (gate
// 2e) call it with the raw slots; the runtime re-runs it at fold time
// against the components each slot actually returned (an engine
// invariant, PROCESSING_INTERNAL). A kind the instance hides is left
// to the kind rule.
func ComposeOverlayTwoSampleInputsRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, disabled func(*types.Request) bool) error {
	for i := range overlays {
		if err := composeOverlayTwoSampleInputsRefusal(i, &overlays[i], requests, labels, inst, disabled); err != nil {
			return err
		}
	}
	return nil
}

// twoSampleCellTwin maps each two-sample vs-ref kind to its crosstab
// twin, and back; a kind absent here is not a two-sample kind.
var twoSampleCellTwin = map[types.OverlayKind]types.OverlayKind{
	types.OverlayKindTVsRef: types.OverlayKindTCell,
	types.OverlayKindZVsRef: types.OverlayKindZCell,
	types.OverlayKindTCell:  types.OverlayKindTVsRef,
	types.OverlayKindZCell:  types.OverlayKindZVsRef,
}

func composeOverlayTwoSampleInputsRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot, disabled func(*types.Request) bool) *errors.CodedError {
	kind := string(spec.Kind)
	twin, twoSample := twoSampleCellTwin[spec.Kind]
	if !twoSample || inst.Hidden(kind) {
		return nil
	}
	cellKind := spec.Kind == types.OverlayKindTCell || spec.Kind == types.OverlayKindZCell
	sides := func(label string) []twosample.Side {
		var out []twosample.Side
		if len(spec.Targets) == 0 {
			return nil // no target: PULSE_OVERLAY_TARGET_UNKNOWN owns the spec
		}
		if label == spec.Reference {
			out = append(out, twosample.Reference)
		}
		for _, t := range spec.Targets {
			if t == label {
				out = append(out, twosample.Target)
				break
			}
		}
		return out
	}
	kinds := aggregationKinds(inst, nil)
	if composeTwoSampleSpecMalformed(i, spec, requests, labels, kinds) {
		return nil
	}
	for j, r := range requests {
		if r == nil || j >= len(labels) {
			continue
		}
		side := sides(labels[j])
		if len(side) == 0 && len(spec.Targets) > 0 {
			continue // a slot the overlay does not read
		}
		at := func(ai int) string {
			return "requests[" + strconv.Itoa(j) + "].aggregations[" + strconv.Itoa(ai) + "]"
		}
		if r.Crosstab != nil {
			if !cellKind {
				return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
					"overlays["+strconv.Itoa(i)+"] ("+kind+") reads slot "+strconv.Quote(labels[j])+" as a series (one mean per group row), "+
						"and that slot is a crosstab, which has no group rows, so the layer would be empty; "+
						"score grouped slots (groups + aggregations), or compare crosstab cells with "+string(twin),
					map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j],
						"shape": string(types.OverlayShapeMatrix), "shapes": []string{string(types.OverlayShapeSeries)}})
			}
			cell := r.Crosstab.Cell
			if !disabled(r) && cell != nil && cellCarriesMoments(inst, kinds.Routed(cell.Type)) {
				continue
			}
			host := "requests[" + strconv.Itoa(j) + "].crosstab.cell"
			if ce := twoSampleParamsRefusal(i, spec, labels[j], host, cell, side, disabled(r)); ce != nil {
				return ce
			}
			continue
		}
		if cellKind {
			continue // PULSE_OVERLAY_SLOT_NOT_CROSSTAB owns it
		}
		ai := 0
		element := false
		var sc seriesfield.Scored
		if seriesfield.AppliesCompose(spec.Kind) {
			var err error
			// No schema here: an element column whose members the slot
			// does not carry inline resolves syntactically; the field
			// rule, which reads the slot's schema, judges it.
			sc, err = seriesfield.ResolveComposeSlot(r.Aggregations, spec, i, labels[j], vectors.AggregationColumns(r, nil, kinds))
			if err != nil {
				continue // PULSE_OVERLAY_FIELD_UNKNOWN owns it
			}
			ai, element = sc.Index, sc.Element(r.Aggregations)
		}
		if ai < 0 || ai >= len(r.Aggregations) || r.Aggregations[ai] == nil {
			continue
		}
		agg := r.Aggregations[ai]
		if element {
			// An expanded element column is one bare mean per row,
			// named by its column.
			scored := *agg
			scored.Label = sc.Column
			if ce := twoSampleParamsRefusal(i, spec, labels[j], at(ai), &scored, side, false); ce != nil {
				return ce
			}
			continue
		}
		if kinds.IsArrayValued(agg.Type) {
			continue // the array-valued rule owns it
		}
		if kinds.IsNonScalar(agg.Type) {
			if !overlayReadsPayload(spec.Kind, kinds.Routed(agg.Type)) || !disabled(r) {
				continue // the non-scalar rule owns it, or the components carry the moments
			}
			label := types.AggregationLabelOf(agg)
			return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
				"overlays["+strconv.Itoa(i)+"] ("+kind+") reads slot "+strconv.Quote(labels[j])+"'s "+at(ai)+" "+label+" ("+string(agg.Type)+
					") from its per-group components (mean, variance, n and the weighted floor), and that slot was built with components disabled, "+
					"so the layer would be empty; enable components on that slot (drop disable_components / Options.DisableComponents) "+
					"or compare a scalar mean with the params triple",
				map[string]any{"overlay_index": i, "kind": kind, "slot": labels[j], "host": at(ai),
					"aggregation": string(agg.Type), "label": label, "reason": "components_disabled"})
		}
		if ce := twoSampleParamsRefusal(i, spec, labels[j], at(ai), agg, side, false); ce != nil {
			return ce
		}
	}
	return nil
}

// composeTwoSampleSpecMalformed reports a spec a structural rule owns,
// judged before any input route: a reference or target label that names
// no slot (PULSE_OVERLAY_REFERENCE_UNKNOWN / _TARGET_UNKNOWN), read
// slots of different shapes (PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT /
// _SLOT_NOT_CROSSTAB), or a params.field a read grouped slot does not
// carry (PULSE_OVERLAY_FIELD_UNKNOWN). Both sides raise those after
// this rule, so it stands aside rather than pre-empt them.
func composeTwoSampleSpecMalformed(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, kinds vectors.AggregationKinds) bool {
	slot := map[string]*types.Request{}
	for j, r := range requests {
		if r != nil && j < len(labels) {
			slot[labels[j]] = r
		}
	}
	read := append([]string{spec.Reference}, spec.Targets...)
	if len(spec.Targets) == 0 {
		read = labels
	}
	var (
		shape types.OverlayShape
		seen  bool
	)
	for _, label := range read {
		r, ok := slot[label]
		if !ok {
			if len(spec.Targets) == 0 {
				continue
			}
			return true
		}
		s := inferComposeSlotShape(r)
		if seen && s != shape {
			return true
		}
		shape, seen = s, true
		if r.Crosstab == nil && seriesfield.AppliesCompose(spec.Kind) {
			if _, err := seriesfield.ResolveComposeSlot(r.Aggregations, spec, i, label, vectors.AggregationColumns(r, nil, kinds)); err != nil {
				return true
			}
		}
	}
	return false
}

// twoSampleParamsRefusal refuses the first of sides whose explicit
// (variance, n) params are absent or unusable (twosample.Inputs), or
// nil. agg is the scalar the slot scores at host (nil: an unset
// crosstab cell); componentsOff marks a crosstab slot whose components
// are disabled (its cell's moments would otherwise be read).
func twoSampleParamsRefusal(i int, spec *types.ComposeOverlaySpec, slot, host string, agg *types.Aggregation, sides []twosample.Side, componentsOff bool) *errors.CodedError {
	for _, side := range sides {
		_, _, missing := twosample.Inputs(spec.Params, side)
		if len(missing) == 0 {
			continue
		}
		kind := string(spec.Kind)
		label := types.AggregationLabelOf(agg)
		aggType := ""
		if agg != nil {
			aggType = string(agg.Type)
		}
		remedy := "score AGG_WELFORD so the slot's components carry mean, variance and n"
		if componentsOff {
			remedy = "enable components on that slot (drop disable_components / Options.DisableComponents) and score AGG_WELFORD so its cell components carry mean, variance and n"
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlays["+strconv.Itoa(i)+"] ("+kind+") scores slot "+strconv.Quote(slot)+"'s "+host+" "+label+" ("+aggType+
				") as a bare mean on its "+string(side)+" side, and params carry no usable "+strings.Join(missing, " / ")+
				" (a finite number > 0; the symmetric "+twosample.SymmetricVarianceKey+" / "+twosample.SymmetricSampleSizeKey+
				" serve both sides); there is no default variance or sample size — supply them, or "+remedy,
			map[string]any{"overlay_index": i, "kind": kind, "slot": slot, "host": host,
				"aggregation": aggType, "label": label, "side": string(side), "params": missing,
				"reason": "params_missing"})
	}
	return nil
}

// builtinMomentAggregators is the set of built-in aggregators whose
// ComponentSchema declares both `mean` and `variance` — the cell
// components a crosstab two-sample overlay reads (with the universal
// `n`) instead of params.
var builtinMomentAggregators = sync.OnceValue(func() map[string]bool {
	out := map[string]bool{}
	for _, op := range aggregatorCapabilityTable() {
		if schemaCarriesMoments(op.ComponentSchema.Keys) {
			out[op.Name] = true
		}
	}
	return out
})

// cellCarriesMoments reports whether the (routed) aggregator t emits
// mean and variance components: a built-in whose ComponentSchema
// declares both, or an extension whose registration does.
func cellCarriesMoments(inst *InstanceSnapshot, t types.AggregationType) bool {
	if t == "" {
		return false
	}
	if builtinMomentAggregators()[string(t)] {
		return true
	}
	if snap := inst.Extensions(); snap != nil {
		if schema, ok := snap.ComponentSchemas[string(t)]; ok {
			return schemaCarriesMoments(schema.Keys)
		}
	}
	return false
}

// schemaCarriesMoments reports whether keys declares both `mean` and
// `variance`.
func schemaCarriesMoments(keys []descriptor.ComponentKey) bool {
	var mean, variance bool
	for _, k := range keys {
		switch k.Name {
		case "mean":
			mean = true
		case "variance":
			variance = true
		}
	}
	return mean && variance
}
