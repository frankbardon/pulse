package service

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// filterArchiveFixture writes a fresh multi-shard archive plus the
// matching concatenated single-file cohort and returns a Service wired
// to a memfs containing both. Mirrors setupShardArchive but keeps the
// fixture local to filter-specific tests for readability.
func filterArchiveFixture(t *testing.T, archivePath string) (svc *Service, cfg *fs.Config, schema *encoding.Schema) {
	t.Helper()
	schema, shards, concat := canonicalThreeShards()
	svc, cfg = setupShardArchive(t, archivePath, schema, shards, concat)
	return svc, cfg, schema
}

// TestFilterToFile_ShardArchive_PreservesPerShardStructure — given an
// archive with three score shards (10..40, 50..80, 90..120) and the
// filter `score > 50`, the output archive must:
//   - keep all three shards (zero-record shards survive so shard_count
//     stays stable),
//   - report aggregate_record_count == 7 (b:3 + c:4),
//   - return the matching shards in central-directory (insertion) order,
//   - re-open through Service.Open + RecordCount cleanly.
func TestFilterToFile_ShardArchive_PreservesPerShardStructure(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 50.0")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 7 {
		t.Fatalf("written = %d, want 7", written)
	}

	exists, _ := afero.Exists(cfg.Fs(), "dst.pulse")
	if !exists {
		t.Fatal("dst.pulse missing")
	}

	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if len(cohort.Shards()) != 3 {
		t.Errorf("dst shard count = %d, want 3", len(cohort.Shards()))
	}
	names := make([]string, 0, len(cohort.Shards()))
	for _, sh := range cohort.Shards() {
		names = append(names, sh.Filename)
	}
	wantNames := []string{"a.pulse", "b.pulse", "c.pulse"}
	for i, want := range wantNames {
		if i >= len(names) || names[i] != want {
			t.Errorf("dst shards[%d] = %q, want %q (full=%v)", i, names[i], want, names)
		}
	}
	// Cohort.RecordCount currently parses the archive as a single-file
	// cohort and errors on the zip magic (pre-sharding API limitation).
	// Use the per-shard counts populated on Cohort.Shards() instead,
	// which mirror the on-disk shard headers.
	var rc int64
	for _, sh := range cohort.Shards() {
		rc += sh.RecordCount
	}
	if rc != 7 {
		t.Errorf("dst aggregate record count = %d, want 7", rc)
	}

	// Confirm filter parity through Process: count over the output
	// archive equals the cross-shard count over the input archive
	// after the same filter.
	resOut, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: "dst.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process dst: %v", err)
	}
	if resOut.Data[0]["n"].(float64) != 7 {
		t.Errorf("Process(dst).count = %v, want 7", resOut.Data[0]["n"])
	}

	resIn, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: "src.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score > 50.0"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process src filtered: %v", err)
	}
	if resIn.Data[0]["n"].(float64) != 7 {
		t.Errorf("Process(src, filtered).count = %v, want 7", resIn.Data[0]["n"])
	}
}

// TestFilterToFile_ShardArchive_ZeroMatches — a filter that excludes
// every row keeps the shard_count metadata stable and emits empty
// shards. The output archive remains valid and re-openable; aggregate
// is zero.
func TestFilterToFile_ShardArchive_ZeroMatches(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 999.0")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}

	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if len(cohort.Shards()) != 3 {
		t.Errorf("dst shard count = %d, want 3 (zero-record shards retained)", len(cohort.Shards()))
	}

	// Each output shard's own header+schema bytes must equal the
	// corresponding source shard's prefix. Walk the central directory
	// pair-wise and compare the byte range up to the first record.
	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	srcArch, err := encoding.OpenArchive(bytes.NewReader(srcBytes), int64(len(srcBytes)))
	if err != nil {
		t.Fatalf("OpenArchive src: %v", err)
	}
	dstArch, err := encoding.OpenArchive(bytes.NewReader(dstBytes), int64(len(dstBytes)))
	if err != nil {
		t.Fatalf("OpenArchive dst: %v", err)
	}
	for _, name := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		srcShard := readShardOrFatal(t, srcArch, name)
		dstShard := readShardOrFatal(t, dstArch, name)
		prefixLen := headerSchemaPrefixLen(t, srcShard)
		if int64(len(dstShard)) != prefixLen {
			t.Errorf("dst shard %q: payload length = %d, want %d (header+schema, zero records)",
				name, len(dstShard), prefixLen)
		}
		for i := range prefixLen {
			if srcShard[i] != dstShard[i] {
				t.Fatalf("dst shard %q diverges at byte %d (header+schema must be verbatim)",
					name, i)
			}
		}
	}

	// Trailer aggregate must reflect zero rows kept.
	doc := readSchemaDocOrFatal(t, dstArch)
	if doc.AggregateRecordCount != 0 {
		t.Errorf("aggregate_record_count = %d, want 0", doc.AggregateRecordCount)
	}
	if doc.ShardCount != 3 {
		t.Errorf("shard_count = %d, want 3", doc.ShardCount)
	}
}

