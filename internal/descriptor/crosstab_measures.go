package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ComposeOverlayMeasureSlotRefusal refuses a Compose-host overlay that
// reads a multi-measure crosstab slot (crosstab.cells, U47 #326). Every
// Compose-host kind compares slots cell by cell under ONE cell
// aggregation per slot — the slot's cell type picks the variance
// convention, its weight the basis, its moments the two-sample legs —
// and a MEASURE-folded slot carries a different aggregation on every
// row, so no Compose kind reads it as one table. Refused rather than
// computed over rows that are not one measure: PULSE_OVERLAY_REF_
// INCOMPATIBLE_WITH_SHAPE with Details {index, kind, slot, reason}.
// Predict (ValidateComposeWithOptions, gate 2a) and the runtime
// (Service.applyComposeOverlays) raise it first, over the same raw
// slots: the reference, then each target (every slot when Targets is
// empty). A hidden kind is left to the unknown-kind gate.
func ComposeOverlayMeasureSlotRefusal(overlays []types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot) error {
	for i := range overlays {
		if err := composeOverlayMeasureSlotRefusal(i, &overlays[i], requests, labels, inst); err != nil {
			return err
		}
	}
	return nil
}

func composeOverlayMeasureSlotRefusal(i int, spec *types.ComposeOverlaySpec, requests []*types.Request, labels []string, inst *InstanceSnapshot) *errors.CodedError {
	if inst.Hidden(string(spec.Kind)) {
		return nil
	}
	reads := func(label string) bool {
		if len(spec.Targets) == 0 || label == spec.Reference {
			return true
		}
		for _, t := range spec.Targets {
			if t == label {
				return true
			}
		}
		return false
	}
	for j, r := range requests {
		if r == nil || j >= len(labels) || !reads(labels[j]) || !r.Crosstab.HasMeasures() {
			continue
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlays["+strconv.Itoa(i)+"] ("+string(spec.Kind)+") reads slot "+strconv.Quote(labels[j])+
				", a multi-measure crosstab (crosstab.cells): a Compose overlay compares slots under one cell aggregation each, "+
				"and that slot carries a different measure on every row; give the slot a single crosstab.cell (one slot per measure)",
			map[string]any{"index": i, "kind": string(spec.Kind), "slot": labels[j], "reason": "multi_measure_slot"})
	}
	return nil
}
