// Package mmmfixture holds deterministic media-mix cohorts for tests
// and the examples corpus: Rows / Schema, a weekly two-market cohort
// whose sales carry a known geometric adstock (the examples'
// mmm_weekly.pulse), and Joint, a one-market cohort whose sales carry a
// known adstock and Hill curve per channel (the REG_MMM_HILL /
// REG_MMM_BAYES operator tests' truth). Test-support code only —
// nothing in the library imports it.
package mmmfixture

import (
	"math"

	"github.com/frankbardon/pulse/encoding"
)

// CohortFile is the cohort filename the request bodies name.
const CohortFile = "mmm_weekly.pulse"

// Weeks is the weekly rows per market.
const Weeks = 104

// Markets are the partitions, in row order.
var Markets = []string{"east", "west"}

// TrueDecay is the geometric adstock decay the cohort's sales carry
// (Rows).
const TrueDecay = 0.5

// Schema is the cohort schema: market, week, four channel spends
// (tv, radio, search, social), two controls (price, promo) and sales.
func Schema() encoding.Schema {
	d := encoding.NewDictionary()
	for _, m := range Markets {
		if _, err := d.Add(m); err != nil {
			panic(err)
		}
	}
	f64 := func(name, desc string) encoding.Field {
		return encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: desc}
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "market", Type: encoding.FieldTypeCategoricalU8, Description: "Sales market of the weekly row", Dictionary: d},
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number since the start of the series"},
		f64("tv", "Weekly television spend in dollars"),
		f64("radio", "Weekly radio spend in dollars"),
		f64("search", "Weekly paid search spend in dollars"),
		f64("social", "Weekly paid social spend in dollars"),
		f64("price", "Average shelf price in dollars"),
		f64("promo", "One when a promotion ran that week, else zero"),
		f64("sales", "Weekly sales revenue in thousands"),
	}}
}

// Rows is the cohort, market by market in week order, each row in
// Schema field order (pulse.CohortRow-compatible). Sales carry a true
// geometric adstock (decay 0.5) of every channel, a Hill saturation, a
// price and a promotion effect — so the swept decays fit differently
// and the holdout RMSE separates them. Every product is
// float64()-barriered (agent-practices: generator rule).
func Rows() [][]any {
	type channel struct {
		base, amp, period, half float64
		lift                    float64
	}
	chans := []channel{
		{400, 300, 3, 800, 260}, // tv
		{120, 80, 4, 240, 140},  // radio
		{50, 30, 2, 100, 90},    // search
		{80, 60, 5, 160, 70},    // social
	}
	const trueDecay = TrueDecay
	var rows [][]any
	for mi, m := range Markets {
		ad := make([]float64, len(chans))
		for wk := 1; wk <= Weeks; wk++ {
			x := float64(wk + 23*mi)
			spend := make([]float64, len(chans))
			sales := 500.0
			for c, ch := range chans {
				s := ch.base + float64(ch.amp*math.Sin(x/ch.period+float64(c))) + float64(0.1*ch.base*float64((wk+c)%4))
				if s < 0 {
					s = 0
				}
				spend[c] = s
				ad[c] = s + float64(trueDecay*ad[c])
				h := math.Pow(ad[c], 1.5) / (math.Pow(ad[c], 1.5) + math.Pow(ch.half, 1.5))
				sales += float64(ch.lift * h)
			}
			price := 10 + float64(0.8*math.Cos(x/6))
			promo := 0.0
			if (wk+mi)%9 == 0 {
				promo = 1
			}
			sales += -float64(12*price) + float64(35*promo) + float64(6*math.Sin(float64(11*wk+3*mi)))
			rows = append(rows, []any{m, uint64(wk), spend[0], spend[1], spend[2], spend[3], price, promo, sales})
		}
	}
	return rows
}
