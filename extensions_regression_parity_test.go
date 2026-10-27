package pulse_test

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Regression parity (U53 E1-S5): REG_PARITY_OLS (streaming) and
// REG_PARITY_OLS_BUF (buffered) are extend reimplementations of REG_OLS —
// weighted least squares on w* (w under a frequency weight,
// w·n_eff/Σw under a probability one), classical standard errors, t
// p-values on N* − q degrees of freedom — so a custom model's figures,
// weighted and unweighted and under a multiplicity block, match the
// built-in's within tolerance.

const (
	regParityOLS    types.RegressionType = "REG_PARITY_OLS"
	regParityOLSBuf types.RegressionType = "REG_PARITY_OLS_BUF"
)

// olsMoments is a weighted co-moment over z = (x_1..x_k, y): Σw, Σw²,
// the weighted means and the centred cross-products, folded by West's
// update (one row) and Chan's merge (two partials).
type olsMoments struct {
	k        int
	sw, sww  float64
	mean     []float64
	m2       [][]float64 // (k+1)×(k+1)
	target   string
	features []string
	weighted bool
}

func newOLSMoments(spec *types.RegressionSpec, weighted bool) *olsMoments {
	k := len(spec.Predictors)
	m := &olsMoments{k: k, mean: make([]float64, k+1), m2: make([][]float64, k+1),
		target: spec.Target, features: slices.Clone(spec.Predictors), weighted: weighted}
	for i := range m.m2 {
		m.m2[i] = make([]float64, k+1)
	}
	return m
}

func (m *olsMoments) add(rec extend.Record) {
	z := make([]float64, m.k+1)
	for i, f := range m.features {
		z[i], _ = rec.NumericValue(f)
	}
	z[m.k], _ = rec.NumericValue(m.target)
	w := 1.0
	if v, ok := rec.Weight(); ok {
		w = v
	}
	m.sw += w
	m.sww += w * w
	d := make([]float64, m.k+1)
	for i := range z {
		d[i] = z[i] - m.mean[i]
		m.mean[i] += d[i] * w / m.sw
	}
	for i := range z {
		for j := range z {
			m.m2[i][j] += w * d[i] * (z[j] - m.mean[j])
		}
	}
}

func (m *olsMoments) merge(o *olsMoments) {
	if o.sw == 0 {
		return
	}
	sw := m.sw + o.sw
	d := make([]float64, m.k+1)
	for i := range d {
		d[i] = o.mean[i] - m.mean[i]
	}
	for i := range d {
		for j := range d {
			m.m2[i][j] += o.m2[i][j] + d[i]*d[j]*m.sw*o.sw/sw
		}
	}
	for i := range m.mean {
		m.mean[i] += d[i] * o.sw / sw
	}
	m.sw, m.sww = sw, m.sww+o.sww
}

// invert inverts a small symmetric positive-definite matrix by
// Gauss–Jordan elimination with partial pivoting.
func invert(a [][]float64) ([][]float64, bool) {
	n := len(a)
	aug := make([][]float64, n)
	for i := range a {
		aug[i] = make([]float64, 2*n)
		copy(aug[i], a[i])
		aug[i][n+i] = 1
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(aug[r][c]) > math.Abs(aug[p][c]) {
				p = r
			}
		}
		if aug[p][c] == 0 {
			return nil, false
		}
		aug[c], aug[p] = aug[p], aug[c]
		piv := aug[c][c]
		for j := range aug[c] {
			aug[c][j] /= piv
		}
		for r := 0; r < n; r++ {
			if r == c || aug[r][c] == 0 {
				continue
			}
			f := aug[r][c]
			for j := range aug[r] {
				aug[r][j] -= f * aug[c][j]
			}
		}
	}
	out := make([][]float64, n)
	for i := range out {
		out[i] = aug[i][n:]
	}
	return out, true
}

