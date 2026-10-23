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

// TestWindowPredictor_ParityCLIProcessSmoke (transformed-predictors
// E1-S2 surface smoke): `pulse api process --json` runs a request whose
// REG_OLS reads a same-request WIN_ADSTOCK label and returns the fit in
// the envelope — no error, the leading-null-free n, a finite slope.
func TestWindowPredictor_ParityCLIProcessSmoke(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := filepath.Join(dir, "weekly.pulse")
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
	reqPath := filepath.Join(dir, "req.json")
	if err := os.WriteFile(reqPath, []byte(`{"cohort":{"filename":"`+cohort+`"},`+
		`"windows":[{"type":"WIN_ADSTOCK","field":"tv","label":"tv_ad","order_by":[{"field":"week"}],`+
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
