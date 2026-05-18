package service

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/spf13/afero"
)

// idScoreRecords builds a small fixture with deterministic ids and
// scores so include-set filters can be reasoned about exactly.
func idScoreSchemaAndRecords() (*encoding.Schema, [][]uint64) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
	}}
	records := [][]uint64{
		{101, math.Float64bits(10.0)},
		{102, math.Float64bits(20.0)},
		{103, math.Float64bits(30.0)},
		{104, math.Float64bits(40.0)},
		{105, math.Float64bits(50.0)},
	}
	return schema, records
}

// loadSet loads an include-set against the file at path using svc's
// schema-resolution helper. Mirrors what the CLI does for callers.
func loadSet(t *testing.T, svc *Service, path string, field string, raw string) processing.MemberSet {
	t.Helper()
	schema, err := svc.ResolveCanonicalSchema(context.Background(), path)
	if err != nil {
		t.Fatalf("ResolveCanonicalSchema: %v", err)
	}
	res, err := processing.LoadMemberSetFromReader(strings.NewReader(raw), schema, field)
	if err != nil {
		t.Fatalf("LoadMemberSetFromReader: %v", err)
	}
	return res.Set
}

func TestFilterToFileBySet_SingleFile_IntegerField(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	set := loadSet(t, svc, "src.pulse", "id", "102\n104\n")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "id", set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2", written)
	}

	// Re-process the output and confirm only ids 102 and 104 made it.
	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	count, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount: %v", err)
	}
	if count != 2 {
		t.Errorf("dst RecordCount = %d, want 2", count)
	}
}

func TestFilterToFileBySet_SingleFile_CategoricalField(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
	}}
	records := [][]uint64{
		{0, math.Float64bits(10.0)}, // red
		{1, math.Float64bits(20.0)}, // green
		{0, math.Float64bits(30.0)}, // red
		{2, math.Float64bits(40.0)}, // blue
		{1, math.Float64bits(50.0)}, // green
	}
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	set := loadSet(t, svc, "src.pulse", "color", "red\nblue\n")
	if _, ok := set.(*processing.BitsetSet); !ok {
		t.Fatalf("set is %T, want *BitsetSet", set)
	}

	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "color", set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3 (2 red + 1 blue)", written)
	}
}

func TestFilterToFileBySet_CombinedWithFilter(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	// Set says {101,102,103,104,105} → all rows match the set; expression
	// narrows to score > 25. Surviving ids: 103 (30), 104 (40), 105 (50).
	set := loadSet(t, svc, "src.pulse", "id", "101\n102\n103\n104\n105\n")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "id", set, "score > 25.0")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3", written)
	}

	// Set narrows to {101,103} ∩ score > 25 ⇒ {103}.
	set = loadSet(t, svc, "src.pulse", "id", "101\n103\n")
	written, err = svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "id", set, "score > 25.0")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr (narrowed): %v", err)
	}
	if written != 1 {
		t.Errorf("written = %d, want 1", written)
	}
}

func TestFilterToFileBySet_EmptySet_ZeroRows(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	set := loadSet(t, svc, "src.pulse", "id", "")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "id", set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 0 {
		t.Errorf("written = %d, want 0", written)
	}
}

func TestFilterToFileBySet_RequiresAtLeastOnePredicate(t *testing.T) {
	cfg := setupTestFS(t, "src.pulse", testSchema(), testRecords())
	svc := New(cfg)

	_, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "", nil, "")
	if err == nil {
		t.Fatal("expected error when both set and filterExpr are empty")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("err = %v, want SERVICE_VALIDATION", err)
	}
}

func TestFilterToFileBySet_RequiresIncludeField(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	set := loadSet(t, svc, "src.pulse", "id", "101\n")
	_, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "", set, "")
	if err == nil {
		t.Fatal("expected error when include field is empty but set is non-nil")
	}
}

func TestFilterToFileBySet_UnknownField(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	// Build a set against a real field, then try to filter against a
	// different unknown field — service-side BuildMemberSetPredicate
	// must reject it.
	set := loadSet(t, svc, "src.pulse", "id", "101\n")
	_, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "nope", set, "")
	if err == nil {
		t.Fatal("expected error for unknown include field")
	}
}

func TestFilterToFileBySet_FilterExprOnlyMatchesOldPath(t *testing.T) {
	// Empty set + non-empty filter expr should behave like the existing
	// FilterToFile path (regression coverage).
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "src.pulse", "dst.pulse", "", nil, "score > 25.0")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3", written)
	}
}

func TestFilterToFileBySet_ShardArchive_IntegerField(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, cfg := setupShardArchive(t, "arch.pulse", schema, shards, concat)
	_ = cfg

	// Include set on `id` across all three shards: pick one row per shard.
	set := loadSet(t, svc, "arch.pulse", "id", "2\n6\n11\n")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "arch.pulse", "dst.pulse", "id", set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3 (one per shard)", written)
	}

	// Confirm the output archive is a shard archive with three shards.
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	if !isShardArchiveMagic(dstBytes) {
		t.Fatal("dst should be shard archive (input is archive)")
	}
	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if len(cohort.Shards()) != 3 {
		t.Errorf("dst shard count = %d, want 3", len(cohort.Shards()))
	}
}

func TestFilterToFileBySet_ShardArchive_CombinedWithFilter(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	// Set: ids 1..12 (all rows); expr: score > 90.
	// Survivors: 100, 110, 120 (ids 10, 11, 12) = 3 rows in shard c.
	set := loadSet(t, svc, "arch.pulse", "id",
		"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "arch.pulse", "dst.pulse", "id", set, "score > 90.0")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr: %v", err)
	}
	if written != 3 {
		t.Errorf("written = %d, want 3", written)
	}
}

func TestFilterToFileBySet_Anchor_SingleShard(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	// Anchor on shard b (ids 5..8); set picks {5, 7}.
	set := loadSet(t, svc, "arch.pulse#b.pulse", "id", "5\n7\n")
	written, err := svc.FilterToFileBySetAndExpr(context.Background(), "arch.pulse#b.pulse", "dst.pulse", "id", set, "")
	if err != nil {
		t.Fatalf("FilterToFileBySetAndExpr anchor: %v", err)
	}
	if written != 2 {
		t.Errorf("written = %d, want 2", written)
	}
}

func TestResolveCanonicalSchema_SingleFile(t *testing.T) {
	schema, records := idScoreSchemaAndRecords()
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	got, err := svc.ResolveCanonicalSchema(context.Background(), "src.pulse")
	if err != nil {
		t.Fatalf("ResolveCanonicalSchema: %v", err)
	}
	if len(got.Fields) != 2 || got.Fields[0].Name != "id" || got.Fields[1].Name != "score" {
		t.Errorf("schema mismatch: %+v", got.Fields)
	}
}

func TestResolveCanonicalSchema_ShardArchive(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	got, err := svc.ResolveCanonicalSchema(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("ResolveCanonicalSchema: %v", err)
	}
	if len(got.Fields) == 0 {
		t.Fatal("schema fields empty")
	}
}

func TestResolveCanonicalSchema_Anchor(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	got, err := svc.ResolveCanonicalSchema(context.Background(), "arch.pulse#b.pulse")
	if err != nil {
		t.Fatalf("ResolveCanonicalSchema: %v", err)
	}
	if len(got.Fields) == 0 {
		t.Fatal("anchor schema fields empty")
	}
	_ = schema
}
