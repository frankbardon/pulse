package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// indexTestSchema mirrors testSchema() (id u32, score f64) but adds a
// categorical region column so build tests can exercise both the
// plain-numeric and categorical key resolution paths.
func indexTestSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north") // id 0
	regionDict.Add("south") // id 1
	regionDict.Add("east")  // id 2

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 12, CsvColumnIdx: 2, Dictionary: regionDict},
		},
	}
}

func indexTestRecords() [][]uint64 {
	return [][]uint64{
		{1, math.Float64bits(10.0), 0}, // north
		{2, math.Float64bits(20.0), 1}, // south
		{3, math.Float64bits(30.0), 0}, // north (duplicate region key)
		{4, math.Float64bits(40.0), 2}, // east
		{5, math.Float64bits(50.0), 1}, // south (duplicate region key)
	}
}

func TestBuildIndex_WritesSidecarAtDerivedPath(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	wantPath := encoding.SidecarIndexPath("cohort.pulse", []string{"id"})
	if res.IndexPath != wantPath {
		t.Errorf("IndexPath = %q, want %q", res.IndexPath, wantPath)
	}

	exists, err := afero.Exists(cfg.Fs(), wantPath)
	if err != nil {
		t.Fatalf("afero.Exists: %v", err)
	}
	if !exists {
		t.Fatalf("sidecar not written at derived path %q", wantPath)
	}

	// Byte-stable read-back via the E1-S1 codec.
	got, err := encoding.ReadIndexFile(cfg.Fs(), wantPath)
	if err != nil {
		t.Fatalf("ReadIndexFile: %v", err)
	}
	if len(got.Keys) != 1 || got.Keys[0].Name != "id" || got.Keys[0].Type != encoding.FieldTypeU32 {
		t.Errorf("Keys = %+v, want [{id u32}]", got.Keys)
	}
}

func TestBuildIndex_KeyBytesAreOnWireNotFloat64(t *testing.T) {
	// A u32 "id" field: on-wire width is 4 bytes, little-endian. If the
	// build path went through a lossy float64/string round-trip (the
	// includeFilterer ParseFloat pattern this story explicitly avoids),
	// the stored key would either be a different byte width (e.g. 8
	// bytes for a raw float64 IEEE754 echo) or a decimal-string byte
	// sequence — neither matches the expected 4-byte LE encoding.
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	idx := res.Index
	found := false
	for _, b := range idx.Buckets {
		for _, e := range b.Entries {
			if len(e.Key) != 4 {
				t.Fatalf("key %v has width %d, want 4 (on-wire u32 width, not float64's 8)", e.Key, len(e.Key))
			}
			id := uint32(e.Key[0]) | uint32(e.Key[1])<<8 | uint32(e.Key[2])<<16 | uint32(e.Key[3])<<24
			if id == 3 {
				found = true
				if len(e.RowIDs) != 1 || e.RowIDs[0] != 2 {
					t.Errorf("id=3 RowIDs = %v, want [2]", e.RowIDs)
				}
			}
		}
	}
	if !found {
		t.Fatal("expected an entry for id=3 (row index 2)")
	}
}

func TestBuildIndex_CategoricalKeyIsOnWireDictionaryID(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	idx := res.Index
	if len(idx.Keys) != 1 || idx.Keys[0].Type != encoding.FieldTypeCategoricalU8 {
		t.Fatalf("Keys = %+v, want categorical_u8", idx.Keys)
	}

	// region is categorical_u8 -> 1-byte key. "north" is dictionary id 0,
	// "south" id 1, "east" id 2 (see indexTestSchema).
	rowIDsFor := map[byte][]uint64{}
	for _, b := range idx.Buckets {
		for _, e := range b.Entries {
			if len(e.Key) != 1 {
				t.Fatalf("categorical key width = %d, want 1", len(e.Key))
			}
			rowIDsFor[e.Key[0]] = e.RowIDs
		}
	}

	// north (id 0) -> rows 0, 2 (duplicate key -> multimap).
	if got := rowIDsFor[0]; len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("north (id 0) RowIDs = %v, want [0 2]", got)
	}
	// south (id 1) -> rows 1, 4.
	if got := rowIDsFor[1]; len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Errorf("south (id 1) RowIDs = %v, want [1 4]", got)
	}
	// east (id 2) -> row 3.
	if got := rowIDsFor[2]; len(got) != 1 || got[0] != 3 {
		t.Errorf("east (id 2) RowIDs = %v, want [3]", got)
	}
}

