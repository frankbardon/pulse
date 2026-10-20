package descriptor

import (
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// vectorFieldAggregations are the scalar-slot aggregators whose Field
// may name a vector field: AGG_COUNT / AGG_NULL_COUNT read only whether
// the field answered on a row (a null vector is a null input, any other
// a present one), and AGG_RATIO ignores Field (IgnoresField; it reads
// its params.numerator_field / denominator_field, which are judged).
var vectorFieldAggregations = map[types.AggregationType]bool{
	types.AGG_COUNT:      true,
	types.AGG_NULL_COUNT: true,
	types.AGG_RATIO:      true,
}

// vectorScalarRefusals is the field-reference walk's vector rule: every
// BUILT-IN slot that reads a single value from a field, naming a native
// vector field (vec_f32 / vec_f64), is refused with
// PULSE_VECTOR_FIELD_NOT_SCALAR (vectors.FieldNotScalar), in pipeline
// order. Judged:
//
//   - features: Field, FEAT_TARGET_ENCODE params.target,
//     FEAT_TRAIN_TEST_SPLIT params.stratify;
//   - filterers (and a vector attribute's params.fit_on filterers):
//     Field, except FILTER_NULL (presence) and FILTER_EXPRESSION (whose
//     expression binds a native field as a vector);
//   - attributes: Field, or Target / Predictors for ATTR_REG_FITTED /
//     RESIDUAL / LEVERAGE — not ATTR_FORMULA (reads its expression) nor
//     a vector attribute (reads its vector input);
//   - tier-1 tests: rows / cols, field, field2, split_by, subject_field;
//   - regressions: target, predictors, covariance cluster / order_by;
//   - groupers (groups and both crosstab axes): Field of a non-vector
//     grouper — a slot with no Type (no smart default fits a vector)
//     included, suggesting GROUP_KMEANS;
//   - aggregations (aggregations, the crosstab cell and margin
//     aggregations): Field of a non-vector aggregator other than
//     vectorFieldAggregations — a slot with no Type included,
//     suggesting AGG_VEC_MEAN — and every params field name
//     (aggParamFieldKeys);
//   - windows and sort keys, when they read RECORD columns (no groups
//     and no aggregations, or a crosstab): a window's Field, order_by
//     and partition_by, each sort key.
//
// Weights (weightFieldTypeRefusal), zone slots (ResolveZones), join
// keys (internal/encoding.JoinKeysRefusals) and point-lookup keys
// (processing.IndexKeyRefusal) refuse a vector field at their own
// rule. An extension operator's slots are its own business. A
// vector-free schema yields nil without walking.
func vectorScalarRefusals(req *types.Request, schema *encoding.Schema, snap *ExtensionsSnapshot, inst *InstanceSnapshot) []*errors.CodedError {
	if req == nil || schema == nil || !schema.HasVectors() {
		return nil
	}
	var out []*errors.CodedError
	judge := func(name, slot, suggest string) {
		if ce := vectors.ScalarRefusal(schema, name, slot, suggest, nil); ce != nil {
			out = append(out, ce)
		}
	}
	at := func(prefix string, i int, typ string) string {
		s := prefix + "[" + strconv.Itoa(i) + "]"
		if typ != "" {
			s += " (" + typ + ")"
		}
		return s
	}

	for i, feat := range req.Features {
		if feat == nil {
			continue
		}
		route := opRoute(inst, feat.Type)
		if !isKnownFeatureType(route) {
			continue
		}
		slot := at("features", i, string(feat.Type))
		judge(feat.Field, slot+" field", "")
		switch route {
		case types.FEAT_TARGET_ENCODE:
			judge(paramString(feat.Params, "target"), slot+" params.target", "")
		case types.FEAT_TRAIN_TEST_SPLIT:
			judge(paramString(feat.Params, "stratify"), slot+" params.stratify", "")
		}
	}

	filter := func(fil *types.Filterer, slot string) {
		route := opRoute(inst, fil.Type)
		if route == types.FILTER_NULL || route == types.FILTER_EXPRESSION || !slices.Contains(types.AllFiltererTypes(), route) {
			return
		}
		judge(fil.Field, slot+" field", "")
	}
	for i, fil := range req.Filterers {
		if fil != nil {
			filter(fil, at("filterers", i, string(fil.Type)))
		}
	}

	for i, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		route := opRoute(inst, attr.Type)
		slot := at("attributes", i, string(attr.Type))
		switch {
		case vectors.IsVectorAttribute(route):
			// AttributeFitOn lists fit_on, then a centroid's filter.
			fs, cf := vectors.AttributeFitOn(route, attr), vectors.ComparisonCentroidFilter(route, attr)
			for j, fil := range fs[:len(fs)-len(cf)] {
				if fil != nil {
					filter(fil, slot+" params.fit_on["+strconv.Itoa(j)+"]")
				}
			}
			for j, fil := range cf {
				filter(fil, slot+" params.reference.centroid.filter["+strconv.Itoa(j)+"]")
			}
		case route == types.ATTR_REG_FITTED || route == types.ATTR_REG_RESIDUAL || route == types.ATTR_REG_LEVERAGE:
			judge(attr.Target, slot+" target", "")
			for j, p := range attr.Predictors {
				judge(p, slot+" predictors["+strconv.Itoa(j)+"]", "")
			}
		case route == types.ATTR_FORMULA || route == types.ATTR_REG_PREDICT:
		case route == types.ATTR_PREVIOUS_STATE:
			judge(attr.Field, slot+" field", "")
			judge(attr.SubjectField, slot+" subject_field", "")
			judge(attr.OrderField, slot+" order_field", "")
		case slices.Contains(types.AllAttributeTypes(), route):
			judge(attr.Field, slot+" field", "")
		}
	}

	for i, t := range req.Tests {
		if t == nil || !slices.Contains(types.AllTestTypes(), opRoute(inst, t.Type)) {
			continue
		}
		slot := at("tests", i, string(t.Type))
		judge(t.Rows, slot+" rows", "")
		judge(t.Cols, slot+" cols", "")
		judge(t.Field, slot+" field", "")
		judge(t.Field2, slot+" field2", "")
		judge(t.SplitBy, slot+" split_by", "")
		judge(t.SubjectField, slot+" subject_field", "")
		if fieldaccept.TakesCovariate(opRoute(inst, t.Type)) {
			judge(fieldaccept.CovariateParam(t.Params), slot+" params.covariate", "")
		}
	}

	for i, reg := range req.Regressions {
		if reg == nil || !slices.Contains(types.AllRegressionTypes(), opRoute(inst, reg.Type)) && !isRegisteredRegression(inst, snap, reg.Type) {
			continue
		}
		slot := at("regressions", i, string(reg.Type))
		judge(reg.Target, slot+" target", "")
		for j, p := range reg.Predictors {
			judge(p, slot+" predictors["+strconv.Itoa(j)+"]", "")
		}
		if c := reg.Covariance; c != nil {
			judge(c.Cluster, slot+" covariance.cluster", "")
			judge(c.OrderBy, slot+" covariance.order_by", "")
		}
		judge(reg.JackknifeBy, slot+" jackknife_by", "")
	}

	grouper := func(g *types.Group, slot string) {
		route := opRoute(inst, g.Type)
		if g.Type != "" && (vectors.IsVectorGroup(route) || !slices.Contains(types.AllGroupTypes(), route)) {
			return
		}
		judge(g.Field, slot+" field", vectors.SuggestGrouper)
	}
	kinds := aggregationKinds(inst, snap)
	aggregation := func(a *types.Aggregation, slot string) {
		route := opRoute(inst, a.Type)
		if a.Type != "" && !slices.Contains(types.AllAggregationTypes(), route) {
			return
		}
		if a.Type == "" || (!kinds.IsVector(a.Type) && !vectorFieldAggregations[route]) {
			judge(a.Field, slot+" field", vectors.SuggestAggregation)
		}
		for _, key := range aggParamFieldKeys[route] {
			judge(paramString(a.Params, key), slot+" params."+key, "")
		}
	}
	for i, g := range req.Groups {
		if g != nil {
			grouper(g, at("groups", i, string(g.Type)))
		}
	}
	for i, a := range req.Aggregations {
		if a != nil {
			aggregation(a, at("aggregations", i, string(a.Type)))
		}
	}
	if spec := req.Crosstab; spec != nil {
		for i, g := range spec.Rows {
			if g != nil {
				grouper(g, at("crosstab.rows", i, string(g.Type)))
			}
		}
		for i, g := range spec.Columns {
			if g != nil {
				grouper(g, at("crosstab.columns", i, string(g.Type)))
			}
		}
		if spec.HasMeasures() {
			for i, a := range spec.Cells {
				if a != nil {
					aggregation(a, at("crosstab.cells", i, string(a.Type)))
				}
			}
		} else if spec.Cell != nil {
			aggregation(spec.Cell, "crosstab.cell ("+string(spec.Cell.Type)+")")
		}
		for i, a := range spec.MarginAggregations {
			if a != nil {
				aggregation(a, at("crosstab.margin_aggregations", i, string(a.Type)))
			}
		}
	}

	// Windows and sort read the OUTPUT row; only a record-level output
	// row (no groups, no aggregations — or a crosstab's record frame)
	// carries the schema's vector fields.
	if req.Crosstab == nil && (len(req.Groups) > 0 || len(req.Aggregations) > 0) {
		return out
	}
	for i, win := range req.Windows {
		if win == nil || !slices.Contains(types.AllWindowTypes(), opRoute(inst, win.Type)) {
			continue
		}
		slot := at("windows", i, string(win.Type))
		if windowparams.FieldRequired(opRoute(inst, win.Type)) {
			judge(win.Field, slot+" field", "")
		}
		if opRoute(inst, win.Type) == types.WIN_LIFT {
			judge(paramString(win.Params, "base"), slot+" params.base", "")
		}
		for j, k := range win.OrderBy {
			judge(k.Field, slot+" order_by["+strconv.Itoa(j)+"]", "")
		}
		for j, p := range win.PartitionBy {
			judge(p, slot+" partition_by["+strconv.Itoa(j)+"]", "")
		}
	}
	for i, k := range req.Sort {
		judge(k.Field, "sort["+strconv.Itoa(i)+"]", "")
	}
	return out
}
