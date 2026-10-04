package pulse

import (
	"context"
	stderrors "errors"
	"fmt"
	"path"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// assertRows reads every row of path back through CohortReader and
// compares it to want, in order.
func assertRows(t *testing.T, p *Pulse, target string, want []CohortRow) *CohortReader {
	t.Helper()
	r := openReader(t, p, target)
	if r.Len() != int64(len(want)) {
		t.Fatalf("%s: Len = %d, want %d", target, r.Len(), len(want))
	}
	for i := range want {
		got, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatalf("%s: RecordAt(%d): %v", target, i, err)
		}
		assertRowEqual(t, fmt.Sprintf("%s row %d", target, i), r.Schema(), got, want[i])
	}
	return r
}

// assertOnlyArchive fails unless the filesystem holds exactly the
// archive: no spool, staging directory or stray shard file.
func assertOnlyArchive(t *testing.T, fsys afero.Fs, archive string) {
	t.Helper()
	if files := walkAll(t, fsys, "/"); len(files) != 1 || path.Base(files[0]) != archive {
		t.Fatalf("filesystem holds %v, want only %s", files, archive)
	}
}

// warningCodes lists the result warnings' codes, in order.
func warningCodes(ws []*errors.CodedError) []errors.Code {
	out := make([]errors.Code, len(ws))
	for i, w := range ws {
		out[i] = w.Code
	}
	return out
}

// TestCohortBuilder_AppendShardReadBack: an anchored target appends ONE
// shard named for the anchor to an existing archive; the archive reads
// back as the old rows then the appended ones, the anchor reads back
// exactly the appended rows, a new categorical label union-merges
// silently, and nothing but the archive is left on the filesystem.
func TestCohortBuilder_AppendShardReadBack(t *testing.T) {
	rows, _ := groupedData()
	first, drop := rows[:60], append([]CohortRow(nil), rows[60:]...)
	drop[0] = append(CohortRow(nil), drop[0]...)
	drop[0][2] = "eve" // a label the archive has never seen
	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 30}}, first)

	res := buildCohort(t, p, "arch.pulse#2026-10.pulse", groupedSchema(), CohortBuilderOptions{}, drop)
	if res.Target != "arch.pulse#2026-10.pulse" || res.Records != int64(len(drop)) ||
		len(res.Shards) != 1 || res.Shards[0] != "2026-10.pulse" || len(res.Warnings) != 0 ||
		len(res.InvalidatedSidecars) != 0 || res.FormatVersion != encoding.FormatVersionV1 {
		t.Fatalf("append result = %+v", res)
	}
	entries, err := p.ListShards(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[2].Filename != "2026-10.pulse" || entries[2].RecordCount != int64(len(drop)) {
		t.Fatalf("archive entries = %+v", entries)
	}
	assertRows(t, p, "arch.pulse", append(append([]CohortRow(nil), first...), drop...))
	assertRows(t, p, "arch.pulse#2026-10.pulse", drop)
	assertOnlyArchive(t, fsys, "arch.pulse")
}