func TestBuildIndex_RowIDsPointAtCorrectRecords(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile cohort: %v", err)
	}
	loc, err := encoding.NewRecordLocator(bytes.NewReader(raw), schema)
	if err != nil {
		t.Fatalf("NewRecordLocator: %v", err)
	}
	reader := bytes.NewReader(raw)

	// Read every bucket entry back via the O(1) record locator and
	// confirm the record at that row-id really carries the indexed id.
	checked := 0
	for _, b := range res.Index.Buckets {
		for _, e := range b.Entries {
			wantID := uint32(e.Key[0]) | uint32(e.Key[1])<<8 | uint32(e.Key[2])<<16 | uint32(e.Key[3])<<24
			for _, rowID := range e.RowIDs {
				values := map[string]float64{}
				nulls := map[string]bool{}
				wide := map[string]any{}
				if err := loc.ReadRecordAt(reader, rowID, values, nulls, wide, nil, nil); err != nil {
					t.Fatalf("ReadRecordAt(%d): %v", rowID, err)
				}
				if uint32(values["id"]) != wantID {
					t.Errorf("row %d: id = %v, want %d", rowID, values["id"], wantID)
				}
				checked++
			}
		}
	}
	if checked != len(indexTestRecords()) {
		t.Errorf("checked %d row-ids, want %d (every record indexed)", checked, len(indexTestRecords()))
	}
}

func TestBuildIndex_FingerprintMatchesSourceContentHash(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile cohort: %v", err)
	}
	want, err := encoding.ComputeFingerprint(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if res.Index.Fingerprint != want {
		t.Errorf("Fingerprint = %x, want %x", res.Index.Fingerprint, want)
	}
}

func TestBuildIndex_Idempotent_ByteIdenticalRebuild(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res1, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"})
	if err != nil {
		t.Fatalf("BuildIndex (1st): %v", err)
	}
	bytes1, err := afero.ReadFile(cfg.Fs(), res1.IndexPath)
	if err != nil {
		t.Fatalf("ReadFile sidecar (1st): %v", err)
	}

	res2, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"})
	if err != nil {
		t.Fatalf("BuildIndex (2nd): %v", err)
	}
	bytes2, err := afero.ReadFile(cfg.Fs(), res2.IndexPath)
	if err != nil {
		t.Fatalf("ReadFile sidecar (2nd): %v", err)
	}

	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("rebuild is not byte-identical:\n 1st = % x\n 2nd = % x", bytes1, bytes2)
	}
}

func TestBuildIndex_NullKeyRowsSkipped(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	// 3 rows: id=1, id=null, id=3.
	ids := []uint64{1, 0, 3}
	nullRows := map[int]bool{1: true}
	for i, id := range ids {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, id); err != nil {
			t.Fatalf("WriteFieldValue: %v", err)
		}
		if bmSize := schema.BitmapByteSize(); bmSize > 0 {
			bm := make([]byte, bmSize)
			if nullRows[i] {
				encoding.BitmapSetNull(bm, 0)
			}
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	totalRowIDs := 0
	for _, b := range res.Index.Buckets {
		for _, e := range b.Entries {
			totalRowIDs += len(e.RowIDs)
			for _, rid := range e.RowIDs {
				if rid == 1 {
					t.Errorf("null-key row (row-id 1) must not be indexed, found in entry %v", e)
				}
			}
		}
	}
	if totalRowIDs != 2 {
		t.Errorf("total indexed row-ids = %d, want 2 (row-id 1 skipped for null key)", totalRowIDs)
	}
}

func TestBuildIndex_UnknownKeyField(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"does_not_exist"})
	if err == nil {
		t.Fatal("expected error for unknown key field")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestBuildIndex_UnsupportedKeyFieldType(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("VISA")
	dict.Add("MC")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: dict},
		},
	}
	records := [][]uint64{
		{1, 1},
		{2, 2},
	}
	cfg := setupTestFS(t, "cohort.pulse", schema, records)
	svc := New(cfg)

	_, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"tags"})
	if err == nil {
		t.Fatal("expected error for unsupported (set_u8) key field type")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestBuildIndex_CompositeKeyRejected(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id", "region"})
	if err == nil {
		t.Fatal("expected error for composite (multi-column) key request")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestBuildIndex_EmptyKeyFieldsRejected(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.BuildIndex(context.Background(), "cohort.pulse", nil)
	if err == nil {
		t.Fatal("expected error for empty key-fields list")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestBuildIndex_ShardArchiveRejected(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	_, err := svc.BuildIndex(context.Background(), "arch.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error building an index against a shard archive")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}
