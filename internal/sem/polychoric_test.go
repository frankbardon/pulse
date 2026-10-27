package sem

import (
	"encoding/csv"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"slices"
	"strconv"
	"testing"
)

// The two-step ordinal statistics (normal.go, bvn.go, ordinal.go,
// ordinal_scores.go) against their oracles:
//   - testdata/bvn_reference.json (bvn_reference.R): R's pbivnorm and an
//     independent quadrature of Sheppard's integral, 1e-12 absolute;
//   - testdata/<name>_lavaan.json (fit_lavaan_polychoric.R): lavaan
//     0.7.3's lavCor(..., ordered = ...) sample statistics, 1e-6
//     absolute (lavaan's nlminb stops near 1e-7 of the root), and the
//     script's independent re-derivation (root-found to 1e-15), 1e-12;
//   - lavaan's casewise scores SC, INNER and A21 (muthen1984).

type bvnRefPoint struct {
	H, K, Rho, Pbivnorm, Sheppard float64
}

type bvnReference struct {
	Pbivnorm string        `json:"pbivnorm"`
	R        string        `json:"r"`
	Gap      float64       `json:"max_abs_reference_gap"`
	Points   []bvnRefPoint `json:"points"`
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestPolychoric_BVNMatchesReference holds BivariateNormalCDF to both
// references across the grid: tails to |h|, |k| = 8, ρ = ±1, and either
// side of the port's branch edges (|ρ| 0.3, 0.75, 0.925).
func TestPolychoric_BVNMatchesReference(t *testing.T) {
	var ref bvnReference
	readJSON(t, "testdata/bvn_reference.json", &ref)
	if ref.Pbivnorm == "" || len(ref.Points) < 2000 {
		t.Fatalf("reference table incomplete: pbivnorm %q, %d points", ref.Pbivnorm, len(ref.Points))
	}
	const tol = 1e-12
	worst := 0.0
	for _, p := range ref.Points {
		got := BivariateNormalCDF(p.H, p.K, p.Rho)
		for _, want := range []float64{p.Pbivnorm, p.Sheppard} {
			d := math.Abs(got - want)
			worst = math.Max(worst, d)
			if !(d <= tol) {
				t.Errorf("Φ₂(%v, %v; %v) = %.17g, reference %.17g (|Δ| %.3g > %g)", p.H, p.K, p.Rho, got, want, d, tol)
			}
		}
	}
	t.Logf("%d points, worst |Δ| %.3g (references agree to %.3g)", len(ref.Points), worst, ref.Gap)
}

// TestPolychoric_BVNLimits: infinite limits reduce exactly; |ρ| > 1 and
// NaN are NaN; Φ₂ is symmetric in (h, k) and reduces to Φ(h)Φ(k) at 0.
func TestPolychoric_BVNLimits(t *testing.T) {
	inf := math.Inf(1)
	cases := []struct{ h, k, r, want float64 }{
		{inf, 0.3, 0.5, stdNormCDF(0.3)},
		{-0.7, inf, -0.5, stdNormCDF(-0.7)},
		{inf, inf, 0.99, 1},
		{-inf, 2, 0.4, 0},
		{1, -inf, 0.4, 0},
	}
	for _, c := range cases {
		if got := BivariateNormalCDF(c.h, c.k, c.r); got != c.want {
			t.Errorf("Φ₂(%v, %v; %v) = %v, want %v", c.h, c.k, c.r, got, c.want)
		}
	}
	for _, c := range [][3]float64{{0, 0, 1.0000001}, {0, 0, -2}, {math.NaN(), 0, 0}, {0, 0, math.NaN()}} {
		if got := BivariateNormalCDF(c[0], c[1], c[2]); !math.IsNaN(got) {
			t.Errorf("Φ₂(%v) = %v, want NaN", c, got)
		}
	}
	for _, r := range []float64{-0.95, -0.4, 0.2, 0.8, 0.97} {
		if a, b := BivariateNormalCDF(0.3, -1.1, r), BivariateNormalCDF(-1.1, 0.3, r); math.Abs(a-b) > 1e-15 {
			t.Errorf("ρ=%v: asymmetric %v vs %v", r, a, b)
		}
	}
	if got, want := BivariateNormalCDF(0.3, -1.1, 0), stdNormCDF(0.3)*stdNormCDF(-1.1); math.Abs(got-want) > 1e-16 {
		t.Errorf("ρ = 0: %v, want %v", got, want)
	}
}

// TestPolychoric_NormalQuantile holds AS 241 to R's qnorm (R 4.6.1,
// %.17g) across both rational arms, their edges and the deep tail.
func TestPolychoric_NormalQuantile(t *testing.T) {
	for _, c := range [][2]float64{
		{1e-300, -37.047096299361201},
		{1e-20, -9.262340089798407},
		{1e-11, -6.7060231554951359},
		{1e-5, -4.2648907939228247},
		{0.003, -2.7477813854449926},
		{0.074, -1.4466320671589785},
		{0.0751, -1.4388253927525398},
		{0.2, -0.84162123357291418},
		{0.5, 0},
		{0.7, 0.52440051270804067},
		{0.925, 1.4395314709384559},
		{0.9251, 1.4402382675279632},
		{0.99, 2.3263478740408408},
		{1 - 1e-9, 5.9978070196016384},
	} {
		if got := stdNormQuantile(c[0]); math.Abs(got-c[1]) > 4e-16*math.Max(1, math.Abs(c[1])) {
			t.Errorf("Φ⁻¹(%v) = %.17g, R qnorm %.17g", c[0], got, c[1])
		}
	}
	if !math.IsInf(stdNormQuantile(0), -1) || !math.IsInf(stdNormQuantile(1), 1) || !math.IsNaN(stdNormQuantile(math.NaN())) {
		t.Error("Φ⁻¹ special values")
	}
}

// twoStepRef is a fit_lavaan_polychoric.R reference.
type twoStepRef struct {
	Lavaan  string `json:"lavaan"`
	Options struct {
		Vars    []string `json:"vars"`
		Ordered []string `json:"ordered"`
		Group   *string  `json:"group"`
		Weight  *string  `json:"weight"`
	} `json:"options"`
	Groups []struct {
		Label       string               `json:"label"`
		N           int                  `json:"n"`
		Thresholds  map[string][]float64 `json:"thresholds"`
		Mean        map[string]float64   `json:"mean"`
		Var         map[string]float64   `json:"var"`
		Cor         [][]*float64         `json:"cor"`
		Independent struct {
			Thresholds map[string][]float64 `json:"thresholds"`
			Cor        [][]*float64         `json:"cor"`
		} `json:"independent"`
	} `json:"groups"`
	Scores *struct {
		Rows  [][]float64 `json:"rows"`
		Inner [][]float64 `json:"inner"`
		A21   [][]float64 `json:"a21"`
	} `json:"scores"`
}

// readCSV returns a CSV's header-keyed columns.
func readCSV(t *testing.T, path string) map[string][]float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string][]float64{}
	for _, r := range recs[1:] {
		for c, name := range recs[0] {
			v, err := strconv.ParseFloat(r[c], 64)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			cols[name] = append(cols[name], v)
		}
	}
	return cols
}

