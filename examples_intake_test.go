package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestExamples_IntakeNullsArePlanted pins what the data_quality
// examples on intake.pulse teach: the nullable income field carries
// REAL nulls (the null count is non-zero, so the present-vs-null
// contrast is no longer a smoke test of zeros), the null filter keeps
// exactly the present rows, and the missing-data story's planted gap
// holds — paper forms leave income blank far more often than web
// forms.
func TestExamples_IntakeNullsArePlanted(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, p, osfs, tmp)

	num := func(t *testing.T, row map[string]any, key string) float64 {
		t.Helper()
		v, ok := row[key].(float64)
		if !ok {
			t.Fatalf("%s = %#v, want a number", key, row[key])
		}
		return v
	}

	resp := processExample(t, p, "aggregations/11_null_count.json", tmp)
	present := num(t, resp.Data[0], "income_present")
	null := num(t, resp.Data[0], "income_null")
	if null == 0 {
		t.Fatalf("income_null = 0: the fixture carries no planted nulls")
	}
	if present == 0 || null < 0.1*(present+null) {
		t.Fatalf("income present %v / null %v: want a visible share of nulls (>= 10%%)", present, null)
	}

	kept := processExample(t, p, "filterers/06_null.json", tmp)
	if got := num(t, kept.Data[0], "rows_present"); got != present {
		t.Fatalf("rows_present = %v, want the %v present incomes", got, present)
	}

	story := processExample(t, p, "aggregations/24_missing_by_channel.json", tmp)
	rates := map[string]float64{}
	for _, row := range story.Data {
		ch, _ := row["channel"].(string)
		pr, nl := num(t, row, "income_present"), num(t, row, "income_null")
		rates[ch] = nl / (pr + nl)
	}
	if len(rates) != 3 {
		t.Fatalf("got channels %v, want web, phone and paper", rates)
	}
	if rates["web"] == 0 || rates["paper"] < 3*rates["web"] {
		t.Fatalf("null rates %v: want paper's income gap at least three times web's", rates)
	}
}
