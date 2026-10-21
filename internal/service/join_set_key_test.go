package service

import (
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// joinSetKeyHeaderBytes writes a header-only `.pulse` carrying schema —
// enough for descriptor.ValidateJoin, which is header-only by contract.
func joinSetKeyHeaderBytes(t *testing.T, fields []encoding.Field) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, &encoding.Schema{Fields: fields}); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	return buf.Bytes()
}

// Predict and runtime must refuse a set_* join key with the SAME code
// and the SAME sentence. Both sides now call the one join-key rule
// (internal/encoding.JoinKeysRefusals); this test keeps a future
// re-duplication honest. A caller who runs
// `pulse predict` and then `pulse api process` must not be told two
// different stories about the same request.
func TestValidateJoin_SetKeyRejectedMessageMatchesProcessing(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c"} {
		_, _ = dict.Add(v)
	}
	leftFields := []encoding.Field{
		{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
	}
	rightFields := []encoding.Field{
		{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
	}
	spec := &types.JoinSpec{
		Right: "right.pulse",
		Kind:  "inner",
		As:    "r_",
		On:    []types.OnPair{{LeftField: "picks", RightField: "picks"}},
	}
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins:  []*types.JoinSpec{spec},
	}

	// Predict arm.
	env := descx.ValidateJoinFromBytes(
		joinSetKeyHeaderBytes(t, leftFields),
		joinSetKeyHeaderBytes(t, rightFields),
		req)
	if len(env.Errors) == 0 {
		t.Fatalf("descriptor.ValidateJoin accepted a set_u8 join key")
	}
	if got := env.Errors[0].Code; got != string(errors.PULSE_JOIN_TYPE_MISMATCH) {
		t.Fatalf("predict code = %s, want %s", got, errors.PULSE_JOIN_TYPE_MISMATCH)
	}
	if got := env.Errors[0].Details["reason"]; got != "set_key" {
		t.Errorf("predict details[reason] = %v, want set_key", got)
	}

	// Runtime arm.
	_, _, rerr := processing.NewHashJoinIterator(
		processing.NewSliceIterator(nil), nil,
		&encoding.Schema{Fields: leftFields},
		&encoding.Schema{Fields: rightFields},
		spec)
	if rerr == nil {
		t.Fatalf("processing.NewHashJoinIterator accepted a set_u8 join key")
	}
	var coded *errors.CodedError
	if !stderrors.As(rerr, &coded) {
		t.Fatalf("runtime error is not coded: %v", rerr)
	}
	if coded.Code != errors.PULSE_JOIN_TYPE_MISMATCH {
		t.Fatalf("runtime code = %s, want %s", coded.Code, errors.PULSE_JOIN_TYPE_MISMATCH)
	}

	if env.Errors[0].Message != coded.Message {
		t.Errorf("predict and runtime disagree on the rejection text:\n  predict: %q\n  runtime: %q",
			env.Errors[0].Message, coded.Message)
	}
}

// A u64 key paired with an f32 / f64 key is refused by the one join-key
// rule (exact or nothing, #458): predict and runtime raise the same
// code, reason and sentence, in both pair orders.
func TestValidateJoin_U64FloatKeyRefusedMessageMatchesProcessing(t *testing.T) {
	for _, c := range []struct{ left, right encoding.FieldType }{
		{encoding.FieldTypeU64, encoding.FieldTypeF64},
		{encoding.FieldTypeF32, encoding.FieldTypeU64},
	} {
		t.Run(c.left.String()+"_"+c.right.String(), func(t *testing.T) {
			leftFields := []encoding.Field{{Name: "id", Type: c.left, ByteOffset: 0}}
			rightFields := []encoding.Field{{Name: "id", Type: c.right, ByteOffset: 0}}
			spec := &types.JoinSpec{Right: "right.pulse", Kind: "inner", As: "r_",
				On: []types.OnPair{{LeftField: "id", RightField: "id"}}}
			req := &types.Request{Cohort: &types.Cohort{Filename: "left.pulse"}, Joins: []*types.JoinSpec{spec}}

			env := descx.ValidateJoinFromBytes(
				joinSetKeyHeaderBytes(t, leftFields), joinSetKeyHeaderBytes(t, rightFields), req)
			if len(env.Errors) == 0 {
				t.Fatalf("descriptor.ValidateJoin accepted a %s / %s join key", c.left, c.right)
			}
			if got := env.Errors[0].Code; got != string(errors.PULSE_JOIN_TYPE_MISMATCH) {
				t.Fatalf("predict code = %s, want %s", got, errors.PULSE_JOIN_TYPE_MISMATCH)
			}
			if got := env.Errors[0].Details["reason"]; got != "u64_float_key" {
				t.Errorf("predict details[reason] = %v, want u64_float_key", got)
			}

			_, _, rerr := processing.NewHashJoinIterator(processing.NewSliceIterator(nil), nil,
				&encoding.Schema{Fields: leftFields}, &encoding.Schema{Fields: rightFields}, spec)
			var coded *errors.CodedError
			if !stderrors.As(rerr, &coded) || coded.Code != errors.PULSE_JOIN_TYPE_MISMATCH {
				t.Fatalf("runtime error = %v, want coded %s", rerr, errors.PULSE_JOIN_TYPE_MISMATCH)
			}
			if coded.Details["reason"] != "u64_float_key" || env.Errors[0].Message != coded.Message {
				t.Errorf("predict and runtime disagree:\n  predict: %q %v\n  runtime: %q %v",
					env.Errors[0].Message, env.Errors[0].Details, coded.Message, coded.Details)
			}
		})
	}
}
