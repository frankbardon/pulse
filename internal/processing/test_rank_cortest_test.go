package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// rankPinCase is one dataset the rank-test pins and parity tests run.
type rankPinCase struct {
	name string
	rows []wRow
	kind types.WeightKind
}

// rankPinData spans every cor.test switch: untied small n (exact for
// both methods), ties, untied n = 60 (Kendall's normal approximation,
// Spearman's AS 89 Edgeworth series), unit frequency weights (exact)
// and integer frequency weights (a weight above 1 is a tie).
func rankPinData() []rankPinCase {
	untied := func(n, mult, mod int) []wRow {
		var out []wRow
		for i := 0; i < n; i++ {
			out = append(out, wRow{x: float64(i) + 0.5, y: float64((i*mult)%mod) + 0.25*float64(i%2), w: 1})
		}
		return out
	}
	tied := []wRow{{1, 1, "", 1}, {2, 3, "", 1}, {2, 2, "", 1}, {4, 4, "", 1}, {5, 5, "", 1}, {5, 1, "", 1}, {7, 6, "", 1}, {8, 6, "", 1}, {9, 9, "", 1}, {3, 8, "", 1}}
	return []rankPinCase{
		{"untied8", untied(8, 3, 11), ""},
		{"untied12", untied(12, 5, 13), ""},
		{"tied10", tied, ""},
		{"untied60", untied(60, 37, 61), ""},
		{"unit_freq8", untied(8, 3, 11), types.WeightKindFrequency},
		{"freq_ties", rankFixture(), types.WeightKindFrequency},
	}
}

