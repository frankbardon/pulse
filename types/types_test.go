package types_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRequestMarshalJSON verifies JSON round-trip for Request.
func TestRequestMarshalJSON(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{
			Filename: "test.pulse",
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "age", Values: []string{"25", "30"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "score"},
		},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "score", Label: "z_score"},
		},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "region"},
		},
		Outputs: []*types.Output{
			{Format: "json"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal Request: %v", err)
	}

	var got types.Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Request: %v", err)
	}

	// Verify key fields survived round-trip.
	if got.Cohort == nil || got.Cohort.Filename != "test.pulse" {
		t.Errorf("Cohort.Filename = %v, want test.pulse", got.Cohort)
	}
	if len(got.Filterers) != 1 || got.Filterers[0].Type != types.FILTER_INCLUDE {
		t.Errorf("Filterers round-trip failed: %+v", got.Filterers)
	}
	if len(got.Aggregations) != 1 || got.Aggregations[0].Type != types.AGG_AVERAGE {
		t.Errorf("Aggregations round-trip failed: %+v", got.Aggregations)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Type != types.ATTR_ZSCORE {
		t.Errorf("Attributes round-trip failed: %+v", got.Attributes)
	}
	if len(got.Groups) != 1 || got.Groups[0].Type != types.GROUP_CATEGORY {
		t.Errorf("Groups round-trip failed: %+v", got.Groups)
	}
	if len(got.Outputs) != 1 || got.Outputs[0].Format != "json" {
		t.Errorf("Outputs round-trip failed: %+v", got.Outputs)
	}
}

// TestResponseMarshalJSON verifies JSON round-trip for Response.
func TestResponseMarshalJSON(t *testing.T) {
	resp := types.Response{
		Data: []map[string]any{
			{"region": "us", "avg_score": 42.5},
		},
		Metadata: &types.ResponseMetadata{
			TotalRows:    1000,
			FilteredRows: 500,
			CohortFile:   "test.pulse",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal Response: %v", err)
	}

	var got types.Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Response: %v", err)
	}

	if len(got.Data) != 1 {
		t.Fatalf("Data length = %d, want 1", len(got.Data))
	}
	if got.Metadata == nil || got.Metadata.TotalRows != 1000 {
		t.Errorf("Metadata.TotalRows = %v, want 1000", got.Metadata)
	}
	if got.Metadata.FilteredRows != 500 {
		t.Errorf("Metadata.FilteredRows = %d, want 500", got.Metadata.FilteredRows)
	}
}

// TestFiltererEnumValues verifies all 4 filter types are present.
func TestFiltererEnumValues(t *testing.T) {
	expected := []types.FiltererType{
		types.FILTER_INCLUDE,
		types.FILTER_EXCLUDE,
		types.FILTER_RANGE,
		types.FILTER_EXPRESSION,
	}

	for _, ft := range expected {
		if ft == "" {
			t.Errorf("FiltererType should not be empty")
		}
	}

	if len(expected) != 4 {
		t.Errorf("expected 4 filterer types, got %d", len(expected))
	}

	// Verify string round-trip through JSON.
	for _, ft := range expected {
		f := types.Filterer{Type: ft, Field: "x"}
		data, err := json.Marshal(f)
		if err != nil {
			t.Errorf("marshal filterer with type %s: %v", ft, err)
			continue
		}
		var got types.Filterer
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal filterer with type %s: %v", ft, err)
			continue
		}
		if got.Type != ft {
			t.Errorf("FiltererType round-trip: got %s, want %s", got.Type, ft)
		}
	}
}

// TestAggregationEnumValues verifies all 9 aggregation types are present.
func TestAggregationEnumValues(t *testing.T) {
	expected := []types.AggregationType{
		types.AGG_COUNT,
		types.AGG_SUM,
		types.AGG_AVERAGE,
		types.AGG_MIN,
		types.AGG_MAX,
		types.AGG_STDDEV,
		types.AGG_RANGE,
		types.AGG_FREQUENCY,
		types.AGG_ZSCORE,
	}

	if len(expected) != 9 {
		t.Errorf("expected 9 aggregation types, got %d", len(expected))
	}

	// Verify string round-trip through JSON.
	for _, at := range expected {
		a := types.Aggregation{Type: at, Field: "x"}
		data, err := json.Marshal(a)
		if err != nil {
			t.Errorf("marshal aggregation with type %s: %v", at, err)
			continue
		}
		var got types.Aggregation
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal aggregation with type %s: %v", at, err)
			continue
		}
		if got.Type != at {
			t.Errorf("AggregationType round-trip: got %s, want %s", got.Type, at)
		}
	}
}

