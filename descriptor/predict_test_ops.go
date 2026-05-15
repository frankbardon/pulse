package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// numericTestFields lists the test types that require their `Field`
// reference to point at a numeric (non-categorical, non-geo) column.
// TEST_CHISQ uses rows/cols instead and is not listed here.
// TEST_PROP_Z's primary field is categorical (the outcome) and is
// handled in validateProportionZ below.
var numericTestFields = map[types.TestType]bool{
	types.TEST_T:              true,
	types.TEST_WELCH:          true,
	types.TEST_ANOVA_F:        true,
	types.TEST_KS:             true,
	types.TEST_TREND:          true,
	types.TEST_PAIRED_T:       true,
	types.TEST_PEARSON_R:      true,
	types.TEST_MANN_WHITNEY_U: true,
	types.TEST_WILCOXON_SR:    true,
	types.TEST_KRUSKAL_WALLIS: true,
	types.TEST_SPEARMAN_R:     true,
	types.TEST_KENDALL_TAU:    true,
	types.TEST_ANOVA_WELCH:    true,
	types.TEST_ANOVA_RM:       true,
	types.TEST_BROWN_FORSYTHE: true,
	types.TEST_SHAPIRO_WILK:   true,
}

// numericField2Tests lists the test types that require a numeric Field2
// (paired or bivariate tests).
var numericField2Tests = map[types.TestType]bool{
	types.TEST_PAIRED_T:    true,
	types.TEST_PEARSON_R:   true,
	types.TEST_WILCOXON_SR: true,
	types.TEST_SPEARMAN_R:  true,
	types.TEST_KENDALL_TAU: true,
}

// validateTests checks tier-1 (Tests) and tier-2 (PostTests) test
// specifications against the schema. Tier-1 tests reference schema
// fields directly; tier-2 tests reference the projected post-pipeline
// column set (aggregator labels, attribute labels, window outputs,
// grouper keys) which is supplied via projected.
//
// Tier-1 validation is field-existence + numeric-typing + alpha range.
// Tier-2 validation skips schema-type checks since the column source is
// computed at runtime; alpha range + unknown-type checks still fire.
func validateTests(env *Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
	for _, t := range req.Tests {
		validateOneTest(env, t, schema, projected, true /*tier1*/, opts)
	}
	for _, t := range req.PostTests {
		validateOneTest(env, t, schema, projected, false /*tier1*/, opts)
	}
}

