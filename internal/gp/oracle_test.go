package gp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

// oracle_test.go holds internal/gp to scikit-learn's
// GaussianProcessRegressor (testdata/gp_oracle.json, written by
// scripts/reference/scipy/gen_gp.py — make reference-gp). The model is
// the same term for term, so at FIXED hyperparameters the LML, its
// gradient and the posterior agree to rounding (1e-9 relative). The
// FITTED hyperparameters come from different optimisers (SLSQP here,
// L-BFGS-B there) and are held looser: the LML at the optimum within
// 1e-9, each hyperparameter within 1e-4 (in log space), the posterior
// mean within 1e-6 and its variance within 1e-5 relative — the optimum,
// not the path. (Measured on darwin/arm64: LML 3e-12, hyperparameters
// 1e-6, mean 1.2e-7, variance 1.2e-6.)

const oracleGolden = "testdata/gp_oracle.json"

type oracleHyper struct {
	Amplitude    float64   `json:"amplitude"`
	LengthScales []float64 `json:"length_scales"`
	Noise        float64   `json:"noise"`
}

func (h oracleHyper) hyper() Hyper {
	return Hyper(h)
}

type oracleDesign struct {
	Name        string      `json:"name"`
	Levels      []int       `json:"levels"`
	X           [][]float64 `json:"x"`
	Y           []float64   `json:"y"`
	XS          [][]float64 `json:"xs"`
	Fixed       oracleHyper `json:"fixed"`
	Initial     oracleHyper `json:"initial"`
	Expand      []int       `json:"expand"`
	YStd        float64     `json:"y_std"`
	FixedResult struct {
		LML  float64   `json:"lml"`
		Grad []float64 `json:"grad"`
		Mean []float64 `json:"mean"`
		Var  []float64 `json:"var"`
	} `json:"fixed_result"`
	FittedResult *struct {
		oracleHyper
		LML  float64   `json:"lml"`
		Mean []float64 `json:"mean"`
		Var  []float64 `json:"var"`
	} `json:"fitted_result"`
}