// TestGroupEnumValues verifies GROUP_CATEGORY and GROUP_ROUNDED are present.
func TestGroupEnumValues(t *testing.T) {
	expected := []types.GroupType{
		types.GROUP_CATEGORY,
		types.GROUP_ROUNDED,
	}

	if len(expected) != 2 {
		t.Errorf("expected 2 group types, got %d", len(expected))
	}

	for _, gt := range expected {
		g := types.Group{Type: gt, Field: "x"}
		data, err := json.Marshal(g)
		if err != nil {
			t.Errorf("marshal group with type %s: %v", gt, err)
			continue
		}
		var got types.Group
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal group with type %s: %v", gt, err)
			continue
		}
		if got.Type != gt {
			t.Errorf("GroupType round-trip: got %s, want %s", got.Type, gt)
		}
	}
}

// TestNoOrbitPrefix verifies no type in the types package references "Orbit".
func TestNoOrbitPrefix(t *testing.T) {
	// Check type names via their string representations.
	typeNames := []string{
		"Request", "Response", "Filterer", "Aggregation",
		"Attribute", "Group", "Output", "Cohort",
		"FileRequest", "FileResponse", "ComposedRequest",
		"VersionResponse", "FiltererType", "AggregationType",
		"GroupType", "AttributeType", "ResponseMetadata",
		"Window", "WindowType", "OrderKey", "FrameSpec",
	}

	for _, name := range typeNames {
		lower := strings.ToLower(name)
		if strings.Contains(lower, "orbit") {
			t.Errorf("type name %q contains orbit reference", name)
		}
	}

	// Check enum value strings for orbit references.
	enumValues := []string{
		string(types.FILTER_INCLUDE), string(types.FILTER_EXCLUDE),
		string(types.FILTER_RANGE), string(types.FILTER_EXPRESSION),
		string(types.AGG_COUNT), string(types.AGG_SUM),
		string(types.AGG_AVERAGE), string(types.AGG_MIN),
		string(types.AGG_MAX), string(types.AGG_STDDEV),
		string(types.AGG_RANGE), string(types.AGG_FREQUENCY),
		string(types.AGG_ZSCORE),
		string(types.GROUP_CATEGORY), string(types.GROUP_ROUNDED),
		string(types.ATTR_ZSCORE), string(types.ATTR_TSCORE),
		string(types.ATTR_NORMALIZED), string(types.ATTR_FORMULA),
		string(types.ATTR_PERCENTILE),
		string(types.WIN_LAG), string(types.WIN_LEAD),
		string(types.WIN_ROW_NUMBER), string(types.WIN_RANK),
		string(types.WIN_DENSE_RANK), string(types.WIN_RUNNING_SUM),
		string(types.WIN_RUNNING_AVG), string(types.WIN_MOVING_AVG),
		string(types.WIN_EWMA), string(types.WIN_PCT_CHANGE),
	}

	for _, v := range enumValues {
		lower := strings.ToLower(v)
		if strings.Contains(lower, "orbit") {
			t.Errorf("enum value %q contains orbit reference", v)
		}
	}
}

// TestComposedRequestMarshalJSON verifies multi-request composition round-trips.
func TestComposedRequestMarshalJSON(t *testing.T) {
	composed := types.ComposedRequest{
		Requests: []*types.Request{
			{
				Cohort: &types.Cohort{Filename: "a.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "id"},
				},
			},
			{
				Cohort: &types.Cohort{Filename: "b.pulse"},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "revenue"},
				},
			},
		},
	}

	data, err := json.Marshal(composed)
	if err != nil {
		t.Fatalf("marshal ComposedRequest: %v", err)
	}

	var got types.ComposedRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ComposedRequest: %v", err)
	}

	if len(got.Requests) != 2 {
		t.Fatalf("Requests length = %d, want 2", len(got.Requests))
	}
	if got.Requests[0].Cohort.Filename != "a.pulse" {
		t.Errorf("first request cohort = %s, want a.pulse", got.Requests[0].Cohort.Filename)
	}
	if got.Requests[1].Aggregations[0].Type != types.AGG_SUM {
		t.Errorf("second request aggregation type = %s, want AGG_SUM", got.Requests[1].Aggregations[0].Type)
	}
}

// TestFileRequestMarshalJSON verifies FileRequest JSON round-trip.
func TestFileRequestMarshalJSON(t *testing.T) {
	req := types.FileRequest{
		Filename: "data.pulse",
		DataDir:  "/var/data",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal FileRequest: %v", err)
	}

	var got types.FileRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal FileRequest: %v", err)
	}

	if got.Filename != "data.pulse" {
		t.Errorf("Filename = %s, want data.pulse", got.Filename)
	}
	if got.DataDir != "/var/data" {
		t.Errorf("DataDir = %s, want /var/data", got.DataDir)
	}
}

