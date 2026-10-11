package vectors

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Vector-input tests — the types.Test Vector / Fields (and Vector2 /
// Fields2) slots, resolved by the one leaf predict, the runtime and
// projection all call (the MatrixSpec.Vector / Fields mirror).
//
// Rules (the contract), per tier-1 test slot "tests[i]":
//
//   - A built-in vector-input type (VectorTestTypes) reads exactly one
//     of vector and fields; field / field2 are refused (it reads no
//     scalar column), and so are both or neither of vector and fields.
//   - vector2 / fields2 (at most one) are the paired second member
//     set: accepted only by a type that reads one (SecondVectorTypes),
//     and only resolving to the first set's member count p.
//   - A vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`); inline fields resolve by the vector
//     rules (ResolveSpec at "tests[i].fields").
//   - Missing data is listwise only: params.missing "listwise" is
//     accepted, "pairwise" (the matrix slot's other mode) and anything
//     else refused.
//   - An operator's params that depend on the member count or the form
//     are judged here too (TEST_HOTELLING_T2: HotellingParams — mu's
//     length, variance "unequal" only on the two-sample form), so
//     predict and the runtime refuse them at the same point.
//   - A paired form (a second set) is never split: vector2 / fields2
//     with split_by is refused.
//   - Any OTHER built-in test — and every post-test — refuses a vector
//     input. An extension test's slots are its own business and are not
//     judged.
//
// Shape refusals are SERVICE_VALIDATION naming `test` (the slot path)
// and `reason`.

// VectorTestTypes returns the built-in test types that read their
// columns from Vector / Fields, in AllTestTypes order.
func VectorTestTypes() []types.TestType {
	return []types.TestType{types.TEST_HOTELLING_T2}
}

// secondVectorTypes are the built-in test types that also read a
// second member set: TEST_HOTELLING_T2's paired form (the difference
// vector). A var so the pairing rules stay testable on their own.
var secondVectorTypes = []types.TestType{types.TEST_HOTELLING_T2}

// SecondVectorTypes returns the built-in test types that also read a
// second member set from Vector2 / Fields2.
func SecondVectorTypes() []types.TestType { return append([]types.TestType(nil), secondVectorTypes...) }

// IsVectorTest reports whether t is a built-in vector-input test type.
func IsVectorTest(t types.TestType) bool { return slices.Contains(VectorTestTypes(), t) }

// TakesSecondVector reports whether t reads a second member set.
func TakesSecondVector(t types.TestType) bool { return slices.Contains(SecondVectorTypes(), t) }

// HasVectorInput reports whether t sets any vector-input slot.
func HasVectorInput(t *types.Test) bool {
	return t != nil && (t.Vector != "" || len(t.Fields) > 0 || t.Vector2 != "" || len(t.Fields2) > 0)
}

// TestInput is one test slot's vector input after resolution. The zero
// value (Has false) is a test that reads no vector.
type TestInput struct {
	// Members is the first member set (axis order).
	Members Resolved
	// Members2 is the second member set; HasSecond reports it is set.
	Members2  Resolved
	HasSecond bool
}

// Has reports whether the slot resolved a vector input.
func (in TestInput) Has() bool { return len(in.Members.Members) > 0 }

// P is the member count of the first set.
func (in TestInput) P() int { return len(in.Members.Members) }

// ResolveTests resolves every tier-1 test's vector input against
// schema (Request.Vectors first, like ResolveMatrices) and judges every
// post-test's. route maps a test type to the built-in it executes as —
// the instance's view, where a hidden type routes to an unknown name;
// nil is the identity. The result is index-aligned with req.Tests (the
// zero TestInput for a test that reads no vector); the first refusal is
// returned. Like Resolve it reads only the schema.
func ResolveTests(req *types.Request, schema *encoding.Schema, route func(types.TestType) types.TestType) ([]TestInput, *errors.CodedError) {
	if req == nil || (len(req.Tests) == 0 && len(req.PostTests) == 0) {
		return nil, nil
	}
	if route == nil {
		route = func(t types.TestType) types.TestType { return t }
	}
	resolved, verr := Resolve(req.Vectors, schema)
	if verr != nil {
		return nil, verr
	}
	var out []TestInput
	if len(req.Tests) > 0 {
		out = make([]TestInput, len(req.Tests))
	}
	for i, t := range req.Tests {
		if t == nil {
			continue
		}
		in, err := resolveTest(testPath(i), t, route(t.Type), req.Vectors, resolved, schema)
		if err != nil {
			return nil, err
		}
		out[i] = in
	}
	for i, t := range req.PostTests {
		if HasVectorInput(t) {
			at := "post_tests[" + strconv.Itoa(i) + "]"
			return nil, testInvalid(at, "vector_not_accepted",
				at+" ("+string(t.Type)+") is a post-test, which reads result-row columns, never a vector; drop vector / fields", nil)
		}
	}
	return out, nil
}

// resolveTest resolves one tier-1 slot whose built-in route is typ.
func resolveTest(at string, t *types.Test, typ types.TestType, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema) (TestInput, *errors.CodedError) {
	if !IsVectorTest(typ) {
		if HasVectorInput(t) && slices.Contains(types.AllTestTypes(), typ) {
			return TestInput{}, testInvalid(at, "vector_not_accepted",
				at+" ("+string(t.Type)+") reads field columns, not a vector; drop vector / fields / vector2 / fields2",
				map[string]any{"type": string(t.Type)})
		}
		return TestInput{}, nil
	}
	name := string(t.Type)
	if t.Field != "" || t.Field2 != "" {
		return TestInput{}, testInvalid(at, "field_on_vector_test",
			at+" ("+name+") reads its columns from vector or fields, not field / field2",
			map[string]any{"type": name})
	}
	switch {
	case t.Vector != "" && len(t.Fields) > 0:
		return TestInput{}, testInvalid(at, "vector_and_fields", at+" sets both vector and fields; set exactly one", nil)
	case t.Vector == "" && len(t.Fields) == 0:
		return TestInput{}, testInvalid(at, "no_vector_or_fields", at+" ("+name+") sets neither vector nor fields; set exactly one", nil)
	}
	hasSecond := t.Vector2 != "" || len(t.Fields2) > 0
	if t.Vector2 != "" && len(t.Fields2) > 0 {
		return TestInput{}, testInvalid(at, "vector2_and_fields2", at+" sets both vector2 and fields2; set at most one", nil)
	}
	if hasSecond && !TakesSecondVector(typ) {
		return TestInput{}, testInvalid(at, "vector2_not_accepted",
			at+" ("+name+") reads one member set; drop vector2 / fields2", map[string]any{"type": name})
	}
	if hasSecond && t.SplitBy != "" {
		return TestInput{}, testInvalid(at, "paired_with_split_by",
			at+" ("+name+") sets vector2 / fields2 (the paired form, one sample of row-wise differences) and split_by; drop one",
			map[string]any{"type": name, "split_by": t.SplitBy})
	}
	if err := checkTestMissing(at, t.Params); err != nil {
		return TestInput{}, err
	}
	first, err := resolveTestSide(at, "vector", "fields", t.Vector, t.Fields, specs, resolved, schema)
	if err != nil {
		return TestInput{}, err
	}
	if typ == types.TEST_HOTELLING_T2 {
		if _, err := HotellingParams(at, t.Params, len(first.Members), HotellingFormOf(t)); err != nil {
			return TestInput{}, err
		}
	}
	in := TestInput{Members: first}
	if hasSecond {
		second, err := resolveTestSide(at, "vector2", "fields2", t.Vector2, t.Fields2, specs, resolved, schema)
		if err != nil {
			return TestInput{}, err
		}
		if len(second.Members) != len(first.Members) {
			return TestInput{}, testInvalid(at, "vector2_length_mismatch",
				at+" pairs "+strconv.Itoa(len(first.Members))+" members with "+strconv.Itoa(len(second.Members))+"; both sets need the same count",
				map[string]any{"p": len(first.Members), "p2": len(second.Members)})
		}
		in.Members2, in.HasSecond = second, true
	}
	return in, nil
}

// resolveTestSide resolves one member set: a named vector or inline
// fields.
func resolveTestSide(at, vecKey, fieldsKey, vector string, fields []string, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema) (Resolved, *errors.CodedError) {
	if vector == "" {
		return ResolveSpec(at+"."+fieldsKey, types.VectorSpec{Fields: fields}, schema)
	}
	r, ok := Find(resolved, vector)
	if !ok {
		defined := make([]string, 0, len(specs))
		for _, v := range specs {
			defined = append(defined, v.Name)
		}
		return Resolved{}, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_UNKNOWN,
			at+" names vector "+strconv.Quote(vector)+", which no vectors entry defines",
			map[string]any{"vector": vector, "slot": at + "." + vecKey, "defined": defined})
	}
	return r, nil
}