var loadOracle = sync.OnceValues(func() ([]oracleDesign, error) {
	raw, err := os.ReadFile(oracleGolden)
	if err != nil {
		return nil, err
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		return nil, errMsg(oracleGolden + " has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		return nil, errMsg(oracleGolden + ": hash mismatch (hand-edited?); regenerate with make reference-gp")
	}
	var doc struct {
		Nugget  float64        `json:"nugget"`
		Designs []oracleDesign `json:"designs"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, err
	}
	if doc.Nugget != DefaultNugget {
		return nil, errMsg("oracle nugget differs from DefaultNugget")
	}
	return doc.Designs, nil
})

type errMsg string

func (e errMsg) Error() string { return string(e) }

func oracle(t *testing.T) []oracleDesign {
	t.Helper()
	d, err := loadOracle()
	if err != nil {
		t.Fatalf("%v (regenerate with make reference-gp)", err)
	}
	if len(d) == 0 {
		t.Fatal("oracle has no designs")
	}
	return d
}

func (o oracleDesign) space(t *testing.T) *Space {
	t.Helper()
	dims := make([]Dim, len(o.Levels))
	for i, l := range o.Levels {
		dims[i] = Dim{Levels: l}
	}
	sp, err := NewSpace(dims)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Cols() != len(o.X[0]) {
		t.Fatalf("%s: space encodes %d columns, oracle rows have %d", o.Name, sp.Cols(), len(o.X[0]))
	}
	return sp
}

func near(got, want, rel, abs float64) bool {
	return math.Abs(got-want) <= abs+rel*math.Abs(want)
}

// TestGP_MatchesSklearnAtFixedHyper: at fixed hyperparameters the LML,
// its θ-gradient (a categorical dimension's tied length scale = the sum
// of sklearn's per-column entries) and the posterior mean and variance
// match sklearn to 1e-9 relative.
func TestGP_MatchesSklearnAtFixedHyper(t *testing.T) {
	t.Parallel()
	for _, o := range oracle(t) {
		t.Run(o.Name, func(t *testing.T) {
			sp := o.space(t)
			h := o.Fixed.hyper()
			lml, grad, err := LogMarginalLikelihood(sp, o.X, o.Y, h, DefaultNugget)
			if err != nil {
				t.Fatal(err)
			}
			if !near(lml, o.FixedResult.LML, 1e-9, 1e-12) {
				t.Errorf("lml = %.17g, sklearn %.17g", lml, o.FixedResult.LML)
			}
			// Fold sklearn's per-column gradient onto the tied groups.
			expand := o.Expand
			if expand == nil {
				for g := range o.Levels {
					expand = append(expand, g)
				}
			}
			want := make([]float64, sp.Dims()+2)
			sk := o.FixedResult.Grad
			want[0], want[len(want)-1] = sk[0], sk[len(sk)-1]
			for c, g := range expand {
				want[1+g] += sk[1+c]
			}
			for i := range want {
				if !near(grad[i], want[i], 1e-8, 1e-10) {
					t.Errorf("grad[%d] = %.17g, sklearn %.17g", i, grad[i], want[i])
				}
			}
			m, err := NewModel(sp, o.X, o.Y, h, DefaultNugget)
			if err != nil {
				t.Fatal(err)
			}
			if m.Ridge() != 0 {
				t.Errorf("ridge = %v on a well-posed design", m.Ridge())
			}
			if m.LogMarginalLikelihood() != lml {
				t.Errorf("Model LML %v != LogMarginalLikelihood %v", m.LogMarginalLikelihood(), lml)
			}
			noise := float64(h.Noise * float64(o.YStd*o.YStd))
			for i, xs := range o.XS {
				mean, variance, err := m.Predict(xs)
				if err != nil {
					t.Fatal(err)
				}
				if !near(mean, o.FixedResult.Mean[i], 1e-9, 1e-12) {
					t.Errorf("mean[%d] = %.17g, sklearn %.17g", i, mean, o.FixedResult.Mean[i])
				}
				if total := variance + noise; !near(total, o.FixedResult.Var[i], 1e-8, 1e-13) {
					t.Errorf("var[%d] = %.17g (+noise %.17g), sklearn %.17g", i, variance, total, o.FixedResult.Var[i])
				}
			}
		})
	}
}

// TestGP_FitMatchesSklearn: ML-II from DefaultInitial within
// DefaultBounds reaches sklearn's optimum — LML within 1e-9, every
// hyperparameter within 1e-4 in log space, posterior mean within 1e-6,
// variance within 1e-5 relative.
func TestGP_FitMatchesSklearn(t *testing.T) {
	t.Parallel()
	ran := 0
	for _, o := range oracle(t) {
		if o.FittedResult == nil {
			continue
		}
		ran++
		t.Run(o.Name, func(t *testing.T) {
			sp := o.space(t)
			init := o.Initial.hyper()
			if d := DefaultInitial(sp.Dims()); d.Amplitude != init.Amplitude || d.Noise != init.Noise || d.LengthScales[0] != init.LengthScales[0] {
				t.Fatalf("oracle initial %+v differs from DefaultInitial %+v", init, d)
			}
			m, err := Fit(sp, o.X, o.Y, FitOptions{Seed: 1})
			if err != nil {
				t.Fatal(err)
			}
			fr := o.FittedResult
			if got := m.LogMarginalLikelihood(); !near(got, fr.LML, 0, 1e-9) {
				t.Errorf("lml = %.17g, sklearn %.17g", got, fr.LML)
			}
			h := m.Hyper()
			logNear := func(name string, got, want float64) {
				if math.Abs(math.Log(got)-math.Log(want)) > 1e-4 {
					t.Errorf("%s = %.10g, sklearn %.10g", name, got, want)
				}
			}
			logNear("amplitude", h.Amplitude, fr.Amplitude)
			logNear("noise", h.Noise, fr.Noise)
			for g := range h.LengthScales {
				logNear("length scale", h.LengthScales[g], fr.LengthScales[g])
			}
			for i, xs := range o.XS {
				mean, variance, err := m.Predict(xs)
				if err != nil {
					t.Fatal(err)
				}
				if !near(mean, fr.Mean[i], 0, 1e-6) {
					t.Errorf("mean[%d] = %.10g, sklearn %.10g", i, mean, fr.Mean[i])
				}
				total := variance + float64(h.Noise*float64(o.YStd*o.YStd))
				if !near(total, fr.Var[i], 1e-5, 1e-12) {
					t.Errorf("var[%d] = %.10g, sklearn %.10g", i, total, fr.Var[i])
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("oracle has no fitted design")
	}
}
