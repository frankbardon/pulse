package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// walkAll lists every file AND directory under root on fsys, sorted
// (recursive ReadDir: afero.Walk over a MemMapFs root trips on its own
// path normalisation).
func walkAll(t *testing.T, fsys afero.Fs, root string) []string {
	t.Helper()
	var out []string
	var walk func(dir string)
	walk = func(dir string) {
		ents, err := afero.ReadDir(fsys, dir)
		if err != nil {
			t.Fatalf("list %s: %v", dir, err)
		}
		for _, e := range ents {
			p := path.Join(dir, e.Name())
			out = append(out, p)
			if e.IsDir() {
				walk(p)
			}
		}
	}
	walk(root)
	sort.Strings(out)
	return out
}

// shardPreamble extracts one archive shard and returns its header +
// schema block bytes and its schema.
func shardPreamble(t *testing.T, p *Pulse, archive, shard string) ([]byte, *encoding.Schema) {
	t.Helper()
	rc, err := p.ExtractShard(context.Background(), archive, shard)
	if err != nil {
		t.Fatalf("ExtractShard(%s): %v", shard, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return data[:len(data)-r.Len()], s
}

// TestCohortBuilder_ShardedReadBack: a ShardSplit build is an archive of
// ceil(rows / MaxRecords) shards named part-00001.pulse…, the last one
// partial, that reads back through CohortReader exactly as the appended
// rows in order (Len = rows appended), and Process over it equals
// Process over the single-file build of the same rows. Every field type
// round-trips through the split.
func TestCohortBuilder_ShardedReadBack(t *testing.T) {
	rows, _ := groupedData()
	for _, tc := range []struct {
		max    int
		counts []int64
	}{
		{50, []int64{50, 50, 20}},
		{40, []int64{40, 40, 40}},
		{120, []int64{120}},
		{500, []int64{120}},
	} {
		t.Run(fmt.Sprint(tc.max), func(t *testing.T) {
			p, fsys := memBuilderEngine(t)
			res := buildCohort(t, p, "arch.pulse", groupedSchema(),
				CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: tc.max}}, rows)
			if res.Records != int64(len(rows)) || len(res.Warnings) != 0 {
				t.Fatalf("result records %d warnings %v", res.Records, res.Warnings)
			}
			entries, err := p.ListShards(context.Background(), "arch.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(tc.counts) || len(res.Shards) != len(tc.counts) {
				t.Fatalf("%d entries / %v shards, want %d", len(entries), res.Shards, len(tc.counts))
			}
			for i, e := range entries {
				name := fmt.Sprintf("part-%05d.pulse", i+1)
				if e.Filename != name || res.Shards[i] != name || e.RecordCount != tc.counts[i] {
					t.Fatalf("shard %d = %+v (result %q), want %s with %d records", i, e, res.Shards[i], name, tc.counts[i])
				}
			}
			r := openReader(t, p, "arch.pulse")
			if r.Len() != int64(len(rows)) {
				t.Fatalf("Len = %d, want %d", r.Len(), len(rows))
			}
			for i := range rows {
				got, err := r.RecordAt(int64(i))
				if err != nil {
					t.Fatal(err)
				}
				assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), got, rows[i])
			}
			if files := walkAll(t, fsys, "/"); len(files) != 1 || path.Base(files[0]) != "arch.pulse" {
				t.Fatalf("filesystem holds %v, want only the archive", files)
			}

			buildCohort(t, p, "flat.pulse", groupedSchema(), CohortBuilderOptions{}, rows)
			run := func(path string) []map[string]any {
				resp, err := p.Process(context.Background(), &Request{
					Cohort:       &types.Cohort{Filename: path},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "name"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "sum"}},
				})
				if err != nil {
					t.Fatalf("Process(%s): %v", path, err)
				}
				return resp.Data
			}
			if a, f := run("arch.pulse"), run("flat.pulse"); !reflect.DeepEqual(a, f) {
				t.Fatalf("Process over archive = %v, single file = %v", a, f)
			}
		})
	}

	t.Run("all field types", func(t *testing.T) {
		p, _ := memBuilderEngine(t)
		prow := parityRows(t)
		buildCohort(t, p, "types.pulse", paritySchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 2}}, prow)
		buildCohort(t, p, "flat.pulse", paritySchema(), CohortBuilderOptions{}, prow)
		r, flat := openReader(t, p, "types.pulse"), openReader(t, p, "flat.pulse")
		if r.Len() != int64(len(prow)) {
			t.Fatalf("Len = %d", r.Len())
		}
		// Against the single-file build's read-back: a set reads back in
		// dictionary order, not the order its labels were appended in.
		for i := range prow {
			got, err := r.RecordAt(int64(i))
			if err != nil {
				t.Fatal(err)
			}
			want, err := flat.RecordAt(int64(i))
			if err != nil {
				t.Fatal(err)
			}
			assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), got, want)
		}
	})

	t.Run("empty", func(t *testing.T) {
		p, _ := memBuilderEngine(t)
		res := buildCohort(t, p, "empty.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 10}}, nil)
		if len(res.Shards) != 1 || res.Records != 0 {
			t.Fatalf("empty sharded build = %+v", res)
		}
		if r := openReader(t, p, "empty.pulse"); r.Len() != 0 {
			t.Fatalf("Len = %d", r.Len())
		}
	})
}

