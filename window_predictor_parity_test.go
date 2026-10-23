package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// window_predictor_parity_test.go — parity gate A of transformed
// predictors (U57): a regression naming a same-request window label
// fits EXACTLY what the two-step reference route fits — materialize the
// features, filters, attributes and windows into a cohort, then Process
// the regression over the materialized columns. "Exactly" is float64
// `==` on every number of every RegressionResult (NaN equal to NaN):
// both routes run live in this process over the same stored bytes, so
// any difference is a pipeline difference, never an FMA one.
//
// The harness is reusable: a case is a single-request route (any func
// returning regression results — a Process, a Compose fit, a sweep
// slot) against a materialize spec plus the step-2 request built over
// the written cohort. Step 2 carries no features or
// filters of its own: a step-2 filter or feature would see the step-1
// survivors, a different population than the single request's features
// (all records) and filters (pre-window). Step-2 attributes are allowed
// — the reference half of a post-window attribute (adstock then hill).
// Add a case by appending a windowParityCase; add a figure to compare by
// adding it to RegressionResult — the comparator walks the whole struct.

// windowParityCase is one parity-gate case.
type windowParityCase struct {
	name string
	// Single runs the one-request route and returns its regression
	// results (processRoute for a plain Process).
	Single func(t *testing.T, p *Pulse) []*types.RegressionResult
	// Materialize is step 1 of the reference route (features,
	// filterers, attributes, windows; its Cohort names the source).
	Materialize *Request
	// Reference builds the step-2 request over the materialized cohort
	// path. It may not carry features or filterers.
	Reference func(cohort string) *Request
	// Check, when set, runs case-specific assertions on the
	// single-route results after the parity comparison.
	Check func(t *testing.T, single []*types.RegressionResult)
}

// processRoute is the plain single-request route: Process req.
func processRoute(req *Request) func(t *testing.T, p *Pulse) []*types.RegressionResult {
	return func(t *testing.T, p *Pulse) []*types.RegressionResult {
		t.Helper()
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("single route Process: %v", err)
		}
		return resp.Regressions
	}
}

// windowParityFromSlots builds the common case: the single request is
// derive (features / filterers / attributes / windows) plus regs; the
// reference materializes derive and regresses regs over the result.
func windowParityFromSlots(name string, derive *Request, regs ...*types.RegressionSpec) windowParityCase {
	single := *derive
	single.Regressions = regs
	return windowParityCase{
		name:        name,
		Single:      processRoute(&single),
		Materialize: derive,
		Reference: func(cohort string) *Request {
			return &Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: regs}
		},
	}
}

// runWindowParity materializes c.Materialize (to a fresh path per
// case), runs both routes and asserts every regression result equal
// bit for bit. It returns both routes' results for further checks.
func runWindowParity(t *testing.T, p *Pulse, c windowParityCase) (single, ref []*types.RegressionResult) {
	t.Helper()
	ctx := context.Background()
	out := "parity-" + c.name + ".pulse"
	if _, err := p.Materialize(ctx, c.Materialize, out, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("reference route Materialize: %v", err)
	}
	step2 := c.Reference(out)
	if len(step2.Features)+len(step2.Filterers) != 0 {
		t.Fatalf("reference step 2 must carry no features or filterers (they would see a different population); got %+v", step2)
	}
	resp, err := p.Process(ctx, step2)
	if err != nil {
		t.Fatalf("reference route Process: %v", err)
	}
	ref = resp.Regressions
	single = c.Single(t, p)
	assertRegressionsBitEqual(t, single, ref)
	if c.Check != nil {
		c.Check(t, single)
	}
	return single, ref
}

// assertRegressionsBitEqual fails unless got and want hold the same
// regression results, every float64 equal under `==` (NaN == NaN). A
// failed slot or an empty fit on either side fails too, so two routes
// refusing alike never pass vacuously.
func assertRegressionsBitEqual(t *testing.T, got, want []*types.RegressionResult) {
	t.Helper()
	if len(got) == 0 || len(got) != len(want) {
		t.Fatalf("regression count: single %d, reference %d", len(got), len(want))
	}
	for i := range got {
		for side, r := range map[string]*types.RegressionResult{"single": got[i], "reference": want[i]} {
			if r == nil || r.Error != nil || r.NObs == 0 || len(r.Coefficients) == 0 {
				t.Fatalf("%s route regression %d did not fit: %+v", side, i, r)
			}
		}
		var diffs []string
		bitDiff(fmt.Sprintf("regressions[%d]", i), reflect.ValueOf(got[i]), reflect.ValueOf(want[i]), &diffs)
		if len(diffs) != 0 {
			sort.Strings(diffs)
			t.Errorf("regression %q: single route differs from the materialize route:\n%v", got[i].Name, diffs)
		}
	}
}

