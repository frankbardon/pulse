package descriptor

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// FieldRefRefusal is the ONE field-reference rule: the first name a
// Request's slots reference that the pipeline never makes available,
// as the coded refusal both sides return — nil when every name
// resolves. The runtime calls it once per Request after smart defaults
// and zone resolution, against the schema it executes over (the
// cohort's, the joined schema, or a chain stage's synthesised input),
// before any record is read; predict and the Compose / chain validators
// report every entry of FieldRefRefusals at the same point. A runtime
// that skipped it would treat each unknown name as an all-null column
// and answer with wrong numbers and no error.
//
// The same walk compiles every expression it meets (ATTR_FORMULA,
// FILTER_EXPRESSION, a total() argument) against the columns visible at
// its pipeline point, refusing a failure as PULSE_FORMULA_COMPILE_ERROR
// (predict_formula.go, checkCompile).
func FieldRefRefusal(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot) error {
	if all := FieldRefRefusals(req, schema, snap); len(all) > 0 {
		return all[0]
	}
	return nil
}

// ScopedFieldRefRefusal is FieldRefRefusal for one instance: the
// extension projection is inst's, and an operator inst hides is walked
// as a never-registered name (its type-specific slots, params and
// output labels are not judged). The runtime's form; nil inst is
// FieldRefRefusal(req, schema, nil).
func ScopedFieldRefRefusal(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) error {
	return ScopedFieldRefRefusalWithModels(req, schema, inst, nil)
}

// ScopedFieldRefRefusalWithModels is ScopedFieldRefRefusal for a
// Request whose ATTR_REG_PREDICT slots resolve against models — the
// runtime's bound set (a Compose slot's fits), predict's static
// model.FitCatalog. Nil binds nothing.
func ScopedFieldRefRefusalWithModels(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, models model.Catalog) error {
	return ScopedChainStageFieldRefRefusal(req, schema, nil, inst, models)
}

// ScopedChainStageFieldRefRefusal is ScopedFieldRefRefusalWithModels for
// a chain stage >= 1, whose synthesised input schema types every
// forwarded aggregation column f64: codes are the input columns that
// carry a categorical code (vectors.AggregationKinds.ChainCodeColumns),
// so the stage's own overlays judge an input-valued aggregation over
// one as the code it is. Nil codes is ScopedFieldRefRefusalWithModels.
func ScopedChainStageFieldRefRefusal(req *types.Request, schema *encoding.Schema, codes map[string]encoding.FieldType, inst *InstanceSnapshot, models model.Catalog) error {
	if all := fieldRefRefusalsCoded(req, schema, codes, inst.Extensions(), inst, models); len(all) > 0 {
		return all[0]
	}
	return nil
}

// ScopedFacetFieldRefRefusals is FacetFieldRefRefusals for one
// instance, on ScopedFieldRefRefusal's terms.
func ScopedFacetFieldRefRefusals(req *types.FacetRequest, schema *encoding.Schema, inst *InstanceSnapshot) []*errors.CodedError {
	return ScopedFacetFieldRefRefusalsWithModels(req, schema, inst, nil)
}

// ScopedFacetFieldRefRefusalsWithModels is ScopedFacetFieldRefRefusals
// for a FacetRequest whose filterers' coef() calls resolve against
// models — the runtime's loaded set, predict's static catalog of the
// request's `models`. Nil binds nothing.
func ScopedFacetFieldRefRefusalsWithModels(req *types.FacetRequest, schema *encoding.Schema, inst *InstanceSnapshot, models model.Catalog) []*errors.CodedError {
	if req == nil {
		return nil
	}
	return fieldRefRefusalsWithModels(&types.Request{Filterers: req.Filterers, TimeZoneField: req.TimeZoneField}, schema, inst.Extensions(), inst, models)
}

// FacetFieldRefRefusals is FieldRefRefusals for a FacetRequest: its
// filterers are judged by the Request filterer rule against the cohort
// schema (a facet has no features, so the schema is the whole column
// set). FacetSchema applies the first entry right after its zone pass;
// ValidateFacet reports every entry at the same point. Fields and
// AdditiveFields keep their own facet-specific refusals.
func FacetFieldRefRefusals(req *types.FacetRequest, schema *encoding.Schema, snap *ExtensionsSnapshot) []*errors.CodedError {
	if req == nil {
		return nil
	}
	return FieldRefRefusals(&types.Request{Filterers: req.Filterers, TimeZoneField: req.TimeZoneField}, schema, snap)
}

// FieldRefRefusals walks every slot that names a field, in PIPELINE
// order, against the columns available at that point:
//
//  0. vector fields — a native vec_f32 / vec_f64 field named in a slot
//     that reads one value (vectorScalarRefusals):
//     PULSE_VECTOR_FIELD_NOT_SCALAR, one per slot, reported first;
//  1. features — the schema plus each earlier feature's outputs
//     (Field; FEAT_TARGET_ENCODE params.target; FEAT_TRAIN_TEST_SPLIT
//     params.stratify);
//  2. filterers — the schema plus every feature output (attributes run
//     after filters, so their labels are NOT visible to a filter — save
//     a before-filters attribute's (ATTR_PREVIOUS_STATE), which an
//     expression filter may read: it runs over the whole cohort first);
//  3. attributes — as filterers plus each EARLIER attribute's label
//     (Field, or Target / Predictors for ATTR_REG_*; ATTR_FORMULA reads
//     its expression and may omit Field). A window-dependent attribute
//     (vectors.PostWindowAttributes: it reads a window label, directly
//     or through an earlier post-window attribute) is judged at 5b when
//     it is row-local and the windows run over the record rows, else is
//     PULSE_ATTRIBUTE_WINDOW_ORDER {attribute, field};
//  4. tier-1 tests, regressions, groups, aggregations and the crosstab
//     axes / cell / margin aggregations — the record columns: schema,
//     features and every attribute label. In a windows-first request
//     (vectors.WindowsFirst: an aggregation, grouper or crosstab axis
//     reads a window or post-window label) step 5's windows and 5b run
//     between the regressions and the groups, against the record
//     columns, so the groups, aggregations and crosstab also see every
//     window and post-window label; a window there reading an
//     aggregation output label is PULSE_WINDOW_INVALID
//     details.reason "windows_first_reads_aggregate";
//  5. windows (order_by, partition_by, and Field on a value-bearing
//     operator), sort and tier-2 post-tests — the OUTPUT row columns: the
//     group fields and aggregation labels (or, with neither, the record
//     columns), plus each earlier window's label (sort and post-tests
//     see every window). A window order_by key that is a schema field
//     is also judged for orderability (IsOrderableType — set_* is
//     refused). A crosstab's sort and windows judge against
//     the record columns; its post-tests run over cell rows and are not
//     judged here. The output namespace itself is judged too: an
//     aggregation label (explicit or "<TYPE>_<field>") equal to a group
//     field or to an earlier aggregation's label is refused, since the
//     row holds one value per name and the other figure was silently
//     lost (not on a crosstab, whose output is its cell grid).
//     5b. post-window attributes — judged as at step 3, in declared
//     order, against the record columns plus every window label and
//     each earlier post-window label (a regression at step 4 and a sort
//     key or post-test at step 5 may read their labels);
//  6. vectors — Request.Vectors resolved against the schema and the
//     request's computed columns (internal/vectors.ResolveRequest): the
//     first PULSE_VECTOR_* refusal, or the SERVICE_VALIDATION
//     unknown-field refusal for a literal member neither the schema nor
//     a feature / attribute / record-row window label names; then a vector name equal to a feature output,
//     attribute or window label of the request
//     (PULSE_VECTOR_NAME_COLLISION — an expression would read the
//     column, never the vector).
//  7. matrices — Request.Matrices resolved by the same leaf
//     (internal/vectors.ResolveMatrices): an unknown / hidden type, a
//     spec setting both or neither of vector and fields, an unknown
//     encoding, a duplicate result name or bad params
//     (SERVICE_VALIDATION, details.reason), an undefined vector
//     (PULSE_VECTOR_UNKNOWN), or an inline-fields vector refusal.
//  8. test vector inputs — every test's vector / fields and vector2 /
//     fields2 resolved by the same leaf (internal/vectors.ResolveTests):
//     the exactly-one shape, a vector input on a test that reads none,
//     an undefined vector (PULSE_VECTOR_UNKNOWN), the inline-fields
//     vector rules, a second set of another length, and params.missing
//     other than "listwise" (SERVICE_VALIDATION, details.reason).
//  9. attribute vector inputs — every attribute's vector / fields and
//     fitted params resolved by the same leaf
//     (internal/vectors.ResolveAttributes): the exactly-one shape, a
//     vector input on an attribute that reads none, an undefined vector,
//     the inline-fields vector rules, params.fit_on's shape and
//     params.missing other than "listwise". A vector attribute's
//     fit_on filterers are judged at step 3 by the filterer rule.
//  10. grouper vector inputs — every grouper slot's (groups and both
//     crosstab axes) vector / fields and params resolved by the same
//     leaf (internal/vectors.ResolveGroups): the exactly-one shape, a
//     vector input on a grouper that reads none, an undefined vector,
//     the inline-fields vector rules and GROUP_KMEANS's params. A vector
//     grouper's key column (vectors.GroupKeyField) is an output column
//     at step 5.
//     A registered extension test, attribute or grouper that sets a
//     vector input has its member sets judged at steps 8-10 by the same
//     member rules (shape, undefined vector, inline-fields rules); its
//     params and other slots stay its own.
//  11. aggregation vector inputs — every aggregation slot's
//     (aggregations, the crosstab cell and its margin aggregations)
//     vector / fields and params resolved by the same leaf
//     (internal/vectors.ResolveAggregations): the exactly-one shape, a
//     vector input on an aggregator that reads a field (an extension's
//     included), an undefined vector, the inline-fields vector rules and
//     params.labels / params.expand. A vector aggregator's array value
//     (its bare label) is refused at step 5 as a Sort key, a window's
//     field / order_by / partition_by or a post-test's field; it, or
//     under params.expand any of its per-element columns, is refused
//     as a request overlay's host (the aggregation it scores: a series
//     kind's params.field, else the first) —
//     PROCESSING_CONFIG, details.reason "array_valued". An expanded
//     per-element column <label>[<elem>] is otherwise an ordinary
//     nullable f64 output column.
//     Steps 6 to 11 contribute one refusal at most between them.
//  12. bound vector fits — every fitted attribute's and every matrix's
//     params.fit judged against the bound names (models: the runtime's
//     Set, predict's FitCatalog) by internal/model.CheckVectorFit, once
//     steps 6-11 passed: an unbound name is PULSE_MODEL_UNKNOWN (the
//     vector fits under details.available), a regression name
//     PULSE_MODEL_KIND_MISMATCH — located by details.attribute (the
//     slot's label) or details.matrix (the result name), the message
//     prefixed with the slot, exactly as the bind re-raises it. The
//     label alignment is the bind's alone (a static catalog never
//     resolves a battery).
//
// Derived names follow the runtime's own naming (featureOutputLabels,
// attributeDefaultLabel, "<TYPE>_<field>" for an aggregation,
// windowLabel), so a defaulted Type is visible in a label. A feature
// whose outputs the rule cannot name (an extension feature) leaves the
// column set open: every later record- and output-level name is
// accepted, as the runtime may legitimately produce it. Empty names are
// judged where the slot requires one (aggregation, group, attribute
// other than ATTR_FORMULA, every built-in filterer but
// FILTER_EXPRESSION) and skipped where it is optional.
//
// Names inside params are judged where an operator reads them: the two
// feature params above, the built-in aggregation params
// (aggParamFieldKeys — AGG_WEIGHTED_MEAN weight_field, AGG_RATIO
// numerator_field / denominator_field, AGG_DISTINCT_SUM distinct_by, on
// every aggregation slot, crosstab cell and margin aggregations
// included) and the post-test row-column params (postTestParamFieldKeys
// — TEST_ANOVA_F / TEST_ANOVA_WELCH n_col / variance_col,
// TEST_TUKEY_HSD n_column when given). An extension operator's names
// are the ones its registration's FieldInputs hook declares for the
// slot's Params (snap.DeclaredFieldInputs; a filterer's hook gets nil,
// as the projection extractor passes it), judged at the slot's own
// pipeline point; without a hook its params are not judged. A nil snap
// judges built-ins only. A nil request or schema yields nil — the
// schema-less mode of a validator that cannot read the cohort.
func FieldRefRefusals(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot) []*errors.CodedError {
	return fieldRefRefusals(req, schema, snap, nil)
}

