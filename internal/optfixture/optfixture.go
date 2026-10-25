// Package optfixture is the single source of the Compose `optimize`
// acceptance fixture: a deterministic weekly price / units cohort, a
// saved demand model, and the canonical request bodies over them —
// a price optimisation on a fitted linear demand curve (coef() over a
// `fits` entry), its twin over the saved model file (no refit), and a
// four-channel budget allocation over hill() response curves with
// Σx = budget and per-channel caps.
//
// The root, CLI and MCP acceptance tests all build the cohort and read
// the requests from here, so every surface runs the same bytes.
// Test-support code only — nothing in the library imports it.
package optfixture

import (
	_ "embed"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// CohortFile is the cohort filename the request bodies name.
const CohortFile = "opt_price.pulse"

// ModelPath is the saved demand model PriceModel loads.
const ModelPath = "models/demand" + model.FileSuffix

// Weeks is the cohort's row count.
const Weeks = 80

// Cost is the unit cost the price requests set as params.cost.
const Cost = 5.0

// The saved demand model: units = ModelIntercept + ModelSlope · price,
// so its profit optimum (price − Cost)·units is
// (ModelSlope·Cost − ModelIntercept) / (2·ModelSlope) = ModelOptimum.
const (
	ModelIntercept = 120.0
	ModelSlope     = -4.0
	ModelOptimum   = 17.5
)

// Price is the price optimisation over the fitted demand curve: a
// REG_OLS `demand` fit (units on price) and a block maximising
// (price − params.cost)·(a + b·price) over price ∈ [1, 30].
//
//go:embed testdata/price.json
var Price []byte

// PriceModel is Price over the saved model file at ModelPath (block
// `models`), no fit.
//
//go:embed testdata/price_model.json
var PriceModel []byte

// Budget is the budget allocation: four channels, each a
// beta·hill(x, half, shape) response, maximising the total response
// under x_tv + x_search + x_social + x_display = params.budget, each
// channel in [0, its cap].
//
//go:embed testdata/budget.json
var Budget []byte

// Schema is the cohort schema: week, price and units.
func Schema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number since the start of the series"},
		{Name: "price", Type: encoding.FieldTypeF64, Description: "Average selling price that week in dollars"},
		{Name: "units", Type: encoding.FieldTypeF64, Description: "Units sold that week"},
	}}
}

// Rows is the cohort in week order (pulse.CohortRow-compatible): price
// cycles over [5, 15] and units = 100 − 4·price plus a bounded,
// zero-mean deterministic wobble. Every product is float64()-barriered.
func Rows() [][]any {
	rows := make([][]any, 0, Weeks)
	for wk := 0; wk < Weeks; wk++ {
		price := 5 + float64(0.25*float64((wk*7)%41))
		wobble := float64(0.5 * float64((wk*13)%9-4))
		units := 100 - float64(4*price) + wobble
		rows = append(rows, []any{uint64(wk), price, units})
	}
	return rows
}

// ModelFile is the saved demand model's bytes (ModelIntercept,
// ModelSlope on price), for the instance filesystem at ModelPath.
func ModelFile() ([]byte, error) {
	f, err := model.NewFile("demand", &types.RegressionSpec{Type: types.REG_OLS, Target: "units", Predictors: []string{"price"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: ModelIntercept, "price": ModelSlope}},
		model.Provenance{})
	if err != nil {
		return nil, err
	}
	return f.Encode()
}
