package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// E2-S2 (PRD FR-8): Compose, ComposeParallel and ProcessChain fire one
// child operation per slot / stage — same kind, scope child, Parent the
// enclosing operation's ID, Index the slot / stage — each with its own
// execution facts. The parent's OperationResult aggregates its
// children: row and byte counters sum, Workers / Shards take the
// maximum, Arm is the children's arm when they agree (else empty).

// childOps splits a recorded operation into its top-level start/end and
// its children's ends keyed by index, checking the event shape: one
// top start first, one top end last, each child's start before its end,
// every child linked to the parent and started under its ctx.
func childOps(t *testing.T, evs []hookEvent, kind observe.OperationKind) (top hookEvent, children map[int]hookEvent) {
	t.Helper()
	if len(evs) < 2 || !evs[0].start || evs[0].info.Scope != observe.ScopeTop ||
		evs[len(evs)-1].start || evs[len(evs)-1].info.Scope != observe.ScopeTop {
		t.Fatalf("want top start first and top end last, got %+v", evs)
	}
	top = evs[len(evs)-1]
	parentID := top.info.ID
	started := map[uint64]bool{}
	children = map[int]hookEvent{}
	for _, e := range evs[1 : len(evs)-1] {
		if e.info.Scope != observe.ScopeChild || e.info.Kind != kind || e.info.Parent != parentID {
			t.Fatalf("child event %+v: want scope child, kind %s, parent %d", e, kind, parentID)
		}
		if e.info.ID == 0 || e.info.ID == parentID {
			t.Errorf("child ID %d (parent %d)", e.info.ID, parentID)
		}
		if e.start {
			if e.marker != parentID {
				t.Errorf("child %d started under ctx marker %v, want the parent's %d — a span would not nest", e.info.Index, e.marker, parentID)
			}
			started[e.info.ID] = true
			continue
		}
		if !started[e.info.ID] {
			t.Errorf("child %d ended before it started", e.info.Index)
		}
		if e.marker != e.info.ID {
			t.Errorf("child %d end saw ctx marker %v, want its own start hook's %d", e.info.Index, e.marker, e.info.ID)
		}
		if _, dup := children[e.info.Index]; dup {
			t.Errorf("child index %d ended twice", e.info.Index)
		}
		children[e.info.Index] = e
	}
	return top, children
}

// assertAggregate: the parent's counters are the children's sum (max
// for Workers / Shards) and its Arm the common child arm or empty.
func assertAggregate(t *testing.T, top hookEvent, children map[int]hookEvent) {
	t.Helper()
	var sum observe.OperationResult
	arms := map[observe.Arm]bool{}
	for _, c := range children {
		sum.RowsScanned += c.res.RowsScanned
		sum.RowsMatched += c.res.RowsMatched
		sum.RowsOut += c.res.RowsOut
		sum.BytesRead += c.res.BytesRead
		sum.Workers = max(sum.Workers, c.res.Workers)
		sum.Shards = max(sum.Shards, c.res.Shards)
		sum.Projected = sum.Projected || c.res.Projected
		arms[c.res.Arm] = true
	}
	if len(arms) == 1 {
		for a := range arms {
			sum.Arm = a
		}
	}
	got := top.res
	if got.RowsScanned != sum.RowsScanned || got.RowsMatched != sum.RowsMatched || got.RowsOut != sum.RowsOut ||
		got.BytesRead != sum.BytesRead || got.Workers != sum.Workers || got.Shards != sum.Shards ||
		got.Projected != sum.Projected || got.Arm != sum.Arm {
		t.Errorf("parent result %+v, want the children's aggregate %+v", got, sum)
	}
	if sum.RowsScanned == 0 {
		t.Error("children scanned no rows — the aggregate check is vacuous")
	}
}

// bufferedObsRequest rides the buffered arm (ATTR_PERCENTILE is not
// streamable), so a batch mixing it with obsRequest has mixed arms.
func bufferedObsRequest() *Request {
	r := obsRequest()
	r.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "amount", Label: "pct"}}
	return r
}