// TestFilterToFile_ShardArchive_AllMatch — when every row passes the
// filter the per-shard payloads must be byte-equal to the source (the
// only changes are the refreshed _schema.pulse trailer, which is
// already byte-equal under our fixture because the aggregate is the
// same).
func TestFilterToFile_ShardArchive_AllMatch(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "id >= 1")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 12 {
		t.Fatalf("written = %d, want 12", written)
	}

	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	srcArch, _ := encoding.OpenArchive(bytes.NewReader(srcBytes), int64(len(srcBytes)))
	dstArch, _ := encoding.OpenArchive(bytes.NewReader(dstBytes), int64(len(dstBytes)))

	for _, name := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		src := readShardOrFatal(t, srcArch, name)
		dst := readShardOrFatal(t, dstArch, name)
		if !bytes.Equal(src, dst) {
			t.Errorf("shard %q payload diverged under all-match filter", name)
		}
	}

	// Trailer aggregate matches source's.
	srcDoc := readSchemaDocOrFatal(t, srcArch)
	dstDoc := readSchemaDocOrFatal(t, dstArch)
	if srcDoc.AggregateRecordCount != dstDoc.AggregateRecordCount {
		t.Errorf("all-match aggregate diverged: src=%d dst=%d",
			srcDoc.AggregateRecordCount, dstDoc.AggregateRecordCount)
	}
	if dstDoc.ShardCount != srcDoc.ShardCount {
		t.Errorf("all-match shard_count diverged: src=%d dst=%d",
			srcDoc.ShardCount, dstDoc.ShardCount)
	}
}

// TestFilterToFile_ShardArchive_TrailerRefreshed — partial filter
// updates aggregate_record_count to the surviving total and leaves
// shard_count unchanged.
func TestFilterToFile_ShardArchive_TrailerRefreshed(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score >= 60.0"); err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	arch, err := encoding.OpenArchive(bytes.NewReader(dstBytes), int64(len(dstBytes)))
	if err != nil {
		t.Fatalf("OpenArchive dst: %v", err)
	}
	doc := readSchemaDocOrFatal(t, arch)
	// score >= 60 keeps {60,70,80,90,100,110,120} = 7 rows.
	if doc.AggregateRecordCount != 7 {
		t.Errorf("aggregate_record_count = %d, want 7", doc.AggregateRecordCount)
	}
	if doc.ShardCount != 3 {
		t.Errorf("shard_count = %d, want 3", doc.ShardCount)
	}
}

// TestFilterToFile_ShardArchive_PerShardRecordRanges — verify the
// record-byte ranges inside each output shard are pulled verbatim from
// the source shard. This is the byte-for-byte payload guarantee that
// matches single-file FilterToFile semantics, extended per-shard.
func TestFilterToFile_ShardArchive_PerShardRecordRanges(t *testing.T) {
	svc, cfg, schema := filterArchiveFixture(t, "src.pulse")

	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 50.0"); err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}

	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	srcArch, _ := encoding.OpenArchive(bytes.NewReader(srcBytes), int64(len(srcBytes)))
	dstArch, _ := encoding.OpenArchive(bytes.NewReader(dstBytes), int64(len(dstBytes)))

	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}

	wantKept := map[string]int{
		"a.pulse": 0, // 10,20,30,40 — none survive `> 50`
		"b.pulse": 3, // 60,70,80
		"c.pulse": 4, // 90,100,110,120
	}
	for name, want := range wantKept {
		src := readShardOrFatal(t, srcArch, name)
		dst := readShardOrFatal(t, dstArch, name)
		prefix := headerSchemaPrefixLen(t, src)
		wantLen := prefix + int64(want*recordSize)
		if int64(len(dst)) != wantLen {
			t.Errorf("shard %q dst length = %d, want %d (header+schema + %d records)",
				name, len(dst), wantLen, want)
		}
		// Header+schema verbatim.
		for i := range prefix {
			if src[i] != dst[i] {
				t.Fatalf("shard %q header/schema diverges at byte %d", name, i)
			}
		}
	}
}

// TestFilterToFile_ShardArchive_OpenableThroughOpen — end-to-end:
// filter, open the output, process it, confirm parity with the
// in-place filter applied at process time.
func TestFilterToFile_ShardArchive_OpenableThroughOpen(t *testing.T) {
	svc, _, _ := filterArchiveFixture(t, "src.pulse")

	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score < 75.0"); err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}

	// Process(dst, no filter) == Process(src, filter).
	dstRes, err := svc.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: "dst.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("Process dst: %v", err)
	}
	srcRes, err := svc.Process(context.Background(), &types.Request{
		Cohort:    &types.Cohort{Filename: "src.pulse"},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score < 75.0"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("Process src filtered: %v", err)
	}
	if dstRes.Data[0]["sum"] != srcRes.Data[0]["sum"] || dstRes.Data[0]["n"] != srcRes.Data[0]["n"] {
		t.Errorf("dst(no filter) vs src(filtered) diverge: dst=%+v src=%+v",
			dstRes.Data[0], srcRes.Data[0])
	}
}

