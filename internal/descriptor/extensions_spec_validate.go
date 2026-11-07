package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/extspec"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// ExtensionSpecValidators carries every non-regression registration's
// opt-in, spec-only Validate hook (the extend.SpecValidator field on
// pulse.AggregatorRegistration, AttributeRegistration, …), keyed by
// registered operator name, per category. A registration without the
// hook is absent. The matrix category's hook rides
// ExtensionMatrixOp.Validate and the regression's
// ExtensionRegression.Validate, beside the rest of their details.
// Predict and the runtime adapters both run a hook through the
// category's Extension*ValidateRefusal below, so they report the same
// refusal.
type ExtensionSpecValidators struct {
	Aggregators map[types.AggregationType]func(*types.Aggregation) error
	Attributes  map[types.AttributeType]func(*types.Attribute) error
	Filterers   map[types.FiltererType]func(*types.Filterer) error
	Groupers    map[types.GroupType]func(*types.Group) error
	Windows     map[types.WindowType]func(*types.Window) error
	Features    map[types.FeatureType]func(*types.Feature) error
	// RowTests and PostTests split the test category by tier: a tier-1
	// test rides Request.Tests, a tier-2 one Request.PostTests.
	RowTests  map[types.TestType]func(*types.Test) error
	PostTests map[types.TestType]func(*types.Test) error
}

// The request slot key each category's spec rides, as a Validate
// refusal's details.slot names it. The runtime factory cannot tell a
// top-level aggregation from a crosstab cell, so the key is the
// category's own slot, never the path.
const (
	extSpecSlotAggregations = "aggregations"
	extSpecSlotAttributes   = "attributes"
	extSpecSlotFilterers    = "filterers"
	extSpecSlotGroups       = "groups"
	extSpecSlotWindows      = "windows"
	extSpecSlotFeatures     = "features"
	extSpecSlotTests        = "tests"
	extSpecSlotPostTests    = "post_tests"
	extSpecSlotMatrices     = "matrices"
)

// ExtensionAggregatorValidateRefusal is validate's verdict on an
// extension aggregation spec (extspec.Refusal: a coded error kept, an
// uncoded one or a panic PULSE_EXTENSION_SPEC_REFUSED {category:
// "aggregator", type, slot: "aggregations"[, name: label]}); nil when
// validate is nil or accepts spec. Predict and the runtime adapter
// (before the factory) both call it.
func ExtensionAggregatorValidateRefusal(spec *types.Aggregation, validate func(*types.Aggregation) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: extCategoryAggregator, Type: string(spec.Type), Slot: extSpecSlotAggregations, Name: spec.Label})
}

// ExtensionAttributeValidateRefusal is the attribute twin
// (slot "attributes", name the label).
func ExtensionAttributeValidateRefusal(spec *types.Attribute, validate func(*types.Attribute) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: extCategoryAttribute, Type: string(spec.Type), Slot: extSpecSlotAttributes, Name: spec.Label})
}

// ExtensionFiltererValidateRefusal is the filterer twin (slot
// "filterers"; a filterer carries no name). The runtime calls it
// before the builder's Build — the call that sees the spec.
func ExtensionFiltererValidateRefusal(spec *types.Filterer, validate func(*types.Filterer) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: "filterer", Type: string(spec.Type), Slot: extSpecSlotFilterers})
}

// ExtensionGrouperValidateRefusal is the grouper twin (slot "groups";
// a group carries no name).
func ExtensionGrouperValidateRefusal(spec *types.Group, validate func(*types.Group) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: "grouper", Type: string(spec.Type), Slot: extSpecSlotGroups})
}

// ExtensionWindowValidateRefusal is the window twin (slot "windows",
// name the label).
func ExtensionWindowValidateRefusal(spec *types.Window, validate func(*types.Window) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: "window", Type: string(spec.Type), Slot: extSpecSlotWindows, Name: spec.Label})
}

// ExtensionFeatureValidateRefusal is the feature twin (slot
// "features", name the label).
func ExtensionFeatureValidateRefusal(spec *types.Feature, validate func(*types.Feature) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: "feature", Type: string(spec.Type), Slot: extSpecSlotFeatures, Name: spec.Label})
}

// ExtensionTestValidateRefusal is the test twin: slot "tests" for a
// tier-1 (row) test, "post_tests" for a tier-2 one; name the label.
func ExtensionTestValidateRefusal(spec *types.Test, post bool, validate func(*types.Test) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	slot := extSpecSlotTests
	if post {
		slot = extSpecSlotPostTests
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: extCategoryTest, Type: string(spec.Type), Slot: slot, Name: spec.Label})
}

// ExtensionMatrixValidateRefusal is the matrix twin (slot "matrices",
// name the matrix name).
func ExtensionMatrixValidateRefusal(spec *types.MatrixSpec, validate func(*types.MatrixSpec) error) *errors.CodedError {
	if spec == nil || validate == nil {
		return nil
	}
	return extspec.Refusal(validate, spec, extspec.Site{Category: extCategoryMatrix, Type: string(spec.Type), Slot: extSpecSlotMatrices, Name: spec.Name})
}

