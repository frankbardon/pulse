package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mmmfixture"
)

// mmm_lhs_sweep_test.go — the seeded `lhs` sweep over the media-mix
// decay fixture (mmm-estimation E2-S2), library surface: the sampled
// twin of the canonical decay sweep (internal/mmmfixture
// DecayLHSSweep) draws LHSSamples decays from [0, 0.95], ranks them by
// the fit's holdout RMSE, and the winner lands near the fixture's TRUE
// decay. The CLI and MCP twins live in internal/cli and internal/mcp
// (mmm_lhs_sweep_test.go).

// lhsDecayOf reads the drawn decay off a sampled slot's default label
// (sample=<i>_decay=<value>).
func lhsDecayOf(t *testing.T, label string) float64 {
	t.Helper()
	_, v, ok := strings.Cut(label, "_decay=")
	if !ok {
		t.Fatalf("label %q carries no decay", label)
	}
	d, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("label %q: %v", label, err)
	}
	return d
}

// assertLHSDecaySweep checks a sampled decay sweep result: LHSSamples
// slots, every one ranked, every drawn decay inside the range, one per
// stratum of [0, 0.95], the echoed seed set, and the best slot's decay
// within ±0.1 of the true decay. It returns the winning decay.
func assertLHSDecaySweep(t *testing.T, out *ComposedResponse) float64 {
	t.Helper()
	n := mmmfixture.LHSSamples
	if len(out.Responses) != n || len(out.Ranking) != n || out.SweepSeed == nil {
		t.Fatalf("slots %d, ranking %d, seed %v; want %d, %d and an echoed seed", len(out.Responses), len(out.Ranking), out.SweepSeed, n, n)
	}
	strata := make([]bool, n)
	for _, r := range out.Responses {
		if len(r.Regressions) != 1 || r.Regressions[0].Error != nil || r.Regressions[0].Holdout == nil || len(r.Data) != 0 {
			t.Fatalf("slot %s: regressions %+v, %d data rows; want one fit with a holdout and no data", r.Label, r.Regressions, len(r.Data))
		}
		d := lhsDecayOf(t, r.Label)
		if d < 0 || d > 0.95 {
			t.Fatalf("slot %s decay %v outside [0, 0.95]", r.Label, d)
		}
		k := min(int(d/0.95*float64(n)), n-1)
		if strata[k] {
			t.Fatalf("two slots in stratum %d (%s): not a Latin hypercube", k, r.Label)
		}
		strata[k] = true
	}
	for i := 1; i < n; i++ {
		if !(out.Ranking[i-1].Value <= out.Ranking[i].Value) {
			t.Fatalf("ranking not ascending at %d: %+v", i, out.Ranking)
		}
	}
	// The winner's decay is read off its typed axes, never its label;
	// the label only cross-checks it.
	top := out.Ranking[0]
	num, ok := top.Axes["decay"].(json.Number)
	if !ok {
		t.Fatalf("winner %+v carries no number decay axis", top)
	}
	best, err := num.Float64()
	if err != nil || best != lhsDecayOf(t, top.Label) || out.Responses[top.Slot].Label != top.Label {
		t.Fatalf("winner %+v: axes decay %v disagrees with its slot / label (%v)", top, best, err)
	}
	if math.Abs(best-mmmfixture.TrueDecay) > 0.1 {
		t.Fatalf("best slot %s: decay %v, want within 0.1 of the true %v; ranking %+v", out.Ranking[0].Label, best, mmmfixture.TrueDecay, out.Ranking)
	}
	return best
}

// TestSweepRandom_MMMLHSRanksNearTrueDecay: the lhs decay sweep runs
// through library Compose (serial and parallel, the same bytes), its
// winner lies within ±0.1 of the fixture's true decay, a rerun gives
// the same bytes, and pinning the echoed seed as rng_seed replays the
// same sample points and ranking. Predict counts the samples and
// reports the same seed.
func TestSweepRandom_MMMLHSRanksNearTrueDecay(t *testing.T) {
	p := mmmPulse(t)
	ctx := context.Background()
	out, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecayLHSSweep, nil))
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	best := assertLHSDecaySweep(t, out)
	t.Logf("lhs winner decay %v (truth %v), seed %d; top %+v", best, mmmfixture.TrueDecay, *out.SweepSeed, out.Ranking[:4])
	want, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}

	again, err := p.Compose(ctx, mmmComposed(t, mmmfixture.DecayLHSSweep, nil))
	if err != nil {
		t.Fatalf("Compose (rerun): %v", err)
	}
	parallel, err := p.ComposeParallel(ctx, mmmComposed(t, mmmfixture.DecayLHSSweep, nil), ComposeOptions{MaxWorkers: 4, FailFast: true})
	if err != nil {
		t.Fatalf("ComposeParallel: %v", err)
	}
	pinned := mmmComposed(t, mmmfixture.DecayLHSSweep, nil)
	pinned.Sweep.RNGSeed = *out.SweepSeed
	replay, err := p.Compose(ctx, pinned)
	if err != nil {
		t.Fatalf("Compose (pinned seed): %v", err)
	}
	for name, got := range map[string]*ComposedResponse{"rerun": again, "parallel": parallel, "pinned seed": replay} {
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, want) {
			t.Errorf("%s bytes differ from the first run", name)
		}
	}

	env, err := p.PredictCompose(ctx, mmmComposed(t, mmmfixture.DecayLHSSweep, nil))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
	}
	cv, ok := env.Data.(*descx.ComposeValidationResult)
	if !ok || !cv.Valid || cv.Sweep == nil || cv.Sweep.ExpandedCount != mmmfixture.LHSSamples ||
		cv.Sweep.RNGSeed == nil || *cv.Sweep.RNGSeed != *out.SweepSeed {
		t.Fatalf("compose predict %+v (sweep %+v); want %d slots and seed %d", env.Data, cv.Sweep, mmmfixture.LHSSamples, *out.SweepSeed)
	}
}