// bitDiff walks a and b (same type) and records every path whose
// values differ; floats compare with `==`, NaN equal to NaN.
func bitDiff(path string, a, b reflect.Value, diffs *[]string) {
	if a.IsValid() != b.IsValid() {
		*diffs = append(*diffs, path+": presence differs")
		return
	}
	if !a.IsValid() {
		return
	}
	switch a.Kind() {
	case reflect.Float32, reflect.Float64:
		x, y := a.Float(), b.Float()
		if !(x == y || (math.IsNaN(x) && math.IsNaN(y))) {
			*diffs = append(*diffs, fmt.Sprintf("%s: %v (%#x) != %v (%#x)", path, x, math.Float64bits(x), y, math.Float64bits(y)))
		}
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() != b.IsNil() {
			*diffs = append(*diffs, path+": nil-ness differs")
			return
		}
		if !a.IsNil() {
			ae, be := a.Elem(), b.Elem()
			if ae.Type() != be.Type() {
				*diffs = append(*diffs, fmt.Sprintf("%s: type %v != %v", path, ae.Type(), be.Type()))
				return
			}
			bitDiff(path, ae, be, diffs)
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			bitDiff(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i), diffs)
		}
	case reflect.Slice, reflect.Array:
		if a.Kind() == reflect.Slice && a.IsNil() != b.IsNil() {
			*diffs = append(*diffs, path+": nil-ness differs")
			return
		}
		if a.Len() != b.Len() {
			*diffs = append(*diffs, fmt.Sprintf("%s: len %d != %d", path, a.Len(), b.Len()))
			return
		}
		for i := 0; i < a.Len(); i++ {
			bitDiff(fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i), diffs)
		}
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			*diffs = append(*diffs, fmt.Sprintf("%s: map len %d != %d", path, a.Len(), b.Len()))
			return
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() {
				*diffs = append(*diffs, fmt.Sprintf("%s[%v]: missing on reference", path, k))
				continue
			}
			bitDiff(fmt.Sprintf("%s[%v]", path, k), a.MapIndex(k), bv, diffs)
		}
	default:
		if a.CanInterface() && !reflect.DeepEqual(a.Interface(), b.Interface()) {
			*diffs = append(*diffs, fmt.Sprintf("%s: %v != %v", path, a.Interface(), b.Interface()))
		}
	}
}

// Weekly multi-channel fixture: two markets × windowParityWeeks weeks of
// tv / radio / search spend, sales and a frequency weight w. Sales
// carry real adstock, lag and saturation structure so every fit is
// well-conditioned; search enters negatively so bounds.nonnegative
// binds. Products are float64()-barriered (agent-practices: generator
// rule) though parity itself never depends on the inputs.
const windowParityWeeks = 52

func windowParitySchema() encoding.Schema {
	d := encoding.NewDictionary()
	for _, m := range []string{"east", "west"} {
		if _, err := d.Add(m); err != nil {
			panic(err)
		}
	}
	f64 := func(name, desc string) encoding.Field {
		return encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: desc}
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "market", Type: encoding.FieldTypeCategoricalU8, Description: "Sales market of the weekly row", Dictionary: d},
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number within the year"},
		f64("tv", "Weekly television spend in dollars"),
		f64("radio", "Weekly radio spend in dollars"),
		f64("search", "Weekly paid search spend in dollars"),
		f64("sales", "Weekly sales revenue in thousands"),
		f64("w", "Frequency weight of the weekly row"),
	}}
}

