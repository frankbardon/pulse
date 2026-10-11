package descriptor

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
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
	if all := fieldRefRefusalsWithModels(req, schema, inst.Extensions(), inst, models); len(all) > 0 {
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
	return fieldRefRefusalsWithModels(&types.Request{Filterers: req.Filterers}, schema, inst.Extensions(), inst, models)
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
	return FieldRefRefusals(&types.Request{Filterers: req.Filterers}, schema, snap)
}

// FieldRefRefusals walks every slot that names a field, in PIPELINE
// order, against the columns available at that point:
//
//  1. features — the schema plus each earlier feature's outputs
//     (Field; FEAT_TARGET_ENCODE params.target; FEAT_TRAIN_TEST_SPLIT
//     params.stratify);
//  2. filterers — the schema plus every feature output (attributes run
//     after filters, so their labels are NOT visible to a filter);
//  3. attributes — as filterers plus each EARLIER attribute's label
//     (Field, or Target / Predictors for ATTR_REG_*; ATTR_FORMULA reads
//     its expression and may omit Field);
//  4. tier-1 tests, regressions, groups, aggregations and the crosstab
//     axes / cell / margin aggregations — the record columns: schema,
//     features and every attribute label;
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
//  6. vectors — Request.Vectors resolved against the schema alone
//     (internal/vectors.Resolve): the first PULSE_VECTOR_* refusal, or
//     the SERVICE_VALIDATION unknown-field refusal for a literal member
//     the schema lacks; then a vector name equal to a feature output,
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
//     Steps 6 to 10 contribute one refusal at most between them.
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
	if req == nil || schema == nil {
		return nil
	}
	w := &fieldRefWalk{cols: make(map[string]bool, len(schema.Fields)), snap: snap, inst: inst}
	for i := range schema.Fields {
		w.cols[schema.Fields[i].Name] = true
	}

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

	// 2. Filterers.
	for _, fil := range req.Filterers {
		if fil == nil {
			continue
		}
		w.checkInputs("filterer", string(fil.Type), nil, "filter")
		if opRoute(inst, fil.Type) == types.FILTER_EXPRESSION {
			w.checkTotal(fil.Expression, string(types.FILTER_EXPRESSION))
			w.checkCoef(fil.Expression, models)
		}
		if fil.Field == "" && !filterFieldRequired(opRoute(inst, fil.Type)) {
			continue
		}
		w.check(fil.Field, func() *errors.CodedError {
			return refusal("filter references unknown field: "+fil.Field,
				map[string]any{"field": fil.Field, "filter": string(fil.Type)})
		})
	}

	// 3. Attributes, each seeing the earlier ones' labels.
	for _, attr := range req.Attributes {
		if attr == nil || attr.Type == "ATTR_RANK" {
			continue
		}
		attrRoute := opRoute(inst, attr.Type)
		switch attrRoute {
		case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
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
			p, sig, cerr := model.CheckScore(attr, models)
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
				w.checkTotal(attr.Expression, totalcall.FormulaSite)
				w.checkCoef(attr.Expression, models)
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

	// 4. Record-level consumers.
	w.checkWeights(req, schema)
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
			w.check(reg.Target, func() *errors.CodedError {
				return refusal("regression references unknown target field: "+reg.Target,
					map[string]any{"field": reg.Target, "type": string(reg.Type)})
			})
		}
		for _, p := range reg.Predictors {
			w.check(p, func() *errors.CodedError {
				return refusal("regression references unknown predictor field: "+p,
					map[string]any{"field": p, "type": string(reg.Type)})
			})
		}
		if c := reg.Covariance; c != nil {
			for _, f := range []string{c.Cluster, c.OrderBy} {
				if f == "" {
					continue
				}
				w.check(f, func() *errors.CodedError {
					return refusal("regression covariance references unknown field: "+f,
						map[string]any{"field": f, "type": string(reg.Type)})
				})
			}
		}
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
		w.check(agg.Field, func() *errors.CodedError {
			return refusal("aggregation references unknown field: "+agg.Field,
				map[string]any{"field": agg.Field, "aggregation": string(agg.Type)})
		})
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
		if spec.Cell != nil {
			agg(spec.Cell, "cell aggregation")
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
			out[key] = true
			if _, ok := owner[key]; !ok {
				owner[key] = map[string]any{"group_index": gi, "group": string(grp.Type)}
			}
		}
		for ai, agg := range req.Aggregations {
			if agg == nil {
				continue
			}
			label := agg.Label
			if label == "" {
				label = string(agg.Type) + "_" + agg.Field
			}
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
				continue
			}
			out[label] = true
			owner[label] = map[string]any{"other_aggregation_index": ai, "other_aggregation": string(agg.Type)}
		}
		w.cols = out
	}
	for i, win := range req.Windows {
		if win == nil {
			continue
		}
		idx := strconv.Itoa(i)
		for _, ok := range win.OrderBy {
			if ok.Field == "" {
				continue
			}
			w.check(ok.Field, func() *errors.CodedError {
				return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
					"window["+idx+"]: order_by field "+ok.Field+" does not exist in schema or upstream pipeline output",
					map[string]any{"window_index": i, "field": ok.Field})
			})
			// Orderability (IsOrderableType) is judged by the schema
			// type; a derived output column carries none.
			if f := schema.Field(ok.Field); f != nil && !IsOrderableType(f.Type) {
				w.out = append(w.out, errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
					"window["+idx+"]: order_by field "+ok.Field+" is not orderable (type "+f.Type.String()+")",
					map[string]any{"window_index": i, "field": ok.Field, "field_type": f.Type.String()}))
			}
		}
		for _, p := range win.PartitionBy {
			if p == "" {
				continue
			}
			w.check(p, func() *errors.CodedError {
				return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
					"window["+idx+"]: partition_by field "+p+" does not exist in schema or upstream pipeline output",
					map[string]any{"window_index": i, "field": p})
			})
		}
		// Only a value-bearing operator reads Field (WIN_ROW_NUMBER /
		// RANK / DENSE_RANK name none, and an extension window's Field
		// is its own business).
		if win.Field != "" && windowFieldRequired[opRoute(inst, win.Type)] {
			w.check(win.Field, func() *errors.CodedError {
				return refusal("window["+idx+"] ("+string(win.Type)+"): field "+win.Field+" does not exist in schema or upstream pipeline output",
					map[string]any{"window_index": i, "field": win.Field, "type": string(win.Type)})
			})
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
	for i, k := range req.Sort {
		if k.Field == "" {
			continue
		}
		idx := strconv.Itoa(i)
		w.check(k.Field, func() *errors.CodedError {
			return refusal("sort["+idx+"]: field "+k.Field+" is not produced by the pipeline (no schema field, aggregation, attribute, group, or window output matches)",
				map[string]any{"sort_index": i, "field": k.Field})
		})
	}
	if req.Crosstab == nil {
		for i, t := range req.PostTests {
			if t != nil {
				w.checkTest(t, i)
			}
		}
	}

	// 6. Vectors — resolved against the SCHEMA the request executes
	// over (members are source columns, never derived ones) by the one
	// resolver internal/vectors, which projection and predict's
	// resolved_vectors echo also call. Resolution stops at its first
	// failure, so it contributes one refusal at most.
	//
	// 7. Matrices — each Request.Matrices spec resolved by the same
	// leaf (vectors.ResolveMatrices, which runs step 6 first): type,
	// vector-XOR-fields shape, encoding, result-name uniqueness, params,
	// the named vector (PULSE_VECTOR_UNKNOWN) or the inline fields (the
	// vector rules). A type the instance hides is an unknown type.
	if _, verr := resolveMatricesFor(req, schema, w.snap, inst); verr != nil {
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
	if _, aerr := vectors.ResolveAttributes(req, schema, func(t types.AttributeType) types.AttributeType {
		return opRoute(inst, t)
	}, w.extensionNamed(func(s *ExtensionsSnapshot) []descriptor.OperatorMeta { return s.Attributes })); aerr != nil {
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
	}
	return w.out
}

// resolveMatricesFor is the one instance-scoped Request.Matrices
// resolution every descriptor site reads (the field-reference walk,
// predict, the limits): a hidden matrix type is unknown, and a
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
	return vectors.ResolveMatricesRouted(req, schema, func(t types.MatrixType) bool {
		return isBuiltinMatrixType(opRoute(inst, t))
	}, func(t types.GroupType) types.GroupType {
		return opRoute(inst, t)
	}, ext)
}