// twoStepInput builds a reference's TwoStepInput: the ordered columns
// coded 0..K-1 by their sorted distinct values (lavaan's ordered()), the
// group column's sorted levels, and the weight column as frequency
// weights.
func twoStepInput(t *testing.T, ref twoStepRef, data string) (TwoStepInput, []int, int) {
	t.Helper()
	cols := readCSV(t, data)
	vars := ref.Options.Vars
	in := TwoStepInput{Kinds: make([]OrdinalKind, len(vars)), Levels: make([]int, len(vars))}
	n := len(cols[vars[0]])
	in.Rows = make([][]float64, n)
	for i := range in.Rows {
		in.Rows[i] = make([]float64, len(vars))
	}
	for v, name := range vars {
		col := cols[name]
		if !slices.Contains(ref.Options.Ordered, name) {
			for i, x := range col {
				in.Rows[i][v] = x
			}
			continue
		}
		levels := slices.Clone(col)
		slices.Sort(levels)
		levels = slices.Compact(levels)
		in.Kinds[v], in.Levels[v] = KindOrdered, len(levels)
		for i, x := range col {
			in.Rows[i][v] = float64(slices.Index(levels, x))
		}
	}
	if ref.Options.Weight != nil {
		in.Weights = cols[*ref.Options.Weight]
	}
	if ref.Options.Group == nil {
		return in, nil, 0
	}
	g := cols[*ref.Options.Group]
	levels := slices.Clone(g)
	slices.Sort(levels)
	levels = slices.Compact(levels)
	group := make([]int, n)
	for i, x := range g {
		group[i] = slices.Index(levels, x)
	}
	return in, group, len(levels)
}

