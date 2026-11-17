//go:build heavy

package sweep_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// de_oracle_heavy_test.go compares mode de with scipy's
// differential_evolution on the same problem, settings and budget
// (testdata/de_oracle.json, scripts/reference/scipy/gen_sweep_de.py,
// make reference-sweep-de). The two draw from different random streams
// (and scipy resamples an out-of-bounds trial where de clips), so their
// trajectories cannot match: the test pins the OUTCOME over the same
// seed count instead.

const deOracleGolden = "testdata/de_oracle.json"

type deOracleProblem struct {
	Name       string      `json:"name"`
	Bounds     [][]float64 `json:"bounds"`
	Optimum    []float64   `json:"optimum"`
	Population int         `json:"population"`
	Budget     int         `json:"budget"`
	F          float64     `json:"f"`
	CR         float64     `json:"cr"`
	Runs       []struct {
		Seed int64     `json:"seed"`
		NFev int       `json:"nfev"`
		X    []float64 `json:"x"`
		Fun  float64   `json:"fun"`
	} `json:"runs"`
}

func deOracle(t *testing.T) []deOracleProblem {
	t.Helper()
	raw, err := os.ReadFile(deOracleGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference-sweep-de)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", deOracleGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference-sweep-de", deOracleGolden)
	}
	var doc struct {
		Problems []deOracleProblem `json:"problems"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Problems
}

// rosenbrock2 is the oracle's objective, every product rounded before
// its add.
func rosenbrock2(x, y float64) float64 {
	d1, d2 := 1-x, y-float64(x*x)
	return float64(d1*d1) + float64(100*float64(d2*d2))
}

// TestDE_MatchesScipyOracle: over the oracle's 20 seeds, on 2-D
// Rosenbrock with population 20, F 0.8, CR 0.9 and a budget of 2000,
// mode de and scipy both land every seed within 1e-3 of the optimum on
// every axis with a best value below 1e-6, and their median best values
// agree within two decades.
func TestDE_MatchesScipyOracle(t *testing.T) {
	t.Parallel()
	for _, p := range deOracle(t) {
		if p.Name != "rosenbrock2" {
			t.Fatalf("unknown oracle problem %q", p.Name)
		}
		var goBest, pyBest []float64
		for _, run := range p.Runs {
			if run.NFev != p.Budget {
				t.Fatalf("oracle seed %d spent %d evaluations, budget %d", run.Seed, run.NFev, p.Budget)
			}
			for j, x := range run.X {
				if math.Abs(x-p.Optimum[j]) > 1e-3 || run.Fun > 1e-6 {
					t.Fatalf("oracle seed %d missed the optimum: %v at %v", run.Seed, run.Fun, run.X)
				}
			}
			pyBest = append(pyBest, run.Fun)

			spec := &types.SweepSpec{
				Mode: types.SweepModeDE, Budget: p.Budget, RNGSeed: run.Seed,
				DE: &types.SweepDE{Population: &p.Population, F: &p.F, CR: &p.CR},
				Axes: []types.SweepAxis{
					{Name: "x", Range: &types.SweepRange{Min: &p.Bounds[0][0], Max: &p.Bounds[0][1]}},
					{Name: "y", Range: &types.SweepRange{Min: &p.Bounds[1][0], Max: &p.Bounds[1][1]}},
				},
				Request: json.RawMessage(`{"params":{"x":{"$var":"x"},"y":{"$var":"y"}}}`),
				Rank:    &types.SweepRank{By: "v"},
			}
			ev, err := sweeptest.Search(spec, func(a map[string]any) (float64, bool) {
				return rosenbrock2(axisNumber(t, a["x"]), axisNumber(t, a["y"])), true
			})
			if err != nil {
				t.Fatal(err)
			}
			b := best(ev)
			x, y := axisNumber(t, b.Axes["x"]), axisNumber(t, b.Axes["y"])
			if len(ev) != p.Budget || math.Abs(x-p.Optimum[0]) > 1e-3 || math.Abs(y-p.Optimum[1]) > 1e-3 || b.Value > 1e-6 {
				t.Errorf("seed %d: %d slots, best %v at (%v, %v)", run.Seed, len(ev), b.Value, x, y)
			}
			goBest = append(goBest, b.Value)
		}
		gm, pm := median(goBest), median(pyBest)
		if math.Abs(math.Log10(gm)-math.Log10(pm)) > 2 {
			t.Errorf("median best value: de %g, scipy %g — more than two decades apart", gm, pm)
		}
		t.Logf("%s: median best de %g, scipy %g", p.Name, gm, pm)
	}
}

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
