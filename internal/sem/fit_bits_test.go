package sem

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// The SEM fit bits golden (U78 test-hygiene, TODO #599): every fit,
// statistic, score and bootstrap of fitBitsCorpus is bit-identical on
// every architecture, because the model arithmetic runs on
// internal/gonumorder (matops.go) and the rest of the package is
// FMA-free on linalg/detmath. testdata/fit_bits.golden pins, per
// fixture, the iteration count, the objective and a SHA-256 of every
// figure the fit reports; it is generated on the arm64 dev host and
// CI's linux/amd64 run checks it — the cross-architecture proof:
//
//	go test -count=1 -tags heavy ./internal/sem -run TestSEM_FitBitsGolden -update-fit-bits
//	go test -count=1 -tags heavy ./internal/sem -run TestSEM_FitBitsGolden -v
//
// (-v logs the corpus digest line "SEM fit corpus digest <hex>
// (GOARCH=…)"). Never edit the golden by hand: its last line is a hash
// of the rest, checked here. A pinned figure that moves is a contract
// event (list it, old → new, in the commit and the migration table).
//
// The corpus covers the early-stop fits the decomposition golden could
// not pin before (default SLSQP at ftol 1e-6, Fisher stops on the
// objective-decrease test, information rank2, mean structure,
// multi-group, WLSMV, bootstrap): with no gonum arithmetic on the path
// they stop at the same iterate on both architectures. Its inputs are
// barriered (fitBitsMoments), so they are the same bits everywhere too.

var updateFitBits = flag.Bool("update-fit-bits", false, "rewrite testdata/fit_bits.golden (arm64 dev host only)")

const (
	fitBitsGolden     = "testdata/fit_bits.golden"
	fitBitsHashMarker = "# golden-hash: "
)

// fitBitsMoments are the biased (divisor N) moments of rows, every
// product barriered so the corpus inputs are the same on every
// architecture.
func fitBitsMoments(rows [][]float64) ([]float64, *mat.SymDense, float64) {
	p, n := len(rows[0]), float64(len(rows))
	mean := make([]float64, p)
	for _, r := range rows {
		for c := range r {
			mean[c] += r[c]
		}
	}
	for c := range mean {
		mean[c] /= n
	}
	s := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			acc := 0.0
			for _, r := range rows {
				acc += float64((r[i] - mean[i]) * (r[j] - mean[j]))
			}
			s.SetSym(i, j, acc/n)
		}
	}
	return mean, s, n
}

// fitFigures is one corpus fixture's output.
type fitFigures struct {
	name       string
	iterations float64
	objective  float64
	figures    []float64
}

func (f *fitFigures) add(v ...float64) { f.figures = append(f.figures, v...) }

func (f *fitFigures) estimates(rows []Estimate) {
	for _, e := range rows {
		f.add(e.Value, e.StdValue, e.SE, e.Z, e.P, e.CILower, e.CIUpper)
	}
}

func (f *fitFigures) stats(st Stats) {
	f.add(st.DoF, st.DoFBaseline, st.Chi2, st.Chi2P, st.Chi2Baseline, st.CFI, st.GFI, st.AGFI,
		st.NFI, st.TLI, st.RMSEA, st.AIC, st.BIC, st.LogLik, float64(st.NParams))
	if sc := st.Scaled; sc != nil {
		f.add(sc.Chi2, sc.ScalingFactor, sc.ShiftParameter, sc.Chi2Baseline, sc.BaselineScalingFactor,
			sc.Chi2P, sc.CFI, sc.TLI, sc.RMSEA)
	}
}

func (f *fitFigures) sym(s *mat.SymDense) {
	if s == nil {
		return
	}
	n := s.SymmetricDim()
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			f.add(s.At(i, j))
		}
	}
}

