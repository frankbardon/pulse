// Package mmmfixture is the single source of the media-mix-model (MMM)
// decay-sweep acceptance fixture: a deterministic weekly cohort (two
// markets × Weeks weeks of four channel spends, two controls and sales)
// and the canonical one-step Compose request over it — four
// WIN_DISTRIBUTED_LAG windows with decay {"$var":"decay"}, a post-window hill()
// saturation per channel, a REG_OLS with bounds.nonnegative on the
// saturated channels plus the controls and a holdout predicate, swept
// over decay 0.0..0.9 and ranked by the fit's holdout RMSE — plus its
// seeded lhs twin, drawing the decay from a continuous range.
//
// The root, CLI and MCP acceptance tests all build the cohort and read
// the request from here, so every surface runs the same bytes.
// Test-support code only — nothing in the library imports it.
package mmmfixture

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
)

// CohortFile is the cohort filename the request bodies name.
const CohortFile = "mmm_weekly.pulse"

// Weeks is the weekly rows per market. HoldoutFrom is the first
// held-out week of the canonical request (`week >= 91`).
const (
	Weeks       = 104
	HoldoutFrom = 91
)

// Markets are the partitions, in row order.
var Markets = []string{"east", "west"}

// DecaySweep is the canonical Compose request: the decay sweep ranked
// by regressions.mmm.holdout.rmse, each slot's `return` excluding data.
//
//go:embed testdata/decay_sweep.json
var DecaySweep []byte

// DecaySweepSplit is the FEAT_TRAIN_TEST_SPLIT variant: the holdout is
// `split == 2`, the test third of a seeded three-way split.
//
//go:embed testdata/decay_sweep_split.json
var DecaySweepSplit []byte

// DecayLHSSweep is the sampled variant of DecaySweep: an `lhs` sweep
// of LHSSamples slots drawing decay from [0, 0.95] (the seed derived
// from the spec), ranked by regressions.mmm.holdout.rmse. Its slots
// carry the default sampled labels (sample=<i>_decay=<value>).
//
//go:embed testdata/decay_lhs_sweep.json
var DecayLHSSweep []byte

// LHSSamples is DecayLHSSweep's `samples`; TrueDecay is the decay the
// fixture's sales carry (Rows), which a ranked sweep should find.
const (
	LHSSamples = 20
	TrueDecay  = 0.5
)

// Decays are the swept decay values, in slot order, as the request
// writes them (the default slot labels are decay_<value>).
var Decays = []string{"0.0", "0.1", "0.2", "0.3", "0.4", "0.5", "0.6", "0.7", "0.8", "0.9"}

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

// SlotBody is the sweep body of composed with every {"$var":"decay"}
// replaced by the JSON number decay (written as the axis writes it) —
// the single request one sweep slot runs, for `pulse predict` and the
// one-request route.
func SlotBody(composed []byte, decay string) ([]byte, error) {
	var outer struct {
		Sweep struct {
			Request json.RawMessage `json:"request"`
		} `json:"sweep"`
	}
	if err := json.Unmarshal(composed, &outer); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(outer.Sweep.Request))
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	n := 0
	body = substitute(body, json.Number(decay), &n)
	if n == 0 {
		return nil, fmt.Errorf("mmmfixture: no {\"$var\":\"decay\"} placeholder in the sweep body")
	}
	return json.Marshal(body)
}

// substitute replaces every {"$var":"decay"} object under v by decay,
// counting the replacements in n.
func substitute(v any, decay json.Number, n *int) any {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 1 && x["$var"] == "decay" {
			*n++
			return decay
		}
		for k, e := range x {
			x[k] = substitute(e, decay, n)
		}
	case []any:
		for i, e := range x {
			x[i] = substitute(e, decay, n)
		}
	}
	return v
}
