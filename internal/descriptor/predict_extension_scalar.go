package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/extscalar"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// predict_extension_scalar.go is predict's half of the extension
// scalar-param pass (internal/extscalar): an embedder number param
// declared expression-capable (pulse.ParamMeta.Expression) judged
// statically over the request's params and the catalog its references
// resolve against — the twin of the runtime's
// processing.ResolveExtensionScalars, over the same environment as the
// built-in window params (windowScalarEnv: a read of the request's own
// loaded `models` folds to the runtime's value).

// extensionScalarKeys are the instance's visible extension operators'
// expression-capable params keys, sorted per operator; nil when none
// declares one.
func extensionScalarKeys(inst *InstanceSnapshot) map[paramread.Key][]string {
	return snapshotScalarKeys(inst.Extensions())
}

// snapshotScalarKeys is extensionScalarKeys over an extension snapshot
// (every registration, hidden or not).
func snapshotScalarKeys(snap *ExtensionsSnapshot) map[paramread.Key][]string {
	if snap == nil {
		return nil
	}
	var out map[paramread.Key][]string
	cats := [...]struct {
		cat   paramread.Category
		metas []descriptor.OperatorMeta
	}{
		{paramread.Aggregator, snap.Aggregators}, {paramread.Attribute, snap.Attributes},
		{paramread.Filterer, snap.Filterers}, {paramread.Grouper, snap.Groupers},
		{paramread.Window, snap.Windows}, {paramread.Feature, snap.Features},
		{paramread.Test, snap.Tests}, {paramread.Regression, snap.Regressions},
	}
	for _, c := range cats {
		for _, m := range c.metas {
			for _, p := range m.Params {
				if p.Expression {
					out = addScalarKey(out, c.cat, m.Name, p.Name)
				}
			}
		}
	}
	for _, m := range snap.MatrixOps {
		for _, p := range snap.MatrixDetails[m.Name].Params {
			if p.Expression {
				out = addScalarKey(out, paramread.Matrix, m.Name, p.Name)
			}
		}
	}
	for k, v := range out {
		slices.Sort(v)
		out[k] = slices.Compact(v)
	}
	return out
}

// addScalarKey records key under (cat, name) in out, allocating out on
// first use.
func addScalarKey(out map[paramread.Key][]string, cat paramread.Category, name, key string) map[paramread.Key][]string {
	if out == nil {
		out = map[paramread.Key][]string{}
	}
	k := paramread.Key{Category: cat, Op: name}
	out[k] = append(out[k], key)
	return out
}

// SweepOptions is the instance facts the item for_each and compose sweep
// expansions judge with (sweep.Options): ExprParams is the visible
// extension operators' expression-capable params keys by operator name
// — predict's twin of the runtime's
// processing.ExtensionRegistry.ScalarParamsByOperator, so an axis
// token in one is refused identically on both sides. A hidden operator
// flags nothing. The zero value when none flags a param. Nil-safe.
func (s *InstanceSnapshot) SweepOptions() sweep.Options {
	keys := extensionScalarKeys(s)
	if len(keys) == 0 {
		return sweep.Options{}
	}
	var out map[string][]string
	for k, v := range keys {
		if s.Hidden(k.Op) {
			continue
		}
		if out == nil {
			out = make(map[string][]string, len(keys))
		}
		out[k.Op] = append(out[k.Op], v...)
	}
	return sweep.Options{ExprParams: out}
}

// extensionFlagged is the extscalar.Flagged of the instance opts
// carries; nil when no visible extension operator declares an
// expression-capable param.
func extensionFlagged(opts *PredictOptions) extscalar.Flagged {
	inst := opts.instance()
	keys := extensionScalarKeys(inst)
	if len(keys) == 0 {
		return nil
	}
	return func(cat paramread.Category, op string) []string {
		if inst.Hidden(op) {
			return nil
		}
		return keys[paramread.Key{Category: cat, Op: op}]
	}
}

// resolveExtensionScalarsStatic runs the static pass over req.
func resolveExtensionScalarsStatic(req *types.Request, opts *PredictOptions) extscalar.Result {
	flagged := extensionFlagged(opts)
	if flagged == nil {
		return extscalar.Result{Request: req}
	}
	// Built here, not in the closure: capturing opts would move every
	// caller's options to the heap (the off-path allocation gate).
	env := windowScalarEnv(req, opts)
	return extscalar.Resolve(req, flagged, staticEnv(env), false)
}

// staticEnv is the static judgement's environment: env and its
// declaration-only bound.
func staticEnv(env scalarslot.Env) extscalar.EnvFunc {
	return func() (scalarslot.Env, exprenv.Bound) { return env, env.Declared() }
}

// validateExtensionScalars reports every refused expression-capable
// extension param of req, in walk order (the runtime raises the first).
func validateExtensionScalars(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	if req == nil {
		return
	}
	for _, ce := range resolveExtensionScalarsStatic(req, opts).Refusals {
		addCodedError(env, ce)
	}
}

// extensionUnresolved lists req's expression-capable extension params
// the static pass left to the runtime, each path prefix + its slot path
// (req is the item-expanded request). Nil when none.
func extensionUnresolved(req *types.Request, opts *PredictOptions, prefix string) []descriptor.UnresolvedScalar {
	if req == nil {
		return nil
	}
	var out []descriptor.UnresolvedScalar
	for _, u := range resolveExtensionScalarsStatic(req, opts).Unresolved {
		out = append(out, descriptor.UnresolvedScalar{Path: prefix + u.Path, Expression: u.Expression, Reads: u.Reads})
	}
	return out
}

// requestUnresolved is every scalar slot of req the static pass left to
// the runtime: its built-in window params, then its expression-capable
// extension params, then its AGG_CONSTANT expressions.
func requestUnresolved(req *types.Request, opts *PredictOptions, prefix string) []descriptor.UnresolvedScalar {
	out := windowUnresolved(req, opts, prefix)
	out = append(out, extensionUnresolved(req, opts, prefix)...)
	return append(out, constantUnresolved(req, opts, prefix)...)
}

// validateFacetExtensionScalars reports every refused expression-capable
// param of a facet's extension filterers, judged over the facet's
// params and its loaded models — the runtime's pass right after it
// binds them (Service.facetSchema) — and returns the ones calling a
// request-bound function, which the runtime judges alone
// (FacetValidationResult.UnresolvedScalars), each at filterers[i]; nil
// when none.
func validateFacetExtensionScalars(env *descriptor.Envelope, req *types.FacetRequest, models model.Catalog, opts *PredictOptions) []descriptor.UnresolvedScalar {
	o := opts.withModels(models)
	flagged := extensionFlagged(o)
	if flagged == nil || req == nil {
		return nil
	}
	_, unresolved, refs := extscalar.ResolveFilterers("filterers", req.Filterers, flagged, staticEnv(foldingScalarEnv(req.Params, o)), false)
	for _, ce := range refs {
		addCodedError(env, ce)
	}
	var out []descriptor.UnresolvedScalar
	for _, u := range unresolved {
		out = append(out, descriptor.UnresolvedScalar{Path: u.Path, Expression: u.Expression, Reads: u.Reads})
	}
	return out
}