// checkTestMissing applies a vector-input test's params.missing:
// "listwise" (the only mode) or absent. Other keys are the operator's.
func checkTestMissing(at string, raw json.RawMessage) *errors.CodedError {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var p struct {
		Missing *string `json:"missing"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return testInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
	}
	if p.Missing == nil || *p.Missing == MissingListwise {
		return nil
	}
	if *p.Missing == MissingPairwise {
		return testInvalid(at, "pairwise_unsupported",
			at+" params.missing \"pairwise\" is not supported on a test: its statistic needs one complete-case sample; use \"listwise\"",
			map[string]any{"param": "missing", "value": *p.Missing, "valid": []string{MissingListwise}})
	}
	return testInvalid(at, "bad_params", at+" params.missing must be \"listwise\"",
		map[string]any{"param": "missing", "value": *p.Missing, "valid": []string{MissingListwise}})
}

// TEST_HOTELLING_T2 params.variance values.
const (
	// HotellingVariancePooled is the default: one covariance shared by
	// both samples (exact F).
	HotellingVariancePooled = "pooled"
	// HotellingVarianceUnequal is the Krishnamoorthy–Yu modified Nel–Van
	// der Merwe approximation (two-sample only; fractional df2).
	HotellingVarianceUnequal = "unequal"
)

// HotellingForm is a TEST_HOTELLING_T2 slot's form, read off its spec.
type HotellingForm string

// TEST_HOTELLING_T2 forms (TestResult.Variant prefixes).
const (
	HotellingOneSample HotellingForm = "one_sample"
	HotellingTwoSample HotellingForm = "two_sample"
	HotellingPaired    HotellingForm = "paired"
)

// HotellingFormOf reads t's form: paired when a second set is present,
// two-sample when split_by is set, one-sample otherwise. (The resolver
// refuses a second set with split_by before params are judged.)
func HotellingFormOf(t *types.Test) HotellingForm {
	switch {
	case t.Vector2 != "" || len(t.Fields2) > 0:
		return HotellingPaired
	case t.SplitBy != "":
		return HotellingTwoSample
	}
	return HotellingOneSample
}

// HotellingConfig is TEST_HOTELLING_T2's decoded params.
type HotellingConfig struct {
	// Mu is the hypothesized mean (one-sample) or mean difference
	// (paired) vector, length p (default zeros); nil on the two-sample
	// form, which tests equal means.
	Mu []float64
	// Variance is HotellingVariancePooled (default) or
	// HotellingVarianceUnequal (two-sample only).
	Variance string
}

// HotellingParams decodes TEST_HOTELLING_T2's params against its member
// count p and form: params.mu, a length-p array (default zeros; judged
// but ignored on the two-sample form), params.variance ("pooled" default, "unequal"
// refused off the two-sample form) and params.missing
// (checkTestMissing's rule). Unknown keys are refused. at is the slot
// path. Predict and the runtime factory both call it.
func HotellingParams(at string, raw json.RawMessage, p int, form HotellingForm) (HotellingConfig, *errors.CodedError) {
	cfg := HotellingConfig{Mu: make([]float64, p), Variance: HotellingVariancePooled}
	if form == HotellingTwoSample {
		cfg.Mu = nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return cfg, nil
	}
	var params struct {
		Mu       []float64 `json:"mu"`
		Variance *string   `json:"variance"`
		Missing  *string   `json:"missing"`
	}
	if err := decodeStrict(raw, &params); err != nil {
		return HotellingConfig{}, testInvalid(at, "bad_params", at+" TEST_HOTELLING_T2 params: "+err.Error()+" (keys: mu, variance, missing)", nil)
	}
	if err := checkTestMissing(at, raw); err != nil {
		return HotellingConfig{}, err
	}
	if params.Variance != nil {
		switch v := *params.Variance; v {
		case HotellingVariancePooled:
		case HotellingVarianceUnequal:
			if form != HotellingTwoSample {
				return HotellingConfig{}, testInvalid(at, "variance_unequal_not_two_sample",
					at+" params.variance \"unequal\" compares two samples' covariances and needs the two-sample form (split_by); the "+string(form)+" form has one covariance — drop variance",
					map[string]any{"param": "variance", "value": v, "form": string(form)})
			}
			cfg.Variance = v
		default:
			return HotellingConfig{}, testInvalid(at, "bad_params", at+" params.variance must be \"pooled\" or \"unequal\"",
				map[string]any{"param": "variance", "value": v, "valid": []string{HotellingVariancePooled, HotellingVarianceUnequal}})
		}
	}
	if params.Mu == nil {
		return cfg, nil
	}
	if len(params.Mu) != p {
		return HotellingConfig{}, testInvalid(at, "bad_params",
			at+" params.mu has "+strconv.Itoa(len(params.Mu))+" entries for "+strconv.Itoa(p)+" members; give one per member",
			map[string]any{"param": "mu", "len": len(params.Mu), "p": p})
	}
	if form != HotellingTwoSample {
		cfg.Mu = params.Mu
	}
	return cfg, nil
}

// TestInputMembers returns the union of every field req's tier-1 tests read
// through inline fields / fields2 (a named vector's members ride
// Members(req)), and whether every one resolved — projection decodes
// wide otherwise.
func TestInputMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	ok = true
	if req == nil {
		return nil, true
	}
	for i, t := range req.Tests {
		if t == nil {
			continue
		}
		for _, side := range []struct {
			key    string
			fields []string
		}{{"fields", t.Fields}, {"fields2", t.Fields2}} {
			if len(side.fields) == 0 {
				continue
			}
			r, err := ResolveSpec(testPath(i)+"."+side.key, types.VectorSpec{Fields: side.fields}, schema)
			if err != nil {
				ok = false
				continue
			}
			members = append(members, r.Members...)
		}
	}
	return members, ok
}

func testPath(i int) string { return "tests[" + strconv.Itoa(i) + "]" }

func testInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"test": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}