// TestFileResponseMarshalJSON verifies FileResponse JSON round-trip.
func TestFileResponseMarshalJSON(t *testing.T) {
	resp := types.FileResponse{
		Filename:   "data.pulse",
		RecordCount: 42000,
		Fields:     []string{"age", "score", "region"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal FileResponse: %v", err)
	}

	var got types.FileResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal FileResponse: %v", err)
	}

	if got.Filename != "data.pulse" {
		t.Errorf("Filename = %s, want data.pulse", got.Filename)
	}
	if got.RecordCount != 42000 {
		t.Errorf("RecordCount = %d, want 42000", got.RecordCount)
	}
	if len(got.Fields) != 3 {
		t.Errorf("Fields length = %d, want 3", len(got.Fields))
	}
}

// TestVersionResponseMarshalJSON verifies VersionResponse JSON round-trip.
func TestVersionResponseMarshalJSON(t *testing.T) {
	resp := types.VersionResponse{
		Version:   "0.1.0",
		BuildDate: "2026-04-26",
		GoVersion: "go1.26.1",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal VersionResponse: %v", err)
	}

	var got types.VersionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal VersionResponse: %v", err)
	}

	if got.Version != "0.1.0" {
		t.Errorf("Version = %s, want 0.1.0", got.Version)
	}
	if got.BuildDate != "2026-04-26" {
		t.Errorf("BuildDate = %s, want 2026-04-26", got.BuildDate)
	}
}

// TestAttributeEnumValues verifies the registered attribute types are present.
// ATTR_RANK was removed; use WIN_RANK instead.
func TestAttributeEnumValues(t *testing.T) {
	expected := []types.AttributeType{
		types.ATTR_ZSCORE,
		types.ATTR_TSCORE,
		types.ATTR_NORMALIZED,
		types.ATTR_FORMULA,
		types.ATTR_PERCENTILE,
		types.ATTR_DATE_PART,
	}

	if len(expected) != 6 {
		t.Errorf("expected 6 attribute types, got %d", len(expected))
	}

	for _, at := range expected {
		a := types.Attribute{Type: at, Field: "x", Label: "y"}
		data, err := json.Marshal(a)
		if err != nil {
			t.Errorf("marshal attribute with type %s: %v", at, err)
			continue
		}
		var got types.Attribute
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal attribute with type %s: %v", at, err)
			continue
		}
		if got.Type != at {
			t.Errorf("AttributeType round-trip: got %s, want %s", got.Type, at)
		}
	}
}

// TestCohortMarshalJSON verifies Cohort JSON round-trip.
func TestCohortMarshalJSON(t *testing.T) {
	c := types.Cohort{
		Filename: "test.pulse",
		DataDir:  "/data",
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal Cohort: %v", err)
	}

	var got types.Cohort
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Cohort: %v", err)
	}

	if got.Filename != "test.pulse" {
		t.Errorf("Filename = %s, want test.pulse", got.Filename)
	}
}

// TestOutputMarshalJSON verifies Output JSON round-trip.
func TestOutputMarshalJSON(t *testing.T) {
	o := types.Output{
		Format:     "json",
		Filename:   "result.json",
		Pretty:     true,
		IncludeNil: false,
	}

	data, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal Output: %v", err)
	}

	var got types.Output
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal Output: %v", err)
	}

	if got.Format != "json" {
		t.Errorf("Format = %s, want json", got.Format)
	}
	if !got.Pretty {
		t.Error("Pretty = false, want true")
	}
}

