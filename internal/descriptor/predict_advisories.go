package descriptor

import (
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regpenalty"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// predict_advisories.go answers PredictResult.Advisories: coded,
// non-blocking notes that the chosen analysis may not fit the data.
// No-execute like the rest of predict — every rule reads only the
// request, the schema and its dictionaries, and figures predict has
// already computed (PValues). The trigger principle is authoritative
// metadata only: no field-name or value-shape heuristics.
//
// An advisory is never a warning (strict mode never sees one) and
// never changes execution. Any operator an advisory names — in its
// message or details.suggested — is one the instance offers.

// advisoryCodes is every registered advisory code, in rule order. It is
// the set Options.SuppressAdvisories is validated against.
var advisoryCodes = []errors.Code{
	errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS,
	errors.PULSE_ADVISORY_MANY_TESTS,
	errors.PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC,
	errors.PULSE_ADVISORY_ORDINAL_PARAMETRIC,
	errors.PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED,
	errors.PULSE_ADVISORY_EXTRA_PASS,
	errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE,
	errors.PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED,
	errors.PULSE_ADVISORY_JACKKNIFE_COST,
	errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED,
	errors.PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT,
}

// AdvisoryCodes returns every registered PULSE_ADVISORY_* code, in rule
// order, as a fresh slice.
func AdvisoryCodes() []string {
	out := make([]string, len(advisoryCodes))
	for i, c := range advisoryCodes {
		out[i] = string(c)
	}
	return out
}

// ValidateSuppressAdvisories refuses an Options.SuppressAdvisories entry
// that is not a registered advisory code with
// PULSE_SUPPRESS_ADVISORY_UNKNOWN (details: code, valid). Validated
// against the whole registry, not the instance view: suppressing a code
// a feature profile can never fire is harmless.
func ValidateSuppressAdvisories(codes []string) error {
	valid := AdvisoryCodes()
	for _, c := range codes {
		if !slices.Contains(valid, c) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN,
				"Options.SuppressAdvisories: "+strconv.Quote(c)+" is not an advisory code",
				map[string]any{"code": c, "valid": valid})
		}
	}
	return nil
}

// manyGroupAlternatives is the replacement the two-group advisory
// suggests, preferred first: Welch's ANOVA (the many-group form of the
// Welch two-sample test both TEST_T and TEST_WELCH run, and the NotFor
// "three or more groups" alternative of both), then the classic F.
var manyGroupAlternatives = []types.TestType{types.TEST_ANOVA_WELCH, types.TEST_ANOVA_F}

// advisoryInput is everything the advisory rules read. Every field is
// metadata predict already holds; nothing here touches a record.
type advisoryInput struct {
	// req is the defaults-resolved request; schema the one it executes
	// over (joined when Request.Joins resolves).
	req    *types.Request
	schema *encoding.Schema
	// cohortSchema is the cohort's own schema — the one its SPSS
	// metadata sidecar describes.
	cohortSchema *encoding.Schema
	// plan is ResolveMultiplicity's (nil when nothing names a block) and
	// pv the result's PValues.
	plan *MultiplicityPlan
	pv   *descriptor.PValueCount
	// suggestedWeight is PredictResult.SuggestedWeight: set only when the
	// sidecar names a usable weight, no slot resolves one and the
	// instance offers capability:weighting.
	suggestedWeight *descriptor.SuggestedWeight
	// measures is PredictOptions.SidecarMeasureLevels.
	measures map[string]string
	inst     *InstanceSnapshot
	// records is the cohort's record count (the file length's, or the
	// archive's shard sum) when recordsKnown — header metadata, never a
	// record read.
	records      int64
	recordsKnown bool
	// prefix is prepended to every slot path an advisory names
	// ("requests[1]." for a Compose slot; "" on a Request root) and
	// extra is merged into every advisory's details (a Compose slot's
	// {"request": i}). subject opens the many-tests message; "" is
	// "The request emits".
	prefix  string
	extra   map[string]any
	subject string
}