func validateOneTest(env *Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool, opts *PredictOptions) {
	// Unknown type — emitted as an error regardless of strict mode.
	if !isKnownTestType(t.Type) && !isExtensionTestType(opts, t.Type) {
		env.AddError(string(errors.PULSE_TEST_UNKNOWN_TYPE),
			"unknown test type: "+string(t.Type),
			map[string]any{"type": string(t.Type)})
		return
	}
	// Alpha range.
	if t.Alpha != 0 && (t.Alpha <= 0 || t.Alpha >= 1) {
		env.AddError(string(errors.PULSE_TEST_INVALID_ALPHA),
			"alpha must lie in (0, 1)",
			map[string]any{"alpha": t.Alpha, "type": string(t.Type)})
	}
	// Chi-square and Fisher exact both use rows + cols.
	if t.Type == types.TEST_CHISQ || t.Type == types.TEST_FISHER_EXACT {
		validateChiSquareFields(env, t, schema, projected, tier1)
		return
	}
	// TEST_PROP_Z has a unique signature: categorical outcome field +
	// categorical split + required params.success. Route through its own
	// validator and bail.
	if t.Type == types.TEST_PROP_Z {
		validateProportionZ(env, t, schema, projected, tier1)
		return
	}
	// Field requirement.
	if t.Field == "" {
		env.AddError(string(errors.SERVICE_VALIDATION),
			string(t.Type)+" requires field",
			map[string]any{"type": string(t.Type)})
		return
	}
	if tier1 && schema != nil {
		f := schema.Field(t.Field)
		if f == nil && !projected[t.Field] {
			env.AddError(string(errors.SERVICE_VALIDATION),
				string(t.Type)+" references unknown field: "+t.Field,
				map[string]any{"type": string(t.Type), "field": t.Field})
			return
		}
		if f != nil && numericTestFields[t.Type] {
			if f.Type.IsCategorical() || f.Type.IsGeo() {
				env.AddError(string(errors.PULSE_TEST_FIELD_NOT_NUMERIC),
					string(t.Type)+" field "+t.Field+" has non-numeric type "+f.Type.String(),
					map[string]any{"type": string(t.Type), "field": t.Field, "field_type": f.Type.String()})
			}
		}
		if numericField2Tests[t.Type] {
			if t.Field2 == "" {
				env.AddError(string(errors.SERVICE_VALIDATION),
					string(t.Type)+" requires field2 (second numeric column)",
					map[string]any{"type": string(t.Type)})
			} else if f2 := schema.Field(t.Field2); f2 == nil && !projected[t.Field2] {
				env.AddError(string(errors.SERVICE_VALIDATION),
					string(t.Type)+" references unknown field2: "+t.Field2,
					map[string]any{"type": string(t.Type), "field2": t.Field2})
			} else if f2 != nil && (f2.Type.IsCategorical() || f2.Type.IsGeo()) {
				env.AddError(string(errors.PULSE_TEST_FIELD2_NOT_NUMERIC),
					string(t.Type)+" field2 "+t.Field2+" has non-numeric type "+f2.Type.String(),
					map[string]any{"type": string(t.Type), "field2": t.Field2, "field_type": f2.Type.String()})
			}
		}
		if t.SplitBy != "" {
			sf := schema.Field(t.SplitBy)
			if sf == nil && !projected[t.SplitBy] {
				env.AddError(string(errors.SERVICE_VALIDATION),
					string(t.Type)+" split_by references unknown field: "+t.SplitBy,
					map[string]any{"type": string(t.Type), "split_by": t.SplitBy})
			} else if sf != nil && !sf.Type.IsCategorical() {
				env.AddError(string(errors.SERVICE_VALIDATION),
					string(t.Type)+" split_by "+t.SplitBy+" must be categorical, got "+sf.Type.String(),
					map[string]any{"type": string(t.Type), "split_by": t.SplitBy, "field_type": sf.Type.String()})
			}
		}
		if t.Type == types.TEST_ANOVA_RM {
			if t.SubjectField == "" {
				env.AddError(string(errors.SERVICE_VALIDATION),
					"TEST_ANOVA_RM requires subject_field",
					map[string]any{"type": string(t.Type)})
			} else if sf := schema.Field(t.SubjectField); sf == nil && !projected[t.SubjectField] {
				env.AddError(string(errors.SERVICE_VALIDATION),
					"TEST_ANOVA_RM subject_field references unknown field: "+t.SubjectField,
					map[string]any{"type": string(t.Type), "subject_field": t.SubjectField})
			} else if sf != nil && !sf.Type.IsCategorical() {
				env.AddError(string(errors.SERVICE_VALIDATION),
					"TEST_ANOVA_RM subject_field "+t.SubjectField+" must be categorical, got "+sf.Type.String(),
					map[string]any{"type": string(t.Type), "subject_field": t.SubjectField, "field_type": sf.Type.String()})
			}
		}
	}
	_ = opts
}

