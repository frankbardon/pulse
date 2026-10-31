//go:build heavy

package synth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/io/csv"
	"github.com/frankbardon/pulse/internal/synth"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// churnFixture mirrors the churn structure of a customer-retention
// source: churn is driven by contract term, short tenure, many support
// contacts and no autopay, while region and age carry no churn effect.
// Contract also drives tenure and autopay, plan drives spend, and older
// customers favour longer contracts.
//
// Three of the four churn drivers are NUMERIC (tenure_months,
// support_contacts, autopay), which is what makes it the fixture for
// numeric model predictors: before they existed, `--fit-models` could
// condition `churned` on contract alone.
//
// Products are written float64(a*b) so the cohort bytes do not depend
// on whether the architecture fuses a multiply-add (agent-practices,
// Cross-architecture numerics).
func churnFixture(t *testing.T, rowCount int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	regions := []string{"North", "South", "East", "West", "Central"}
	plans := []string{"Basic", "Standard", "Premium", "Unlimited"}
	contracts := []string{"Monthly", "One-Year", "Two-Year"}
	contractEffect := []float64{0, -1.2, -2.1}
	planEffect := []float64{0.3, 0, -0.2, -0.3}

	var buf strings.Builder
	buf.WriteString("region,age,plan,contract,tenure_months,monthly_spend,support_contacts,autopay,churned\n")
	for i := 0; i < rowCount; i++ {
		region := regions[rng.Intn(len(regions))]
		age := 18 + rng.Intn(68)
		plan := rng.Intn(len(plans))

		// Older customers favour longer contracts.
		pLong := 0.25 + float64(0.006*float64(age-18))
		contract := 0
		if u := rng.Float64(); u < pLong {
			contract = 1
			if rng.Float64() < 0.45 {
				contract = 2
			}
		}
		tenure := 1 + rng.Intn(24)
		switch contract {
		case 1:
			tenure = 6 + rng.Intn(48)
		case 2:
			tenure = 12 + rng.Intn(90)
		}
		support := 0
		for k := 0; k < 6; k++ {
			if rng.Float64() < 0.16 {
				support++
			}
		}
		autopay := 0
		if rng.Float64() < 0.40+float64(0.15*float64(contract)) {
			autopay = 1
		}
		spend := 25.0 + float64(22.0*float64(plan)) + float64(rng.NormFloat64()*6)
		if spend < 5 {
			spend = 5
		}

		z := 0.4 + contractEffect[contract] + planEffect[plan] +
			float64(-0.03*float64(tenure)) + float64(0.45*float64(support)) - float64(0.55*float64(autopay))
		p := 1 / (1 + math.Exp(-z))
		churned := 0
		if rng.Float64() < p {
			churned = 1
		}
		fmt.Fprintf(&buf, "%s,%d,%s,%s,%d,%.2f,%d,%d,%d\n",
			region, age, plans[plan], contracts[contract], tenure, spend, support, autopay, churned)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "age", Type: encoding.FieldTypeU8},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8},
		{Name: "contract", Type: encoding.FieldTypeCategoricalU8},
		{Name: "tenure_months", Type: encoding.FieldTypeU16},
		{Name: "monthly_spend", Type: encoding.FieldTypeF64},
		{Name: "support_contacts", Type: encoding.FieldTypeU4},
		{Name: "autopay", Type: encoding.FieldTypePackedBool},
		{Name: "churned", Type: encoding.FieldTypePackedBool},
	}}
	return importCSVWithSchema(t, buf.String(), schema)
}

// importCSVWithSchema imports CSV text under an explicit schema into a
// throwaway in-memory filesystem and returns the .pulse bytes.
func importCSVWithSchema(t *testing.T, csvText string, schema *encoding.Schema) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs, DisablePanicRecovery: true})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/fixture.csv", []byte(csvText), 0o644); err != nil {
		t.Fatalf("write fixture csv: %v", err)
	}
	job := &pio.ImportJob{
		Source: csv.NewReader(fs, "/fixture.csv"),
		Target: "/fixture.pulse",
		Schema: schema,
		FS:     fs,
	}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import fixture csv: %v", err)
	}
	data, err := afero.ReadFile(fs, "/fixture.pulse")
	if err != nil {
		t.Fatalf("read imported fixture: %v", err)
	}
	return data
}