func windowParityRows() []CohortRow {
	var rows []CohortRow
	for mi, m := range []string{"east", "west"} {
		ad, prevRadio := 0.0, 0.0
		for wk := 1; wk <= windowParityWeeks; wk++ {
			x := float64(wk + 17*mi)
			tv := 400 + float64(300*math.Sin(x/3)) + float64(40*float64(wk%5))
			radio := 120 + float64(80*math.Cos(x/4)) + float64(15*float64(wk%3))
			search := 50 + float64(30*math.Sin(x/2+1)) + float64(5*float64(wk%7))
			ad = tv + float64(0.6*ad)
			sales := 200 + float64(0.05*ad) + float64(0.3*prevRadio) - float64(4*math.Log1p(search)) +
				float64(0.5*float64(wk)) + float64(3*math.Sin(float64(7*wk+mi)))
			rows = append(rows, CohortRow{m, uint64(wk), tv, radio, search, sales, float64(1 + (wk+mi)%3)})
			prevRadio = radio
		}
	}
	return rows
}

// windowParityPulse builds a hermetic instance (opts.FS replaced) with
// the fixture written at weekly.pulse.
func windowParityPulse(t *testing.T, opts Options) *Pulse {
	t.Helper()
	return windowParityPulseRows(t, opts, windowParityRows())
}

// windowParityPulseRows is windowParityPulse over caller-edited rows.
func windowParityPulseRows(t *testing.T, opts Options, rows []CohortRow) *Pulse {
	t.Helper()
	p, _ := matPulse(t, opts)
	b, err := p.NewCohortBuilder(context.Background(), "weekly.pulse", windowParitySchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range rows {
		if err := b.Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p
}

// windowParityDerive is the derived half of every case: a feature
// (FEAT_LOG over every record), a filter (weeks ≥ 3: the windows see
// survivors only), an attribute (radio in hundreds) and three windows
// per market in week order — WIN_ADSTOCK geometric over tv, WIN_LAG
// over the attribute (null on each market's first surviving week) and
// WIN_EWMA over the feature output — plus trend, a row-local attribute.
func windowParityDerive() *Request {
	byMarket := []string{"market"}
	byWeek := []types.OrderKey{{Field: "week"}}
	return &Request{
		Cohort:    &types.Cohort{Filename: "weekly.pulse"},
		Features:  []*types.Feature{{Type: types.FEAT_LOG, Field: "search"}},
		Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"3", "1000"}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "radio_h", Expression: "radio / 100"},
			{Type: types.ATTR_FORMULA, Label: "trend", Expression: "week / 52"},
		},
		Windows: []*types.Window{
			{Type: types.WIN_ADSTOCK, Field: "tv", Label: "tv_ad", PartitionBy: byMarket, OrderBy: byWeek,
				Params: json.RawMessage(`{"kernel":"geometric","decay":0.6,"max_lag":6}`)},
			{Type: types.WIN_LAG, Field: "radio_h", Label: "radio_lag", PartitionBy: byMarket, OrderBy: byWeek},
			{Type: types.WIN_EWMA, Field: "LOG_search", Label: "search_ewma", PartitionBy: byMarket, OrderBy: byWeek,
				Params: json.RawMessage(`{"alpha":0.4}`), Frame: &types.FrameSpec{Mode: "rows"}},
		},
	}
}

// windowParityOLS is the base fit: sales on the three window outputs
// and the trend attribute.
func windowParityOLS(name string) *types.RegressionSpec {
	return &types.RegressionSpec{Name: name, Type: types.REG_OLS, Target: "sales",
		Predictors: []string{"tv_ad", "radio_lag", "search_ewma", "trend"}}
}

// windowParityNObs is the listwise n: two markets × (52 − 2 filtered
// weeks), minus each market's leading null lag.
const windowParityNObs = 2*(windowParityWeeks-2) - 2

