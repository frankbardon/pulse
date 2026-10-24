package sem

import (
	"bytes"
	"encoding/csv"
	"flag"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/internal/brandfixture"
)

// The brand-survey fixture is the lavaan oracles' data: a generic,
// synthetic two-wave survey whose true model is testdata/brand_spec.json.
// brandSurvey generates it from the spec and its seed alone, so the
// committed CSV is reproducible byte for byte; the lavaan references
// (testdata/fit_lavaan.R, run by testdata/gen_lavaan.sh) are fitted to
// that CSV.

var regenBrand = flag.Bool("regen-brand", false, "rewrite testdata/brand_data.csv from testdata/brand_spec.json")

func loadBrandSpec(t *testing.T) brandfixture.Spec {
	t.Helper()
	b, err := os.ReadFile("testdata/brand_spec.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := brandfixture.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// brandSurvey renders the spec's rows as the oracles' CSV
// (internal/brandfixture, the one generator the examples' brand_survey
// cohort shares).
func brandSurvey(s brandfixture.Spec) []byte { return s.OracleCSV() }

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
