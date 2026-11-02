// Package testopts reads the methodological options of the two-group
// and contingency tests (U64 #644), each an opt-in whose absence is the
// test's historical behaviour, byte for byte:
//
//   - TEST_T params.variance: "unequal" (default, Welch) or "pooled"
//     (Student, R's t.test(var.equal = TRUE)); two-sample only.
//   - TEST_MANN_WHITNEY_U params.exact: false (default, the normal
//     approximation) or true (the exact permutation distribution of the
//     rank sum, ties included, R 4.6's wilcox.test(exact = TRUE)).
//   - TEST_CHISQ params.correct: false (default) or true (Yates'
//     continuity correction on a 2 × 2 independence table, R's
//     chisq.test(correct = TRUE); inert on any other table).
//   - TEST_PROP_Z params.interval: "wald" (default) or "wilson" (the
//     one-proportion score interval of prop.test), and params.correct:
//     false (default) or true (prop.test's continuity correction on the
//     statistic and the interval).
//
// Predict (internal/descriptor) and the runtime constructors
// (internal/processing) both read the options here, so what one
// refuses the other refuses with the same code, message and details.
// The values are probed (paramread.Probe): the constructors' own params
// structs record the keys as read.
//
// Leaf: types, errors and paramread only. No-execute.
package testopts

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Params keys.
const (
	VarianceKey = "variance"
	ExactKey    = "exact"
	CorrectKey  = "correct"
	IntervalKey = "interval"
)

// TEST_T params.variance values.
const (
	VarianceUnequal = "unequal"
	VariancePooled  = "pooled"
)

// TEST_PROP_Z params.interval values.
const (
	IntervalWald   = "wald"
	IntervalWilson = "wilson"
)

// Options are a test's decoded options; the zero value is every
// default.
type Options struct {
	// Pooled is TEST_T params.variance "pooled".
	Pooled bool
	// Exact is TEST_MANN_WHITNEY_U params.exact.
	Exact bool
	// Correct is TEST_CHISQ / TEST_PROP_Z params.correct.
	Correct bool
	// Wilson is TEST_PROP_Z params.interval "wilson".
	Wilson bool
}

// Takes reports whether op reads any option.
func Takes(op types.TestType) bool {
	switch op {
	case types.TEST_T, types.TEST_MANN_WHITNEY_U, types.TEST_CHISQ, types.TEST_PROP_Z:
		return true
	}
	return false
}

// Parse reads t's options and judges each against the test's shape (a
// pooled variance needs split_by, a Wilson interval is one-proportion,
// a continuity correction is not read by the covariate-adjusted
// proportion test). Every refusal is PROCESSING_CONFIG with details
// {type, param, value?, valid?, reason?}. A test that reads no option
// returns the zero Options.
func Parse(t *types.Test) (Options, *errors.CodedError) {
	var out Options
	if t == nil || !Takes(t.Type) {
		return out, nil
	}
	raw := bytes.TrimSpace(t.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	var in map[string]json.RawMessage
	if paramread.Probe(raw, &in) != nil {
		// A params blob that is not an object is the constructor's own
		// decode refusal.
		return out, nil
	}
	op := t.Type
	switch op {
	case types.TEST_T:
		v, ok, cerr := enumParam(op, in, VarianceKey, []string{VarianceUnequal, VariancePooled})
		if cerr != nil {
			return out, cerr
		}
		out.Pooled = ok && v == VariancePooled
		if out.Pooled && t.SplitBy == "" {
			return out, refusal(op, VarianceKey, v, "a pooled variance compares two groups: it needs split_by (the one-sample test reads one variance)", "needs_split_by")
		}
	case types.TEST_MANN_WHITNEY_U:
		b, _, cerr := boolParam(op, in, ExactKey)
		if cerr != nil {
			return out, cerr
		}
		out.Exact = b
	case types.TEST_CHISQ:
		b, _, cerr := boolParam(op, in, CorrectKey)
		if cerr != nil {
			return out, cerr
		}
		out.Correct = b
	case types.TEST_PROP_Z:
		v, ok, cerr := enumParam(op, in, IntervalKey, []string{IntervalWald, IntervalWilson})
		if cerr != nil {
			return out, cerr
		}
		out.Wilson = ok && v == IntervalWilson
		b, _, cerr := boolParam(op, in, CorrectKey)
		if cerr != nil {
			return out, cerr
		}
		out.Correct = b
		_, hasP0 := in["p0"]
		if out.Wilson && !hasP0 {
			return out, refusal(op, IntervalKey, v, "the Wilson interval is the one-proportion form's (params.p0); the two-proportion interval is Wald", "needs_p0")
		}
		if out.Correct && hasCovariate(in) {
			return out, refusal(op, CorrectKey, true, "the covariate-adjusted difference (params.covariate) is a Wald test on the adjusted indicator, which takes no continuity correction", "covariate")
		}
	}
	return out, nil
}

// hasCovariate reports a non-empty params.covariate.
func hasCovariate(in map[string]json.RawMessage) bool {
	raw, ok := in["covariate"]
	if !ok {
		return false
	}
	var s string
	return json.Unmarshal(raw, &s) == nil && s != ""
}

// enumParam reads key as one of valid; ok is false when absent.
func enumParam(op types.TestType, in map[string]json.RawMessage, key string, valid []string) (string, bool, *errors.CodedError) {
	raw, present := in[key]
	if !present {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		for _, v := range valid {
			if s == v {
				return s, true, nil
			}
		}
	}
	return "", false, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(op)+" params."+key+" must be one of "+strings.Join(valid, ", ")+", got "+string(raw),
		map[string]any{"type": string(op), "param": key, "value": string(raw), "valid": valid})
}

// boolParam reads key as a JSON boolean; ok is false when absent.
func boolParam(op types.TestType, in map[string]json.RawMessage, key string) (bool, bool, *errors.CodedError) {
	raw, present := in[key]
	if !present {
		return false, false, nil
	}
	var b *bool
	if err := json.Unmarshal(raw, &b); err != nil || b == nil {
		return false, false, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			string(op)+" params."+key+" must be true or false, got "+string(raw),
			map[string]any{"type": string(op), "param": key, "value": string(raw)})
	}
	return *b, true, nil
}

func refusal(op types.TestType, key string, value any, why, reason string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		string(op)+" params."+key+": "+why,
		map[string]any{"type": string(op), "param": key, "value": value, "reason": reason})
}