// fieldRefRefusals is the walk behind every exported form. inst is the
// instance feature set: every type-keyed decision (which slots a type
// reads, its params, its default output label) reads the type's route
// (opRoute), so a hidden operator is walked exactly as a
// never-registered name. Nil hides nothing.
func fieldRefRefusals(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot) []*errors.CodedError {
	return fieldRefRefusalsWithModels(req, schema, snap, inst, nil)
}

// fieldRefRefusalsWithModels is fieldRefRefusals with the models an
// ATTR_REG_PREDICT slot resolves against (nil: none bound).
func fieldRefRefusalsWithModels(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot, models model.Catalog) []*errors.CodedError {
	return fieldRefRefusalsCoded(req, schema, nil, snap, inst, models)
}

// fieldRefRefusalsCoded is fieldRefRefusalsWithModels with the code
// columns a chain stage >= 1 inherits (nil: none) — read only by the
// overlay categorical-code rule.
func fieldRefRefusalsCoded(req *types.Request, schema *encoding.Schema, codes map[string]encoding.FieldType, snap *ExtensionsSnapshot, inst *InstanceSnapshot, models model.Catalog) []*errors.CodedError {
	if req == nil || schema == nil {
		return nil
	}
	w := &fieldRefWalk{cols: make(map[string]bool, len(schema.Fields)), snap: snap, inst: inst, req: req}
	for i := range schema.Fields {
		w.cols[schema.Fields[i].Name] = true
	}
	w.initCompile(req, schema)

	// -1. The request's params block (exprenv.NormalizeParams — the
	// values every expression compile below folds): a null value, a
	// non-identifier name, a mixed list or any other value is
	// PULSE_PARAMS_INVALID, judged first, once.
	if _, cerr := exprenv.NormalizeParams(req.Params); cerr != nil {
		w.out = append(w.out, cerr)
	}

	// 0. A native vector field named in a slot that reads one value
	// (vectorScalarRefusals) — PULSE_VECTOR_FIELD_NOT_SCALAR, judged
	// first so it wins over the slot's own type refusals.
	w.out = append(w.out, vectorScalarRefusals(req, schema, snap, inst)...)

	// 1. Features.
	for _, feat := range req.Features {
		if feat == nil {
			continue
		}
		mk := func(field string) func() *errors.CodedError {
			return func() *errors.CodedError {
				return refusal("feature references unknown field: "+field,
					map[string]any{"field": field, "feature": string(feat.Type)})
			}
		}
		featRoute := opRoute(inst, feat.Type)
		switch featRoute {
		case types.FEAT_TRAIN_TEST_SPLIT:
			if s := paramString(feat.Params, "stratify"); s != "" {
				w.check(s, func() *errors.CodedError {
					return refusal("feature FEAT_TRAIN_TEST_SPLIT: stratify references unknown field "+s,
						map[string]any{"field": s, "feature": string(feat.Type)})
				})
			}
		default:
			if feat.Field != "" {
				w.check(feat.Field, mk(feat.Field))
			}
			if featRoute == types.FEAT_TARGET_ENCODE {
				if t := paramString(feat.Params, "target"); t != "" {
					w.check(t, func() *errors.CodedError {
						return refusal("feature FEAT_TARGET_ENCODE: target references unknown field "+t,
							map[string]any{"field": t, "feature": string(feat.Type)})
					})
				}
				// params.fit_on compiles as an expression filter
				// against the schema plus EARLIER features' outputs
				// (this feature's labels join cols only below), so a
				// forward or self reference is the compile's
				// unknown-name refusal — on both sides, this walk being
				// the runtime's entry check too.
				if src := paramString(feat.Params, "fit_on"); strings.TrimSpace(src) != "" {
					before := len(w.out)
					w.checkTotal(src, string(types.FILTER_EXPRESSION))
					w.checkCoef(src, models)
					w.checkCompile(src, false, before)
				}
			}
		}
		w.checkInputs("feature", string(feat.Type), feat.Params, "feature")
		if !isKnownFeatureType(featRoute) {
			w.open = true
			continue
		}
		for _, label := range featureOutputLabels(feat, schema) {
			w.shadow(label, func() *errors.CodedError {
				return refusal("feature "+string(feat.Type)+" output "+label+" shadows an existing field",
					map[string]any{"label": label, "feature": string(feat.Type)})
			})
			w.cols[label] = true
			w.claim(label, map[string]any{"slot": "feature " + string(feat.Type), "feature": string(feat.Type)})
		}
	}

	// 2. Filterers. A before-filters attribute (prevstate.BeforeFilters:
	// ATTR_PREVIOUS_STATE) runs over the whole cohort ahead of the
	// filter pass, so an expression filter — and any later expression —
	// may read its output column (a derived float column, null on a
	// first record); a field-keyed filter still may not.
	for _, label := range prevstate.BeforeFiltersLabels(req.Attributes, func(t types.AttributeType) types.AttributeType { return opRoute(inst, t) }) {
		if w.preFilter == nil {
			w.preFilter = map[string]bool{}
		}
		w.preFilter[label] = true
	}
	for _, fil := range req.Filterers {
		if fil == nil {
			continue
		}
		w.checkInputs("filterer", string(fil.Type), nil, "filter")
		if opRoute(inst, fil.Type) == types.FILTER_EXPRESSION {
			before := len(w.out)
			w.checkTotal(fil.Expression, string(types.FILTER_EXPRESSION))
			w.checkCoef(fil.Expression, models)
			w.checkCompile(fil.Expression, false, before)
		}
		if fil.Field == "" && !filterFieldRequired(opRoute(inst, fil.Type)) {
			continue
		}
		w.check(fil.Field, func() *errors.CodedError {
			return refusal("filter references unknown field: "+fil.Field,
				map[string]any{"field": fil.Field, "filter": string(fil.Type)})
		})
	}

	// 3. Attributes, each seeing the earlier ones' labels. A
	// window-dependent attribute (vectors.PostWindowAttributes — the
	// classification the engine orders by) is not judged here: a
	// row-local one runs after the windows and is judged at step 5b,
	// beside the window labels; any other is
	// PULSE_ATTRIBUTE_WINDOW_ORDER, judged before the open column set
	// an extension feature leaves (no feature output carries a window's
	// value). Its label still joins the columns, so a later reader is
	// not refused twice.
	route := func(t types.AttributeType) types.AttributeType { return opRoute(inst, t) }
	pw := vectors.PostWindowAttributes(req, schema, route, models)
	w.windowsFirst, w.overRecords = pw.WindowsFirst, vectors.WindowsOverRecords(req, schema)
	for _, win := range req.Windows {
		if win == nil {
			continue
		}
		if w.winLabels == nil {
			w.winLabels = map[string]bool{}
		}
		w.winLabels[windowLabel(win)] = true
	}
	for _, l := range pw.Labels {
		if w.postLabels == nil {
			w.postLabels = map[string]bool{}
		}
		w.postLabels[l] = true
	}
	addAttrLabel := func(attr *types.Attribute) {
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr, inst)
		}
		w.shadow(label, func() *errors.CodedError {
			return refusal("attribute label "+label+" shadows an existing field",
				map[string]any{"label": label, "attribute": string(attr.Type)})
		})
		w.cols[label] = true
		w.claim(label, map[string]any{"slot": "attribute " + string(attr.Type), "attribute": string(attr.Type)})
	}
	judgeAttr := func(attr *types.Attribute) {
		attrRoute := opRoute(inst, attr.Type)
		if cerr := prevstate.ForeignSlotRefusal(attr, attrRoute); cerr != nil {
			w.out = append(w.out, cerr)
		}
		switch attrRoute {
		case types.ATTR_PREVIOUS_STATE:
			// Source columns only (the schema): the state's levels and
			// the subject / order values are cohort fields, never an
			// earlier attribute's or feature's output.
			for _, ref := range prevstate.FieldRefs(attr) {
				if schema == nil || schema.Field(ref.Name) != nil {
					continue
				}
				w.out = append(w.out, refusal(string(attr.Type)+" "+string(ref.Slot)+" references unknown cohort field: "+ref.Name+" (it reads source columns only)",
					map[string]any{"field": ref.Name, "attribute": string(attr.Type), "slot": string(ref.Slot)}))
			}
		case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
			if model.FitReferenced(attrRoute, attr) {
				// params.fit scores a bound fit (internal/model.
				// CheckFitReference — the rule the attribute's bind
				// re-runs), then every column the score reads: the
				// fit's terms and, for a residual, its target.
				p, sig, cerr := model.CheckFitReference(attr, models, scorableRegression(w.inst, w.snap))
				if cerr != nil {
					w.out = append(w.out, cerr)
					break
				}
				for _, col := range sig.Terms {
					w.check(col, func() *errors.CodedError { return model.PredictorMissingFor(attr.Type, p.Fit, col) })
					if f := schema.Field(col); f != nil && !regressionAcceptsType(sig.Type, f.Type) {
						w.out = append(w.out, model.PredictorNotNumericFor(attr.Type, p.Fit, col, f.Type.String()))
					}
				}
				if attrRoute == types.ATTR_REG_RESIDUAL && sig.Target != "" {
					w.check(sig.Target, func() *errors.CodedError { return model.TargetMissing(attr.Type, p.Fit, sig.Target) })
					if f := schema.Field(sig.Target); f != nil && !regressionAcceptsType(sig.Type, f.Type) {
						w.out = append(w.out, model.TargetNotNumeric(attr.Type, p.Fit, sig.Target, f.Type.String()))
					}
				}
				break
			}
			if attr.Target != "" {
				w.check(attr.Target, func() *errors.CodedError {
					return refusal(string(attr.Type)+" Target references unknown field: "+attr.Target,
						map[string]any{"field": attr.Target, "attribute": string(attr.Type)})
				})
			}
			for _, name := range attr.Predictors {
				w.check(name, func() *errors.CodedError {
					return refusal(string(attr.Type)+" predictor references unknown field: "+name,
						map[string]any{"field": name, "attribute": string(attr.Type)})
				})
			}
		case types.ATTR_REG_PREDICT:
			// The scoring rule (internal/model.CheckScore — the one the
			// attribute's bind re-runs), then every column the bound
			// model may read: present on the frame, numeric when it is a
			// schema field.
			p, sig, cerr := model.CheckScoreFor(attr, models, scorableRegression(w.inst, w.snap))
			if cerr != nil {
				w.out = append(w.out, cerr)
				break
			}
			for _, col := range sig.Terms {
				w.check(col, func() *errors.CodedError { return model.PredictorMissing(p.Fit, col) })
				if f := schema.Field(col); f != nil && !regressionAcceptsType(sig.Type, f.Type) {
					w.out = append(w.out, model.PredictorNotNumeric(p.Fit, col, f.Type.String()))
				}
			}
		default:
			if vectors.IsVectorAttribute(attrRoute) {
				// A vector-input attribute reads its members (step 9,
				// source columns only) and its params.fit_on filterers,
				// judged here against the attribute-step columns: the
				// fit predicate runs on the record beside the earlier
				// attributes' labels.
				for _, fil := range vectors.AttributeFitOn(attrRoute, attr) {
					w.checkFitOn(fil, attr, models)
				}
				break
			}
			if attrRoute == types.ATTR_FORMULA {
				before := len(w.out)
				w.checkTotal(attr.Expression, totalcall.FormulaSite)
				w.checkCoef(attr.Expression, models)
				w.checkCompile(attr.Expression, true, before)
			}
			// An attribute reading a vector input without a field (a
			// registered extension's; a built-in is refused at step 9)
			// names no field column to judge.
			if (attrRoute != types.ATTR_FORMULA || attr.Field != "") && (attr.Field != "" || !vectors.HasAttributeVectorInput(attr)) {
				w.check(attr.Field, func() *errors.CodedError {
					return refusal("attribute references unknown field: "+attr.Field,
						map[string]any{"field": attr.Field, "attribute": string(attr.Type)})
				})
			}
		}
		w.checkInputs("attribute", string(attr.Type), attr.Params, "attribute")
		addAttrLabel(attr)
	}
	for i, attr := range req.Attributes {
		if attr == nil || attr.Type == "ATTR_RANK" || pw.IsPost(i) {
			continue
		}
		if dep := pw.DepOf(i); dep != "" {
			w.out = append(w.out, vectors.AttributeWindowOrderRefusal(req, pw.WindowsFirst, attr, dep))
			addAttrLabel(attr)
			continue
		}
		judgeAttr(attr)
	}

	// 4. Record-level consumers. A regression may also read a window
	// label of this request, or a post-window attribute's label (judged
	// by checkRegression): windows run after the attributes, but over
	// the record rows (vectors.WindowsOverRecords) the engine copies each
	// window label a regression reads back onto its record, and runs the
	// post-window attributes over those records, before the fit. The
	// windows themselves are judged at step 5, against what precedes
	// them, and the post-window attributes right after them.
	w.checkWeights(req, schema)
	w.checkZoneFields(req, schema)
	for _, t := range req.Tests {
		if t != nil {
			w.checkTest(t, -1)
			w.checkInputs("test", string(t.Type), t.Params, "type")
		}
	}
	for _, reg := range req.Regressions {
		if reg == nil {
			continue
		}
		if reg.Target != "" {
			w.checkRegression(req, reg, reg.Target, func() *errors.CodedError {
				return refusal("regression references unknown target field: "+reg.Target,
					map[string]any{"field": reg.Target, "type": string(reg.Type)})
			})
		}
		for _, p := range reg.Predictors {
			w.checkRegression(req, reg, p, func() *errors.CodedError {
				return refusal("regression references unknown predictor field: "+p,
					map[string]any{"field": p, "type": string(reg.Type)})
			})
		}
		if c := reg.Covariance; c != nil {
			for _, f := range []string{c.Cluster, c.OrderBy} {
				if f == "" {
					continue
				}
				w.checkRegression(req, reg, f, func() *errors.CodedError {
					return refusal("regression covariance references unknown field: "+f,
						map[string]any{"field": f, "type": string(reg.Type)})
				})
			}
		}
		if f := reg.JackknifeBy; f != "" {
			w.checkRegression(req, reg, f, func() *errors.CodedError {
				return refusal("regression jackknife_by references unknown field: "+f,
					map[string]any{"field": f, "type": string(reg.Type)})
			})
		}
		w.checkInputs("regression", string(reg.Type), reg.Params, "type")
		// A model regression's declared reads beyond its target and
		// predictors (REG_MMM_BAYES's params.time), which the runtime
		// projection decodes from the same source.
		for _, f := range model.ModelRegressionFieldInputs(reg) {
			w.checkRegression(req, reg, f, func() *errors.CodedError {
				return refusal("regression references unknown field: "+f,
					map[string]any{"field": f, "type": string(reg.Type), "field_inputs": true})
			})
		}
		w.checkHoldout(req, reg, models)
	}
	// The windows (judged at step 5 on the historical order, against the
	// output-row columns) and the post-window attributes right after
	// them (step 5b). A windows-first request (pw.WindowsFirst,
	// vectors.WindowsFirst — an aggregation, grouper or crosstab axis
	// reads a window output) judges them here instead, between the
	// regressions and the groups: its windows run over the record rows
	// before the groups, aggregations and crosstab, which then read the
	// window and post-window labels as record columns. A window there
	// reading an aggregation's output label (not a record column) is
	// PULSE_WINDOW_INVALID details.reason "windows_first_reads_aggregate"
	// (aggregations run after the windows in that mode), judged before
	// an extension feature's open column set admits the name.
	var aggLabels map[string]bool
	if pw.WindowsFirst {
		aggLabels = windowsFirstAggregationLabels(req)
	}
	winCheck := func(i int, name string, mk func() *errors.CodedError) {
		if aggLabels[name] && !w.cols[name] {
			w.out = append(w.out, windowsFirstReadsAggregate(i, name))
			return
		}
		w.check(name, mk)
	}
	judgeWindowsAndPost := func() {
		for i, win := range req.Windows {
			if win == nil {
				continue
			}
			idx := strconv.Itoa(i)
			for _, ok := range win.OrderBy {
				if ok.Field == "" || w.arrayRef(ok.Field, "window["+idx+"] order_by", map[string]any{"window_index": i}) {
					continue
				}
				winCheck(i, ok.Field, func() *errors.CodedError {
					return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
						"window["+idx+"]: order_by field "+ok.Field+" does not exist in schema or upstream pipeline output",
						map[string]any{"window_index": i, "field": ok.Field})
				})
				// Orderability (IsOrderableType) is judged by the schema
				// type; a derived output column carries none.
				// A vector field is step 0's refusal (vectorScalarRefusals).
				if f := schema.Field(ok.Field); f != nil && !f.Type.IsVector() && !IsOrderableType(f.Type) {
					w.out = append(w.out, errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
						"window["+idx+"]: order_by field "+ok.Field+" is not orderable (type "+f.Type.String()+")",
						map[string]any{"window_index": i, "field": ok.Field, "field_type": f.Type.String()}))
				}
			}
			for _, p := range win.PartitionBy {
				if p == "" || w.arrayRef(p, "window["+idx+"] partition_by", map[string]any{"window_index": i}) {
					continue
				}
				winCheck(i, p, func() *errors.CodedError {
					return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
						"window["+idx+"]: partition_by field "+p+" does not exist in schema or upstream pipeline output",
						map[string]any{"window_index": i, "field": p})
				})
			}
			// Only a value-bearing operator reads Field (WIN_ROW_NUMBER /
			// RANK / DENSE_RANK name none, and an extension window's Field
			// is its own business).
			if win.Field != "" && windowparams.FieldRequired(opRoute(inst, win.Type)) &&
				!w.arrayRef(win.Field, "window["+idx+"] ("+string(win.Type)+") field", map[string]any{"window_index": i, "type": string(win.Type)}) {
				winCheck(i, win.Field, func() *errors.CodedError {
					return refusal("window["+idx+"] ("+string(win.Type)+"): field "+win.Field+" does not exist in schema or upstream pipeline output",
						map[string]any{"window_index": i, "field": win.Field, "type": string(win.Type)})
				})
			}
			// WIN_LIFT's params.base names a column like its field does.
			if opRoute(inst, win.Type) == types.WIN_LIFT {
				if base := paramString(win.Params, "base"); base != "" &&
					!w.arrayRef(base, "window["+idx+"] (WIN_LIFT) params.base", map[string]any{"window_index": i, "type": string(win.Type)}) {
					winCheck(i, base, func() *errors.CodedError {
						return refusal("window["+idx+"] ("+string(win.Type)+"): params.base "+base+" does not exist in schema or upstream pipeline output",
							map[string]any{"window_index": i, "field": base, "type": string(win.Type), "param": "base"})
					})
				}
			}
			w.checkInputs("window", string(win.Type), win.Params, "window")
			label := windowLabel(win)
			w.shadow(label, func() *errors.CodedError {
				return refusal("window["+idx+"] label "+label+" shadows an existing column",
					map[string]any{"window_index": i, "label": label, "type": string(win.Type)})
			})
			w.cols[label] = true
			w.claim(label, map[string]any{"slot": "window[" + idx + "] " + string(win.Type), "window_index": i, "window": string(win.Type)})
		}
		// 5b. Post-window attributes (vectors.PostWindowAttributes: a
		// row-local attribute reading a window label, directly or through an
		// earlier post-window attribute) — judged as at step 3, in declared
		// order, against the record columns plus every window label and each
		// earlier post-window label. Only the windows-over-records shape has
		// any, where w.cols is still the record column set.
		for i, attr := range req.Attributes {
			if pw.IsPost(i) {
				judgeAttr(attr)
			}
		}
	}

	if pw.WindowsFirst {
		judgeWindowsAndPost()
	}
	for _, grp := range req.Groups {
		if grp == nil {
			continue
		}
		// A vector-input grouper (GROUP_KMEANS) reads its members (step
		// 10, source columns only), never a field.
		if !vectors.IsVectorGroup(opRoute(inst, grp.Type)) {
			w.check(grp.Field, func() *errors.CodedError {
				return refusal("group references unknown field: "+grp.Field,
					map[string]any{"field": grp.Field, "group": string(grp.Type)})
			})
		}
		w.checkInputs("grouper", string(grp.Type), grp.Params, "group")
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		// A vector aggregator (AGG_VEC_*, or an extension declared
		// VectorInput) reads its members (step 11, source columns only),
		// never a field.
		if !aggregationKinds(inst, w.snap).IsVector(agg.Type) {
			w.check(agg.Field, func() *errors.CodedError {
				return refusal("aggregation references unknown field: "+agg.Field,
					map[string]any{"field": agg.Field, "aggregation": string(agg.Type)})
			})
		}
		w.checkAgg(agg, "aggregation "+string(agg.Type), "aggregation", string(agg.Type))
	}
	if spec := req.Crosstab; spec != nil {
		axis := func(field, role string) {
			if field == "" {
				return
			}
			w.check(field, func() *errors.CodedError {
				return refusal("crosstab "+role+" references unknown field: "+field,
					map[string]any{"field": field, "role": role})
			})
		}
		grouper := func(g *types.Group, role string) {
			if !vectors.IsVectorGroup(opRoute(inst, g.Type)) {
				axis(g.Field, role)
			}
			w.checkInputs("grouper", string(g.Type), g.Params, "role", role)
		}
		agg := func(a *types.Aggregation, role string) {
			axis(a.Field, role)
			w.checkAgg(a, "crosstab "+role+" "+string(a.Type), "role", role)
		}
		for _, g := range spec.Rows {
			if g != nil {
				grouper(g, "row grouper")
			}
		}
		for _, g := range spec.Columns {
			if g != nil {
				grouper(g, "column grouper")
			}
		}
		for _, cell := range spec.CellAggregations() {
			if cell != nil {
				agg(cell, "cell aggregation")
			}
		}
		for _, a := range spec.MarginAggregations {
			if a != nil {
				agg(a, "margin aggregation")
			}
		}
	}

	// 5. Output-row consumers.
	if req.Crosstab == nil && (len(req.Groups) > 0 || len(req.Aggregations) > 0) {
		out := make(map[string]bool, len(req.Groups)+len(req.Aggregations)+len(req.Windows))
		// owner names the slot that claimed each output column, so a
		// collision's details can name both sides.
		owner := make(map[string]map[string]any, len(req.Groups)+len(req.Aggregations))
		for gi, grp := range req.Groups {
			if grp == nil {
				continue
			}
			key := vectors.GroupKeyField(opRoute(inst, grp.Type), grp)
			// Two groupers writing one key column: each composite row
			// carries one value per column, so the second grouper's
			// key would overwrite the first's and the row would no
			// longer name its combination.
			if prior, ok := owner[key]; ok {
				pi := prior["group_index"].(int)
				w.out = append(w.out, refusal("group["+strconv.Itoa(gi)+"] field "+key+" collides with group["+strconv.Itoa(pi)+"] field "+key,
					map[string]any{"field": key, "group_index": gi, "group": string(grp.Type),
						"other_group_index": pi, "other_group": prior["group"]}))
				continue
			}
			out[key] = true
			owner[key] = map[string]any{"group_index": gi, "group": string(grp.Type)}
		}
		for ai, agg := range req.Aggregations {
			if agg == nil {
				continue
			}
			// The columns the slot writes: its label, or an expanding
			// vector aggregation's per-element columns
			// (vectors.AggregationOutputColumns — the names the
			// runtime's row writer uses). A scalar slot takes the
			// allocation-free path (the observability-off gate).
			label := types.AggregationLabelOf(agg)
			cols := [1]string{label}
			outCols := cols[:]
			if kinds := aggregationKinds(inst, w.snap); kinds.IsArrayValued(agg.Type) {
				if vectors.AggregationExpands(agg, kinds) {
					outCols, _ = vectors.AggregationOutputColumns(req, agg, schema, kinds)
				}
				// An array-valued slot's bare label names no single
				// number (judged below). Its expanded per-element
				// columns are ordinary nullable f64 output columns:
				// they join the output set like any scalar label.
				if w.arrayCols == nil {
					w.arrayCols = map[string]*types.Aggregation{}
				}
				w.arrayCols[label] = agg
			}
			for _, label := range outCols {
				// Output-namespace collision: the row map holds one value
				// per name, so an aggregation label equal to a group field
				// was overwritten by the group key (the aggregation
				// silently dropped) and a duplicate label kept only the
				// last aggregation's figure.
				if prior, ok := owner[label]; ok && label != "" {
					details := map[string]any{"label": label, "aggregation_index": ai, "aggregation": string(agg.Type)}
					var with string
					if gi, isGroup := prior["group_index"]; isGroup {
						with = "group[" + strconv.Itoa(gi.(int)) + "] field"
					} else {
						with = "aggregation[" + strconv.Itoa(prior["other_aggregation_index"].(int)) + "] label"
					}
					for k, v := range prior {
						details[k] = v
					}
					w.out = append(w.out, refusal("aggregation["+strconv.Itoa(ai)+"] label "+label+" collides with "+with+" "+label,
						details))
					break
				}
				out[label] = true
				owner[label] = map[string]any{"other_aggregation_index": ai, "other_aggregation": string(agg.Type)}
			}
		}
		w.cols = out
	}
	if !pw.WindowsFirst {
		judgeWindowsAndPost()
	}
	// An array-valued aggregation's bare label (AGG_VEC_*: w.arrayCols)
	// names no single number to order by; an expanded per-element
	// column <label>[<elem>] is an ordinary output column.
	for i, k := range req.Sort {
		if k.Field == "" {
			continue
		}
		idx := strconv.Itoa(i)
		if w.arrayRef(k.Field, "sort["+idx+"]", map[string]any{"sort_index": i}) {
			continue
		}
		w.check(k.Field, func() *errors.CodedError {
			return refusal("sort["+idx+"]: field "+k.Field+" is not produced by the pipeline (no schema field, aggregation, attribute, group, or window output matches)",
				map[string]any{"sort_index": i, "field": k.Field})
		})
	}
	// A request overlay reads ONE aggregation as its host value: the one
	// a series kind's params.field names (an output label, or an
	// expanded element column of an array-valued one), else the first.
	ovKinds := aggregationKinds(inst, w.snap)
	w.out = append(w.out, processOverlayArrayValuedRefusals(req, ovKinds, vectors.CodeFieldTypes(schema, codes),
		vectors.AggregationColumns(req, func() *encoding.Schema { return schema }, ovKinds),
		func(k types.OverlayKind) bool { return !inst.Hidden(string(k)) && overlayhost.ReadsCellValue(k) })...)
	if req.Crosstab == nil {
		for i, t := range req.PostTests {
			if t != nil {
				w.checkTest(t, i)
			}
		}
	}

	// 6. Vectors — resolved against the SCHEMA the request executes
	// over by the one resolver internal/vectors, which projection and
	// predict's resolved_vectors echo also call. A literal member may
	// name a column the request computes (vectors.ComputedColumns: a
	// feature output, attribute or record-row window label), which
	// only a MAT_* slot reads (vectors.ComputedRefusal). Resolution stops at its first
	// failure, so it contributes one refusal at most.
	//
	// 7. Matrices — each Request.Matrices spec resolved by the same
	// leaf (vectors.ResolveMatrices, which runs step 6 first): type,
	// vector-XOR-fields shape, encoding, result-name uniqueness, params,
	// the named vector (PULSE_VECTOR_UNKNOWN) or the inline fields (the
	// vector rules). A type the instance hides is an unknown type.
	mats, verr := resolveMatricesFor(req, schema, w.snap, inst)
	if verr != nil {
		w.out = append(w.out, verr)
		return w.out
	}
	// 6b. A vector name equal to a feature, attribute or window output
	// label (PULSE_VECTOR_NAME_COLLISION) — the schema-field collision's
	// twin for the derived columns, judged once every vector resolved.
	if cerr := w.vectorLabelCollision(req); cerr != nil {
		w.out = append(w.out, cerr)
		return w.out
	}
	// 8. Test vector inputs — each test's vector / fields (and vector2
	// / fields2) resolved by the same leaf (vectors.ResolveTests): the
	// shape rules, the named vector (PULSE_VECTOR_UNKNOWN), the inline
	// fields (the vector rules), the equal-length pairing and the
	// listwise-only missing mode. Steps 6-8 contribute one refusal at
	// most between them.
	if _, terr := vectors.ResolveTests(req, schema, func(t types.TestType) types.TestType {
		return opRoute(inst, t)
	}, w.extensionNamed(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Tests })); terr != nil {
		w.out = append(w.out, terr)
		return w.out
	}
	// 9. Attribute vector inputs — each attribute's vector / fields and
	// fitted params (fit_on, missing, the operator's own keys) resolved
	// by the same leaf (vectors.ResolveAttributes). Steps 6-9 contribute
	// one refusal at most between them.
	ains, aerr := vectors.ResolveAttributes(req, schema, func(t types.AttributeType) types.AttributeType {
		return opRoute(inst, t)
	}, w.extensionNamed(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Attributes }), inst.Hidden)
	if aerr != nil {
		w.out = append(w.out, aerr)
		return w.out
	}
	// 10. Grouper vector inputs — every grouper slot's vector / fields
	// and params resolved by the same leaf (vectors.ResolveGroups).
	// Steps 6-10 contribute one refusal at most between them.
	if _, gerr := vectors.ResolveGroups(req, schema, func(t types.GroupType) types.GroupType {
		return opRoute(inst, t)
	}, w.extensionNamed(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Groupers })); gerr != nil {
		w.out = append(w.out, gerr)
		return w.out
	}
	// 11. Aggregation vector inputs — every aggregation slot's (the
	// aggregations, the crosstab cell and its margin aggregations)
	// vector / fields and params resolved by the same leaf
	// (vectors.ResolveAggregations). Steps 6-11 contribute one refusal
	// at most between them.
	if _, aerr := vectors.ResolveAggregations(req, schema, aggregationKinds(inst, w.snap)); aerr != nil {
		w.out = append(w.out, aerr)
		return w.out
	}
	// 12. Bound vector fits (params.fit), once every input resolved.
	w.out = append(w.out, vectorFitRefusals(req, schema, mats, ains, inst, models)...)
	return w.out
}

