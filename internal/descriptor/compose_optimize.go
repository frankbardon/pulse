package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/internal/tablecall"
	"github.com/frankbardon/pulse/types"
)

// OptimizeRefusal is the ComposedRequest `optimize` blocks' rule set
// (optblock.CheckLoaded) over the batch as it runs — its top-level
// params (already validated by InheritComposeParams) and its `fits`
// (the names coef() resolves; sweepAxes, the batch's sweep's axis
// names, are the axes sweep_axis() may read off a sweep-bound one —
// nil: unknown) — with the instance's embedder
// ExprFunction names, its named tables (ExprTables: lookup() declared
// when a LookupTable is registered; literal lookup() / range_label()
// calls judged by internal/tablecall), the
// instance's registered populations (outer: the namespace a block's
// `models` entry may not rebind, with the fits) and the loader that
// reads a block's `models` files. Predict (validateComposeExpanded) and
// the runtime (Service.compose / composeParallel) call it at the same
// point, right after model.CheckFits, so both refuse the same block
// identically. Every result(slot, aggregation) call is judged against
// the batch's effective slots (resultcall.CatalogOf over req.Requests,
// sweep and banner slots included). It returns each block's decoded `models` files
// (index-aligned; what the runtime solves with) — nil, nil when the
// batch declares no block.
func OptimizeRefusal(req *types.ComposedRequest, sweepAxes []string, exprFunctions []string, tables tablecall.Catalog, outer model.Catalog, load optblock.Loader) ([]*model.Loaded, *errors.CodedError) {
	checked, cerr := OptimizeCheck(req, sweepAxes, exprFunctions, tables, outer, load)
	if cerr != nil || checked == nil {
		return nil, cerr
	}
	return checked.Loaded, nil
}

// OptimizeCheck is OptimizeRefusal that also returns the bounds and
// starts the declaration-only check left unresolved (optblock.CheckAll)
// — predict's ComposeValidationResult.UnresolvedScalars. Nil, nil when
// the batch declares no block.
func OptimizeCheck(req *types.ComposedRequest, sweepAxes []string, exprFunctions []string, tables tablecall.Catalog, outer model.Catalog, load optblock.Loader) (*optblock.Checked, *errors.CodedError) {
	if req == nil || len(req.Optimize) == 0 {
		return nil, nil
	}
	params, _ := exprenv.NormalizeParams(req.Params)
	fits := model.CatalogFromFits(req.Fits).WithSweepAxes(sweepAxes)
	return optblock.CheckAll(req.Optimize, optblock.Env{
		Params:     params,
		Fits:       fits,
		Extensions: exprFunctions,
		Lookup:     tables != nil && tables.HasLookup(),
		Tables:     tables,
		Outer:      model.WithinCatalog(outer, fits),
		Load:       load,
		Results:    resultcall.CatalogOf(req.Requests),
	})
}

// optimizeUnresolved is the blocks' unresolved bounds and starts as
// ComposeValidationResult.UnresolvedScalars entries (nil without any).
func optimizeUnresolved(checked *optblock.Checked) []descriptor.UnresolvedScalar {
	if checked == nil || len(checked.Unresolved) == 0 {
		return nil
	}
	out := make([]descriptor.UnresolvedScalar, len(checked.Unresolved))
	for i, u := range checked.Unresolved {
		out[i] = descriptor.UnresolvedScalar{Path: u.Field, Expression: u.Expression, Reads: u.Reads}
	}
	return out
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