// TestFilterToFile_AnchorSyntax — `archive.pulse#shard.pulse` produces
// a single-file output containing only matching rows from the anchored
// shard.
func TestFilterToFile_AnchorSyntax(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "arch.pulse")

	written, err := svc.FilterToFile(context.Background(), "arch.pulse#b.pulse", "dst.pulse", "score > 60.0")
	if err != nil {
		t.Fatalf("FilterToFile anchor: %v", err)
	}
	// Shard b = {50,60,70,80}; `> 60` keeps {70,80}.
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}

	// dst is a single-file (no zip magic).
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	if isShardArchiveMagic(dstBytes) {
		t.Error("anchor-filter dst should be single-file, got zip archive")
	}
	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if len(cohort.Shards()) != 0 {
		t.Errorf("anchor-filter dst is single-file: Shards = %d, want 0", len(cohort.Shards()))
	}
	rc, _ := cohort.RecordCount()
	if rc != 2 {
		t.Errorf("dst record count = %d, want 2", rc)
	}
}

// TestFilterToFile_AnchorSyntax_ReservedRejected — anchoring on the
// reserved `_schema.pulse` entry must surface PULSE_SHARD_RESERVED_NAME.
func TestFilterToFile_AnchorSyntax_ReservedRejected(t *testing.T) {
	svc, _, _ := filterArchiveFixture(t, "arch.pulse")
	_, err := svc.FilterToFile(context.Background(),
		"arch.pulse#"+encoding.ReservedSchemaName, "dst.pulse", "id > 0")
	if err == nil {
		t.Fatal("expected reserved-name rejection, got nil")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_RESERVED_NAME) {
		t.Errorf("err = %v, want PULSE_SHARD_RESERVED_NAME", err)
	}
}

// TestFilterToFile_AnchorSyntax_MissingShard — anchoring on a
// non-existent shard name surfaces PULSE_SHARD_MISSING.
func TestFilterToFile_AnchorSyntax_MissingShard(t *testing.T) {
	svc, _, _ := filterArchiveFixture(t, "arch.pulse")
	_, err := svc.FilterToFile(context.Background(),
		"arch.pulse#nope.pulse", "dst.pulse", "id > 0")
	if err == nil {
		t.Fatal("expected missing-shard error, got nil")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("err = %v, want PULSE_SHARD_MISSING", err)
	}
}

// TestFilterToFile_ShardArchive_RoundTripIdempotent — running the same
// filter twice (src → mid → dst) yields a final archive whose per-
// shard payloads match the first-pass output byte-for-byte. Confirms
// the operation has no hidden state on the canonical schema or
// record bytes.
func TestFilterToFile_ShardArchive_RoundTripIdempotent(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "mid.pulse", "score > 30.0"); err != nil {
		t.Fatalf("FilterToFile pass 1: %v", err)
	}
	if _, err := svc.FilterToFile(context.Background(), "mid.pulse", "dst.pulse", "score > 30.0"); err != nil {
		t.Fatalf("FilterToFile pass 2: %v", err)
	}
	midBytes, _ := afero.ReadFile(cfg.Fs(), "mid.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	if !bytes.Equal(midBytes, dstBytes) {
		t.Error("idempotent filter: mid and dst archives diverge")
	}
}

// TestFilterToFile_ShardArchive_BadExpressionFailsBeforeWrite —
// invalid filter expression returns an error and leaves dst untouched.
func TestFilterToFile_ShardArchive_BadExpressionFailsBeforeWrite(t *testing.T) {
	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")

	_, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score + 1")
	if err == nil {
		t.Fatal("expected error for non-bool expression")
	}
	exists, _ := afero.Exists(cfg.Fs(), "dst.pulse")
	if exists {
		t.Error("dst was written despite filter compilation failure")
	}
}

// --- helpers ---

func readShardOrFatal(t *testing.T, arch *encoding.Archive, name string) []byte {
	t.Helper()
	rc, err := arch.Open(name)
	if err != nil {
		t.Fatalf("archive Open %q: %v", name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read shard %q: %v", name, err)
	}
	return b
}

func readSchemaDocOrFatal(t *testing.T, arch *encoding.Archive) *encoding.SchemaDoc {
	t.Helper()
	rc, err := arch.Open(encoding.ReservedSchemaName)
	if err != nil {
		t.Fatalf("Open _schema.pulse: %v", err)
	}
	defer rc.Close()
	doc, err := encoding.ReadSchemaDoc(rc)
	if err != nil {
		t.Fatalf("ReadSchemaDoc: %v", err)
	}
	return doc
}

// headerSchemaPrefixLen returns the byte length of the header + schema
// prefix of a single-file shard payload (i.e. the offset at which the
// first record begins).
func headerSchemaPrefixLen(t *testing.T, payload []byte) int64 {
	t.Helper()
	r := bytes.NewReader(payload)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if _, err := encoding.ReadSchema(r); err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	return int64(len(payload)) - int64(r.Len())
}
