package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func TestListIndexes_EmptyWhenNoneBuilt(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d indexes, want 0", len(got))
	}
}

func TestListIndexes_EnumeratesEveryBuiltSidecar(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex(id): %v", err)
	}
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"}); err != nil {
		t.Fatalf("BuildIndex(region): %v", err)
	}
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region", "id"}); err != nil {
		t.Fatalf("BuildIndex(region,id): %v", err)
	}

	got, err := svc.ListIndexes(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ListIndexes: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d indexes, want 3: %+v", len(got), got)
	}

	byKeyCount := map[int]bool{}
	for _, info := range got {
		byKeyCount[len(info.Keys)] = true
		if info.IndexPath == "" {
			t.Errorf("IndexInfo.IndexPath empty for %+v", info)
		}
		if info.DistinctKeys == 0 {
			t.Errorf("IndexInfo.DistinctKeys = 0, want > 0 for %+v", info)
		}
		if info.IndexedRecords == 0 {
			t.Errorf("IndexInfo.IndexedRecords = 0, want > 0 for %+v", info)
		}
	}
	if !byKeyCount[1] || !byKeyCount[2] {
		t.Errorf("expected both a single-key and a composite-key entry, got key counts %v", byKeyCount)
	}

	// A single-key "id" build entry must report Keys == ["id"].
	found := false
	for _, info := range got {
		if len(info.Keys) == 1 && info.Keys[0] == "id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an entry with Keys == [id], got %+v", got)
	}
}

func TestListIndexes_ShardArchiveRejected(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	_, err := svc.ListIndexes(context.Background(), "arch.pulse")
	if err == nil {
		t.Fatal("expected error listing indexes for a shard archive")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED) {
		t.Errorf("expected PULSE_INDEX_UNSUPPORTED_SHARDED, got: %v", err)
	}
}

func TestDropIndex_RemovesSidecar(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	exists, err := afero.Exists(cfg.Fs(), res.IndexPath)
	if err != nil || !exists {
		t.Fatalf("precondition: sidecar not present before drop (exists=%v err=%v)", exists, err)
	}

	if err := svc.DropIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("DropIndex: %v", err)
	}

	exists, err = afero.Exists(cfg.Fs(), res.IndexPath)
	if err != nil {
		t.Fatalf("afero.Exists after drop: %v", err)
	}
	if exists {
		t.Errorf("sidecar %q still present after DropIndex", res.IndexPath)
	}
}

func TestDropIndex_DoesNotAffectOtherSidecars(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex(id): %v", err)
	}
	regionRes, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"})
	if err != nil {
		t.Fatalf("BuildIndex(region): %v", err)
	}

	if err := svc.DropIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("DropIndex(id): %v", err)
	}

	exists, err := afero.Exists(cfg.Fs(), regionRes.IndexPath)
	if err != nil {
		t.Fatalf("afero.Exists: %v", err)
	}
	if !exists {
		t.Error("dropping the id index must not remove the region index sidecar")
	}
}

func TestDropIndex_MissingIndex(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	err := svc.DropIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error dropping an index that was never built")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("expected PULSE_INDEX_MISSING, got: %v", err)
	}
}

func TestDropIndex_EmptyKeyFieldsRejected(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	err := svc.DropIndex(context.Background(), "cohort.pulse", nil)
	if err == nil {
		t.Fatal("expected error for empty key-fields list")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestDropIndex_ShardArchiveRejected(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	err := svc.DropIndex(context.Background(), "arch.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error dropping an index against a shard archive")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED) {
		t.Errorf("expected PULSE_INDEX_UNSUPPORTED_SHARDED, got: %v", err)
	}
}
