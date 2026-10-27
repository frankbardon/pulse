package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// U51 E4-S3 (FR-37): FilterToFileOptions.Flatten writes a shard
// archive's surviving rows, shard by shard in archive order, into ONE
// single-file cohort under the canonical schema — streamed into a stage
// and published by one rename, memory O(one shard). The non-flatten
// archive arm streams too (spool + archive writer), so no filter-to-file
// output is built in a whole-output buffer any more.

// flattenRecords is the record region of a single-file cohort.
func flattenRecords(t *testing.T, data []byte) []byte {
	t.Helper()
	return data[headerSchemaPrefixLen(t, data):]
}

func flatten(t *testing.T, svc *Service, src, dst string, opts FilterToFileOptions) *FilterToFileOutcome {
	t.Helper()
	opts.Flatten = true
	out, err := svc.FilterToFileWithOptions(context.Background(), src, dst, opts)
	if err != nil {
		t.Fatalf("flatten %s: %v", src, err)
	}
	return out
}

// assertNoStages fails when a `*.pulse.tmp-*` stage or spool survived.
func assertNoStages(t *testing.T, fsys afero.Fs, dir string) {
	t.Helper()
	names, err := afero.ReadDir(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if strings.Contains(n.Name(), shardAdminTempPattern) {
			t.Errorf("stage left behind: %s", n.Name())
		}
	}
}

// TestShardStream_FlattenRoundTrip: a pure flatten of a three-shard
// archive is a single-file cohort (pulse magic, not zip) whose records
// are byte-equal to the single-file twin's, in archive order, and
// whose schema is the canonical one.
func TestShardStream_FlattenRoundTrip(t *testing.T) {
	t.Parallel()

	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")
	fsys := cfg.Fs()

	out := flatten(t, svc, "src.pulse", "flat.pulse", FilterToFileOptions{})
	if out.Records != 12 || !out.Flattened {
		t.Fatalf("outcome = %+v, want 12 records, flattened", out)
	}
	got := gReadFile(t, fsys, "flat.pulse")
	if isShardArchiveMagic(got) {
		t.Fatal("flatten wrote a shard archive, want a single file")
	}
	twin := gReadFile(t, fsys, "src.pulse.concat")
	if !bytes.Equal(flattenRecords(t, got), flattenRecords(t, twin)) {
		t.Fatalf("flattened records differ from the single-file twin's")
	}
	cohort, err := svc.Open(context.Background(), "flat.pulse")
	if err != nil {
		t.Fatalf("Open flat: %v", err)
	}
	if len(cohort.Shards()) != 0 {
		t.Fatalf("flat output opened as an archive (%d shards)", len(cohort.Shards()))
	}
	assertNoStages(t, fsys, ".")
}

// TestShardStream_FlattenFilteredMatchesProcess: a filtered flatten
// keeps exactly the rows Process keeps under the same filter, and the
// flat file answers as the filtered archive does.
func TestShardStream_FlattenFilteredMatchesProcess(t *testing.T) {
	t.Parallel()

	svc, _, _ := filterArchiveFixture(t, "src.pulse")
	ctx := context.Background()
	const expr = "score > 50.0"

	out := flatten(t, svc, "src.pulse", "flat.pulse", FilterToFileOptions{Expression: expr})
	if out.Records != 7 {
		t.Fatalf("records = %d, want 7", out.Records)
	}
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
			{Type: types.AGG_MIN, Field: "id", Label: "lo"},
		}
	}
	flat, err := svc.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "flat.pulse"}, Aggregations: aggs()})
	if err != nil {
		t.Fatal(err)
	}
	src, err := svc.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: "src.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
		Aggregations: aggs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(flat.Data) != fmt.Sprint(src.Data) {
		t.Fatalf("flat %v != filtered archive %v", flat.Data, src.Data)
	}
}