// runRankPost runs typ's tier-2 (post) variant over rows' x / y.
func runRankPost(t *testing.T, typ types.TestType, rows []wRow) *types.TestResult {
	t.Helper()
	spec := &types.Test{Type: typ, Field: "x", Field2: "y"}
	var post PostTest
	var err error
	if typ == types.TEST_SPEARMAN_R {
		post, err = newSpearmanRPost(spec, nil)
	} else {
		post, err = newKendallTauPost(spec, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	in := make([]map[string]any, len(rows))
	for i, r := range rows {
		in[i] = map[string]any{"x": r.x, "y": r.y}
	}
	res, err := post.Run(in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func bitsOf(v any) uint64 {
	switch x := v.(type) {
	case float64:
		return math.Float64bits(x)
	case nil:
		return 0
	}
	return 1
}

// rankStatBits is a rank test's non-p figures as IEEE-754 bits,
// captured from the code before the tests' p moved to cor.test
// (U28 E3-G2): Statistic, DF, details t, s, var_s (0 when absent).
type rankStatBits struct{ stat, df, t, s, varS uint64 }

// rankStatPins: ROW and POST give the same bits on every unweighted
// dataset, so one entry covers both variants there.
var rankStatPins = map[string]rankStatBits{
	"untied8/TEST_SPEARMAN_R":     {0x3fe5555555555555, 0x4018000000000000, 0x400186f174f88472, 0, 0},
	"untied8/TEST_KENDALL_TAU":    {0x3fe2492492492492, 0, 0, 0x4030000000000000, 0x4050555555555555},
	"untied12/TEST_SPEARMAN_R":    {0x3fc57b864407292d, 0x4024000000000000, 0x3fe13a504673df04, 0, 0},
	"untied12/TEST_KENDALL_TAU":   {0x3fbf07c1f07c1f08, 0, 0, 0x4020000000000000, 0x406a955555555555},
	"tied10/TEST_SPEARMAN_R":      {0x3fe51ab30afe6df0, 0x4020000000000000, 0x4003da37ca0c6aae, 0, 0},
	"tied10/TEST_KENDALL_TAU":     {0x3fe29aca6b29aca7, 0, 0, 0x4039000000000000, 0x405e45b05b05b05b},
	"untied60/TEST_SPEARMAN_R":    {0x3fbd3fbf5b34396b, 0x404d000000000000, 0x3fec071be0d93f48, 0, 0},
	"untied60/TEST_KENDALL_TAU":   {0x3fb84c5db8d736ac, 0, 0, 0x4065000000000000, 0x40d801d555555555},
	"unit_freq8/TEST_SPEARMAN_R":  {0x3fe5555555555555, 0x4018000000000000, 0x400186f174f88472, 0, 0},
	"unit_freq8/TEST_KENDALL_TAU": {0x3fe2492492492492, 0, 0, 0x4030000000000000, 0x4050555555555555},
	"freq_ties/TEST_SPEARMAN_R":   {0x3fe7f63a90f7ed19, 0x403f000000000000, 0x40192957473f1987, 0, 0},
	"freq_ties/TEST_KENDALL_TAU":  {0x3fe3cb316dc9e4cb, 0, 0, 0x406f200000000000, 0x40ab0a288a2288a2},
}

func rankStatBitsOf(res *types.TestResult) rankStatBits {
	return rankStatBits{math.Float64bits(res.Statistic), math.Float64bits(res.DF),
		bitsOf(res.Details["t"]), bitsOf(res.Details["s"]), bitsOf(res.Details["var_s"])}
}

// TestRankTests_StatisticsUnchangedByCorTestP: aligning the rank tests'
// p with cor.test (U28 E3-G2) moved only p (and Kendall's z, now the
// uncorrected deviate); ρ, τ, DF, t, S and Var(S) keep their pre-change
// bits on both variants, unweighted and under frequency weights.
func TestRankTests_StatisticsUnchangedByCorTestP(t *testing.T) {
	t.Parallel()

	for _, d := range rankPinData() {
		for _, typ := range []types.TestType{types.TEST_SPEARMAN_R, types.TEST_KENDALL_TAU} {
			key := d.name + "/" + string(typ)
			want, ok := rankStatPins[key]
			if !ok {
				t.Fatalf("%s: no pin", key)
			}
			if got := rankStatBitsOf(runWeightedRowTest(t, types.Test{Type: typ, Field: "x", Field2: "y"}, d.rows, d.kind)); got != want {
				t.Errorf("%s row: bits %#v, want %#v", key, got, want)
			}
			if d.kind == "" {
				if got := rankStatBitsOf(runRankPost(t, typ, d.rows)); got != want {
					t.Errorf("%s post: bits %#v, want %#v", key, got, want)
				}
			}
		}
	}
}

// TestRankTests_PIsCorTestHelper: each variant's p is the shared
// cor.test helper over its own rows (kendallCorTestP /
// spearmanCorTestP), the row and post variants agree, and Kendall's z
// is the uncorrected S/√Var(S).
func TestRankTests_PIsCorTestHelper(t *testing.T) {
	t.Parallel()

	for _, d := range rankPinData() {
		var xs, ys, ws []float64
		for _, r := range d.rows {
			w := 1.0
			if d.kind != "" {
				w = r.w
			}
			xs, ys, ws = append(xs, r.x), append(ys, r.y), append(ws, w)
		}
		rx, tx := weightedMidRanks(xs, ws)
		ry, ty := weightedMidRanks(ys, ws)
		var n float64
		for _, w := range ws {
			n += w
		}
		rho, _ := rankPearson(rx, ry, ws, n)
		c, dd, kx, ky := kendallCounts(xs, ys, ws)
		tau, _ := kendallTauB(c, dd, kx, ky)
		want := map[types.TestType]float64{
			types.TEST_SPEARMAN_R:  spearmanCorTestP(rho, rx, ry, n, tx, ty),
			types.TEST_KENDALL_TAU: kendallCorTestP(tau, c, dd, n, tx, ty),
		}
		for typ, wp := range want {
			res := runWeightedRowTest(t, types.Test{Type: typ, Field: "x", Field2: "y"}, d.rows, d.kind)
			if res.PValue != wp || math.IsNaN(wp) {
				t.Errorf("%s/%s row p = %.17g, cor.test helper %.17g", d.name, typ, res.PValue, wp)
			}
			if typ == types.TEST_KENDALL_TAU {
				if z, want := res.Details["z"].(float64), (c-dd)/math.Sqrt(kendallVarS(n, tx, ty)); z != want {
					t.Errorf("%s kendall z = %.17g, want uncorrected %.17g", d.name, z, want)
				}
			}
			if d.kind == "" {
				if pp := runRankPost(t, typ, d.rows).PValue; pp != wp {
					t.Errorf("%s/%s post p = %.17g, row %.17g", d.name, typ, pp, wp)
				}
			}
		}
	}
}
