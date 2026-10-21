package encoding

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestDictionaryIndexFault (U51 E5-S8): the shared refusal fires for a
// code at or past the dictionary's length — and for a negative or
// fractional echo — and never for an in-range code, a field with no
// dictionary or a non-dictionary type. An in-range entry whose label is
// "" is a genuine value, not a fault.
//
// Falsified by changing `v < float64(n)` to `v <= float64(n)` (the
// one-past-the-end case then passes).
func TestDictionaryIndexFault(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, s := range []string{"north", "", "south"} {
		_, _ = dict.Add(s)
	}
	cat := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict}
	for _, tc := range []struct {
		v    float64
		want bool
	}{{0, false}, {1, false}, {2, false}, {3, true}, {65535, true}, {-1, true}, {0.5, true}} {
		err := CategoricalIndexFault(cat, tc.v)
		if (err != nil) != tc.want {
			t.Errorf("CategoricalIndexFault(%v) = %v, want fault %v", tc.v, err, tc.want)
			continue
		}
		if err == nil {
			continue
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID ||
			ce.Details["reason"] != ReasonCategoricalIndexOutOfRange || ce.Details["dictionary_size"] != 3 ||
			ce.Details["field"] != "region" || ce.Details["index"] != tc.v {
			t.Errorf("CategoricalIndexFault(%v) = %v", tc.v, err)
		}
	}
	if err := CategoricalIndexFault(&encoding.Field{Name: "n", Type: encoding.FieldTypeU8}, 99); err != nil {
		t.Errorf("non-categorical field faulted: %v", err)
	}
	if err := CategoricalIndexFault(&encoding.Field{Name: "c", Type: encoding.FieldTypeCategoricalU8}, 99); err != nil {
		t.Errorf("dictionary-less categorical faulted: %v", err)
	}
	if err := CategoricalIndexFault(nil, 99); err != nil {
		t.Errorf("nil field faulted: %v", err)
	}

	set := &encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict}
	if err := SetMemberFault(set, encoding.SetMaskFromUint64(0b111)); err != nil {
		t.Errorf("in-range mask faulted: %v", err)
	}
	if err := SetMemberFault(set, encoding.SetMaskFromUint64(0)); err != nil {
		t.Errorf("empty mask faulted: %v", err)
	}
	err := SetMemberFault(set, encoding.SetMaskFromUint64(0b1001))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != ReasonSetMemberOutOfRange || ce.Details["bit"] != 3.0 {
		t.Errorf("bit 3 of a 3-entry set: %v", err)
	}
	if err := SetMemberFault(cat, encoding.SetMaskFromUint64(0b1000)); err != nil {
		t.Errorf("non-set field faulted: %v", err)
	}
}