// computeAdvisories runs every advisory rule, in advisoryCodes order.
// Suppressed codes are dropped. Nil when none fires.
func computeAdvisories(in advisoryInput) []descriptor.Advisory {
	if in.req == nil {
		return nil
	}
	var out []descriptor.Advisory
	add := func(as ...descriptor.Advisory) {
		for _, a := range as {
			if in.inst.AdvisorySuppressed(a.Code) {
				continue
			}
			for k, v := range in.extra {
				a.Details[k] = v
			}
			out = append(out, a)
		}
	}
	add(twoGroupAdvisories(in.req, in.schema, in.prefix, in.inst)...)
	if a, ok := manyTestsAdvisory(in.plan != nil, in.pv, in.subject); ok {
		add(a)
	}
	add(measureLevelAdvisories(in.req, in.cohortSchema, in.measures, in.prefix, in.inst)...)
	if a, ok := weightUnusedAdvisory(in.suggestedWeight); ok {
		add(a)
	}
	add(extraPassAdvisories(in.req, in.prefix, in.inst)...)
	add(seriesAxisAdvisories(in.req, in.schema, in.prefix, in.inst)...)
	add(extensionParamsAdvisories(in.req, in.prefix, in.inst)...)
	if in.recordsKnown {
		add(jackknifeCostAdvisories(in.req, in.records, in.prefix, in.inst)...)
	}
	add(penalizedInferenceAdvisories(in.req, in.prefix, in.inst)...)
	return out
}

// penalizedInferenceAdvisories: one
// PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED per REG_OLS / REG_GLM
// slot the instance offers whose standard errors and p-values the
// runtime reports null — penalty l1 / elasticnet without a Resample
// modifier (regpenalty.InferenceUndefined, the engines' own predicate).
// The wording is regpenalty.InferenceAdvisory's, shared with the
// advisory the fit's RegressionResult carries.
func penalizedInferenceAdvisories(req *types.Request, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	var out []descriptor.Advisory
	for i, reg := range req.Regressions {
		if reg == nil || !regpenalty.InferenceUndefined(reg) {
			continue
		}
		if op := opRoute(inst, reg.Type); op != types.REG_OLS && op != types.REG_GLM {
			continue
		}
		msg, details := regpenalty.InferenceAdvisory(reg, prefix+"regressions["+strconv.Itoa(i)+"]")
		out = append(out, descriptor.Advisory{
			Code:    string(regpenalty.InferenceAdvisoryCode),
			Message: msg,
			Details: details,
		})
	}
	return out
}

// jackknifeCostAdvisories: one PULSE_ADVISORY_JACKKNIFE_COST per
// REG_OLS / REG_GLM slot the instance offers that runs the leave-one-out
// jackknife (regresample.LeaveOneOut) over a cohort of at least
// regresample.LeaveOneOutCostRows records. The count is the cohort's —
// filters may leave fewer rows, so it is an upper bound on the refits.
func jackknifeCostAdvisories(req *types.Request, records int64, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	if records < regresample.LeaveOneOutCostRows {
		return nil
	}
	var out []descriptor.Advisory
	for i, reg := range req.Regressions {
		if reg == nil || !regresample.LeaveOneOut(reg) {
			continue
		}
		if op := opRoute(inst, reg.Type); op != types.REG_OLS && op != types.REG_GLM {
			continue
		}
		slot := prefix + "regressions[" + strconv.Itoa(i) + "]"
		out = append(out, descriptor.Advisory{
			Code: string(errors.PULSE_ADVISORY_JACKKNIFE_COST),
			Message: string(reg.Type) + " at " + slot + " runs the leave-one-out jackknife over a cohort of " +
				strconv.FormatInt(records, 10) + " records, so the run refits the model once per row and its cost grows with the square of the row count; " +
				"jackknife_groups " + strconv.Itoa(regresample.SuggestedGroups) + " refits " + strconv.Itoa(regresample.SuggestedGroups) +
				" times instead (or jackknife_by a field of natural clusters).",
			Details: map[string]any{
				"slot":      slot,
				"operator":  string(reg.Type),
				"records":   records,
				"threshold": regresample.LeaveOneOutCostRows,
				"suggested": map[string]any{"jackknife_groups": regresample.SuggestedGroups},
			},
		})
	}
	return out
}