// validateExtensionSpecs runs every extension slot's Validate hook
// (the categories above; regressions run theirs in validateRegression).
// The caller runs it only when the request body's own rules reported
// nothing — the runtime judges its own rules before any factory, so a
// spec an engine rule refuses is that rule's refusal, never the
// hook's. A type the instance hides is never consulted (it is refused
// as unknown upstream). Each hook is handed the slot as the runtime
// factory receives it (prepareExtensionSpecs: expression params
// resolved, weights stamped, vector inputs rewritten), never the slot
// as written; a slot carrying an expression param predict cannot
// compute is left to the runtime. Slots are walked in request order,
// crosstab axes and cells after the top-level slots.
func validateExtensionSpecs(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || !extensionsFromOpts(opts).hasSpecValidators() {
		return
	}
	judgeExtensionSpecs(env, prepareExtensionSpecs(req, schema, opts), opts)
}

// validateFacetExtensionSpecs is validateExtensionSpecs over a facet's
// filterers, their expression params resolved over the facet's params
// and loaded models (facetExtensionSpecs) — the runtime's
// processing.ResolveExtensionScalarFilterers before FacetSchema builds
// them.
func validateFacetExtensionSpecs(env *descriptor.Envelope, req *types.FacetRequest, models model.Catalog, opts *PredictOptions) {
	if req == nil || !extensionsFromOpts(opts).hasSpecValidators() {
		return
	}
	judgeExtensionSpecs(env, facetExtensionSpecs(req, models, opts), opts)
}

// judgeExtensionSpecs runs every hook over view's slots
// (validateExtensionSpecs).
func judgeExtensionSpecs(env *descriptor.Envelope, view *extensionSpecView, opts *PredictOptions) {
	snap := extensionsFromOpts(opts)
	inst := opts.instance()
	v := snap.SpecValidators
	req := view.req
	add := func(cerr *errors.CodedError) {
		if cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	}
	judged := func(name, path string) bool { return !inst.Hidden(name) && view.judged(path) }
	at := func(slot string, i int) string { return slot + "[" + strconv.Itoa(i) + "]" }
	agg := func(a *types.Aggregation, path string) {
		if a != nil && judged(string(a.Type), path) {
			add(ExtensionAggregatorValidateRefusal(a, v.Aggregators[a.Type]))
		}
	}
	group := func(g *types.Group, path string) {
		if g != nil && judged(string(g.Type), path) {
			add(ExtensionGrouperValidateRefusal(g, v.Groupers[g.Type]))
		}
	}
	for i, f := range req.Features {
		if f != nil && judged(string(f.Type), at(extSpecSlotFeatures, i)) {
			add(ExtensionFeatureValidateRefusal(f, v.Features[f.Type]))
		}
	}
	for i, f := range req.Filterers {
		if f != nil && judged(string(f.Type), at(extSpecSlotFilterers, i)) {
			add(ExtensionFiltererValidateRefusal(f, v.Filterers[f.Type]))
		}
	}
	for i, a := range req.Attributes {
		if a != nil && judged(string(a.Type), at(extSpecSlotAttributes, i)) {
			add(ExtensionAttributeValidateRefusal(a, v.Attributes[a.Type]))
		}
	}
	for i, g := range req.Groups {
		group(g, at(extSpecSlotGroups, i))
	}
	for i, a := range req.Aggregations {
		agg(a, at(extSpecSlotAggregations, i))
	}
	for i, w := range req.Windows {
		if w != nil && judged(string(w.Type), at(extSpecSlotWindows, i)) {
			add(ExtensionWindowValidateRefusal(w, v.Windows[w.Type]))
		}
	}
	for i, t := range req.Tests {
		if t != nil && judged(string(t.Type), at(extSpecSlotTests, i)) {
			add(ExtensionTestValidateRefusal(view.test(i, t), false, v.RowTests[t.Type]))
		}
	}
	for i, t := range req.PostTests {
		if t != nil && judged(string(t.Type), at(extSpecSlotPostTests, i)) {
			add(ExtensionTestValidateRefusal(groupedtests.WithoutPartition(t), true, v.PostTests[t.Type]))
		}
	}
	for i := range req.Matrices {
		m := &req.Matrices[i]
		if judged(string(m.Type), at(extSpecSlotMatrices, i)) {
			add(ExtensionMatrixValidateRefusal(view.matrix(i), snap.MatrixDetails[string(m.Type)].Validate))
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, g := range ct.Rows {
			group(g, at("crosstab.rows", i))
		}
		for i, g := range ct.Columns {
			group(g, at("crosstab.columns", i))
		}
		agg(ct.Cell, "crosstab.cell")
		for i, a := range ct.Cells {
			agg(a, at("crosstab.cells", i))
		}
		for i, a := range ct.MarginAggregations {
			agg(a, at("crosstab.margin_aggregations", i))
		}
	}
}
