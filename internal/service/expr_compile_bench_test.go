package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Compile-once expr-lang programs (E5-S5).
//
// FILTER_EXPRESSION and ATTR_FORMULA used to compile their expr-lang
// program once per ROW. They now compile once per build and run the
// cached program per row. This bench reports ns/row on the join-shaped
// fixture of BenchmarkFilterPrecompute (66-field parent group over a
// 29-field child block, 100K rows), on the 0x01 twin and the grouped
// 0x02 cohort:
//
//   - filter/<arm>:  the filter pass alone over pre-decoded rows (filter
//     precompute OFF, so every row evaluates the expression);
//   - formula/<arm>: Processor.Process over pre-decoded rows with one
//     ATTR_FORMULA and an AGG_SUM over its label;
//   - process-filter/<arm>, process-formula/<arm>: end to end through
//     Service.Process from the in-memory cohort file;
//   - process-control/<arm>: the same end-to-end path with no expression
//     (FILTER_RANGE + AGG_SUM), the floor the process arms sit on.
//
// The deterministic gates in `make test` are TestExprProgram_* in
// processing/ (compile counts independent of row count).
func BenchmarkExprCompileOnce(b *testing.B) {
	fsch, gsch, regions := groupWidthTwins(b, 66, filterPrecomputeParents)
	rows := joinShapeRows(filterPrecomputeParents)

	memFs := afero.NewMemMapFs()
	for key, region := range regions {
		s := fsch
		if key[:2] == "v2" {
			s = gsch
		}
		var buf bytes.Buffer
		if err := encoding.WritePreamble(&buf, s); err != nil {
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

	filter := &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "p_10 > 1.5 && c_u64_00 < 549755813888"}
	formula := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "p_10 * 2 + c_u64_00 / 1024", Label: "score"}
	filterReq := &types.Request{Filterers: []*types.Filterer{filter},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "p_00"}}}
	formulaReq := &types.Request{Attributes: []*types.Attribute{formula},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}

	// The control: the same shape with no expression — FILTER_RANGE and a
	// plain AGG_SUM — so the process arms read as expression overhead.
	controlReq := &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "p_10", Values: []string{"1.5", "1e300"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "c_u64_00"}}}

	for _, cohort := range []string{"v1", "v2"} {
		s := fsch
		if cohort == "v2" {
			s = gsch
		}
		key := cohort + "/scattered"
		decode := func(req *types.Request) []*processing.Record {
			needed := processing.NeededFields(req, s, nil)
			keep := encoding.FieldFilter(func(n string) bool { return needed.Has(n) })
			plan, err := s.BuildDecodePlan(retainedFromFilter(s, keep))
			if err != nil {
				b.Fatal(err)
			}
			binding := recordBindingFor(s, plan, keep)
			rr := encoding.NewRecordReader(bytes.NewReader(regions[key]), s)
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
			return recs
		}

		filterRecs := decode(filterReq)
		b.Run(fmt.Sprintf("filter/%s", cohort), func(b *testing.B) {
			prev := processing.SetFilterPrecompute(false)
			defer processing.SetFilterPrecompute(prev)
			chain := filterReq.Filterers
			best := time.Duration(-1)
			for b.Loop() {
				fns, err := processing.BuildFilters(chain, s, nil)
				if err != nil {
					b.Fatal(err)
				}
				counters := processing.NewFilterPassCounters(chain)
				start := time.Now()
				for _, r := range filterRecs {
					if _, err := processing.ApplyFilterPass(r, chain, fns, counters); err != nil {
						b.Fatal(err)
					}
				}
				if d := time.Since(start); best < 0 || d < best {
					best = d
				}
			}
			b.ReportMetric(float64(best.Nanoseconds())/float64(rows), "ns/row")
		})

		formulaRecs := decode(formulaReq)
		b.Run(fmt.Sprintf("formula/%s", cohort), func(b *testing.B) {
			best := time.Duration(-1)
			for b.Loop() {
				proc := processing.NewProcessorWithExtensions(s, nil)
				start := time.Now()
				if _, err := proc.Process(context.Background(), formulaReq, processing.NewSliceIterator(formulaRecs)); err != nil {
					b.Fatal(err)
				}
				if d := time.Since(start); best < 0 || d < best {
					best = d
				}
			}
			b.ReportMetric(float64(best.Nanoseconds())/float64(rows), "ns/row")
		})

		for _, pc := range []struct {
			name string
			req  *types.Request
		}{{"process-filter", filterReq}, {"process-formula", formulaReq}, {"process-control", controlReq}} {
			b.Run(fmt.Sprintf("%s/%s", pc.name, cohort), func(b *testing.B) {
				prev := processing.SetFilterPrecompute(false)
				defer processing.SetFilterPrecompute(prev)
				svc := New(cfg)
				req := *pc.req
				req.Cohort = &types.Cohort{Filename: "/" + filterPrecomputeFile(key)}
				best := time.Duration(-1)
				for b.Loop() {
					start := time.Now()
					if _, err := svc.Process(context.Background(), &req); err != nil {
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