// TestCohortBuilder_ShardedGroupsDecidedOnce: with Groups and
// ElideConstants the gate and the elision are decided ONCE over all
// rows — a field constant within every shard but not across them is
// NOT elided anywhere, a group the gate drops is dropped in every shard
// (one PULSE_GROUP_TOO_NARROW, not one per shard) — and every shard
// carries the byte-identical preamble (schema, dictionaries, group
// layout), so the archive merge reports no PULSE_SHARD_* rewrite. The
// archive reads back as the appended rows.
func TestCohortBuilder_ShardedGroupsDecidedOnce(t *testing.T) {
	rows, _ := groupedData()
	for i := range rows {
		// version is 3 on the first 60 rows and 4 after: constant within
		// each 60-row shard, not across the build.
		if i >= 60 {
			rows[i][7] = uint64(4)
		}
	}
	p, _ := memBuilderEngine(t)
	narrow := pio.GroupDecl{Members: []string{"source"}}
	res := buildCohort(t, p, "arch.pulse", groupedSchema(), CohortBuilderOptions{
		Groups:         []pio.GroupDecl{custGroup, narrow},
		ElideConstants: true,
		Shards:         &ShardSplit{MaxRecords: 60},
	}, rows)

	if len(res.Warnings) != 1 || !errors.HasCode(res.Warnings[0], errors.PULSE_GROUP_TOO_NARROW) {
		t.Fatalf("warnings = %v, want exactly one PULSE_GROUP_TOO_NARROW", res.Warnings)
	}
	if !reflect.DeepEqual(res.ElidedConstants, []string{"source"}) {
		t.Fatalf("elided = %v, want only source (version varies across the build)", res.ElidedConstants)
	}
	if res.FormatVersion != encoding.FormatVersionV2 || len(res.Schema.Groups) != 2 {
		t.Fatalf("format %#x with %d groups, want 0x02 with cust + constant groups", res.FormatVersion, len(res.Schema.Groups))
	}
	if len(res.Shards) != 2 {
		t.Fatalf("shards = %v", res.Shards)
	}
	first, schema := shardPreamble(t, p, "arch.pulse", res.Shards[0])
	if len(schema.Groups) != 2 {
		t.Fatalf("shard %s carries %d groups, want 2", res.Shards[0], len(schema.Groups))
	}
	for _, g := range schema.Groups {
		for _, m := range g.Members {
			if f := schema.Fields[m.Field]; f.Name == "version" {
				t.Fatalf("version grouped in shard %s (group %+v)", res.Shards[0], g)
			}
		}
	}
	for _, sh := range res.Shards[1:] {
		if pre, _ := shardPreamble(t, p, "arch.pulse", sh); !bytes.Equal(pre, first) {
			t.Fatalf("shard %s preamble differs from %s", sh, res.Shards[0])
		}
	}

	r := openReader(t, p, "arch.pulse")
	if r.Len() != int64(len(rows)) {
		t.Fatalf("Len = %d", r.Len())
	}
	for i := range rows {
		got, err := r.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		assertRowEqual(t, fmt.Sprintf("row %d", i), r.Schema(), got, rows[i])
	}
}

// shardStageFailFs refuses to create a staged shard file, after the
// staging directory exists and the full pass is done.
type shardStageFailFs struct{ afero.Fs }

