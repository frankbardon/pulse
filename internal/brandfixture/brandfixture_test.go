package brandfixture

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"
)

// TestBrandFixture_ExamplesCohortIsTheOracleRows holds the examples'
// brand_survey.csv (internal/examples/fixtures/gen.go) to the spec's
// rows: an id, the wave spelled wave1 / wave2, then the very values the
// lavaan oracles' brand_data.csv carries — so the SEM examples read the
// data the oracles fit, planted wave-2 shift included.
func TestBrandFixture_ExamplesCohortIsTheOracleRows(t *testing.T) {
	body, err := os.ReadFile("../sem/testdata/brand_spec.json")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../examples/fixtures/brand_survey.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rows := spec.Rows()
	if len(recs) != len(rows)+1 || len(rows) != spec.N {
		t.Fatalf("%d CSV lines, %d rows, n %d", len(recs), len(rows), spec.N)
	}
	cols := spec.Columns()
	if got, want := len(recs[0]), len(cols)+2; got != want || recs[0][1] != spec.WaveField {
		t.Fatalf("header %v", recs[0])
	}
	waves := map[int]int{}
	for i, r := range rows {
		rec := recs[i+1]
		waves[r.Wave]++
		if rec[0] != strconv.Itoa(i+1) || rec[1] != "wave"+strconv.Itoa(r.Wave) {
			t.Fatalf("row %d: id %s wave %s, want %d / wave%d", i, rec[0], rec[1], i+1, r.Wave)
		}
		for j, v := range r.Values {
			if rec[j+2] != strconv.FormatFloat(v, 'f', -1, 64) {
				t.Fatalf("row %d %s: %s, the spec draws %v (regenerate: go run internal/examples/fixtures/gen.go)", i, cols[j], rec[j+2], v)
			}
		}
	}
	if waves[1] != spec.N/2 || waves[2] != spec.N-spec.N/2 {
		t.Errorf("wave split %v", waves)
	}
}
