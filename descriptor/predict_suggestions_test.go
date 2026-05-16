package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// findSuggestionByPath returns the first suggestion whose Path matches
// the dotted-form path; nil when none match. Lets table-driven tests
// avoid index-based assertions that break when ordering shifts.
func findSuggestionByPath(t *testing.T, ss []Suggestion, dotted string) *Suggestion {
	t.Helper()
	for i := range ss {
		if strings.Join(ss[i].Path, ".") == dotted {
			return &ss[i]
		}
	}
	return nil
}

// runPredictForSuggestions builds a minimal .pulse file and returns the
// suggestion slice. Shared driver for the suggestion-focused tests.
func runPredictForSuggestions(t *testing.T, schema *encoding.Schema, req *types.Request) []Suggestion {
	t.Helper()
	data := buildTestPulseFile(t, schema)
	env := PredictFromBytes(data, req, nil)
	result, ok := env.Data.(*PredictResult)
	if !ok {
		t.Fatal("envelope data is not *PredictResult")
	}
	return result.Suggestions
}

// ----- Source 1: field name typos ---------------------------------------

func TestPredict_Suggestions_TypoMatchesNearestField(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Total revenue in USD per row"},
			{Name: "age", Type: encoding.FieldTypeU8, Description: "Subject age in years at observation"},
		},
	}

	cases := []struct {
		name       string
		field      string
		wantTop    string
		wantConf   float64
		wantInList []string // optional: other candidates must be present
	}{
		{name: "edit_distance_1", field: "revenu", wantTop: "revenue", wantConf: 0.9},
		{name: "transposition_distance_2", field: "revneue", wantTop: "revenue", wantConf: 0.7},
		{name: "insertion_distance_1", field: "revenuee", wantTop: "revenue", wantConf: 0.9},
		{name: "deletion_distance_1", field: "revnue", wantTop: "revenue", wantConf: 0.9},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: c.field}},
			}
			ss := runPredictForSuggestions(t, schema, req)
			s := findSuggestionByPath(t, ss, "Aggregations.0.Field")
			if s == nil {
				t.Fatalf("expected a typo suggestion for %s; got none in %+v", c.field, ss)
			}
			if len(s.Proposed) == 0 {
				t.Fatalf("expected proposed candidates; got empty")
			}
			if s.Proposed[0] != c.wantTop {
				t.Errorf("Proposed[0] = %v, want %s", s.Proposed[0], c.wantTop)
			}
			if s.Confidence != c.wantConf {
				t.Errorf("Confidence = %v, want %v", s.Confidence, c.wantConf)
			}
			if s.Current != c.field {
				t.Errorf("Current = %v, want %s", s.Current, c.field)
			}
		})
	}
}

func TestPredict_Suggestions_NoTypoWhenFieldExists(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Total revenue in USD per row"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "revenue"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	if s := findSuggestionByPath(t, ss, "Aggregations.0.Field"); s != nil {
		t.Errorf("did not expect typo suggestion on valid field; got %+v", s)
	}
}

// ----- Source 2: operator/type mismatches -------------------------------

func TestPredict_Suggestions_NumericAggOnCategorical(t *testing.T) {
	dict := makeDictionary(t, "A", "B", "C")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: dict},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "grade"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Aggregations.0.Type")
	if s == nil {
		t.Fatalf("expected operator-type suggestion; got %+v", ss)
	}
	want := []string{"AGG_DISTINCT_COUNT", "AGG_FREQUENCY", "AGG_MODE"}
	if len(s.Proposed) != len(want) {
		t.Fatalf("Proposed count = %d, want %d", len(s.Proposed), len(want))
	}
	for i, w := range want {
		if s.Proposed[i] != w {
			t.Errorf("Proposed[%d] = %v, want %s", i, s.Proposed[i], w)
		}
	}
	if s.Confidence != 0.6 {
		t.Errorf("Confidence = %v, want 0.6", s.Confidence)
	}
	if s.Current != "AGG_SUM" {
		t.Errorf("Current = %v, want AGG_SUM", s.Current)
	}
}

func TestPredict_Suggestions_DecimalAggMismatch(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Description: "Transaction amount in USD"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "amount"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Aggregations.0.Type")
	if s == nil {
		t.Fatalf("expected decimal-mismatch suggestion; got %+v", ss)
	}
	if len(s.Proposed) == 0 {
		t.Fatalf("expected proposed candidates")
	}
	// AGG_AVERAGE should be in the proposed list (decimal-supported).
	found := false
	for _, p := range s.Proposed {
		if p == "AGG_AVERAGE" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected AGG_AVERAGE in proposed list; got %v", s.Proposed)
	}
}


// ----- Source 3: date misuse ---------------------------------------------

func TestPredict_Suggestions_GroupCategoryOnDate(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "ts", Type: encoding.FieldTypeDate, Description: "Event timestamp as epoch days"},
		},
	}
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "ts"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Groups.0.Type")
	if s == nil {
		t.Fatalf("expected GROUP_DATE suggestion; got %+v", ss)
	}
	if len(s.Proposed) != 1 || s.Proposed[0] != "GROUP_DATE" {
		t.Errorf("Proposed = %v, want [GROUP_DATE]", s.Proposed)
	}
	if s.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", s.Confidence)
	}
}

// ----- Source 4: missing required params --------------------------------

