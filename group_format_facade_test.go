package pulse

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// groupedTwinFS builds two in-memory filesystems holding the SAME
// synthetic parent/child cohort under the same name: the 0x01 import,
// and its 0x02 twin with the parent block (region + two parent columns,
// one of them nullable) in an indexed group and a global-constant column
// in a constant group.
func groupedTwinFS(t *testing.T) (v1, v2 afero.Fs) {
	t.Helper()
	cols := []string{"id", "region", "parent_code", "parent_weight", "amount", "score", "source"}
	var rows [][]string
	for i := 0; i < 360; i++ {
		p := i / 12
		weight := fmt.Sprintf("%d", 10+p%9)
		if p%5 == 0 {
			weight = "" // a nullable parent column, null for every child of the parent
		}
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			[]string{"north", "south", "east", "west"}[p%4],
			fmt.Sprint(1000 + p),
			weight,
			fmt.Sprintf("%d.%02d", 10+i%37, i%100),
			fmt.Sprint((i * 7) % 11),
			"batch-a",
		})
	}
	v1 = afero.NewMemMapFs()
	createTestPulseFile(t, v1, "cohort.pulse", cols, rows)
	raw, err := afero.ReadFile(v1, "cohort.pulse")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	schema, n, err := encoding.DedupCohort(&out, bytes.NewReader(raw), []encoding.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: []string{"region", "parent_code", "parent_weight"}, Key: []string{"parent_code"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"source"}},
	})
	if err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	if n != 360 || schema.GroupEntryCount(0) != 30 {
		t.Fatalf("dedup: %d rows, %d entries; want 360 rows, 30 entries", n, schema.GroupEntryCount(0))
	}
	if !schema.Fields[schema.Groups[0].Members[2].Field].Nullable {
		t.Fatal("fixture: parent_weight should import as nullable")
	}
	if out.Len() >= len(raw) {
		t.Fatalf("grouped cohort %d bytes is not smaller than the flat %d", out.Len(), len(raw))
	}
	v2 = afero.NewMemMapFs()
	if err := afero.WriteFile(v2, "cohort.pulse", out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return v1, v2
}

// TestGroupedCohort_FacadeParity: a 0x01 cohort and its deduped 0x02
// twin produce byte-identical output on every facade read path —
// Process, streaming, sample, facet, profile (run continuation
// included), export, lookup, filter-to-file, inspect, predict, count —
// plus requests that filter, group and aggregate on group MEMBERS.
// Shard archives do not carry groups yet: building one from a grouped
// cohort is a coded refusal, never a silently wrong archive.
func TestGroupedCohort_FacadeParity(t *testing.T) {
	ctx := context.Background()
	fs1, fs2 := groupedTwinFS(t)

	probes := formatProbes(ctx)
	memberReq := func() *Request {
		return &Request{
			Cohort: &types.Cohort{Filename: "cohort.pulse"},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "parent_weight", Values: []string{"11", "16"}},
				{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "east", "west"}},
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_CATEGORY, Field: "source"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "parent_weight"}, {Type: types.AGG_AVERAGE, Field: "amount"}, {Type: types.AGG_COUNT, Field: "id"}},
		}
	}
	probes = append(probes,
		formatProbe{"ProcessMembers", func(p *Pulse, _ afero.Fs) (any, error) { return p.Process(ctx, memberReq()) }},
		formatProbe{"ProcessMembersNoProjection", func(p *Pulse, _ afero.Fs) (any, error) {
			r := memberReq()
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_FREQUENCY, Field: "parent_code"})
			return p.Process(ctx, r)
		}},
		formatProbe{"ExportSPSS", func(p *Pulse, _ afero.Fs) (any, error) {
			w := spss.NewWriterToBuffer(spss.WriterOptions{})
			if _, err := p.Export(ctx, pio.NewExportJob("cohort.pulse", w)); err != nil {
				return nil, err
			}
			return w.Bytes(), nil
		}},
		formatProbe{"LookupMember", func(p *Pulse, _ afero.Fs) (any, error) {
			if _, err := p.BuildIndex(ctx, "cohort.pulse", []string{"parent_code"}); err != nil {
				return nil, err
			}
			return p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Field: "parent_code", Value: "1007", Multiplicity: LookupMultiplicityAll})
		}},
	)

	for _, pr := range probes {
		t.Run(pr.name, func(t *testing.T) {
			var out [2]string
			for i, fsys := range []afero.Fs{fs1, fs2} {
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				got, err := pr.run(p, fsys)
				if pr.name == "ShardArchive" && i == 1 {
					if !perrors.HasCode(err, perrors.PULSE_SHARD_SCHEMA_MISMATCH) {
						t.Fatalf("shard archive over a grouped cohort: err = %v, want PULSE_SHARD_SCHEMA_MISMATCH", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s on cohort %d: %v", pr.name, i+1, err)
				}
				out[i] = mustJSON(t, pr.name, got)
			}
			if out[0] != out[1] {
				t.Fatalf("%s differs between 0x01 and its grouped 0x02 twin:\n 0x01: %s\n 0x02: %s", pr.name, out[0], out[1])
			}
		})
	}

	// FilterToFile carries the dictionary through unchanged: the output
	// is a valid 0x02 cohort with the source's groups.
	t.Run("FilterToFileCarriesGroups", func(t *testing.T) {
		p, err := New(Options{FS: fs2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.FilterToFile(ctx, "cohort.pulse", "carried.pulse", "score > 5"); err != nil {
			t.Fatal(err)
		}
		dst, err := afero.ReadFile(fs2, "carried.pulse")
		if err != nil {
			t.Fatal(err)
		}
		s, v, err := encoding.ReadPreamble(bytes.NewReader(dst))
		if err != nil || v != encoding.FormatVersionV2 || len(s.Groups) != 2 {
			t.Fatalf("filtered copy: version 0x%02x, err %v; want 0x02 with 2 groups", v, err)
		}
	})
}