// twoGroupAdvisories: one PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS per
// TEST_T / TEST_WELCH slot (tests, then post-tests) whose split_by is a
// categorical field with more than two dictionary entries. A packed_bool
// split has two groups and never fires; a field split_by may not name
// (numeric) is predict's error, not an advisory.
func twoGroupAdvisories(req *types.Request, schema *encoding.Schema, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	var out []descriptor.Advisory
	for _, tier := range []struct {
		key  string
		list []*types.Test
	}{{"tests", req.Tests}, {"post_tests", req.PostTests}} {
		for i, t := range tier.list {
			if t == nil || t.SplitBy == "" {
				continue
			}
			routed := opRoute(inst, t.Type)
			if routed != types.TEST_T && routed != types.TEST_WELCH {
				continue
			}
			groups := splitGroupCount(schema, t.SplitBy)
			if groups <= 2 {
				continue
			}
			slot := prefix + tier.key + "[" + strconv.Itoa(i) + "]"
			details := map[string]any{
				"slot":     slot,
				"operator": string(routed),
				"field":    t.Field,
				"split_by": t.SplitBy,
				"groups":   groups,
			}
			next := "filter split_by down to the two groups you mean to compare"
			for _, alt := range manyGroupAlternatives {
				if inst.Enabled(string(alt)) {
					details["suggested"] = string(alt)
					next = "consider " + string(alt) + ", which compares every group at once, or " + next
					break
				}
			}
			out = append(out, descriptor.Advisory{
				Code: string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS),
				Message: string(routed) + " at " + slot + " splits " + t.Field + " by " + t.SplitBy +
					", whose dictionary holds " + strconv.Itoa(groups) +
					" groups, but a two-sample test compares exactly two; " + next + ".",
				Details: details,
			})
		}
	}
	return out
}

// splitGroupCount is the group count a split_by field resolves to from
// the schema alone: a categorical field's dictionary entry count, 2 for
// packed_bool, 0 (unknown) otherwise.
func splitGroupCount(schema *encoding.Schema, field string) int {
	if schema == nil {
		return 0
	}
	for _, f := range schema.Fields {
		if f.Name != field {
			continue
		}
		switch {
		case f.Type.IsCategorical() && f.Dictionary != nil:
			return f.Dictionary.Count()
		case f.Type == encoding.FieldTypePackedBool:
			return 2
		}
		return 0
	}
	return 0
}

// manyTestsAdvisory: PULSE_ADVISORY_MANY_TESTS when the request emits at
// least MultiplicityTriggerThreshold uncorrected p-values and nothing —
// the request, a slot or Options.DefaultMultiplicity — names a
// multiplicity block (named false: the resolved plan is nil). pv is nil
// when the instance hides the multiple-comparison capability, so the
// advisory never proposes it. subject opens the message ("" is "The
// request emits").
func manyTestsAdvisory(named bool, pv *descriptor.PValueCount, subject string) (descriptor.Advisory, bool) {
	if named || pv == nil || pv.Uncorrected < descriptor.MultiplicityTriggerThreshold {
		return descriptor.Advisory{}, false
	}
	if subject == "" {
		subject = "The request emits"
	}
	count := strconv.Itoa(pv.Uncorrected)
	switch pv.Basis {
	case descriptor.PValueBasisLowerBound:
		count = "at least " + count
	case descriptor.PValueBasisDictionary:
		count = "an estimated " + count
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_MANY_TESTS),
		Message: subject + " " + count + " uncorrected p-values (threshold " +
			strconv.Itoa(descriptor.MultiplicityTriggerThreshold) +
			") and names no multiplicity block; with this many tests some may fall below alpha by chance alone, so consider adding one such as holm.",
		Details: map[string]any{
			"total":       pv.Total,
			"uncorrected": pv.Uncorrected,
			"basis":       pv.Basis,
			"threshold":   descriptor.MultiplicityTriggerThreshold,
			"suggested":   map[string]any{"multiplicity": map[string]any{"method": "holm"}},
		},
	}, true
}