// TestShardStream_FlattenWidenedSetFields: an archive whose set field
// was widened set_u8 → set_u16 by an add flattens under the WIDENED
// rung and the union dictionary; its records equal a set_u16 file
// written directly over the union.
func TestShardStream_FlattenWidenedSetFields(t *testing.T) {
	t.Parallel()

	svc, fsys := setWidenFixture(t)
	if _, err := svc.AddShard(context.Background(), "arch.pulse", "add.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	out := flatten(t, svc, "arch.pulse", "flat.pulse", FilterToFileOptions{})
	if out.Records != 5 {
		t.Fatalf("records = %d, want 5", out.Records)
	}
	union := []string{"tv", "radio", "print", "web", "mail", "podcast", "streaming", "sms", "outdoor"}
	cohort, err := svc.Open(context.Background(), "flat.pulse")
	if err != nil {
		t.Fatal(err)
	}
	f := cohort.Schema().Fields[1]
	if f.Type != encoding.FieldTypeSetU16 || strings.Join(f.Dictionary.Values(), ",") != strings.Join(union, ",") {
		t.Fatalf("flat set field = %v %v, want set_u16 over %v", f.Type, f.Dictionary.Values(), union)
	}
	twin := writeSetShard(t, encoding.FieldTypeSetU16, union, [][2]uint64{
		{1, 0b00001}, {2, 0b00110}, {3, 0b11000},
		{4, 1 << 5}, {5, 1<<6 | 1<<8},
	})
	if !bytes.Equal(flattenRecords(t, gReadFile(t, fsys, "flat.pulse")), flattenRecords(t, twin)) {
		t.Fatal("flattened set records differ from the set_u16 twin")
	}
}

// TestShardStream_FlattenGroupedArchive: a grouped (0x02) archive
// flattens to a 0x02 single file carrying the CANONICAL group
// dictionaries (both shards' rows address them), and every answer —
// unfiltered and through a filtered flatten — equals the flat-layout
// archive built from the same data.
func TestShardStream_FlattenGroupedArchive(t *testing.T) {
	t.Parallel()

	a1, a2, aRows := gShardA().build(t, nil)
	b1, b2, bRows := gShardB().build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "fa/a.pulse": a1, "fb/b.pulse": b1})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "flatarch.pulse", []string{"fa/a.pulse", "fb/b.pulse"}); err != nil {
		t.Fatal(err)
	}

	out := flatten(t, svc, "arch.pulse", "out.pulse", FilterToFileOptions{})
	if out.Records != int64(aRows+bRows) {
		t.Fatalf("records = %d, want %d", out.Records, aRows+bRows)
	}
	data := gReadFile(t, fsys, "out.pulse")
	if data[8] != encoding.FormatVersionV2 {
		t.Fatalf("flattened grouped archive wrote format 0x%02x, want 0x02", data[8])
	}
	cohort, err := svc.Open(ctx, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cs := schemaDocFromArchive(t, fsys, "arch.pulse").Schema
	if s := cohort.Schema(); !s.HasGroups() || s.GroupEntryCount(0) != cs.GroupEntryCount(0) || s.GroupEntryCount(1) != 1 {
		t.Fatalf("flat groups = %d/%d entries, want the canonical %d/1", s.GroupEntryCount(0), s.GroupEntryCount(1), cs.GroupEntryCount(0))
	}
	// Identical answers; only the run counters' shard_count tells the
	// archive from the file.
	want := gAnswers(t, svc, "flatarch.pulse", 1)
	for i := range want {
		want[i] = strings.Replace(want[i], `,"shard_count":2`, "", 1)
	}
	gAssertSameAnswers(t, "flattened grouped archive", gAnswers(t, svc, "out.pulse", 1), want)

	// A filtered flatten keeps what Process keeps.
	const expr = `region == "north" || weight >= 15`
	out = flatten(t, svc, "arch.pulse", "filtered.pulse", FilterToFileOptions{Expression: expr})
	res, err := svc.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: "flatarch.pulse"},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := res.Data[0]["n"].(float64); int64(n) != out.Records || n == 0 || int64(n) == int64(aRows+bRows) {
		t.Fatalf("filtered flatten wrote %d rows, Process keeps %v (of %d)", out.Records, n, aRows+bRows)
	}
	for i, req := range gRequests("filtered.pulse") {
		flatReq := gRequests("flatarch.pulse")[i]
		flatReq.Filterers = append([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}, flatReq.Filterers...)
		got, err := svc.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		want, err := svc.Process(ctx, flatReq)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got.Data) != fmt.Sprint(want.Data) {
			t.Fatalf("request #%d: filtered flatten %v != filtered archive %v", i, got.Data, want.Data)
		}
	}
}

