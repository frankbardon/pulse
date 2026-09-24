package service

import (
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
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
// and the SAME sentence. descriptor/ cannot import processing/, so the
// rejection text is duplicated by hand in descriptor/join.go — this
// test is what keeps the two copies honest. A caller who runs
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
	env := descriptor.ValidateJoinFromBytes(
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
