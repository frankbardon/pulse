package processing

import (
	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/extscalar"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

// ResolveExtensionScalars is the runtime's request scalar pre-resolution
// pass. First every AGG_CONSTANT params.expr (internal/aggconst) is
// evaluated and rewritten into the literal params.value it stands for;
// then the extension scalar-param pass (internal/extscalar): every
// expression-capable extension param
// (pulse.ParamMeta.Expression) written as an expression string is
// evaluated against the request's params (exts' fold), its bound
// models (coef(), sweep_axis()), the instance's tables and the
// embedder's functions, and written as the number the extension's
// factory decodes. The first refusal is raised (predict reports it
// first). req comes back as itself when no slot carries such an
// expression; otherwise a copy (the caller's request is never mutated).
//
// Every engine entry applies it ahead of Processor.prepare — Process,
// RunCrosstab, RunCrosstabFused, DeriveRecords — and so must every arm
// that builds operators off a request without a Processor (the
// parallel decode and shard reducers, a facet's filterers).
func ResolveExtensionScalars(req *types.Request, exts *ExtensionRegistry, models *model.Set) (*types.Request, error) {
	req, err := resolveConstantScalars(req, exts, models)
	if err != nil {
		return nil, err
	}
	if req == nil || exts == nil || len(exts.ScalarParams) == 0 {
		return req, nil
	}
	res := extscalar.Resolve(req, exts.ExtensionScalarParams, extensionScalarEnv(exts, models), true)
	if len(res.Refusals) > 0 {
		return nil, res.Refusals[0]
	}
	return res.Request, nil
}

// resolveConstantScalars is the AGG_CONSTANT half of the pass: every
// slot's params.expr evaluated against the request's params, bound
// models, tables and expression functions (run true) and written as
// params.value; the first refusal is raised (predict reports it first).
// A hidden AGG_CONSTANT is left to the unknown-aggregation refusal.
func resolveConstantScalars(req *types.Request, exts *ExtensionRegistry, models *model.Set) (*types.Request, error) {
	if req == nil || exts.isHidden(string(types.AGG_CONSTANT)) {
		return req, nil
	}
	res := aggconst.ResolveRequest(req, aggconst.EnvFunc(extensionScalarEnv(exts, models)), true)
	if len(res.Refusals) > 0 {
		return nil, res.Refusals[0].Refusal.Coded()
	}
	return res.Request, nil
}

// ResolveExtensionScalarFilterers is ResolveExtensionScalars over a
// filterer list alone (a facet's), located at prefix[i].
func ResolveExtensionScalarFilterers(prefix string, fs []*types.Filterer, exts *ExtensionRegistry, models *model.Set) ([]*types.Filterer, error) {
	if len(fs) == 0 || exts == nil || len(exts.ScalarParams) == 0 {
		return fs, nil
	}
	out, _, refs := extscalar.ResolveFilterers(prefix, fs, exts.ExtensionScalarParams, extensionScalarEnv(exts, models), true)
	if len(refs) > 0 {
		return nil, refs[0]
	}
	return out, nil
}

// extensionScalarEnv is the runtime environment of the pass: the
// scalar-slot environment of exts and models (scalarEnvOf) and the
// bound carrying their implementations.
func extensionScalarEnv(exts *ExtensionRegistry, models *model.Set) extscalar.EnvFunc {
	return func() (scalarslot.Env, exprenv.Bound) {
		return scalarEnvOf(exts, models), exts.ExprBoundWithModels(models)
	}
}

// resolveExtensionScalars is ResolveExtensionScalars with this
// processor's registry and bound models.
func (p *Processor) resolveExtensionScalars(req *types.Request) (*types.Request, error) {
	return ResolveExtensionScalars(req, p.exts, p.models)
}
