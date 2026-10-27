package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sampledSweep is a sweep-only batch drawing `samples` slots of the
// t-test body over mu ∈ [10, 60] under mode, ranked by the statistic.
// extra is spliced into the sweep object (e.g. `,"rng_seed":7`).
func sampledSweep(t *testing.T, mode string, samples int, extra string) *ComposedRequest {
	t.Helper()
	n, _ := json.Marshal(samples)
	return decodeComposedJSON(t, `{"sweep":{"mode":"`+mode+`","samples":`+string(n)+extra+`,
		"axes":[{"name":"mu","range":{"min":10,"max":60}}],
		"request":`+rankSweepBody+`,"rank":{"by":"tests.t.statistic"}}}`)
}

// TestSweepRandom_ComposeSerialParallelIdentical: a seeded lhs sweep
// gives byte-identical ComposedResponse JSON from Compose and
// ComposeParallel — slots, ranking and the echoed sweep_seed — and the
// seed it echoes is the one predict reports, as are its slot labels.
func TestSweepRandom_ComposeSerialParallelIdentical(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	for _, mode := range []string{"lhs", "random"} {
		t.Run(mode, func(t *testing.T) {
			c := sampledSweep(t, mode, 12, "")
			var wires []string
			var seeds []int64
			for _, name := range []string{"Compose", "ComposeParallel"} {
				resp, err := composeRuns(p)[name](c)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(resp.Responses) != 12 || len(resp.Ranking) != 12 || resp.SweepSeed == nil {
					t.Fatalf("%s: %d slots, %d ranked, seed %v", name, len(resp.Responses), len(resp.Ranking), resp.SweepSeed)
				}
				raw, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				wires = append(wires, string(raw))
				seeds = append(seeds, *resp.SweepSeed)
			}
			if wires[0] != wires[1] {
				t.Fatalf("serial and parallel differ:\n%s\n%s", wires[0], wires[1])
			}
			if !strings.Contains(wires[0], `"sweep_seed":`) {
				t.Fatalf("wire carries no sweep_seed: %s", wires[0])
			}

			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			res := env.Data.(*ComposePredictResult)
			s := res.Sweep
			if !res.Valid || s == nil || s.RNGSeed == nil || *s.RNGSeed != seeds[0] ||
				s.ExpandedCount != 12 || string(s.Mode) != mode {
				t.Fatalf("predict summary %+v (errors %+v), runtime seed %d", s, env.Errors, seeds[0])
			}
			if len(s.Axes) != 1 || s.Axes[0].Count != 12 || s.Axes[0].Range == nil {
				t.Fatalf("predict axes %+v", s.Axes)
			}
			resp, _ := p.Compose(ctx, c)
			labels := make([]string, len(resp.Responses))
			for i, r := range resp.Responses {
				labels[i] = r.Label
			}
			if !reflect.DeepEqual(labels, s.Labels) {
				t.Fatalf("runtime labels %q, predict %q", labels, s.Labels)
			}
		})
	}
}

