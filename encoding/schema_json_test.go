package encoding

import (
	"encoding/json"
	"testing"
	"unicode"
)

// assertSnakeKeys walks a decoded JSON value and fails on any object key
// carrying an upper-case letter: every JSON surface built from a schema
// speaks snake_case, never the Go field names.
func assertSnakeKeys(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			for _, r := range k {
				if unicode.IsUpper(r) {
					t.Errorf("%s: key %q is not snake_case", path, k)
					break
				}
			}
			assertSnakeKeys(t, path+"."+k, child)
		}
	case []any:
		for _, child := range x {
			assertSnakeKeys(t, path+"[]", child)
		}
	}
}

// TestSchema_JSONSnakeCaseAndTypeNames: a schema marshals with
// snake_case keys and string type / kind names — `"type": "f64"`, never
// the numeric type byte — and a dictionary-bearing field reports its
// entry count rather than the empty object its unexported state used to
// render as. The JSON shape is a report shape only: the `.pulse` bytes
// never pass through it.
func TestSchema_JSONSnakeCaseAndTypeNames(t *testing.T) {
	t.Parallel()

	d := NewDictionary()
	_, _ = d.Add("x")
	_, _ = d.Add("y")
	s := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeF64, Nullable: true, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "b", Type: FieldTypeCategoricalU8, ByteOffset: 8, CsvColumnIdx: 1, Dictionary: d},
			{Name: "c", Type: FieldTypeDecimal128, ByteOffset: 9, Precision: 10, Scale: 2},
			{Name: "v", Type: FieldTypeVecF32, ByteOffset: 25},
		},
		Groups:  []Group{{Kind: GroupKindIndexed, Members: []GroupMember{{Field: 1, Key: true}}, Entries: []byte{0, 1}}},
		Vectors: []VectorField{{FieldIndex: 3, Dim: 2, Kind: VectorKindMeasure, Labels: []string{"p", "q"}}},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	assertSnakeKeys(t, "schema", got)

	fields, _ := got["fields"].([]any)
	if len(fields) != 4 {
		t.Fatalf("fields = %v, want 4 entries\n%s", got["fields"], raw)
	}
	wantTypes := []string{"f64", "categorical_u8", "decimal128", "vec_f32"}
	for i, f := range fields {
		m := f.(map[string]any)
		if m["type"] != wantTypes[i] {
			t.Errorf("fields[%d].type = %#v, want %q", i, m["type"], wantTypes[i])
		}
	}
	b := fields[1].(map[string]any)
	if b["dictionary_size"] != float64(2) {
		t.Errorf("fields[1].dictionary_size = %#v, want 2\n%s", b["dictionary_size"], raw)
	}
	if _, ok := fields[0].(map[string]any)["dictionary_size"]; ok {
		t.Errorf("a dictionary-free field carries dictionary_size\n%s", raw)
	}
	if c := fields[2].(map[string]any); c["precision"] != float64(10) || c["scale"] != float64(2) {
		t.Errorf("decimal precision/scale = %v/%v, want 10/2", c["precision"], c["scale"])
	}
	groups, _ := got["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["kind"] != "indexed" {
		t.Errorf("groups = %#v, want one indexed group\n%s", got["groups"], raw)
	}
	if _, ok := groups[0].(map[string]any)["entries"]; ok {
		t.Errorf("group carries its raw entry bytes on the wire\n%s", raw)
	}
	vecs, _ := got["vectors"].([]any)
	if len(vecs) != 1 || vecs[0].(map[string]any)["kind"] != "measure" {
		t.Errorf("vectors = %#v, want one measure vector\n%s", got["vectors"], raw)
	}

	// A plain cohort omits the empty group / vector sections.
	plain, err := json.Marshal(&Schema{Fields: []Field{{Name: "a", Type: FieldTypeU8}}})
	if err != nil {
		t.Fatal(err)
	}
	var pm map[string]any
	_ = json.Unmarshal(plain, &pm)
	if _, ok := pm["groups"]; ok {
		t.Errorf("ungrouped schema carries groups: %s", plain)
	}
	if _, ok := pm["vectors"]; ok {
		t.Errorf("vector-free schema carries vectors: %s", plain)
	}
}

// TestFieldType_TextRoundTrip: the string type name decodes back to the
// same FieldType for every registered type, and an unknown name is
// refused rather than decoded as u8.
func TestFieldType_TextRoundTrip(t *testing.T) {
	t.Parallel()

	for ft := FieldType(0); ft < fieldTypeCount; ft++ {
		raw, err := json.Marshal(ft)
		if err != nil {
			t.Fatalf("%v: %v", ft, err)
		}
		if string(raw) != `"`+ft.String()+`"` {
			t.Errorf("marshal %d = %s, want %q", ft, raw, ft.String())
		}
		var back FieldType
		if err := json.Unmarshal(raw, &back); err != nil || back != ft {
			t.Errorf("unmarshal %s = %v (%v), want %v", raw, back, err, ft)
		}
	}
	var ft FieldType
	if err := json.Unmarshal([]byte(`"f65"`), &ft); err == nil {
		t.Errorf("unknown type name decoded as %v, want an error", ft)
	}
	var vk VectorKind
	if err := json.Unmarshal([]byte(`"scale"`), &vk); err != nil || vk != VectorKindScale {
		t.Errorf("vector kind scale decoded as %v (%v)", vk, err)
	}
	var gk GroupKind
	if err := json.Unmarshal([]byte(`"constant"`), &gk); err != nil || gk != GroupKindConstant {
		t.Errorf("group kind constant decoded as %v (%v)", gk, err)
	}
}