// vectorFitRefusals is step 12: each fitted attribute's params.fit and
// each comparison slot's {population} reference (in slot order), then
// each matrix's params.fit, judged by model.CheckVectorFit
// against models and located the way the runtime's bind locates it
// (details.attribute = the slot's label / details.matrix = the result
// name, the message prefixed with the slot). A bound fit whose member
// labels the catalog knows (a loaded population; every runtime fit) is
// then aligned to the slot's labels by the bind's own rule
// (vectors.AlignFit — PULSE_VECTOR_REFERENCE_MISMATCH labels_differ);
// predict's static Compose-fit signatures carry no labels and skip it.
// ains is step 9's index-aligned attribute resolution.
func vectorFitRefusals(req *types.Request, schema *encoding.Schema, mats []vectors.Matrix, ains []vectors.AttributeInput, inst *InstanceSnapshot, models model.Catalog) []*errors.CodedError {
	var out []*errors.CodedError
	for i, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		typ := opRoute(inst, attr.Type)
		name := vectors.AttributeFit(typ, attr)
		pop := vectors.ComparisonPopulation(typ, attr)
		if name == "" && pop == "" {
			continue
		}
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr, inst)
		}
		var labels []string
		labelsOK := false
		if i < len(ains) {
			labels, labelsOK = vectors.AttributeFitLabels(typ, attr, ains[i].Members, schema)
		}
		if name != "" {
			check := model.CheckVectorFit
			if typ == types.ATTR_SEM_SCORE {
				// ATTR_SEM_SCORE reads a SEM fit (kind sem).
				check = model.CheckSEMFit
			}
			if cerr := check(models, name); cerr != nil {
				cerr.Details["attribute"] = label
				cerr.Message = label + ": " + cerr.Message
				out = append(out, cerr)
				continue
			}
			sig, _ := models.Signature(name)
			if sig.Members != nil && labelsOK {
				if _, cerr := vectors.AlignFit("attribute", label, name, sig.Members, labels); cerr != nil {
					out = append(out, cerr)
					continue
				}
			}
			if typ == types.ATTR_SEM_SCORE && sig.Ordinal {
				// An ordinal SEM fit scores by EBM: Bartlett is refused
				// at bind (vectors.SEMScoreOrdinalMethodRefusal).
				if cerr := vectors.SEMScoreOrdinalMethodRefusal(label, name, vectors.SEMScoreMethodOf(attr), ""); cerr != nil {
					out = append(out, cerr)
					continue
				}
			}
		}
		// A {population} reference (after the slot's own fit, as the
		// runtime binds them): unbound is PULSE_VECTOR_REFERENCE_UNKNOWN,
		// a population whose labels the catalog knows aligns by label.
		if pop == "" {
			continue
		}
		if cerr := model.CheckVectorFit(models, pop); cerr != nil {
			out = append(out, vectors.PopulationRefusal(label, pop, cerr))
			continue
		}
		if sig, _ := models.Signature(pop); sig.Members != nil && labelsOK {
			if _, cerr := vectors.PopulationAlign(label, pop, sig.Members, labels); cerr != nil {
				out = append(out, cerr)
			}
		}
	}
	for _, m := range mats {
		if m.Fit == "" {
			continue
		}
		if cerr := model.CheckVectorFit(models, m.Fit); cerr != nil {
			cerr.Details["matrix"] = m.Name
			cerr.Message = "matrix " + m.Name + ": " + cerr.Message
			out = append(out, cerr)
			continue
		}
		if sig, _ := models.Signature(m.Fit); sig.Members != nil {
			if _, cerr := vectors.AlignFit("matrix", m.Name, m.Fit, sig.Members, m.FitLabels()); cerr != nil {
				out = append(out, cerr)
			}
		}
	}
	return out
}