// TestShardStream_FlattenSingleFileIsNoOp: --flatten on a single-file
// (or anchored) input changes nothing: with a predicate the output is
// the non-flatten output, without one it is the input; Flattened stays
// false.
func TestShardStream_FlattenSingleFileIsNoOp(t *testing.T) {
	t.Parallel()

	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")
	fsys := cfg.Fs()
	ctx := context.Background()

	if _, err := svc.FilterToFile(ctx, "src.pulse.concat", "plain.pulse", "score > 50.0"); err != nil {
		t.Fatal(err)
	}
	out := flatten(t, svc, "src.pulse.concat", "flat.pulse", FilterToFileOptions{Expression: "score > 50.0"})
	if out.Flattened || out.Records != 7 {
		t.Fatalf("outcome = %+v, want 7 records, not flattened", out)
	}
	if !bytes.Equal(gReadFile(t, fsys, "flat.pulse"), gReadFile(t, fsys, "plain.pulse")) {
		t.Fatal("flatten over a single file differs from the plain filter")
	}
	out = flatten(t, svc, "src.pulse.concat", "copy.pulse", FilterToFileOptions{})
	if out.Flattened || out.Records != 12 {
		t.Fatalf("outcome = %+v, want 12 records, not flattened", out)
	}
	if !bytes.Equal(gReadFile(t, fsys, "copy.pulse"), gReadFile(t, fsys, "src.pulse.concat")) {
		t.Fatal("a predicate-free flatten of a single file is not the input")
	}
	// Anchored: one shard is already one file.
	out = flatten(t, svc, "src.pulse#b.pulse", "anchor.pulse", FilterToFileOptions{})
	if out.Flattened || out.Records != 4 {
		t.Fatalf("anchored outcome = %+v, want 4 records, not flattened", out)
	}
}

// TestShardStream_FilterToFileNeedsPredicateWithoutFlatten: the options
// form keeps FilterToFileBySetAndExpr's refusal unless Flatten is set.
func TestShardStream_FilterToFileNeedsPredicateWithoutFlatten(t *testing.T) {
	t.Parallel()

	svc, _, _ := filterArchiveFixture(t, "src.pulse")
	_, err := svc.FilterToFileWithOptions(context.Background(), "src.pulse", "dst.pulse", FilterToFileOptions{})
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("err = %v, want SERVICE_VALIDATION", err)
	}
}

// TestShardStream_FlattenFailureLeavesDestination: a run that fails
// after the stage is open (a cancelled context) leaves an existing
// destination byte-identical and no stage behind — the archive arm too.
func TestShardStream_FlattenFailureLeavesDestination(t *testing.T) {
	t.Parallel()

	for _, flat := range []bool{true, false} {
		t.Run(fmt.Sprintf("flatten=%v", flat), func(t *testing.T) {
			t.Parallel()

			svc, cfg, _ := filterArchiveFixture(t, "src.pulse")
			fsys := cfg.Fs()
			old := []byte("previous output")
			if err := afero.WriteFile(fsys, "dst.pulse", old, 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := svc.FilterToFileWithOptions(ctx, "src.pulse", "dst.pulse", FilterToFileOptions{Expression: "score > 0.0", Flatten: flat})
			if err == nil {
				t.Fatal("a cancelled run succeeded")
			}
			if !bytes.Equal(gReadFile(t, fsys, "dst.pulse"), old) {
				t.Fatal("a failed run touched the destination")
			}
			assertNoStages(t, fsys, ".")
		})
	}
}

// TestShardStream_FilterArchiveArmLeavesNoSpool: the streamed archive
// arm publishes the same archive the per-shard contract describes and
// removes its spool.
func TestShardStream_FilterArchiveArmLeavesNoSpool(t *testing.T) {
	t.Parallel()

	svc, cfg, _ := filterArchiveFixture(t, "src.pulse")
	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 50.0"); err != nil {
		t.Fatal(err)
	}
	assertNoStages(t, cfg.Fs(), ".")
}
