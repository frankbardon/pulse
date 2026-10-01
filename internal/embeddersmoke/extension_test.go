package embeddersmoke

// The extension-author flow: an operator written against the public
// extend package, registered through pulse.Options.Extensions and run
// end to end through Process. This file is held to the strict
// extension-author import set (see extensionAuthorImports in
// imports_test.go) — pulse, extend, types, encoding, errors and stdlib.

import (
	"context"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// smokeSum is AGG_SUM authored against extend: it reads rows only
// through extend.Record and implements both the buffered and the
// online contract.
type smokeSum struct {
	sum     float64
	updates *int
}

var (
	_ extend.Aggregator        = (*smokeSum)(nil)
	_ extend.OnlineAggregator  = (*smokeSum)(nil)
	_ extend.AggregatorFactory = newSmokeSum(nil)
)

func newSmokeSum(updates *int) extend.AggregatorFactory {
	return func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
		return &smokeSum{updates: updates}, nil
	}
}

func (a *smokeSum) Aggregate(rows extend.Rows, field string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(field); ok {
			a.sum += v
		}
	}
	return a.sum, nil
}

func (a *smokeSum) UpdateRow(rec extend.Record, field string) error {
	if a.updates != nil {
		*a.updates++
	}
	if v, ok := rec.NumericValue(field); ok {
		a.sum += v
	}
	return nil
}

func (a *smokeSum) Finalize() (float64, error) { return a.sum, nil }

func TestExtendAggregatorThroughProcess(t *testing.T) {
	var updates int
	p, fs := newEngine(t, pulse.Options{Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_SMOKE_SUM",
			Description: "AGG_SUM authored against the public extend package.",
			Factory:     newSmokeSum(&updates),
			Streamable:  true,
		}},
	}})
	ingest(t, p, fs, "sales.pulse")
	ctx := context.Background()

	builtin, err := p.Process(ctx, sumByRegion("sales.pulse"))
	if err != nil {
		t.Fatalf("Process(AGG_SUM): %v", err)
	}
	req := sumByRegion("sales.pulse")
	req.Aggregations[0].Type = "AGG_SMOKE_SUM"
	ext, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process(AGG_SMOKE_SUM): %v", err)
	}

	want, got := totals(t, builtin), totals(t, ext)
	if len(want) != 2 || want["north"] != 15 || want["south"] != 20 {
		t.Fatalf("built-in totals = %v, want north=15 south=20", want)
	}
	if len(got) != len(want) {
		t.Fatalf("extension totals = %v, built-in %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("extension total[%s] = %v, built-in %v", k, got[k], v)
		}
	}
	if updates != 3 {
		t.Errorf("UpdateRow calls = %d, want 3 (the streamable registration must take the online path)", updates)
	}
}

// The remaining categories authored against extend alone: an
// attribute (two-pass tier), a streaming feature, both test tiers and
// a window. The compile-time assertions prove the public spellings
// suffice; the test proves pulse.New accepts them and Process runs
// the attribute and feature end to end.

type smokeDouble struct{}

func (smokeDouble) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		v, _ := rows.At(i).NumericValue(field)
		out[i] = 2 * v
	}
	return out, nil
}
func (smokeDouble) Row(rec extend.Record, field string) (float64, error) {
	v, _ := rec.NumericValue(field)
	return 2 * v, nil
}
func (smokeDouble) PrePass(extend.Record, string) error { return nil }
func (smokeDouble) Finalize() error                     { return nil }

type smokeFeature struct{}

func (smokeFeature) Compute(rows extend.Rows, field string) (map[string]extend.FeatureOutput, error) {
	vals, _ := smokeDouble{}.Compute(rows, field)
	return map[string]extend.FeatureOutput{"amount_x2": {Values: vals}}, nil
}
func (smokeFeature) PrePass(extend.Record, string) error { return nil }
func (smokeFeature) Finalize() error                     { return nil }
func (smokeFeature) EmitRow(rec extend.Record, field string) (map[string]extend.FeatureOutput, error) {
	v, _ := smokeDouble{}.Row(rec, field)
	return map[string]extend.FeatureOutput{"amount_x2": {Values: []float64{v}}}, nil
}

