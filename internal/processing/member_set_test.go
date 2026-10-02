package processing

import (
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func memberSetCategoricalSchema(t *testing.T, name string, ft encoding.FieldType, entries ...string) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, e := range entries {
		if _, err := dict.Add(e); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: name, Type: ft, Dictionary: dict},
	}}
}

func u64Schema(name string, ft encoding.FieldType) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{{Name: name, Type: ft}}}
}

func TestLoadMemberSet_Categorical_Bitset(t *testing.T) {
	schema := memberSetCategoricalSchema(t, "color", encoding.FieldTypeCategoricalU8, "red", "green", "blue")
	r := strings.NewReader("red\nblue\nred\n")
	res, err := LoadMemberSetFromReader(r, schema, "color")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	bs, ok := res.Set.(*BitsetSet)
	if !ok {
		t.Fatalf("set is %T, want *BitsetSet", res.Set)
	}
	if bs.Len() != 2 {
		t.Errorf("Len = %d, want 2 (red dedup)", bs.Len())
	}
	if !bs.Contains(0) {
		t.Error("red (id 0) missing")
	}
	if bs.Contains(1) {
		t.Error("green (id 1) should not be present")
	}
	if !bs.Contains(2) {
		t.Error("blue (id 2) missing")
	}
	if res.Lines != 3 {
		t.Errorf("Lines = %d, want 3", res.Lines)
	}
}

func TestLoadMemberSet_CategoricalDrops_NotInDictionary(t *testing.T) {
	schema := memberSetCategoricalSchema(t, "color", encoding.FieldTypeCategoricalU8, "red", "green")
	r := strings.NewReader("red\norange\nyellow\n")
	res, err := LoadMemberSetFromReader(r, schema, "color")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if res.NotInDictionary != 2 {
		t.Errorf("NotInDictionary = %d, want 2", res.NotInDictionary)
	}
	if res.Set.Len() != 1 {
		t.Errorf("Set.Len = %d, want 1", res.Set.Len())
	}
}

func TestLoadMemberSet_Integer_Uint64(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU64)
	r := strings.NewReader("1\n2\n2\n42\n9999999999\n")
	res, err := LoadMemberSetFromReader(r, schema, "id")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	us, ok := res.Set.(*Uint64Set)
	if !ok {
		t.Fatalf("set is %T, want *Uint64Set", res.Set)
	}
	if us.Len() != 4 {
		t.Errorf("Len = %d, want 4 (dedup)", us.Len())
	}
	for _, want := range []uint64{1, 2, 42, 9999999999} {
		if !us.Contains(want) {
			t.Errorf("missing %d", want)
		}
	}
	if us.Contains(3) {
		t.Error("unexpected 3 in set")
	}
}

func TestLoadMemberSet_IntegerInvalid(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	r := strings.NewReader("1\nabc\n2\n-3\n4\n")
	res, err := LoadMemberSetFromReader(r, schema, "id")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if res.Invalid != 2 {
		t.Errorf("Invalid = %d, want 2", res.Invalid)
	}
	if res.Set.Len() != 3 {
		t.Errorf("Set.Len = %d, want 3 (1,2,4)", res.Set.Len())
	}
}

func TestLoadMemberSet_FloatRejected(t *testing.T) {
	schema := u64Schema("x", encoding.FieldTypeF64)
	_, err := LoadMemberSetFromReader(strings.NewReader("1.0\n"), schema, "x")
	if err == nil {
		t.Fatal("expected error for f64 field")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("err = %v, want SERVICE_VALIDATION", err)
	}
	_, err = LoadMemberSetFromReader(strings.NewReader("1.0\n"), u64Schema("x", encoding.FieldTypeF32), "x")
	if err == nil {
		t.Fatal("expected error for f32 field")
	}
}

func TestLoadMemberSet_UnknownField(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	_, err := LoadMemberSetFromReader(strings.NewReader("1\n"), schema, "nope")
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("err = %v, want SERVICE_VALIDATION", err)
	}
}

func TestLoadMemberSet_BlanksBOM_CRLF(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU64)
	r := strings.NewReader("\xEF\xBB\xBF1\r\n\r\n2\n\n   3   \n")
	res, err := LoadMemberSetFromReader(r, schema, "id")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if res.Set.Len() != 3 {
		t.Errorf("Set.Len = %d, want 3 (1,2,3)", res.Set.Len())
	}
	if res.Lines != 3 {
		t.Errorf("Lines = %d, want 3", res.Lines)
	}
	us := res.Set.(*Uint64Set)
	for _, v := range []uint64{1, 2, 3} {
		if !us.Contains(v) {
			t.Errorf("missing %d after CRLF/BOM/whitespace stripping", v)
		}
	}
}

