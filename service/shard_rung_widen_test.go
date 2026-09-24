package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// rungFixture seeds an archive from a shard at seedRung and stages a
// second shard at addRung. The two shards carry DISJOINT dictionaries
// whose union still fits inside both rungs unless the caller says
// otherwise, so the only force in play is the rung divergence itself.
func rungFixture(t *testing.T, seedRung, addRung encoding.FieldType) (*Service, afero.Fs) {
	t.Helper()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	seed := writeSetShard(t, seedRung,
		[]string{"tv", "radio", "print"},
		[][2]uint64{
			{1, 0b001}, // tv
			{2, 0b110}, // radio + print
		})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	add := writeSetShard(t, addRung,
		[]string{"web", "mail"},
		[][2]uint64{
			{3, 0b01}, // web
			{4, 0b10}, // mail
		})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}
	return svc, fsys
}

// The case users actually hit: a new period's import inferred a WIDER
// set rung than the archive was seeded at. Before this, structural
// cohesion refused it outright and the only way forward was a full
// re-import of every prior period. Now the archive widens to meet it.
func TestAddShard_IncomingWiderRungWidensArchive(t *testing.T) {
	svc, _ := rungFixture(t, encoding.FieldTypeSetU8, encoding.FieldTypeSetU64)
	ctx := context.Background()

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("a shard at a wider set rung must widen the archive, not be refused: %v", err)
	}

	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open widened archive: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU64 {
		t.Fatalf("canonical set rung = %v, want set_u64", got)
	}
	// Both shard payloads must carry the new rung: a canonical block
	// describing a stride the records do not have opens fine and decodes
	// every later field at the wrong offset.
	for _, sh := range []string{"seed.pulse", "add.pulse"} {
		schema := shardSchemaFromArchive(t, svc, "arch.pulse", sh)
		if schema.Fields[1].Type != encoding.FieldTypeSetU64 {
			t.Errorf("shard %s set rung = %v, want set_u64", sh, schema.Fields[1].Type)
		}
	}

	if len(res.Widened) != 1 {
		t.Fatalf("Widened = %+v, want exactly one entry", res.Widened)
	}
	w := res.Widened[0]
	if w.From != "set_u8" || w.To != "set_u64" || w.IncomingFrom != "set_u64" {
		t.Errorf("widening = %+v, want canonical set_u8 / incoming set_u64 -> set_u64", w)
	}
	if !w.ArchiveWidened() {
		t.Errorf("ArchiveWidened() = false on an archive-wide rewrite")
	}
	// The seed shard moved; the arriving shard was already at the
	// target and must NOT have been re-widened.
	if w.ShardsRewritten != 1 {
		t.Errorf("ShardsRewritten = %d, want 1 (the seed shard only — the arriving shard already declared set_u64)", w.ShardsRewritten)
	}

	// The assertion that catches a word-order or offset slip: the counts
	// stay plausible under corruption, the LABELS do not.
	counts := setSelectionCounts(t, svc, "arch.pulse")
	want := map[string]float64{
		"tv":          1,
		"print|radio": 1,
		"web":         1,
		"mail":        1,
	}
	if len(counts) != len(want) {
		t.Errorf("selection groups = %v, want %v", counts, want)
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
}

// A wider incoming rung is an archive-wide rewrite and so carries the
// same MANDATORY warning a dictionary overflow does.
func TestAddShard_IncomingWiderRungEmitsMandatoryWarning(t *testing.T) {
	svc, _ := rungFixture(t, encoding.FieldTypeSetU8, encoding.FieldTypeSetU64)

	res, err := svc.AddShard(context.Background(), "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	var found *encoding.CohesionWarning
	for i := range res.Warnings {
		if res.Warnings[i].Code == string(errors.PULSE_SHARD_SET_WIDENED) {
			found = &res.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no PULSE_SHARD_SET_WIDENED warning on a rung-divergence widen; warnings=%+v", res.Warnings)
	}
	for _, want := range []string{"opts", "set_u8", "set_u64"} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("warning message %q does not name %q", found.Message, want)
		}
	}
	if got := found.Details["archive_widened"]; got != true {
		t.Errorf("details[archive_widened] = %v, want true", got)
	}
}

