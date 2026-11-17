package regordinal

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"slices"

	perr "github.com/frankbardon/pulse/errors"
	pmodel "github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// replicate.go pins REG_ORDINAL's modifier refits (U68 E2-S1, decision
// D5): resample replicates (jackknife, grouped jackknife, bootstrap)
// and selection candidates are fresh fits over a row subset or a
// predictor subset (internal/regadapt's regressionModifierEngine), and
// a subset's levels would otherwise derive from its own rows — a
// replicate that lost a level would report other threshold names. The
// pin fixes params.levels at the slot's first fit's levels, so a refit
// whose rows lack one fails with ReasonReplicateEmptyLevel: a bootstrap
// skips that replicate (requested − succeeded counts it), a jackknife
// fails the slot alone (every replicate enters its variance), a
// selection candidate is dropped.

// ReasonReplicateEmptyLevel is the PROCESSING_REGRESSION_INSUFFICIENT_DATA
// reason of a pinned refit whose rows carry no row at one of the first
// fit's levels.
const ReasonReplicateEmptyLevel = "replicate_empty_level"

// reasonEmptyLevel is resolveLevels' params.levels refusal reason.
const reasonEmptyLevel = "empty_level"

// ReplicatePin is REG_ORDINAL's pin of the modifier refits after first,
// the slot's first successful fit: spec with params.levels set to the
// levels first fitted (its other params kept), the empty-level refusal
// re-reasoned, and the thresholds marked as carrying no p-value.
func ReplicatePin(spec *types.RegressionSpec, first *types.RegressionResult) (pmodel.ReplicatePin, error) {
	levels, ok := stringsOutput(first, "levels")
	thresholds, tok := stringsOutput(first, "thresholds")
	if !ok || !tok {
		return pmodel.ReplicatePin{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			string(Name)+": the first fit reports no levels / thresholds to pin the refits to", map[string]any{"type": string(Name)})
	}
	var p params
	if len(spec.Params) > 0 {
		if err := paramread.DecodeStrict(paramread.Regression, string(Name), spec.Params, &p); err != nil {
			return pmodel.ReplicatePin{}, configError(spec, "params: "+err.Error(), map[string]any{"slot": "params"})
		}
	}
	p.Levels = slices.Clone(levels)
	raw, err := json.Marshal(p)
	if err != nil {
		return pmodel.ReplicatePin{}, perr.NewCodedErrorWithDetails(perr.PROCESSING_INTERNAL,
			string(Name)+": encoding the pinned params: "+err.Error(), map[string]any{"type": string(Name)})
	}
	pinned := *spec
	pinned.Params = raw
	return pmodel.ReplicatePin{
		Spec:     &pinned,
		Refusal:  func(err error) error { return replicateRefusal(err, levels) },
		NoPValue: thresholds,
	}, nil
}

// ReplicatePins are the package's replicate pins keyed by type (the
// model-regression wiring, internal/regadapt, reads them beside
// Factories).
var ReplicatePins = map[types.RegressionType]func(*types.RegressionSpec, *types.RegressionResult) (pmodel.ReplicatePin, error){
	types.REG_ORDINAL: ReplicatePin,
}

// replicateRefusal re-reasons a pinned refit's empty-level refusal: the
// level is the first fit's, which this refit's rows lost.
func replicateRefusal(err error, levels []string) error {
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != reasonEmptyLevel {
		return err
	}
	lv, _ := ce.Details["level"].(string)
	return insufficient(fmt.Sprintf("a refit's rows carry no %q response, a level the full fit has (every resample replicate and selection candidate keeps the full fit's levels %v): a jackknife needs every replicate, so the slot fails; a bootstrap skips the replicate", lv, levels),
		map[string]any{"reason": ReasonReplicateEmptyLevel, "level": lv, "levels": slices.Clone(levels)})
}

// stringsOutput reads the string-list output key of res.
func stringsOutput(res *types.RegressionResult, key string) ([]string, bool) {
	if res == nil {
		return nil, false
	}
	switch v := res.Outputs[key].(type) {
	case []string:
		return slices.Clone(v), true
	case []any:
		out := make([]string, len(v))
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	return nil, false
}
