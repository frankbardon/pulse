package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/extspec"
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
// as unknown upstream). Slots are walked in request order, crosstab
// axes and cells after the top-level slots.
func validateExtensionSpecs(env *descriptor.Envelope, req *types.Request, opts *PredictOptions) {
	snap := extensionsFromOpts(opts)
	if req == nil || snap == nil {
		return
	}
	inst := opts.instance()
	v := snap.SpecValidators
	add := func(cerr *errors.CodedError) {
		if cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	}
	visible := func(name string) bool { return !inst.Hidden(name) }
	agg := func(a *types.Aggregation) {
		if a != nil && visible(string(a.Type)) {
			add(ExtensionAggregatorValidateRefusal(a, v.Aggregators[a.Type]))
		}
	}
	group := func(g *types.Group) {
		if g != nil && visible(string(g.Type)) {
			add(ExtensionGrouperValidateRefusal(g, v.Groupers[g.Type]))
		}
	}
	for _, f := range req.Features {
		if f != nil && visible(string(f.Type)) {
			add(ExtensionFeatureValidateRefusal(f, v.Features[f.Type]))
		}
	}
	for _, f := range req.Filterers {
		if f != nil && visible(string(f.Type)) {
			add(ExtensionFiltererValidateRefusal(f, v.Filterers[f.Type]))
		}
	}
	for _, a := range req.Attributes {
		if a != nil && visible(string(a.Type)) {
			add(ExtensionAttributeValidateRefusal(a, v.Attributes[a.Type]))
		}
	}
	for _, g := range req.Groups {
		group(g)
	}
	for _, a := range req.Aggregations {
		agg(a)
	}
	for _, w := range req.Windows {
		if w != nil && visible(string(w.Type)) {
			add(ExtensionWindowValidateRefusal(w, v.Windows[w.Type]))
		}
	}
	for _, t := range req.Tests {
		if t != nil && visible(string(t.Type)) {
			add(ExtensionTestValidateRefusal(t, false, v.RowTests[t.Type]))
		}
	}
	for _, t := range req.PostTests {
		if t != nil && visible(string(t.Type)) {
			add(ExtensionTestValidateRefusal(t, true, v.PostTests[t.Type]))
		}
	}
	for i := range req.Matrices {
		m := &req.Matrices[i]
		if visible(string(m.Type)) {
			add(ExtensionMatrixValidateRefusal(m, snap.MatrixDetails[string(m.Type)].Validate))
		}
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			group(g)
		}
		for _, g := range ct.Columns {
			group(g)
		}
		agg(ct.Cell)
		for _, a := range ct.Cells {
			agg(a)
		}
		for _, a := range ct.MarginAggregations {
			agg(a)
		}
	}
}