// The other direction must NEVER narrow the archive: narrowing drops
// every selection above the target's ceiling, silently. The arriving
// shard is promoted to the archive's rung instead.
func TestAddShard_IncomingNarrowerRungNeverNarrowsTheArchive(t *testing.T) {
	svc, _ := rungFixture(t, encoding.FieldTypeSetU64, encoding.FieldTypeSetU8)
	ctx := context.Background()

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("a shard at a narrower set rung must be promoted, not refused: %v", err)
	}

	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open archive: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU64 {
		t.Fatalf("canonical set rung = %v, want set_u64 — a narrower incoming shard must never narrow the archive", got)
	}
	for _, sh := range []string{"seed.pulse", "add.pulse"} {
		schema := shardSchemaFromArchive(t, svc, "arch.pulse", sh)
		if schema.Fields[1].Type != encoding.FieldTypeSetU64 {
			t.Errorf("shard %s set rung = %v, want set_u64", sh, schema.Fields[1].Type)
		}
	}

	if len(res.Widened) != 1 {
		t.Fatalf("Widened = %+v, want exactly one entry", res.Widened)
	}
	w := res.Widened[0]
	if w.ArchiveWidened() {
		t.Errorf("ArchiveWidened() = true, but only the arriving shard moved (%+v)", w)
	}
	if w.From != "set_u64" || w.To != "set_u64" || w.IncomingFrom != "set_u8" {
		t.Errorf("widening = %+v, want archive set_u64 unchanged / incoming set_u8 -> set_u64", w)
	}
	if w.ShardsRewritten != 1 {
		t.Errorf("ShardsRewritten = %d, want 1 (the arriving shard alone)", w.ShardsRewritten)
	}

	counts := setSelectionCounts(t, svc, "arch.pulse")
	want := map[string]float64{"tv": 1, "print|radio": 1, "web": 1, "mail": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("selection groups = %v, want %v", counts, want)
	}
}

// Rung divergence and a dictionary union that outgrows both rungs
// compose: the target is the narrowest rung that holds the union, which
// may be wider than either side declared.
func TestAddShard_RungDivergencePlusDictOverflowWidensToFit(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()

	seedVals := make([]string, 8)
	for i := range seedVals {
		seedVals[i] = "s" + string(rune('a'+i))
	}
	seed := writeSetShard(t, encoding.FieldTypeSetU8, seedVals,
		[][2]uint64{{1, 0b00000001}, {2, 0b10000000}})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	addVals := make([]string, 60)
	for i := range addVals {
		addVals[i] = "a" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	add := writeSetShard(t, encoding.FieldTypeSetU64, addVals,
		[][2]uint64{{3, 1 << 0}, {4, 1 << 59}})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 8 + 60 = 68 members: past set_u64's ceiling even though the
	// arriving shard itself declared set_u64.
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU128 {
		t.Fatalf("canonical set rung = %v, want set_u128 for a 68-member union", got)
	}
	if res.Widened[0].DictEntries != 68 {
		t.Errorf("UnionEntries = %d, want 68", res.Widened[0].DictEntries)
	}

	counts := setSelectionCounts(t, svc, "arch.pulse")
	// The arriving shard's bit 59 is canonical bit 8+59 = 67 — past the
	// low word, so a word-order or truncation slip renames it.
	want := map[string]float64{"sa": 1, "sh": 1, "aa0": 1, "ah2": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("selection groups = %v, want %v", counts, want)
	}
}

// Structural cohesion stays strict for everything else. Only the set
// rung relaxes.
func TestAddShard_StructuralCohesionStaysStrictOffTheRungDimension(t *testing.T) {
	ctx := context.Background()

	t.Run("a diverging field name is still fatal", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)
		seed := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv"}, [][2]uint64{{1, 1}})
		if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}
		add := writeSetShard(t, encoding.FieldTypeSetU64, []string{"radio"}, [][2]uint64{{2, 1}})
		add = renameFirstField(t, add, "id", "ix")
		if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
		if !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
			t.Fatalf("error = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
		}
	})

	t.Run("a set column meeting a non-set column is still fatal", func(t *testing.T) {
		cfg := fs.NewMemMap()
		fsys := cfg.Fs()
		svc := New(cfg)
		seed := writeSetShard(t, encoding.FieldTypeSetU64, []string{"tv"}, [][2]uint64{{1, 1}})
		if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}
		// Same stride (8 bytes), same offsets — only the type byte and
		// the dictionary differ, so nothing but the type check can
		// catch it.
		add := writeNumericSecondFieldShard(t, [][2]uint64{{2, 7}})
		if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
		if !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
			t.Fatalf("error = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
		}
	})
}

