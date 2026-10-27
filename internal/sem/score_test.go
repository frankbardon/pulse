package sem

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
)

// lavaanScoreRef is a predict_lavaan.R reference: lavPredict's
// regression and Bartlett scores of every stride-th row (0-based data
// row indices), per group, and each group's column means over every
// row.
type lavaanScoreRef struct {
	Lavaan    string   `json:"lavaan"`
	Converged bool     `json:"converged"`
	Latents   []string `json:"latents"`
	Options   struct {
		Group      string   `json:"group"`
		GroupEqual []string `json:"group_equal"`
	} `json:"options"`
	Groups []struct {
		Label          string      `json:"label"`
		N              int         `json:"n"`
		Rows           []int       `json:"rows"`
		Regression     [][]float64 `json:"regression"`
		Bartlett       [][]float64 `json:"bartlett"`
		RegressionMean []float64   `json:"regression_mean"`
		BartlettMean   []float64   `json:"bartlett_mean"`
	} `json:"groups"`
}

func loadLavaanScoreRef(t *testing.T, path string) lavaanScoreRef {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r lavaanScoreRef
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Lavaan == "" || !r.Converged || len(r.Groups) == 0 {
		t.Fatalf("%s: no lavaan release recorded, lavaan did not converge, or no scores", path)
	}
	return r
}

// scoreTol holds a score to lavaan's: absolute on the score's scale
// (a score near 0 has no relative scale) — the fits agree to ~1e-6
// relative (lavaanEstTol bounds them at 1e-4), and a score is a
// weighted sum of O(1) deviations.
const scoreTol = 1e-4

// scoreCases are the lavaan score references: a single-group mean
// structure, the same model under scalar invariance across the waves,
// an observed covariate with a free latent mean (lavaan's phantom
// latents), an indicator with an outgoing arrow (an endogenous observed
// inner variable) and a second-order factor.
var scoreCases = []struct{ name, model string }{
	{"brand_score_ms", "brand_ms"},
	{"brand_score_mg", "brand_ms"},
	{"brand_score_mx", "brand_mx"},
	{"brand_score_xind", "brand_xind"},
	{"brand_score_ho", "brand_ho"},
}

// TestSEMScore_MatchesLavaanPredict holds both methods' scores of every
// recorded row, and each group's mean score over all its rows, to
// lavaan's lavPredict.
func TestSEMScore_MatchesLavaanPredict(t *testing.T) {
	t.Parallel()

	for _, c := range scoreCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			ref := loadLavaanScoreRef(t, "testdata/"+c.name+"_lavaan.json")
			desc, err := os.ReadFile("testdata/" + c.model + "_model.txt")
			if err != nil {
				t.Fatal(err)
			}
			fits, models, rows := fitForScores(t, string(desc), ref)
			for g, gr := range ref.Groups {
				params, ok := fits[g].Params()
				if !ok {
					t.Fatal("the fit carries no parameter matrices")
				}
				for _, method := range []ScoreMethod{ScoreRegression, ScoreBartlett} {
					want, wantMean := gr.Regression, gr.RegressionMean
					if method == ScoreBartlett {
						want, wantMean = gr.Bartlett, gr.BartlettMean
					}
					for l, lat := range ref.Latents {
						sc, err := NewScorer(models[g], params, lat, method)
						if err != nil {
							t.Fatalf("%s %s: %v", lat, method, err)
						}
						for k, r := range gr.Rows {
							got := sc.Score(rows[r])
							if d := math.Abs(got - want[k][l]); !(d <= scoreTol) {
								t.Errorf("group %q row %d %s %s = %.10g, lavaan %.10g (diff %.2g)", gr.Label, r, lat, method, got, want[k][l], d)
							}
						}
						sum, n := 0.0, 0
						for r := range rows {
							if groupOf(ref, r) == g {
								sum += sc.Score(rows[r])
								n++
							}
						}
						if n != gr.N {
							t.Fatalf("group %q has %d rows, lavaan %d", gr.Label, n, gr.N)
						}
						if d := math.Abs(sum/float64(n) - wantMean[l]); !(d <= scoreTol) {
							t.Errorf("group %q %s %s mean = %.10g, lavaan %.10g", gr.Label, lat, method, sum/float64(n), wantMean[l])
						}
					}
				}
			}
		})
	}
}

// scoreGroups maps each data row to its group index (0: single group).
// Parallel tests read and fill it, so scoreGroupsMu guards it.
var (
	scoreGroupsMu sync.Mutex
	scoreGroups   = map[string][]int{}
)

func groupOf(ref lavaanScoreRef, r int) int {
	if ref.Options.Group == "" {
		return 0
	}
	scoreGroupsMu.Lock()
	defer scoreGroupsMu.Unlock()
	return scoreGroups[ref.Options.Group][r]
}

