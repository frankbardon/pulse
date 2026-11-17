//go:build heavy

package sweep_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// bo_oracle_heavy_test.go compares mode bo with scikit-optimize's
// gp_minimize on the same problem, settings and budget
// (testdata/bo_oracle.json, scripts/reference/scipy/gen_sweep_bo.py,
// make reference-sweep-bo), and checks bo beats a random search of the
// same budget. The two engines draw from different random streams (and
// skopt polishes by L-BFGS-B where bo runs SLSQP), so their trajectories
// cannot match: the test pins the OUTCOME over the same seed count.

const boOracleGolden = "testdata/bo_oracle.json"

type boOracleProblem struct {
	Name         string      `json:"name"`
	Bounds       [][]float64 `json:"bounds"`
	OptimumValue float64     `json:"optimum_value"`
	NInit        int         `json:"n_init"`
	Budget       int         `json:"budget"`
	Runs         []struct {
		Seed int64     `json:"seed"`
		NFev int       `json:"nfev"`
		X    []float64 `json:"x"`
		Fun  float64   `json:"fun"`
	} `json:"runs"`
}

func boOracle(t *testing.T) []boOracleProblem {
	t.Helper()
	raw, err := os.ReadFile(boOracleGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference-sweep-bo)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", boOracleGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference-sweep-bo", boOracleGolden)
	}
	var doc struct {
		Problems []boOracleProblem `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Problems
}

// branin is the oracle's objective (Branin–Hoo; minimum 0.397887 at
// three points), every product rounded before its add.
func branin(x, y float64) float64 {
	const pi = math.Pi
	b, c, tt := 5.1/(4*pi*pi), 5/pi, 1/(8*pi)
	d := y - float64(b*float64(x*x)) + float64(c*x) - 6
	return float64(d*d) + float64(10*(1-tt)*detmath.Cos(x)) + 10
}

// braninSpec is a mode-bo (or random) sweep over p's bounds.
func braninSpec(p boOracleProblem, mode types.SweepMode, seed int64) *types.SweepSpec {
	spec := &types.SweepSpec{
		Mode: mode, RNGSeed: seed,
		Axes: []types.SweepAxis{
			{Name: "x", Range: &types.SweepRange{Min: &p.Bounds[0][0], Max: &p.Bounds[0][1]}},
			{Name: "y", Range: &types.SweepRange{Min: &p.Bounds[1][0], Max: &p.Bounds[1][1]}},
		},
		Request: json.RawMessage(`{"params":{"x":{"$var":"x"},"y":{"$var":"y"}}}`),
		Rank:    &types.SweepRank{By: "v"},
	}
	if mode == types.SweepModeBO {
		spec.Budget, spec.BO = p.Budget, &types.SweepBO{NInit: &p.NInit}
	} else {
		spec.Samples = p.Budget
	}
	return spec
}

// boBranin runs mode bo on the oracle problem at seed: its best value.
func boBranin(t *testing.T, p boOracleProblem, seed int64) float64 {
	t.Helper()
	ev, err := sweeptest.Search(braninSpec(p, types.SweepModeBO, seed), func(a map[string]any) (float64, bool) {
		return branin(axisNumber(t, a["x"]), axisNumber(t, a["y"])), true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != p.Budget {
		t.Fatalf("seed %d: %d slots, budget %d", seed, len(ev), p.Budget)
	}
	return best(ev).Value
}

// TestBO_MatchesSkoptOracle: over the oracle's 20 seeds, on Branin with
// a 4-point Latin-hypercube design and a budget of 30, mode bo and
// gp_minimize both bring every seed within 0.05 of the optimum value,
// and their median best values agree within 0.005.
func TestBO_MatchesSkoptOracle(t *testing.T) {
	t.Parallel()
	for _, p := range boOracle(t) {
		if p.Name != "branin" {
			t.Fatalf("unknown oracle problem %q", p.Name)
		}
		var goBest, pyBest []float64
		for _, run := range p.Runs {
			if run.NFev != p.Budget {
				t.Fatalf("oracle seed %d spent %d evaluations, budget %d", run.Seed, run.NFev, p.Budget)
			}
			if run.Fun-p.OptimumValue > 0.05 {
				t.Fatalf("oracle seed %d missed the optimum: %v at %v", run.Seed, run.Fun, run.X)
			}
			pyBest = append(pyBest, run.Fun)
			b := boBranin(t, p, run.Seed)
			if b-p.OptimumValue > 0.05 {
				t.Errorf("seed %d: best %v, optimum %v", run.Seed, b, p.OptimumValue)
			}
			goBest = append(goBest, b)
		}
		gm, pm := median(goBest), median(pyBest)
		if math.Abs(gm-pm) > 0.005 {
			t.Errorf("median best value: bo %v, skopt %v — more than 0.005 apart", gm, pm)
		}
		t.Logf("%s: median best bo %v, skopt %v", p.Name, gm, pm)
	}
}

// TestBO_BeatsRandom: on the oracle problem and budget, mode bo's
// median best value over the 20 seeds is within 0.01 of the optimum and
// below a random search's (mode random, samples = budget, same seeds).
func TestBO_BeatsRandom(t *testing.T) {
	t.Parallel()
	p := boOracle(t)[0]
	var boBest, rndBest []float64
	for seed := int64(1); seed <= 20; seed++ {
		boBest = append(boBest, boBranin(t, p, seed))
		exp, err := sweep.Expand(&types.ComposedRequest{Sweep: braninSpec(p, types.SweepModeRandom, seed)}, sweep.Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		b := math.Inf(1)
		for _, a := range exp.Axes {
			b = min(b, branin(axisNumber(t, a["x"]), axisNumber(t, a["y"])))
		}
		rndBest = append(rndBest, b)
	}
	bm, rm := median(boBest), median(rndBest)
	if bm-p.OptimumValue > 0.01 || bm >= rm {
		t.Errorf("median best: bo %v, random %v (optimum %v)", bm, rm, p.OptimumValue)
	}
	t.Logf("median best: bo %v, random %v", bm, rm)
}
