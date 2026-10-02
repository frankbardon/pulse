package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Filter precompute over parent-group dictionary entries (E5-S1).
//
// The join-shaped fixture of BenchmarkGroupedDecode_ParentWidth at the
// effort's reference shape: a 66-field parent block (key included) in
// one indexed group over a 29-field child block, 12–13 child rows per
// parent (mean 12.5x), sorted and scattered. Four filters:
//
//   - member-include: FILTER_INCLUDE on a parent categorical — precomputed;
//   - member-expr:    FILTER_EXPRESSION over a parent f64 — precomputed;
//   - child-include:  FILTER_INCLUDE on a child categorical — the control,
//     never precomputable;
//   - mixed-expr:     FILTER_EXPRESSION over a parent AND a child field —
//     per row by design.
//
// Three arms: v1 (the 0x01 twin), v2/perrow (grouped, precompute off),
// v2/pre (grouped, precompute on). Reported: ns/row for the filter pass
// alone over pre-decoded rows, ns/row end to end through Process, and
// predicate evaluations per op. The bench FAILS if a member filter on
// v2/pre evaluates its predicate more than once per 10 rows — the ratio
// gate; the deterministic gates in `make test` are
// TestFilterPrecompute_ParityEveryFilterer and
// TestGroupedCohort_FilterPrecomputeParity.

const filterPrecomputeParents = 8000 // x12.5 = 100,000 rows

func filterPrecomputeCases() []struct {
	name   string
	member bool
	f      *types.Filterer
} {
	return []struct {
		name   string
		member bool
		f      *types.Filterer
	}{
		{"member-include", true, &types.Filterer{Type: types.FILTER_INCLUDE, Field: "p_02", Values: []string{"p_02_v00", "p_02_v03"}}},
		{"member-expr", true, &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "p_10 > 1.5"}},
		{"child-include", false, &types.Filterer{Type: types.FILTER_INCLUDE, Field: "c_cat8_00", Values: []string{"c_cat8_00_v01"}}},
		{"mixed-expr", false, &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "p_10 > 1.5 && c_u64_00 < 549755813888"}},
	}
}

func BenchmarkFilterPrecompute(b *testing.B) {
	fsch, gsch, regions := groupWidthTwins(b, 66, filterPrecomputeParents)
	rows := joinShapeRows(filterPrecomputeParents)
	b.Logf("%d rows, %d parent entries, flat stride %d B, grouped stride %d B",
		rows, gsch.GroupEntryCount(0), fsch.RecordByteSize(), gsch.RecordByteSize())

	memFs := afero.NewMemMapFs()
	for key, region := range regions {
		s := fsch
		if key[:2] == "v2" {
			s = gsch
		}
		var buf bytes.Buffer
		if err := encx.WritePreamble(&buf, s); err != nil {
			b.Fatal(err)
		}
		buf.Write(region)
		if err := afero.WriteFile(memFs, "/"+filterPrecomputeFile(key), buf.Bytes(), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	cfg, err := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	if err != nil {
		b.Fatal(err)
	}

	type arm struct{ name, cohort string }
	for _, tc := range filterPrecomputeCases() {
		chain := []*types.Filterer{tc.f}
		for _, order := range []string{"sorted", "scattered"} {
			for _, a := range []arm{{"v1", "v1"}, {"v2-perrow", "v2"}, {"v2-pre", "v2"}} {
				s := fsch
				if a.cohort == "v2" {
					s = gsch
				}
				pre := a.name == "v2-pre"
				// Pre-decoded rows, projected to what the filter reads.
				needed := processing.NeededFields(&types.Request{Filterers: chain}, s, nil)
				keep := encx.FieldFilter(func(n string) bool { return needed.Has(n) })
				plan, err := encx.BuildDecodePlan(s, retainedFromFilter(s, keep))
				if err != nil {
					b.Fatal(err)
				}
				binding := recordBindingFor(s, plan, keep)
				rr := encx.NewRecordReader(bytes.NewReader(regions[a.cohort+"/"+order]), s)
				recs := make([]*processing.Record, 0, rows)
				for {
					rec := binding.NewRecord()
					err := rr.ReadRecordReusedWithPlan(rec, keep, plan)
					if err == io.EOF {
						break
					}
					if err != nil {
						b.Fatal(err)
					}
					recs = append(recs, rec)
				}

				b.Run(fmt.Sprintf("filter/%s/%s/%s", tc.name, order, a.name), func(b *testing.B) {
					prev := processing.SetFilterPrecompute(pre)
					defer processing.SetFilterPrecompute(prev)
					var evals int64
					var passed int
					best := time.Duration(-1)
					for b.Loop() {
						fns, err := processing.BuildFilters(chain, s, nil)
						if err != nil {
							b.Fatal(err)
						}
						counters := processing.NewFilterPassCounters(chain)
						before := processing.FilterPrecomputeStats().EntryEvaluations
						start := time.Now()
						passed = 0
						for _, r := range recs {
							ok, err := processing.ApplyFilterPass(r, chain, fns, counters)
							if err != nil {
								b.Fatal(err)
							}
							if ok {
								passed++
							}
						}
						if d := time.Since(start); best < 0 || d < best {
							best = d
						}
						evals = processing.FilterPrecomputeStats().EntryEvaluations - before
					}
					if !pre {
						evals = int64(rows) // per row, by construction
					}
					if pre && tc.member && evals*10 > int64(rows) {
						b.Fatalf("member filter evaluated %d times over %d rows; want at least a 10x reduction", evals, rows)
					}
					if passed == 0 || passed == rows {
						b.Fatalf("filter passes %d of %d rows", passed, rows)
					}
					b.ReportMetric(float64(best.Nanoseconds())/float64(rows), "ns/row")
					b.ReportMetric(float64(evals), "evals/op")
				})

				b.Run(fmt.Sprintf("process/%s/%s/%s", tc.name, order, a.name), func(b *testing.B) {
					prev := processing.SetFilterPrecompute(pre)
					defer processing.SetFilterPrecompute(prev)
					svc := New(cfg)
					req := &types.Request{
						Cohort:       &types.Cohort{Filename: "/" + filterPrecomputeFile(a.cohort+"/"+order)},
						Filterers:    chain,
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c_u64_00"}, {Type: types.AGG_COUNT, Field: "p_00"}},
					}
					best := time.Duration(-1)
					for b.Loop() {
						start := time.Now()
						if _, err := svc.Process(context.Background(), req); err != nil {
							b.Fatal(err)
						}
						if d := time.Since(start); best < 0 || d < best {
							best = d
						}
					}
					b.ReportMetric(float64(best.Nanoseconds())/float64(rows), "ns/row")
				})
			}
		}
	}
}

func filterPrecomputeFile(key string) string {
	return map[string]string{
		"v1/sorted": "fp_v1_sorted.pulse", "v1/scattered": "fp_v1_scattered.pulse",
		"v2/sorted": "fp_v2_sorted.pulse", "v2/scattered": "fp_v2_scattered.pulse",
	}[key]
}
