package pulse

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestDateTime_AnalyticsAggregatorsReadTheNumericChannel is the runtime
// half of the `datetime` capability declaration.
//
// descriptor/capabilities_aggregators.go declaring a type is a CLAIM;
// this is the confirmation that the claim is honoured. The chain under
// test is: infer a `datetime` column on import → decode it through
// encoding.decodeFixed, which writes epoch SECONDS into Record.values as
// a float64 → read it back through Record.NumericValue, which is where
// every analytics aggregator's collectValues() sources its slice.
//
// Epoch seconds, not days: the day-truncation adapter in
// processing/date_field.go is scoped to the date-family GROUPERS and
// FILTERS (GROUP_DATE, GROUP_DATE_RANGES, FILTER_DATE_RANGES) and does
// not sit on the aggregation path. The expected values below are
// therefore raw epoch seconds, and a stray `/ 86400` anywhere on the
// aggregation path would rescale every one of them by 86,400.
func TestDateTime_AnalyticsAggregatorsReadTheNumericChannel(t *testing.T) {
	// 2024-01-01T00:00:00Z and three ten-second steps, tiled so
	// inference sees a full sample window. Tiling keeps the multiset
	// {0,10,20,30}s uniform, so the population moments are exact.
	const base = 1704067200 // 2024-01-01T00:00:00Z in epoch seconds
	stamps := []string{
		"2024-01-01T00:00:00Z",
		"2024-01-01T00:00:10Z",
		"2024-01-01T00:00:20Z",
		"2024-01-01T00:00:30Z",
	}
	var rows [][]string
	for i := range 40 {
		rows = append(rows, []string{stamps[i%len(stamps)]})
	}

	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "dt.pulse", []string{"seen_at"}, rows)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The test is only meaningful if the column really is `datetime`.
	ins, err := p.Inspect(context.Background(), "dt.pulse")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var fieldType string
	for _, f := range ins.Fields {
		if f.Name == "seen_at" {
			fieldType = f.Type
		}
	}
	if fieldType != encoding.FieldTypeDateTime.String() {
		t.Fatalf("seen_at inferred as %q, want %q — the rest of this test would prove nothing",
			fieldType, encoding.FieldTypeDateTime.String())
	}

	// Population moments over the offsets {0,10,20,30}: mean 15,
	// variance (225+25+25+225)/4 = 125, stddev sqrt(125).
	cases := []struct {
		alias string
		agg   types.AggregationType
		want  float64
	}{
		{"sum", types.AGG_SUM, float64(base)*40 + 10*(0+1+2+3)*10},
		{"avg", types.AGG_AVERAGE, float64(base) + 15},
		{"min", types.AGG_MIN, float64(base)},
		{"max", types.AGG_MAX, float64(base) + 30},
		{"rng", types.AGG_RANGE, 30},
		{"med", types.AGG_MEDIAN, float64(base) + 15},
		{"var", types.AGG_VARIANCE, 125},
		{"sd", types.AGG_STDDEV, math.Sqrt(125)},
		{"p100", types.AGG_PERCENTILE, float64(base) + 30},
	}

	var aggs []*types.Aggregation
	for _, c := range cases {
		a := &types.Aggregation{Type: c.agg, Field: "seen_at", Label: c.alias}
		if c.agg == types.AGG_PERCENTILE {
			a.Params = json.RawMessage(`{"percentile": 100}`)
		}
		aggs = append(aggs, a)
	}

	resp, err := p.Process(context.Background(), &Request{
		Cohort:       &types.Cohort{Filename: "dt.pulse"},
		Aggregations: aggs,
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("Data has %d rows, want 1", len(resp.Data))
	}
	row := resp.Data[0]

	for _, c := range cases {
		raw, ok := row[c.alias]
		if !ok {
			t.Errorf("%s: no %q key in the result row (%v)", c.agg, c.alias, row)
			continue
		}
		got, ok := raw.(float64)
		if !ok {
			t.Errorf("%s: %q = %#v, want a float64", c.agg, c.alias, raw)
			continue
		}
		// Relative tolerance: the epoch-second magnitudes are ~1.7e9
		// and Welford accumulates in float64.
		tol := math.Max(1e-6, math.Abs(c.want)*1e-9)
		if math.Abs(got-c.want) > tol {
			t.Errorf("%s: %q = %v, want %v (+/- %v)", c.agg, c.alias, got, c.want, tol)
		}
	}
}