// TestObserveChildOperations: Compose and ComposeParallel with N slots
// fire 1 parent + N children (index 0..N-1), and ProcessChain one child
// per stage, each child with its own arm; the parent aggregates them.
// Run under -race for ComposeParallel's concurrent children.
//
// Falsified by dropping the end(err) after a slot's Process (the child
// never ends) or the Absorb in opRun.finish (the parent's counters stay
// zero).
func TestObserveChildOperations(t *testing.T) {
	ctx := context.Background()
	rec := &hookRecorder{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks()})

	slots := func() []*Request { return []*Request{obsRequest(), bufferedObsRequest(), obsRequest()} }
	runs := []struct {
		name string
		kind observe.OperationKind
		n    int
		arms []observe.Arm
		run  func() error
	}{
		{"Compose", observe.OpCompose, 3, []observe.Arm{observe.ArmStreaming, observe.ArmBuffered, observe.ArmStreaming}, func() error {
			_, err := p.Compose(ctx, &ComposedRequest{Requests: slots()})
			return err
		}},
		{"ComposeParallel", observe.OpComposeParallel, 3, []observe.Arm{observe.ArmStreaming, observe.ArmBuffered, observe.ArmStreaming}, func() error {
			_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: slots()}, ComposeOptions{MaxWorkers: 3})
			return err
		}},
		{"ComposeParallelSameArm", observe.OpComposeParallel, 4, []observe.Arm{observe.ArmStreaming, observe.ArmStreaming, observe.ArmStreaming, observe.ArmStreaming}, func() error {
			_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), obsRequest(), obsRequest(), obsRequest()}}, ComposeOptions{MaxWorkers: 4})
			return err
		}},
		{"ProcessChain", observe.OpProcessChain, 2, []observe.Arm{observe.ArmStreaming, observe.ArmStreaming}, func() error {
			s0 := obsRequest()
			s0.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}}
			s0.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
			s1 := &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}}}
			_, err := p.ProcessChain(ctx, &ChainRequest{Cohort: s0.Cohort, Stages: []*ChainStage{{Request: s0}, {Request: s1}}})
			return err
		}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			rec.take()
			if err := r.run(); err != nil {
				t.Fatal(err)
			}
			evs := rec.take()
			top, children := childOps(t, evs, r.kind)
			if len(children) != r.n || len(evs) != 2+2*r.n {
				t.Fatalf("want 1 parent + %d children (%d events), got %d children in %d events", r.n, 2+2*r.n, len(children), len(evs))
			}
			for i := 0; i < r.n; i++ {
				c, ok := children[i]
				if !ok {
					t.Fatalf("no child with index %d", i)
				}
				if c.res.Code != observe.CodeOK || c.res.Arm != r.arms[i] {
					t.Errorf("child %d: code %q arm %q, want ok / %q", i, c.res.Code, c.res.Arm, r.arms[i])
				}
				if i == 0 && c.info.Cohort != obsCohort {
					t.Errorf("child 0 cohort %q, want %q", c.info.Cohort, obsCohort)
				}
				if c.info.RequestHash == "" {
					t.Errorf("child %d has no request hash", i)
				}
			}
			assertAggregate(t, top, children)
		})
	}
}

// TestObserveChildFailure: a failing slot ends its child with the
// error's code; the parent ends with the operation's code; the failure
// is logged once (by the parent), not per child.
func TestObserveChildFailure(t *testing.T) {
	ctx := context.Background()
	rec := &hookRecorder{}
	h := &captureHandler{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks(), Logger: h.logger()})
	h.take()
	bad := obsRequest()
	bad.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "no_such_field"}}
	_, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest(), bad}})
	if err == nil {
		t.Fatal("want an error from the failing slot")
	}
	top, children := childOps(t, rec.take(), observe.OpCompose)
	if len(children) != 2 || children[0].res.Code != observe.CodeOK || children[1].res.Code == observe.CodeOK {
		t.Fatalf("children = %+v, want slot 0 ok and slot 1 failed", children)
	}
	if top.res.Code != operationCode(err) {
		t.Errorf("parent code %q, want %q", top.res.Code, operationCode(err))
	}
	if n := len(only(h.take(), logMsgFailed)); n != 1 {
		t.Errorf("%d failure records, want 1 (the parent's)", n)
	}
}
