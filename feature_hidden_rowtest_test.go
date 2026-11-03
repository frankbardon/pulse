package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestHiddenVectorTest_PredictRuntimeParity: a hidden vector row test
// (TEST_BOX_M with neither vector nor fields) is refused by predict and
// by the runtime (planRowTests) with the same coded error. That error
// is the PULSE_TEST_UNKNOWN_TYPE a never-registered name gets, not the
// vector-input refusal the unhidden test gets. The unprofiled control
// takes the vector-input refusal on both sides, which shows the hidden
// cell reaches the name.
func TestHiddenVectorTest_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	const hidden, never = "TEST_BOX_M", "TEST_NEVER_REGISTERED"
	fsys := parityFS(t)
	cfg := parityHostConfig{profiles: map[string][]string{
		"no-vector-tests": {"capability:process", string(types.AGG_COUNT), string(types.TEST_SHAPIRO_WILK)},
	}}
	req := func(typ string) *Request {
		return &Request{
			Cohort: &types.Cohort{Filename: parityCohort},
			Tests:  []*types.Test{{Type: types.TestType(typ), Label: "probe"}},
		}
	}
	// both returns the predict and runtime refusals as canonical
	// {code, message, details} bytes and fails unless they agree.
	both := func(t *testing.T, h *parityHost, typ string) []byte {
		t.Helper()
		pr, err := h.p.Predict(context.Background(), req(typ))
		if err != nil {
			t.Fatalf("Predict(%s): %v", typ, err)
		}
		if pr.Valid || len(pr.Errors) == 0 {
			t.Fatalf("Predict(%s) is valid with no error", typ)
		}
		e := pr.Errors[0]
		predicted, mErr := json.Marshal(map[string]any{"code": e.Code, "message": e.Message, "details": e.Details})
		if mErr != nil {
			t.Fatal(mErr)
		}
		ran := parityOutcome(h.p.Process(context.Background(), req(typ)))
		if !parityIsError(ran) {
			t.Fatalf("Process(%s) succeeded: %s", typ, ran)
		}
		if !bytes.Equal(predicted, ran) {
			t.Errorf("%s: predict and runtime disagree\npredict: %s\nruntime: %s", typ, predicted, ran)
		}
		return ran
	}

	scoped := newParityHostWith(t, fsys, "no-vector-tests", cfg)
	if !scoped.p.svc.InstanceSnapshot().Hidden(hidden) {
		t.Fatalf("the fixture profile does not hide %s", hidden)
	}
	got := both(t, scoped, hidden)
	want := both(t, scoped, never)
	if !strings.Contains(string(want), "PULSE_TEST_UNKNOWN_TYPE") {
		t.Fatalf("never-registered %s: %s, want PULSE_TEST_UNKNOWN_TYPE", never, want)
	}
	if subst := strings.ReplaceAll(string(got), hidden, never); subst != string(want) {
		t.Errorf("hidden %s diverges from never-registered\nhidden: %s\nnever:  %s", hidden, got, want)
	}

	open := both(t, newParityHostWith(t, fsys, "", cfg), hidden)
	if !strings.Contains(string(open), "no_vector_or_fields") {
		t.Errorf("unprofiled %s: %s, want the no_vector_or_fields refusal (the hidden cell is vacuous otherwise)", hidden, open)
	}
}
