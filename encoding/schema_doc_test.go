package encoding_test

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

func sampleSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"US", "CA"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("seed dict: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0,
				Description: "primary identifier"},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Description: "ISO country", Dictionary: dict},
		},
	}
}

func TestSchemaDoc_WriteRead_RoundTrip(t *testing.T) {
	schema := sampleSchema(t)

	var buf bytes.Buffer
	if err := encoding.WriteSchemaDoc(&buf, schema, 4242, 7); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}

	doc, err := encoding.ReadSchemaDoc(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadSchemaDoc: %v", err)
	}
	if doc.AggregateRecordCount != 4242 {
		t.Errorf("AggregateRecordCount = %d, want 4242", doc.AggregateRecordCount)
	}
	if doc.ShardCount != 7 {
		t.Errorf("ShardCount = %d, want 7", doc.ShardCount)
	}
	if doc.Schema == nil || len(doc.Schema.Fields) != 2 {
		t.Fatalf("Schema fields = %d, want 2", len(doc.Schema.Fields))
	}
	if doc.Schema.Fields[0].Name != "id" || doc.Schema.Fields[1].Name != "country" {
		t.Errorf("schema fields mismatch: %+v", doc.Schema.Fields)
	}
	if dict, ok := doc.Schema.Categorical("country"); !ok || dict.Count() != 2 {
		t.Errorf("country dict not roundtripped: ok=%v count=%d", ok, dict.Count())
	}
}

func TestSchemaDoc_ReadWithoutExtension_ReturnsZeroMetadata(t *testing.T) {
	schema := sampleSchema(t)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	// No SHRD trailer — simulates an older _schema.pulse or an
	// incidental single-file cohort read through ReadSchemaDoc.

	doc, err := encoding.ReadSchemaDoc(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadSchemaDoc without trailer: %v", err)
	}
	if doc.AggregateRecordCount != 0 || doc.ShardCount != 0 {
		t.Errorf("expected zero metadata, got agg=%d count=%d",
			doc.AggregateRecordCount, doc.ShardCount)
	}
}