// TestDateTime_MatchesIntegerColumnAcrossAnalyticsAggregators is the
// exhaustive half: every aggregator whose capability declaration gained
// `datetime` must return EXACTLY what it returns for an integer column
// holding the same epoch-second magnitudes.
//
// Equality rather than hand-computed expectations is deliberate. The
// claim being made by the declaration is not "AGG_KURTOSIS produces
// -1.36 here" — it is "a datetime column reaches the aggregator through
// the same numeric channel as any other integer column, unmodified".
// Byte-equal parity states precisely that, covers all sixteen operators
// without sixteen hand-derived constants, and would break instantly if
// anything on the aggregation path special-cased the temporal type (a
// day truncation, a rescale, a null).
func TestDateTime_MatchesIntegerColumnAcrossAnalyticsAggregators(t *testing.T) {
	stamps := []string{
		"2024-01-01T00:00:00Z",
		"2024-01-01T00:00:10Z",
		"2024-01-01T00:00:20Z",
		"2024-01-01T00:00:31Z", // asymmetric, so skewness is non-zero
	}
	secs := []string{"1704067200", "1704067210", "1704067220", "1704067231"}
	weights := []string{"1", "2", "3", "4"}

	var rows [][]string
	for i := range 40 {
		j := i % len(stamps)
		rows = append(rows, []string{stamps[j], secs[j], weights[j], strconv.Itoa(i % 7)})
	}

	memFs := afero.NewMemMapFs()
	createTestPulseFile(t, memFs, "par.pulse",
		[]string{"seen_at", "as_int", "w", "k"}, rows)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ins, err := p.Inspect(context.Background(), "par.pulse")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	types_ := map[string]string{}
	for _, f := range ins.Fields {
		types_[f.Name] = f.Type
	}
	if types_["seen_at"] != encoding.FieldTypeDateTime.String() {
		t.Fatalf("seen_at inferred as %q, want datetime", types_["seen_at"])
	}
	if types_["as_int"] == encoding.FieldTypeDateTime.String() {
		t.Fatalf("as_int also inferred as datetime — the comparison would be vacuous")
	}

	// Every aggregator whose AcceptsTypes list gained `datetime`.
	cases := []struct {
		agg    types.AggregationType
		params string
	}{
		{types.AGG_SUM, ""},
		{types.AGG_AVERAGE, ""},
		{types.AGG_MIN, ""},
		{types.AGG_MAX, ""},
		{types.AGG_STDDEV, ""},
		{types.AGG_VARIANCE, ""},
		{types.AGG_RANGE, ""},
		{types.AGG_MEDIAN, ""},
		{types.AGG_PERCENTILE, `{"percentile": 75}`},
		{types.AGG_ZSCORE, ""},
		{types.AGG_SKEWNESS, ""},
		{types.AGG_KURTOSIS, ""},
		{types.AGG_DISTINCT_SUM, `{"distinct_by": "k"}`},
		{types.AGG_WEIGHTED_MEAN, `{"weight_field": "w"}`},
		{types.AGG_CI_LOWER, ""},
		{types.AGG_CI_UPPER, ""},
	}

	run := func(field string) map[string]any {
		t.Helper()
		var aggs []*types.Aggregation
		for _, c := range cases {
			a := &types.Aggregation{Type: c.agg, Field: field, Label: string(c.agg)}
			if c.params != "" {
				a.Params = json.RawMessage(c.params)
			}
			aggs = append(aggs, a)
		}
		resp, err := p.Process(context.Background(), &Request{
			Cohort:       &types.Cohort{Filename: "par.pulse"},
			Aggregations: aggs,
		})
		if err != nil {
			t.Fatalf("Process(%s): %v", field, err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("Process(%s): Data has %d rows, want 1", field, len(resp.Data))
		}
		return resp.Data[0]
	}

	gotDT := run("seen_at")
	gotInt := run("as_int")

	for _, c := range cases {
		key := string(c.agg)
		dt, ok := gotDT[key]
		if !ok {
			t.Errorf("%s: missing from the datetime result row", key)
			continue
		}
		iv, ok := gotInt[key]
		if !ok {
			t.Errorf("%s: missing from the integer result row", key)
			continue
		}
		dtf, ok1 := dt.(float64)
		ivf, ok2 := iv.(float64)
		if !ok1 || !ok2 {
			t.Errorf("%s: datetime=%#v integer=%#v — expected float64 on both arms", key, dt, iv)
			continue
		}
		if math.IsNaN(dtf) && math.IsNaN(ivf) {
			t.Errorf("%s: both arms NaN — the comparison proves nothing", key)
			continue
		}
		if dtf != ivf {
			t.Errorf("%s: datetime=%v, integer column with identical values=%v — the aggregation path is not type-transparent",
				key, dtf, ivf)
		}
	}
}
