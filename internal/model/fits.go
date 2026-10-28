package model

import (
	"bytes"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// The PULSE_FIT_INVALID reasons.
const (
	ReasonNameMissing     = "name_missing"
	ReasonRequestMissing  = "request_missing"
	ReasonRegressionCount = "regression_count"
	ReasonGrouped         = "grouped"
	// ReasonKindAmbiguous: a fit request holding a regression AND a
	// vector battery (a fit is one kind).
	ReasonKindAmbiguous = "kind_ambiguous"
	// ReasonVectorCount: more than one vector battery.
	ReasonVectorCount = "vector_count"
	// ReasonVectorSource: a battery whose fold has no single listwise
	// state (params.missing pairwise, MAT_DISTANCE's per-group
	// centroids) or folds transformed values (MAT_RELIABILITY
	// params.reverse).
	ReasonVectorSource = "vector_source"
)

// VectorFitAttributeTypes are the fitted vector attributes a vector fit
// request may hold as its battery (the attribute folds the battery's
// listwise co-moment). ATTR_VEC_DISTANCE is not one: its fit exists
// only under a fitted metric.
func VectorFitAttributeTypes() []types.AttributeType {
	return []types.AttributeType{types.ATTR_MAHALANOBIS, types.ATTR_PC_SCORE, types.ATTR_FACTOR_SCORE}
}

// IsVectorFitAttribute reports whether t is a VectorFitAttributeTypes
// entry.
func IsVectorFitAttribute(t types.AttributeType) bool {
	switch t {
	case types.ATTR_MAHALANOBIS, types.ATTR_PC_SCORE, types.ATTR_FACTOR_SCORE:
		return true
	}
	return false
}

// VectorBatteries counts req's vector batteries: its Matrices specs
// plus its VectorFitAttributeTypes attributes.
func VectorBatteries(req *types.Request) int {
	if req == nil {
		return 0
	}
	n := len(req.Matrices)
	for _, a := range req.Attributes {
		if a != nil && IsVectorFitAttribute(a.Type) {
			n++
		}
	}
	return n
}

// FitKindOf is the kind a fit request binds: a regression fit (exactly
// one non-null regression, no vector battery), a SEM fit (no
// regression, exactly one battery, a MAT_SEM matrix — IsSEMFitRequest),
// a vector fit (no regression, exactly one other vector battery), or ""
// for a request CheckFits refuses.
func FitKindOf(req *types.Request) types.FitKind {
	if req == nil {
		return ""
	}
	nV := VectorBatteries(req)
	switch {
	case len(req.Regressions) == 1 && req.Regressions[0] != nil && nV == 0:
		return types.FitKindRegression
	case IsSEMFitRequest(req):
		return types.FitKindSEM
	case len(req.Regressions) == 0 && nV == 1:
		return types.FitKindVector
	}
	return ""
}

// CheckFits is the structural rule set over a ComposedRequest `fits`
// list, run by the runtime (before any fit or slot runs) and by
// PredictCompose alike, so both refuse the same batch with the same
// error. Entries are judged in order, each fully before the next; the
// first fault wins:
//
//   - an empty name                            — PULSE_FIT_INVALID name_missing
//   - no request                               — PULSE_FIT_INVALID request_missing
//   - a regression AND a vector battery        — PULSE_FIT_INVALID kind_ambiguous
//   - more than one vector battery             — PULSE_FIT_INVALID vector_count
//   - no battery, or more than one regression  — PULSE_FIT_INVALID regression_count
//   - groups or a crosstab                     — PULSE_FIT_INVALID grouped
//   - a vector battery with no single listwise
//     state (VectorSourceRefusal)              — PULSE_FIT_INVALID vector_source
//   - an invalid `save` path (CheckSavePath on a regression fit's own
//     and its regression's, CheckPopulationSavePath on a vector
//     fit's, CheckSEMSavePath on a SEM fit's)  — PULSE_MODEL_SAVE_INVALID
//   - a name an earlier entry already binds    — PULSE_MODEL_NAME_COLLISION
//
// A vector battery is a Matrices spec or a VectorFitAttributeTypes
// attribute (VectorBatteries). Every refusal carries details.fit (the
// 0-based entry index) and details.name. Nil for an empty list. Every
// save path is judged here, before any fit runs, so no fit writes a
// model or population file in a batch a later entry's path would
// refuse.
//
// CheckFits judges a sweep-free batch: a from_sweep entry is refused
// from_sweep_no_sweep (or from_sweep_with_request). CheckFitsIn takes
// the batch's sweep.
func CheckFits(fits []types.FitSpec) error {
	return CheckFitsIn(fits, SweepScope{})
}

// CheckFitsIn is CheckFits over a batch whose sweep is scope. A
// from_sweep entry (types.FitFromSweep), after the name, is judged by
// the from_sweep rules instead of the request rules — PULSE_FIT_INVALID
// from_sweep_with_request, _no_sweep, _no_rank, _regression_unknown,
// _signature_varies, _cycle (details.regression, and details.slot — the
// 0-based sweep slot — on the last two) — then its `save` as a
// regression fit's (CheckSavePath) and its name's uniqueness.
func CheckFitsIn(fits []types.FitSpec, scope SweepScope) error {
	seen := make(map[string]bool, len(fits))
	for i, f := range fits {
		if f.Name == "" {
			return fitInvalid(i, f.Name, ReasonNameMissing, "has no name", nil)
		}
		if f.FromSweep != nil {
			if err := checkSweepFit(i, f, scope); err != nil {
				return err
			}
			if f.Save != "" {
				if cerr := CheckSavePath(f.Save); cerr != nil {
					cerr.Details["fit"] = i
					cerr.Details["name"] = f.Name
					return cerr
				}
			}
			if seen[f.Name] {
				return fitNameCollision(i, f.Name)
			}
			seen[f.Name] = true
			continue
		}
		if f.Request == nil {
			return fitInvalid(i, f.Name, ReasonRequestMissing, "has no request", nil)
		}
		nR, nV := len(f.Request.Regressions), VectorBatteries(f.Request)
		switch {
		case nR > 0 && nV > 0:
			return fitInvalid(i, f.Name, ReasonKindAmbiguous,
				fmt.Sprintf("holds %d regression(s) and %d vector batteries; a fit is one regression or one vector battery", nR, nV),
				map[string]any{"regressions": nR, "vectors": nV})
		case nV > 1:
			return fitInvalid(i, f.Name, ReasonVectorCount,
				fmt.Sprintf("holds %d vector batteries (matrices and fitted attributes); a vector fit holds exactly one", nV),
				map[string]any{"count": nV})
		case nV == 0 && nR != 1:
			return fitInvalid(i, f.Name, ReasonRegressionCount,
				fmt.Sprintf("holds %d regressions; a fit request holds exactly one regression or one vector battery", nR),
				map[string]any{"count": nR})
		case nV == 0 && f.Request.Regressions[0] == nil:
			return fitInvalid(i, f.Name, ReasonRegressionCount,
				"holds a null regression; a fit request holds exactly one",
				map[string]any{"count": 0})
		}
		if len(f.Request.Groups) > 0 || f.Request.Crosstab != nil {
			return fitInvalid(i, f.Name, ReasonGrouped,
				"is grouped; a fit is one ungrouped model (drop groups / crosstab)", nil)
		}
		cerr := (*errors.CodedError)(nil)
		if IsSEMFitRequest(f.Request) {
			if f.Save != "" {
				cerr = CheckSEMSavePath(f.Save)
			}
		} else if nV == 1 {
			if what, extra := VectorSourceRefusal(f.Request); what != "" {
				return fitInvalid(i, f.Name, ReasonVectorSource, what, extra)
			}
			if f.Save != "" {
				cerr = CheckPopulationSavePath(f.Save)
			}
		} else {
			if f.Save != "" {
				cerr = CheckSavePath(f.Save)
			}
			if cerr == nil {
				cerr = CheckRegressionSaves(f.Request.Regressions)
			}
		}
		if cerr != nil {
			cerr.Details["fit"] = i
			cerr.Details["name"] = f.Name
			return cerr
		}
		if seen[f.Name] {
			return fitNameCollision(i, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// fitNameCollision is the PULSE_MODEL_NAME_COLLISION refusal of fits[i],
// whose name an earlier entry already binds.
func fitNameCollision(i int, name string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_NAME_COLLISION,
		fmt.Sprintf("fits[%d]: model name %q is bound twice", i, name),
		map[string]any{"fit": i, "name": name})
}

// VectorSourceRefusal judges a vector fit request's one Matrices
// battery (a fitted attribute battery always folds one listwise
// state): MAT_DISTANCE (per-group centroids, not one state),
// params.missing "pairwise" (no single listwise state) and a
// MAT_RELIABILITY params.reverse list (the fold reads reverse-keyed
// values, not the members') are refused — the message tail and the
// details; "" when the battery is a valid source. Params are read
// loosely: a malformed params object is the slot resolution's to
// refuse.
func VectorSourceRefusal(req *types.Request) (string, map[string]any) {
	if req == nil || len(req.Matrices) != 1 {
		return "", nil
	}
	spec := req.Matrices[0]
	if spec.Type == types.MAT_DISTANCE {
		return "takes a vector fit from MAT_DISTANCE, whose fold is per-group centroids, not one listwise state",
			map[string]any{"type": string(spec.Type)}
	}
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var p struct {
		Missing *string  `json:"missing"`
		Reverse []string `json:"reverse"`
	}
	if paramread.Probe(raw, &p) != nil {
		return "", nil
	}
	if p.Missing != nil && *p.Missing == "pairwise" {
		return "takes a vector fit from a params.missing \"pairwise\" matrix, which has no single listwise state; use \"listwise\"",
			map[string]any{"type": string(spec.Type), "param": "missing"}
	}
	if spec.Type == types.MAT_RELIABILITY && len(p.Reverse) > 0 {
		return "takes a vector fit from a MAT_RELIABILITY with params.reverse, whose fold reads reverse-keyed values, not the members'",
			map[string]any{"type": string(spec.Type), "param": "reverse"}
	}
	return "", nil
}

func fitInvalid(i int, name, reason, what string, extra map[string]any) error {
	details := map[string]any{"fit": i, "name": name, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_FIT_INVALID,
		fmt.Sprintf("fits[%d]: fit request %s", i, what), details)
}

// FitRequest returns the request a fit runs: a shallow clone of
// spec.Request with its `return` cleared. The fit's figures surface
// whole as ComposedResponse.Fits[name] (outside every `return`), and a
// `return` that skipped the regressions slot would leave nothing to
// bind. The caller's request is never mutated.
func FitRequest(spec types.FitSpec) *types.Request {
	if spec.Request == nil {
		return nil
	}
	r := *spec.Request
	r.Return = nil
	return &r
}

// SEMOrdinalEstimator is a SEM fit request's battery's params.estimator
// when it is an ordinal one ("dwls" / "wlsmv"), else "" — the static
// face of an ordinal SEM fit (Signature.Ordinal), whose scores are
// empirical Bayes modal. Params are read loosely: a malformed params
// object is the slot resolution's to refuse.
func SEMOrdinalEstimator(req *types.Request) string {
	var p struct {
		Estimator *string `json:"estimator"`
	}
	if !semFitParams(req, &p) || p.Estimator == nil {
		return ""
	}
	if *p.Estimator == "dwls" || *p.Estimator == "wlsmv" {
		return *p.Estimator
	}
	return ""
}

// SEMGroupOf is a SEM fit request's params.group, read loosely ("" when
// ungrouped, or when the request is no SEM fit or its params do not
// decode — CheckFits refuses those first).
func SEMGroupOf(req *types.Request) string {
	var p struct {
		Group *string `json:"group"`
	}
	if !semFitParams(req, &p) || p.Group == nil {
		return ""
	}
	return *p.Group
}

// semFitParams probes a SEM fit request's one MAT_SEM params into dst;
// false when req is no such request or the params are absent or do not
// decode.
func semFitParams(req *types.Request, dst any) bool {
	if req == nil || len(req.Matrices) != 1 || req.Matrices[0].Type != types.MAT_SEM {
		return false
	}
	raw := bytes.TrimSpace(req.Matrices[0].Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	return paramread.Probe(raw, dst) == nil
}
