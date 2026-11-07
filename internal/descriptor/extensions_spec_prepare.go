package descriptor

import (
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/extscalar"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// extensions_spec_prepare.go replays, on a copy, the rewrites the
// runtime applies to a request before any extension factory sees a
// slot, so an extension's spec-only Validate hook judges at predict the
// spec it is handed at runtime:
//
//  1. the expression-capable extension params resolved
//     (internal/extscalar — processing.ResolveExtensionScalars);
//  2. the weight in force stamped on every slot (weighting.Stamp —
//     processing.StampWeightsWith);
//  3. the vector-input rewrites of Processor.prepare: attributes
//     (vectors.RewriteAttributeInputs), groupers
//     (vectors.RewriteGroupInputs) and aggregations
//     (vectors.RewriteAggregationInputs);
//  4. per slot, at the walk: a tier-1 test's resolved member sets
//     (vectors.TestSlotRewrite), a tier-2 test's partition key removed
//     (groupedtests.WithoutPartition) and an extension matrix
//     operator's engine-owned params stripped
//     (vectors.ExtensionMatrixSpec).
//
// Every rewrite is the runtime's own function, never a copy of it.
// Predict's other output never reads the prepared request: it feeds
// the hooks alone.

// extensionSpecView is a request as the runtime's extension factories
// see it (steps 1-3 above), plus what the per-slot steps need.
type extensionSpecView struct {
	req *types.Request
	// deferred are the slot paths ("aggregations[0]", "crosstab.cell",
	// …) carrying an expression param the static pass could not compute
	// (or refused): the runtime alone judges such a slot's hook, since
	// predict does not know the number the hook would read.
	deferred map[string]bool
	// tests are the tier-1 tests' resolved member sets, index-aligned
	// with req.Tests (nil when none reads a member set or resolution
	// failed).
	tests []vectors.TestInput
	// matrices are the resolved matrix plans by spec index.
	matrices map[int]vectors.Matrix
}

// hasAny reports whether any category carries a Validate hook.
func (v ExtensionSpecValidators) hasAny() bool {
	return len(v.Aggregators) > 0 || len(v.Attributes) > 0 || len(v.Filterers) > 0 ||
		len(v.Groupers) > 0 || len(v.Windows) > 0 || len(v.Features) > 0 ||
		len(v.RowTests) > 0 || len(v.PostTests) > 0
}

// hasSpecValidators reports whether snap registers any spec-only
// Validate hook (every category, matrices and regressions included).
// Nil-safe.
func (s *ExtensionsSnapshot) hasSpecValidators() bool {
	if s == nil {
		return false
	}
	if s.SpecValidators.hasAny() {
		return true
	}
	for _, d := range s.MatrixDetails {
		if d.Validate != nil {
			return true
		}
	}
	for _, d := range s.RegressionDetails {
		if d.Validate != nil {
			return true
		}
	}
	return false
}

// prepareExtensionSpecs builds req's extensionSpecView against schema
// and opts' instance, default weight and bound models. req is never
// mutated.
func prepareExtensionSpecs(req *types.Request, schema *encoding.Schema, opts *PredictOptions) *extensionSpecView {
	inst := opts.instance()
	snap := extensionsFromOpts(opts)
	if snap == nil {
		snap = inst.Extensions()
	}
	view := &extensionSpecView{}

	// 1. Expression params.
	res := resolveExtensionScalarsStatic(req, opts)
	r := res.Request
	for _, u := range res.Unresolved {
		view.defer_(u.Path)
	}
	for _, ce := range res.Refusals {
		if p, ok := ce.Details["path"].(string); ok {
			view.defer_(p)
		}
	}

	// 2. Weights.
	r = weighting.Stamp(r, opts.defaultWeight(), func(category, op string) bool {
		if inst.Hidden(op) {
			return false
		}
		_, aware := snap.WeightAwareness(category, op)
		return aware
	})

	// 3. Vector inputs.
	models := opts.boundModels()
	named := func(metas func(*ExtensionsSnapshot) []descriptor.OperatorMeta) func(string) bool {
		if snap == nil {
			return nil
		}
		return func(name string) bool { return snapshotHasName(metas(snap), name) }
	}
	cls := classifierOf(snap, models)
	r = vectors.RewriteAttributeInputs(r, schema, cls, func(t types.AttributeType) types.AttributeType {
		return opRoute(inst, t)
	}, named(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Attributes }))
	r = vectors.RewriteGroupInputs(r, schema, cls, func(t types.GroupType) types.GroupType {
		return opRoute(inst, t)
	}, named(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Groupers }))
	r = vectors.RewriteAggregationInputs(r, schema, cls, aggregationKinds(inst, snap))
	view.req = r

	// 4. What the per-slot rewrites read.
	if len(r.Tests) > 0 {
		if inputs, err := vectors.ResolveTests(r, schema, cls, func(t types.TestType) types.TestType {
			return opRoute(inst, t)
		}, named(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Tests })); err == nil {
			view.tests = inputs
		}
	}
	if len(r.Matrices) > 0 {
		if plans, err := resolveMatricesFor(r, schema, snap, inst, models); err == nil {
			view.matrices = make(map[int]vectors.Matrix, len(plans))
			for _, m := range plans {
				view.matrices[m.Index] = m
			}
		}
	}
	return view
}

// facetExtensionSpecs is prepareExtensionSpecs over a facet's
// filterers: their expression params resolved over the facet's params
// and its loaded models (the runtime's
// processing.ResolveExtensionScalarFilterers); a filterer carries no
// weight and no vector input.
func facetExtensionSpecs(req *types.FacetRequest, models model.Catalog, opts *PredictOptions) *extensionSpecView {
	view := &extensionSpecView{req: &types.Request{Filterers: req.Filterers}}
	o := opts.withModels(models)
	flagged := extensionFlagged(o)
	if flagged == nil {
		return view
	}
	fs, unresolved, refs := extscalar.ResolveFilterers(extSpecSlotFilterers, req.Filterers, flagged, staticEnv(foldingScalarEnv(req.Params, o)), false)
	view.req.Filterers = fs
	for _, u := range unresolved {
		view.defer_(u.Path)
	}
	for _, ce := range refs {
		if p, ok := ce.Details["path"].(string); ok {
			view.defer_(p)
		}
	}
	return view
}

// defer_ records the slot owning the param at path (its prefix before
// the first ".params.").
func (v *extensionSpecView) defer_(path string) {
	slot, _, _ := strings.Cut(path, ".params.")
	if v.deferred == nil {
		v.deferred = map[string]bool{}
	}
	v.deferred[slot] = true
}

// judged reports whether predict judges the hook of the slot at path
// (false: an expression param left to the runtime).
func (v *extensionSpecView) judged(path string) bool {
	return !v.deferred[path]
}

// test is the spec tier-1 test i's factory receives.
func (v *extensionSpecView) test(i int, t *types.Test) *types.Test {
	if i < len(v.tests) {
		return vectors.TestSlotRewrite(t, v.tests[i])
	}
	return t
}

// matrix is the spec matrix i's extension factory receives (the spec
// as stamped when its plan did not resolve).
func (v *extensionSpecView) matrix(i int) *types.MatrixSpec {
	spec := v.req.Matrices[i]
	if m, ok := v.matrices[i]; ok && m.Extension {
		spec = vectors.ExtensionMatrixSpec(spec, m)
	}
	return &spec
}
