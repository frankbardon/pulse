package model

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// saves.go is the batch half of the `save` rule: every save in a
// request or Compose batch is judged before anything runs — its path
// (CheckSavePath) and its uniqueness (two saves naming one file would
// let the later write silently replace the earlier one) — and every
// `models` entry of a batch or chain before any fit, slot or stage
// runs. The runtime and predict call the same functions, so both refuse
// the same tree with the same error.

// ReasonSaveDuplicate is the PULSE_MODEL_SAVE_INVALID reason of a second
// save naming a file an earlier save in the same tree writes.
const ReasonSaveDuplicate = "duplicate"

// SaveSite is one `save` in a request tree: where it sits (e.g.
// "regressions[0]", "fits[1]", "requests[2].regressions[0]") and the
// path it writes.
type SaveSite struct {
	Site string
	Path string
}

// RequestSaveSites lists req's regression saves, in order, each site
// prefixed by prefix ("" for a standalone request).
func RequestSaveSites(prefix string, req *types.Request) []SaveSite {
	if req == nil {
		return nil
	}
	var out []SaveSite
	for j, r := range req.Regressions {
		if r != nil && r.Save != "" {
			out = append(out, SaveSite{Site: fmt.Sprintf("%sregressions[%d]", prefix, j), Path: r.Save})
		}
	}
	return out
}

// CheckSaveDuplicates refuses the first site whose path (cleaned) an
// earlier site already writes: PULSE_MODEL_SAVE_INVALID reason
// duplicate, details.path and details.sites (the earlier site, then
// this one). Nil when every path is distinct.
func CheckSaveDuplicates(sites []SaveSite) *errors.CodedError {
	seen := make(map[string]string, len(sites))
	for _, s := range sites {
		key := cleanModelPath(s.Path)
		if first, dup := seen[key]; dup {
			return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_SAVE_INVALID,
				fmt.Sprintf("save path %q is written twice, by %s and by %s; the later write would replace the earlier", s.Path, first, s.Site),
				map[string]any{"path": s.Path, "reason": ReasonSaveDuplicate, "sites": []any{first, s.Site}})
		}
		seen[key] = s.Site
	}
	return nil
}

// CheckRequestSaves is the standalone request's save rule: every
// regression save path (CheckRegressionSaves), then their uniqueness.
func CheckRequestSaves(req *types.Request) *errors.CodedError {
	if req == nil {
		return nil
	}
	if cerr := CheckRegressionSaves(req.Regressions); cerr != nil {
		return cerr
	}
	return CheckSaveDuplicates(RequestSaveSites("", req))
}

// CheckComposeBatch is the Compose pre-pass rule, run right after
// CheckFits by the runtime (serial and parallel, before any fit or slot
// runs) and by PredictCompose, in this order, the first fault winning:
//
//  1. every slot's regression save path (CheckRegressionSaves,
//     details.request = the slot index) — the fits' own were CheckFits';
//  2. the uniqueness of every save in the batch — fits[i],
//     fits[i].request.regressions[j], requests[k].regressions[j]
//     (CheckSaveDuplicates);
//  3. every fit request's `models` and `populations` (CheckRefs,
//     nothing bound outside it — a fit runs before the batch binds;
//     details.fit);
//  4. every slot's `models` and `populations` against the fits' names
//     (CheckRefs, details.request).
//
// requests are the effective slots (sweep slots included).
func CheckComposeBatch(fits []types.FitSpec, requests []*types.Request) *errors.CodedError {
	for k, req := range requests {
		if req == nil {
			continue
		}
		if cerr := CheckRegressionSaves(req.Regressions); cerr != nil {
			cerr.Details["request"] = k
			return cerr
		}
	}
	var sites []SaveSite
	for i, f := range fits {
		if f.Save != "" {
			sites = append(sites, SaveSite{Site: fmt.Sprintf("fits[%d]", i), Path: f.Save})
		}
		sites = append(sites, RequestSaveSites(fmt.Sprintf("fits[%d].request.", i), f.Request)...)
	}
	for k, req := range requests {
		sites = append(sites, RequestSaveSites(fmt.Sprintf("requests[%d].", k), req)...)
	}
	if cerr := CheckSaveDuplicates(sites); cerr != nil {
		return cerr
	}
	for i, f := range fits {
		if f.Request == nil {
			continue
		}
		if cerr := CheckRefs(RequestRefs(f.Request), nil); cerr != nil {
			cerr.Details["fit"] = i
			return cerr
		}
	}
	cat := CatalogFromFits(fits)
	for k, req := range requests {
		if req == nil {
			continue
		}
		if cerr := CheckRefs(RequestRefs(req), cat); cerr != nil {
			cerr.Details["request"] = k
			return cerr
		}
	}
	return nil
}

// CheckChainModels is the chain pre-pass rule, run by the runtime
// before the cohort opens and by PredictChain: every stage's `models`
// and `populations` (CheckRefs; each stage its own namespace;
// details.stage). A chain writes no
// model file — the chain gate refuses a regression on every stage
// (PULSE_CHAIN_NOT_MERGEABLE), stage 0 before it runs — so its saves
// need no rule here.
func CheckChainModels(stages []*types.ChainStage) *errors.CodedError {
	for i, st := range stages {
		if st == nil || st.Request == nil {
			continue
		}
		if cerr := CheckRefs(RequestRefs(st.Request), nil); cerr != nil {
			cerr.Details["stage"] = i
			return cerr
		}
	}
	return nil
}
