package io

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

func TestParseSchemaFieldType(t *testing.T) {
	for _, name := range RegisteredFieldTypeNames() {
		ft, err := ParseSchemaFieldType("col", name)
		if err != nil {
			t.Fatalf("ParseSchemaFieldType(%q): %v", name, err)
		}
		if got := ft.String(); got != name {
			t.Errorf("ParseSchemaFieldType(%q).String() = %q", name, got)
		}
	}
}

// An unknown type name is refused, never coerced to f64 (the old
// fallback turned a typo into a silently mistyped column).
func TestParseSchemaFieldType_UnknownRefused(t *testing.T) {
	for _, name := range []string{"unknown_type", "float", "F64", ""} {
		_, err := ParseSchemaFieldType("price", name)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN {
			t.Fatalf("ParseSchemaFieldType(%q) err = %v, want PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN", name, err)
		}
		if ce.Details["field"] != "price" || ce.Details["type"] != name {
			t.Errorf("details = %v, want field=price type=%q", ce.Details, name)
		}
		valid, _ := ce.Details["valid"].([]string)
		known := 0
		for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
			known++
		}
		if len(valid) != known || valid[0] != "u8" || valid[len(valid)-1] != "set_u256" {
			t.Errorf("details.valid = %v, want the %d registered names in type-byte order", valid, known)
		}
	}
}

// The bit-packed source: every bit-packed field is followed by a
// byte-aligned one, which is exactly the order a ByteSize() offset sum
// got wrong (u4 / packed_bool report ByteSize()==0 but occupy a byte).
var bitPackedHeader = []string{"flag", "level", "id", "ok", "score"}

func bitPackedRows() [][]string {
	return [][]string{
		{"true", "3", "70000", "false", "1.5"},
		{"false", "12", "4000000", "true", "2.25"},
		{"true", "0", "1", "true", "-0.5"},
	}
}

const bitPackedSchemaDoc = `[
  {"name": "flag",  "type": "packed_bool"},
  {"name": "level", "type": "u4"},
  {"name": "id",    "type": "u32"},
  {"name": "ok",    "type": "packed_bool"},
  {"name": "score", "type": "f64"}
]`

func inferredBitPackedSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	res, err := InferSchemaWithOptions(&stringRowsReader{header: bitPackedHeader, rows: bitPackedRows()}, InferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Schema
}

// A schema file is laid out by the canonical routine: its schema equals
// the one inference builds for the same fields, offsets and bit
// positions included.
func TestParseSchemaJSON_BitPackedLayoutMatchesInference(t *testing.T) {
	got, err := ParseSchemaJSON([]byte(bitPackedSchemaDoc))
	if err != nil {
		t.Fatal(err)
	}
	want := inferredBitPackedSchema(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema file layout diverges from inference:\n got  %+v\n want %+v", got.Fields, want.Fields)
	}
	offsets := make([]int, len(got.Fields))
	for i, f := range got.Fields {
		offsets[i] = f.ByteOffset
	}
	if !reflect.DeepEqual(offsets, []int{0, 1, 2, 6, 7}) {
		t.Fatalf("offsets = %v, want [0 1 2 6 7]", offsets)
	}
}