func TestPredict_Suggestions_WindowMissingOrderBy(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "ts", Type: encoding.FieldTypeDate, Description: "Event timestamp as epoch days"},
			{Name: "qty", Type: encoding.FieldTypeF64, Description: "Quantity sold per event"},
		},
	}
	req := &types.Request{
		Windows: []*types.Window{{Type: types.WIN_LAG, Field: "qty"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Windows.0.OrderBy")
	if s == nil {
		t.Fatalf("expected missing-OrderBy suggestion; got %+v", ss)
	}
	if len(s.Proposed) == 0 {
		t.Fatalf("expected proposed OrderBy candidates; got empty")
	}
	// All numeric/date fields should appear (alphabetical).
	want := []any{"qty", "ts"}
	if len(s.Proposed) != len(want) {
		t.Fatalf("Proposed count = %d, want %d", len(s.Proposed), len(want))
	}
	for i, w := range want {
		if s.Proposed[i] != w {
			t.Errorf("Proposed[%d] = %v, want %v", i, s.Proposed[i], w)
		}
	}
	if s.Confidence != 0.5 {
		t.Errorf("Confidence = %v, want 0.5", s.Confidence)
	}
}

func TestPredict_Suggestions_PercentileMissingP(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_PERCENTILE, Field: "score"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Aggregations.0.Params.percentile")
	if s == nil {
		t.Fatalf("expected missing-percentile suggestion; got %+v", ss)
	}
	if len(s.Proposed) == 0 {
		t.Errorf("expected common percentile values in Proposed")
	}
	if s.Confidence != 0.5 {
		t.Errorf("Confidence = %v, want 0.5", s.Confidence)
	}
}

func TestPredict_Suggestions_PercentileWithParamSilent(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_PERCENTILE, Field: "score", Params: json.RawMessage(`{"percentile": 95}`)}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	if s := findSuggestionByPath(t, ss, "Aggregations.0.Params.percentile"); s != nil {
		t.Errorf("did not expect missing-percentile suggestion when param is supplied; got %+v", s)
	}
}

// ----- Source 5: streamability hints ------------------------------------

func TestPredict_Suggestions_StreamabilityAlternative(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Aggregations.0.Type")
	if s == nil {
		t.Fatalf("expected streamability suggestion for AGG_MEDIAN; got %+v", ss)
	}
	if len(s.Proposed) != 1 || s.Proposed[0] != "AGG_AVERAGE" {
		t.Errorf("Proposed = %v, want [AGG_AVERAGE]", s.Proposed)
	}
	if s.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", s.Confidence)
	}
	if s.Current != "AGG_MEDIAN" {
		t.Errorf("Current = %v, want AGG_MEDIAN", s.Current)
	}
}

func TestPredict_Suggestions_StreamabilityNoPeer(t *testing.T) {
	// ATTR_PERCENTILE has no streamable peer; suggestion must still
	// fire so the caller is not left in silence.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "score"}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Attributes.0.Type")
	if s == nil {
		t.Fatalf("expected suggestion noting ATTR_PERCENTILE has no streamable peer; got %+v", ss)
	}
	if len(s.Proposed) != 0 {
		t.Errorf("Proposed should be empty for no-peer suggestion; got %v", s.Proposed)
	}
	if s.Confidence != 0.6 {
		t.Errorf("Confidence = %v, want 0.6", s.Confidence)
	}
}

func TestPredict_Suggestions_StreamabilityValidRequest(t *testing.T) {
	// Otherwise-valid request that uses AGG_MEDIAN — Streamable=false
	// but Errors should be empty. Suggestion fires on the success
	// path.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}},
	}
	data := buildTestPulseFile(t, schema)
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("expected no errors; got %v", env.Errors)
	}
	result := env.Data.(*PredictResult)
	if !result.Valid {
		t.Error("Valid = false on a request whose only flaw is non-streamability")
	}
	if result.Streamable {
		t.Error("Streamable = true; want false for AGG_MEDIAN")
	}
	if len(result.Suggestions) == 0 {
		t.Error("Suggestions empty on success+non-streamable request; want streamability suggestion")
	}
}

func TestPredict_Suggestions_StreamabilityGroupQuantile(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score"}},
		Groups:       []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}},
	}
	ss := runPredictForSuggestions(t, schema, req)
	s := findSuggestionByPath(t, ss, "Groups.0.Type")
	if s == nil {
		t.Fatalf("expected streamability suggestion for GROUP_QUANTILE; got %+v", ss)
	}
	if len(s.Proposed) != 1 || s.Proposed[0] != "GROUP_RANGE" {
		t.Errorf("Proposed = %v, want [GROUP_RANGE]", s.Proposed)
	}
}

// ----- Envelope / JSON shape --------------------------------------------

func TestPredict_Suggestions_EmptySliceJSON(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score in range [0, 1]"},
		},
	}
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "score"}},
	}
	data := buildTestPulseFile(t, schema)
	env := PredictFromBytes(data, req, nil)

	buf, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(buf), `"suggestions":[]`) {
		t.Errorf("expected suggestions:[] in JSON output; got %s", string(buf))
	}
	if strings.Contains(string(buf), `"suggestions":null`) {
		t.Errorf("suggestions should never be null in JSON output; got %s", string(buf))
	}
}

// ----- Levenshtein helper ----------------------------------------------

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"revenu", "revenue", 1},
		{"revneue", "revenue", 2},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