// A union past set_u256 has nowhere to widen to and stays fatal even
// when the rungs also diverge.
func TestAddShard_RungDivergenceAboveWidestRungStaysFatal(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()

	seedVals := make([]string, 200)
	for i := range seedVals {
		seedVals[i] = "s" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	seed := writeSetShard(t, encoding.FieldTypeSetU256, seedVals, [][2]uint64{{1, 1}})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	addVals := make([]string, 60)
	for i := range addVals {
		addVals[i] = "a" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	add := writeSetShard(t, encoding.FieldTypeSetU64, addVals, [][2]uint64{{2, 1}})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}

	before, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "add.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
		t.Fatalf("error = %v, want PULSE_SHARD_DICT_WIDTH_OVERFLOW", err)
	}
	after, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatalf("ReadFile archive after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("a refused add rewrote the archive; the rewrite must be atomic")
	}
}

// A rung-divergence widen must leave the archive verifiable: every
// shard at one rung, cohesion clean.
func TestAddShard_RungWidenedArchiveVerifiesClean(t *testing.T) {
	svc, _ := rungFixture(t, encoding.FieldTypeSetU8, encoding.FieldTypeSetU64)
	ctx := context.Background()
	if _, err := svc.AddShard(ctx, "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	res, err := svc.VerifyShardArchive(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("verify reported %d error(s) on a rung-widened archive: %+v", len(res.Errors), res.Errors)
	}
}

// renameFirstField rebuilds a single-file shard with its first field
// renamed. Byte offsets, types and records are untouched, so the only
// thing structural cohesion can object to is the name.
func renameFirstField(t *testing.T, cohort []byte, from, to string) []byte {
	t.Helper()
	idx := bytes.Index(cohort, []byte(from))
	if idx < 0 {
		t.Fatalf("field %q not found in the shard bytes", from)
	}
	if len(from) != len(to) {
		t.Fatalf("renameFirstField needs an equal-length name (%q -> %q)", from, to)
	}
	out := append([]byte(nil), cohort...)
	copy(out[idx:], to)
	return out
}

// writeNumericSecondFieldShard emits a shard whose second column is a
// plain u64 rather than a set — same stride, same offsets as a set_u64
// shard, so only the type-byte check can refuse it.
func writeNumericSecondFieldShard(t *testing.T, records [][2]uint64) []byte {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "opts", Type: encoding.FieldTypeU64, ByteOffset: 4, CsvColumnIdx: 1},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	rec := make([]byte, 12)
	for _, r := range records {
		binary.LittleEndian.PutUint32(rec[0:4], uint32(r[0]))
		binary.LittleEndian.PutUint64(rec[4:12], r[1])
		buf.Write(rec)
	}
	return buf.Bytes()
}

// writeOffsetSkewedShard emits a shard whose STORED byte offset for the
// set column is one past where the field widths put it. Nothing else
// diverges — same names, same type bytes, same rung — so the strict
// structural pass is the only thing that can refuse it.
func writeOffsetSkewedShard(t *testing.T, ft encoding.FieldType, values []string) []byte {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add %q: %v", v, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "opts", Type: ft, ByteOffset: 5, CsvColumnIdx: 1, Dictionary: d},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	rec := make([]byte, 4+ft.ByteSize())
	binary.LittleEndian.PutUint32(rec[0:4], 9)
	rec[4] = 1
	buf.Write(rec)
	return buf.Bytes()
}

// The post-reconciliation strict pass is load-bearing on its own. With
// the rungs MATCHING there is no widen plan, so nothing upstream has
// normalised the layout, and a stored byte offset that disagrees with
// the field widths is a divergence only ValidateStructuralCohesion sees
// — the dictionary union-merge does not compare offsets. A shard
// admitted here would decode every field after it at the wrong address.
func TestAddShard_DivergentByteOffsetIsRefusedByTheStrictPass(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()

	seed := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv"}, [][2]uint64{{1, 1}})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	add := writeOffsetSkewedShard(t, encoding.FieldTypeSetU8, []string{"tv"})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}

	_, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("error = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
	}
}