// resolveMatricesFor is the one instance-scoped Request.Matrices
// resolution every descriptor site reads (the field-reference walk,
// predict, the limits): a hidden matrix type is unknown, an offered
// extension MAT_* operator resolves through its registration, and a
// MAT_DISTANCE params.between grouper type routes through the instance
// (a hidden grouper is unknown); a registered extension grouper (snap,
// else the instance's own projection) is accepted as a between, as the
// runtime accepts it.
func resolveMatricesFor(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot) ([]vectors.Matrix, *errors.CodedError) {
	if snap == nil {
		snap = inst.Extensions()
	}
	var ext func(string) bool
	if snap != nil {
		ext = func(name string) bool { return snapshotHasName(snap.Groupers, name) }
	}
	return vectors.ResolveMatricesWith(req, schema, vectors.MatrixResolve{
		Known: func(t types.MatrixType) bool {
			return isBuiltinMatrixType(opRoute(inst, t))
		},
		Route: func(t types.GroupType) types.GroupType {
			return opRoute(inst, t)
		},
		BetweenExt: ext,
		// An offered extension MAT_* operator resolves through its
		// registration, as the runtime resolves it
		// (processing.resolveMatrixPlans).
		Extension: matrixExtensionFor(inst, snap),
		// A MAT_SEM params surface the instance hides resolves as never
		// existing, as the runtime resolves it.
		Hidden: inst.Hidden,
	})
}

