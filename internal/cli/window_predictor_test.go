package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
)

// windowPredictorCLICohort writes the 30-week tv / sales cohort (sales
// carry a geometric adstock of tv) under a temp data dir and returns
// the dir and the cohort path.
func windowPredictorCLICohort(t *testing.T) (dir, cohort string) {
	t.Helper()
	dir = withTempDataDir(t)
	cohort = filepath.Join(dir, "weekly.pulse")
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(t.Context(), cohort, encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "sales", Type: encoding.FieldTypeF64},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ad := 0.0
	for wk := 1; wk <= 30; wk++ {
		tv := 100 + float64(60*math.Sin(float64(wk)/2))
		ad = tv + float64(0.5*ad)
		if err := b.Append(pulse.CohortRow{uint64(wk), tv, 10 + float64(0.2*ad) + math.Cos(float64(3*wk))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, cohort
}

// TestWindowPredictor_ParityCLIProcessSmoke (transformed-predictors
// E1-S2 surface smoke): `pulse api process --json` runs a request whose
// REG_OLS reads a same-request WIN_DISTRIBUTED_LAG label and returns the fit in
// the envelope — no error, the leading-null-free n, a finite slope.
func TestWindowPredictor_ParityCLIProcessSmoke(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir, cohort := windowPredictorCLICohort(t)
	reqPath := filepath.Join(dir, "req.json")
	if err := os.WriteFile(reqPath, []byte(`{"cohort":{"filename":"`+cohort+`"},`+
		`"windows":[{"type":"WIN_DISTRIBUTED_LAG","field":"tv","label":"tv_ad","order_by":[{"field":"week"}],`+
		`"params":{"kernel":"geometric","decay":0.5,"max_lag":4}}],`+
		`"regressions":[{"type":"REG_OLS","name":"mmm","target":"sales","predictors":["tv_ad"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "process", "--json", "-r", reqPath); err != nil {
		t.Fatalf("process: %v\n%s", err, buf.String())
	}
	var env struct {
		Errors []any `json:"errors"`
		Data   struct {
			Regressions []struct {
				Name         string             `json:"name"`
				NObs         int                `json:"n_obs"`
				Coefficients map[string]float64 `json:"coefficients"`
				Error        any                `json:"error"`
			} `json:"regressions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, buf.String())
	}
	if len(env.Errors) != 0 || len(env.Data.Regressions) != 1 {
		t.Fatalf("envelope %s", buf.String())
	}
	r := env.Data.Regressions[0]
	if r.Name != "mmm" || r.Error != nil || r.NObs != 30 || math.IsNaN(r.Coefficients["tv_ad"]) || r.Coefficients["tv_ad"] == 0 {
		t.Fatalf("regression %+v; want mmm fitted on 30 rows with a tv_ad slope", r)
	}
}

// adstockHillSweep is a compose holding only a sweep over the adstock
// decay of cohort: WIN_DISTRIBUTED_LAG (decay {"$var":"decay"}) → a post-window
// hill() attribute → REG_OLS with the saturated term bounded
// nonnegative.
func adstockHillSweep(cohort string) string {
	return `{"sweep":{"axes":[{"name":"decay","values":[0.2,0.5,0.8]}],"label":"decay_{{decay}}","request":` +
		`{"cohort":{"filename":"` + cohort + `"},` +
		`"windows":[{"type":"WIN_DISTRIBUTED_LAG","field":"tv","label":"tv_ad","order_by":[{"field":"week"}],` +
		`"params":{"kernel":"geometric","decay":{"$var":"decay"},"max_lag":4}}],` +
		`"attributes":[{"type":"ATTR_FORMULA","label":"tv_sat","expression":"hill(tv_ad, 200, 2)"}],` +
		`"regressions":[{"type":"REG_OLS","name":"mmm","target":"sales","predictors":["tv_sat"],` +
		`"bounds":{"nonnegative":["tv_sat"]}}]}}}`
}

// TestWindowPredictor_ParityCLIComposeSweep (transformed-predictors
// E2-S2 surface): `pulse api compose --json` runs a sweep over the
// adstock decay of an adstock → hill → regression chain — one fitted
// slot per decay, each slope different, each bit for bit the slot the
// library's Compose fits for the same batch.
func TestWindowPredictor_ParityCLIComposeSweep(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir, cohort := windowPredictorCLICohort(t)
	composed := adstockHillSweep(cohort)
	reqPath := filepath.Join(dir, "sweep.json")
	if err := os.WriteFile(reqPath, []byte(composed), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "compose", "--json", "-r", reqPath); err != nil {
		t.Fatalf("compose: %v\n%s", err, buf.String())
	}
	var env struct {
		Errors []any `json:"errors"`
		Data   struct {
			Responses []struct {
				Label       string `json:"label"`
				Regressions []struct {
					NObs         int                `json:"n_obs"`
					Coefficients map[string]float64 `json:"coefficients"`
					Error        any                `json:"error"`
				} `json:"regressions"`
			} `json:"responses"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, buf.String())
	}
	if len(env.Errors) != 0 || len(env.Data.Responses) != 3 {
		t.Fatalf("want 3 sweep slots and no errors; envelope %s", buf.String())
	}

	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal([]byte(composed), &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(t.Context(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	seen := map[float64]string{}
	for i, s := range env.Data.Responses {
		if len(s.Regressions) != 1 || s.Regressions[0].Error != nil || s.Regressions[0].NObs != 30 {
			t.Fatalf("slot %s: %+v; want one fit on 30 rows", s.Label, s.Regressions)
		}
		slope := s.Regressions[0].Coefficients["tv_sat"]
		if want := lib.Responses[i].Regressions[0].Coefficients["tv_sat"]; slope != want || !(slope > 0) {
			t.Fatalf("slot %s tv_sat = %v, library %v; want equal and positive", s.Label, slope, want)
		}
		if prev, dup := seen[slope]; dup {
			t.Fatalf("slots %s and %s fit the same slope %v: the swept decay never reached the adstock", prev, s.Label, slope)
		}
		seen[slope] = s.Label
	}
}