// TestWindowPredictor_ParityWithMaterializeRoute is parity gate A: a
// single request regressing on window labels fits bit for bit what
// materialize → Process fits over the materialized columns — plain
// OLS, bounds.nonnegative, a seeded bootstrap and a weighted fit.
func TestWindowPredictor_ParityWithMaterializeRoute(t *testing.T) {
	p := windowParityPulse(t, Options{})
	nObs := func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		if single[0].NObs != windowParityNObs {
			t.Fatalf("n_obs = %d, want %d (each market's leading null lag dropped listwise)", single[0].NObs, windowParityNObs)
		}
	}

	plain := windowParityFromSlots("plain", windowParityDerive(), windowParityOLS("plain"))
	plain.Check = nObs

	bounded := windowParityOLS("bounded")
	bounded.Bounds = &types.RegressionBounds{Nonnegative: []string{"tv_ad", "radio_lag", "search_ewma"}}
	bound := windowParityFromSlots("bounds", windowParityDerive(), bounded)
	bound.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		nObs(t, single)
		// The unconstrained search slope is negative: the bound binds.
		if c := single[0].Coefficients["search_ewma"]; c != 0 {
			t.Fatalf("bounded search_ewma = %v, want 0 (the nonnegative bound binds)", c)
		}
	}

	boot := windowParityOLS("bootstrap")
	boot.Resample, boot.BootstrapIters, boot.RNGSeed = "bootstrap", 64, 20261023
	bootstrap := windowParityFromSlots("bootstrap", windowParityDerive(), boot)
	bootstrap.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		nObs(t, single)
		if single[0].BootstrapSucceeded == 0 {
			t.Fatalf("bootstrap succeeded 0 of %d replicates", single[0].BootstrapRequested)
		}
	}

	wtd := windowParityOLS("weighted")
	wtd.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	weighted := windowParityFromSlots("weighted", windowParityDerive(), wtd)
	weighted.Check = nObs

	for _, c := range []windowParityCase{plain, bound, bootstrap, weighted} {
		t.Run(c.name, func(t *testing.T) { runWindowParity(t, p, c) })
	}
}

// countedLag is an embedder window (the previous row's field value in
// its partition, null on the first) that counts its Compute calls — the
// probe that the engine runs a window once per Process, never per
// bootstrap replicate.
type countedLag struct {
	field string
	calls *int
}

func (c countedLag) Compute(rows []map[string]any, partitions [][]int, label string) error {
	*c.calls++
	for _, part := range partitions {
		var prev any
		for _, i := range part {
			rows[i][label] = prev
			prev = rows[i][c.field]
		}
	}
	return nil
}

// TestWindowPredictor_ParityBootstrapRunsWindowsOnce: a seeded
// bootstrap over an embedder window's label matches the materialize
// route bit for bit, and the single route computes the window ONCE for
// all replicates — a replicate resamples the windowed records, it
// never re-runs the window over a resampled series.
func TestWindowPredictor_ParityBootstrapRunsWindowsOnce(t *testing.T) {
	calls := 0
	p := windowParityPulse(t, Options{Extensions: Extensions{Windows: []WindowRegistration{{
		Name:        "WIN_ACME_COUNTED_LAG",
		Description: "Previous row's value in the partition, counting its runs",
		Factory: func(spec *types.Window, _ extend.WindowOptions) (extend.WindowComputer, error) {
			return countedLag{field: spec.Field, calls: &calls}, nil
		},
	}}}})
	derive := windowParityDerive()
	derive.Windows = append(derive.Windows, &types.Window{Type: "WIN_ACME_COUNTED_LAG", Field: "tv", Label: "tv_lag",
		PartitionBy: []string{"market"}, OrderBy: []types.OrderKey{{Field: "week"}}})
	boot := windowParityOLS("bootstrap")
	boot.Predictors = append(boot.Predictors, "tv_lag")
	boot.Resample, boot.BootstrapIters, boot.RNGSeed = "bootstrap", 32, 7
	c := windowParityFromSlots("bootstrap-once", derive, boot)
	single := c.Single
	c.Single = func(t *testing.T, p *Pulse) []*types.RegressionResult {
		calls = 0
		out := single(t, p)
		if calls != 1 {
			t.Fatalf("window computed %d times for one Process with %d bootstrap replicates, want 1", calls, boot.BootstrapIters)
		}
		return out
	}
	c.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		if r := single[0]; r.BootstrapRequested != 32 || r.BootstrapSucceeded == 0 || r.NObs != windowParityNObs {
			t.Fatalf("bootstrap %d/%d, n_obs %d; want 32 requested, some succeeded, n_obs %d",
				r.BootstrapSucceeded, r.BootstrapRequested, r.NObs, windowParityNObs)
		}
	}
	runWindowParity(t, p, c)
}