func converged(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

func (f *fitFigures) fit(fit *Fit) {
	f.iterations, f.objective = float64(fit.Iterations), fit.Objective
	f.add(converged(fit.Converged))
	f.estimates(fit.Estimates)
	f.estimates(fit.Defined)
	f.stats(fit.Stats)
	f.sym(fit.Implied)
	f.add(fit.ImpliedMean...)
}

func (f *fitFigures) digest() string {
	h := sha256.New()
	var buf [8]byte
	for _, v := range f.figures {
		bits := math.Float64bits(v)
		if math.IsNaN(v) {
			bits = 0x7ff8000000000001 // a NaN's sign is not part of the contract
		}
		binary.LittleEndian.PutUint64(buf[:], bits)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fitBitsCorpus runs every fixture.
func fitBitsCorpus(t *testing.T) []fitFigures {
	t.Helper()
	var out []fitFigures
	model := func(name string, opt ModelOptions) (*Model, string) {
		desc, err := os.ReadFile("testdata/" + name + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModelWithOptions(string(desc), opt)
		if err != nil {
			t.Fatal(err)
		}
		return m, string(desc)
	}
	rowsOf := func(path string, cols []string) [][]float64 {
		_, rows := readCSVColumns(t, path, cols)
		return rows
	}

	// Covariance-only semopy-convention fits: hz (Holzinger–Swineford)
	// and poldem (Bollen) under every solver / information pair.
	for _, data := range []struct{ model, csv string }{{"hz", "hz"}, {"poldem", "poldem"}} {
		m, desc := model(data.model, ModelOptions{})
		_, s, n := fitBitsMoments(rowsOf("testdata/"+data.csv+"_data.csv", m.Observed))
		for _, c := range []struct {
			name string
			opt  Options
		}{
			{"slsqp", Options{}},
			{"slsqp_rank2", Options{Information: InformationRank2}},
			{"slsqp_tight", Options{Ftol: 1e-14, MaxIter: 100000}},
			{"fisher", Options{Solver: SolverFisher}},
			{"fisher_rank2", Options{Solver: SolverFisher, Information: InformationRank2}},
		} {
			fit, err := FitCov(m, s, n, c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if err := ComputeStats(fit, desc); err != nil {
				t.Fatal(err)
			}
			f := fitFigures{name: data.model + "." + c.name}
			f.fit(fit)
			out = append(out, f)
		}
	}

	// Mean-structure lavaan-convention fits (Fisher by default) with
	// regression and Bartlett scores of every latent at a few rows, and
	// the := defined parameters.
	for _, c := range []struct {
		name, model string
		opt         Options
	}{
		{"brand_ms.fisher", "brand_ms", Options{}},
		{"brand_ms.rank2", "brand_ms", Options{Information: InformationRank2}},
		{"brand_ms.slsqp", "brand_ms", Options{Solver: SolverSLSQP}},
		{"brand_def.fisher", "brand_def", Options{}},
	} {
		m, desc := model(c.model, ModelOptions{MeanStructure: true})
		rows := rowsOf("testdata/brand_data.csv", m.Observed)
		mean, s, n := fitBitsMoments(rows)
		fit, err := FitMoments(m, s, s, mean, n, c.opt)
		if err != nil {
			t.Fatal(err)
		}
		if err := ComputeStats(fit, desc); err != nil {
			t.Fatal(err)
		}
		f := fitFigures{name: c.name}
		f.fit(fit)
		params, ok := fit.Params()
		if !ok {
			t.Fatalf("%s: no parameter matrices", c.name)
		}
		for _, lat := range m.Latents() {
			for _, method := range []ScoreMethod{ScoreRegression, ScoreBartlett} {
				sc, err := NewScorer(m, params, lat, method)
				if err != nil {
					t.Fatalf("%s: scorer %s/%s: %v", c.name, lat, method, err)
				}
				f.add(sc.Weights...)
				for r := 0; r < len(rows); r += 97 {
					f.add(sc.Score(rows[r]))
				}
			}
		}
		out = append(out, f)
	}

	// Multi-group (wave) configural fits, Fisher and SLSQP.
	{
		desc, err := os.ReadFile("testdata/brand_ms_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewGroupModel(string(desc), ModelOptions{})
		if err != nil {
			t.Fatal(err)
		}
		all := rowsOf("testdata/brand_data.csv", append([]string{"wave"}, m.Observed...))
		var labels []string
		for _, r := range all {
			if l := formatLevel(r[0]); !slices.Contains(labels, l) {
				labels = append(labels, l)
			}
		}
		sort.Strings(labels)
		groups := make([]GroupMoments, len(labels))
		for g, lab := range labels {
			var sub [][]float64
			for _, r := range all {
				if formatLevel(r[0]) == lab {
					sub = append(sub, r[1:])
				}
			}
			mean, s, n := fitBitsMoments(sub)
			groups[g] = GroupMoments{Label: lab, S: s, Raw: s, Mean: mean, N: n, Level: g}
		}
		for _, c := range []struct {
			name string
			opt  Options
		}{
			{"brand_mg.fisher", Options{}},
			{"brand_mg.slsqp", Options{Solver: SolverSLSQP}},
		} {
			gf, err := FitGroups(m, groups, c.opt)
			if err != nil {
				t.Fatal(err)
			}
			f := fitFigures{name: c.name, iterations: float64(gf.Iterations), objective: gf.Objective}
			f.add(converged(gf.Converged))
			f.stats(gf.Stats)
			f.add(gf.Chi2Group...)
			for _, g := range gf.Groups {
				f.estimates(g.Estimates)
				f.sym(g.Implied)
				f.add(g.ImpliedMean...)
			}
			out = append(out, f)
		}
	}

	// The ordinal two-step estimators over the nine 1–5 ratings.
	{
		m, _ := model("brand_ord", ModelOptions{Ordered: []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}})
		raw := rowsOf("testdata/brand_data.csv", m.Observed)
		in := TwoStepInput{Kinds: make([]OrdinalKind, len(m.Observed)), Levels: make([]int, len(m.Observed)), Rows: make([][]float64, len(raw))}
		for i := range in.Rows {
			in.Rows[i] = make([]float64, len(m.Observed))
		}
		for v := range m.Observed {
			col := make([]float64, len(raw))
			for i, r := range raw {
				col[i] = r[v]
			}
			levels := slices.Clone(col)
			slices.Sort(levels)
			levels = slices.Compact(levels)
			in.Kinds[v], in.Levels[v] = KindOrdered, len(levels)
			for i, x := range col {
				in.Rows[i][v] = float64(slices.Index(levels, x))
			}
		}
		for _, est := range []Estimator{EstimatorWLSMV, EstimatorDWLS} {
			fit, err := FitOrdinal(m, in, est, Options{})
			if err != nil {
				t.Fatal(err)
			}
			f := fitFigures{name: "brand_ord." + string(est)}
			f.fit(fit)
			f.sym(fit.Ordinal.VCov)
			out = append(out, f)
		}
	}

	// A small bootstrap of the defined-parameter fit (resampled refits).
	{
		m, _ := model("brand_def", ModelOptions{MeanStructure: true})
		mean, s, n := fitBitsMoments(rowsOf("testdata/brand_data.csv", m.Observed))
		fit, err := FitMoments(m, s, s, mean, n, Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := RunBootstrap(m, csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed), Options{},
			BootstrapOptions{Iters: 12, Seed: 78, Level: 0.9}, BootstrapRows(fit))
		if err != nil {
			t.Fatal(err)
		}
		f := fitFigures{name: "brand_def.bootstrap", iterations: float64(b.Succeeded)}
		f.add(b.SE...)
		f.add(b.CILower...)
		f.add(b.CIUpper...)
		out = append(out, f)
	}
	return out
}

func renderFitBits(figs []fitFigures) []byte {
	var b bytes.Buffer
	b.WriteString("# SEM fit bits (internal/sem TestSEM_FitBitsGolden). Regenerate with -update-fit-bits on arm64; never hand-edit.\n")
	b.WriteString("# <fixture> <iterations> <objective bits> <objective> <sha256 of every figure>\n")
	for _, f := range figs {
		fmt.Fprintf(&b, "%s %d %#016x %.17g %s\n", f.name, int(f.iterations), math.Float64bits(f.objective), f.objective, f.digest())
	}
	sum := sha256.Sum256(b.Bytes())
	b.WriteString(fitBitsHashMarker + hex.EncodeToString(sum[:]) + "\n")
	return b.Bytes()
}

// corpusDigest is one digest over the whole golden body: the line the
// test logs, so a CI log shows the architecture's bits at a glance.
func corpusDigest(golden []byte) string {
	sum := sha256.Sum256(golden)
	return hex.EncodeToString(sum[:])
}

func TestSEM_FitBitsGolden(t *testing.T) {
	t.Parallel()

	got := renderFitBits(fitBitsCorpus(t))
	t.Logf("SEM fit corpus digest %s (GOARCH=%s)", corpusDigest(got), runtime.GOARCH)
	if *updateFitBits {
		if err := os.WriteFile(fitBitsGolden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(fitBitsGolden)
	if err != nil {
		t.Fatalf("%v (generate with -update-fit-bits)", err)
	}
	body, stored, ok := strings.Cut(string(want), fitBitsHashMarker)
	if sum := sha256.Sum256([]byte(body)); !ok || strings.TrimSpace(stored) != hex.EncodeToString(sum[:]) {
		t.Fatalf("%s: golden-hash mismatch (hand-edited? regenerate with -update-fit-bits)", fitBitsGolden)
	}
	if bytes.Equal(got, want) {
		return
	}
	wantLines := map[string]string{}
	for _, l := range strings.Split(body, "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		name, rest, _ := strings.Cut(l, " ")
		wantLines[name] = rest
	}
	for _, l := range strings.Split(string(got), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		name, rest, _ := strings.Cut(l, " ")
		w, ok := wantLines[name]
		switch {
		case !ok:
			t.Errorf("%s: not pinned (got %s)", name, rest)
		case w != rest:
			t.Errorf("%s moved (GOARCH=%s):\n  got  %s\n  want %s", name, runtime.GOARCH, rest, w)
		}
		delete(wantLines, name)
	}
	for name := range wantLines {
		t.Errorf("%s: pinned but no longer computed", name)
	}
	if !t.Failed() {
		t.Errorf("golden differs only in its header or order; regenerate with -update-fit-bits")
	}
}
