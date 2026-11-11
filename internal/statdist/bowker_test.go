package statdist

import (
	"math"
	"testing"
)

// TestBowkerExactP_OnePairIsBinomial: on one discordant pair the exact
// Bowker test is the exact binomial McNemar — its p equals
// BinomialTwoSidedP(n_12, n, ½) (binom.test) over a grid of small and
// large pairs, both tails, ties and one-way tables, and the statistic is
// (n_12 − n_21)² / n.
func TestBowkerExactP_OnePairIsBinomial(t *testing.T) {
	t.Parallel()

	grid := [][2]int{{150, 86}, {86, 150}, {300, 288}, {1000, 940}}
	for a := 0; a <= 25; a++ {
		for b := 0; b <= 25; b++ {
			grid = append(grid, [2]int{a, b})
		}
	}
	for _, pr := range grid {
		stat, p, _, ok := BowkerExactP([][2]int{pr}, BowkerExactMaxTerms)
		if !ok {
			t.Fatalf("%v refused", pr)
		}
		n := pr[0] + pr[1]
		if n == 0 {
			if !math.IsNaN(stat) || !math.IsNaN(p) {
				t.Errorf("%v: stat %v p %v, want NaN (nothing to test)", pr, stat, p)
			}
			continue
		}
		want := BinomialTwoSidedP(pr[0], n, 0.5)
		if !(math.Abs(p-want) <= 1e-12*want) {
			t.Errorf("%v: p %.17g, want binom.test %.17g", pr, p, want)
		}
		d := float64(pr[0] - pr[1])
		if wantStat := float64(d*d) / float64(n); stat != wantStat {
			t.Errorf("%v: statistic %v, want %v", pr, stat, wantStat)
		}
	}
}

// TestBowkerExactP_ClosedForms: small tables whose exact p is a dyadic
// rational by hand. Two pairs of one move each always give X² = 2
// (p = 1); pairs (2, 0) and (1, 1) give X² = 2 + 0, and X² ≥ 2 fails
// only when both pairs split evenly, so p = 1 − ¼; (3, 0) and (2, 0): X² = 3 + 2 is the
// maximum, P = 2/8 · 2/4 = 1/8. An empty pair is skipped.
func TestBowkerExactP_ClosedForms(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		pairs      [][2]int
		stat, want float64
	}{
		{[][2]int{{1, 0}, {0, 1}}, 2, 1},
		{[][2]int{{2, 0}, {1, 1}}, 2, 0.75},
		{[][2]int{{3, 0}, {0, 0}, {2, 0}}, 5, 0.125},
		{[][2]int{{1, 1}, {2, 2}}, 0, 1},
	} {
		stat, p, _, ok := BowkerExactP(c.pairs, BowkerExactMaxTerms)
		if !ok || stat != c.stat || math.Abs(p-c.want) > 1e-15 {
			t.Errorf("%v: stat %v p %.17g ok %v, want %v and %v", c.pairs, stat, p, ok, c.stat, c.want)
		}
	}
	if _, p, _, ok := BowkerExactP([][2]int{{-1, 2}}, BowkerExactMaxTerms); !ok || !math.IsNaN(p) {
		t.Errorf("negative count: p %v ok %v, want NaN", p, ok)
	}
}

// TestBowkerExactP_WorkCap: the convolution stops before it would
// enumerate more than maxTerms terms — ok false, p NaN, the statistic
// still set and terms the count that broke the cap — and runs at
// exactly the cap. A many-moves 4 x 4 is refused at BowkerExactMaxTerms.
func TestBowkerExactP_WorkCap(t *testing.T) {
	t.Parallel()

	pairs := [][2]int{{3, 1}, {2, 2}, {4, 0}} // widths 3, 3, 3: 3 + 3·3 + 6·3 terms (values merge)
	_, full, terms, ok := BowkerExactP(pairs, BowkerExactMaxTerms)
	if !ok || math.IsNaN(full) {
		t.Fatalf("uncapped: p %v ok %v", full, ok)
	}
	_, p, got, ok := BowkerExactP(pairs, terms)
	if !ok || p != full || got != terms {
		t.Fatalf("at the cap %d: p %v terms %d ok %v, want %v", terms, p, got, ok, full)
	}
	stat, p, got, ok := BowkerExactP(pairs, terms-1)
	if ok || !math.IsNaN(p) || got <= terms-1 || stat != 1+0+4 {
		t.Fatalf("one under the cap: stat %v p %v terms %d ok %v", stat, p, got, ok)
	}

	big := [][2]int{{30, 25}, {28, 31}, {40, 22}, {27, 33}, {35, 30}, {29, 36}}
	if _, p, terms, ok := BowkerExactP(big, BowkerExactMaxTerms); ok || !math.IsNaN(p) || terms <= BowkerExactMaxTerms {
		t.Fatalf("large 4 x 4: p %v terms %d ok %v, want refused past %d", p, terms, ok, BowkerExactMaxTerms)
	}
}

// TestBowkerTail_RelErr: an outcome counts as at least as extreme when
// its statistic times 1 + 1e-7 reaches the observed one (fisher.test's
// relErr), so a value 5e-8 below the observed statistic — the same
// rational reached through other roundings — is in, and one 2e-7 below
// is out; the sum is clamped at 1.
func TestBowkerTail_RelErr(t *testing.T) {
	t.Parallel()

	support := []bowkerTerm{{v: 1, p: 0.5}, {v: 2, p: 0.25}, {v: 3, p: 0.25}}
	for _, c := range []struct{ obs, want float64 }{
		{2, 0.5}, {float64(2 * (1 + 5e-8)), 0.5}, {float64(2 * (1 + 2e-7)), 0.25}, {0, 1}, {3.5, 0},
	} {
		if got := bowkerTail(support, c.obs); got != c.want {
			t.Errorf("bowkerTail(obs %.17g) = %v, want %v", c.obs, got, c.want)
		}
	}
	if got := bowkerTail([]bowkerTerm{{v: 0, p: 0.6}, {v: 1, p: 0.6}}, 0); got != 1 {
		t.Errorf("unclamped tail %v", got)
	}
}
