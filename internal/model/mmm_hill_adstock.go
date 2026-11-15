package model

import (
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// mmm_hill_adstock.go is the static face of REG_MMM_HILL's in-model
// adstock (params.adstock): the probe predict, the runtime adapter, the
// term catalog and the adstock lineage share, the two request-level
// refusals the field-reference walk raises for predict and the run
// alike, and the result-only ill-conditioning advisory the service
// post-hook attaches. internal/regmmm fits the model.

// MMMHillAdstock is a REG_MMM_HILL spec's params.adstock kernel name and
// whether the spec sets one (absent or null: false). Any other type, or
// params that do not decode, report false (the factory refuses those).
func MMMHillAdstock(spec *types.RegressionSpec) (string, bool) {
	if spec == nil || spec.Type != types.REG_MMM_HILL || len(spec.Params) == 0 {
		return "", false
	}
	var p struct {
		Adstock *string `json:"adstock"`
	}
	if paramread.Probe(spec.Params, &p) != nil || p.Adstock == nil {
		return "", false
	}
	return *p.Adstock, true
}

// The refusal reasons of an in-model adstocked REG_MMM_HILL slot.
const (
	// ReasonAdstockHoldout refuses the engine holdout on an in-model
	// adstocked REG_MMM_HILL fit under params.carryover false: its
	// held-out rows are raw spend, which the row scorer would read as
	// pre-adstocked channel columns. The default carries the fit's
	// adstock into them instead (mmm_carry.go).
	ReasonAdstockHoldout = "adstock_holdout"
	// ReasonChannelPreadstocked refuses params.adstock on a channel that
	// is a WIN_DISTRIBUTED_LAG output of the same request: the spend
	// would be adstocked twice and the fit's adstock record would
	// describe only the inner kernel.
	ReasonChannelPreadstocked = "channel_preadstocked"
)

// MMMHillAdstockHoldoutRefusal is the engine holdout's refusal on a
// REG_MMM_HILL spec with params.adstock and params.carryover false:
// PROCESSING_CONFIG {regression, slot: "holdout", reason:
// adstock_holdout[, name]}. Nil for any other spec, one without a
// holdout, or one that carries (the default).
func MMMHillAdstockHoldoutRefusal(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Holdout == "" {
		return nil
	}
	if _, ok := MMMHillAdstock(spec); !ok || MMMCarryover(spec) {
		return nil
	}
	details := map[string]any{"regression": string(spec.Type), "slot": HoldoutKey, "reason": ReasonAdstockHoldout}
	if spec.Name != "" {
		details["name"] = spec.Name
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		"regression "+string(spec.Type)+" with params.adstock and params.carryover false cannot take an engine holdout: its held-out rows are raw spend, which the scorer would read as adstocked columns; drop carryover false (the holdout then carries the fit's adstock in), or drop holdout",
		details)
}

// MMMHillPreadstockedRefusal is the refusal of a REG_MMM_HILL spec with
// params.adstock whose channel is the output column of a
// WIN_DISTRIBUTED_LAG window of req: PROCESSING_CONFIG {regression,
// type, slot: "params.adstock", reason: channel_preadstocked, channel,
// window[, name]}, the first such channel in params.channels order. Nil
// otherwise.
func MMMHillPreadstockedRefusal(req *types.Request, spec *types.RegressionSpec) *errors.CodedError {
	if req == nil {
		return nil
	}
	if _, ok := MMMHillAdstock(spec); !ok {
		return nil
	}
	channels, _, _ := lineageChannels(spec)
	for _, ch := range channels {
		for i, w := range req.Windows {
			if w == nil || w.Type != types.WIN_DISTRIBUTED_LAG || windowparams.Label(w) != ch {
				continue
			}
			details := map[string]any{"regression": spec.Name, "type": string(spec.Type), "slot": "params.adstock",
				"reason": ReasonChannelPreadstocked, "channel": ch, "window": i}
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: channel %q is the output of windows[%d] (WIN_DISTRIBUTED_LAG), but params.adstock adstocks the channel inside the fit — feed the raw spend column, or drop params.adstock", spec.Type, ch, i),
				details)
		}
	}
	return nil
}

// MMMConditionKey is an in-model adstocked REG_MMM_HILL fit's outputs
// key holding the condition number of its joint parameter correlation
// matrix (the 1-norm condition number over the free parameters).
const MMMConditionKey = "condition_number"

// MMMConditionThreshold is the condition number above which an in-model
// adstocked REG_MMM_HILL fit raises PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED:
// past 1e6 the joint correlation matrix is within a millionth of
// singular — some combination of decay, curve and effect is pinned down
// by the data no better than that, so the individual standard errors
// (decay's above all) are unreliable even where they are finite.
const MMMConditionThreshold = 1e6

// MMMConditionAdvisoryCode is the result-only advisory an ill-conditioned
// in-model adstocked REG_MMM_HILL fit carries.
const MMMConditionAdvisoryCode = errors.PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED

// MMMHillConditionAdvisory is the advisory a fitted REG_MMM_HILL result
// res of spec carries (slot is its "regressions[i]" path) when spec sets
// params.adstock and outputs.condition_number is above
// MMMConditionThreshold or undefined (a joint block that does not
// invert: every standard error null): its message and details {slot,
// operator, condition_number (absent when undefined), threshold,
// suggested: {"resample": "bootstrap"}}, and true. False otherwise.
func MMMHillConditionAdvisory(spec *types.RegressionSpec, slot string, res *types.RegressionResult) (string, map[string]any, bool) {
	if res == nil || res.Error != nil {
		return "", nil, false
	}
	if _, ok := MMMHillAdstock(spec); !ok {
		return "", nil, false
	}
	raw, present := res.Outputs[MMMConditionKey]
	if !present {
		return "", nil, false
	}
	cond, isNum := raw.(float64)
	if !isNum {
		cond = math.NaN()
	}
	if cond <= MMMConditionThreshold {
		return "", nil, false
	}
	details := map[string]any{"slot": slot, "operator": string(spec.Type), "threshold": MMMConditionThreshold,
		"suggested": map[string]any{"resample": "bootstrap"}}
	what := "does not invert"
	if !math.IsNaN(cond) && !math.IsInf(cond, 0) {
		details[MMMConditionKey] = cond
		what = "has condition number " + strconv.FormatFloat(cond, 'g', 4, 64) + ", above " + strconv.FormatFloat(MMMConditionThreshold, 'g', -1, 64)
	}
	msg := slot + " (" + string(spec.Type) + " with params.adstock): the joint parameter correlation matrix " + what +
		", so the data barely separate the decay from the curve and the effect; the standard errors are unreliable. Read them with care, or set resample bootstrap."
	return msg, details, true
}
