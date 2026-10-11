package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Model-error aggregators (AGG_MAPE / AGG_RMSE / AGG_MAE, fit-scoring
// E2-S1) are Mergeable: the buffered arm, the streaming arm, the serial
// shard-archive arm and the parallel shard arm (ShardWorkers=3) answer
// the same figures and components, grouped and ungrouped. The actual is
// qty (u32, zero on some rows → n_zero_actual), the prediction score
// (nullable f64 → n_null_prediction).

func merrArmAggs() []*types.Aggregation {
	params := json.RawMessage(`{"prediction_field":"score"}`)
	return []*types.Aggregation{
		{Type: types.AGG_MAPE, Field: "qty", Label: "mape", Params: params},
		{Type: types.AGG_RMSE, Field: "qty", Label: "rmse", Params: params},
		{Type: types.AGG_MAE, Field: "qty", Label: "mae", Params: params},
		{Type: types.AGG_MAE, Field: "qty", Label: "mae_w", Params: params,
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency})},
	}
}

// merrClose compares two decoded JSON documents: numbers to a relative
// 1e-12 (merge order moves the last bits of a float sum), everything
// else exactly.
func merrClose(a, b any) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		return ok && math.Abs(av-bv) <= 1e-12*math.Max(1, math.Abs(bv))
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !merrClose(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !merrClose(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func merrDoc(t *testing.T, resp *types.Response) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(mustJSON(t, resp.Data)+"\n"), &doc); err != nil {
		t.Fatal(err)
	}
	var comps any
	if err := json.Unmarshal([]byte(mustJSON(t, resp.Components.Aggregations)), &comps); err != nil {
		t.Fatal(err)
	}
	return []any{doc, comps}
}

// TestModelError_ArmsAgree: buffered ≡ streaming ≡ archive serial ≡
// archive ShardWorkers=3, grouped and ungrouped.
func TestModelError_ArmsAgree(t *testing.T) {
	fsys := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, fsys, "twin.pulse", s, 0, paritySmallRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, fsys, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
	}
	serial, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 1, DecodeWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serial.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	exts := pulse.ServiceForTest(serial).Extensions()

	groupings := map[string][]*types.Group{
		"ungrouped": nil,
		"grouped":   {{Type: types.GROUP_CATEGORY, Field: "region"}},
	}
	for name, groups := range groupings {
		t.Run(name, func(t *testing.T) {
			req := func(cohort string) *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: groups, Aggregations: merrArmAggs()}
			}
			// ATTR_PERCENTILE is non-streamable and adds no output
			// column: it forces the buffered arm.
			buffered := req("twin.pulse")
			buffered.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"}}
			if processing.CanStreamRequestWithExtensions(buffered, s, exts) {
				t.Fatal("fixture: the buffered arm streams")
			}
			if !processing.CanStreamRequestWithExtensions(req("twin.pulse"), s, exts) ||
				!processing.CanMergeRequestWithExtensions(req("archive.pulse"), s, exts) {
				t.Fatal("model-error request neither streams nor merges")
			}
			base := gcProcess(t, serial, buffered)
			comps := base.Components.Aggregations
			if len(comps) != 4 {
				t.Fatalf("%d component entries, want 4", len(comps))
			}
			op := comps[0].Operator
			if name == "grouped" {
				op = comps[0].Groups[0].Operator
			}
			if op["n_zero_actual"] == 0 || op["n_null_prediction"] == 0 || op["n_scored"] == 0 {
				t.Fatalf("fixture: degenerate counters %v", op)
			}
			want := merrDoc(t, base)
			arms := []struct {
				name string
				p    *pulse.Pulse
				req  *types.Request
			}{
				{"streaming", serial, req("twin.pulse")},
				{"archive_serial", serial, req("archive.pulse")},
				{"archive_shard_workers_3", parallel, req("archive.pulse")},
			}
			for _, arm := range arms {
				if got := merrDoc(t, gcProcess(t, arm.p, arm.req)); !merrClose(got, want) {
					t.Errorf("%s answers differently from the buffered arm\n got  %v\n want %v", arm.name, got, want)
				}
			}
		})
	}
}

// TestModelError_StreamTerminalMatchesBuffered: an ungrouped streamed
// run's terminal chunk carries the buffered components.
func TestModelError_StreamTerminalMatchesBuffered(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "twin.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: "twin.pulse"}, Aggregations: merrArmAggs()}
	resp := gcProcess(t, p, req)
	sr, err := p.ProcessStreamResult(context.Background(), req)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	var chunks []pulse.StreamChunk[pulse.Row]
	for c := range sr.Chunks {
		chunks = append(chunks, c)
	}
	if done := <-sr.Done; done.Status != pulse.StreamCompleted {
		t.Fatalf("stream status = %v, err = %v", done.Status, done.Error)
	}
	last := chunks[len(chunks)-1]
	if last.Components == nil {
		t.Fatal("terminal chunk has no components")
	}
	if got, want := mustJSON(t, last.Components.Aggregations), mustJSON(t, resp.Components.Aggregations); got != want {
		t.Errorf("terminal chunk components differ from the buffered response\n got  %s\n want %s", got, want)
	}
}
