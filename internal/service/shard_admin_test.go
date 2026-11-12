package service

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// adminScoreSchema is the test schema used across shard-admin gates: a
// u32 id plus an f64 score. Matches the layout used by shard_iter_test
// so payloads round-trip identically.
func adminScoreSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
}

// adminWriteShardFile writes a complete single-file .pulse at path on
// fsys. Returns the bytes for callers that want a parallel handle.
func adminWriteShardFile(t *testing.T, fsys afero.Fs, path string, schema *encoding.Schema, records [][]uint64) []byte {
	t.Helper()
	data := writeSinglePulse(t, schema, records)
	if err := afero.WriteFile(fsys, path, data, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return data
}

// adminTwoSimpleShards returns two non-colliding shard payloads
// pre-written to fsys with deterministic record content.
func adminTwoSimpleShards(t *testing.T, fsys afero.Fs) (schema *encoding.Schema, shardA, shardB string) {
	t.Helper()
	schema = adminScoreSchema()
	shardA = "a.pulse"
	shardB = "b.pulse"
	adminWriteShardFile(t, fsys, shardA, schema, [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
	})
	adminWriteShardFile(t, fsys, shardB, schema, [][]uint64{
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
	})
	return schema, shardA, shardB
}

// CreateShardArchive and AddShard must reject any source path whose
// basename equals the reserved `_schema.pulse` entry. The error code is
// PULSE_SHARD_RESERVED_NAME and surfaces at the precheck (no I/O).
func TestShardArchiveReservedName(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	// Reserved name as one of the create-list paths.
	schema := adminScoreSchema()
	good := "good.pulse"
	reserved := encx.ReservedSchemaName
	adminWriteShardFile(t, fsys, good, schema, [][]uint64{{1, math.Float64bits(1.0)}})
	adminWriteShardFile(t, fsys, reserved, schema, [][]uint64{{2, math.Float64bits(2.0)}})

	_, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{good, reserved})
	if err == nil {
		t.Fatal("CreateShardArchive with reserved basename: expected error")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_RESERVED_NAME) {
		t.Errorf("Create reserved: code = %v, want PULSE_SHARD_RESERVED_NAME", err)
	}

	// Even when path is nested, reserved basename still rejected.
	nestedDir := "subdir"
	if err := fsys.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	adminWriteShardFile(t, fsys, filepath.Join(nestedDir, reserved), schema,
		[][]uint64{{3, math.Float64bits(3.0)}})
	_, err = svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{good, filepath.Join(nestedDir, reserved)})
	if !errors.HasCode(err, errors.PULSE_SHARD_RESERVED_NAME) {
		t.Errorf("Create reserved (nested path): code = %v, want PULSE_SHARD_RESERVED_NAME", err)
	}

	// AddShard against an existing archive must reject reserved basename
	// too, surfacing the precheck before any archive read happens.
	_, err = svc.CreateShardArchive(context.Background(), "arch.pulse", []string{good})
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	_, err = svc.AddShard(context.Background(), "arch.pulse", reserved)
	if !errors.HasCode(err, errors.PULSE_SHARD_RESERVED_NAME) {
		t.Errorf("Add reserved: code = %v, want PULSE_SHARD_RESERVED_NAME", err)
	}
}

// Both Create (two source paths sharing a basename) and Add (incoming
// basename matches an entry already in the archive) must raise
// PULSE_SHARD_NAME_COLLISION.
func TestShardArchiveNameCollision(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	schema := adminScoreSchema()
	// Two sibling directories holding a file with the same basename.
	if err := fsys.MkdirAll("dirA", 0o755); err != nil {
		t.Fatalf("MkdirAll dirA: %v", err)
	}
	if err := fsys.MkdirAll("dirB", 0o755); err != nil {
		t.Fatalf("MkdirAll dirB: %v", err)
	}
	pathA := "dirA/shard.pulse"
	pathB := "dirB/shard.pulse"
	adminWriteShardFile(t, fsys, pathA, schema, [][]uint64{{1, math.Float64bits(10.0)}})
	adminWriteShardFile(t, fsys, pathB, schema, [][]uint64{{2, math.Float64bits(20.0)}})

	_, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{pathA, pathB})
	if err == nil {
		t.Fatal("CreateShardArchive duplicate basename: expected error")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_NAME_COLLISION) {
		t.Errorf("Create collision: code = %v, want PULSE_SHARD_NAME_COLLISION", err)
	}

	// AddShard against an archive that already contains the basename.
	// Seed the archive with shard.pulse, then attempt to add another
	// file from a different directory but with the same basename.
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{pathA}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	_, err = svc.AddShard(context.Background(), "arch.pulse", pathB)
	if err == nil {
		t.Fatal("AddShard duplicate basename: expected error")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_NAME_COLLISION) {
		t.Errorf("Add collision: code = %v, want PULSE_SHARD_NAME_COLLISION", err)
	}
}