// twoStepFixtures are the reference datasets: the brand survey (nine
// 1–5 ratings and the 0–10 recommend), per wave, and the edge sets.
var twoStepFixtures = []struct{ ref, data string }{
	{"brand_twostep", "brand"},
	{"brand_twostep_mg", "brand"},
	{"poly_extreme", "poly_extreme"},
	{"poly_sparse", "poly_sparse"},
	{"poly_binary", "poly_binary"},
	{"poly_weighted", "poly_weighted"},
}

func fitTwoStepFixture(t *testing.T, name, data string) (twoStepRef, TwoStepInput, []*TwoStepStats) {
	t.Helper()
	var ref twoStepRef
	readJSON(t, "testdata/"+name+"_lavaan.json", &ref)
	if ref.Lavaan == "" {
		t.Fatalf("%s: no lavaan release recorded", name)
	}
	in, group, levels := twoStepInput(t, ref, "testdata/"+data+"_data.csv")
	if group == nil {
		s, err := TwoStep(in)
		if err != nil {
			t.Fatal(err)
		}
		return ref, in, []*TwoStepStats{s}
	}
	ss, err := TwoStepGroups(in, group, levels)
	if err != nil {
		t.Fatal(err)
	}
	return ref, in, ss
}

// TestPolychoric_MatchesLavaan holds thresholds, numeric means and
// variances, and every polychoric, polyserial and Pearson correlation
// to lavaan within 1e-6 and to the independent derivation within 1e-12,
// on every fixture (grouped and frequency-weighted ones included).
func TestPolychoric_MatchesLavaan(t *testing.T) {
	const lavTol, indTol = 1e-6, 1e-12
	for _, fx := range twoStepFixtures {
		t.Run(fx.ref, func(t *testing.T) {
			ref, _, stats := fitTwoStepFixture(t, fx.ref, fx.data)
			if len(stats) != len(ref.Groups) {
				t.Fatalf("%d groups, reference %d", len(stats), len(ref.Groups))
			}
			worstLav, worstInd := 0.0, 0.0
			check := func(what string, got, want, tol float64, worst *float64) {
				d := math.Abs(got - want)
				*worst = math.Max(*worst, d)
				if !(d <= tol) {
					t.Errorf("%s = %.12g, want %.12g (|Δ| %.3g > %g)", what, got, want, d, tol)
				}
			}
			for g, s := range stats {
				rg := ref.Groups[g]
				if math.Abs(s.N-float64(rg.N)) > 1e-9 {
					t.Errorf("group %d: N %v, reference %d", g, s.N, rg.N)
				}
				for v, name := range ref.Options.Vars {
					if s.Kinds[v] == KindOrdered {
						if len(s.Thresholds[v]) != len(rg.Thresholds[name]) {
							t.Fatalf("%s: %d thresholds, reference %d", name, len(s.Thresholds[v]), len(rg.Thresholds[name]))
						}
						for k, th := range s.Thresholds[v] {
							check(name+"|t"+strconv.Itoa(k+1), th, rg.Thresholds[name][k], lavTol, &worstLav)
							check(name+"|t"+strconv.Itoa(k+1)+" (independent)", th, rg.Independent.Thresholds[name][k], indTol, &worstInd)
						}
						continue
					}
					check(name+" mean", s.Mean[v], rg.Mean[name], lavTol, &worstLav)
					check(name+" var", s.Var[v], rg.Var[name], lavTol*math.Max(1, rg.Var[name]), &worstLav)
				}
				for _, p := range s.Pairs {
					what := p.Kind.String() + " " + ref.Options.Vars[p.I] + "~~" + ref.Options.Vars[p.J]
					check(what, p.Rho, *rg.Cor[p.I][p.J], lavTol, &worstLav)
					if ind := rg.Independent.Cor[p.I][p.J]; ind != nil {
						check(what+" (independent)", p.Rho, *ind, indTol, &worstInd)
					}
					if s.Cor[p.I][p.J] != p.Rho || s.Cor[p.J][p.I] != p.Rho {
						t.Errorf("%s: Cor does not mirror the pair estimate", what)
					}
				}
			}
			t.Logf("worst |Δ| vs lavaan %.3g, vs independent %.3g", worstLav, worstInd)
		})
	}
}

