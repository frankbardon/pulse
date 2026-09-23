package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// writeSetShard emits a single-file .pulse with one u32 id column and
// one set column at rung ft. records is a slice of (id, mask) where the
// mask's bit i names dictValues[i] in this shard's OWN dictionary.
func writeSetShard(t *testing.T, ft encoding.FieldType, dictValues []string, records [][2]uint64) []byte {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range dictValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add %q: %v", v, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "opts", Type: ft, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	width := ft.ByteSize()
	rec := make([]byte, 4+width)
	for _, r := range records {
		for i := range rec {
			rec[i] = 0
		}
		binary.LittleEndian.PutUint32(rec[0:4], uint32(r[0]))
		var scratch [8]byte
		binary.LittleEndian.PutUint64(scratch[:], r[1])
		n := width
		if n > 8 {
			n = 8
		}
		copy(rec[4:], scratch[:n])
		buf.Write(rec)
	}
	return buf.Bytes()
}

// setWidenFixture seeds an archive holding one set_u8 shard whose
// dictionary uses five of the rung's eight bits, and stages a second
// shard whose four members are all new — a union of nine, one past the
// set_u8 ceiling.
func setWidenFixture(t *testing.T) (*Service, afero.Fs) {
	t.Helper()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	seed := writeSetShard(t, encoding.FieldTypeSetU8,
		[]string{"tv", "radio", "print", "web", "mail"},
		[][2]uint64{
			{1, 0b00001}, // tv
			{2, 0b00110}, // radio + print
			{3, 0b11000}, // web + mail
		})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile seed: %v", err)
	}
	if err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	add := writeSetShard(t, encoding.FieldTypeSetU8,
		[]string{"podcast", "streaming", "sms", "outdoor"},
		[][2]uint64{
			{4, 0b0001}, // podcast
			{5, 0b1010}, // streaming + outdoor
		})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile add: %v", err)
	}
	return svc, fsys
}

// TestAddShard_WidensArchiveWhenMergedDictOutgrowsSetRung is the story's
// core case: the merged dictionary (9 members) does not fit set_u8, so
// the archive widens to set_u16 instead of refusing the shard.
func TestAddShard_WidensArchiveWhenMergedDictOutgrowsSetRung(t *testing.T) {
	svc, _ := setWidenFixture(t)
	ctx := context.Background()

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("AddShard with a set-dict overflow must widen, not refuse: %v", err)
	}

	// (1) The canonical schema carries the wider rung and the union dict.
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open widened archive: %v", err)
	}
	f := cohort.Schema().Fields[1]
	if f.Type != encoding.FieldTypeSetU16 {
		t.Fatalf("canonical set rung = %v, want set_u16", f.Type)
	}
	wantDict := []string{"tv", "radio", "print", "web", "mail", "podcast", "streaming", "sms", "outdoor"}
	gotDict := f.Dictionary.Values()
	if strings.Join(gotDict, ",") != strings.Join(wantDict, ",") {
		t.Fatalf("canonical dict = %v, want %v", gotDict, wantDict)
	}
	// The widened field is 2 bytes now, so its offset is unchanged but
	// the stride moved from 5 to 6.
	if got := cohort.Schema().RecordByteSize(); got != 6 {
		t.Errorf("widened stride = %d, want 6", got)
	}

	// (2) EVERY shard payload was widened, not just the incoming one.
	for _, sh := range []string{"seed.pulse", "add.pulse"} {
		schema := shardSchemaFromArchive(t, svc, "arch.pulse", sh)
		if schema.Fields[1].Type != encoding.FieldTypeSetU16 {
			t.Errorf("shard %s set rung = %v, want set_u16", sh, schema.Fields[1].Type)
		}
	}

	// (3) The result names the widen.
	if len(res.Widened) != 1 {
		t.Fatalf("Widened = %+v, want exactly one entry", res.Widened)
	}
	w := res.Widened[0]
	if w.Field != "opts" || w.From != "set_u8" || w.To != "set_u16" {
		t.Errorf("widening identity = %+v, want opts set_u8 -> set_u16", w)
	}
	if w.ShardsRewritten != 2 {
		t.Errorf("ShardsRewritten = %d, want 2 (the existing shard plus the incoming one)", w.ShardsRewritten)
	}
	if w.RecordsRewritten != 5 {
		t.Errorf("RecordsRewritten = %d, want 5", w.RecordsRewritten)
	}
	if w.DictEntries != 9 {
		t.Errorf("DictEntries = %d, want 9", w.DictEntries)
	}

	// (4) Every selection survived, in both shards, under the remapped
	// canonical indices. This is the assertion that would catch a
	// word-order or offset slip: the counts stay plausible either way,
	// the LABELS do not.
	counts := setSelectionCounts(t, svc, "arch.pulse")
	want := map[string]float64{
		"tv":                1, // seed bit 0
		"print|radio":       1, // seed bits 1+2
		"mail|web":          1, // seed bits 3+4
		"podcast":           1, // incoming bit 0 -> canonical bit 5
		"outdoor|streaming": 1, // incoming bits 1+3 -> canonical bits 6+8
	}
	if len(counts) != len(want) {
		t.Errorf("selection groups = %v, want %v", counts, want)
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
	// "sms" is dictionary entry 7 and no record selects it. An empty
	// mask is a valid "no selection", so a stray bit would show up as a
	// selection label naming it.
	for k := range counts {
		if strings.Contains(k, "sms") {
			t.Errorf("selection %q names a member no record selected", k)
		}
	}
}