// TestRequestEmptyFields verifies that a Request with nil slices round-trips.
func TestRequestEmptyFields(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{Filename: "empty.pulse"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Cohort == nil || got.Cohort.Filename != "empty.pulse" {
		t.Errorf("Cohort round-trip failed")
	}
}

// TestFiltererWithValues verifies that Filterer values round-trip correctly.
func TestFiltererWithValues(t *testing.T) {
	f := types.Filterer{
		Type:   types.FILTER_RANGE,
		Field:  "age",
		Values: []string{"18", "65"},
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Filterer
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Values) != 2 || got.Values[0] != "18" || got.Values[1] != "65" {
		t.Errorf("Values = %v, want [18 65]", got.Values)
	}
}

// TestFiltererWithExpression verifies that expression filterers round-trip.
func TestFiltererWithExpression(t *testing.T) {
	f := types.Filterer{
		Type:       types.FILTER_EXPRESSION,
		Expression: "age > 18 && age < 65",
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Filterer
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Expression != "age > 18 && age < 65" {
		t.Errorf("Expression = %s, want age > 18 && age < 65", got.Expression)
	}
}

// TestWindowEnumValues verifies all 10 window types are present and round-trip via JSON.
func TestWindowEnumValues(t *testing.T) {
	expected := []types.WindowType{
		types.WIN_LAG,
		types.WIN_LEAD,
		types.WIN_ROW_NUMBER,
		types.WIN_RANK,
		types.WIN_DENSE_RANK,
		types.WIN_RUNNING_SUM,
		types.WIN_RUNNING_AVG,
		types.WIN_MOVING_AVG,
		types.WIN_EWMA,
		types.WIN_PCT_CHANGE,
	}

	if len(expected) != 10 {
		t.Errorf("expected 10 window types, got %d", len(expected))
	}

	for _, wt := range expected {
		w := types.Window{Type: wt, Field: "x", OrderBy: []types.OrderKey{{Field: "ts"}}}
		data, err := json.Marshal(w)
		if err != nil {
			t.Errorf("marshal window with type %s: %v", wt, err)
			continue
		}
		var got types.Window
		if err := json.Unmarshal(data, &got); err != nil {
			t.Errorf("unmarshal window with type %s: %v", wt, err)
			continue
		}
		if got.Type != wt {
			t.Errorf("WindowType round-trip: got %s, want %s", got.Type, wt)
		}
	}
}

// TestAllWindowTypesAlphabetical verifies AllWindowTypes returns alphabetically sorted entries.
func TestAllWindowTypesAlphabetical(t *testing.T) {
	all := types.AllWindowTypes()
	if len(all) != 10 {
		t.Fatalf("AllWindowTypes returned %d entries, want 10", len(all))
	}
	for i := 1; i < len(all); i++ {
		if string(all[i]) < string(all[i-1]) {
			t.Errorf("AllWindowTypes not sorted: %s before %s", all[i-1], all[i])
		}
	}
}

// TestWindowMarshalJSON verifies a Window with all fields round-trips.
func TestWindowMarshalJSON(t *testing.T) {
	preceding := 3
	following := 0
	w := types.Window{
		Type:        types.WIN_MOVING_AVG,
		Field:       "revenue",
		Label:       "ma_3",
		PartitionBy: []string{"region"},
		OrderBy:     []types.OrderKey{{Field: "ts"}, {Field: "id", Desc: true}},
		Frame: &types.FrameSpec{
			Mode:      "rows",
			Preceding: &preceding,
			Following: &following,
		},
		Params: json.RawMessage(`{"alpha": 0.5}`),
	}

	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Window
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != types.WIN_MOVING_AVG {
		t.Errorf("Type = %s, want WIN_MOVING_AVG", got.Type)
	}
	if got.Label != "ma_3" {
		t.Errorf("Label = %s, want ma_3", got.Label)
	}
	if len(got.PartitionBy) != 1 || got.PartitionBy[0] != "region" {
		t.Errorf("PartitionBy = %v, want [region]", got.PartitionBy)
	}
	if len(got.OrderBy) != 2 || got.OrderBy[0].Field != "ts" || !got.OrderBy[1].Desc {
		t.Errorf("OrderBy round-trip failed: %+v", got.OrderBy)
	}
	if got.Frame == nil || got.Frame.Mode != "rows" {
		t.Fatalf("Frame round-trip failed: %+v", got.Frame)
	}
	if got.Frame.Preceding == nil || *got.Frame.Preceding != 3 {
		t.Errorf("Frame.Preceding = %v, want 3", got.Frame.Preceding)
	}
	if got.Frame.Following == nil || *got.Frame.Following != 0 {
		t.Errorf("Frame.Following = %v, want 0", got.Frame.Following)
	}
}

// TestRequestWithWindowsRoundTrip verifies Request.Windows round-trips via JSON.
func TestRequestWithWindowsRoundTrip(t *testing.T) {
	req := types.Request{
		Cohort: &types.Cohort{Filename: "ts.pulse"},
		Windows: []*types.Window{
			{
				Type:    types.WIN_LAG,
				Field:   "revenue",
				OrderBy: []types.OrderKey{{Field: "date"}},
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Windows) != 1 || got.Windows[0].Type != types.WIN_LAG {
		t.Errorf("Windows round-trip failed: %+v", got.Windows)
	}
	if got.Windows[0].OrderBy[0].Field != "date" {
		t.Errorf("OrderBy field = %s, want date", got.Windows[0].OrderBy[0].Field)
	}
}

// TestGroupWithRoundedInterval verifies that rounded groups carry interval.
func TestGroupWithRoundedInterval(t *testing.T) {
	g := types.Group{
		Type:     types.GROUP_ROUNDED,
		Field:    "age",
		Interval: 10,
	}

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got types.Group
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Interval != 10 {
		t.Errorf("Interval = %f, want 10", got.Interval)
	}
}
