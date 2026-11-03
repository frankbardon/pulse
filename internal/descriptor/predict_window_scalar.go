package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/windowscalar"
	"github.com/frankbardon/pulse/types"
)

// predict_window_scalar.go is predict's half of the window scalar-param
// pass (internal/windowscalar): the static judgement over the request's
// params and the catalog its references resolve against
// (PredictOptions.boundModels — a Compose slot's fits, a request's
// loaded `models`), the twin of the runtime's
// processing.Processor.resolveWindowScalars.

// windowScalarEnv is the static scalar-slot environment of req: its
// params (an invalid block binds none — its refusal is reported
// elsewhere, and the runtime folds none either), the catalog opts
// carries, the instance's tables and the embedder's functions.
func windowScalarEnv(req *types.Request, opts *PredictOptions) scalarslot.Env {
	return scalarEnvFor(req.Params, opts)
}

// scalarEnvFor is the static scalar-slot environment of a request host
// carrying params, over the catalog opts carries.
func scalarEnvFor(p types.Params, opts *PredictOptions) scalarslot.Env {
	params, _ := exprenv.NormalizeParams(p)
	tables := ExprTables(extensionsFromOpts(opts), opts.instance())
	return scalarslot.Env{
		Params:     params,
		Fits:       opts.boundModels(),
		Tables:     tables,
		Extensions: predictExprFunctionNames(opts),
		Lookup:     tables != nil && tables.HasLookup(),
	}
}

// resolveWindowScalarsStatic runs the static pass over one window: the
// window the reading judges, its deferred keys, the unresolved params
// and every refusal.
func resolveWindowScalarsStatic(w *types.Window, env scalarslot.Env) (windowscalar.Resolved, []*windowscalar.Refusal) {
	return windowscalar.Resolve(w, env, env.Declared(), false)
}

// windowUnresolved lists req's scalar window params the static pass
// left to the runtime, each path prefix + "windows[j].params.<key>"
// (req is the item-expanded request). Nil when none.
func windowUnresolved(req *types.Request, opts *PredictOptions, prefix string) []descriptor.UnresolvedScalar {
	if req == nil {
		return nil
	}
	var out []descriptor.UnresolvedScalar
	var env *scalarslot.Env
	for j, w := range req.Windows {
		if len(windowscalar.Exprs(w)) == 0 {
			continue
		}
		if env == nil {
			e := windowScalarEnv(req, opts)
			env = &e
		}
		res, _ := resolveWindowScalarsStatic(w, *env)
		for _, u := range res.Unresolved {
			out = append(out, descriptor.UnresolvedScalar{
				Path:       prefix + "windows[" + strconv.Itoa(j) + "].params." + u.Key,
				Expression: u.Expression,
				Reads:      u.Reads,
			})
		}
	}
	return out
}

// composeWindowUnresolved is the batch's scalar params — built-in
// window params, then expression-capable extension params — the static
// pass left to the runtime: every slot's (requests[i]., judged on its
// normalized request against its catalog), then every request-bound
// fit's (fits[i].request., against the populations and its own loaded
// entries; a from_sweep fit's request is a sweep slot, listed there).
func composeWindowUnresolved(req *types.ComposedRequest, slots []*types.Request, catalogs []model.Catalog, fitCatalog func(i int) model.Catalog, opts *PredictOptions) []descriptor.UnresolvedScalar {
	var out []descriptor.UnresolvedScalar
	for i, slot := range req.Requests {
		if i < len(slots) && slots[i] != nil {
			slot = slots[i]
		}
		var cat model.Catalog
		if i < len(catalogs) {
			cat = catalogs[i]
		}
		out = append(out, requestUnresolved(slot, opts.withModels(cat), "requests["+strconv.Itoa(i)+"].")...)
	}
	l := opts.instance().Limits()
	for i, f := range req.Fits {
		if f.FromSweep != nil || f.Request == nil {
			continue
		}
		fr := f.Request
		if expanded, _, _, err := ExpandRequestItems(fr, l); err == nil {
			fr = expanded
		}
		out = append(out, requestUnresolved(fr, opts.withModels(fitCatalog(i)), "fits["+strconv.Itoa(i)+"].request.")...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