// TestWindowPredictor_ParityNullAndNaNCells pins how a missing window
// value reaches a fit, with the materialized column as the reference.
// A NULL window cell (each market's leading lag; adstock on a NaN spend
// row) is stored null — the reader returns nil, never NaN — and drops
// its record listwise on both routes alike. A NaN window VALUE (a lag
// carrying a NaN spend forward) is stored as NaN, not null — derivedFloat
// keeps it, as it keeps a NaN schema cell — yet a regression reads a
// non-finite input as missing (regression.InputValue, E1-S3), so it
// drops its record listwise too, on both routes alike; so does a NaN
// schema cell regressed on directly.
func TestWindowPredictor_ParityNullAndNaNCells(t *testing.T) {
	rows := windowParityRows()
	rows[10][2] = math.NaN() // east, week 11: tv is NaN
	p := windowParityPulseRows(t, Options{}, rows)
	derive := windowParityDerive()
	derive.Windows = append(derive.Windows, &types.Window{Type: types.WIN_LAG, Field: "tv", Label: "tv_lag",
		PartitionBy: []string{"market"}, OrderBy: []types.OrderKey{{Field: "week"}}})

	// The reference: what the materialized columns hold.
	if _, err := p.Materialize(context.Background(), derive, "cells.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	schema, got := readAll(t, p, "cells.pulse")
	col := map[string]int{}
	for i, f := range schema.Fields {
		col[f.Name] = i
	}
	cell := func(market string, week uint64, name string) any {
		t.Helper()
		for _, r := range got {
			if r[col["market"]] == market && r[col["week"]] == week {
				return r[col[name]]
			}
		}
		t.Fatalf("no materialized row %s week %d", market, week)
		return nil
	}
	for _, m := range []string{"east", "west"} {
		if v := cell(m, 3, "radio_lag"); v != nil {
			t.Errorf("%s week 3 radio_lag = %v, want null (leading lag)", m, v)
		}
	}
	if v := cell("east", 11, "tv_ad"); v != nil {
		t.Errorf("east week 11 tv_ad = %v, want null (adstock on a NaN spend row)", v)
	}
	if v, ok := cell("east", 12, "tv_lag").(float64); !ok || !math.IsNaN(v) {
		t.Errorf("east week 12 tv_lag = %v, want a stored NaN (not null)", cell("east", 12, "tv_lag"))
	}

	// Null cells drop listwise identically: one more record (the NaN
	// adstock row) than the base fit loses.
	nullCase := windowParityFromSlots("null-cells", derive, windowParityOLS("null-cells"))
	nullCase.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		if single[0].NObs != windowParityNObs-1 {
			t.Fatalf("n_obs = %d, want %d (leading lags and the null adstock row dropped)", single[0].NObs, windowParityNObs-1)
		}
	}
	runWindowParity(t, p, nullCase)

	// A NaN value drops listwise on both routes, like a null: the fit
	// succeeds without the NaN-lag record (east week 12) on top of the
	// null-cell drops.
	nanReg := windowParityOLS("nan-value")
	nanReg.Predictors = append(nanReg.Predictors, "tv_lag")
	nanCase := windowParityFromSlots("nan-value", derive, nanReg)
	nanCase.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		if single[0].Error != nil {
			t.Fatalf("NaN-value slot failed: %+v", single[0].Error)
		}
		if single[0].NObs != windowParityNObs-2 {
			t.Fatalf("n_obs = %d, want %d (the NaN-lag row dropped like a null)", single[0].NObs, windowParityNObs-2)
		}
	}
	runWindowParity(t, p, nanCase)

	// A NaN schema cell regressed on directly drops the same way.
	schemaReg := &types.RegressionSpec{Name: "nan-schema", Type: types.REG_OLS, Target: "sales",
		Predictors: []string{"tv", "radio_lag", "trend"}}
	schemaCase := windowParityFromSlots("nan-schema", derive, schemaReg)
	schemaCase.Check = func(t *testing.T, single []*types.RegressionResult) {
		t.Helper()
		if single[0].Error != nil {
			t.Fatalf("NaN-schema slot failed: %+v", single[0].Error)
		}
		if single[0].NObs != windowParityNObs-1 {
			t.Fatalf("n_obs = %d, want %d (the NaN tv row dropped like a null)", single[0].NObs, windowParityNObs-1)
		}
	}
	runWindowParity(t, p, schemaCase)
}