// checkFitOn judges one params.fit_on filterer of a fitted vector
// attribute by the Request filterer rule (step 2) against the columns
// visible to the attribute.
func (w *fieldRefWalk) checkFitOn(fil *types.Filterer, attr *types.Attribute, models model.Catalog) {
	w.checkInputs("filterer", string(fil.Type), nil, "filter")
	if opRoute(w.inst, fil.Type) == types.FILTER_EXPRESSION {
		before := len(w.out)
		w.checkTotal(fil.Expression, string(types.FILTER_EXPRESSION))
		w.checkCoef(fil.Expression, models)
		w.checkCompile(fil.Expression, false, before)
	}
	if fil.Field == "" && !filterFieldRequired(opRoute(w.inst, fil.Type)) {
		return
	}
	w.check(fil.Field, func() *errors.CodedError {
		return refusal(string(attr.Type)+" fit_on filter references unknown field: "+fil.Field,
			map[string]any{"field": fil.Field, "filter": string(fil.Type), "attribute": string(attr.Type)})
	})
}

// fieldRefWalk carries the column set available at the current
// pipeline point and the refusals found so far.
type fieldRefWalk struct {
	cols map[string]bool
	open bool
	snap *ExtensionsSnapshot
	// inst routes every type-keyed decision (opRoute); nil hides
	// nothing.
	inst *InstanceSnapshot
	out  []*errors.CodedError
	// labels records each derived column a feature, attribute or window
	// names (its first claimer's slot, for the details of a vector-name
	// collision at step 6).
	labels map[string]map[string]any
	// arrayCols maps the bare label of every array-valued aggregation
	// (AGG_VEC_*) to its slot: a Sort key, a window or a post-test may
	// not read it (arrayRef). Under params.expand the per-element
	// columns are not here — they are ordinary output columns.
	arrayCols map[string]*types.Aggregation
	// compile is the expression compile's state (predict_formula.go,
	// initCompile).
	compile exprCompileScope
	// preFilter holds the output column of every before-filters
	// attribute (prevstate.BeforeFilters): an expression compiled from
	// the filter step on reads it as a derived column, since the runtime
	// writes it before any filter runs.
	preFilter map[string]bool
	// winLabels holds every window label of the request (windowLabel),
	// seeded before step 4: the names a regression may read once the
	// windows run (checkRegression), and a weight may never read.
	winLabels map[string]bool
	// postLabels holds the labels of the request's post-window
	// attributes (vectors.PostWindowAttributes), seeded before step 3:
	// names a regression may read (checkRegression), judged at step 5b.
	postLabels map[string]bool
	// req is the request walked (the shape a window-order refusal
	// names).
	req *types.Request
	// windowsFirst is the request's post-window plan's WindowsFirst
	// (vectors.WindowsFirst over the walk's route and models), seeded at
	// step 3: the windows run over the record rows before the groups,
	// aggregations and crosstab.
	windowsFirst bool
	// overRecords is vectors.WindowsOverRecords — the catalog-free view
	// the engine's regression window copy reads too
	// (vectors.RegressionWindowReads) — seeded at step 3: a regression
	// or holdout may read a window label only then.
	overRecords bool
}