// `pulse.Open("archive.pulse#shard.pulse")` must:
//   - return a one-shard cohort whose schema comes from the anchored
//     shard's own header,
//   - leave Cohort.Shards() empty (anchor stands alone),
//   - allow Process to run identically vs opening the standalone shard,
//   - raise PULSE_SHARD_MISSING for an absent anchor name,
//   - reject anchors against non-archive (single-file) cohorts.
func TestShardArchiveAnchorSyntax(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	schema, shardA, shardB := adminTwoSimpleShards(t, fsys)
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	// 1) Anchor returns a one-shard cohort with the shard's own schema
	//    and Shards() empty.
	cohort, err := svc.Open(context.Background(), "arch.pulse#a.pulse")
	if err != nil {
		t.Fatalf("Open anchor: %v", err)
	}
	if len(cohort.Shards()) != 0 {
		t.Errorf("anchor cohort Shards = %d, want 0", len(cohort.Shards()))
	}
	if cohort.Schema() == nil || len(cohort.Schema().Fields) != len(schema.Fields) {
		t.Fatalf("anchor cohort schema = %v, want %d fields", cohort.Schema(), len(schema.Fields))
	}
	for i := range schema.Fields {
		if cohort.Schema().Fields[i].Name != schema.Fields[i].Name {
			t.Errorf("schema.Fields[%d].Name = %q, want %q",
				i, cohort.Schema().Fields[i].Name, schema.Fields[i].Name)
		}
	}

	// 2) Process against the anchor matches Process against the
	//    standalone shard byte-for-byte.
	reqAnchor := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse#a.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "v"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	reqStandalone := &types.Request{
		Cohort: &types.Cohort{Filename: shardA},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "v"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	rA, err := svc.Process(context.Background(), reqAnchor)
	if err != nil {
		t.Fatalf("Process anchor: %v", err)
	}
	rB, err := svc.Process(context.Background(), reqStandalone)
	if err != nil {
		t.Fatalf("Process standalone: %v", err)
	}
	if rA.Data[0]["v"] != rB.Data[0]["v"] || rA.Data[0]["n"] != rB.Data[0]["n"] {
		t.Errorf("anchor vs standalone diverge: anchor=%+v standalone=%+v",
			rA.Data[0], rB.Data[0])
	}
	if rA.Data[0]["n"].(float64) != 2 {
		t.Errorf("anchor count = %v, want 2", rA.Data[0]["n"])
	}

	// 3) Missing anchor name surfaces PULSE_SHARD_MISSING.
	_, err = svc.Open(context.Background(), "arch.pulse#missing.pulse")
	if err == nil {
		t.Fatal("Open missing anchor: expected error")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("missing anchor: code = %v, want PULSE_SHARD_MISSING", err)
	}

	// 4) Anchor against a single-file (non-archive) cohort raises
	//    PULSE_ARCHIVE_MAGIC_INVALID (single-file lacks zip magic).
	adminWriteShardFile(t, fsys, "single.pulse", schema, [][]uint64{
		{1, math.Float64bits(10.0)},
	})
	_, err = svc.Open(context.Background(), "single.pulse#x.pulse")
	if err == nil {
		t.Fatal("Open anchor on single-file: expected error")
	}
	if !errors.HasCode(err, errors.PULSE_ARCHIVE_MAGIC_INVALID) {
		t.Errorf("anchor on single-file: code = %v, want PULSE_ARCHIVE_MAGIC_INVALID", err)
	}

	// 5) Anchor against the reserved canonical schema is rejected with
	//    PULSE_SHARD_RESERVED_NAME (it is not a real shard).
	_, err = svc.Open(context.Background(), "arch.pulse#"+encx.ReservedSchemaName)
	if !errors.HasCode(err, errors.PULSE_SHARD_RESERVED_NAME) {
		t.Errorf("anchor on reserved entry: code = %v, want PULSE_SHARD_RESERVED_NAME", err)
	}
}