func TestLoadMemberSet_DecimalIsStringSet(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2},
	}}
	r := strings.NewReader("12.34\n0.01\n12.34\n")
	res, err := LoadMemberSetFromReader(r, schema, "amt")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if _, ok := res.Set.(*StringSet); !ok {
		t.Fatalf("set is %T, want *StringSet", res.Set)
	}
	if res.Set.Len() != 2 {
		t.Errorf("Set.Len = %d, want 2", res.Set.Len())
	}
}

func TestLoadMemberSet_EmptyFile(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	res, err := LoadMemberSetFromReader(strings.NewReader(""), schema, "id")
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	if res.Set.Len() != 0 {
		t.Errorf("Set.Len = %d, want 0", res.Set.Len())
	}
	if res.Lines != 0 {
		t.Errorf("Lines = %d, want 0", res.Lines)
	}
}

func TestBuildMemberSetPredicate_Bitset_Categorical(t *testing.T) {
	schema := memberSetCategoricalSchema(t, "color", encoding.FieldTypeCategoricalU8, "red", "green", "blue")
	bs := newBitsetSet(3)
	bs.Add(0) // red
	bs.Add(2) // blue

	fn, err := BuildMemberSetPredicate(bs, schema, "color")
	if err != nil {
		t.Fatalf("BuildMemberSetPredicate: %v", err)
	}

	rec := NewRecord(schema, map[string]float64{"color": 0})
	keep, err := fn(rec)
	if err != nil {
		t.Fatalf("fn: %v", err)
	}
	if !keep {
		t.Error("red should be kept")
	}

	rec = NewRecord(schema, map[string]float64{"color": 1})
	keep, _ = fn(rec)
	if keep {
		t.Error("green should be dropped")
	}

	rec = NewRecord(schema, map[string]float64{"color": 2})
	keep, _ = fn(rec)
	if !keep {
		t.Error("blue should be kept")
	}
}

func TestBuildMemberSetPredicate_Bitset_NonCategoricalRejected(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	bs := newBitsetSet(4)
	_, err := BuildMemberSetPredicate(bs, schema, "id")
	if err == nil {
		t.Fatal("expected error for bitset on non-categorical field")
	}
}

func TestBuildMemberSetPredicate_Uint64Set(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	us := newUint64Set(4)
	us.Add(1)
	us.Add(7)

	fn, err := BuildMemberSetPredicate(us, schema, "id")
	if err != nil {
		t.Fatalf("BuildMemberSetPredicate: %v", err)
	}

	for _, c := range []struct {
		v    float64
		want bool
	}{{1, true}, {2, false}, {7, true}, {0, false}} {
		keep, _ := fn(NewRecord(schema, map[string]float64{"id": c.v}))
		if keep != c.want {
			t.Errorf("id=%v: keep=%v want=%v", c.v, keep, c.want)
		}
	}
}

func TestBuildMemberSetPredicate_StringSet_Categorical(t *testing.T) {
	schema := memberSetCategoricalSchema(t, "color", encoding.FieldTypeCategoricalU8, "red", "green", "blue")
	ss := newStringSet(2)
	ss.Add("green")

	fn, err := BuildMemberSetPredicate(ss, schema, "color")
	if err != nil {
		t.Fatalf("BuildMemberSetPredicate: %v", err)
	}

	rec := NewRecord(schema, map[string]float64{"color": 1})
	keep, _ := fn(rec)
	if !keep {
		t.Error("green should be kept (string lookup via dict)")
	}
	rec = NewRecord(schema, map[string]float64{"color": 0})
	keep, _ = fn(rec)
	if keep {
		t.Error("red should be dropped")
	}
}

func TestBuildMemberSetPredicate_FloatRejected(t *testing.T) {
	schema := u64Schema("x", encoding.FieldTypeF64)
	_, err := BuildMemberSetPredicate(newUint64Set(0), schema, "x")
	if err == nil {
		t.Fatal("expected float rejection")
	}
}

func TestBuildMemberSetPredicate_NullValueDrops(t *testing.T) {
	schema := u64Schema("id", encoding.FieldTypeU32)
	us := newUint64Set(2)
	us.Add(1)
	fn, err := BuildMemberSetPredicate(us, schema, "id")
	if err != nil {
		t.Fatalf("BuildMemberSetPredicate: %v", err)
	}
	rec := NewRecordWithNulls(schema, map[string]float64{}, map[string]bool{"id": true})
	keep, _ := fn(rec)
	if keep {
		t.Error("null record value must not match set")
	}
}

func TestBitsetSet_ContainsOutOfRange(t *testing.T) {
	bs := newBitsetSet(8)
	if bs.Contains(99) {
		t.Error("out-of-range id should return false without panic")
	}
}

// silence "math" import if all f-math removed during refactor
var _ = math.Pi
