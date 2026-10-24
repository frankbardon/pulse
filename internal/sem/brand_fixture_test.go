package sem

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
)

// The brand-survey fixture is the lavaan oracles' data: a generic,
// synthetic two-wave survey whose true model is testdata/brand_spec.json.
// brandSurvey generates it from the spec and its seed alone, so the
// committed CSV is reproducible byte for byte; the lavaan references
// (testdata/fit_lavaan.R, run by testdata/gen_lavaan.sh) are fitted to
// that CSV.

var regenBrand = flag.Bool("regen-brand", false, "rewrite testdata/brand_data.csv from testdata/brand_spec.json")

type brandSpec struct {
	N         int    `json:"n"`
	Seed      uint64 `json:"seed"`
	WaveField string `json:"wave_field"`
	Factors   []struct {
		Name       string    `json:"name"`
		Items      []string  `json:"items"`
		Loadings   []float64 `json:"loadings"`
		Wave2Shift float64   `json:"wave2_shift"`
	} `json:"factors"`
	FactorCorrelation [][]float64 `json:"factor_correlation"`
	Item              struct {
		Intercept float64 `json:"intercept"`
		NoiseSD   float64 `json:"noise_sd"`
		Min       float64 `json:"min"`
		Max       float64 `json:"max"`
	} `json:"item"`
	Outcome struct {
		Name      string    `json:"name"`
		Intercept float64   `json:"intercept"`
		Weights   []float64 `json:"weights"`
		NoiseSD   float64   `json:"noise_sd"`
		Min       float64   `json:"min"`
		Max       float64   `json:"max"`
	} `json:"outcome"`
}

func loadBrandSpec(t *testing.T) brandSpec {
	t.Helper()
	b, err := os.ReadFile("testdata/brand_spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var s brandSpec
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// brandSurvey renders the spec's rows as CSV. Every product is written
// float64(a*b) so no architecture fuses it into an FMA: the draws and
// therefore the rounded ratings are the same bits everywhere.
func brandSurvey(s brandSpec) []byte {
	k := len(s.Factors)
	// Lower Cholesky factor of the factor correlation (FMA-free).
	l := make([][]float64, k)
	for i := range l {
		l[i] = make([]float64, k)
		for j := 0; j <= i; j++ {
			acc := s.FactorCorrelation[i][j]
			for t := 0; t < j; t++ {
				acc -= float64(l[i][t] * l[j][t])
			}
			if i == j {
				l[i][j] = math.Sqrt(acc)
			} else {
				l[i][j] = acc / l[j][j]
			}
		}
	}
	clamp := func(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, math.Round(v))) }
	rng := rand.New(rand.NewPCG(s.Seed, s.Seed^0x9e3779b97f4a7c15))
	var buf bytes.Buffer
	buf.WriteString(s.WaveField)
	for _, f := range s.Factors {
		for _, it := range f.Items {
			buf.WriteString("," + it)
		}
	}
	buf.WriteString("," + s.Outcome.Name + "\n")
	z := make([]float64, k)
	f := make([]float64, k)
	for r := 0; r < s.N; r++ {
		wave := 1
		if r >= s.N/2 {
			wave = 2
		}
		for i := range z {
			z[i] = rng.NormFloat64()
		}
		for i := range f {
			acc := 0.0
			for j := 0; j <= i; j++ {
				acc += float64(l[i][j] * z[j])
			}
			if wave == 2 {
				acc += s.Factors[i].Wave2Shift
			}
			f[i] = acc
		}
		buf.WriteString(strconv.Itoa(wave))
		for i, fac := range s.Factors {
			for j := range fac.Items {
				v := s.Item.Intercept + float64(fac.Loadings[j]*f[i]) + float64(s.Item.NoiseSD*rng.NormFloat64())
				buf.WriteString("," + strconv.FormatFloat(clamp(v, s.Item.Min, s.Item.Max), 'f', -1, 64))
			}
		}
		v := s.Outcome.Intercept
		for i, w := range s.Outcome.Weights {
			v += float64(w * f[i])
		}
		v += float64(s.Outcome.NoiseSD * rng.NormFloat64())
		buf.WriteString("," + strconv.FormatFloat(clamp(v, s.Outcome.Min, s.Outcome.Max), 'f', -1, 64) + "\n")
	}
	return buf.Bytes()
}

// TestSEM_BrandSurveyRegenerates holds the committed brand-survey CSV
// to its spec and seed: regenerated, it is the same bytes.
func TestSEM_BrandSurveyRegenerates(t *testing.T) {
	got := brandSurvey(loadBrandSpec(t))
	if *regenBrand {
		if err := os.WriteFile("testdata/brand_data.csv", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/brand_data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/brand_data.csv is not what testdata/brand_spec.json and its seed generate; regenerate with -regen-brand (and refit the lavaan references, testdata/gen_lavaan.sh)")
	}
}

// TestSEM_BrandSurveyShape sanity-checks the generated data against
// the spec: ranges, the wave split and the sign of the wave-2 shifts.
func TestSEM_BrandSurveyShape(t *testing.T) {
	s := loadBrandSpec(t)
	cols := []string{"wave", "q1", "v1", "s1", "recommend"}
	_, rows := readCSVColumns(t, "testdata/brand_data.csv", cols)
	if len(rows) != s.N {
		t.Fatalf("rows = %d, want %d", len(rows), s.N)
	}
	var mean [3][2]float64
	var cnt [2]float64
	for _, r := range rows {
		w := int(r[0]) - 1
		cnt[w]++
		for i := 0; i < 3; i++ {
			if r[1+i] < s.Item.Min || r[1+i] > s.Item.Max {
				t.Fatalf("rating %v out of range", r[1+i])
			}
			mean[i][w] += r[1+i]
		}
		if r[4] < s.Outcome.Min || r[4] > s.Outcome.Max {
			t.Fatalf("recommend %v out of range", r[4])
		}
	}
	if cnt[0] != cnt[1] {
		t.Fatalf("waves %v, want an even split", cnt)
	}
	d := func(i int) float64 { return mean[i][1]/cnt[1] - mean[i][0]/cnt[0] }
	if math.Abs(d(0)) > 0.15 || d(1) > -0.1 || d(2) < 0.3 {
		t.Fatalf("marker wave-2 shifts quality %.3f value %.3f service %.3f do not follow the spec", d(0), d(1), d(2))
	}
}

// readCSVColumns returns the named columns of a CSV, row by row.
func readCSVColumns(t *testing.T, path string, cols []string) ([]string, [][]float64) {
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
	idx := map[string]int{}
	for i, h := range recs[0] {
		idx[h] = i
	}
	out := make([][]float64, len(recs)-1)
	for r := range out {
		out[r] = make([]float64, len(cols))
		for c, name := range cols {
			i, ok := idx[name]
			if !ok {
				t.Fatalf("%s has no column %q", path, name)
			}
			v, err := strconv.ParseFloat(recs[r+1][i], 64)
			if err != nil {
				t.Fatal(err)
			}
			out[r][c] = v
		}
	}
	return cols, out
}