// AddShard's failure contract: a successful Add leaves no residual temp
// file at the canonical archive's directory and the archive is
// well-formed. The complementary half — a failed mid-write leaving the
// prior archive intact — is exercised via an afero overlay that tears
// the in-place tail append after a few bytes: the add truncates the
// archive back to its prior length. (The rewrite path's rename failure
// is pinned by TestAddShard_WidenIsAtomicUnderRenameFailure.)
//
// Uses a real OsFs against t.TempDir() for the success half, a memfs
// with the injected write failure for the other.
func TestShardArchiveAddCrashRecovery(t *testing.T) {
	t.Parallel()

	t.Run("success leaves no temp residue", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := fs.New(fs.WithFs(afero.NewOsFs()))
		if err != nil {
			t.Fatalf("fs.New OsFs: %v", err)
		}
		fsys := cfg.Fs()
		svc := New(cfg)

		schema := adminScoreSchema()
		shardA := filepath.Join(dir, "a.pulse")
		shardB := filepath.Join(dir, "b.pulse")
		adminWriteShardFile(t, fsys, shardA, schema, [][]uint64{
			{1, math.Float64bits(10.0)},
		})
		adminWriteShardFile(t, fsys, shardB, schema, [][]uint64{
			{2, math.Float64bits(20.0)},
		})

		archive := filepath.Join(dir, "arch.pulse")
		if _, err := svc.CreateShardArchive(context.Background(), archive,
			[]string{shardA}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}

		// Snapshot the directory before Add so we can detect residue.
		// AddShard should leave only `arch.pulse` plus the shard
		// sources after success — no `*.pulse.tmp-*` files.
		before := listDirNames(t, dir)
		if _, err := svc.AddShard(context.Background(), archive, shardB); err != nil {
			t.Fatalf("AddShard: %v", err)
		}
		after := listDirNames(t, dir)

		for _, name := range after {
			if strings.Contains(name, ".pulse.tmp-") {
				t.Errorf("temp file residue after AddShard: %q (before=%v after=%v)",
					name, before, after)
			}
		}

		// Sanity: archive now lists two shards and reads correctly.
		shards, err := svc.ListShards(context.Background(), archive)
		if err != nil {
			t.Fatalf("ListShards: %v", err)
		}
		if len(shards) != 2 {
			t.Errorf("post-Add shard count = %d, want 2", len(shards))
		}
	})

	t.Run("append write failure preserves prior archive", func(t *testing.T) {
		base := afero.NewMemMapFs()
		schema := adminScoreSchema()
		shardA := "a.pulse"
		shardB := "b.pulse"
		adminWriteShardFile(t, base, shardA, schema, [][]uint64{
			{1, math.Float64bits(10.0)},
		})
		adminWriteShardFile(t, base, shardB, schema, [][]uint64{
			{2, math.Float64bits(20.0)},
		})

		cfg, err := fs.New(fs.WithFs(base))
		if err != nil {
			t.Fatalf("fs.New seed: %v", err)
		}
		svc := New(cfg)
		archive := "arch.pulse"
		if _, err := svc.CreateShardArchive(context.Background(), archive,
			[]string{shardA}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}

		// Capture the pre-Add archive bytes so we can verify they are
		// unchanged after the simulated write failure.
		priorBytes, err := afero.ReadFile(base, archive)
		if err != nil {
			t.Fatalf("ReadFile prior: %v", err)
		}

		// The in-place append writes part of its tail and then fails:
		// AddShard surfaces the error and truncates the archive back to
		// its prior length, so its old EOCD is the last record again.
		failing := &tornAppendFs{Fs: base, name: archive, after: 40}
		failCfg, err := fs.New(fs.WithFs(failing))
		if err != nil {
			t.Fatalf("fs.New failing: %v", err)
		}
		_, err = New(failCfg).AddShard(context.Background(), archive, shardB)
		if err == nil {
			t.Fatal("AddShard with a failing append: expected error")
		}
		if !errors.HasCode(err, errors.SERVICE_RESOURCE) && !errors.HasCode(err, errors.ENCODING_IO) {
			t.Errorf("expected SERVICE_RESOURCE / ENCODING_IO, got: %v", err)
		}
		if failing.wrote == 0 {
			t.Fatal("fixture: the append failed before writing any byte")
		}

		// Canonical archive must be byte-identical to its prior state.
		postBytes, err := afero.ReadFile(base, archive)
		if err != nil {
			t.Fatalf("ReadFile post: %v", err)
		}
		if string(postBytes) != string(priorBytes) {
			t.Errorf("archive bytes changed despite the failed append (prior=%d bytes, post=%d bytes)",
				len(priorBytes), len(postBytes))
		}

		// The archive still opens and lists the original shard count.
		shards, err := svc.ListShards(context.Background(), archive)
		if err != nil {
			t.Fatalf("ListShards post-failure: %v", err)
		}
		if len(shards) != 1 {
			t.Errorf("post-failure shard count = %d, want 1", len(shards))
		}
	})
}