// fitForScores fits ref's model to brand_data.csv as lavaan did — one
// group, or the joint multi-group fit by ref's group column — and
// returns each group's fit and model and every data row over the
// model's Observed order.
func fitForScores(t *testing.T, desc string, ref lavaanScoreRef) ([]*Fit, []*Model, [][]float64) {
	t.Helper()
	const data = "testdata/brand_data.csv"
	if ref.Options.Group == "" {
		m, err := NewModelWithOptions(desc, ModelOptions{MeanStructure: true})
		if err != nil {
			t.Fatal(err)
		}
		mean, s, n := readCSVMoments(t, data, m.Observed)
		fit, err := FitMoments(m, s, s, mean, n, Options{})
		if err != nil {
			t.Fatal(err)
		}
		_, rows := readCSVColumns(t, data, m.Observed)
		return []*Fit{fit}, []*Model{m}, rows
	}
	m, err := NewGroupModel(desc, ModelOptions{GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	labels := make([]string, len(ref.Groups))
	for g, gr := range ref.Groups {
		labels[g] = gr.Label
	}
	groups := readCSVGroupMoments(t, data, ref.Options.Group, labels, m.Observed)
	gf, err := FitGroups(m, groups, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, all := readCSVColumns(t, data, append([]string{ref.Options.Group}, m.Observed...))
	idx := make([]int, len(all))
	rows := make([][]float64, len(all))
	for r, row := range all {
		idx[r] = -1
		for g, l := range labels {
			if formatLevel(row[0]) == l {
				idx[r] = g
			}
		}
		rows[r] = row[1:]
	}
	scoreGroupsMu.Lock()
	scoreGroups[ref.Options.Group] = idx
	scoreGroupsMu.Unlock()
	models := make([]*Model, len(gf.Groups))
	for g, f := range gf.Groups {
		models[g] = f.Model
	}
	return gf.Groups, models, rows
}

// TestSEMScore_CovarianceOnlyCenteredAtFitRows pins the convention
// without a mean structure: the scores are the mean-structure fit's
// less nothing but the centering — centered at the fit rows (mean 0),
// the weights the same (a single-group mean structure estimates the
// means saturated, so the covariance part is one fit).
func TestSEMScore_CovarianceOnlyCenteredAtFitRows(t *testing.T) {
	t.Parallel()

	desc, err := os.ReadFile("testdata/brand_ms_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	ref := loadLavaanScoreRef(t, "testdata/brand_score_ms_lavaan.json")
	m, err := NewModelWithOptions(string(desc), ModelOptions{Lavaan: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	params, ok := fit.Params()
	if !ok || params.SampleMean != nil {
		t.Fatalf("a covariance-only fit's params carry no sample mean (got ok %v, %v)", ok, params.SampleMean)
	}
	if _, err := NewScorer(m, params, "value", ScoreRegression); err == nil {
		t.Fatal("a scorer without the fit rows' mean must be refused")
	}
	params.SampleMean = mean
	_, rows := readCSVColumns(t, "testdata/brand_data.csv", m.Observed)
	gr := ref.Groups[0]
	for l, lat := range ref.Latents {
		for _, method := range []ScoreMethod{ScoreRegression, ScoreBartlett} {
			sc, err := NewScorer(m, params, lat, method)
			if err != nil {
				t.Fatal(err)
			}
			if sc.Base != 0 {
				t.Errorf("%s %s base = %g, want 0 (no latent mean)", lat, method, sc.Base)
			}
			want := gr.Regression
			if method == ScoreBartlett {
				want = gr.Bartlett
			}
			for k, r := range gr.Rows {
				if d := math.Abs(sc.Score(rows[r]) - want[k][l]); !(d <= scoreTol) {
					t.Errorf("row %d %s %s = %.10g, lavaan (mean structure) %.10g", r, lat, method, sc.Score(rows[r]), want[k][l])
				}
			}
		}
	}
}

// TestSEMScore_Refusals pins the scorer's refusals: an observed
// variable or unknown name is no latent, a singular implied covariance
// has no scores.
func TestSEMScore_Refusals(t *testing.T) {
	t.Parallel()

	desc, err := os.ReadFile("testdata/brand_ms_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	params, _ := fit.Params()
	for _, name := range []string{"q1", "recommend", "nope"} {
		if _, err := NewScorer(m, params, name, ScoreRegression); err == nil || !strings.Contains(err.Error(), "not a latent") {
			t.Errorf("%s: err %v, want a not-a-latent refusal", name, err)
		}
	}
	if _, err := NewScorer(m, params, "value", "ml"); err == nil {
		t.Error("an unknown method must be refused")
	}
	bad := copyParams(&params)
	for i := range bad.Theta {
		for j := range bad.Theta[i] {
			bad.Theta[i][j] = 0
		}
	}
	for i := range bad.Psi {
		for j := range bad.Psi[i] {
			bad.Psi[i][j] = 0
		}
	}
	if _, err := NewScorer(m, bad, "value", ScoreRegression); err != ErrScoreSingular {
		t.Errorf("a zero implied covariance: err %v, want ErrScoreSingular", err)
	}
}