// SPSS measure levels the sidecar records (record 7/11) that the
// measure-level rules act on. "scale" and "unset" never fire.
const (
	measureNominal = "nominal"
	measureOrdinal = "ordinal"
)

// meanFamilyAggregators are the aggregators that read a field as a
// quantity with meaningful distances — a mean, or a moment built on
// one. A nominal field's codes have none.
var meanFamilyAggregators = []types.AggregationType{
	types.AGG_AVERAGE, types.AGG_WEIGHTED_MEAN, types.AGG_STDDEV, types.AGG_VARIANCE,
	types.AGG_ZSCORE, types.AGG_SKEWNESS, types.AGG_KURTOSIS,
	types.AGG_CI_LOWER, types.AGG_CI_UPPER, types.AGG_WELFORD,
}

// parametricTests are the tests that compare means or a straight-line
// correlation, so assume an evenly spaced (interval) measure.
var parametricTests = []types.TestType{
	types.TEST_T, types.TEST_WELCH, types.TEST_Z_TWO_SAMPLE, types.TEST_PAIRED_T,
	types.TEST_ANOVA_F, types.TEST_ANOVA_WELCH, types.TEST_ANOVA_RM, types.TEST_PEARSON_R,
}

// rankBasedTests are the alternatives ORDINAL_PARAMETRIC may suggest:
// tests that read only the order of the values.
var rankBasedTests = []string{
	string(types.TEST_MANN_WHITNEY_U), string(types.TEST_WILCOXON_SR), string(types.TEST_KRUSKAL_WALLIS),
	string(types.TEST_SPEARMAN_R), string(types.TEST_KENDALL_TAU),
}

// advisoryParametricTestNames is the owner list of
// PULSE_ADVISORY_ORDINAL_PARAMETRIC.
func advisoryParametricTestNames() []string {
	out := make([]string, len(parametricTests))
	for i, t := range parametricTests {
		out[i] = string(t)
	}
	return out
}

// advisoryQuantityOperators is the owner list of
// PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC: every operator the rule fires on.
func advisoryQuantityOperators() []string {
	out := make([]string, 0, len(meanFamilyAggregators)+len(parametricTests))
	for _, a := range meanFamilyAggregators {
		out = append(out, string(a))
	}
	return append(out, advisoryParametricTestNames()...)
}

// sidecarMeasure is the measure level the sidecar records for field —
// nominal or ordinal only — when field is a numeric column of the
// cohort schema (the schema the sidecar describes). A field the schema
// no longer carries, or carries as a non-numeric type (a labelled SPSS
// numeric imports categorical), yields "": a stale or inapplicable
// entry is silent, like suggested_weight.
func sidecarMeasure(cohort *encoding.Schema, measures map[string]string, field string) string {
	if field == "" || len(measures) == 0 || cohort == nil {
		return ""
	}
	m := measures[field]
	if m != measureNominal && m != measureOrdinal {
		return ""
	}
	f := cohort.Field(field)
	if f == nil || !(f.Type.IsNumeric() || f.Type == encoding.FieldTypeU4) {
		return ""
	}
	return m
}

