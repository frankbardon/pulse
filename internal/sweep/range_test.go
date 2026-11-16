package sweep

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// integerSpec is a sampled sweep over two integer range axes: decay on
// [0.2, 6.7] (the integers 1..6, linear) and lambda on [1, 1000] (log).
func integerSpec(mode types.SweepMode, n int) *types.SweepSpec {
	spec := sampledSpec(mode, n)
	spec.Axes[0].Range = &types.SweepRange{Min: fp(0.2), Max: fp(6.7), Integer: true}
	spec.Axes[1].Range = &types.SweepRange{Min: fp(1), Max: fp(1000), Scale: types.SweepScaleLog, Integer: true}
	return spec
}

// paramText is slot i's params.<key> as its JSON text.
func paramText(t *testing.T, exp *Expansion, i int, key string) string {
	t.Helper()
	b, err := json.Marshal(exp.Composed.Requests[i].Params[key])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSweepRange_IntegerDraws: under integer every draw is a whole
// number inside [ceil(min), floor(max)], rendered in integer form; a
// linear integer axis reaches every integer of its range, and a log one
// spreads log-uniformly (≈40% of draws on [1, 1000] are at most 10,
// against ≈1% linear).
func TestSweepRange_IntegerDraws(t *testing.T) {
	t.Parallel()

	const n = 600
	for _, mode := range []types.SweepMode{types.SweepModeRandom, types.SweepModeLHS} {
		spec := integerSpec(mode, n)
		spec.RNGSeed = 13
		rows, exp := drawn(t, spec)
		hit := map[float64]int{}
		small := 0
		for i, row := range rows {
			for _, ax := range spec.Axes {
				v := row[ax.Name]
				if v != math.Trunc(v) || v < math.Ceil(*ax.Range.Min) || v > math.Floor(*ax.Range.Max) {
					t.Fatalf("%s slot %d %s=%v: not an integer in range", mode, i, ax.Name, v)
				}
			}
			for _, key := range []string{"d", "l"} {
				if txt := paramText(t, exp, i, key); strings.ContainsAny(txt, ".eE") {
					t.Fatalf("%s slot %d %s rendered %q, want integer form", mode, i, key, txt)
				}
			}
			hit[row["decay"]]++
			if row["lambda"] <= 10 {
				small++
			}
		}
		if len(hit) != 6 {
			t.Fatalf("%s: linear integer axis reached %v, want each of 1..6", mode, hit)
		}
		if frac := float64(small) / n; frac < 0.3 || frac > 0.5 {
			t.Fatalf("%s: log integer draws <= 10: %v, want ~0.40", mode, frac)
		}
	}
}

// TestSweepRange_IntegerLHSStratifies: an lhs over a linear integer
// range with as many samples as integers draws every integer exactly
// once — each stratum maps to one integer.
func TestSweepRange_IntegerLHSStratifies(t *testing.T) {
	t.Parallel()

	for seed := int64(1); seed <= 20; seed++ {
		spec := integerSpec(types.SweepModeLHS, 6)
		spec.RNGSeed = seed
		rows, _ := drawn(t, spec)
		seen := map[float64]int{}
		for _, row := range rows {
			seen[row["decay"]]++
		}
		for k := 1.0; k <= 6; k++ {
			if seen[k] != 1 {
				t.Fatalf("seed %d: integer %v drawn %d times (%v), want once", seed, k, seen[k], seen)
			}
		}
	}
}

// TestSweepRange_IntegerFrozenDraws pins an integer sweep's first draws
// to their exact text on every architecture (CI runs amd64): the map is
// integer PCG words, FMA-free arithmetic, linalg/detmath and an exact
// floor.
func TestSweepRange_IntegerFrozenDraws(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mode types.SweepMode
		want []string
	}{
		{types.SweepModeRandom, frozenIntegerRandom},
		{types.SweepModeLHS, frozenIntegerLHS},
	} {
		spec := integerSpec(tc.mode, 3)
		spec.RNGSeed = 42
		exp, err := Expand(sampledComposed(spec), Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(exp.Labels, tc.want) {
			t.Fatalf("%s labels\n got %q\nwant %q", tc.mode, exp.Labels, tc.want)
		}
	}
}

var (
	frozenIntegerRandom = []string{
		"sample=0_decay=3_lambda=813",
		"sample=1_decay=5_lambda=7",
		"sample=2_decay=6_lambda=658",
	}
	frozenIntegerLHS = []string{
		"sample=0_decay=2_lambda=16",
		"sample=1_decay=5_lambda=1",
		"sample=2_decay=4_lambda=729",
	}
)

// TestRangeValue_UnitMap: RangeValue maps u = 0 to min (ceil(min)
// under integer), the top of [0, 1) to max (floor(max)), keeps u's
// order, lands log's midpoint on the geometric mean and clamps a u
// outside [0, 1) onto a bound — the one point map an adaptive proposer
// reuses.
func TestRangeValue_UnitMap(t *testing.T) {
	t.Parallel()

	const top = 1 - 0x1p-53
	lin := &types.SweepRange{Min: fp(-2), Max: fp(3)}
	lg := &types.SweepRange{Min: fp(1e-2), Max: fp(1e2), Scale: types.SweepScaleLog}
	in := &types.SweepRange{Min: fp(0.5), Max: fp(4.5), Integer: true}
	for _, tc := range []struct {
		rg       *types.SweepRange
		u, want  float64
		approx   bool
		wantText string
	}{
		{lin, 0, -2, false, "-2"},
		{lin, top, 3, true, ""},
		{lin, 0.5, 0.5, false, "0.5"},
		{lin, -1, -2, false, "-2"},
		{lin, 2, 3, false, "3"},
		{lg, 0, 1e-2, true, ""},
		{lg, 0.5, 1, true, ""},
		{lg, top, 1e2, true, ""},
		{in, 0, 1, false, "1"},
		{in, top, 4, false, "4"},
		{in, 0.5, 3, false, "3"}, // [0.5, 4.5] → 2.5 rounds half up
		{in, 0.49, 2, false, "2"},
	} {
		got := RangeValue(tc.rg, tc.u)
		if tc.approx && math.Abs(got-tc.want) > 1e-9*math.Max(1, math.Abs(tc.want)) || !tc.approx && got != tc.want {
			t.Fatalf("RangeValue(%+v, %v) = %v, want %v", *tc.rg, tc.u, got, tc.want)
		}
		if tc.wantText != "" && string(RangeNumber(tc.rg, tc.u)) != tc.wantText {
			t.Fatalf("RangeNumber(%+v, %v) = %s, want %s", *tc.rg, tc.u, RangeNumber(tc.rg, tc.u), tc.wantText)
		}
	}
	for _, rg := range []*types.SweepRange{lin, lg, in} {
		prev := math.Inf(-1)
		for i := 0; i < 1000; i++ {
			v := RangeValue(rg, float64(i)/1000)
			if v < prev {
				t.Fatalf("%+v: not monotone at u=%v (%v < %v)", *rg, float64(i)/1000, v, prev)
			}
			prev = v
		}
	}
	big := &types.SweepRange{Min: fp(1e6), Max: fp(2e6), Integer: true}
	if got := string(RangeNumber(big, 0)); got != "1000000" {
		t.Fatalf("integer 1e6 renders %q, want 1000000", got)
	}
}