// TestCohortBuilder_AppendShardRefusals: every append rule is refused
// at NewCohortBuilder with a coded error — a missing archive (no
// implicit create), a directory or single-file cohort in its place, a
// malformed / compressed / reserved / taken shard name, and the options
// an append does not admit — and nothing is created.
func TestCohortBuilder_AppendShardRefusals(t *testing.T) {
	rows, _ := groupedData()
	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 60}}, rows)
	buildCohort(t, p, "flat.pulse", groupedSchema(), CohortBuilderOptions{}, rows[:5])
	if err := fsys.MkdirAll("dir.pulse", 0o755); err != nil {
		t.Fatal(err)
	}
	before := listFiles(t, fsys, "/")
	archBytes := readFile(t, fsys, "arch.pulse")

	for _, tc := range []struct {
		name   string
		target string
		opts   CohortBuilderOptions
		code   errors.Code
		reason string
		option string
	}{
		{"missing archive", "nope.pulse#s.pulse", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "archive_not_found", ""},
		{"directory", "dir.pulse#s.pulse", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "target_is_directory", ""},
		{"single-file cohort", "flat.pulse#s.pulse", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "not_an_archive", ""},
		{"no shard name", "arch.pulse#", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "anchored_target", ""},
		{"no archive name", "#s.pulse", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "anchored_target", ""},
		{"nested shard name", "arch.pulse#a/s.pulse", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "invalid_shard_name", ""},
		{"compressed shard", "arch.pulse#s.pulse.zst", CohortBuilderOptions{}, errors.SERVICE_VALIDATION, "compressed_target", ""},
		{"reserved name", "arch.pulse#_schema.pulse", CohortBuilderOptions{}, errors.PULSE_SHARD_RESERVED_NAME, "", ""},
		{"name taken", "arch.pulse#part-00002.pulse", CohortBuilderOptions{}, errors.PULSE_SHARD_NAME_COLLISION, "", ""},
		{"overwrite", "arch.pulse#s.pulse", CohortBuilderOptions{Overwrite: true}, errors.SERVICE_VALIDATION, "append_option", "Overwrite"},
		{"groups", "arch.pulse#s.pulse", CohortBuilderOptions{Groups: []pio.GroupDecl{custGroup}}, errors.SERVICE_VALIDATION, "append_option", "Groups"},
		{"elide", "arch.pulse#s.pulse", CohortBuilderOptions{ElideConstants: true}, errors.SERVICE_VALIDATION, "append_option", "ElideConstants"},
		{"shards", "arch.pulse#s.pulse", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 10}}, errors.SERVICE_VALIDATION, "append_option", "Shards"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.NewCohortBuilder(context.Background(), tc.target, groupedSchema(), tc.opts)
			if !errors.HasCode(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("err %v is not coded", err)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
			if tc.option != "" && ce.Details["option"] != tc.option {
				t.Fatalf("option = %v, want %s", ce.Details["option"], tc.option)
			}
		})
	}
	if after := listFiles(t, fsys, "/"); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("refusals changed the filesystem: %v -> %v", before, after)
	}
	if string(readFile(t, fsys, "arch.pulse")) != string(archBytes) {
		t.Fatal("a refusal rewrote the archive")
	}
}

// TestCohortBuilder_AppendShardCloseFailures: AddShard's errors are
// Close's, unchanged — a shard name taken while the build ran
// (PULSE_SHARD_NAME_COLLISION) and a cohesion failure against an
// archive replaced while the build ran (PULSE_SHARD_SCHEMA_MISMATCH:
// the construction-time pre-check is advisory, AddShard is the
// authority) — and a failed
// Close leaves the archive byte-identical with no spool or staging.
func TestCohortBuilder_AppendShardCloseFailures(t *testing.T) {
	rows, _ := groupedData()
	setup := func(t *testing.T) (*Pulse, afero.Fs, []byte) {
		p, fsys := memBuilderEngine(t)
		buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 60}}, rows)
		return p, fsys, readFile(t, fsys, "arch.pulse")
	}
	check := func(t *testing.T, fsys afero.Fs, b *CohortBuilder, before []byte, code errors.Code) {
		t.Helper()
		if _, err := b.Close(); !errors.HasCode(err, code) {
			t.Fatalf("Close = %v, want %s", err, code)
		}
		if string(readFile(t, fsys, "arch.pulse")) != string(before) {
			t.Fatal("a failed append rewrote the archive")
		}
		assertOnlyArchive(t, fsys, "arch.pulse")
	}

	t.Run("name taken meanwhile", func(t *testing.T) {
		p, fsys, _ := setup(t)
		b, err := p.NewCohortBuilder(context.Background(), "arch.pulse#late.pulse", groupedSchema(), CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows[:10] {
			if err := b.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		// Someone else adds late.pulse while the build is open.
		buildCohort(t, p, "arch.pulse#late.pulse", groupedSchema(), CohortBuilderOptions{}, rows[10:20])
		check(t, fsys, b, readFile(t, fsys, "arch.pulse"), errors.PULSE_SHARD_NAME_COLLISION)
	})

	t.Run("cohesion", func(t *testing.T) {
		// The construction-time pre-check passed against the archive as
		// it was; the archive is then replaced by one the build no longer
		// coheres with, so AddShard at Close — the authority — refuses.
		p, fsys, _ := setup(t)
		b, err := p.NewCohortBuilder(context.Background(), "arch.pulse#other.pulse", groupedSchema(), CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows[:10] {
			if err := b.Append(r); err != nil {
				t.Fatal(err)
			}
		}
		if err := fsys.Remove("arch.pulse"); err != nil {
			t.Fatal(err)
		}
		buildCohort(t, p, "arch.pulse", paritySchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 60}}, parityRows(t))
		check(t, fsys, b, readFile(t, fsys, "arch.pulse"), errors.PULSE_SHARD_SCHEMA_MISMATCH)
	})
}