func (f shardStageFailFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if strings.Contains(name, ".build-shards-") && strings.HasSuffix(name, "part-00002.pulse") {
		return nil, fmt.Errorf("injected shard create failure")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

func (f shardStageFailFs) Create(name string) (afero.File, error) {
	return f.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

// TestCohortBuilder_ShardedFailedCloseLeavesNothing: a sharded Close
// that fails while staging a shard or while the archive is published
// leaves no archive, no spool, no staged shard and no staging
// directory; an existing archive being overwritten stays as it was.
func TestCohortBuilder_ShardedFailedCloseLeavesNothing(t *testing.T) {
	rows, _ := groupedData()
	for _, tc := range []struct {
		name string
		wrap func(afero.Fs) afero.Fs
	}{
		{"archive rename", func(m afero.Fs) afero.Fs { return renameFailFs{m} }},
		{"shard staging", func(m afero.Fs) afero.Fs { return shardStageFailFs{m} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := afero.NewMemMapFs()
			p, err := New(Options{FS: tc.wrap(mem)})
			if err != nil {
				t.Fatal(err)
			}
			attempt := func(target string, overwrite bool) {
				b, err := p.NewCohortBuilder(context.Background(), target, groupedSchema(),
					CohortBuilderOptions{Overwrite: overwrite, Groups: []pio.GroupDecl{custGroup}, Shards: &ShardSplit{MaxRecords: 50}})
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					if err := b.Append(r); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := b.Close(); err == nil {
					t.Fatal("Close succeeded through an injected failure")
				}
			}
			attempt("arch.pulse", false)
			if left := walkAll(t, mem, "/"); len(left) != 0 {
				t.Fatalf("failed sharded Close left %v", left)
			}

			orig := []byte("original archive bytes")
			if err := afero.WriteFile(mem, "keep.pulse", orig, 0o644); err != nil {
				t.Fatal(err)
			}
			attempt("keep.pulse", true)
			if got := readFile(t, mem, "keep.pulse"); !bytes.Equal(got, orig) {
				t.Fatal("a failed sharded overwrite changed the existing archive")
			}
			if left := walkAll(t, mem, "/"); len(left) != 1 {
				t.Fatalf("failed sharded overwrite left %v", left)
			}
		})
	}
}

// TestCohortBuilder_ShardSplitRefusals: MaxRecords of 0 or less is
// SERVICE_VALIDATION at NewCohortBuilder, before anything is written;
// an existing archive without Overwrite is refused like a single file.
func TestCohortBuilder_ShardSplitRefusals(t *testing.T) {
	for _, n := range []int{0, -5} {
		p, fsys := memBuilderEngine(t)
		_, err := p.NewCohortBuilder(context.Background(), "a.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: n}})
		if !errors.HasCode(err, errors.SERVICE_VALIDATION) || !strings.Contains(err.Error(), "MaxRecords") {
			t.Fatalf("MaxRecords %d: err = %v, want SERVICE_VALIDATION", n, err)
		}
		if left := walkAll(t, fsys, "/"); len(left) != 0 {
			t.Fatalf("refusal left %v", left)
		}
	}
	p, _ := memBuilderEngine(t)
	rows, _ := groupedData()
	buildCohort(t, p, "a.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 50}}, rows)
	if _, err := p.NewCohortBuilder(context.Background(), "a.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 50}}); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("existing archive without Overwrite: err = %v", err)
	}
	res := buildCohort(t, p, "a.pulse", groupedSchema(), CohortBuilderOptions{Overwrite: true, Shards: &ShardSplit{MaxRecords: 100}}, rows)
	if len(res.Shards) != 2 {
		t.Fatalf("overwritten archive shards = %v", res.Shards)
	}
	if r := openReader(t, p, "a.pulse"); r.Len() != int64(len(rows)) {
		t.Fatalf("overwritten archive Len = %d", r.Len())
	}
}

// TestCohortBuilder_ShardedDataDirRoot: under Options{DataDir} a sharded
// build at the data-dir root publishes in place (the archive's atomic
// write stages beside it, inside the data dir) and leaves only the
// archive.
func TestCohortBuilder_ShardedDataDirRoot(t *testing.T) {
	dir := t.TempDir()
	p, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := groupedData()
	buildCohort(t, p, "root.pulse", groupedSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 50}}, rows)
	ents, err := afero.ReadDir(afero.NewOsFs(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "root.pulse" {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("data dir holds %v, want only root.pulse", names)
	}
	if r := openReader(t, p, "root.pulse"); r.Len() != int64(len(rows)) {
		t.Fatalf("Len = %d", r.Len())
	}
}

// TestCohesionFindings: shard-archive warnings become coded findings
// with code, message and details intact.
func TestCohesionFindings(t *testing.T) {
	if got := cohesionFindings(nil); got != nil {
		t.Fatalf("nil warnings = %v", got)
	}
	got := cohesionFindings([]CohesionWarning{{
		Code: string(errors.PULSE_SHARD_GROUPS_REWRITTEN), Message: "regrouped",
		Details: map[string]any{"reason": "incoming_regrouped"},
	}})
	if len(got) != 1 || got[0].Code != errors.PULSE_SHARD_GROUPS_REWRITTEN ||
		got[0].Message != "regrouped" || got[0].Details["reason"] != "incoming_regrouped" {
		t.Fatalf("findings = %+v", got)
	}
}
