package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// buildCategoricalCohort writes a small fixture with a categorical
// "region" column and a numeric "score" column. The dictionary holds
// 4 distinct region values; total rows = 12.
func buildCategoricalCohort(t *testing.T) (*Service, string) {
	t.Helper()
	memFs := afero.NewMemMapFs()

	dict := encoding.NewDictionary()
	regions := []string{"north", "south", "east", "west"}
	for _, v := range regions {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}

	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)

	rows := []struct {
		region uint8
		score  float64
	}{
		{0, 10}, {0, 20}, {0, 30}, // north x3
		{1, 15}, {1, 25}, // south x2
		{2, 40}, {2, 50}, {2, 60}, {2, 70}, // east x4
		{3, 80}, {3, 90}, {3, 100}, // west x3
	}
	for _, r := range rows {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(r.region))
		encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(r.score))
	}

	afero.WriteFile(memFs, "facet.pulse", buf.Bytes(), 0644)
	cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	return New(cfg), "facet.pulse"
}

func TestFacetSchema_DiscreteAndNumeric(t *testing.T) {
	svc, path := buildCategoricalCohort(t)

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region", "score"},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if resp.TotalRecords != 12 || resp.FilteredRecords != 12 {
		t.Fatalf("totals = (%d, %d), want (12, 12)", resp.TotalRecords, resp.FilteredRecords)
	}
	region := resp.Fields["region"]
	if region == nil || region.Kind != "discrete" {
		t.Fatalf("region: kind=%q", region.Kind)
	}
	if region.Discrete.DistinctCount != 4 {
		t.Fatalf("region distinct = %d, want 4", region.Discrete.DistinctCount)
	}
	if len(region.Discrete.Values) != 4 {
		t.Fatalf("region values len = %d, want 4", len(region.Discrete.Values))
	}
	// First entry must have the largest count (4) and the value "east".
	if region.Discrete.Values[0].Value != "east" || region.Discrete.Values[0].Count != 4 {
		t.Fatalf("region top = %+v, want east/4", region.Discrete.Values[0])
	}

	score := resp.Fields["score"]
	if score == nil || score.Kind != "numeric" {
		t.Fatalf("score: kind=%q", score.Kind)
	}
	if score.Numeric.Count != 12 {
		t.Fatalf("score count = %d, want 12", score.Numeric.Count)
	}
	if score.Numeric.Min != 10 || score.Numeric.Max != 100 {
		t.Fatalf("score min/max = %v/%v, want 10/100", score.Numeric.Min, score.Numeric.Max)
	}
}

func TestFacetSchema_ValidationUnknownField(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"missing"},
	}
	if _, err := svc.FacetSchema(context.Background(), req); err == nil {
		t.Fatal("expected SERVICE_VALIDATION error for unknown field")
	}
}

func TestFacetSchema_ValidationEmptyFields(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{Cohort: &types.Cohort{Filename: path}}
	if _, err := svc.FacetSchema(context.Background(), req); err == nil {
		t.Fatal("expected SERVICE_VALIDATION error for empty fields slice")
	}
}

func TestFacetSchema_DiscreteTopKTruncation(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort:       &types.Cohort{Filename: path},
		Fields:       []string{"region"},
		DiscreteTopK: 2,
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	region := resp.Fields["region"]
	if len(region.Discrete.Values) != 2 {
		t.Fatalf("len values = %d, want 2", len(region.Discrete.Values))
	}
	if region.Discrete.TruncatedAt != 2 {
		t.Fatalf("TruncatedAt = %d, want 2", region.Discrete.TruncatedAt)
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("expected truncation warning")
	}
}