// TestSweepRandom_KeyOrderSameDraws: at rng_seed 0 a sweep body that
// differs only in key order (any depth) and whitespace derives the same
// sweep_seed and byte-identical ComposedResponse JSON — ranking
// included — from Compose and ComposeParallel, and predict reports
// that seed.
func TestSweepRandom_KeyOrderSameDraws(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	shuffled := `{"tests":[{"params":{"mu":{"$var":"mu"}},"label":"t","field":"age","type":"TEST_T"}],
		"aggregations":[{"label":"v","field":"age","type":"AGG_AVERAGE"}], "cohort":{"filename":"` + parityCohort + `"}}`
	for _, mode := range []string{"lhs", "random"} {
		var wires []string
		for _, body := range []string{rankSweepBody, shuffled} {
			c := decodeComposedJSON(t, `{"sweep":{"mode":"`+mode+`","samples":6,
				"axes":[{"name":"mu","range":{"min":10,"max":60}}],
				"request":`+body+`,"rank":{"by":"tests.t.statistic"}}}`)
			for _, name := range []string{"Compose", "ComposeParallel"} {
				resp, err := composeRuns(p)[name](c)
				if err != nil {
					t.Fatalf("%s %s: %v", mode, name, err)
				}
				raw, _ := json.Marshal(resp)
				wires = append(wires, string(raw))
				env, err := p.PredictCompose(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				if s := env.Data.(*ComposePredictResult).Sweep; s == nil || s.RNGSeed == nil || resp.SweepSeed == nil || *s.RNGSeed != *resp.SweepSeed {
					t.Fatalf("%s %s: predict seed %+v, runtime %v", mode, name, s, resp.SweepSeed)
				}
			}
		}
		for i := 1; i < len(wires); i++ {
			if wires[i] != wires[0] {
				t.Fatalf("%s: run %d differs:\n%s\n%s", mode, i, wires[0], wires[i])
			}
		}
	}
}

// TestSweepRandom_SeedEchoAndReplay: a pinned rng_seed is echoed as is;
// a zero seed is echoed as the derived one, and pinning that value
// replays the run byte for byte. A grid sweep echoes no seed (the wire
// is as before: no sweep_seed key, no predict rng_seed).
func TestSweepRandom_SeedEchoAndReplay(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	pinned, err := p.Compose(ctx, sampledSweep(t, "random", 4, `,"rng_seed":7`))
	if err != nil {
		t.Fatal(err)
	}
	if pinned.SweepSeed == nil || *pinned.SweepSeed != 7 {
		t.Fatalf("pinned seed echo %v", pinned.SweepSeed)
	}
	derived, err := p.Compose(ctx, sampledSweep(t, "random", 4, ""))
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := json.Marshal(*derived.SweepSeed)
	replay, err := p.Compose(ctx, sampledSweep(t, "random", 4, `,"rng_seed":`+string(seed)))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(derived)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatalf("pinning the echoed seed does not replay:\n%s\n%s", a, b)
	}

	grid := decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"mu","values":[10,50]}],"request":`+rankSweepBody+`}}`)
	resp, err := p.Compose(ctx, grid)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resp)
	if resp.SweepSeed != nil || strings.Contains(string(raw), "sweep_seed") {
		t.Fatalf("grid sweep echoes a seed: %s", raw)
	}
	env, _ := p.PredictCompose(ctx, grid)
	if s := env.Data.(*ComposePredictResult).Sweep; s == nil || s.RNGSeed != nil {
		t.Fatalf("grid predict summary %+v", s)
	}
}

// TestSweepRandom_RefusalParity: every sampling refusal — and the
// MaxComposeSlots count over samples — is the same error (code,
// message, details) from PredictCompose, Compose and ComposeParallel,
// before any slot runs.
func TestSweepRandom_RefusalParity(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{Limits: Limits{MaxComposeSlots: 4}})
	axis := func(a string) string {
		return `{"sweep":{` + a + `,"request":` + rankSweepBody + `}}`
	}
	cases := []struct {
		name string
		raw  string
		code perr.Code
	}{
		{"range under grid", axis(`"axes":[{"name":"mu","range":{"min":1,"max":2}}]`), perr.PULSE_SWEEP_MODE_MISMATCH},
		{"values under random", axis(`"mode":"random","samples":2,"axes":[{"name":"mu","values":[1,2]}]`), perr.PULSE_SWEEP_MODE_MISMATCH},
		{"samples under zip", axis(`"mode":"zip","samples":2,"axes":[{"name":"mu","values":[1,2]}]`), perr.PULSE_SWEEP_MODE_MISMATCH},
		{"min >= max", axis(`"mode":"lhs","samples":2,"axes":[{"name":"mu","range":{"min":3,"max":3}}]`), perr.PULSE_SWEEP_RANGE_INVALID},
		{"log min <= 0", axis(`"mode":"lhs","samples":2,"axes":[{"name":"mu","range":{"min":0,"max":3,"scale":"log"}}]`), perr.PULSE_SWEEP_RANGE_INVALID},
		{"missing samples", axis(`"mode":"random","axes":[{"name":"mu","range":{"min":1,"max":3}}]`), perr.PULSE_SWEEP_SAMPLES_INVALID},
		{"over the slot limit", axis(`"mode":"lhs","samples":5,"axes":[{"name":"mu","range":{"min":1,"max":3}}]`), perr.PULSE_LIMIT_EXCEEDED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := decodeComposedJSON(t, tc.raw)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("predict errors %+v, want %s", env.Errors, tc.code)
			}
			for name, run := range composeRuns(p) {
				_, rerr := run(c)
				ce := requireCoded(t, rerr, tc.code)
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}
}

// TestSweepRandom_ForEachNeverSamples: an item for_each refuses the
// sampling modes on every surface with PULSE_FOR_EACH_INVALID.
func TestSweepRandom_ForEachNeverSamples(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	var req types.Request
	if err := json.Unmarshal([]byte(`{"cohort":{"filename":"`+parityCohort+`"},
		"attributes":[{"type":"ATTR_FORMULA","label":"a{{k}}","expression":"age * 2",
		"for_each":{"mode":"random","axes":[{"name":"k","values":[1,2]}]}}]}`), &req); err != nil {
		t.Fatal(err)
	}
	env, err := p.Predict(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_FOR_EACH_INVALID) {
		t.Fatalf("predict %+v", env.Errors)
	}
	_, rerr := p.Process(ctx, &req)
	ce := requireCoded(t, rerr, perr.PULSE_FOR_EACH_INVALID)
	if ce.Message != env.Errors[0].Message {
		t.Fatalf("runtime %q, predict %q", ce.Message, env.Errors[0].Message)
	}
}

// TestSweepRandom_SeedPinnedOutsideReturn: a Compose-level `return`
// shapes only the top-level overlays, so the echoed sweep_seed — like
// `ranking` — is always on the wire.
func TestSweepRandom_SeedPinnedOutsideReturn(t *testing.T) {
	p := sweepPulse(t, Options{})
	c := decodeComposedJSON(t, `{"return":{"preset":"minimal","precision":1},"sweep":{"mode":"random","samples":2,"rng_seed":9,
		"axes":[{"name":"mu","range":{"min":10,"max":60}}],"request":`+rankSweepBody+`}}`)
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			SweepSeed *int64 `json:"sweep_seed"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.SweepSeed == nil || *wire.SweepSeed != 9 {
			t.Fatalf("%s: shaped wire lost sweep_seed: %s", name, raw)
		}
	}
}