// The widen warning is MANDATORY. An archive-wide rewrite that happens
// silently is indistinguishable from a cheap append.
func TestAddShard_WidenEmitsMandatoryWarning(t *testing.T) {
	svc, _ := setWidenFixture(t)

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
		t.Fatalf("no PULSE_SHARD_SET_WIDENED warning on a widening add; warnings=%+v", res.Warnings)
	}
	for _, want := range []string{"opts", "set_u8", "set_u16"} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("warning message %q does not name %q", found.Message, want)
		}
	}
	if !strings.Contains(found.Message, "2 shard(s)") {
		t.Errorf("warning message %q does not name the number of shards rewritten", found.Message)
	}
	for k, want := range map[string]any{
		"field": "opts", "from": "set_u8", "to": "set_u16", "shards_rewritten": 2,
	} {
		if got := found.Details[k]; got != want {
			t.Errorf("warning details[%q] = %v, want %v", k, got, want)
		}
	}
}

// aggregate_record_count and shard_count in the SHRD trailer must still
// be right after the whole-archive re-stride.
func TestAddShard_WidenKeepsSchemaDocTrailerCorrect(t *testing.T) {
	svc, fsys := setWidenFixture(t)
	if _, err := svc.AddShard(context.Background(), "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}

	doc := schemaDocFromArchive(t, fsys, "arch.pulse")
	if doc.AggregateRecordCount != 5 {
		t.Errorf("aggregate_record_count = %d, want 5 (3 seed + 2 added)", doc.AggregateRecordCount)
	}
	if doc.ShardCount != 2 {
		t.Errorf("shard_count = %d, want 2", doc.ShardCount)
	}
}

// Cohesion must still hold post-widen, and `shard verify` must pass.
func TestAddShard_WidenedArchiveVerifiesClean(t *testing.T) {
	svc, _ := setWidenFixture(t)
	ctx := context.Background()
	if _, err := svc.AddShard(ctx, "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	res, err := svc.VerifyShardArchive(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("verify on a widened archive reported %d error(s): %+v", len(res.Errors), res.Errors)
	}
}

// The anchor open must resolve a widened shard as a one-shard cohort.
func TestAddShard_WidenedArchiveOpensByAnchor(t *testing.T) {
	svc, _ := setWidenFixture(t)
	ctx := context.Background()
	if _, err := svc.AddShard(ctx, "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	cohort, err := svc.Open(ctx, "arch.pulse#seed.pulse")
	if err != nil {
		t.Fatalf("anchor open of a widened shard: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU16 {
		t.Errorf("anchored shard set rung = %v, want set_u16", got)
	}
	got, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount: %v", err)
	}
	if got != 3 {
		t.Errorf("anchored shard record count = %d, want 3", got)
	}
}

// A union above 256 has nowhere left to widen to.
func TestAddShard_UnionAboveTheWidestRungStaysFatal(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()

	seedDict := make([]string, 200)
	for i := range seedDict {
		seedDict[i] = "s" + strconv.Itoa(i)
	}
	addDict := make([]string, 100)
	for i := range addDict {
		addDict[i] = "a" + strconv.Itoa(i)
	}

	seed := writeSetShard(t, encoding.FieldTypeSetU256, seedDict, [][2]uint64{{1, 1}})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	add := writeSetShard(t, encoding.FieldTypeSetU256, addDict, [][2]uint64{{2, 1}})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	before, _ := afero.ReadFile(fsys, "arch.pulse")
	_, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err == nil {
		t.Fatal("a 300-member union must not be accepted: set_u256 is the ceiling")
	}
	var ce *errors.CodedError
	if !asCodedError(err, &ce) || ce.Code != errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW {
		t.Fatalf("error = %v, want PULSE_SHARD_DICT_WIDTH_OVERFLOW", err)
	}
	after, _ := afero.ReadFile(fsys, "arch.pulse")
	if !bytes.Equal(before, after) {
		t.Error("a refused add must leave the archive byte-identical")
	}
}

// A mid-rewrite failure must leave the ORIGINAL archive intact and
// openable — a half-widened archive opens fine and decodes every field
// after the widened one at the wrong offset.
func TestAddShard_WidenIsAtomicUnderRenameFailure(t *testing.T) {
	svc, fsys := setWidenFixture(t)
	ctx := context.Background()
	before, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}

	failCfg, err := fs.New(fs.WithFs(&failingRenameFs{Fs: fsys}))
	if err != nil {
		t.Fatalf("fs.New failing: %v", err)
	}
	if _, err := New(failCfg).AddShard(ctx, "arch.pulse", "add.pulse"); err == nil {
		t.Fatal("AddShard with a failing Rename: expected an error")
	}

	after, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatalf("reading archive after the failed widen: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a failed widen left the archive modified; the rewrite is not atomic")
	}
	// Still openable, still at the pre-widen rung.
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("original archive no longer opens after a failed widen: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU8 {
		t.Errorf("rolled-back rung = %v, want the original set_u8", got)
	}
}

