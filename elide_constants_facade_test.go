package pulse

import (
	"context"
	"fmt"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// elidedTwinFS imports the SAME synthetic source twice under the same
// name: once as today (0x01) and once with ImportJob.ElideConstants,
// which stores the three full-pass constants — a categorical, a numeric
// and an all-null column — once in the schema block (0x02).
func elidedTwinFS(t *testing.T) (flat, elided afero.Fs) {
	t.Helper()
	cols := []string{"id", "region", "amount", "score", "source", "flag", "blank"}
	var rows [][]string
	for i := 0; i < 300; i++ {
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			[]string{"north", "south", "east", "west"}[i%4],
			fmt.Sprintf("%d.%02d", 10+i%37, i%100),
			fmt.Sprint((i * 7) % 11),
			"batch-a",
			"7",
			"",
		})
	}
	build := func(elide bool) afero.Fs {
		fsys := afero.NewMemMapFs()
		job := pio.NewImportJob(newMockReader(cols, rows), "cohort.pulse")
		job.FS = fsys
		job.ElideConstants = elide
		rep, err := job.Run(context.Background())
		if err != nil {
			t.Fatalf("import (elide=%v): %v", elide, err)
		}
		if got := len(rep.ElidedConstants); (got == 3) != elide {
			t.Fatalf("import (elide=%v): elided %v", elide, rep.ElidedConstants)
		}
		return fsys
	}
	return build(false), build(true)
}

// TestElidedConstants_FacadeParity: a cohort imported with constant
// elision and the same cohort imported without it produce byte-identical
// output on every facade read path (Process, streaming, sample, facet,
// profile, export, lookup, filter-to-file, inspect, predict, count) and
// on requests that group, filter and aggregate on the elided fields
// themselves — the null constant included. Shard archives do not carry
// groups yet: that is a coded refusal, never a wrong archive.
func TestElidedConstants_FacadeParity(t *testing.T) {
	ctx := context.Background()
	fs1, fs2 := elidedTwinFS(t)

	constReq := func() *Request {
		return &Request{
			Cohort:       &types.Cohort{Filename: "cohort.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "source", Values: []string{"batch-a"}}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "source"}, {Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "flag"}, {Type: types.AGG_SUM, Field: "blank"}, {Type: types.AGG_AVERAGE, Field: "amount"}},
		}
	}
	probes := append(formatProbes(ctx),
		formatProbe{"ProcessConstants", func(p *Pulse, _ afero.Fs) (any, error) { return p.Process(ctx, constReq()) }},
		formatProbe{"FacetConstant", func(p *Pulse, _ afero.Fs) (any, error) { return p.Facet(ctx, "cohort.pulse", "source") }},
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
				if err != nil {
					t.Fatalf("%s on cohort %d: %v", pr.name, i+1, err)
				}
				out[i] = mustJSON(t, pr.name, got)
			}
			if out[0] != out[1] {
				t.Fatalf("%s differs between the flat and the constant-elided cohort:\n flat:   %s\n elided: %s", pr.name, out[0], out[1])
			}
		})
	}
}
