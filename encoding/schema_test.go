package encoding

import "testing"

func TestFieldTypes_All17Present(t *testing.T) {
	// Verify we have exactly 17 field types (0..16).
	if fieldTypeCount != 17 {
		t.Fatalf("expected 17 field types, got %d", fieldTypeCount)
	}

	types := []FieldType{
		FieldTypeU8, FieldTypeU16, FieldTypeU32, FieldTypeU64,
		FieldTypeF32, FieldTypeF64,
		FieldTypeNullableBool, FieldTypeNullableU4, FieldTypeNullableU8, FieldTypeNullableU16,
		FieldTypeDate, FieldTypePackedBool,
		FieldTypeCategoricalU8, FieldTypeCategoricalU16, FieldTypeCategoricalU32,
		FieldTypeDecimal128, FieldTypeNullableDecimal128,
	}
	if len(types) != int(fieldTypeCount) {
		t.Fatalf("type list has %d entries, sentinel says %d", len(types), fieldTypeCount)
	}
	for i, ft := range types {
		if int(ft) != i {
			t.Errorf("type at index %d has value %d", i, ft)
		}
	}
}

func TestFieldType_ByteSize(t *testing.T) {
	cases := []struct {
		ft   FieldType
		want int
	}{
		{FieldTypeU8, 1},
		{FieldTypeU16, 2},
		{FieldTypeU32, 4},
		{FieldTypeU64, 8},
		{FieldTypeF32, 4},
		{FieldTypeF64, 8},
		{FieldTypeNullableBool, 0},
		{FieldTypeNullableU4, 0},
		{FieldTypeNullableU8, 1},
		{FieldTypeNullableU16, 2},
		{FieldTypeDate, 4},
		{FieldTypePackedBool, 0},
		{FieldTypeCategoricalU8, 1},
		{FieldTypeCategoricalU16, 2},
		{FieldTypeCategoricalU32, 4},
		{FieldTypeDecimal128, 16},
		{FieldTypeNullableDecimal128, 16},
	}
	for _, tc := range cases {
		if got := tc.ft.ByteSize(); got != tc.want {
			t.Errorf("%s.ByteSize() = %d, want %d", tc.ft, got, tc.want)
		}
	}
}

func TestFieldType_String(t *testing.T) {
	cases := []struct {
		ft   FieldType
		want string
	}{
		{FieldTypeU8, "u8"},
		{FieldTypeU16, "u16"},
		{FieldTypeU32, "u32"},
		{FieldTypeU64, "u64"},
		{FieldTypeF32, "f32"},
		{FieldTypeF64, "f64"},
		{FieldTypeNullableBool, "nullable_bool"},
		{FieldTypeNullableU4, "nullable_u4"},
		{FieldTypeNullableU8, "nullable_u8"},
		{FieldTypeNullableU16, "nullable_u16"},
		{FieldTypeDate, "date"},
		{FieldTypePackedBool, "packed_bool"},
		{FieldTypeCategoricalU8, "categorical_u8"},
		{FieldTypeCategoricalU16, "categorical_u16"},
		{FieldTypeCategoricalU32, "categorical_u32"},
		{FieldTypeDecimal128, "decimal128"},
		{FieldTypeNullableDecimal128, "nullable_decimal128"},
		{FieldType(255), "unknown(255)"},
	}
	for _, tc := range cases {
		if got := tc.ft.String(); got != tc.want {
			t.Errorf("FieldType(%d).String() = %q, want %q", tc.ft, got, tc.want)
		}
	}
}

func TestFieldType_IsCategorical(t *testing.T) {
	categorical := map[FieldType]bool{
		FieldTypeCategoricalU8:  true,
		FieldTypeCategoricalU16: true,
		FieldTypeCategoricalU32: true,
	}
	for ft := FieldType(0); ft < fieldTypeCount; ft++ {
		want := categorical[ft]
		if got := ft.IsCategorical(); got != want {
			t.Errorf("%s.IsCategorical() = %v, want %v", ft, got, want)
		}
	}
}

func TestFieldType_MaxCategoricalEntries(t *testing.T) {
	cases := []struct {
		ft   FieldType
		want uint32
	}{
		{FieldTypeCategoricalU8, 256},
		{FieldTypeCategoricalU16, 65536},
		{FieldTypeCategoricalU32, 4294967295},
		{FieldTypeU8, 0},
	}
	for _, tc := range cases {
		if got := tc.ft.MaxCategoricalEntries(); got != tc.want {
			t.Errorf("%s.MaxCategoricalEntries() = %d, want %d", tc.ft, got, tc.want)
		}
	}
}

func TestFieldType_ByteSize_Unknown(t *testing.T) {
	if got := FieldType(200).ByteSize(); got != 0 {
		t.Errorf("unknown type ByteSize() = %d, want 0", got)
	}
}

func TestSchema_Field(t *testing.T) {
	s := &Schema{
		Fields: []Field{
			{Name: "age", Type: FieldTypeU8},
			{Name: "score", Type: FieldTypeF64},
		},
	}

	f := s.Field("age")
	if f == nil {
		t.Fatal("Field(age) returned nil")
	}
	if f.Type != FieldTypeU8 {
		t.Errorf("Field(age).Type = %v, want U8", f.Type)
	}

	if s.Field("missing") != nil {
		t.Error("Field(missing) should be nil")
	}
}

func TestSchema_Categorical(t *testing.T) {
	dict := NewDictionary()
	dict.Add("red")
	dict.Add("blue")

	s := &Schema{
		Fields: []Field{
			{Name: "color", Type: FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "age", Type: FieldTypeU8},
		},
	}

	d, ok := s.Categorical("color")
	if !ok || d == nil {
		t.Fatal("Categorical(color) failed")
	}
	if d.Count() != 2 {
		t.Errorf("dict count = %d, want 2", d.Count())
	}

	_, ok = s.Categorical("age")
	if ok {
		t.Error("Categorical(age) should return false")
	}

	_, ok = s.Categorical("missing")
	if ok {
		t.Error("Categorical(missing) should return false")
	}
}
