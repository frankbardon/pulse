package pulse_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// prevStateComponentRows: subject 1 a clean sequence, subject 2 a null
// first state (its next record a gap), subject 3 a differing tie on day
// 1 (its day-2 record ambiguous), subject 4 a null order value only.
var prevStateComponentRows = [][3]any{
	{uint64(1), int32(1), "free"}, {uint64(1), int32(2), "paid"}, {uint64(1), int32(3), "paid"},
	{uint64(2), int32(1), nil}, {uint64(2), int32(2), "free"},
	{uint64(3), int32(1), "free"}, {uint64(3), int32(1), "paid"}, {uint64(3), int32(2), "paid"},
	{uint64(4), nil, "free"},
}

// wantPrevStateComponents is the slot's entry over those rows: two
// present outputs (subject 1's days 2 and 3) and seven nulls — four
// first time points, one gap, one ambiguous tie, one null order.
var wantPrevStateComponents = types.AttributeComponents{
	Label: "prev", Type: types.ATTR_PREVIOUS_STATE, N: 2, NNull: 7,
	Operator: map[string]any{"n_subjects": int64(3), "n_first": int64(4), "n_gap": int64(1), "n_ambiguous": int64(1)},
}

// prevStateComponentsRequest groups the panel by plan (grouped=true) or
// not, with the previous-state slot.
func prevStateComponentsRequest(grouped bool) *types.Request {
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "panel.pulse"},
		Attributes:   prevStateAttr(),
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}}
	}
	return req
}

// assertPrevStateComponents checks resp carries exactly the previous
// state's Components.Attributes entry.
func assertPrevStateComponents(t *testing.T, arm string, c *types.ResponseComponents) {
	t.Helper()
	if c == nil || len(c.Attributes) != 1 {
		t.Fatalf("%s: components attributes %+v, want one entry", arm, c)
	}
	if !reflect.DeepEqual(c.Attributes[0], wantPrevStateComponents) {
		t.Errorf("%s: entry %+v\nwant %+v", arm, c.Attributes[0], wantPrevStateComponents)
	}
}

// TestPreviousState_ComponentsEveryArm: the slot's
// Response.Components.Attributes entry — floor {n, n_null} plus
// {n_subjects, n_first, n_gap, n_ambiguous} — is the same on the
// ungrouped, grouped and crosstab buffered arms and a Compose slot,
// cohort-wide (one entry, never per bucket).
func TestPreviousState_ComponentsEveryArm(t *testing.T) {
	t.Parallel()

	p, _ := prevStateFixture(t, pulse.Options{})
	ctx := context.Background()
	for _, grouped := range []bool{false, true} {
		resp, err := p.Process(ctx, prevStateComponentsRequest(grouped))
		if err != nil {
			t.Fatal(err)
		}
		arm := "ungrouped"
		if grouped {
			arm = "grouped"
		}
		assertPrevStateComponents(t, arm, resp.Components)
	}
	xt, err := p.Process(ctx, transitionCrosstab("panel.pulse", "prev", "plan", prevStateAttr()))
	if err != nil {
		t.Fatal(err)
	}
	assertPrevStateComponents(t, "crosstab", xt.Components)

	comp, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{prevStateComponentsRequest(true)}})
	if err != nil {
		t.Fatal(err)
	}
	assertPrevStateComponents(t, "compose", comp.Responses[0].Components)
}

// TestPreviousState_ComponentsStreamTerminalOnly: mergeability None —
// a ProcessStreamResult chunk before the last carries the entry's floor
// but no operator keys; the terminal chunk carries the buffered entry.
func TestPreviousState_ComponentsStreamTerminalOnly(t *testing.T) {
	t.Parallel()

	p, _ := prevStateFixture(t, pulse.Options{})
	res, err := p.ProcessStreamResult(context.Background(), prevStateComponentsRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	var chunks []pulse.StreamChunk[pulse.Row]
	for c := range res.Chunks {
		chunks = append(chunks, c)
	}
	if term := <-res.Done; term.Status != pulse.StreamCompleted {
		t.Fatalf("stream %v: %v", term.Status, term.Error)
	}
	if len(chunks) < 2 {
		t.Fatalf("%d chunks, want one per plan bucket", len(chunks))
	}
	mid := chunks[0].Components
	if mid == nil || len(mid.Attributes) != 1 || mid.Attributes[0].Operator != nil ||
		mid.Attributes[0].N != wantPrevStateComponents.N || mid.Attributes[0].NNull != wantPrevStateComponents.NNull {
		t.Errorf("mid-stream entry %+v, want the floor without operator keys", mid)
	}
	assertPrevStateComponents(t, "terminal chunk", chunks[len(chunks)-1].Components)
}

// TestPreviousState_ComponentsOptOut: the engine opt-out and the
// request opt-out each leave the entry absent.
func TestPreviousState_ComponentsOptOut(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := prevStateFixture(t, pulse.Options{DisableComponents: true})
	resp, err := p.Process(ctx, prevStateComponentsRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Components != nil && len(resp.Components.Attributes) != 0 {
		t.Errorf("engine opt-out: attributes %+v, want absent", resp.Components.Attributes)
	}

	p, _ = prevStateFixture(t, pulse.Options{})
	off := true
	req := prevStateComponentsRequest(false)
	req.DisableComponents = &off
	resp, err = p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Components != nil && len(resp.Components.Attributes) != 0 {
		t.Errorf("request opt-out: attributes %+v, want absent", resp.Components.Attributes)
	}
}

// prevStateFixture writes prevStateComponentRows as panel.pulse.
func prevStateFixture(t *testing.T, opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	panelCohort(t, p, "panel.pulse", prevStateComponentRows)
	return p, fs
}