// windowsFirstAggregationLabels is the set of output labels a
// windows-first request's aggregations write (types.AggregationLabelOf:
// req.Aggregations and the crosstab's cell and margin aggregations) —
// the names a window may not read in that mode, the aggregations
// running after the windows.
func windowsFirstAggregationLabels(req *types.Request) map[string]bool {
	out := map[string]bool{}
	add := func(a *types.Aggregation) {
		if a != nil {
			if l := types.AggregationLabelOf(a); l != "" {
				out[l] = true
			}
		}
	}
	for _, a := range req.Aggregations {
		add(a)
	}
	if spec := req.Crosstab; spec != nil {
		for _, a := range spec.CellAggregations() {
			add(a)
		}
		for _, a := range spec.MarginAggregations {
			add(a)
		}
	}
	return out
}

// windowsFirstReadsAggregate is PULSE_WINDOW_INVALID for window i of a
// windows-first request reading the aggregation output label field:
// details {window_index, field, reason: "windows_first_reads_aggregate"}.
func windowsFirstReadsAggregate(i int, field string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
		"window["+strconv.Itoa(i)+"] reads aggregation output "+field+", but an aggregation, grouper or crosstab axis of this request reads a window output, so the windows run first over the records, before any aggregation; drop the window-output read from the aggregations, or materialize the windows first",
		map[string]any{"window_index": i, "field": field, "reason": "windows_first_reads_aggregate"})
}

// arrayRef refuses a downstream read (a Sort key, a window's field /
// order_by / partition_by, a post-test's field) of an array-valued
// aggregation's bare label — one figure per vector member, no single
// number — with PROCESSING_CONFIG {field, aggregation, reason:
// "array_valued"} plus extra. An expanded per-element column is never
// here: it is an ordinary output column the existence check judges.
// It reports whether it refused, so the caller skips its own existence
// check.
func (w *fieldRefWalk) arrayRef(name, at string, extra map[string]any) bool {
	agg, ok := w.arrayCols[name]
	if !ok {
		return false
	}
	details := map[string]any{"field": name, "aggregation": string(agg.Type), "reason": "array_valued"}
	for k, v := range extra {
		details[k] = v
	}
	w.out = append(w.out, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		at+": "+name+" is array-valued ("+string(agg.Type)+", one figure per vector member); it cannot be ordered by, windowed or tested — set params.expand and read an element column "+name+"[<element>]", details))
	return true
}