type smokeRowTest struct{ n float64 }

func (s *smokeRowTest) UpdateRow(extend.Record) error { s.n++; return nil }
func (s *smokeRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: s.n, PValue: 1}, nil
}

type smokePostTest struct{}

func (smokePostTest) Run(rows []map[string]any) (*types.TestResult, error) {
	return &types.TestResult{Statistic: float64(len(rows)), PValue: 1}, nil
}

type smokeWindow struct{}

func (smokeWindow) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for pos, i := range part {
			rows[i][label] = float64(pos)
		}
	}
	return nil
}

var (
	_ extend.TwoPassAttribute         = smokeDouble{}
	_ extend.StreamingFeatureComputer = smokeFeature{}
	_ extend.FeatureComputer          = smokeFeature{}
	_ extend.RowTest                  = (*smokeRowTest)(nil)
	_ extend.PostTest                 = smokePostTest{}
	_ extend.WindowComputer           = smokeWindow{}
)

func TestExtendOtherCategoriesThroughProcess(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{Extensions: pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name: "ATTR_SMOKE_DOUBLE", Mode: pulse.AttributeModeTwoPass,
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return smokeDouble{}, nil
			},
		}},
		Features: []pulse.FeatureRegistration{{
			Name: "FEAT_SMOKE_DOUBLE", Streamable: true,
			Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) {
				return smokeFeature{}, nil
			},
		}},
		Tests: []pulse.TestRegistration{
			{Name: "TEST_SMOKE_ROWS", Tier: pulse.TestTierRow,
				RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return &smokeRowTest{}, nil }},
			{Name: "TEST_SMOKE_RESULT_ROWS", Tier: pulse.TestTierPost,
				PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) { return smokePostTest{}, nil }},
		},
		Windows: []pulse.WindowRegistration{{
			Name: "WIN_SMOKE_ORDINAL",
			Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) {
				return smokeWindow{}, nil
			},
		}},
	}})
	ingest(t, p, fs, "sales.pulse")
	req := sumByRegion("sales.pulse")
	req.Attributes = []*types.Attribute{{Type: "ATTR_SMOKE_DOUBLE", Field: "amount", Label: "amount_attr_x2"}}
	req.Features = []*types.Feature{{Type: "FEAT_SMOKE_DOUBLE", Field: "amount", Label: "amount_x2"}}
	req.Aggregations = append(req.Aggregations,
		&types.Aggregation{Type: types.AGG_SUM, Field: "amount_attr_x2", Label: "attr_total"},
		&types.Aggregation{Type: types.AGG_SUM, Field: "amount_x2", Label: "feat_total"})
	req.Tests = []*types.Test{{Type: "TEST_SMOKE_ROWS", Field: "amount", Label: "rows"}}
	req.PostTests = []*types.Test{{Type: "TEST_SMOKE_RESULT_ROWS", Field: "total", Label: "result_rows"}}
	req.Windows = []*types.Window{{Type: "WIN_SMOKE_ORDINAL", Field: "total", Label: "ord",
		OrderBy: []types.OrderKey{{Field: "total"}}}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("rows = %d, want 2", len(resp.Data))
	}
	for _, row := range resp.Data {
		total, ok := row["total"].(float64)
		if !ok || total <= 0 {
			t.Fatalf("row %v: total is not a positive float64", row)
		}
		if row["attr_total"] != 2*total || row["feat_total"] != 2*total {
			t.Errorf("row %v: attr_total / feat_total should be 2 * total", row)
		}
		if _, ok := row["ord"].(float64); !ok {
			t.Errorf("row %v missing window column ord", row)
		}
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Statistic != 3 {
		t.Errorf("tier-1 tests = %+v, want statistic 3 (rows)", resp.Tests)
	}
	if len(resp.PostTests) != 1 || resp.PostTests[0].Statistic != 2 {
		t.Errorf("tier-2 tests = %+v, want statistic 2 (result rows)", resp.PostTests)
	}
}

