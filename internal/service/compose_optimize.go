package service

import (
	stderrors "errors"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/optsolve"
	"github.com/frankbardon/pulse/internal/resultcall"
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
	loaded, cerr := descx.OptimizeRefusal(composed, ext.sweepAxes(), s.exprFunctionNames(), s.extensions.TablesCatalog(), s.populationCatalog(), s.optimizeLoader, s.extensions.AggregationKinds())
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

// solveOptimizeBlock compiles block i against the bound set — every
// bound and start evaluated, then range-checked, on the models' values —
// and solves it. A bound or start that reads a Compose fit that failed
// on its data fails the block alone with the fit's code, exactly as the
// objective's read does; any other Compile refusal is fatal.
func (s *Service) solveOptimizeBlock(spec *types.OptimizeSpec, i int, env optblock.Env, set *model.Set, results *resultcall.Reader) (*optsolve.Outcome, error) {
	bound := s.extensions.ExprBoundWithModels(set)
	bound.Result = results.Func
	var view *drawView
	if spec.Posterior != nil {
		view = newDrawView(set)
		view.bind(&bound)
	}
	c, cerr := optblock.Compile(spec, i, env, bound)
	if cerr != nil {
		if se, ok := optsolve.FailedFit(cerr); ok {
			return &optsolve.Outcome{Result: types.OptimizeResult{Name: spec.Name, Direction: spec.Direction, Error: se}, Failed: true}, nil
		}
		return nil, cerr
	}
	seed := optblock.EffectiveSeed(spec)
	if view == nil {
		return optsolve.Solve(spec, i, c, seed)
	}
	if _, failed := set.Failed(c.PosteriorFit); failed {
		// The carrier failed on its data: its draws do not exist, and the
		// objective's first read fails the block alone with its code.
		return optsolve.Solve(spec, i, c, seed)
	}
	draws, err := view.load(set, c.PosteriorFit, spec.Posterior.Draws, seed)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"optimize block "+strconv.Quote(spec.Name)+": reading the posterior draws of fit "+strconv.Quote(c.PosteriorFit)+": "+err.Error(),
			map[string]any{"optimize": i, "field": "optimize[" + strconv.Itoa(i) + "].posterior", "fit": c.PosteriorFit})
	}
	return optsolve.SolvePosterior(spec, i, c, seed, draws)
}

// drawView is a posterior block's bound-model readers: coef(),
// sweep_axis() and response() over whichever Set is selected — the
// block's bound set (the posterior means, which its bounds and starts
// read) or the set with the carrier fit at one draw
// (model.Set.WithMMMDraw), each built once, before the solve.
type drawView struct {
	readers []modelReaders // [0] the means, then one per evaluated draw
	cur     int
}

// modelReaders are the bound-model builtins over one Set.
type modelReaders struct {
	coef, axis, response func(args ...any) (any, error)
}

func readersOf(set *model.Set) modelReaders {
	return modelReaders{coef: model.CoefBuiltin(set), axis: model.SweepAxisBuiltin(set), response: model.ResponseBuiltin(set)}
}

func newDrawView(set *model.Set) *drawView {
	return &drawView{readers: []modelReaders{readersOf(set)}}
}

// bind points b's bound-model readers at the view's selection (an
// embedder function that shadows coef keeps its own name: it is
// registered after, exactly as over ExprBoundWithModels).
func (v *drawView) bind(b *exprenv.Bound) {
	b.Coef = func(args ...any) (any, error) { return v.readers[v.cur].coef(args...) }
	b.SweepAxis = func(args ...any) (any, error) { return v.readers[v.cur].axis(args...) }
	b.Response = func(args ...any) (any, error) { return v.readers[v.cur].response(args...) }
}

// load builds one reader set per evaluated draw of fit — every draw, or
// the seeded subsample of n (optsolve.SubsampleDraws) — and returns the
// solver's Draws over them.
func (v *drawView) load(set *model.Set, fit string, n int, seed int64) (*optsolve.Draws, error) {
	d, err := set.MMMDrawsOf(fit)
	if err != nil {
		return nil, err
	}
	picks := optsolve.SubsampleDraws(seed, d.Len(), n)
	for _, k := range picks {
		ds, err := set.WithMMMDraw(fit, d, k)
		if err != nil {
			return nil, err
		}
		v.readers = append(v.readers, readersOf(ds))
	}
	return &optsolve.Draws{
		Fit: fit, Count: len(picks), Available: d.Len(), Level: d.Level,
		Select: func(k int) { v.cur = k + 1 },
	}, nil
}

// runComposeOptimize solves every `optimize` block once the fits have
// run (fits: the batch's bound set — the registered populations plus
// every fit), in declaration order, each against its own compiled
// expressions: coef() over fits plus the block's loaded `models`
// (loaded, from optimizeRefusal — never read twice), result() over the
// slots the result pre-phase already ran (responses; runComposeResultPhase).
// No record is read here.
//
// A block that fails on its numbers — no start converged, the
// constraints could not be met, or it reads a Compose fit that failed
// on its data — fails alone: its result keeps {name, direction, error}
// and a PULSE_SLOT_FAILED warning (slot_kind optimizations, the block
// index and name) lifts to the batch; under Strict the batch fails with
// the block's own code instead. Any other fault is fatal. Serial and
// ComposeParallel call it identically, so both answer the same bytes.
// No block returns all nils.
func (s *Service) runComposeOptimize(composed *types.ComposedRequest, ext *composeExpansion, fits *model.Set, loaded []*model.Loaded, responses []*types.Response) ([]types.OptimizeResult, []*types.ResponseWarning, error) {
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
		Tables:     s.extensions.TablesCatalog(),
		Outer:      model.WithinCatalog(s.populationCatalog(), catalog),
		Load:       s.optimizeLoader,
		Loaded:     loaded,
		Results:    descx.ResultCatalog(composed.Requests, s.extensions.AggregationKinds()),
	}
	results := &resultcall.Reader{Catalog: env.Results, Responses: responses}
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
		res, err := s.solveOptimizeBlock(spec, i, env, set, results)
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