// claim records label as a derived column of the slot described by who
// (first claimer wins).
func (w *fieldRefWalk) claim(label string, who map[string]any) {
	if label == "" {
		return
	}
	if w.labels == nil {
		w.labels = map[string]map[string]any{}
	}
	if _, ok := w.labels[label]; !ok {
		w.labels[label] = who
	}
}

// vectorLabelCollision is PULSE_VECTOR_NAME_COLLISION for the first
// Request.Vectors name equal to a feature, attribute or window output
// label of the same request: an expression binds a named vector beside
// those columns, and the column would silently win.
func (w *fieldRefWalk) vectorLabelCollision(req *types.Request) *errors.CodedError {
	for i, v := range req.Vectors {
		who, ok := w.labels[v.Name]
		if !ok {
			continue
		}
		at := "vectors[" + strconv.Itoa(i) + "]"
		details := map[string]any{"vector": at, "name": v.Name, "label": v.Name}
		for k, val := range who {
			details[k] = val
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_NAME_COLLISION,
			"vector name "+strconv.Quote(v.Name)+" ("+at+") is also the output label of "+who["slot"].(string)+"; an expression could not tell the two apart",
			details)
	}
	return nil
}

// extensionNamed is the vector resolvers' extension predicate for one
// category: a routed type name the snapshot registers in that category
// (metas). Nil without a snapshot — no extension is registered.
func (w *fieldRefWalk) extensionNamed(metas func(*ExtensionsSnapshot) []descriptor.OperatorMeta) func(string) bool {
	if w.snap == nil {
		return nil
	}
	return func(name string) bool { return snapshotHasName(metas(w.snap), name) }
}

// aggParamFieldKeys lists, per built-in aggregation, the Params keys
// that name a record column (the set the projection extractor's
// addAggParamFields decodes). Unjudged, an unknown name there read as
// an all-null column and the operator answered a confident empty or
// zero result.
// The table lives in internal/vectors (AggregationParamFieldKeys),
// where the windows-first classification reads the same keys.
var aggParamFieldKeys = vectors.AggregationParamFieldKeys

// postTestParamFieldKeys lists, per built-in tier-2 test, the Params
// keys that name an OUTPUT row column. TEST_TUKEY_HSD's n_column
// defaults to "n" when omitted; only an explicit name is judged.
var postTestParamFieldKeys = map[types.TestType][]string{
	types.TEST_ANOVA_F:     {"n_col", "variance_col"},
	types.TEST_ANOVA_WELCH: {"n_col", "variance_col"},
	types.TEST_TUKEY_HSD:   {"n_column"},
}

// checkAgg judges the column names an aggregation reads beyond Field:
// a built-in's params (aggParamFieldKeys) and an extension's
// FieldInputs. prefix names the slot in the message ("aggregation
// AGG_RATIO", "crosstab cell aggregation AGG_RATIO"); slotKey /
// slotValue is the details entry naming the slot.
func (w *fieldRefWalk) checkAgg(agg *types.Aggregation, prefix, slotKey string, slotValue any) {
	for _, key := range aggParamFieldKeys[opRoute(w.inst, agg.Type)] {
		name := paramString(agg.Params, key)
		if name == "" {
			continue // missing: the operator's own PROCESSING_CONFIG refusal
		}
		w.check(name, func() *errors.CodedError {
			return refusal(prefix+": params."+key+" references unknown field "+name,
				map[string]any{"field": name, "param": key, slotKey: slotValue})
		})
	}
	w.checkInputs("aggregator", string(agg.Type), agg.Params, slotKey, slotValue)
}

// checkInputs judges the names an extension operator's FieldInputs hook
// declares for raw, against the columns available at this pipeline
// point. slotKey is the details key naming the slot ("aggregation",
// "filter", "group", ...); its value is the operator type unless an
// explicit slotValue is given (a crosstab role). A built-in operator, or
// an extension without a hook, declares nothing.
func (w *fieldRefWalk) checkInputs(category, name string, raw json.RawMessage, slotKey string, slotValue ...any) {
	inputs, ok := w.snap.DeclaredFieldInputs(category, name, raw)
	if !ok {
		return
	}
	var slot any = name
	if len(slotValue) > 0 {
		slot = slotValue[0]
	}
	for _, in := range inputs {
		if in == "" {
			continue
		}
		if category == "attribute" && (w.winLabels[in] || w.postLabels[in]) && !w.cols[in] {
			// An extension attribute is never row-local here: it cannot
			// run after the windows (PULSE_ATTRIBUTE_WINDOW_ORDER).
			w.out = append(w.out, vectors.AttributeWindowOrderRefusal(w.req, w.windowsFirst, &types.Attribute{Type: types.AttributeType(name)}, in))
			continue
		}
		w.check(in, func() *errors.CodedError {
			return refusal(category+" "+name+": FieldInputs references unknown field "+in,
				map[string]any{"field": in, slotKey: slot, "field_inputs": true})
		})
	}
}

// check refuses name unless it is an available column. An empty name
// reaches check only from a slot that requires one, and is refused even
// once an extension feature opened the set — no operator produces a
// column named "".
func (w *fieldRefWalk) check(name string, mk func() *errors.CodedError) {
	if name != "" && (w.open || w.cols[name]) {
		return
	}
	w.out = append(w.out, mk())
}

// checkRegression is check for a name a regression slot reads per
// record (target, predictors, covariance cluster / order_by,
// jackknife_by), which may also be a window label of the request. Over
// the record rows (vectors.WindowsOverRecords) the engine copies the
// label onto each record before the fit, so it is a column; elsewhere
// the windows run over grouped / aggregated output rows and no record
// carries it — PULSE_REGRESSION_WINDOW_UNAVAILABLE, judged before the
// open column set an extension feature leaves (its outputs never carry
// a window's value either). A column of that name already present wins
// (the window's own shadow refusal follows at step 5).
func (w *fieldRefWalk) checkRegression(req *types.Request, reg *types.RegressionSpec, name string, mk func() *errors.CodedError) {
	if name == "" || !(w.winLabels[name] || w.postLabels[name]) || w.cols[name] {
		w.check(name, mk)
		return
	}
	if w.overRecords {
		return
	}
	w.out = append(w.out, errors.NewCodedErrorWithDetails(errors.PULSE_REGRESSION_WINDOW_UNAVAILABLE,
		"regression "+string(reg.Type)+" reads window output "+name+", which only the grouped or aggregated output rows carry, not the records the fit reads",
		map[string]any{"regression": string(reg.Type), "field": name, "reason": "grouped_shape"}))
}

// checkHoldout judges a regression's holdout predicate (step 4): the
// fit's type must be scorable (model.HoldoutScorerRefusal — the
// held-out rows are scored by the fit's scorer); a window or post-window
// attribute label it reads is the record column checkRegression admits
// only where the windows run over the record rows, else
// PULSE_REGRESSION_WINDOW_UNAVAILABLE (details.slot "holdout"); then the
// total() and coef() rules and the boolean compile of a filter
// expression (exprenv.SiteHoldout), against the record columns plus
// every window and post-window label — the engine compiles it on the
// first fully built record. An unknown name fails that compile
// (PULSE_FORMULA_COMPILE_ERROR), as in a FILTER_EXPRESSION.
func (w *fieldRefWalk) checkHoldout(req *types.Request, reg *types.RegressionSpec, models model.Catalog) {
	src := reg.Holdout
	if src == "" {
		return
	}
	before := len(w.out)
	_, builtin := knownRegressionTypes[opRoute(w.inst, reg.Type)]
	_, _, extension := regressionExtensionFor(w.inst, w.snap, reg.Type)
	if builtin || extension {
		if cerr := model.HoldoutScorerRefusal(reg, scorableRegression(w.inst, w.snap)); cerr != nil {
			w.out = append(w.out, cerr)
			return
		}
	}
	refs, _, _ := exprident.Free(src)
	extra := map[string]bool{}
	for _, name := range refs {
		if !(w.winLabels[name] || w.postLabels[name]) || w.cols[name] {
			continue
		}
		if !w.overRecords {
			w.out = append(w.out, errors.NewCodedErrorWithDetails(errors.PULSE_REGRESSION_WINDOW_UNAVAILABLE,
				"regression "+string(reg.Type)+" holdout reads window output "+name+", which only the grouped or aggregated output rows carry, not the records the fit reads",
				map[string]any{"regression": string(reg.Type), "field": name, "reason": "grouped_shape", "slot": model.HoldoutKey}))
			return
		}
		extra[name] = true
	}
	w.checkTotal(src, string(exprenv.SiteHoldout))
	w.checkCoef(src, models)
	if len(w.out) > before {
		return
	}
	w.compile.extra = extra
	if ce := w.compileRefusal(src, exprenv.SiteHoldout, w.compile.bound.Options()); ce != nil {
		w.out = append(w.out, ce)
	}
	w.compile.extra = nil
}