// Verify reports per-set-field headroom so a widen is foreseeable.
func TestVerifyShardArchive_ReportsSetWidthHeadroom(t *testing.T) {
	svc, _ := setWidenFixture(t)
	res, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(res.SetWidthHeadroom) != 1 {
		t.Fatalf("SetWidthHeadroom = %+v, want one entry for `opts`", res.SetWidthHeadroom)
	}
	h := res.SetWidthHeadroom[0]
	if h.Field != "opts" || h.Type != "set_u8" {
		t.Errorf("headroom identity = %+v, want opts/set_u8", h)
	}
	if h.Used != 5 || h.Capacity != 8 || h.Headroom != 3 {
		t.Errorf("headroom figures = %+v, want used=5 capacity=8 headroom=3", h)
	}
	if h.NextType != "set_u16" {
		t.Errorf("next_type = %q, want set_u16", h.NextType)
	}
}

// An add that does NOT overflow must not widen, must not warn, and must
// leave the rung alone — the widen is the exception, not the path.
func TestAddShard_NoWidenWhenTheUnionFits(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()

	seed := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "radio"},
		[][2]uint64{{1, 0b01}, {2, 0b10}})
	if err := afero.WriteFile(fsys, "seed.pulse", seed, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"seed.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	add := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv", "print"},
		[][2]uint64{{3, 0b10}})
	if err := afero.WriteFile(fsys, "add.pulse", add, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err := svc.AddShard(ctx, "arch.pulse", "add.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if len(res.Widened) != 0 {
		t.Errorf("a fitting union must not widen; got %+v", res.Widened)
	}
	for _, w := range res.Warnings {
		if w.Code == string(errors.PULSE_SHARD_SET_WIDENED) {
			t.Errorf("a fitting union must not warn about widening: %v", w)
		}
	}
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU8 {
		t.Errorf("rung = %v, want the untouched set_u8", got)
	}
	// And the remapped selection still resolves: "print" is bit 1 in the
	// incoming shard and bit 2 in canonical.
	counts := setSelectionCounts(t, svc, "arch.pulse")
	if counts["print"] != 1 {
		t.Errorf("print count = %v, want 1 (full=%v)", counts["print"], counts)
	}
}

// --- helpers ---------------------------------------------------------

func shardSchemaFromArchive(t *testing.T, svc *Service, archivePath, shard string) *encoding.Schema {
	t.Helper()
	rc, err := svc.ExtractShard(context.Background(), archivePath, shard)
	if err != nil {
		t.Fatalf("ExtractShard %s: %v", shard, err)
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatalf("draining shard %s: %v", shard, err)
	}
	schema, err := readSinglePulseSchema(buf.Bytes())
	if err != nil {
		t.Fatalf("reading shard %s schema: %v", shard, err)
	}
	return schema
}

func schemaDocFromArchive(t *testing.T, fsys afero.Fs, archivePath string) *encoding.SchemaDoc {
	t.Helper()
	data, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	doc, err := readSchemaDocEntry(arch)
	if err != nil {
		t.Fatalf("readSchemaDocEntry: %v", err)
	}
	return doc
}

// setSelectionCounts groups the archive by the `opts` set VALUE and
// returns selection-label → record count, resolved through the
// canonical dictionary — the end-to-end check that a widen preserved
// MEANING and not merely shape. A word-order slip, a stale byte offset
// or a missed remap all leave the counts plausible; they change the
// LABELS.
func setSelectionCounts(t *testing.T, svc *Service, archivePath string) map[string]float64 {
	t.Helper()
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: archivePath},
		Groups: []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "opts"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		},
	})
	if err != nil {
		t.Fatalf("Process over the archive: %v", err)
	}
	out := map[string]float64{}
	for _, row := range resp.Data {
		k, _ := row["opts"].(string)
		v, _ := row["n"].(float64)
		out[k] = v
	}
	return out
}
