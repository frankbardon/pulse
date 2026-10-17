package descriptor

import (
	"encoding/json"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

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
func validateTests(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, projected map[string]bool, opts *PredictOptions) {
	for _, t := range req.Tests {
		validateOneTest(env, t, schema, projected, true /*tier1*/, opts)
	}
	// A grouped request runs every tier-1 test once per group: a test
	// splitting on a grouper's field is refused — the runtime's refusal
	// (groupedtests.SplitRefusal), code, message and Details alike.
	if err := groupedtests.SplitRefusal(req.Tests, req.Groups); err != nil {
		env.AddError(string(err.Code), err.Message, err.Details)
	}
	for _, t := range req.PostTests {
		validateOneTest(env, t, schema, projected, false /*tier1*/, opts)
	}
	// A grouped request runs every post-test once per partition (every
	// key but the last by default): a partition_by off the prefix rule,
	// post-tests at mixed depths, or a split on a partition key column is
	// refused — the runtime's refusal (groupedtests.PostTestDepth).
	if _, err := groupedtests.PostTestDepth(req.PostTests, req.Groups); err != nil {
		env.AddError(string(err.Code), err.Message, err.Details)
	}
}

func validateOneTest(env *descriptor.Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool, opts *PredictOptions) {
	// Unknown type — emitted as an error regardless of strict mode. A
	// type the instance hides is unknown exactly as a never-registered
	// one.
	if !isKnownTestType(opRoute(opts.instance(), t.Type)) && !isExtensionTestType(opts, t.Type) {
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
	// A vector-input test reads vector / fields — its shape, member
	// types and member-count params are the field-reference rule's
	// (FieldRefRefusals step 8) — plus an optional categorical split_by.
	if vectors.IsVectorTest(opRoute(opts.instance(), t.Type)) {
		validateVectorTest(env, t, schema, tier1)
		return
	}
	// A registered extension test reading a vector input (its members
	// judged at FieldRefRefusals step 8) may name no field.
	if t.Field == "" && tier1 && vectors.HasVectorInput(t) && isExtensionTestType(opts, t.Type) {
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
		// Unknown names are FieldRefRefusals' refusals; only schema
		// fields are type-checked here.
		f := schema.Field(t.Field)
		if f == nil && !projected[t.Field] {
			return
		}
		// The accepted-types table's refusals (field, then field2),
		// the runtime constructors' (testFieldTypeRefusal) word for
		// word: a categorical's index and a set's bitmask are refused.
		if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotField, t.Field, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
		if numericField2Tests[t.Type] {
			if t.Field2 == "" {
				env.AddError(string(errors.SERVICE_VALIDATION),
					string(t.Type)+" requires field2 (second numeric column)",
					map[string]any{"type": string(t.Type)})
			} else if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotField2, t.Field2, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			}
		}
		// The accepted-types table's split_by refusal (a test that
		// reads no split_by has no row, and its split_by is not judged).
		if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotSplitBy, t.SplitBy, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
		if t.Type == types.TEST_ANOVA_RM {
			if t.SubjectField == "" {
				env.AddError(string(errors.SERVICE_VALIDATION),
					"TEST_ANOVA_RM requires subject_field",
					map[string]any{"type": string(t.Type)})
			} else if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotSubject, t.SubjectField, schema); cerr != nil {
				env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			}
		}
	}
	_ = opts
}

// validateProportionZ validates TEST_PROP_Z's unique signature: a
// categorical outcome column (Field), a categorical split column
// (SplitBy), and a required Params.success value. A missing field or
// split_by is PROCESSING_CONFIG, the runtime constructor's code (and
// every sibling test's missing-split_by refusal), message for message. Tier-2 variants are
// not implemented for TEST_PROP_Z so the tier1 flag controls only the
// schema-aware checks.
func validateProportionZ(env *descriptor.Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool) {
	if t.Field == "" {
		env.AddError(string(errors.PROCESSING_CONFIG),
			"TEST_PROP_Z requires field (categorical outcome column)",
			map[string]any{"type": string(t.Type)})
		return
	}
	// params.p0 selects the one-proportion test, which reads no
	// split_by (the runtime constructor's rule, P0Refusal below).
	p0, hasP0 := p0Param(t.Params)
	if t.SplitBy == "" && !hasP0 {
		env.AddError(string(errors.PROCESSING_CONFIG),
			"TEST_PROP_Z requires split_by (categorical group column)",
			map[string]any{"type": string(t.Type)})
	}
	if !hasSuccessParam(t.Params) {
		env.AddError(string(errors.PULSE_TEST_SUCCESS_VALUE_MISSING),
			"TEST_PROP_Z requires params.success (the dictionary value treated as a success)",
			map[string]any{"type": string(t.Type)})
	} else if hasP0 {
		if cerr := fieldaccept.P0Refusal(string(t.Type), t.SplitBy, p0); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	}
	if !tier1 || schema == nil {
		return
	}
	// An unknown name is FieldRefRefusals' refusal; a non-categorical
	// outcome or split the accepted-types table's, field then split_by
	// (the runtime constructor's order).
	if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotField, t.Field, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		return
	}
	if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotSplitBy, t.SplitBy, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		return
	}
	// A packed_bool outcome's success is "0" / "1" only — the runtime
	// constructor's next refusal, the same coded error.
	if success, ok := successParam(t.Params); ok {
		if cerr := fieldaccept.SuccessLevelRefusal(string(t.Type), t.Field, success, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	}
}