// TestPolychoric_EdgeCases pins the edge fixtures' structural outcomes:
// the ±0.999 / ±0.995 bounds, lavaan's 2 × 2 shortcuts and zero cells.
func TestPolychoric_EdgeCases(t *testing.T) {
	pair := func(s *TwoStepStats, vars []string, a, b string) PairEstimate {
		i, j := slices.Index(vars, a), slices.Index(vars, b)
		if i < j {
			i, j = j, i
		}
		for _, p := range s.Pairs {
			if p.I == i && p.J == j {
				return p
			}
		}
		t.Fatalf("no pair %s, %s", a, b)
		return PairEstimate{}
	}
	ref, _, ss := fitTwoStepFixture(t, "poly_extreme", "poly_extreme")
	v := ref.Options.Vars
	if p := pair(ss[0], v, "a", "c"); p.Rho != PolychoricBound || !p.Bound {
		t.Errorf("a~~c (latent 0.9999) = %v bound %v, want the 0.999 bound", p.Rho, p.Bound)
	}
	if p := pair(ss[0], v, "a", "b"); p.Bound || p.Rho < 0.98 {
		t.Errorf("a~~b (latent 0.995) = %v bound %v, want an interior estimate", p.Rho, p.Bound)
	}
	if p := pair(ss[0], v, "a", "d"); p.Bound || p.Rho > -0.95 {
		t.Errorf("a~~d (latent -0.98) = %v bound %v", p.Rho, p.Bound)
	}
	if p := pair(ss[0], v, "xr", "a"); p.Kind != PairPolyserial || p.Rho != PolyserialBound || !p.Bound {
		t.Errorf("xr~~a (latent 0.9995) = %v %v bound %v, want the polyserial 0.995 bound", p.Kind, p.Rho, p.Bound)
	}
	if p := pair(ss[0], v, "x", "y"); p.Kind != PairPearson {
		t.Errorf("x~~y kind %v", p.Kind)
	}
	ref, _, ss = fitTwoStepFixture(t, "poly_binary", "poly_binary")
	v = ref.Options.Vars
	want := []struct {
		a, b         string
		closed, zero bool
		rho          float64 // NaN: interior, not pinned
	}{
		{"x1", "x2", true, false, -math.Cos(2 * math.Pi * 15 / 40)}, // both thresholds 0
		{"x1", "x4", true, false, 0},                                // odds ratio 1
		{"x1", "x5", true, true, 1},                                 // two empty cells
		{"x1", "x3", false, true, math.NaN()},                       // one empty cell: +0.5 / -0.5
		{"x1", "x6", false, true, math.NaN()},                       // 2 × 3: no continuity correction
	}
	for _, w := range want {
		p := pair(ss[0], v, w.a, w.b)
		if p.Closed != w.closed || p.ZeroCells != w.zero || (!math.IsNaN(w.rho) && math.Abs(p.Rho-w.rho) > 1e-15) {
			t.Errorf("%s~~%s: rho %v closed %v zero %v; want rho %v closed %v zero %v", w.a, w.b, p.Rho, p.Closed, p.ZeroCells, w.rho, w.closed, w.zero)
		}
	}
	_, _, ss = fitTwoStepFixture(t, "poly_sparse", "poly_sparse")
	for _, p := range ss[0].Pairs {
		if !p.ZeroCells {
			t.Errorf("sparse pair %d,%d: expected empty cells", p.I, p.J)
		}
	}
}