// output is the OLS fit off the co-moment, on w*: under a probability
// weight every centred moment and Σw scale by c = n_eff/Σw (β is
// unchanged; the inferential figures read N* = n_eff).
func (m *olsMoments) output(kind types.WeightKind) (extend.RegressionOutput, error) {
	k := m.k
	q := float64(k + 1)
	c := 1.0
	if m.weighted && kind == types.WeightKindProbability {
		c = (m.sw * m.sw / m.sww) / m.sw
	}
	nStar := c * m.sw
	if nStar <= q {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "too few rows")
	}
	sxx := make([][]float64, k)
	for i := range sxx {
		sxx[i] = make([]float64, k)
		for j := range sxx[i] {
			sxx[i][j] = c * m.m2[i][j]
		}
	}
	inv, ok := invert(sxx)
	if !ok {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_RANK_DEFICIENT, "singular design")
	}
	beta := make([]float64, k)
	for i := range beta {
		for j := range beta {
			beta[i] += inv[i][j] * c * m.m2[j][k]
		}
	}
	syy := c * m.m2[k][k]
	explained := 0.0
	for i := range beta {
		explained += beta[i] * c * m.m2[i][k]
	}
	rss := syy - explained
	df := nStar - q
	sigma2 := rss / df
	icpt := m.mean[k]
	for i := range beta {
		icpt -= beta[i] * m.mean[i]
	}
	quad := 0.0
	for i := range beta {
		for j := range beta {
			quad += m.mean[i] * inv[i][j] * m.mean[j]
		}
	}
	coef := func(name string, est, variance float64) extend.RegressionCoefficient {
		se := math.Sqrt(variance)
		t := est / se
		return extend.RegressionCoefficient{Name: name, Estimate: est, StdErr: extend.Figure(se),
			Statistic: extend.Figure(t), PValue: extend.Figure(statdist.StudentTTwoSidedP(t, df))}
	}
	out := extend.RegressionOutput{Coefficients: []extend.RegressionCoefficient{coef("(intercept)", icpt, sigma2*(1/nStar+quad))}}
	for i, f := range m.features {
		out.Coefficients = append(out.Coefficients, coef(f, beta[i], sigma2*inv[i][i]))
	}
	r2 := 1 - rss/syy
	out.Fit = extend.RegressionFit{
		R2:             extend.Figure(r2),
		AdjR2:          extend.Figure(1 - (1-r2)*(nStar-1)/df),
		ResidualStdErr: extend.Figure(math.Sqrt(sigma2)),
		DF:             extend.Figure(df),
	}
	return out, nil
}

type streamOLS struct {
	m    *olsMoments
	kind types.WeightKind
}

func (s *streamOLS) UpdateRow(rec extend.Record) error { s.m.add(rec); return nil }
func (s *streamOLS) Merge(o extend.StreamingRegression) error {
	s.m.merge(o.(*streamOLS).m)
	return nil
}
func (s *streamOLS) Finalize() (extend.RegressionOutput, error) { return s.m.output(s.kind) }

type bufferedOLS struct {
	spec *types.RegressionSpec
	kind types.WeightKind
}

func (b bufferedOLS) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	m := newOLSMoments(b.spec, b.spec.Weight.Spec() != nil)
	for i := 0; i < rows.Len(); i++ {
		m.add(rows.At(i))
	}
	return m.output(b.kind)
}

func olsWeightKind(spec *types.RegressionSpec) types.WeightKind {
	if w := spec.Weight.Spec(); w != nil {
		return w.EffectiveKind()
	}
	return ""
}

// regressionParityRegistrations registers both clones, weight-aware.
func regressionParityRegistrations() []pulse.RegressionRegistration {
	return []pulse.RegressionRegistration{
		{
			Name: regParityOLS, Description: "Test-only streaming extend reimplementation of REG_OLS.",
			Streamable: true, WeightAware: true, PValues: true,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return &streamOLS{m: newOLSMoments(spec, spec.Weight.Spec() != nil), kind: olsWeightKind(spec)}, nil
			},
		},
		{
			Name: regParityOLSBuf, Description: "Test-only buffered extend reimplementation of REG_OLS.",
			WeightAware: true, PValues: true,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
				return bufferedOLS{spec: spec, kind: olsWeightKind(spec)}, nil
			},
		},
	}
}

