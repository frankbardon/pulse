package service

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/postfit"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// postFitRefusal is the batch's `post_fit` rules (postfit.Check) against
// this instance's registered post-fit operators and the shared extension
// params check. It runs right after the comparisons' rules, before any
// fit or slot.
func (s *Service) postFitRefusal(composed *types.ComposedRequest) error {
	if len(composed.PostFit) == 0 {
		return nil
	}
	if cerr := postfit.Check(composed.PostFit, composed.Fits, s.extensions.PostFitDecls(), descx.DeclaredParamsRefusal); cerr != nil {
		return cerr
	}
	return nil
}

// runComposePostFit runs every `post_fit` entry once the optimize blocks
// have run and every slot has finished, in declaration order, reading no
// record: each operator sees the fits its entry names (fits: the batch's
// bound set), every slot's result (responses, through the result()
// reader) and the optimize solutions.
//
// An entry that fails on its data — a fit it names failed, or its
// operator returned an error — fails alone: its result keeps {label,
// type, error} and a PULSE_SLOT_FAILED warning (slot_kind post_fit, the
// entry index and label) lifts to the batch; under Strict the batch
// fails with the entry's own code instead. An output the registration's
// Outputs do not admit is fatal, and a panicking operator is the fatal
// PROCESSING_PANIC (recoverInto; re-panicked under
// DisablePanicRecovery). Serial and ComposeParallel call it
// identically, so both answer the same bytes. No entry returns all nils.
func (s *Service) runComposePostFit(composed *types.ComposedRequest, fits *model.Set, responses []*types.Response, optimizations []types.OptimizeResult) ([]types.PostFitResult, []*types.ResponseWarning, error) {
	if len(composed.PostFit) == 0 {
		return nil, nil, nil
	}
	env := postfit.Env{
		Fits:          fits,
		Results:       &resultcall.Reader{Catalog: descx.ResultCatalog(composed.Requests, s.extensions.AggregationKinds()), Responses: responses},
		Optimizations: optimizations,
	}
	out := make([]types.PostFitResult, len(composed.PostFit))
	var warnings []*types.ResponseWarning
	for i := range composed.PostFit {
		spec := &composed.PostFit[i]
		// postFitRefusal admitted the type.
		op := s.extensions.PostFits[spec.Type]
		res, err := s.runPostFitEntry(i, spec, op, env)
		if err != nil {
			return nil, nil, err
		}
		out[i] = res.Result
		if res.Failed {
			f := slotfail.Failure{Kind: slotfail.KindPostFit, Index: i, Label: spec.Label, Err: res.Result.Error}
			if s.strict {
				return nil, nil, slotfail.StrictError(f)
			}
			warnings = append(warnings, slotfail.Warning(f))
		}
	}
	return out, warnings, nil
}

// runPostFitEntry is postfit.Run under the engine's panic recover.
func (s *Service) runPostFitEntry(i int, spec *types.PostFitSpec, op *postfit.Operator, env postfit.Env) (res postfit.Outcome, err error) {
	defer recoverInto(panicOpComposePostFit, s.disablePanicRecovery, &err)
	return postfit.Run(i, spec, op, env)
}