// atLavaan returns a copy of s carrying the reference's (single-group)
// estimates.
func atLavaan(s *TwoStepStats, ref twoStepRef) *TwoStepStats {
	c := cloneStats(s)
	g := ref.Groups[0]
	for v, name := range ref.Options.Vars {
		if c.Kinds[v] == KindOrdered {
			copy(c.Thresholds[v], g.Thresholds[name])
		} else {
			c.Mean[v], c.Var[v] = g.Mean[name], g.Var[name]
		}
	}
	c.Pairs = slices.Clone(s.Pairs)
	for k := range c.Pairs {
		c.Pairs[k].Rho = *g.Cor[c.Pairs[k].I][c.Pairs[k].J]
	}
	return c
}

// TestPolychoric_PairScoresAreDerivatives checks every PairScores
// component against a central difference of the pair's casewise
// log-likelihood — the non-polychoric pairs' threshold, mean and
// variance scores included, which lavaan's A21 does not expose.
func TestPolychoric_PairScoresAreDerivatives(t *testing.T) {
	_, in, ss := fitTwoStepFixture(t, "poly_extreme", "poly_extreme")
	s := ss[0]
	const h = 1e-6
	for k, p := range s.Pairs {
		for _, r := range []int{0, 7, 311} {
			row := in.Rows[r]
			got := s.PairScores(k, row)
			ll := func(mut func(c *TwoStepStats), rho float64) float64 {
				c := cloneStats(s)
				mut(c)
				return pairCaseLogLik(c, p, row, rho)
			}
			id := func(*TwoStepStats) {}
			num := (ll(id, p.Rho+h) - ll(id, p.Rho-h)) / (2 * h)
			if math.Abs(num-got.Rho) > 1e-5*math.Max(1, math.Abs(num)) {
				t.Errorf("pair %d row %d: ρ-score %v, numeric %v", k, r, got.Rho, num)
			}
			for side, v := range []int{p.I, p.J} {
				th := got.ThI
				vs := got.VarI
				if side == 1 {
					th, vs = got.ThJ, got.VarJ
				}
				for m := range th {
					up := func(d float64) func(c *TwoStepStats) {
						return func(c *TwoStepStats) {
							if c.Kinds[v] == KindOrdered {
								c.Thresholds[v][m] += d
							} else {
								c.Mean[v] += d
							}
						}
					}
					num := (ll(up(h), p.Rho) - ll(up(-h), p.Rho)) / (2 * h)
					if math.Abs(num-th[m]) > 1e-5*math.Max(1, math.Abs(num)) {
						t.Errorf("pair %d row %d var %d: threshold/mean score %d = %v, numeric %v", k, r, v, m, th[m], num)
					}
				}
				if s.Kinds[v] == KindNumeric {
					up := func(d float64) func(c *TwoStepStats) { return func(c *TwoStepStats) { c.Var[v] += d } }
					num := (ll(up(h), p.Rho) - ll(up(-h), p.Rho)) / (2 * h)
					if math.Abs(num-vs) > 1e-5*math.Max(1, math.Abs(num)) {
						t.Errorf("pair %d row %d var %d: variance score %v, numeric %v", k, r, v, vs, num)
					}
				} else if !math.IsNaN(vs) {
					t.Errorf("pair %d: an ordered column's variance score is %v, want NaN", k, vs)
				}
			}
		}
	}
}