// validateProportionZ validates TEST_PROP_Z's unique signature: a
// categorical outcome column (Field), a categorical split column
// (SplitBy), and a required Params.success value. Tier-2 variants are
// not implemented for TEST_PROP_Z so the tier1 flag controls only the
// schema-aware checks.
func validateProportionZ(env *Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool) {
	if t.Field == "" {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"TEST_PROP_Z requires field (categorical outcome column)",
			map[string]any{"type": string(t.Type)})
		return
	}
	if t.SplitBy == "" {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"TEST_PROP_Z requires split_by (categorical group column)",
			map[string]any{"type": string(t.Type)})
	}
	if !hasSuccessParam(t.Params) {
		env.AddError(string(errors.PULSE_TEST_SUCCESS_VALUE_MISSING),
			"TEST_PROP_Z requires params.success (the dictionary value treated as a success)",
			map[string]any{"type": string(t.Type)})
	}
	if !tier1 || schema == nil {
		return
	}
	for axis, name := range map[string]string{"field": t.Field, "split_by": t.SplitBy} {
		if name == "" {
			continue
		}
		f := schema.Field(name)
		if f == nil && !projected[name] {
			env.AddError(string(errors.SERVICE_VALIDATION),
				"TEST_PROP_Z "+axis+" references unknown field: "+name,
				map[string]any{"axis": axis, "field": name})
			continue
		}
		if f != nil && !f.Type.IsCategorical() {
			env.AddError(string(errors.PULSE_TEST_FIELD_NOT_NUMERIC),
				"TEST_PROP_Z "+axis+" field "+name+" must be categorical, got "+f.Type.String(),
				map[string]any{"axis": axis, "field": name, "field_type": f.Type.String()})
		}
	}
}

// hasSuccessParam reports whether the raw Params blob carries a
// non-empty "success" value. Predict tolerates missing or malformed
// params (the registry factory surfaces a parse error at process
// time), but a *missing* success value is the canonical failure mode
// callers hit so it is worth catching up front.
func hasSuccessParam(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var params struct {
		Success string `json:"success"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	return params.Success != ""
}

func validateChiSquareFields(env *Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool) {
	if t.Rows == "" || t.Cols == "" {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"TEST_CHISQ requires rows and cols",
			map[string]any{"rows": t.Rows, "cols": t.Cols})
		return
	}
	if !tier1 || schema == nil {
		return
	}
	for axis, field := range map[string]string{"rows": t.Rows, "cols": t.Cols} {
		f := schema.Field(field)
		if f == nil && !projected[field] {
			env.AddError(string(errors.SERVICE_VALIDATION),
				"TEST_CHISQ "+axis+" references unknown field: "+field,
				map[string]any{"axis": axis, "field": field})
			continue
		}
		if f != nil && !f.Type.IsCategorical() {
			env.AddError(string(errors.PULSE_TEST_FIELD_NOT_NUMERIC),
				"TEST_CHISQ "+axis+" field "+field+" must be categorical, got "+f.Type.String(),
				map[string]any{"axis": axis, "field": field, "field_type": f.Type.String()})
		}
	}
}

// isKnownTestType reports whether the type is in the registered enum set.
// Predict does not import processing so it cannot consult the runtime
// registry directly; the enum check is the canonical "known" gate.
func isKnownTestType(t types.TestType) bool {
	for _, known := range types.AllTestTypes() {
		if t == known {
			return true
		}
	}
	return false
}

// streamableTestReasons reports the streamability gates triggered by
// req.Tests (tier-1). Tier-2 tests never affect streamability — they
// run on the materialized result row set after windows.
func streamableTestReasons(req *types.Request) []string {
	var reasons []string
	for _, t := range req.Tests {
		if !t.Type.Streamable() {
			reasons = append(reasons, "test "+string(t.Type)+" is not streamable")
		}
	}
	if len(req.Tests) > 0 {
		if len(req.Groups) > 0 {
			reasons = append(reasons, "tier-1 tests do not yet compose with groupers in the streaming path")
		}
		if len(req.Features) > 0 {
			reasons = append(reasons, "tier-1 tests do not yet compose with features in the streaming path")
		}
		for _, attr := range req.Attributes {
			if attr.Type == types.ATTR_ZSCORE || attr.Type == types.ATTR_TSCORE || attr.Type == types.ATTR_NORMALIZED {
				reasons = append(reasons, "tier-1 tests do not yet compose with two-pass attribute "+string(attr.Type))
				break
			}
		}
	}
	return reasons
}
