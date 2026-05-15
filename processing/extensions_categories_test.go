package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// ---------- Filterer round-trip --------------------------------------

// keepHighFilter implements FiltererBuilder + FilterFunc — keeps rows
// whose "score" field is > 5.
type keepHighFilter struct{}

func (keepHighFilter) Build(*types.Filterer, *encoding.Schema) (FilterFunc, error) {
	return func(r *Record) (bool, error) {
		v, _ := r.NumericValue("score")
		return v > 5, nil
	}, nil
}

func keepHighFilterFactory() FiltererBuilder { return keepHighFilter{} }

func TestExtensions_FiltererRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 3}),
		NewRecord(schema, map[string]float64{"score": 7}),
		NewRecord(schema, map[string]float64{"score": 9}),
	}
	exts := &ExtensionRegistry{
		Filterers: map[types.FiltererType]FiltererFactory{
			"FILTER_ACME_HIGH": keepHighFilterFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Filterers: []*types.Filterer{
			{Type: "FILTER_ACME_HIGH"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["n"].(float64); got != 2 {
		t.Errorf("filtered count = %v, want 2", got)
	}
}

// ---------- Grouper round-trip ---------------------------------------

// parityGrouper buckets records into "even" / "odd" by floor(score) %
// 2. Implements Grouper + StreamingGrouper so it works in both
// execution paths.
type parityGrouper struct{}

func (parityGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	out := map[string][]*Record{}
	for _, r := range records {
		key, ok, _ := parityGrouper{}.KeyForRow(r, field)
		if !ok {
			continue
		}
		out[key] = append(out[key], r)
	}
	return out, nil
}

func (parityGrouper) KeyForRow(r *Record, field string) (string, bool, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return "", false, nil
	}
	if int(v)%2 == 0 {
		return "even", true, nil
	}
	return "odd", true, nil
}

func parityGrouperFactory(*types.Group, *encoding.Schema) (Grouper, error) {
	return parityGrouper{}, nil
}

func TestExtensions_GrouperRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 2}),
		NewRecord(schema, map[string]float64{"score": 4}),
		NewRecord(schema, map[string]float64{"score": 3}),
	}
	exts := &ExtensionRegistry{
		Groupers: map[types.GroupType]GrouperFactory{
			"GROUP_ACME_PARITY": parityGrouperFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Groups: []*types.Group{
			{Type: "GROUP_ACME_PARITY", Field: "score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 groups (even/odd), got %d: %v", len(resp.Data), resp.Data)
	}
	seen := map[string]float64{}
	for _, row := range resp.Data {
		seen[row["score"].(string)] = row["n"].(float64)
	}
	if seen["even"] != 2 || seen["odd"] != 1 {
		t.Errorf("group counts = %v; want even=2 odd=1", seen)
	}
}

// ---------- Window round-trip ----------------------------------------

// constComputer writes a constant 7 into the labeled column for every
// row, ignoring partitions.
type constComputer struct{}

func (constComputer) Compute(rows []map[string]any, partitions [][]int, label string) error {
	_ = partitions
	for _, r := range rows {
		r[label] = 7.0
	}
	return nil
}

func constWindowFactory(*types.Window, window.WindowOptions) (window.WindowComputer, error) {
	return constComputer{}, nil
}

func TestExtensions_WindowRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 1}),
		NewRecord(schema, map[string]float64{"score": 2}),
	}
	exts := &ExtensionRegistry{
		Windows: map[types.WindowType]window.WindowFactory{
			"WIN_ACME_CONST": constWindowFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
		},
		Windows: []*types.Window{
			{Type: "WIN_ACME_CONST", Field: "score", Label: "k"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["k"]; got != 7.0 {
		t.Errorf("window output = %v, want 7.0", got)
	}
}

// ---------- Feature round-trip ---------------------------------------

// doubleFeature emits "double" = 2 * score for every record. Buffered-
// only (no StreamingComputer) so feature.Apply runs over the materialised
// record set.
type doubleFeature struct{}

func (doubleFeature) Compute(records []feature.Record, field string) (map[string]feature.Output, error) {
	values := make([]float64, len(records))
	for i, r := range records {
		v, _ := r.NumericValue(field)
		values[i] = v * 2
	}
	return map[string]feature.Output{"double": {Values: values}}, nil
}

func doubleFeatureFactory(*types.Feature, *encoding.Schema) (feature.Computer, error) {
	return doubleFeature{}, nil
}

func TestExtensions_FeatureRoundTrip(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 2}),
		NewRecord(schema, map[string]float64{"score": 3}),
	}
	exts := &ExtensionRegistry{
		Features: map[types.FeatureType]feature.Factory{
			"FEAT_ACME_DOUBLE": doubleFeatureFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Features: []*types.Feature{
			{Type: "FEAT_ACME_DOUBLE", Field: "score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "double", Label: "doubled_sum"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["doubled_sum"].(float64); got != (2+3)*2.0 {
		t.Errorf("doubled_sum = %v, want %v", got, (2+3)*2.0)
	}
}

// ---------- Test (tier-1) round-trip ---------------------------------

// constRowTest emits a sentinel TestResult after consuming every row.
type constRowTest struct {
	count int
}

func (c *constRowTest) UpdateRow(*Record) error { c.count++; return nil }
func (c *constRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Type: "TEST_ACME_CONST", Statistic: 1.23, PValue: 0.5}, nil
}

func constRowTestFactory(*types.Test, *encoding.Schema) (RowTest, error) {
	return &constRowTest{}, nil
}

func TestExtensions_TestRoundTrip_Tier1(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 1}),
		NewRecord(schema, map[string]float64{"score": 2}),
	}
	exts := &ExtensionRegistry{
		RowTests: map[types.TestType]RowTestFactory{
			"TEST_ACME_CONST": constRowTestFactory,
		},
		Streamable: map[string]bool{
			StreamabilityKey("test", "TEST_ACME_CONST"): true,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		Tests: []*types.Test{
			{Type: "TEST_ACME_CONST", Field: "score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("expected 1 test result, got %d", len(resp.Tests))
	}
	if resp.Tests[0].Statistic != 1.23 {
		t.Errorf("custom test statistic = %v, want 1.23", resp.Tests[0].Statistic)
	}
}

// ---------- Test (tier-2) round-trip ---------------------------------

// constPostTest emits a sentinel after running over the materialised
// post-window row set.
type constPostTest struct{}

func (constPostTest) Run(rows []map[string]any) (*types.TestResult, error) {
	return &types.TestResult{Type: "TEST_ACME_POST", Statistic: 4.56, PValue: 0.01}, nil
}

func constPostTestFactory(*types.Test, *encoding.Schema) (PostTest, error) {
	return constPostTest{}, nil
}

func TestExtensions_TestRoundTrip_Tier2(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	records := []*Record{
		NewRecord(schema, map[string]float64{"score": 1}),
		NewRecord(schema, map[string]float64{"score": 2}),
	}
	exts := &ExtensionRegistry{
		PostTests: map[types.TestType]PostTestFactory{
			"TEST_ACME_POST": constPostTestFactory,
		},
	}
	proc := NewProcessorWithExtensions(schema, exts)
	req := &types.Request{
		PostTests: []*types.Test{
			{Type: "TEST_ACME_POST", Field: "score"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
		},
	}
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.PostTests) != 1 {
		t.Fatalf("expected 1 post-test result, got %d", len(resp.PostTests))
	}
	if resp.PostTests[0].Statistic != 4.56 {
		t.Errorf("custom post-test statistic = %v, want 4.56", resp.PostTests[0].Statistic)
	}
}