func cloneStats(s *TwoStepStats) *TwoStepStats {
	c := *s
	c.Mean, c.Var = slices.Clone(s.Mean), slices.Clone(s.Var)
	c.Thresholds = make([][]float64, len(s.Thresholds))
	for i, th := range s.Thresholds {
		c.Thresholds[i] = slices.Clone(th)
	}
	return &c
}

// pairCaseLogLik is one row's bivariate log-likelihood, written from
// the definitions (not from the score code): polychoric log π_cell,
// polyserial log φ(y; μ, σ²) + log P(category | y), Pearson log φ₂.
func pairCaseLogLik(s *TwoStepStats, p PairEstimate, row []float64, rho float64) float64 {
	cdf2 := func(a, b float64) float64 {
		return BivariateNormalCDF(a, b, rho)
	}
	lim := func(v int) (float64, float64) {
		th := s.Thresholds[v]
		c := int(row[v])
		up, lo := math.Inf(1), math.Inf(-1)
		if c < len(th) {
			up = th[c]
		}
		if c > 0 {
			lo = th[c-1]
		}
		return up, lo
	}
	switch p.Kind {
	case PairPolychoric:
		u1, l1 := lim(p.I)
		u2, l2 := lim(p.J)
		return math.Log(cdf2(u1, u2) - cdf2(l1, u2) - cdf2(u1, l2) + cdf2(l1, l2))
	case PairPolyserial:
		x, o := p.I, p.J
		if s.Kinds[x] == KindOrdered {
			x, o = o, x
		}
		sd := math.Sqrt(s.Var[x])
		z := (row[x] - s.Mean[x]) / sd
		up, lo := lim(o)
		r := math.Sqrt(1 - rho*rho)
		pr := stdNormCDF((up-rho*z)/r) - stdNormCDF((lo-rho*z)/r)
		return -0.5*z*z - math.Log(sd) + math.Log(pr)
	}
	a, b := (row[p.I]-s.Mean[p.I])/math.Sqrt(s.Var[p.I]), (row[p.J]-s.Mean[p.J])/math.Sqrt(s.Var[p.J])
	return -0.5*math.Log(s.Var[p.I]) - 0.5*math.Log(s.Var[p.J]) - 0.5*math.Log(1-rho*rho) -
		(a*a-2*rho*a*b+b*b)/(2*(1-rho*rho))
}

// TestPolychoric_WeightsAreReplication: a frequency weight w is w copies
// of the row — the weighted fit equals the replicated one within
// summation-order rounding (lavaan's oracle replicates; this ties the
// Go weights to it directly).
func TestPolychoric_WeightsAreReplication(t *testing.T) {
	var ref twoStepRef
	readJSON(t, "testdata/poly_weighted_lavaan.json", &ref)
	in, _, _ := twoStepInput(t, ref, "testdata/poly_weighted_data.csv")
	weighted, err := TwoStep(in)
	if err != nil {
		t.Fatal(err)
	}
	rep := TwoStepInput{Kinds: in.Kinds, Levels: in.Levels}
	for i, row := range in.Rows {
		for range int(in.Weights[i]) {
			rep.Rows = append(rep.Rows, row)
		}
	}
	replicated, err := TwoStep(rep)
	if err != nil {
		t.Fatal(err)
	}
	if weighted.N != replicated.N || weighted.Rows == replicated.Rows {
		t.Fatalf("N %v vs %v, rows %d vs %d", weighted.N, replicated.N, weighted.Rows, replicated.Rows)
	}
	for k := range weighted.Pairs {
		if d := math.Abs(weighted.Pairs[k].Rho - replicated.Pairs[k].Rho); d > 1e-12 {
			t.Errorf("pair %d: weighted %v, replicated %v", k, weighted.Pairs[k].Rho, replicated.Pairs[k].Rho)
		}
	}
	for v := range weighted.Thresholds {
		for k := range weighted.Thresholds[v] {
			if d := math.Abs(weighted.Thresholds[v][k] - replicated.Thresholds[v][k]); d > 1e-14 {
				t.Errorf("var %d threshold %d: %v vs %v", v, k, weighted.Thresholds[v][k], replicated.Thresholds[v][k])
			}
		}
	}
}