// shadow refuses a derived column (attribute label, feature output,
// window label) whose name is already an available column — a schema
// field, an earlier derived column or, for a window, an output-row
// column. Writing it would overwrite that column in place, and the
// buffered and streaming attribute arms disagreed about the overwritten
// field's null mark; no documented request relies on the overwrite, so
// both sides refuse it. A collision with a name only an extension
// feature might produce (open set) is not knowable and not judged.
func (w *fieldRefWalk) shadow(label string, mk func() *errors.CodedError) {
	if label != "" && w.cols[label] {
		w.out = append(w.out, mk())
	}
}

// checkTest judges the fields a test reads: tier-1 (postIndex < 0)
// against the record columns, a tier-2 post-test (postIndex = its
// slot) against the output row columns, the latter's details carrying
// post_test_index. The slots judged per type are the ones the test
// reads — rows/cols for TEST_CHISQ / TEST_FISHER_EXACT, field,
// split_by and params.covariate for TEST_PROP_Z, otherwise field, field2 (paired /
// bivariate tests), split_by, subject_field (TEST_ANOVA_RM) and, on a
// post-test, its order_by keys.
func (w *fieldRefWalk) checkTest(t *types.Test, postIndex int) {
	add := func(name, msg string, details map[string]any) {
		if name == "" {
			return
		}
		if postIndex >= 0 && w.arrayRef(name, "post_tests["+strconv.Itoa(postIndex)+"] ("+string(t.Type)+")",
			map[string]any{"post_test_index": postIndex, "type": string(t.Type)}) {
			return
		}
		w.check(name, func() *errors.CodedError {
			if postIndex >= 0 {
				details["post_test_index"] = postIndex
			}
			return refusal(msg, details)
		})
	}
	typ := string(t.Type)
	route := opRoute(w.inst, t.Type)
	switch route {
	case types.TEST_CHISQ, types.TEST_FISHER_EXACT:
		add(t.Rows, "TEST_CHISQ rows references unknown field: "+t.Rows, map[string]any{"axis": "rows", "field": t.Rows})
		add(t.Cols, "TEST_CHISQ cols references unknown field: "+t.Cols, map[string]any{"axis": "cols", "field": t.Cols})
	case types.TEST_PROP_Z:
		add(t.Field, "TEST_PROP_Z field references unknown field: "+t.Field, map[string]any{"axis": "field", "field": t.Field})
		add(t.SplitBy, "TEST_PROP_Z split_by references unknown field: "+t.SplitBy, map[string]any{"axis": "split_by", "field": t.SplitBy})
		if postIndex < 0 {
			cov := fieldaccept.CovariateParam(t.Params)
			add(cov, typ+": params.covariate references unknown field "+cov, map[string]any{"type": typ, "field": cov, "param": fieldaccept.CovariateKey})
		}
	default:
		add(t.Field, typ+" references unknown field: "+t.Field, map[string]any{"type": typ, "field": t.Field})
		if numericField2Tests[route] {
			add(t.Field2, typ+" references unknown field2: "+t.Field2, map[string]any{"type": typ, "field2": t.Field2})
		}
		add(t.SplitBy, typ+" split_by references unknown field: "+t.SplitBy, map[string]any{"type": typ, "split_by": t.SplitBy})
		if postIndex < 0 && fieldaccept.TakesCovariate(route) {
			cov := fieldaccept.CovariateParam(t.Params)
			add(cov, typ+": params.covariate references unknown field "+cov, map[string]any{"type": typ, "field": cov, "param": fieldaccept.CovariateKey})
		}
		if route == types.TEST_ANOVA_RM {
			add(t.SubjectField, "TEST_ANOVA_RM subject_field references unknown field: "+t.SubjectField,
				map[string]any{"type": typ, "subject_field": t.SubjectField})
		}
	}
	if postIndex >= 0 {
		for _, ok := range t.OrderBy {
			add(ok.Field, typ+" order_by references unknown field: "+ok.Field, map[string]any{"type": typ, "field": ok.Field})
		}
		for _, key := range postTestParamFieldKeys[route] {
			name := paramString(t.Params, key)
			add(name, typ+": params."+key+" references unknown field "+name, map[string]any{"type": typ, "field": name, "param": key})
		}
	}
}

// checkWeights judges every explicitly named weight field — the
// request's `weight` and each slot's own — against the cohort SCHEMA,
// not the derived column set: a weight is read straight off the record
// as a stored column, so an attribute label or feature output is no
// weight. An inherited Options.DefaultWeight is not a request reference
// and is judged by ResolveWeights where it applies.
func (w *fieldRefWalk) checkWeights(req *types.Request, schema *encoding.Schema) {
	judge := func(slot string, spec *types.WeightSpec) {
		if spec == nil || spec.Field == "" || schema.Field(spec.Field) != nil {
			return
		}
		if w.winLabels[spec.Field] {
			// A window label is no weight: windows run after every
			// weight is resolved.
			w.out = append(w.out, refusal(slot+" references window output "+spec.Field+" (a weight must name a cohort field; materialize the window first)",
				map[string]any{"field": spec.Field, "slot": slot, "window": spec.Field}))
			return
		}
		w.out = append(w.out, refusal(slot+" references unknown field: "+spec.Field+" (a weight must name a cohort field)",
			map[string]any{"field": spec.Field, "slot": slot}))
	}
	judge("weight", req.Weight)
	for _, s := range weightSlots(req, w.inst) {
		judge(s.slot+".weight", s.weight.Spec())
	}
}

// checkZoneFields judges every per-row zone field — the request's
// `time_zone_field` and each slot's own `tz_field` — against the cohort
// SCHEMA, not the derived column set: a zone is read through the
// field's dictionary, which only a stored categorical column carries.
// It is the refusal of a zone field absent from the schema the request
// executes over (the joined schema of a join, the synthesised schema of
// a chain stage); ResolveZones, which runs first, judges a present
// one's type and dictionary.
func (w *fieldRefWalk) checkZoneFields(req *types.Request, schema *encoding.Schema) {
	judge := func(slot, name string) {
		if name == "" || schema.Field(name) != nil {
			return
		}
		w.out = append(w.out, refusal(slot+" references unknown field: "+name+" (a zone field must name a categorical cohort field)",
			map[string]any{"field": name, "slot": slot}))
	}
	judge("time_zone_field", req.TimeZoneField)
	for _, s := range requestZoneSlots(req, w.inst) {
		judge(s.slot+".tz_field", s.tzField)
	}
}

func refusal(msg string, details map[string]any) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}

// paramString reads one string field name out of an operator's params
// (FEAT_TARGET_ENCODE "target", AGG_RATIO "numerator_field", ...); ""
// when absent or unparseable — malformed params are the operator's own
// refusal, not this rule's.
func paramString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

// filterFieldRequired reports whether a filterer type reads Field:
// every built-in except FILTER_EXPRESSION (which reads its expression).
// An extension filterer's Field is its own business, so an empty one is
// not judged.
func filterFieldRequired(t types.FiltererType) bool {
	if t == types.FILTER_EXPRESSION {
		return false
	}
	for _, b := range types.AllFiltererTypes() {
		if b == t {
			return true
		}
	}
	return false
}

// isBuiltinMatrixType reports whether t is a built-in matrix operator
// ("" — a hidden type's route — is not).
func isBuiltinMatrixType(t types.MatrixType) bool {
	for _, k := range types.AllMatrixTypes() {
		if k == t {
			return true
		}
	}
	return false
}

// checkTotal records expr's total(expr) refusal at site
// (internal/totalcall.Refusal — the rule the engine's build runs too):
// any call outside ATTR_FORMULA, a nested call or a wrong arity.
func (w *fieldRefWalk) checkTotal(expr, site string) {
	if cerr := totalcall.Refusal(expr, site); cerr != nil {
		w.out = append(w.out, cerr)
	}
}

// checkCoef records the first refusal of expr's all-literal coef() and
// sweep_axis() calls against models (internal/model/coefcall.
// StaticRefusal — the check the engine's build runs too); an embedder
// ExprFunction named coef owns that name, leaving only the sweep_axis()
// calls judged (coefcall.SweepAxisRefusal).
func (w *fieldRefWalk) checkCoef(expr string, models model.Catalog) {
	check := coefcall.StaticRefusal
	if w.snap != nil {
		for _, fn := range w.snap.ExprFunctions {
			if fn.Name == model.CoefFunc {
				check = coefcall.SweepAxisRefusal
			}
		}
	}
	if cerr := check(expr, models); cerr != nil {
		w.out = append(w.out, cerr)
	}
}
