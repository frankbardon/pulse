package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// writeCategoricalShard emits a single-file .pulse with one u32 id and
// one categorical_u8 country column. records is a slice of (id, dictIdx).
func writeCategoricalShard(t *testing.T, dictValues []string, records [][2]uint32) []byte {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range dictValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: d},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range records {
		var rec [5]byte
		binary.LittleEndian.PutUint32(rec[0:4], r[0])
		rec[4] = byte(r[1])
		buf.Write(rec[:])
	}
	return buf.Bytes()
}

// TestShardArchiveDictUnionMerge confirms CreateShardArchive accepts
// shards with divergent (non-prefix-related) categorical dictionaries:
// the canonical _schema.pulse holds the union, divergent shards are
// rewritten with remapped indices, and downstream Process resolves
// every record to the original string value across shards.
func TestShardArchiveDictUnionMerge(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	// Shard A: countries [US, CA, MX].
	// Shard B: countries [US, CA, BR] — BR diverges from MX, neither
	// is a prefix of the other.
	shardA := writeCategoricalShard(t,
		[]string{"US", "CA", "MX"},
		[][2]uint32{{1, 0}, {2, 1}, {3, 2}}) // US, CA, MX
	shardB := writeCategoricalShard(t,
		[]string{"US", "CA", "BR"},
		[][2]uint32{{4, 0}, {5, 1}, {6, 2}}) // US, CA, BR

	if err := afero.WriteFile(fsys, "a.pulse", shardA, 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := afero.WriteFile(fsys, "b.pulse", shardB, 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}

	if err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive with divergent dicts: %v", err)
	}

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open archive: %v", err)
	}
	dict := cohort.Schema().Fields[1].Dictionary
	got := dict.Values()
	want := []string{"US", "CA", "MX", "BR"}
	if len(got) != len(want) {
		t.Fatalf("canonical dict size: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("canonical dict[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "country"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	gotCounts := map[string]float64{}
	for _, row := range resp.Data {
		k, _ := row["country"].(string)
		v, _ := row["n"].(float64)
		gotCounts[k] = v
	}
	wantCounts := map[string]float64{"US": 2, "CA": 2, "MX": 1, "BR": 1}
	for k, v := range wantCounts {
		if gotCounts[k] != v {
			t.Errorf("country=%s: got %v, want %v (full=%v)", k, gotCounts[k], v, gotCounts)
		}
	}
}

// TestShardArchiveDictUnionAddShard confirms AddShard against an
// existing archive accepts a shard whose categorical dictionary
// diverges from canonical: the archive's canonical dict grows to the
// union and the new shard's records resolve correctly via Process.
func TestShardArchiveDictUnionAddShard(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	seed := writeCategoricalShard(t,
		[]string{"US", "CA"},
		[][2]uint32{{1, 0}, {2, 1}}) // US, CA
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive seed: %v", err)
	}

	// Divergent additional shard.
	add := writeCategoricalShard(t,
		[]string{"US", "BR"},
		[][2]uint32{{3, 0}, {4, 1}}) // US, BR
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}
	if err := svc.AddShard(context.Background(), "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard divergent: %v", err)
	}

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dict := cohort.Schema().Fields[1].Dictionary.Values()
	want := []string{"US", "CA", "BR"}
	if len(dict) != len(want) {
		t.Fatalf("canonical dict: got %v, want %v", dict, want)
	}
	for i := range want {
		if dict[i] != want[i] {
			t.Errorf("dict[%d]: got %q, want %q", i, dict[i], want[i])
		}
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "country"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	gotCounts := map[string]float64{}
	for _, row := range resp.Data {
		k, _ := row["country"].(string)
		v, _ := row["n"].(float64)
		gotCounts[k] = v
	}
	wantCounts := map[string]float64{"US": 2, "CA": 1, "BR": 1}
	for k, v := range wantCounts {
		if gotCounts[k] != v {
			t.Errorf("country=%s: got %v, want %v (full=%v)", k, gotCounts[k], v, gotCounts)
		}
	}
}
