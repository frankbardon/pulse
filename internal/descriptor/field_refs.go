package descriptor

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
func FieldRefRefusal(req *types.Request, schema *encoding.Schema) error {
	if all := FieldRefRefusals(req, schema); len(all) > 0 {
		return all[0]
	}
	return nil
}

// FacetFieldRefRefusals is FieldRefRefusals for a FacetRequest: its
// filterers are judged by the Request filterer rule against the cohort
// schema (a facet has no features, so the schema is the whole column
// set). FacetSchema applies the first entry right after its zone pass;
// ValidateFacet reports every entry at the same point. Fields and
// AdditiveFields keep their own facet-specific refusals.
func FacetFieldRefRefusals(req *types.FacetRequest, schema *encoding.Schema) []*errors.CodedError {
	if req == nil {
		return nil
	}
	return FieldRefRefusals(&types.Request{Filterers: req.Filterers}, schema)
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
//     see every window). A crosstab's sort and windows judge against
//     the record columns; its post-tests run over cell rows and are not
//     judged here.
//
// Derived names follow the runtime's own naming (featureOutputLabels,
// attributeDefaultLabel, "<TYPE>_<field>" for an aggregation,
// windowLabel), so a defaulted Type is visible in a label. A feature
// whose outputs the rule cannot name (an extension feature) leaves the
// column set open: every later record- and output-level name is
// accepted, as the runtime may legitimately produce it. Empty names are
// judged where the slot requires one (aggregation, group, attribute
// other than ATTR_FORMULA) and skipped where it is optional. Fields
// named only inside an operator's params (other than the two feature
// params above) are not judged. A nil request or schema yields nil —
// the schema-less mode of a validator that cannot read the cohort.
func FieldRefRefusals(req *types.Request, schema *encoding.Schema) []*errors.CodedError {
	if req == nil || schema == nil {
		return nil
	}
	w := &fieldRefWalk{cols: make(map[string]bool, len(schema.Fields))}
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
		switch feat.Type {
		case types.FEAT_TRAIN_TEST_SPLIT:
			if s := featureParamField(feat.Params, "stratify"); s != "" {
				w.check(s, func() *errors.CodedError {
					return refusal("feature FEAT_TRAIN_TEST_SPLIT: stratify references unknown field "+s,
						map[string]any{"field": s, "feature": string(feat.Type)})
				})
			}
		default:
			if feat.Field != "" {
				w.check(feat.Field, mk(feat.Field))
			}
			if feat.Type == types.FEAT_TARGET_ENCODE {
				if t := featureParamField(feat.Params, "target"); t != "" {
					w.check(t, func() *errors.CodedError {
						return refusal("feature FEAT_TARGET_ENCODE: target references unknown field "+t,
							map[string]any{"field": t, "feature": string(feat.Type)})
					})
				}
			}
		}
		if !isKnownFeatureType(feat.Type) {
			w.open = true
			continue
		}
		for _, label := range featureOutputLabels(feat, schema) {
			w.cols[label] = true
		}
	}

	// 2. Filterers.
	for _, fil := range req.Filterers {
		if fil == nil || fil.Field == "" {
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
		switch attr.Type {
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
		default:
			if attr.Type != types.ATTR_FORMULA || attr.Field != "" {
				w.check(attr.Field, func() *errors.CodedError {
					return refusal("attribute references unknown field: "+attr.Field,
						map[string]any{"field": attr.Field, "attribute": string(attr.Type)})
				})
			}
		}
		label := attr.Label
		if label == "" {
			label = attributeDefaultLabel(attr)
		}
		w.cols[label] = true
	}

	// 4. Record-level consumers.
	for _, t := range req.Tests {
		if t != nil {
			w.checkTest(t, -1)
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
	}
	for _, grp := range req.Groups {
		if grp == nil {
			continue
		}
		w.check(grp.Field, func() *errors.CodedError {
			return refusal("group references unknown field: "+grp.Field,
				map[string]any{"field": grp.Field, "group": string(grp.Type)})
		})
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			continue
		}
		w.check(agg.Field, func() *errors.CodedError {
			return refusal("aggregation references unknown field: "+agg.Field,
				map[string]any{"field": agg.Field, "aggregation": string(agg.Type)})
		})
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
		for _, g := range spec.Rows {
			if g != nil {
				axis(g.Field, "row grouper")
			}
		}
		for _, g := range spec.Columns {
			if g != nil {
				axis(g.Field, "column grouper")
			}
		}
		if spec.Cell != nil {
			axis(spec.Cell.Field, "cell aggregation")
		}
		for _, agg := range spec.MarginAggregations {
			if agg != nil {
				axis(agg.Field, "margin aggregation")
			}
		}
	}

	// 5. Output-row consumers.
	if req.Crosstab == nil && (len(req.Groups) > 0 || len(req.Aggregations) > 0) {
		out := make(map[string]bool, len(req.Groups)+len(req.Aggregations)+len(req.Windows))
		for _, grp := range req.Groups {
			if grp != nil {
				out[grp.Field] = true
			}
		}
		for _, agg := range req.Aggregations {
			if agg == nil {
				continue
			}
			label := agg.Label
			if label == "" {
				label = string(agg.Type) + "_" + agg.Field
			}
			out[label] = true
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
		if win.Field != "" && windowFieldRequired[win.Type] {
			w.check(win.Field, func() *errors.CodedError {
				return refusal("window["+idx+"] ("+string(win.Type)+"): field "+win.Field+" does not exist in schema or upstream pipeline output",
					map[string]any{"window_index": i, "field": win.Field, "type": string(win.Type)})
			})
		}
		w.cols[windowLabel(win)] = true
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
	return w.out
}

// fieldRefWalk carries the column set available at the current
// pipeline point and the refusals found so far.
type fieldRefWalk struct {
	cols map[string]bool
	open bool
	out  []*errors.CodedError
}

func (w *fieldRefWalk) check(name string, mk func() *errors.CodedError) {
	if w.open || w.cols[name] {
		return
	}
	w.out = append(w.out, mk())
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
	switch t.Type {
	case types.TEST_CHISQ, types.TEST_FISHER_EXACT:
		add(t.Rows, "TEST_CHISQ rows references unknown field: "+t.Rows, map[string]any{"axis": "rows", "field": t.Rows})
		add(t.Cols, "TEST_CHISQ cols references unknown field: "+t.Cols, map[string]any{"axis": "cols", "field": t.Cols})
	case types.TEST_PROP_Z:
		add(t.Field, "TEST_PROP_Z field references unknown field: "+t.Field, map[string]any{"axis": "field", "field": t.Field})
		add(t.SplitBy, "TEST_PROP_Z split_by references unknown field: "+t.SplitBy, map[string]any{"axis": "split_by", "field": t.SplitBy})
	default:
		add(t.Field, typ+" references unknown field: "+t.Field, map[string]any{"type": typ, "field": t.Field})
		if numericField2Tests[t.Type] {
			add(t.Field2, typ+" references unknown field2: "+t.Field2, map[string]any{"type": typ, "field2": t.Field2})
		}
		add(t.SplitBy, typ+" split_by references unknown field: "+t.SplitBy, map[string]any{"type": typ, "split_by": t.SplitBy})
		if t.Type == types.TEST_ANOVA_RM {
			add(t.SubjectField, "TEST_ANOVA_RM subject_field references unknown field: "+t.SubjectField,
				map[string]any{"type": typ, "subject_field": t.SubjectField})
		}
	}
	if postIndex >= 0 {
		for _, ok := range t.OrderBy {
			add(ok.Field, typ+" order_by references unknown field: "+ok.Field, map[string]any{"type": typ, "field": ok.Field})
		}
	}
}

func refusal(msg string, details map[string]any) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}

// featureParamField reads one string field name out of a feature's
// params (FEAT_TARGET_ENCODE "target", FEAT_TRAIN_TEST_SPLIT
// "stratify"); "" when absent or unparseable — malformed params are
// the feature validators' refusal, not this rule's.
func featureParamField(raw json.RawMessage, key string) string {
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