// regressionParitySuite pairs REG_OLS with its clones. A regression
// never merges (both arms run serially in the parallel modes), and a
// buffered clone's row carries a non-streamable attribute so the
// built-in runs buffered too.
func regressionParitySuite() paritySuite {
	rowConf := func(name string, ext types.RegressionType, predictors []string, weight *types.WeightSpec, mult *types.RegressionMultiplicity, conf float64) parityRow {
		req := func(t types.RegressionType) *types.Request {
			spec := &types.RegressionSpec{Type: t, Name: "fit", Target: "score", Predictors: predictors, Multiplicity: mult, ConfLevel: conf}
			if weight != nil {
				spec.Weight = types.SlotWeightOf(*weight)
			}
			r := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
				Regressions:  []*types.RegressionSpec{spec},
			}
			if ext == regParityOLSBuf {
				r.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "score", Label: "score_pct"}}
			}
			return r
		}
		return parityRow{name: name, builtin: req(types.REG_OLS), extension: req(ext),
			bufferedOnly: ext == regParityOLSBuf, serialOnly: true}
	}
	row := func(name string, ext types.RegressionType, predictors []string, weight *types.WeightSpec, mult *types.RegressionMultiplicity) parityRow {
		return rowConf(name, ext, predictors, weight, mult, 0)
	}
	freq := &types.WeightSpec{Field: "qty", Kind: types.WeightKindFrequency}
	prob := &types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability}
	holm := &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
	two := []string{"qty", "day"}
	one := []string{"day"}
	return paritySuite{
		name:   "regression",
		noPath: true, // a streaming model is fed row by row on the buffered pass too
		rename: map[string]string{string(regParityOLS): string(types.REG_OLS), string(regParityOLSBuf): string(types.REG_OLS)},
		register: func(*parityProbe) pulse.Extensions {
			return pulse.Extensions{Regressions: regressionParityRegistrations()}
		},
		rows: []parityRow{
			row("streaming", regParityOLS, two, nil, nil),
			row("streaming_holm", regParityOLS, two, nil, holm),
			row("streaming_frequency", regParityOLS, one, freq, nil),
			row("streaming_probability_holm", regParityOLS, one, prob, holm),
			row("buffered", regParityOLSBuf, two, nil, holm),
			row("buffered_frequency", regParityOLSBuf, one, freq, nil),
			row("buffered_probability", regParityOLSBuf, one, prob, holm),
			// conf_level (owner decision #3): the engine's t interval on
			// the clone's reported residual df is REG_OLS's.
			rowConf("streaming_conf90", regParityOLS, two, nil, nil, 0.9),
			rowConf("streaming_frequency_conf80", regParityOLS, one, freq, nil, 0.8),
			rowConf("buffered_probability_conf99", regParityOLSBuf, one, prob, holm, 0.99),
			rowConf("buffered_conf90", regParityOLSBuf, two, nil, nil, 0.9),
		},
	}
}

// regressionParityTol is the relative tolerance between REG_OLS and its
// clone: the two compute the same figures by different arithmetic
// (REG_OLS's own accumulator and solver; the clone's co-moment and
// Gauss–Jordan inverse).
const regressionParityTol = 1e-9