// hasSuccessParam reports whether the raw Params blob carries a
// non-empty "success" value. Predict tolerates missing or malformed
// params (the registry factory surfaces a parse error at process
// time), but a *missing* success value is the canonical failure mode
// callers hit so it is worth catching up front.
func hasSuccessParam(raw []byte) bool {
	_, ok := successParam(raw)
	return ok
}

// p0Param returns the raw Params blob's "p0" value; false when params
// are absent, malformed or carry none (null included) — the runtime
// constructor's reading.
func p0Param(raw []byte) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var params struct {
		P0 *float64 `json:"p0"`
	}
	if paramread.Probe(raw, &params) != nil || params.P0 == nil {
		return 0, false
	}
	return *params.P0, true
}

// expectedParam returns the raw Params blob's "expected" map (TEST_CHISQ
// goodness of fit); false when params are absent, malformed or carry
// none (null included) — the runtime constructor's reading.
func expectedParam(raw []byte) (map[string]float64, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var params struct {
		Expected map[string]float64 `json:"expected"`
	}
	if paramread.Probe(raw, &params) != nil || params.Expected == nil {
		return nil, false
	}
	return params.Expected, true
}

// successParam returns the raw Params blob's non-empty "success" value;
// false when params are absent, malformed or carry none.
func successParam(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var params struct {
		Success string `json:"success"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return "", false
	}
	return params.Success, params.Success != ""
}

func validateChiSquareFields(env *descriptor.Envelope, t *types.Test, schema *encoding.Schema, projected map[string]bool, tier1 bool) {
	// params.expected selects TEST_CHISQ's goodness-of-fit form: rows
	// alone, then the shared params rule (the runtime constructor's
	// order — rows, alpha, ExpectedRefusal, the rows axis type).
	if expected, ok := expectedParam(t.Params); ok && t.Type == types.TEST_CHISQ {
		if t.Rows == "" {
			env.AddError(string(errors.SERVICE_VALIDATION),
				"TEST_CHISQ goodness of fit (params.expected) requires rows",
				map[string]any{"rows": t.Rows})
			return
		}
		var s *encoding.Schema
		if tier1 {
			s = schema
		}
		if cerr := fieldaccept.ExpectedRefusal(string(t.Type), t.Rows, t.Cols, expected, s); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			return
		}
		if !tier1 || schema == nil {
			return
		}
		if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotRows, t.Rows, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
		return
	}
	if t.Rows == "" || t.Cols == "" {
		env.AddError(string(errors.SERVICE_VALIDATION),
			"TEST_CHISQ requires rows and cols",
			map[string]any{"rows": t.Rows, "cols": t.Cols})
		return
	}
	if !tier1 || schema == nil {
		return
	}
	// An unknown name is FieldRefRefusals' refusal; a non-categorical
	// axis the accepted-types table's, rows then cols (the runtime
	// constructor's contingencyAxisRefusal, which stops at the first).
	if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotRows, t.Rows, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		return
	}
	if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotCols, t.Cols, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
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
func streamableTestReasons(req *types.Request, schema *encoding.Schema, opts *PredictOptions) []string {
	var reasons []string
	for _, t := range req.Tests {
		if !streamableWithOverlay(opts, "test", string(t.Type), opRoute(opts.instance(), t.Type).Streamable()) {
			reasons = append(reasons, "test "+string(t.Type)+" is not streamable")
		}
	}
	if len(req.Tests) > 0 {
		if len(req.Groups) > 0 {
			reasons = append(reasons, "grouped tier-1 tests run once per group via the buffered path")
		}
		if len(req.Features) > 0 {
			reasons = append(reasons, "tier-1 tests do not yet compose with features in the streaming path")
		}
		if tp := firstTwoPassAttribute(req, schema, opts); tp != "" {
			reasons = append(reasons, "tier-1 tests do not yet compose with two-pass attribute "+string(tp))
		}
	}
	return reasons
}

// validateVectorTest is a vector-input test's predict arm: a
// categorical split_by. A post-test slot is not judged here: no vector
// test runs as one.
func validateVectorTest(env *descriptor.Envelope, t *types.Test, schema *encoding.Schema, tier1 bool) {
	if !tier1 || schema == nil || t.SplitBy == "" {
		return
	}
	if cerr := fieldaccept.Check(string(t.Type), fieldaccept.SlotSplitBy, t.SplitBy, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
	}
}