// measureLevelAdvisories: PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC for a
// nominal numeric field under a mean-family aggregator (aggregations,
// crosstab cell, crosstab margin aggregations) or a parametric test,
// and PULSE_ADVISORY_ORDINAL_PARAMETRIC for an ordinal one under a
// parametric test — one per (slot, field), in slot order. The levels
// come from the cohort's SPSS metadata sidecar only; with no sidecar
// nothing fires.
func measureLevelAdvisories(req *types.Request, cohort *encoding.Schema, measures map[string]string, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	if len(measures) == 0 {
		return nil
	}
	var out []descriptor.Advisory
	aggSlot := func(slot string, a *types.Aggregation) {
		if a == nil {
			return
		}
		routed := opRoute(inst, a.Type)
		if routed == "" || !slices.Contains(meanFamilyAggregators, routed) {
			return
		}
		if sidecarMeasure(cohort, measures, a.Field) != measureNominal {
			return
		}
		out = append(out, nominalAdvisory(prefix+slot, string(routed), a.Field, types.AGG_FREQUENCY, "counts each code", inst))
	}
	for i, a := range req.Aggregations {
		aggSlot("aggregations["+strconv.Itoa(i)+"]", a)
	}
	if req.Crosstab != nil {
		for i, cell := range req.Crosstab.CellAggregations() {
			aggSlot(req.Crosstab.CellSlotPath(i), cell)
		}
		for i, a := range req.Crosstab.MarginAggregations {
			aggSlot("crosstab.margin_aggregations["+strconv.Itoa(i)+"]", a)
		}
	}
	for _, tier := range []struct {
		key  string
		list []*types.Test
	}{{"tests", req.Tests}, {"post_tests", req.PostTests}} {
		for i, t := range tier.list {
			if t == nil {
				continue
			}
			routed := opRoute(inst, t.Type)
			if routed == "" || !slices.Contains(parametricTests, routed) {
				continue
			}
			slot := prefix + tier.key + "[" + strconv.Itoa(i) + "]"
			for _, field := range []string{t.Field, t.Field2} {
				switch sidecarMeasure(cohort, measures, field) {
				case measureNominal:
					out = append(out, nominalAdvisory(slot, string(routed), field, types.TEST_CHISQ, "compares the mix of codes across groups", inst))
				case measureOrdinal:
					out = append(out, ordinalAdvisory(slot, routed, field, inst))
				}
			}
		}
	}
	return out
}

// nominalAdvisory builds one PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC.
// alt is the count-based replacement, suggested only when the instance
// offers it.
func nominalAdvisory[T ~string](slot, op, field string, alt T, does string, inst *InstanceSnapshot) descriptor.Advisory {
	details := map[string]any{"slot": slot, "operator": op, "field": field, "measure": measureNominal}
	next := "group by the field or count its codes instead"
	if inst.Enabled(string(alt)) {
		details["suggested"] = string(alt)
		next = "consider " + string(alt) + ", which " + does + ", instead"
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC),
		Message: op + " at " + slot + " treats " + field + " as a quantity, but the cohort's SPSS metadata records it as nominal, " +
			"so its codes are labels with no order or distance; " + next + ".",
		Details: details,
	}
}

// ordinalAdvisory builds one PULSE_ADVISORY_ORDINAL_PARAMETRIC. The
// suggestion is the first rank-based alternative in the test's own
// Purpose.NotFor that the instance offers; none leaves it absent.
func ordinalAdvisory(slot string, op types.TestType, field string, inst *InstanceSnapshot) descriptor.Advisory {
	details := map[string]any{"slot": slot, "operator": string(op), "field": field, "measure": measureOrdinal}
	next := "a rank-based test, which reads only the order of the levels, fits it better"
	if p, ok := PurposeOf(string(op)); ok {
		for _, alt := range p.NotFor {
			if slices.Contains(rankBasedTests, alt.Use) && inst.Enabled(alt.Use) {
				details["suggested"] = alt.Use
				next = "consider " + alt.Use + ", a rank-based test that reads only the order of the levels"
				break
			}
		}
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_ORDINAL_PARAMETRIC),
		Message: string(op) + " at " + slot + " treats " + field + " as evenly spaced, but the cohort's SPSS metadata records it as ordinal, " +
			"so its levels are ordered without a known spacing; " + next + ".",
		Details: details,
	}
}

