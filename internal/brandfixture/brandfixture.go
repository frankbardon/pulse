// Package brandfixture is the single generator of the generic synthetic
// two-wave brand survey: three correlated factors (quality, value,
// service) measured by three 1-5 ratings each, a 0-10 recommend
// outcome, and a second wave whose factor means shift by the spec's
// wave2_shift. The spec (internal/sem/testdata/brand_spec.json) and its
// seed fix every row: the lavaan oracles' data (OracleCSV,
// internal/sem/testdata/brand_data.csv) and the examples' brand_survey
// cohort (internal/examples/fixtures/gen.go) are the same draws.
//
// Every product is written float64(a*b) so no architecture fuses it
// into an FMA: the draws and the rounded ratings are the same bits
// everywhere. Test-support code only — nothing in the library imports
// it.
package brandfixture

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"strconv"
)

// Spec is brand_spec.json.
type Spec struct {
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

// Parse decodes a brand_spec.json body.
func Parse(body []byte) (Spec, error) {
	var s Spec
	err := json.Unmarshal(body, &s)
	return s, err
}

// Row is one respondent: the wave (1 or 2: rows 0..N/2-1 are wave 1)
// and the values in Columns order (every item, then the outcome).
type Row struct {
	Wave   int
	Values []float64
}

// Columns are the value columns in Row.Values order: every factor's
// items in spec order, then the outcome.
func (s Spec) Columns() []string {
	var out []string
	for _, f := range s.Factors {
		out = append(out, f.Items...)
	}
	return append(out, s.Outcome.Name)
}

// Rows draws the spec's N respondents from its seed.
func (s Spec) Rows() []Row {
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
	z := make([]float64, k)
	f := make([]float64, k)
	out := make([]Row, 0, s.N)
	for r := 0; r < s.N; r++ {
		row := Row{Wave: 1}
		if r >= s.N/2 {
			row.Wave = 2
		}
		for i := range z {
			z[i] = rng.NormFloat64()
		}
		for i := range f {
			acc := 0.0
			for j := 0; j <= i; j++ {
				acc += float64(l[i][j] * z[j])
			}
			if row.Wave == 2 {
				acc += s.Factors[i].Wave2Shift
			}
			f[i] = acc
		}
		for i, fac := range s.Factors {
			for j := range fac.Items {
				v := s.Item.Intercept + float64(fac.Loadings[j]*f[i]) + float64(s.Item.NoiseSD*rng.NormFloat64())
				row.Values = append(row.Values, clamp(v, s.Item.Min, s.Item.Max))
			}
		}
		v := s.Outcome.Intercept
		for i, w := range s.Outcome.Weights {
			v += float64(w * f[i])
		}
		v += float64(s.Outcome.NoiseSD * rng.NormFloat64())
		row.Values = append(row.Values, clamp(v, s.Outcome.Min, s.Outcome.Max))
		out = append(out, row)
	}
	return out
}

// OracleCSV renders the rows as the lavaan oracles read them
// (brand_data.csv): a header of the wave field and Columns, then one
// line per row with the wave as 1 / 2.
func (s Spec) OracleCSV() []byte {
	var buf bytes.Buffer
	buf.WriteString(s.WaveField)
	for _, c := range s.Columns() {
		buf.WriteString("," + c)
	}
	buf.WriteString("\n")
	for _, r := range s.Rows() {
		buf.WriteString(strconv.Itoa(r.Wave))
		for _, v := range r.Values {
			buf.WriteString("," + strconv.FormatFloat(v, 'f', -1, 64))
		}
		buf.WriteString("\n")
	}
	return buf.Bytes()
}