// TestPolychoric_Refusals: a constant item, an empty category (pooled
// levels a group lacks), a constant numeric column and malformed input
// are typed OrdinalErrors naming the column, the group and the reason.
func TestPolychoric_Refusals(t *testing.T) {
	ord2 := []OrdinalKind{KindOrdered, KindOrdered}
	rows := [][]float64{{0, 0}, {1, 1}, {2, 1}, {0, 0}, {1, 0}, {2, 1}}
	cases := []struct {
		name   string
		in     TwoStepInput
		group  []int
		reason string
		v, g   int
	}{
		{"single level", TwoStepInput{Kinds: ord2, Levels: []int{3, 1}, Rows: [][]float64{{0, 0}, {1, 0}, {2, 0}}}, nil, ReasonConstant, 1, -1},
		{"one category held", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 1}, {1, 1}, {2, 1}}}, nil, ReasonConstant, 1, -1},
		{"weight on one category", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 0}, {1, 1}, {2, 1}, {0, 0}, {1, 0}, {2, 0}}, Weights: []float64{1, 0, 0, 1, 1, 1}}, nil, ReasonConstant, 1, -1},
		{"empty middle category", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 0}, {2, 1}, {0, 1}}}, nil, ReasonEmptyCategory, 0, -1},
		{"empty top category", TwoStepInput{Kinds: ord2, Levels: []int{4, 2}, Rows: rows}, nil, ReasonEmptyCategory, 0, -1},
		{"constant within a group", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 0}, {1, 1}, {2, 0}, {0, 1}, {1, 1}, {2, 1}}}, []int{0, 0, 0, 1, 1, 1}, ReasonConstant, 1, 1},
		{"pooled level absent in a group", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 0}, {1, 1}, {2, 0}, {0, 1}, {1, 0}, {1, 1}}}, []int{0, 0, 0, 1, 1, 1}, ReasonEmptyCategory, 0, 1},
		{"constant numeric", TwoStepInput{Kinds: []OrdinalKind{KindNumeric, KindOrdered}, Levels: []int{0, 2}, Rows: [][]float64{{3, 0}, {3, 1}, {3, 1}}}, nil, ReasonConstant, 0, -1},
		{"non-integer code", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0.5, 0}, {1, 1}, {2, 1}}}, nil, ReasonInvalidInput, 0, -1},
		{"code out of range", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{3, 0}, {1, 1}, {2, 1}}}, nil, ReasonInvalidInput, 0, -1},
		{"non-finite numeric", TwoStepInput{Kinds: []OrdinalKind{KindNumeric, KindOrdered}, Levels: []int{0, 2}, Rows: [][]float64{{math.NaN(), 0}, {3, 1}}}, nil, ReasonInvalidInput, 0, -1},
		{"negative weight", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: rows, Weights: []float64{1, 1, 1, 1, 1, -1}}, nil, ReasonInvalidInput, -1, -1},
		{"no rows", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}}, nil, ReasonNoData, -1, -1},
		{"zero weight", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: rows, Weights: make([]float64, 6)}, nil, ReasonNoData, -1, -1},
		{"ragged row", TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: [][]float64{{0, 0}, {1}}}, nil, ReasonInvalidInput, -1, -1},
	}
	for _, c := range cases {
		var err error
		if c.group != nil {
			_, err = TwoStepGroups(c.in, c.group, 2)
		} else {
			_, err = TwoStep(c.in)
		}
		var oe *OrdinalError
		if !stderrors.As(err, &oe) {
			t.Errorf("%s: err %v, want an *OrdinalError", c.name, err)
			continue
		}
		if oe.Reason != c.reason || oe.Var != c.v || oe.Group != c.g {
			t.Errorf("%s: reason %q var %d group %d, want %q %d %d (%v)", c.name, oe.Reason, oe.Var, oe.Group, c.reason, c.v, c.g, err)
		}
	}
	if _, err := TwoStepGroups(TwoStepInput{Kinds: ord2, Levels: []int{3, 2}, Rows: rows}, []int{0, 1}, 2); err == nil {
		t.Error("a group index shorter than the rows was accepted")
	}
}