func TestFacetSchema_NumericPercentiles(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort:             &types.Cohort{Filename: path},
		Fields:             []string{"score"},
		NumericPercentiles: []float64{0.5, 0.95},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	score := resp.Fields["score"]
	if len(score.Numeric.Percentiles) != 2 {
		t.Fatalf("percentiles len = %d, want 2", len(score.Numeric.Percentiles))
	}
	// Sorted values: 10,15,20,25,30,40,50,60,70,80,90,100 (n=12).
	// Linear-interp p50 at rank 0.5 * (n-1) = 5.5 => avg(values[5], values[6]) = 45.
	p50, ok := score.Numeric.Percentiles["p50"]
	if !ok {
		t.Fatalf("missing p50 key (have %v)", score.Numeric.Percentiles)
	}
	if math.Abs(p50-45.0) > 1e-6 {
		t.Fatalf("p50 = %v, want 45", p50)
	}
}

func TestFacetSchema_NumericHistogram(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort:           &types.Cohort{Filename: path},
		Fields:           []string{"score"},
		IncludeHistogram: true,
		HistogramBins:    10,
		HistogramRange:   [2]float64{0, 100},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	hist := resp.Fields["score"].Numeric.Histogram
	if hist == nil {
		t.Fatal("histogram nil")
	}
	if len(hist.Bins) != 10 {
		t.Fatalf("bins len = %d, want 10", len(hist.Bins))
	}
	var sum int64
	for _, b := range hist.Bins {
		sum += b
	}
	if sum != resp.Fields["score"].Numeric.Count {
		t.Fatalf("histogram bin sum %d != non-null count %d", sum, resp.Fields["score"].Numeric.Count)
	}
}

func TestFacetSchema_AdditiveStripsScopedField(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	// Filter on region=north only; without additive, region.east should
	// not appear. Additive on region strips that clause so all four
	// regions reappear.
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}},
		},
		AdditiveFields: []string{"region"},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if resp.FilteredRecords != 3 {
		t.Fatalf("filtered = %d, want 3", resp.FilteredRecords)
	}
	if len(resp.Fields["region"].Discrete.Values) != 1 {
		t.Fatalf("base region values = %d, want 1 (north only)", len(resp.Fields["region"].Discrete.Values))
	}
	add := resp.Additive["region"]
	if add == nil {
		t.Fatal("missing additive[region]")
	}
	if add.Discrete.DistinctCount != 4 {
		t.Fatalf("additive distinct = %d, want 4", add.Discrete.DistinctCount)
	}
}

func TestFacetSchema_AdditiveDifferentFromBase(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}},
		},
		AdditiveFields: []string{"region"},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	baseTotal := int64(0)
	for _, v := range resp.Fields["region"].Discrete.Values {
		baseTotal += v.Count
	}
	addTotal := int64(0)
	for _, v := range resp.Additive["region"].Discrete.Values {
		addTotal += v.Count
	}
	if !(addTotal > baseTotal) {
		t.Fatalf("additive total %d not > base total %d", addTotal, baseTotal)
	}
}

func TestFacetSchema_CategoricalFastPathDictionaryStrings(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region"},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	for _, v := range resp.Fields["region"].Discrete.Values {
		switch v.Value {
		case "north", "south", "east", "west":
		default:
			t.Fatalf("expected dictionary-resolved value, got %q", v.Value)
		}
	}
}

func TestFacetSchema_AdditiveExpressionFilterReferenceRejected(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_EXPRESSION, Expression: "region == \"north\""},
		},
		AdditiveFields: []string{"region"},
	}
	if _, err := svc.FacetSchema(context.Background(), req); err == nil {
		t.Fatal("expected SERVICE_VALIDATION error when additive field appears in FILTER_EXPRESSION")
	}
}

func TestFacetSchema_FilterIncludeReducesCounts(t *testing.T) {
	svc, path := buildCategoricalCohort(t)
	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"east"}},
		},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if resp.FilteredRecords != 4 {
		t.Fatalf("filtered = %d, want 4", resp.FilteredRecords)
	}
	if len(resp.Fields["region"].Discrete.Values) != 1 {
		t.Fatalf("region values len = %d, want 1", len(resp.Fields["region"].Discrete.Values))
	}
}
