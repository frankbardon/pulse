package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestObserveUnionCohort: an operation reading a union in place of a
// cohort reports OperationInfo.Cohort as its members' cohort paths in
// declared order joined by "+" — top-level operations and a Compose
// slot's child alike — never a member label.
//
// Falsified by dropping the union arm of opSpec.cohortPath (Cohort
// reads empty) or by reading req.Cohort alone in startChild.
func TestObserveUnionCohort(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rec := &hookRecorder{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks()})
	union := func() []types.UnionMember {
		return []types.UnionMember{
			{Cohort: &types.Cohort{Filename: obsCohort}, Label: "real"},
			{Cohort: &types.Cohort{Filename: obsCohort, DataDir: "twin"}, Label: "synthetic"},
		}
	}
	want := obsCohort + "+twin/" + obsCohort
	unionReq := func() *Request {
		return &Request{Union: union(), Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}}}
	}
	// The second member is unreadable (twin/ does not exist): the
	// operation fails, but its info is reported before the work runs.
	runs := []struct {
		name string
		kind observe.OperationKind
		run  func()
	}{
		{"Process", observe.OpProcess, func() { _, _ = p.Process(ctx, unionReq()) }},
		{"SampleWithRequest", observe.OpSample, func() { _, _ = p.SampleWithRequest(ctx, &SampleRequest{Union: union(), N: 1}) }},
		{"Lookup", observe.OpLookup, func() {
			_, _ = p.Lookup(ctx, &LookupRequest{Union: union(), Field: "region", Value: "north"})
		}},
		{"FacetSchema", observe.OpFacetSchema, func() {
			_, _ = p.FacetSchema(ctx, &types.FacetRequest{Union: union(), Fields: []string{"region"}})
		}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			rec.take()
			r.run()
			evs := rec.take()
			if len(evs) == 0 || evs[0].info.Kind != r.kind {
				t.Fatalf("events %+v, want a %s operation", evs, r.kind)
			}
			if got := evs[0].info.Cohort; got != want {
				t.Errorf("Cohort = %q, want %q", got, want)
			}
		})
	}

	t.Run("ComposeChild", func(t *testing.T) {
		rec.take()
		_, _ = p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), unionReq()}})
		var seen bool
		for _, e := range rec.take() {
			if e.info.Scope == observe.ScopeChild && e.info.Index == 1 && e.start {
				seen = true
				if e.info.Cohort != want {
					t.Errorf("union slot child Cohort = %q, want %q", e.info.Cohort, want)
				}
			}
		}
		if !seen {
			t.Fatal("no child started for the union slot")
		}
	})
}
