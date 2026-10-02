package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestGroupedCohort_FilterPrecomputeParity (E5-S1): on every execution
// arm that filters, a request filtering on parent-group MEMBERS returns
// byte-identical output — data, Components (filterer {n_in, n_out,
// n_null_input} included) and all — on the 0x01 cohort, on its grouped
// twin with the filter precompute on, and on the twin with it off. The
// arms that decode through the grouped reuse decoder must also ROUTE
// through the precompute: over groupedTwinFS's 30-entry parent group the
// two member filters evaluate their predicates exactly 30 + 15 times per
// run (slot 1 reaches every entry; slot 2 only the 15 north/east ones),
// against 360 + 180 evaluations per row — a deterministic work gate.
func TestGroupedCohort_FilterPrecomputeParity(t *testing.T) {
	ctx := context.Background()
	fs1, fs2 := groupedTwinFS(t)
	cohort := &types.Cohort{Filename: "cohort.pulse"}
	members := func() []*types.Filterer {
		return []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "east"}},
			{Type: types.FILTER_RANGE, Field: "parent_weight", Values: []string{"11", "16"}},
		}
	}
	const perRun = 30 + 15
	streaming := func() *Request {
		return &Request{
			Cohort:       cohort,
			Filterers:    members(),
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}, {Type: types.AGG_COUNT, Field: "id"}},
		}
	}
	buffered := func() *Request {
		r := streaming()
		r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "amount"})
		return r
	}
	crosstab := func(cell types.AggregationType) *Request {
		return &Request{
			Cohort:    cohort,
			Filterers: members(),
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "source"}},
				Cell:    &types.Aggregation{Type: cell, Field: "amount"},
			},
		}
	}
	probes := []struct {
		name  string
		evals int64 // entry evaluations the grouped, precompute-on run must make
		run   func(p *Pulse) (any, error)
	}{
		{"ProcessStreaming", perRun, func(p *Pulse) (any, error) { return p.Process(ctx, streaming()) }},
		{"ProcessBuffered", perRun, func(p *Pulse) (any, error) { return p.Process(ctx, buffered()) }},
		{"ProcessStream", perRun, func(p *Pulse) (any, error) {
			it, err := p.ProcessStream(ctx, streaming())
			if err != nil {
				return nil, err
			}
			defer it.Close()
			var rows []any
			for {
				row, ok, err := it.Next(ctx)
				if err != nil {
					return nil, err
				}
				if !ok {
					return rows, nil
				}
				rows = append(rows, row)
			}
		}},
		{"CrosstabFused", perRun, func(p *Pulse) (any, error) { return p.Process(ctx, crosstab(types.AGG_SUM)) }},
		{"CrosstabBuffered", perRun, func(p *Pulse) (any, error) { return p.Process(ctx, crosstab(types.AGG_MEDIAN)) }},
		{"Compose", 2 * perRun, func(p *Pulse) (any, error) {
			return p.Compose(ctx, &ComposedRequest{Requests: []*Request{streaming(), buffered()}})
		}},
		{"ProcessChain", perRun, func(p *Pulse) (any, error) {
			return p.ProcessChain(ctx, &ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{
				{Request: streaming()},
				{Request: &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "AGG_SUM_amount"}}}},
			}})
		}},
		{"FacetSchema", perRun, func(p *Pulse) (any, error) {
			return p.FacetSchema(ctx, &FacetRequest{Cohort: cohort, Fields: []string{"score", "region"}, Filterers: members()})
		}},
		// A filter mixing a member with a row field evaluates per row
		// beside a precomputed one; only the member slot is precomputed.
		{"MixedMemberAndRowField", 30, func(p *Pulse) (any, error) {
			r := buffered()
			r.Filterers = []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "region", Values: []string{"south"}},
				{Type: types.FILTER_EXPRESSION, Expression: `parent_code > 1004 && score > 3`},
			}
			return p.Process(ctx, r)
		}},
		// E5-S5: an expression over a NULLABLE member used to fail to
		// compile on every null row. Null now binds nil; the unknown
		// predicate drops the row — on both formats, both arms.
		{"NullableMemberExpression", 30, func(p *Pulse) (any, error) {
			r := buffered()
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `parent_weight > 14`}}
			return p.Process(ctx, r)
		}},
		{"NullableMemberFormula", 0, func(p *Pulse) (any, error) {
			r := buffered()
			r.Filterers = nil
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: `(parent_weight ?? 0) * 2`, Label: "w2"}}
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "w2"})
			return p.Process(ctx, r)
		}},
	}
	for _, pr := range probes {
		t.Run(pr.name, func(t *testing.T) {
			var outs []string
			for _, arm := range []struct {
				fsys       afero.Fs
				precompute bool
			}{{fs1, true}, {fs2, true}, {fs2, false}} {
				prev := processing.SetFilterPrecompute(arm.precompute)
				p, err := New(Options{FS: arm.fsys})
				if err != nil {
					processing.SetFilterPrecompute(prev)
					t.Fatalf("New: %v", err)
				}
				before := processing.FilterPrecomputeStats()
				got, err := pr.run(p)
				delta := processing.FilterPrecomputeStats().EntryEvaluations - before.EntryEvaluations
				processing.SetFilterPrecompute(prev)
				if err != nil {
					t.Fatalf("%s (grouped=%v, precompute=%v): %v", pr.name, arm.fsys == fs2, arm.precompute, err)
				}
				outs = append(outs, mustJSON(t, pr.name, got))
				switch {
				case arm.fsys == fs1 || !arm.precompute:
					if delta != 0 {
						t.Fatalf("%d entry evaluations where no precompute may run", delta)
					}
				case delta != pr.evals:
					t.Fatalf("grouped arm evaluated %d entries, want %d", delta, pr.evals)
				}
			}
			if outs[0] != outs[1] || outs[1] != outs[2] {
				t.Fatalf("%s differs:\n 0x01:            %s\n 0x02 precompute: %s\n 0x02 per-row:    %s", pr.name, outs[0], outs[1], outs[2])
			}
		})
	}
}