// Groupers (single-key streaming and fan-out) and a filterer authored
// against extend alone.

type smokeRegionGrouper struct{ all bool }

func (g smokeRegionGrouper) keys(rec extend.Record, field string) []string {
	v, ok := rec.StringValue(field)
	if !ok {
		return nil
	}
	if g.all {
		return []string{v, "all"}
	}
	return []string{v}
}

func (g smokeRegionGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	out := map[string][]int{}
	for i := 0; i < rows.Len(); i++ {
		for _, k := range g.keys(rows.At(i), field) {
			out[k] = append(out[k], i)
		}
	}
	return out, nil
}

// smokeKeyGrouper streams one key per row.
type smokeKeyGrouper struct{ smokeRegionGrouper }

func (g smokeKeyGrouper) KeyForRow(rec extend.Record, field string) (string, bool, error) {
	ks := g.keys(rec, field)
	if len(ks) == 0 {
		return "", false, extend.ErrGrouperKeyNull
	}
	return ks[0], true, nil
}

// smokeFanOutGrouper streams every row into its region AND "all".
type smokeFanOutGrouper struct{ smokeRegionGrouper }

func (g smokeFanOutGrouper) KeysForRow(rec extend.Record, field string) ([]string, bool, error) {
	ks := g.keys(rec, field)
	return ks, len(ks) > 0, nil
}

// smokeMinFilter keeps rows whose field is at least Values[0].
type smokeMinFilter struct{}

func (smokeMinFilter) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	lo, err := strconv.ParseFloat(spec.Values[0], 64)
	if err != nil {
		return nil, err
	}
	return func(rec extend.Record) (bool, error) {
		v, ok := rec.NumericValue(spec.Field)
		return ok && v >= lo, nil
	}, nil
}

var (
	_ extend.StreamingGrouper         = smokeKeyGrouper{}
	_ extend.MultiKeyStreamingGrouper = smokeFanOutGrouper{}
	_ extend.FiltererBuilder          = smokeMinFilter{}
)

func TestExtendGroupersAndFiltererThroughProcess(t *testing.T) {
	p, fs := newEngine(t, pulse.Options{Extensions: pulse.Extensions{
		Groupers: []pulse.GrouperRegistration{
			{Name: "GROUP_SMOKE_REGION", Streamable: true,
				Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) { return smokeKeyGrouper{}, nil }},
			{Name: "GROUP_SMOKE_REGION_ALL", Streamable: true, FansOut: true,
				Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
					return smokeFanOutGrouper{smokeRegionGrouper{all: true}}, nil
				}},
		},
		Filterers: []pulse.FiltererRegistration{{
			Name:    "FILTER_SMOKE_MIN",
			Factory: func() extend.FiltererBuilder { return smokeMinFilter{} },
		}},
	}})
	ingest(t, p, fs, "sales.pulse")
	ctx := context.Background()
	run := func(g types.GroupType, filters ...*types.Filterer) map[string]float64 {
		t.Helper()
		req := sumByRegion("sales.pulse")
		req.Groups[0].Type = g
		req.Filterers = filters
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process(%s): %v", g, err)
		}
		return totals(t, resp)
	}
	check := func(name string, got, want map[string]float64) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: totals = %v, want %v", name, got, want)
			return
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: total[%s] = %v, want %v", name, k, got[k], v)
			}
		}
	}
	check("GROUP_CATEGORY", run(types.GROUP_CATEGORY), map[string]float64{"north": 15, "south": 20})
	check("GROUP_SMOKE_REGION", run("GROUP_SMOKE_REGION"), map[string]float64{"north": 15, "south": 20})
	check("GROUP_SMOKE_REGION_ALL", run("GROUP_SMOKE_REGION_ALL"), map[string]float64{"north": 15, "south": 20, "all": 35})
	check("FILTER_SMOKE_MIN", run("GROUP_SMOKE_REGION",
		&types.Filterer{Type: "FILTER_SMOKE_MIN", Field: "amount", Values: []string{"10"}}),
		map[string]float64{"north": 10, "south": 20})
}