// setSchema is an id plus one set_u8 multi-select.
func setSchema(labels ...string) encoding.Schema {
	var dict *encoding.Dictionary
	if len(labels) > 0 {
		dict = encoding.NewDictionary()
		for _, l := range labels {
			_, _ = dict.Add(l)
		}
	}
	fields := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Row identifier for the set-widening append."},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict, Description: "Selected tags for the set-widening append."},
	}
	fields[1].ByteOffset = fields[0].Type.ByteSize()
	fields[1].CsvColumnIdx = 1
	return encoding.Schema{Fields: fields}
}

// TestCohortBuilder_AppendShardWidensSet: a set field whose union with
// the archive's dictionary outgrows the archive's set_u8 rung widens
// the archive to set_u16, and AddShard's MANDATORY
// PULSE_SHARD_SET_WIDENED warning reaches CohortBuildResult.Warnings
// with its details intact. Every row reads back, through the archive
// and through the anchor.
func TestCohortBuilder_AppendShardWidensSet(t *testing.T) {
	label := func(c byte) string { return string(rune('a' + c)) }
	var old, drop []CohortRow
	for i := range 8 {
		old = append(old, CohortRow{uint64(i), []string{label(byte(i))}})
		drop = append(drop, CohortRow{uint64(100 + i), []string{label(byte(8 + i))}})
	}
	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "sets.pulse", setSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 8}}, old)

	res := buildCohort(t, p, "sets.pulse#more.pulse", setSchema(), CohortBuilderOptions{}, drop)
	if got := warningCodes(res.Warnings); len(got) != 1 || got[0] != errors.PULSE_SHARD_SET_WIDENED {
		t.Fatalf("warnings = %v, want one PULSE_SHARD_SET_WIDENED", res.Warnings)
	}
	if d := res.Warnings[0].Details; d["field"] != "tags" || d["to"] != encoding.FieldTypeSetU16.String() {
		t.Fatalf("widening details = %v", d)
	}
	r := assertRows(t, p, "sets.pulse", append(append([]CohortRow(nil), old...), drop...))
	if ft := r.Schema().Fields[1].Type; ft != encoding.FieldTypeSetU16 {
		t.Fatalf("archive tags rung = %s, want set_u16", ft)
	}
	assertRows(t, p, "sets.pulse#more.pulse", drop)
	assertOnlyArchive(t, fsys, "sets.pulse")
}

// TestCohortBuilder_AppendShardToGroupedArchive: appending to a grouped
// (0x02) archive — whose group layout the builder may not shape — has
// AddShard re-encode the ungrouped shard to the archive's layout, with
// the MANDATORY PULSE_SHARD_GROUPS_REWRITTEN warning (incoming_regrouped)
// in CohortBuildResult.Warnings. The stored shard carries the archive's
// group, and every row reads back through the archive and the anchor.
func TestCohortBuilder_AppendShardToGroupedArchive(t *testing.T) {
	rows, _ := groupedData()
	first, drop := rows[:60], rows[60:]
	p, fsys := memBuilderEngine(t)
	buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{
		Groups: []pio.GroupDecl{custGroup}, Shards: &ShardSplit{MaxRecords: 30},
	}, first)

	res := buildCohort(t, p, "arch.pulse#drop.pulse", groupedSchema(), CohortBuilderOptions{}, drop)
	if got := warningCodes(res.Warnings); len(got) != 1 || got[0] != errors.PULSE_SHARD_GROUPS_REWRITTEN {
		t.Fatalf("warnings = %v, want one PULSE_SHARD_GROUPS_REWRITTEN", res.Warnings)
	}
	if reason := res.Warnings[0].Details["reason"]; reason != "incoming_regrouped" {
		t.Fatalf("regroup reason = %v, want incoming_regrouped", reason)
	}
	if res.FormatVersion != encoding.FormatVersionV1 {
		t.Fatalf("built shard version = %d, want the ungrouped 0x01 it was built as", res.FormatVersion)
	}
	if _, s := shardPreamble(t, p, "arch.pulse", "drop.pulse"); len(s.Groups) != 1 {
		t.Fatalf("stored shard carries %d groups, want the archive's one", len(s.Groups))
	}
	assertRows(t, p, "arch.pulse", rows)
	assertRows(t, p, "arch.pulse#drop.pulse", drop)
	assertOnlyArchive(t, fsys, "arch.pulse")
}
