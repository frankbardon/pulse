package descriptor

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// writeShardPayload returns the bytes of a single-file .pulse (header +
// schema + nRecord placeholder records computed from the schema's
// per-record byte size). The records themselves are zero-filled — the
// only thing tests assert is the count, which encoding.PeekShardRecordCount
// derives from (payload_size - header - schema) / record_size.
func writeShardPayload(t *testing.T, schema *encoding.Schema, nRecord int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	// Append nRecord zero-filled records.
	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}
	if recordSize > 0 {
		buf.Write(make([]byte, recordSize*nRecord))
	}
	return buf.Bytes()
}

// buildShardArchiveBytes composes a zip archive carrying the canonical
// _schema.pulse entry plus the named shards in the supplied order. The
// canonical aggregate count in the schema-doc is the sum of every
// shard's record count.
func buildShardArchiveBytes(t *testing.T, schema *encoding.Schema, shards []struct {
	Name    string
	NRecord int
}) []byte {
	t.Helper()
	var doc bytes.Buffer
	var total uint64
	for _, s := range shards {
		total += uint64(s.NRecord)
	}
	if err := encoding.WriteSchemaDoc(&doc, schema, total, uint16(len(shards))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}
	write(encoding.ReservedSchemaName, doc.Bytes())
	for _, s := range shards {
		write(s.Name, writeShardPayload(t, schema, s.NRecord))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func twoShardInspectSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, Description: "Unique identifier for each record", ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Test score for the participant", ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
}

// TestShardArchiveInspect — non-skippable gate. Inspect on a shard
// archive exposes Shards in central-directory order with per-shard
// RecordCount and a cumulative aggregate matching the sum.
func TestShardArchiveInspect(t *testing.T) {
	schema := twoShardInspectSchema(t)
	shards := []struct {
		Name    string
		NRecord int
	}{
		{Name: "20190101.pulse", NRecord: 5},
		{Name: "20190108.pulse", NRecord: 7},
		{Name: "20190115.pulse", NRecord: 3},
	}
	data := buildShardArchiveBytes(t, schema, shards)

	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	result, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatal("Data is not *InspectResult")
	}

	// Schema came from the canonical _schema.pulse.
	if result.FieldCount != 2 {
		t.Errorf("FieldCount = %d, want 2", result.FieldCount)
	}
	if len(result.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(result.Fields))
	}
	if result.Fields[0].Name != "id" || result.Fields[1].Name != "score" {
		t.Errorf("Fields = %q,%q; want id,score",
			result.Fields[0].Name, result.Fields[1].Name)
	}

	// Shards populated in central-directory order with correct counts.
	if len(result.Shards) != 3 {
		t.Fatalf("Shards len = %d, want 3", len(result.Shards))
	}
	var sum int64
	for i, want := range shards {
		got := result.Shards[i]
		if got.Filename != want.Name {
			t.Errorf("Shards[%d].Filename = %q, want %q", i, got.Filename, want.Name)
		}
		if got.RecordCount != int64(want.NRecord) {
			t.Errorf("Shards[%d].RecordCount = %d, want %d", i, got.RecordCount, want.NRecord)
		}
		sum += got.RecordCount
	}
	if result.RecordCount != sum {
		t.Errorf("aggregate RecordCount = %d, want %d", result.RecordCount, sum)
	}
}

// TestShardArchiveInspect_SingleFileEmptyShards keeps the single-file
// path's surface contract: Shards is an empty (non-nil) slice so the
// JSON envelope emits "[]" not "null", and RecordCount stays zero.
func TestShardArchiveInspect_SingleFileEmptyShards(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Single-file score column"},
		},
	}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	result := env.Data.(*InspectResult)
	if result.Shards == nil {
		t.Fatal("Shards is nil; want empty slice")
	}
	if len(result.Shards) != 0 {
		t.Errorf("Shards len = %d, want 0", len(result.Shards))
	}
	if result.RecordCount != 0 {
		t.Errorf("RecordCount = %d, want 0", result.RecordCount)
	}
}

// TestShardArchiveInspect_CanonicalSchemaWins confirms the canonical
// schema (with its description metadata) populates Fields even when
// shard payloads might shape differently — the canonical entry is the
// single source of truth for the cohort's schema view.
func TestShardArchiveInspect_CanonicalSchemaWins(t *testing.T) {
	schema := twoShardInspectSchema(t)
	data := buildShardArchiveBytes(t, schema, []struct {
		Name    string
		NRecord int
	}{
		{Name: "a.pulse", NRecord: 1},
		{Name: "b.pulse", NRecord: 2},
	})
	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	result := env.Data.(*InspectResult)
	if result.Fields[1].Description != "Test score for the participant" {
		t.Errorf("canonical description not surfaced: got %q",
			result.Fields[1].Description)
	}
	if result.Fields[1].DescriptionSource != "schema" {
		t.Errorf("DescriptionSource = %q, want schema",
			result.Fields[1].DescriptionSource)
	}
}
