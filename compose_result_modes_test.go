package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_result_modes_test.go pins the result pre-phase across the
// execution modes (library-gaps E5-S2): serial ≡ parallel bytes, the
// failure of a read slot on every orchestrator (fail-fast and not) and
// MaxComposeSlots counting a pre-phase slot once. Stream order is
// pinned at the CLI (internal/cli compose_result_stream_test.go). No
// assertion depends on scheduling: at most one slot fails under
// FailFast, and a non-fail-fast pool cancels nothing.

// TestComposeResult_SerialParallelParity: a batch whose block reads
// slot "after" (index 1) through its upper bound and "weeks" (index 0)
// through a constraint answers the same bytes on Compose and on
// ComposeParallel, fail-fast or not, at 1 and 4 workers.
func TestComposeResult_SerialParallelParity(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := resultBatch(t, `result("after", "mean_units") + 20`)
	c.Optimize[0].Constraints = []types.OptimizeConstraint{{Expr: `result("weeks", "top_price") - p`, Type: types.OptimizeConstraintIneq}}
	serial, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(serial)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []ComposeOptions{{MaxWorkers: 1, FailFast: true}, {MaxWorkers: 4, FailFast: true}, {MaxWorkers: 4}} {
		par, err := p.ComposeParallel(context.Background(), c, o)
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		got, err := json.Marshal(par)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%+v: parallel differs from serial:\n par %s\n ser %s", o, got, want)
		}
	}
}

// failingReadBatch is resultBatch with its upper bound reading slot
// "after" (index 1), whose cohort does not exist, so the slot passes
// every static check and fails when it runs.
func failingReadBatch(t *testing.T) *ComposedRequest {
	c := resultBatch(t, `result("after", "mean_units")`)
	c.Requests[1].Cohort = &types.Cohort{Filename: "missing.pulse"}
	return c
}

// TestComposeResult_FailedReadSlotFailFast: on Compose and on a
// fail-fast ComposeParallel, the read slot's failure stops the batch
// before any block with the slot's own code, located details.request
// 1, naming it (result_slot "after") and its first reader (optimize 0);
// predict accepts the batch (the cohort is read at run time).
func TestComposeResult_FailedReadSlotFailFast(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := failingReadBatch(t)
	var codes []perr.Code
	for name, run := range failFastRuns(p) {
		resp, err := run(c)
		var ce *perr.CodedError
		if resp != nil || !stderrors.As(err, &ce) {
			t.Fatalf("%s: resp %v, err %v", name, resp, err)
		}
		if ce.Details["request"] != 1 || ce.Details["result_slot"] != "after" || ce.Details["optimize"] != 0 {
			t.Fatalf("%s: %s %v", name, ce.Code, ce.Details)
		}
		codes = append(codes, ce.Code)
	}
	if codes[0] != codes[1] || codes[0] == perr.PULSE_RESULT_INVALID || codes[0] == perr.SERVICE_INTERNAL {
		t.Fatalf("codes %v, want the slot's own code on both", codes)
	}
}

// TestComposeResult_FailedReadSlotAggregated: a non-fail-fast
// ComposeParallel runs every other slot after the read slot failed —
// slot 2, which no block reads, fails too — and reports the whole
// batch: SERVICE_INTERNAL, failed_indices [1 2], result_slots
// ["after"]. No block is solved (the error is the only result).
func TestComposeResult_FailedReadSlotAggregated(t *testing.T) {
	t.Parallel()

	p := optPulse(t, Options{})
	c := failingReadBatch(t)
	c.Requests = append(c.Requests, &types.Request{Label: "also_missing", Cohort: &types.Cohort{Filename: "missing.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "week", Label: "n"}}})
	resp, err := p.ComposeParallel(context.Background(), c, ComposeOptions{MaxWorkers: 4})
	ce := requireCoded(t, err, perr.SERVICE_INTERNAL)
	if resp != nil || !reflect.DeepEqual(ce.Details["failed_indices"], []int{1, 2}) || !reflect.DeepEqual(ce.Details["result_slots"], []string{"after"}) {
		t.Fatalf("resp %v, details %v", resp, ce.Details)
	}
}

// TestComposeResult_ComposeSlotsCountedOnce: MaxComposeSlots counts a
// pre-phase slot once — a two-slot batch whose block reads one runs
// under a limit of 2 on both orchestrators and predict, and is refused
// under 1 on every side.
func TestComposeResult_ComposeSlotsCountedOnce(t *testing.T) {
	t.Parallel()

	c := func() *ComposedRequest { return resultBatch(t, `result("weeks", "n") / 8`) }
	ok := optPulse(t, Options{Limits: Limits{MaxComposeSlots: 2}})
	if env, err := ok.PredictCompose(context.Background(), c()); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict under 2: %v %+v", err, env.Errors)
	}
	for name, run := range composeRuns(ok) {
		if resp, err := run(c()); err != nil || len(resp.Responses) != 2 || len(resp.Optimizations) != 1 {
			t.Fatalf("%s under 2: %v", name, err)
		}
	}
	tight := optPulse(t, Options{Limits: Limits{MaxComposeSlots: 1}})
	env, err := tight.PredictCompose(context.Background(), c())
	if err != nil || len(env.Errors) == 0 {
		t.Fatalf("predict under 1 accepted: %v", err)
	}
	for name, run := range composeRuns(tight) {
		if _, err := run(c()); err == nil {
			t.Fatalf("%s under 1 accepted", name)
		} else if ce := requireCoded(t, err, perr.Code(env.Errors[0].Code)); ce == nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