// churnLogitBody is the churn logistic model the source and its twin
// are both scored with: contract dummies plus every numeric driver,
// and age, which carries no true effect.
const churnLogitBody = `{
  "cohort": {"filename": "$COHORT"},
  "features": [{"type": "FEAT_ONE_HOT", "field": "contract"}],
  "regressions": [{
    "type": "REG_GLM", "name": "churn_logit", "target": "churned",
    "family": "binomial", "link": "logit",
    "predictors": ["contract_One-Year", "contract_Two-Year", "tenure_months", "support_contacts", "autopay", "age"]
  }]
}`

// churnLogit fits churnLogitBody over cohort bytes.
func churnLogit(t *testing.T, data []byte) *types.RegressionResult {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/c.pulse", data, 0o644); err != nil {
		t.Fatalf("write cohort: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, DisablePanicRecovery: true})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	var req types.Request
	if err := json.Unmarshal([]byte(strings.ReplaceAll(churnLogitBody, "$COHORT", "/c.pulse")), &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("churn logit failed: %+v", resp.Regressions)
	}
	return resp.Regressions[0]
}

// churnTwin profiles source with the full capture a data owner runs
// (`--include-stats --include-correlations --conditional --fit-models
// --residual-correlations --fit-shape`) and generates a same-size twin.
func churnTwin(t *testing.T, source []byte, rows int) ([]byte, *synth.Profile) {
	t.Helper()
	prof, err := synth.ProfileBytes(source, synth.ProfileOptions{
		IncludeStats:            true,
		IncludeCorrelations:     true,
		IncludeConditional:      true,
		FitModels:               true,
		FitResidualCorrelations: true,
		FitShape:                true,
		Seed:                    7,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	spec, _ := synth.SpecFromProfile(prof, rows)
	twin, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	return twin, prof
}

// TestProfileNumericPredictors_ChurnTwinCapturesNumericDrivers is the
// privacy-twin acceptance of numeric model predictors: `--fit-models`
// now conditions `churned` on its numeric drivers, and a twin generated
// from that profile reproduces their effect on a churn logistic model.
//
// Measured on this fixture (20,000 rows, arm64):
//
//	                         source   twin before   twin after
//	McFadden pseudo-R²       0.209    0.107         0.125
//	support_contacts β       +0.436   +0.035        +0.355
//	autopay β                −0.527   −0.267        −0.358
//	tenure_months β          −0.0276  −0.0066       −0.0089
//
// "before" is the tree prior to numeric candidates, when `churned` was
// modelled on contract alone. The remaining gap — tenure above all — is
// the linear-probability fit behind a 0/1 target being read as a
// probit: a driver whose effect saturates near p = 0 is attenuated by
// the LPM slope. The pseudo-R² bar (≥ 0.85× the source's) belongs to
// the next story, so it is reported here, not asserted.
func TestProfileNumericPredictors_ChurnTwinCapturesNumericDrivers(t *testing.T) {
	t.Parallel()

	const n = 20000
	source := churnFixture(t, n, 42)
	twin, prof := churnTwin(t, source, n)

	var churn *synth.FieldModel
	for i := range prof.Models {
		if prof.Models[i].Field == "churned" {
			churn = &prof.Models[i]
		}
	}
	if churn == nil {
		t.Fatalf("no model for churned; warnings=%v", prof.Warnings)
	}
	got := map[string]bool{}
	for _, p := range churn.Predictors {
		if p.Kind == synth.ModelPredictorNumeric {
			got[p.Field] = true
		}
	}
	for _, driver := range []string{"tenure_months", "support_contacts", "autopay"} {
		if !got[driver] {
			t.Errorf("churned's model does not carry numeric driver %q; predictors=%+v", driver, churn.Predictors)
		}
	}

	src := churnLogit(t, source)
	tw := churnLogit(t, twin)
	t.Logf("churn logit pseudo-R² source %.4f twin %.4f (ratio %.3f)", src.PseudoR2, tw.PseudoR2, tw.PseudoR2/src.PseudoR2)
	for _, driver := range []string{"tenure_months", "support_contacts", "autopay"} {
		s, w := src.Coefficients[driver], tw.Coefficients[driver]
		t.Logf("%s: source β %.4f twin β %.4f", driver, s, w)
		if (s < 0) != (w < 0) {
			t.Errorf("%s: twin β %.4f does not keep the source's sign (%.4f)", driver, w, s)
		}
	}
	// support_contacts reached churn only through a residual correlation
	// before (twin β 8% of the source's); as a numeric predictor it
	// keeps most of its effect.
	if r := tw.Coefficients["support_contacts"] / src.Coefficients["support_contacts"]; r < 0.5 {
		t.Errorf("twin keeps %.0f%% of support_contacts' churn effect, want ≥ 50%%", 100*r)
	}
}