func closeRel(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= regressionParityTol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// assertRegressionParity holds every figure REG_OLS reports to the
// clone's within regressionParityTol: n_obs, the weighted floor, the
// coefficient table (estimate, standard error, p-value, the confidence
// interval at conf_level), the fit statistics and the
// multiplicity-adjusted p-values with their echo.
// The clone may report more (statistics), never less.
func assertRegressionParity(t *testing.T, builtin, ext []*types.RegressionResult) {
	t.Helper()
	if len(builtin) != len(ext) {
		t.Fatalf("regressions: builtin %d results, extension %d", len(builtin), len(ext))
	}
	for i := range builtin {
		b, e := builtin[i], ext[i]
		if b == nil || e == nil || b.Error != nil || e.Error != nil {
			t.Fatalf("regressions[%d]: builtin %+v extension %+v", i, b, e)
		}
		if b.Name != e.Name || b.NObs != e.NObs || b.NObs == 0 {
			t.Errorf("regressions[%d] identity: builtin (%s, n=%d) extension (%s, n=%d)", i, b.Name, b.NObs, e.Name, e.NObs)
		}
		for _, f := range []struct {
			name string
			b, e float64
		}{
			{"r2", b.R2, e.R2}, {"adj_r2", b.AdjR2, e.AdjR2}, {"residual_std_err", b.ResidualStdErr, e.ResidualStdErr},
			{"df", b.DF, e.DF}, {"sum_weights", b.SumWeights, e.SumWeights}, {"n_eff", b.NEff, e.NEff},
			{"conf_level", b.ConfLevel, e.ConfLevel},
		} {
			if !closeRel(f.b, f.e) {
				t.Errorf("regressions[%d].%s: builtin %v extension %v", i, f.name, f.b, f.e)
			}
		}
		for _, m := range []struct {
			name string
			b, e map[string]float64
		}{
			{"coefficients", b.Coefficients, e.Coefficients}, {"std_errors", b.StdErrors, e.StdErrors},
			{"p_values", b.PValues, e.PValues}, {"p_adjusted", b.PAdjusted, e.PAdjusted},
			{"ci_lower", b.CILower, e.CILower}, {"ci_upper", b.CIUpper, e.CIUpper},
		} {
			keys := make([]string, 0, len(m.b))
			for k := range m.b {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(m.b) != len(m.e) && m.name != "p_values" && m.name != "std_errors" {
				t.Errorf("regressions[%d].%s keys: builtin %v extension %v", i, m.name, m.b, m.e)
			}
			for _, k := range keys {
				got, ok := m.e[k]
				if !ok || !closeRel(m.b[k], got) {
					t.Errorf("regressions[%d].%s[%s]: builtin %v extension %v (present %v)", i, m.name, k, m.b[k], got, ok)
				}
			}
		}
		if len(b.SignificantAdjusted) != len(e.SignificantAdjusted) {
			t.Errorf("regressions[%d].significant_adjusted: builtin %v extension %v", i, b.SignificantAdjusted, e.SignificantAdjusted)
		}
		for k, v := range b.SignificantAdjusted {
			if e.SignificantAdjusted[k] != v {
				t.Errorf("regressions[%d].significant_adjusted[%s]: builtin %v extension %v", i, k, v, e.SignificantAdjusted[k])
			}
		}
		if (b.Multiplicity == nil) != (e.Multiplicity == nil) ||
			b.Multiplicity != nil && fmt.Sprint(*b.Multiplicity) != fmt.Sprint(*e.Multiplicity) {
			t.Errorf("regressions[%d].multiplicity: builtin %+v extension %+v", i, b.Multiplicity, e.Multiplicity)
		}
	}
}

// TestExtensions_RegressionWorkerInvariant: a custom regression's result
// is byte-identical across DecodeWorkers (a cohort past the parallel
// decode threshold) and across ShardWorkers (a shard archive) — a
// regression never merges, so every worker count folds the admitted
// rows in record order — streaming and buffered, weighted and not.
func TestExtensions_RegressionWorkerInvariant(t *testing.T) {
	t.Parallel()

	large := parityLargeCohort(t.TempDir())(t)
	prob := types.SlotWeightOf(types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability})
	specs := []*types.RegressionSpec{
		{Type: regParityOLS, Name: "s", Target: "score", Predictors: []string{"qty", "day"}},
		{Type: regParityOLS, Name: "sw", Target: "score", Predictors: []string{"day"}, Weight: prob},
		{Type: regParityOLSBuf, Name: "b", Target: "score", Predictors: []string{"qty", "day"}},
		{Type: regParityOLSBuf, Name: "bw", Target: "score", Predictors: []string{"day"}, Weight: prob,
			Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}},
	}
	ext := pulse.Extensions{Regressions: regressionParityRegistrations()}
	run := func(t *testing.T, opts pulse.Options, path string) string {
		t.Helper()
		opts.Extensions = ext
		p, err := pulse.New(opts)
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		resp := processE2E(t, p, &types.Request{Cohort: &types.Cohort{Filename: path}, Regressions: specs})
		for _, r := range resp.Regressions {
			if r == nil || r.Error != nil || r.NObs == 0 {
				t.Fatalf("regression failed: %+v", r)
			}
		}
		return mustJSON(t, resp.Regressions)
	}
	t.Run("decode_workers", func(t *testing.T) {
		want := run(t, pulse.Options{DataDir: large, DecodeWorkers: 1}, "large.pulse")
		for _, w := range []int{2, 4, 7} {
			if got := run(t, pulse.Options{DataDir: large, DecodeWorkers: w}, "large.pulse"); got != want {
				t.Errorf("DecodeWorkers %d differs from 1:\n%s\n%s", w, got, want)
			}
		}
	})
	t.Run("shard_workers", func(t *testing.T) {
		fsys := afero.NewMemMapFs()
		s := paritySchema(t)
		shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
		for i, path := range shards {
			writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
		}
		archive, err := pulse.New(pulse.Options{FS: fsys})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := archive.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}
		want := run(t, pulse.Options{FS: fsys, ShardWorkers: 1}, "archive.pulse")
		for _, w := range []int{2, 3} {
			if got := run(t, pulse.Options{FS: fsys, ShardWorkers: w}, "archive.pulse"); got != want {
				t.Errorf("ShardWorkers %d differs from 1:\n%s\n%s", w, got, want)
			}
		}
	})
}
