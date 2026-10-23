package mcp

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// TestWindowPredictor_ParityMCPProcessSmoke (transformed-predictors
// E1-S2 surface smoke): pulse_process runs a request whose REG_OLS reads
// a same-request WIN_LAG label under the default (standard) return and
// returns the fit — no error, each leading null lag dropped listwise.
func TestWindowPredictor_ParityMCPProcessSmoke(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "weekly.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "radio", Type: encoding.FieldTypeF64},
		{Name: "sales", Type: encoding.FieldTypeF64},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prev := 0.0
	for wk := 1; wk <= 30; wk++ {
		radio := 50 + float64(20*math.Cos(float64(wk)/3))
		if err := b.Append(pulse.CohortRow{uint64(wk), radio, 5 + float64(0.4*prev) + math.Sin(float64(5*wk))}); err != nil {
			t.Fatal(err)
		}
		prev = radio
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	out := invokeTool(t, p, Config{}, "pulse_process", `{"cohort":{"filename":"weekly.pulse"},`+
		`"windows":[{"type":"WIN_LAG","field":"radio","label":"radio_lag","order_by":[{"field":"week"}]}],`+
		`"regressions":[{"type":"REG_OLS","name":"mmm","target":"sales","predictors":["radio_lag"]}]}`)
	var resp struct {
		Regressions []struct {
			Name         string             `json:"name"`
			NObs         int                `json:"n_obs"`
			Coefficients map[string]float64 `json:"coefficients"`
			Error        any                `json:"error"`
		} `json:"regressions"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Regressions) != 1 {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	r := resp.Regressions[0]
	if r.Name != "mmm" || r.Error != nil || r.NObs != 29 || math.IsNaN(r.Coefficients["radio_lag"]) || r.Coefficients["radio_lag"] == 0 {
		t.Fatalf("regression %+v; want mmm fitted on 29 rows with a radio_lag slope\n%s", r, out)
	}
}
