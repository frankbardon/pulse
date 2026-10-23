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

// TestWindowPredictor_ParityMCPComposeSweep (transformed-predictors
// E2-S2 surface): pulse_compose runs a sweep over the adstock decay of
// an adstock → hill → regression chain under the default (standard)
// return — one fitted slot per decay, each slope different, each bit
// for bit the slot the library's Compose fits for the same batch.
func TestWindowPredictor_ParityMCPComposeSweep(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "weekly.pulse", encoding.Schema{Fields: []encoding.Field{
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
	composed := `{"sweep":{"axes":[{"name":"decay","values":[0.2,0.5,0.8]}],"label":"decay_{{decay}}","request":` +
		`{"cohort":{"filename":"weekly.pulse"},` +
		`"windows":[{"type":"WIN_ADSTOCK","field":"tv","label":"tv_ad","order_by":[{"field":"week"}],` +
		`"params":{"kernel":"geometric","decay":{"$var":"decay"},"max_lag":4}}],` +
		`"attributes":[{"type":"ATTR_FORMULA","label":"tv_sat","expression":"hill(tv_ad, 200, 2)"}],` +
		`"regressions":[{"type":"REG_OLS","name":"mmm","target":"sales","predictors":["tv_sat"],` +
		`"bounds":{"nonnegative":["tv_sat"]}}]}}}`
	out := invokeTool(t, p, Config{}, "pulse_compose", composed)
	var resp struct {
		Responses []struct {
			Label       string `json:"label"`
			Regressions []struct {
				NObs         int                `json:"n_obs"`
				Coefficients map[string]float64 `json:"coefficients"`
				Error        any                `json:"error"`
			} `json:"regressions"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Responses) != 3 {
		t.Fatalf("want 3 sweep slots: %v\n%s", err, out)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal([]byte(composed), &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	seen := map[float64]string{}
	for i, s := range resp.Responses {
		if len(s.Regressions) != 1 || s.Regressions[0].Error != nil || s.Regressions[0].NObs != 30 {
			t.Fatalf("slot %s: %+v; want one fit on 30 rows\n%s", s.Label, s.Regressions, out)
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
