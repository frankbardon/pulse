package encoding

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func joinKeySchemas() (*encoding.Schema, *encoding.Schema) {
	left := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "amount", Type: encoding.FieldTypeDecimal128},
		{Name: "picks", Type: encoding.FieldTypeSetU8},
	}}
	right := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeCategoricalU16},
		{Name: "picks", Type: encoding.FieldTypeSetU8},
	}}
	return left, right
}

// TestJoinKeysRefusals_OrderCodesAndDetails pins the one join-key rule:
// every refusal, in the runtime's order, with its code, message and
// details.
func TestJoinKeysRefusals_OrderCodesAndDetails(t *testing.T) {
	left, right := joinKeySchemas()
	spec := &types.JoinSpec{Kind: "left", On: []types.OnPair{
		{LeftField: "id", RightField: "id"},         // u32 ~ f64: ok
		{LeftField: "region", RightField: "region"}, // categorical widths: ok
		{LeftField: "", RightField: "id"},           // blank side
		{LeftField: "zz", RightField: "id"},         // unknown left
		{LeftField: "id", RightField: "zz"},         // unknown right
		{LeftField: "amount", RightField: "id"},     // decimal vs float
		{LeftField: "picks", RightField: "picks"},   // set key, same rung
	}}
	got := JoinKeysRefusals(left, right, spec)
	want := []struct {
		code    errors.Code
		message string
		details string
	}{
		{errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED, "only inner join is implemented in v1", `{"kind":"left"}`},
		{errors.PULSE_JOIN_KEYS_EMPTY, "OnPair requires both LeftField and RightField", `{"index":2}`},
		{errors.PULSE_JOIN_FIELD_UNKNOWN, "OnPair.LeftField not found in left schema", `{"field":"zz","index":3}`},
		{errors.PULSE_JOIN_FIELD_UNKNOWN, "OnPair.RightField not found in right schema", `{"field":"zz","index":4}`},
		{errors.PULSE_JOIN_TYPE_MISMATCH, "join key types are not compatible",
			`{"index":5,"left_field":"amount","left_type":"decimal128","right_field":"id","right_type":"f64"}`},
		{errors.PULSE_JOIN_TYPE_MISMATCH, JoinKeySetRejection,
			`{"index":6,"left_field":"picks","left_type":"set_u8","reason":"set_key","right_field":"picks","right_type":"set_u8"}`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refusals, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		d, _ := json.Marshal(got[i].Details)
		if got[i].Code != w.code || got[i].Message != w.message || string(d) != w.details {
			t.Errorf("[%d] = %s %q %s, want %s %q %s", i, got[i].Code, got[i].Message, d, w.code, w.message, w.details)
		}
	}
	first, ok := JoinKeysRefusal(left, right, spec).(*errors.CodedError)
	if !ok || first.Code != got[0].Code || first.Message != got[0].Message {
		t.Fatalf("JoinKeysRefusal = %v, want the first refusal %v", first, got[0])
	}
}

func TestJoinKeysRefusals_EmptyOnAndAccepted(t *testing.T) {
	left, right := joinKeySchemas()
	got := JoinKeysRefusals(left, right, &types.JoinSpec{})
	if len(got) != 1 || got[0].Code != errors.PULSE_JOIN_KEYS_EMPTY || got[0].Details != nil {
		t.Fatalf("empty On = %v, want one detail-less PULSE_JOIN_KEYS_EMPTY", got)
	}
	ok := &types.JoinSpec{Kind: "inner", On: []types.OnPair{{LeftField: "id", RightField: "id"}}}
	if err := JoinKeysRefusal(left, right, ok); err != nil {
		t.Fatalf("valid spec refused: %v", err)
	}
	if JoinKeysRefusals(nil, right, ok) != nil || JoinKeysRefusals(left, right, nil) != nil {
		t.Fatal("nil schema / spec must yield no refusals")
	}
}

func TestJoinKeyTypesCompatible(t *testing.T) {
	cases := []struct {
		a, b encoding.FieldType
		want bool
	}{
		{encoding.FieldTypeU8, encoding.FieldTypeF32, true},
		{encoding.FieldTypeDate, encoding.FieldTypeU32, true},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU32, true},
		{encoding.FieldTypeDecimal128, encoding.FieldTypeDecimal128, true},
		{encoding.FieldTypeDecimal128, encoding.FieldTypeF64, false},
		{encoding.FieldTypeCategoricalU8, encoding.FieldTypeU8, false},
		{encoding.FieldTypeSetU8, encoding.FieldTypeSetU8, false},
		{encoding.FieldTypeDateTime, encoding.FieldTypeDateTime, true},
		{encoding.FieldTypeDateTime, encoding.FieldTypeU64, false},
	}
	for _, c := range cases {
		if got := JoinKeyTypesCompatible(c.a, c.b); got != c.want {
			t.Errorf("JoinKeyTypesCompatible(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