// weightUnusedAdvisory: PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED wraps the
// suggested_weight echo, so it fires exactly when that echo is set — a
// usable sidecar weight, no slot resolving a weight, capability:weighting
// offered — and is silent on everything that silences the echo (no,
// stale or malformed sidecar; an anchor path; PredictBytes).
func weightUnusedAdvisory(sw *descriptor.SuggestedWeight) (descriptor.Advisory, bool) {
	if sw == nil {
		return descriptor.Advisory{}, false
	}
	return descriptor.Advisory{
		Code: string(errors.PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED),
		Message: "The cohort's SPSS metadata records " + sw.Field + " as its weighting variable, but no slot resolves a weight, " +
			"so every row counts once; set weight to " + sw.Field + " on the request to weight by it, or leave it out when unweighted figures are what you want.",
		Details: map[string]any{
			"field":     sw.Field,
			"source":    sw.Source,
			"suggested": map[string]any{"weight": map[string]any{"field": sw.Field}},
		},
	}, true
}

// extraPassAddedScans is the scan count one total-bearing ATTR_FORMULA
// adds: its own pre-pass, which folds every distinct total() argument
// of the formula in that single scan. Two such formulas the planner
// schedules in the same pre-pass layer (or one beside a two-pass
// attribute in its layer) share the scan, so the figure is per formula,
// an upper bound on what the request as a whole adds.
const extraPassAddedScans = 1

// extraPassAdvisories: one PULSE_ADVISORY_EXTRA_PASS per ATTR_FORMULA
// (the instance offers) whose expression calls total(expr), in declared
// order. The predicate is the runtime's (totalcall.Contains, beside the
// attribute route), so the advisory fires exactly when the formula
// takes the two-pass drive. details {slot, operator, label, totals,
// added_scans}: totals is the distinct one-argument total() calls the
// pre-pass sums.
func extraPassAdvisories(req *types.Request, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	var out []descriptor.Advisory
	for i, attr := range req.Attributes {
		if attr == nil || opRoute(inst, attr.Type) != types.ATTR_FORMULA || !totalcall.Contains(attr.Expression) {
			continue
		}
		label := attr.Label
		if label == "" {
			label = string(attr.Type) + "_" + attr.Field
		}
		slot := prefix + "attributes[" + strconv.Itoa(i) + "]"
		totals := len(totalcall.Args(attr.Expression))
		noun := "arguments"
		if totals == 1 {
			noun = "argument"
		}
		out = append(out, descriptor.Advisory{
			Code: string(errors.PULSE_ADVISORY_EXTRA_PASS),
			Message: "ATTR_FORMULA at " + slot + " (" + label + ") calls total() over " + strconv.Itoa(totals) + " " + noun +
				", so the run adds " + strconv.Itoa(extraPassAddedScans) + " pre-pass over the filtered rows to sum them before any row is evaluated; " +
				"a two-pass attribute scheduled in the same pass shares it, and with groupers or features the request runs buffered.",
			Details: map[string]any{
				"slot":        slot,
				"operator":    string(types.ATTR_FORMULA),
				"label":       label,
				"totals":      totals,
				"added_scans": extraPassAddedScans,
			},
		})
	}
	return out
}

// advisoryOrderedOverlayNames is the owner list of
// PULSE_ADVISORY_SERIES_AXIS_NOT_DATE: the partitioned series overlays
// that read their rows in order (seriespartition.Ordered).
func advisoryOrderedOverlayNames() []string {
	var out []string
	for _, k := range seriespartition.Kinds() {
		if seriespartition.Ordered(k) {
			out = append(out, string(k))
		}
	}
	return out
}

