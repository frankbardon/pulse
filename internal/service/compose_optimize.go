package service

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/optsolve"
	"github.com/frankbardon/pulse/internal/slotfail"
	"github.com/frankbardon/pulse/types"
)

// optimizeRefusal is the batch's `optimize` rules (descx.OptimizeRefusal,
// predict's too) against this instance's embedder ExprFunctions,
// LookupTables, registered populations and model-file loader: each
// block's decoded `models` files (index-aligned) when every block
// passes, nil without any block. It runs right after model.CheckFits,
// before any fit or slot.
func (s *Service) optimizeRefusal(composed *types.ComposedRequest, ext *composeExpansion) ([]*model.Loaded, error) {
	if len(composed.Optimize) == 0 {
		return nil, nil
	}
	loaded, cerr := descx.OptimizeRefusal(composed, ext.sweepAxes(), s.exprFunctionNames(), s.hasLookup(), s.populationCatalog(), s.optimizeLoader)
	if cerr != nil {
		return nil, cerr
	}
	return loaded, nil
}

// exprFunctionNames are the embedder's ExprFunction names (nil without
// any).
func (s *Service) exprFunctionNames() []string {
	if s.extensions == nil {
		return nil
	}
	var names []string
	for _, fn := range s.extensions.ExprFunctions {
		names = append(names, fn.Name)
	}
	return names
}

// hasLookup reports a registered LookupTable (lookup() is declared).
func (s *Service) hasLookup() bool {
	return s.extensions != nil && len(s.extensions.LookupTables) > 0
}

// optimizeLoader is loadFiles as an optblock.Loader: predict's
// loadModelFiles twin.
func (s *Service) optimizeLoader(refs model.Refs) (*model.Loaded, *errors.CodedError) {
	loaded, err := s.loadFiles(refs)
	if err != nil {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return nil, ce
		}
		return nil, errors.NewCodedError(errors.PULSE_MODEL_FILE_UNREADABLE, err.Error())
	}
	return loaded, nil
}

// runComposeOptimize solves every `optimize` block once the fits have
// run (fits: the batch's bound set — the registered populations plus
// every fit), in declaration order, each against its own compiled
// expressions: coef() over fits plus the block's loaded `models`
// (loaded, from optimizeRefusal — never read twice). No record is read.
//
// A block that fails on its numbers — no start converged, the
// constraints could not be met, or it reads a Compose fit that failed
// on its data — fails alone: its result keeps {name, direction, error}
// and a PULSE_SLOT_FAILED warning (slot_kind optimizations, the block
// index and name) lifts to the batch; under Strict the batch fails with
// the block's own code instead. Any other fault is fatal. Serial and
// ComposeParallel call it identically, so both answer the same bytes.
// No block returns all nils.
func (s *Service) runComposeOptimize(composed *types.ComposedRequest, ext *composeExpansion, fits *model.Set, loaded []*model.Loaded) ([]types.OptimizeResult, []*types.ResponseWarning, error) {
	if len(composed.Optimize) == 0 {
		return nil, nil, nil
	}
	params, _ := exprenv.NormalizeParams(composed.Params)
	catalog := model.CatalogFromFits(composed.Fits).WithSweepAxes(ext.sweepAxes())
	env := optblock.Env{
		Params:     params,
		Fits:       catalog,
		Extensions: s.exprFunctionNames(),
		Lookup:     s.hasLookup(),
		Outer:      model.WithinCatalog(s.populationCatalog(), catalog),
		Load:       s.optimizeLoader,
		Loaded:     loaded,
	}
	out := make([]types.OptimizeResult, len(composed.Optimize))
	var warnings []*types.ResponseWarning
	for i := range composed.Optimize {
		spec := &composed.Optimize[i]
		set := fits
		if i < len(loaded) && loaded[i] != nil {
			var err error
			if set, err = fits.WithLoaded(model.Refs{Models: spec.Models}, loaded[i]); err != nil {
				return nil, nil, descx.RefusalAt(err, "optimize", i)
			}
		}
		c, cerr := optblock.Compile(spec, i, env, s.extensions.ExprBoundWithModels(set))
		if cerr != nil {
			return nil, nil, cerr
		}
		res, err := optsolve.Solve(spec, i, c, optblock.EffectiveSeed(spec))
		if err != nil {
			return nil, nil, err
		}
		out[i] = res.Result
		if res.Failed {
			f := slotfail.Failure{Kind: slotfail.KindOptimizations, Index: i, Label: spec.Name, Err: res.Result.Error}
			if s.strict {
				return nil, nil, slotfail.StrictError(f)
			}
			warnings = append(warnings, slotfail.Warning(f))
		}
	}
	return out, warnings, nil
}
