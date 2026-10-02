package encoding

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// JoinKeySetRejection is the one explanation for refusing a set_*
// column as a join key, whichever side asks.
//
// A set mask has no single unambiguous equality value — the empty
// selection, a single-member mask and a multi-member mask are all
// distinct legal states — and the equality the hash join would use is
// worse than ambiguous: its key stringifies the LOSSY float64 echo of
// the mask (the low 64 bits for set_u128 / set_u256, a value already
// past float64's mantissa above 2^53 for set_u64), so two different
// selections collapse onto one key and rows join that share no answer.
const JoinKeySetRejection = "a set_* column cannot be a join key: a multi-select bitmask has no single unambiguous equality value (empty selection, single-member and multi-member masks are all distinct legal states), and its numeric echo is lossy — use a FILTER_SET_* membership predicate instead"

// JoinKeysRefusals is the ONE join-key rule. It judges a JoinSpec's
// kind and OnPairs against the left and right cohort schemas and
// returns every refusal, in the order the runtime meets them:
//
//   - no OnPair at all — PULSE_JOIN_KEYS_EMPTY (no details);
//   - a kind other than inner — PULSE_JOIN_KIND_NOT_IMPLEMENTED {kind};
//   - per OnPair i, in order: a blank side — PULSE_JOIN_KEYS_EMPTY
//     {index}; a LeftField the left schema lacks, then a RightField the
//     right schema lacks — PULSE_JOIN_FIELD_UNKNOWN {field, index}; key
//     types that cannot be compared — PULSE_JOIN_TYPE_MISMATCH
//     {left_field, left_type, right_field, right_type, index}, plus
//     reason "set_key" with JoinKeySetRejection for a set_* key.
//
// The runtime (processing.NewHashJoinIterator, and the service before it
// materialises the right side) refuses with the first entry; predict,
// ValidateJoin and the Compose / chain validators report them — so a
// caller sees the same code, message and details from either side.
// A nil spec or schema yields nil: there is nothing to judge.
func JoinKeysRefusals(left, right *encoding.Schema, spec *types.JoinSpec) []*errors.CodedError {
	if spec == nil || left == nil || right == nil {
		return nil
	}
	var out []*errors.CodedError
	if len(spec.On) == 0 {
		out = append(out, errors.NewCodedError(errors.PULSE_JOIN_KEYS_EMPTY,
			"JoinSpec.On is empty; at least one OnPair is required"))
	}
	if kind := spec.Kind; kind != "" && kind != "inner" {
		out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED,
			"only inner join is implemented in v1",
			map[string]any{"kind": kind}))
	}
	for i, pair := range spec.On {
		if pair.LeftField == "" || pair.RightField == "" {
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_KEYS_EMPTY,
				"OnPair requires both LeftField and RightField",
				map[string]any{"index": i}))
			continue
		}
		lf := left.Field(pair.LeftField)
		if lf == nil {
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_UNKNOWN,
				"OnPair.LeftField not found in left schema",
				map[string]any{"field": pair.LeftField, "index": i}))
			continue
		}
		rf := right.Field(pair.RightField)
		if rf == nil {
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_UNKNOWN,
				"OnPair.RightField not found in right schema",
				map[string]any{"field": pair.RightField, "index": i}))
			continue
		}
		if !JoinKeyTypesCompatible(lf.Type, rf.Type) {
			details := map[string]any{
				"left_field":  pair.LeftField,
				"left_type":   lf.Type.String(),
				"right_field": pair.RightField,
				"right_type":  rf.Type.String(),
				"index":       i,
			}
			msg := "join key types are not compatible"
			if lf.Type.IsSet() || rf.Type.IsSet() {
				// Same rung on both sides still lands here, so say why
				// rather than leave "not compatible" reading as a typo.
				msg = JoinKeySetRejection
				details["reason"] = "set_key"
			}
			out = append(out, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_TYPE_MISMATCH, msg, details))
		}
	}
	return out
}

// JoinKeysRefusal is the first of JoinKeysRefusals, or nil — the
// refusal the runtime returns.
func JoinKeysRefusal(left, right *encoding.Schema, spec *types.JoinSpec) error {
	if all := JoinKeysRefusals(left, right, spec); len(all) > 0 {
		return all[0]
	}
	return nil
}

// JoinKeyTypesCompatible reports whether two schema types can be
// compared as equi-join keys after normalisation. The conservative v1
// rule: a set_* column never (not even against an identical rung — see
// JoinKeySetRejection); identical types match; categorical types of
// any width match each other (dict strings normalise to text); the
// unsigned-int / float / date family matches within itself. decimal128
// matches only decimal128 (precision differences matter).
func JoinKeyTypesCompatible(a, b encoding.FieldType) bool {
	if a.IsSet() || b.IsSet() {
		return false
	}
	if a == b {
		return true
	}
	if a.IsCategorical() && b.IsCategorical() {
		return true
	}
	return joinNumericFamily(a) && joinNumericFamily(b)
}

func joinNumericFamily(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate:
		return true
	}
	return false
}