// TestPolychoric_LocalAscentFromStart: a sparse 5 × 5 table (a
// bootstrap replicate of the brand survey's v1 × v3) whose probability
// floors turn the score negative again just inside −0.999 keeps its
// interior maximum — lavaan's 0.687378420067 (lavCor, nlminb from the
// Pearson start) — instead of the lower edge a global edge test picks.
func TestPolychoric_LocalAscentFromStart(t *testing.T) {
	freq := [][]float64{{49, 56, 14, 1, 0}, {59, 166, 114, 18, 0}, {7, 98, 216, 72, 2}, {1, 41, 92, 87, 16}, {0, 0, 23, 31, 37}}
	in := TwoStepInput{Kinds: []OrdinalKind{KindOrdered, KindOrdered}, Levels: []int{5, 5}}
	for a, row := range freq {
		for b, n := range row {
			for i := 0; i < int(n); i++ {
				in.Rows = append(in.Rows, []float64{float64(a), float64(b)})
			}
		}
	}
	s, err := TwoStep(in)
	if err != nil {
		t.Fatal(err)
	}
	pe := s.Pairs[0]
	if math.Abs(pe.Rho-0.687378420067) > 1e-6 || pe.Bound {
		t.Fatalf("polychoric %.12f (bound %v), lavaan 0.687378420067", pe.Rho, pe.Bound)
	}
	tab := newPolyTable(s.Thresholds[0], s.Thresholds[1], freq)
	if g, _ := tab.score(-PolychoricBound); !(g < 0) {
		t.Fatalf("score at the lower edge %v: the table no longer exercises the floor-bent edge", g)
	}
}

// TestPolychoric_TooManyCategories: an ordered column declaring more
// than MaxOrderedCategories categories is refused (ReasonTooManyCategories)
// before any statistic; one at the ceiling fits.
func TestPolychoric_TooManyCategories(t *testing.T) {
	build := func(k int) TwoStepInput {
		in := TwoStepInput{Kinds: []OrdinalKind{KindOrdered, KindOrdered}, Levels: []int{k, 3}}
		for i := 0; i < 20*k; i++ {
			in.Rows = append(in.Rows, []float64{float64(i % k), float64((i / 7) % 3)})
		}
		return in
	}
	_, err := TwoStep(build(MaxOrderedCategories + 1))
	var oe *OrdinalError
	if !stderrors.As(err, &oe) || oe.Reason != ReasonTooManyCategories || oe.Var != 0 {
		t.Fatalf("%d categories: %v, want too_many_categories on column 0", MaxOrderedCategories+1, err)
	}
	if _, err := TwoStep(build(MaxOrderedCategories)); err != nil {
		t.Fatalf("%d categories: %v", MaxOrderedCategories, err)
	}
}