// tornAppendFs opens name for writing as a file that accepts `after`
// bytes and then fails every write: an append torn by an I/O error.
type tornAppendFs struct {
	afero.Fs
	name  string
	after int
	wrote int
}

func (f *tornAppendFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil || name != f.name || flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return file, err
	}
	return &tornFile{File: file, fs: f}, nil
}

type tornFile struct {
	afero.File
	fs *tornAppendFs
}

func (t *tornFile) Write(p []byte) (int, error) {
	room := t.fs.after - t.fs.wrote
	if room <= 0 {
		return 0, io.ErrShortWrite
	}
	n, err := t.File.Write(p[:min(room, len(p))])
	t.fs.wrote += n
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

// listDirNames returns the basenames of every entry directly inside
// dir. Used by the temp-residue assertion above.
func listDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// failingRenameFs is an afero.Fs that forwards every operation to the
// base except Rename, which always returns an error. Used to exercise
// the atomic-write failure branch of AddShard.
type failingRenameFs struct {
	afero.Fs
}

func (f *failingRenameFs) Rename(oldname, newname string) error {
	return &os.PathError{Op: "rename", Path: newname, Err: io.ErrUnexpectedEOF}
}

// TestShardArchiveCreateRoundTrip confirms that a freshly-created
// archive's contents read back identically to the union of the source
// shard payloads.
func TestShardArchiveCreateRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	schema, shardA, shardB := adminTwoSimpleShards(t, fsys)
	_ = schema
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "v"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	if got := resp.Data[0]["n"].(float64); got != 4 {
		t.Errorf("count = %v, want 4", got)
	}
	if got := resp.Data[0]["v"].(float64); got != 100.0 {
		t.Errorf("sum = %v, want 100.0 (10+20+30+40)", got)
	}
}

// TestShardArchiveExtractRoundTrip confirms ExtractShard yields bytes
// that parse as a standalone single-file .pulse with the original
// shard's records intact.
func TestShardArchiveExtractRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	_, shardA, shardB := adminTwoSimpleShards(t, fsys)
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	rc, err := svc.ExtractShard(context.Background(), "arch.pulse", "a.pulse")
	if err != nil {
		t.Fatalf("ExtractShard: %v", err)
	}
	defer rc.Close()
	extracted, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if err := afero.WriteFile(fsys, "extracted.pulse", extracted, 0o644); err != nil {
		t.Fatalf("WriteFile extracted: %v", err)
	}
	cohort, err := svc.Open(context.Background(), "extracted.pulse")
	if err != nil {
		t.Fatalf("Open extracted as single-file: %v", err)
	}
	if len(cohort.Shards()) != 0 {
		t.Errorf("extracted cohort Shards = %d, want 0 (standalone)", len(cohort.Shards()))
	}
	if cohort.Schema() == nil || len(cohort.Schema().Fields) != 2 {
		t.Errorf("extracted schema fields = %v, want 2 fields", cohort.Schema())
	}
}

// TestShardArchiveRemoveDropsShard confirms a successful Remove omits
// the shard from subsequent List output and preserves the canonical
// schema (orphan dictionary entries retained per design).
func TestShardArchiveRemoveDropsShard(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	_, shardA, shardB := adminTwoSimpleShards(t, fsys)
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse",
		[]string{shardA, shardB}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if _, err := svc.RemoveShard(context.Background(), "arch.pulse", "a.pulse"); err != nil {
		t.Fatalf("RemoveShard: %v", err)
	}

	shards, err := svc.ListShards(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}
	if len(shards) != 1 || shards[0].Filename != "b.pulse" {
		t.Errorf("post-remove shards = %v, want [b.pulse]", shards)
	}

	// Removing a non-existent shard surfaces PULSE_SHARD_MISSING.
	_, err = svc.RemoveShard(context.Background(), "arch.pulse", "ghost.pulse")
	if !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("remove ghost: code = %v, want PULSE_SHARD_MISSING", err)
	}
}
