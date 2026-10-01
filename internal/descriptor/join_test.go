package descriptor

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func buildJoinPulseBytes(t *testing.T, fields []encoding.Field) []byte {
	t.Helper()
	schema := &encoding.Schema{Fields: fields}
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	return buf.Bytes()
}

func TestValidateJoin_Valid(t *testing.T) {
	left := buildJoinPulseBytes(t, []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	})
	right := buildJoinPulseBytes(t, []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	})
	env := ValidateJoinFromBytes(left, right, &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
	})
	if len(env.Errors) > 0 {
		t.Fatalf("expected no errors, got %+v", env.Errors)
	}
	res := env.Data.(*JoinValidationResult)
	if !res.Valid {
		t.Fatalf("expected Valid=true, got %+v", res)
	}
	if len(res.JoinedFields) != 4 {
		t.Errorf("expected 4 joined fields, got %v", res.JoinedFields)
	}
}

func TestValidateJoin_TypeMismatch(t *testing.T) {
	left := buildJoinPulseBytes(t, []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
	})
	right := buildJoinPulseBytes(t, []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeDecimal128, ByteOffset: 0, CsvColumnIdx: 0, Precision: 18, Scale: 2},
	})
	env := ValidateJoinFromBytes(left, right, &types.Request{
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
	})
	if len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_JOIN_TYPE_MISMATCH" {
		t.Fatalf("expected PULSE_JOIN_TYPE_MISMATCH, got %+v", env.Errors)
	}
}

func TestValidateJoin_KindReserved(t *testing.T) {
	left := buildJoinPulseBytes(t, []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}})
	right := buildJoinPulseBytes(t, []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}})
	env := ValidateJoinFromBytes(left, right, &types.Request{
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", Kind: "outer", As: "r_", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
	})
	codes := []string{}
	for _, e := range env.Errors {
		codes = append(codes, e.Code)
	}
	found := false
	for _, c := range codes {
		if c == "PULSE_JOIN_KIND_NOT_IMPLEMENTED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PULSE_JOIN_KIND_NOT_IMPLEMENTED among errors, got %v", codes)
	}
}

func TestValidateJoin_FieldCollision(t *testing.T) {
	left := buildJoinPulseBytes(t, []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}})
	right := buildJoinPulseBytes(t, []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}})
	// no As, both sides have "id"
	env := ValidateJoinFromBytes(left, right, &types.Request{
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
	})
	codes := []string{}
	for _, e := range env.Errors {
		codes = append(codes, e.Code)
	}
	found := false
	for _, c := range codes {
		if c == "PULSE_JOIN_FIELD_COLLISION" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PULSE_JOIN_FIELD_COLLISION among errors, got %v", codes)
	}
}
