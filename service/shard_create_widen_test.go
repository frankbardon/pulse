package service

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// createFixture stages two single-file shards on a fresh MemMapFs and
// returns the service plus their paths, ready for CreateShardArchive.
func createFixture(t *testing.T, aRung, bRung encoding.FieldType, aVals, bVals []string,
	aRecs, bRecs [][2]uint64) (*Service, afero.Fs, []string) {
	t.Helper()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	if err := afero.WriteFile(fsys, "a.pulse", writeSetShard(t, aRung, aVals, aRecs), 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := afero.WriteFile(fsys, "b.pulse", writeSetShard(t, bRung, bVals, bRecs), 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	return svc, fsys, []string{"a.pulse", "b.pulse"}
}

// Create auto-widens on a set-dictionary overflow exactly as add does.
// The asymmetry this removes had no defence: the same two files produced
// an archive when fed one after the other and an error when fed
// together.
func TestCreateShardArchive_WidensOnSetDictOverflow(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU8,
		[]string{"tv", "radio", "print", "web", "mail"},
		[]string{"podcast", "streaming", "sms", "outdoor"},
		[][2]uint64{{1, 0b00001}, {2, 0b00110}, {3, 0b11000}},
		[][2]uint64{{4, 0b0001}, {5, 0b1010}})
	ctx := context.Background()

	res, err := svc.CreateShardArchive(ctx, "arch.pulse", paths)
	if err != nil {
		t.Fatalf("CreateShardArchive with a set-dict overflow must widen, not refuse: %v", err)
	}

	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := cohort.Schema().Fields[1]
	if f.Type != encoding.FieldTypeSetU16 {
		t.Fatalf("canonical set rung = %v, want set_u16 for a 9-member union", f.Type)
	}
	wantDict := []string{"tv", "radio", "print", "web", "mail", "podcast", "streaming", "sms", "outdoor"}
	if got := strings.Join(f.Dictionary.Values(), ","); got != strings.Join(wantDict, ",") {
		t.Fatalf("canonical dict = %s, want %s", got, strings.Join(wantDict, ","))
	}
	// Every shard payload must carry the new rung, not just the one
	// that triggered the widen.
	for _, sh := range []string{"a.pulse", "b.pulse"} {
		schema := shardSchemaFromArchive(t, svc, "arch.pulse", sh)
		if schema.Fields[1].Type != encoding.FieldTypeSetU16 {
			t.Errorf("shard %s set rung = %v, want set_u16", sh, schema.Fields[1].Type)
		}
	}
	if res.ShardCount != 2 {
		t.Errorf("ShardCount = %d, want 2", res.ShardCount)
	}

	// Labels, not counts: a word-order or offset slip keeps the counts
	// plausible and renames the selections.
	counts := setSelectionCounts(t, svc, "arch.pulse")
	want := map[string]float64{
		"tv":                1,
		"print|radio":       1,
		"mail|web":          1,
		"podcast":           1,
		"outdoor|streaming": 1,
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

// The warning is MANDATORY on the create path too: a silent
// whole-archive rewrite is the failure class the byte-layout contract
// exists to prevent, and the shards inside the archive are no longer
// byte-identical to the files the caller named.
func TestCreateShardArchive_WidenEmitsMandatoryWarning(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU8,
		[]string{"tv", "radio", "print", "web", "mail"},
		[]string{"podcast", "streaming", "sms", "outdoor"},
		[][2]uint64{{1, 0b00001}},
		[][2]uint64{{4, 0b0001}})

	res, err := svc.CreateShardArchive(context.Background(), "arch.pulse", paths)
	if err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if len(res.Widened) != 1 {
		t.Fatalf("Widened = %+v, want exactly one entry", res.Widened)
	}
	var found *encoding.CohesionWarning
	for i := range res.Warnings {
		if res.Warnings[i].Code == string(errors.PULSE_SHARD_SET_WIDENED) {
			found = &res.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no PULSE_SHARD_SET_WIDENED warning on a widening create; warnings=%+v", res.Warnings)
	}
	for _, want := range []string{"opts", "set_u8", "set_u16"} {
		if !strings.Contains(found.Message, want) {
			t.Errorf("warning message %q does not name %q", found.Message, want)
		}
	}
	if got := found.Details["archive_widened"]; got != true {
		t.Errorf("details[archive_widened] = %v, want true", got)
	}
}

// Rung divergence between two seeded shards resolves the same way it
// does on add: up to the wider rung, never down.
func TestCreateShardArchive_WidensOnRungDivergence(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU64,
		[]string{"tv", "radio"},
		[]string{"web", "mail"},
		[][2]uint64{{1, 0b01}, {2, 0b10}},
		[][2]uint64{{3, 0b01}, {4, 0b10}})
	ctx := context.Background()

	res, err := svc.CreateShardArchive(ctx, "arch.pulse", paths)
	if err != nil {
		t.Fatalf("a seed set at diverging rungs must widen, not be refused: %v", err)
	}
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU64 {
		t.Fatalf("canonical set rung = %v, want set_u64", got)
	}
	if len(res.Widened) != 1 || res.Widened[0].To != "set_u64" {
		t.Fatalf("Widened = %+v, want one promotion to set_u64", res.Widened)
	}

	counts := setSelectionCounts(t, svc, "arch.pulse")
	want := map[string]float64{"tv": 1, "radio": 1, "web": 1, "mail": 1}
	if len(counts) != len(want) {
		t.Errorf("selection groups = %v, want %v", counts, want)
	}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
}

// A shard seeded at a NARROWER rung than the accumulated canonical must
// be promoted, never narrow the archive.
func TestCreateShardArchive_NarrowerLaterShardNeverNarrowsTheArchive(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU8,
		[]string{"tv", "radio"},
		[]string{"web", "mail"},
		[][2]uint64{{1, 0b01}, {2, 0b10}},
		[][2]uint64{{3, 0b01}, {4, 0b10}})
	ctx := context.Background()

	res, err := svc.CreateShardArchive(ctx, "arch.pulse", paths)
	if err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := cohort.Schema().Fields[1].Type; got != encoding.FieldTypeSetU64 {
		t.Fatalf("canonical set rung = %v, want set_u64 — a narrower later shard must never narrow the archive", got)
	}
	if len(res.Widened) != 1 || res.Widened[0].ArchiveWidened() {
		t.Fatalf("Widened = %+v, want one entry that did NOT widen the archive", res.Widened)
	}
	counts := setSelectionCounts(t, svc, "arch.pulse")
	for k, v := range map[string]float64{"tv": 1, "radio": 1, "web": 1, "mail": 1} {
		if counts[k] != v {
			t.Errorf("selection %q count = %v, want %v (full=%v)", k, counts[k], v, counts)
		}
	}
}

// Above set_u256 there is nowhere to widen to, so create stays fatal and
// writes nothing.
func TestCreateShardArchive_UnionAboveWidestRungStaysFatal(t *testing.T) {
	aVals := make([]string, 200)
	for i := range aVals {
		aVals[i] = "a" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	bVals := make([]string, 100)
	for i := range bVals {
		bVals[i] = "b" + string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	svc, fsys, paths := createFixture(t,
		encoding.FieldTypeSetU256, encoding.FieldTypeSetU256,
		aVals, bVals,
		[][2]uint64{{1, 1}}, [][2]uint64{{2, 1}})

	_, err := svc.CreateShardArchive(context.Background(), "arch.pulse", paths)
	if !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
		t.Fatalf("error = %v, want PULSE_SHARD_DICT_WIDTH_OVERFLOW", err)
	}
	if exists, _ := afero.Exists(fsys, "arch.pulse"); exists {
		t.Error("a refused create left an archive behind; the write must be atomic")
	}
}

// An ordinary create — matching rungs, a union that fits — must stay
// warning-free and plan-free. Auto-widen must not turn every create into
// a rewrite.
func TestCreateShardArchive_OrdinaryCreateWidensNothing(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU64,
		[]string{"tv", "radio"},
		[]string{"web", "mail"},
		[][2]uint64{{1, 0b01}},
		[][2]uint64{{3, 0b01}})

	res, err := svc.CreateShardArchive(context.Background(), "arch.pulse", paths)
	if err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if len(res.Widened) != 0 {
		t.Errorf("Widened = %+v, want none on an ordinary create", res.Widened)
	}
	for _, w := range res.Warnings {
		if w.Code == string(errors.PULSE_SHARD_SET_WIDENED) {
			t.Errorf("unexpected widen warning on an ordinary create: %+v", w)
		}
	}
}

// Structural cohesion stays strict on the create path too.
func TestCreateShardArchive_CohesionStaysStrictOffTheRungDimension(t *testing.T) {
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)

	a := writeSetShard(t, encoding.FieldTypeSetU8, []string{"tv"}, [][2]uint64{{1, 1}})
	b := renameFirstField(t, writeSetShard(t, encoding.FieldTypeSetU64, []string{"web"}, [][2]uint64{{2, 1}}), "id", "ix")
	if err := afero.WriteFile(fsys, "a.pulse", a, 0o644); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := afero.WriteFile(fsys, "b.pulse", b, 0o644); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}

	_, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"a.pulse", "b.pulse"})
	if !errors.HasCode(err, errors.PULSE_SHARD_SCHEMA_MISMATCH) {
		t.Fatalf("error = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
	}
}

// A widened create must produce an archive `pulse shard verify` accepts:
// one rung across the canonical block and every shard.
func TestCreateShardArchive_WidenedArchiveVerifiesClean(t *testing.T) {
	svc, _, paths := createFixture(t,
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU64,
		[]string{"tv", "radio"},
		[]string{"web", "mail"},
		[][2]uint64{{1, 0b01}},
		[][2]uint64{{3, 0b01}})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	res, err := svc.VerifyShardArchive(ctx, "arch.pulse")
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("verify reported %d error(s) on a widened archive: %+v", len(res.Errors), res.Errors)
	}
}