// End to end: a schema FILE mixing bit-packed and byte-aligned fields,
// declared in a different order than the source, imports and reads back
// every value, and the cohort's schema block is the one an inferred
// import of the same source writes — so the two are structurally
// cohesive shards.
func TestImport_SchemaFileBitPacked_RoundTrip(t *testing.T) {
	fs := afero.NewMemMapFs()
	const reordered = `[
  {"name": "score", "type": "f64"},
  {"name": "ok",    "type": "packed_bool"},
  {"name": "id",    "type": "u32"},
  {"name": "level", "type": "u4"},
  {"name": "flag",  "type": "packed_bool"}
]`
	if err := afero.WriteFile(fs, "schema.json", []byte(reordered), 0o644); err != nil {
		t.Fatal(err)
	}
	schema, err := LoadSchemaFile(fs, "schema.json")
	if err != nil {
		t.Fatal(err)
	}
	job := &ImportJob{Source: &stringRowsReader{header: bitPackedHeader, rows: bitPackedRows()}, Target: "file.pulse", FS: fs, Schema: schema}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, vals, _ := decodeAll(t, fs, "file.pulse")
	want := []map[string]float64{
		{"score": 1.5, "ok": 0, "id": 70000, "level": 3, "flag": 1},
		{"score": 2.25, "ok": 1, "id": 4000000, "level": 12, "flag": 0},
		{"score": -0.5, "ok": 1, "id": 1, "level": 0, "flag": 1},
	}
	if !reflect.DeepEqual(vals, want) {
		t.Fatalf("values = %v, want %v", vals, want)
	}
	gotOffsets := make([]int, len(got.Fields))
	for i, f := range got.Fields {
		gotOffsets[i] = f.ByteOffset
	}
	if !reflect.DeepEqual(gotOffsets, []int{0, 8, 9, 13, 14}) {
		t.Fatalf("written offsets = %v, want [0 8 9 13 14]", gotOffsets)
	}

	// The same fields in the source's order, from a schema file and from
	// inference, must write the same schema block.
	if err := afero.WriteFile(fs, "inorder.json", []byte(bitPackedSchemaDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	inorder, err := LoadSchemaFile(fs, "inorder.json")
	if err != nil {
		t.Fatal(err)
	}
	for path, s := range map[string]*encoding.Schema{"explicit.pulse": inorder, "inferred.pulse": nil} {
		j := &ImportJob{Source: &stringRowsReader{header: bitPackedHeader, rows: bitPackedRows()}, Target: path, FS: fs, Schema: s}
		if _, err := j.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	explicit, _, _ := decodeAll(t, fs, "explicit.pulse")
	inferred, _, _ := decodeAll(t, fs, "inferred.pulse")
	if !reflect.DeepEqual(explicit.Fields, inferred.Fields) {
		t.Fatalf("schema-file cohort schema diverges from inferred:\n explicit %+v\n inferred %+v", explicit.Fields, inferred.Fields)
	}
	if _, err := encx.ValidateStructuralCohesion(inferred, explicit); err != nil {
		t.Fatalf("schema-file cohort is not cohesive with the inferred one: %v", err)
	}
}

// The schema-template document round-trips through ParseSchemaJSON,
// decimal precision and scale included.
func TestSchemaFileTemplate_RoundTrips(t *testing.T) {
	src := &encoding.Schema{Fields: []encoding.Field{
		{Name: "flag", Type: encoding.FieldTypePackedBool, Description: "dropped"},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 12, Scale: 2},
		{Name: "level", Type: encoding.FieldTypeU4},
	}}
	encx.LayoutFields(src.Fields)
	for i := range src.Fields {
		src.Fields[i].CsvColumnIdx = i
	}
	doc, err := json.Marshal(SchemaFileTemplate(src))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSchemaJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	src.Fields[0].Description = ""
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("round trip = %+v, want %+v", got.Fields, src.Fields)
	}
	var raw []map[string]any
	if err := json.Unmarshal(doc, &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw[0]["precision"]; has {
		t.Errorf("non-decimal field carries precision: %v", raw[0])
	}
}

// TestParseSchemaJSON_MalformedIsCoded: a document that is not an array
// of field objects is PULSE_IMPORT_SCHEMA_INVALID (reason malformed), not
// an uncoded error an envelope can only report under a placeholder.
func TestParseSchemaJSON_MalformedIsCoded(t *testing.T) {
	for _, doc := range []string{`{"name":"a"}`, `[{"name":"a","type":"u8"`, `"u8"`} {
		_, err := ParseSchemaJSON([]byte(doc))
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_SCHEMA_INVALID {
			t.Fatalf("ParseSchemaJSON(%s) err = %v, want PULSE_IMPORT_SCHEMA_INVALID", doc, err)
		}
		if ce.Details["reason"] != "malformed" {
			t.Errorf("ParseSchemaJSON(%s) reason = %v, want malformed", doc, ce.Details["reason"])
		}
	}
}

// TestSchemaFromFields_EmptyRefused: an explicit schema with no fields
// cannot describe a cohort; it is refused rather than read as "infer".
func TestSchemaFromFields_EmptyRefused(t *testing.T) {
	for _, run := range []func() error{
		func() error { _, err := SchemaFromFields(nil); return err },
		func() error { _, err := ParseSchemaJSON([]byte(`[]`)); return err },
	} {
		err := run()
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_SCHEMA_INVALID || ce.Details["reason"] != "empty" {
			t.Fatalf("err = %v, want PULSE_IMPORT_SCHEMA_INVALID reason empty", err)
		}
	}
}

// TestSchemaFromFields_MatchesParseSchemaJSON: the entry-level builder
// and the document decoder lay a schema out identically.
func TestSchemaFromFields_MatchesParseSchemaJSON(t *testing.T) {
	defs := []SchemaFileField{
		{Name: "flag", Type: "packed_bool", Nullable: true},
		{Name: "n", Type: "u16", Description: "count"},
		{Name: "amt", Type: "decimal128", Precision: 12, Scale: 2},
	}
	raw, err := json.Marshal(defs)
	if err != nil {
		t.Fatal(err)
	}
	fromDoc, err := ParseSchemaJSON(raw)
	if err != nil {
		t.Fatalf("ParseSchemaJSON: %v", err)
	}
	fromDefs, err := SchemaFromFields(defs)
	if err != nil {
		t.Fatalf("SchemaFromFields: %v", err)
	}
	if !reflect.DeepEqual(fromDoc, fromDefs) {
		t.Errorf("SchemaFromFields = %+v, ParseSchemaJSON = %+v", fromDefs, fromDoc)
	}
}