// checkFitOn judges one params.fit_on filterer of a fitted vector
// attribute by the Request filterer rule (step 2) against the columns
// visible to the attribute.
func (w *fieldRefWalk) checkFitOn(fil *types.Filterer, attr *types.Attribute, models model.Catalog) {
	w.checkInputs("filterer", string(fil.Type), nil, "filter")
	if opRoute(w.inst, fil.Type) == types.FILTER_EXPRESSION {
		w.checkTotal(fil.Expression, string(types.FILTER_EXPRESSION))
		w.checkCoef(fil.Expression, models)
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
var aggParamFieldKeys = map[types.AggregationType][]string{
	types.AGG_WEIGHTED_MEAN: {"weight_field"},
	types.AGG_RATIO:         {"numerator_field", "denominator_field"},
	types.AGG_DISTINCT_SUM:  {"distinct_by"},
	types.AGG_MAPE:          {"prediction_field"},
	types.AGG_RMSE:          {"prediction_field"},
	types.AGG_MAE:           {"prediction_field"},
}

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
// reads — rows/cols for TEST_CHISQ / TEST_FISHER_EXACT, field and
// split_by for TEST_PROP_Z, otherwise field, field2 (paired /
// bivariate tests), split_by, subject_field (TEST_ANOVA_RM) and, on a
// post-test, its order_by keys.
func (w *fieldRefWalk) checkTest(t *types.Test, postIndex int) {
	add := func(name, msg string, details map[string]any) {
		if name == "" {
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
	default:
		add(t.Field, typ+" references unknown field: "+t.Field, map[string]any{"type": typ, "field": t.Field})
		if numericField2Tests[route] {
			add(t.Field2, typ+" references unknown field2: "+t.Field2, map[string]any{"type": typ, "field2": t.Field2})
		}
		add(t.SplitBy, typ+" split_by references unknown field: "+t.SplitBy, map[string]any{"type": typ, "split_by": t.SplitBy})
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
		w.out = append(w.out, refusal(slot+" references unknown field: "+spec.Field+" (a weight must name a cohort field)",
			map[string]any{"field": spec.Field, "slot": slot}))
	}
	judge("weight", req.Weight)
	for _, s := range weightSlots(req, w.inst) {
		judge(s.slot+".weight", s.weight.Spec())
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

// checkCoef records the first refusal of expr's all-literal coef()
// calls against models (internal/model/coefcall.StaticRefusal — the
// check the engine's build runs too), unless an embedder ExprFunction
// named coef owns the name.
func (w *fieldRefWalk) checkCoef(expr string, models model.Catalog) {
	if w.snap != nil {
		for _, fn := range w.snap.ExprFunctions {
			if fn.Name == model.CoefFunc {
				return
			}
		}
	}
	if cerr := coefcall.StaticRefusal(expr, models); cerr != nil {
		w.out = append(w.out, cerr)
	}
}