// seriesAxisIsDate reports whether grouper g buckets time: a date
// grouper the instance offers, or any grouper over a date or datetime
// column of the schema.
func seriesAxisIsDate(g *types.Group, schema *encoding.Schema, inst *InstanceSnapshot) bool {
	switch opRoute(inst, g.Type) {
	case types.GROUP_DATE, types.GROUP_DATE_RANGES:
		return true
	}
	if schema == nil || g.Field == "" {
		return false
	}
	f := schema.Field(g.Field)
	return f != nil && (f.Type == encoding.FieldTypeDate || f.Type == encoding.FieldTypeDateTime)
}

// seriesAxisAdvisories: one PULSE_ADVISORY_SERIES_AXIS_NOT_DATE per
// series overlay (the instance offers) that reads its rows in order
// (seriespartition.Ordered) on a SERIES host of two or more groupers
// with no params.partition_by, when the last grouper — the implicit
// series axis — is not a date grouper (seriesAxisIsDate). An explicit
// partition_by states the order and never fires; the _TOTAL kinds are
// order-free and never fire. details {slot, operator, series_axis,
// grouper, groups[, date_axis]}: date_axis is the first leading key
// column that does bucket time, when one exists.
func seriesAxisAdvisories(req *types.Request, schema *encoding.Schema, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	n := len(req.Groups)
	if req.Crosstab != nil || n < 2 || len(req.Overlays) == 0 {
		return nil
	}
	last := req.Groups[n-1]
	if last == nil || seriesAxisIsDate(last, schema, inst) {
		return nil
	}
	cols := seriespartition.KeyColumns(req.Groups)
	dateAxis := ""
	for i, g := range req.Groups[:n-1] {
		if g != nil && seriesAxisIsDate(g, schema, inst) {
			dateAxis = cols[i]
			break
		}
	}
	var out []descriptor.Advisory
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		kind := opRoute(inst, spec.Kind)
		if !seriespartition.Ordered(kind) || seriespartition.Explicit(spec) {
			continue
		}
		slot := prefix + "overlays[" + strconv.Itoa(i) + "]"
		details := map[string]any{
			"slot":        slot,
			"operator":    string(kind),
			"series_axis": cols[n-1],
			"grouper":     string(last.Type),
			"groups":      append([]string(nil), cols...),
		}
		next := "if this is the order you mean, set params.partition_by to the leading key columns to state it."
		if dateAxis != "" {
			details["date_axis"] = dateAxis
			next = "put " + dateAxis + " last in groups so the series runs through time within each segment, or, " + next
		} else {
			next = "if " + cols[n-1] + " is the order you mean, set params.partition_by to the leading key columns to state it."
		}
		out = append(out, descriptor.Advisory{
			Code: string(errors.PULSE_ADVISORY_SERIES_AXIS_NOT_DATE),
			Message: string(kind) + " at " + slot + " reads its rows in order and, with no params.partition_by, runs its series down " +
				cols[n-1] + " (" + string(last.Type) + "), the last of " + strconv.Itoa(n) + " groupers, which is not a date grouper; " + next,
			Details: details,
		})
	}
	return out
}

// extensionParamsAdvisories: one PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED
// per slot naming an extension operator whose registration declares no
// params yet carrying a non-empty params object — the keys the params
// allowlist (ParamsRefusal) could not check. Walks the same slots, in
// the same order, as the allowlist.
func extensionParamsAdvisories(req *types.Request, prefix string, inst *InstanceSnapshot) []descriptor.Advisory {
	if inst.Extensions() == nil {
		return nil
	}
	var out []descriptor.Advisory
	c := paramsChecker{inst: inst, undeclared: func(path, op string, keys []string) {
		slot := prefix + path
		out = append(out, descriptor.Advisory{
			Code: string(errors.PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED),
			Message: op + " at " + slot + " is an extension operator whose registration declares no params, so its params keys (" +
				strings.Join(keys, ", ") + ") pass to it unchecked; a misspelled key is not refused. Check them against the operator's documentation.",
			Details: map[string]any{
				"slot":     slot,
				"operator": op,
				"keys":     append([]string(nil), keys...),
			},
		})
	}}
	c.request(req)
	return out
}
