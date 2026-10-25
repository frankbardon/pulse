package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/types"
)

// OptimizeRefusal is the ComposedRequest `optimize` blocks' rule set
// (optblock.CheckLoaded) over the batch as it runs — its top-level
// params (already validated by InheritComposeParams) and its `fits`
// (the names coef() resolves) — with the instance's embedder
// ExprFunction names, whether a LookupTable is registered, the
// instance's registered populations (outer: the namespace a block's
// `models` entry may not rebind, with the fits) and the loader that
// reads a block's `models` files. Predict (validateComposeExpanded) and
// the runtime (Service.compose / composeParallel) call it at the same
// point, right after model.CheckFits, so both refuse the same block
// identically. It returns each block's decoded `models` files
// (index-aligned; what the runtime solves with) — nil, nil when the
// batch declares no block.
func OptimizeRefusal(req *types.ComposedRequest, exprFunctions []string, lookup bool, outer model.Catalog, load optblock.Loader) ([]*model.Loaded, *errors.CodedError) {
	if req == nil || len(req.Optimize) == 0 {
		return nil, nil
	}
	params, _ := exprenv.NormalizeParams(req.Params)
	fits := model.CatalogFromFits(req.Fits)
	return optblock.CheckLoaded(req.Optimize, optblock.Env{
		Params:     params,
		Fits:       fits,
		Extensions: exprFunctions,
		Lookup:     lookup,
		Outer:      model.WithinCatalog(outer, fits),
		Load:       load,
	})
}

// predictExprFunctionNames is the embedder ExprFunction names the
// predict snapshot carries (nil without one).
func predictExprFunctionNames(opts *PredictOptions) []string {
	snap := extensionsFromOpts(opts)
	if snap == nil {
		return nil
	}
	out := make([]string, 0, len(snap.ExprFunctions))
	for _, fn := range snap.ExprFunctions {
		out = append(out, fn.Name)
	}
	return out
}

// predictHasLookup reports whether the predict snapshot registers any
// LookupTable (lookup() is declared).
func predictHasLookup(opts *PredictOptions) bool {
	snap := extensionsFromOpts(opts)
	return snap != nil && len(snap.LookupTables) > 0
}

// optimizeSummaries is predict's static view of the blocks (nil without
// any), read once they passed OptimizeRefusal.
func optimizeSummaries(specs []types.OptimizeSpec) []descriptor.OptimizeSummary {
	if len(specs) == 0 {
		return nil
	}
	out := make([]descriptor.OptimizeSummary, len(specs))
	for i := range specs {
		spec := &specs[i]
		names := make([]string, len(spec.Variables))
		for j, v := range spec.Variables {
			names[j] = v.Name
		}
		out[i] = descriptor.OptimizeSummary{
			Name:        spec.Name,
			Direction:   spec.Direction,
			Variables:   names,
			Constraints: len(spec.Constraints),
			Starts:      spec.EffectiveStarts(),
			RNGSeed:     optblock.EffectiveSeed(spec),
		}
	}
	return out
}
