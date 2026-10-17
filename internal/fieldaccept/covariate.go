package fieldaccept

import (
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// CovariateKey is the params key of the mean tests' covariate
// adjustment (CUPED, U48).
const CovariateKey = "covariate"

// The Details["reason"] values of PULSE_TEST_COVARIATE_INVALID.
const (
	CovariateReasonUnsupported    = "unsupported_test"
	CovariateReasonWithoutSplitBy = "without_split_by"
	CovariateReasonNotNumeric     = "not_numeric"
	CovariateReasonIsField        = "is_field"
)

// covariateTests are the tests that read params.covariate: the
// two-sample mean tests, whose difference of means the covariate
// adjusts (CUPED). Every other test — and every post-test — refuses it.
var covariateTests = map[types.TestType]bool{
	types.TEST_T:            true,
	types.TEST_WELCH:        true,
	types.TEST_Z_TWO_SAMPLE: true,
}

// TakesCovariate reports whether the tier-1 test type op reads
// params.covariate.
func TakesCovariate(op types.TestType) bool { return covariateTests[op] }

// CovariateTests returns the test types that read params.covariate,
// sorted.
func CovariateTests() []string {
	out := make([]string, 0, len(covariateTests))
	for t := range covariateTests {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

// CovariateUnsupportedRefusal is the refusal of params.covariate on a
// test that does not read it (op at slot path, e.g. "tests[1]" or
// "post_tests[0]"): the params allowlist raises it in place of the
// generic unknown-key refusal, on predict and every runtime funnel
// alike, so a covariate is never silently ignored.
func CovariateUnsupportedRefusal(path, op string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_COVARIATE_INVALID,
		path+": "+op+" takes no params.covariate; the covariate adjustment (CUPED) runs on the two-sample mean tests only (TEST_T with split_by, TEST_WELCH, TEST_Z_TWO_SAMPLE)",
		map[string]any{"type": op, "reason": CovariateReasonUnsupported, "path": path + ".params." + CovariateKey, "supported_tests": CovariateTests()})
}

// CovariateParam returns the raw Params blob's "covariate" value; ""
// when params are absent, malformed or carry none (a lenient probe —
// the test's constructor decodes and records it).
func CovariateParam(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var p struct {
		Covariate string `json:"covariate"`
	}
	if paramread.Probe(raw, &p) != nil {
		return ""
	}
	return p.Covariate
}

// CovariateRefusal is the covariate-taking tests' params.covariate
// rule: an empty covariate is absent (no adjustment); a set one needs
// split_by (the adjustment compares two groups), must not be the
// test's own field, and on a schema field must be numeric (the tier-1
// test value set: no categorical index, no set bitmask). An unknown
// name is the field-reference walk's refusal and a vector the vector
// rule's, both earlier; a derived column is not type-judged. Predict
// and the runtime constructors both raise it
// (PULSE_TEST_COVARIATE_INVALID), after params.reference. Nil when
// usable.
func CovariateRefusal(op, field, splitBy, covariate string, schema *encoding.Schema) *errors.CodedError {
	if covariate == "" {
		return nil
	}
	refuse := func(reason, msg string, extra map[string]any) *errors.CodedError {
		details := map[string]any{"type": op, "reason": reason, "covariate": covariate}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_COVARIATE_INVALID, msg, details)
	}
	if splitBy == "" {
		return refuse(CovariateReasonWithoutSplitBy,
			op+" params.covariate adjusts the difference between two split_by groups; it needs split_by (drop covariate for the one-sample form)", nil)
	}
	if covariate == field {
		return refuse(CovariateReasonIsField,
			op+" params.covariate "+covariate+" is the test's own field; name a pre-period column measured before assignment", map[string]any{"field": field})
	}
	if schema == nil {
		return nil
	}
	f := schema.Field(covariate)
	if f == nil || f.Type.IsVector() || testValue(f.Type) {
		return nil
	}
	return refuse(CovariateReasonNotNumeric,
		op+" params.covariate "+covariate+" has type "+f.Type.String()+"; it must be numeric (a categorical's index and a set's bitmask are not values)",
		map[string]any{"field_type": f.Type.String()})
}
