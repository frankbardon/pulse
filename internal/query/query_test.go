package query

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fixtureSchema is the canonical schema the table tests run against. It
// covers the field-type spread the parser cares about: numeric,
// categorical, date, and a couple of disambiguation candidates.
func fixtureSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64},
			{Name: "sessions", Type: encoding.FieldTypeU32},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8},
			{Name: "signup_date", Type: encoding.FieldTypeDate},
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "score2", Type: encoding.FieldTypeF64},
		},
	}
}

func TestNaturalQuery_HeuristicGrammar(t *testing.T) {
	type want struct {
		aggTypes   []types.AggregationType
		aggFields  []string
		groupTypes []types.GroupType
		groupField []string
		filterAt   int    // index into Filterers when checking; -1 = no filter expected
		filterType types.FiltererType
		filterField string
		testType   types.TestType
		testField  string
		testField2 string
		winType    types.WindowType
		sortField  string
	}

	cases := []struct {
		name  string
		query string
		want  want
	}{
		{
			name:  "average <field>",
			query: "average revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_AVERAGE},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "avg synonym",
			query: "avg revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_AVERAGE},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "mean synonym",
			query: "mean revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_AVERAGE},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "sum",
			query: "sum sessions",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_SUM},
				aggFields: []string{"sessions"},
				filterAt:  -1,
			},
		},
		{
			name:  "total synonym",
			query: "total sessions",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_SUM},
				aggFields: []string{"sessions"},
				filterAt:  -1,
			},
		},
		{
			name:  "median",
			query: "median revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_MEDIAN},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "stddev",
			query: "stddev revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_STDDEV},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "min",
			query: "min revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_MIN},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "max",
			query: "max revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_MAX},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "minimum synonym",
			query: "minimum revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_MIN},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "by single field",
			query: "average revenue by region",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_AVERAGE},
				aggFields:  []string{"revenue"},
				groupTypes: []types.GroupType{""}, // empty Type → smart-defaults will fill
				groupField: []string{"region"},
				filterAt:   -1,
			},
		},
		{
			name:  "by two fields with and",
			query: "average revenue by region and country",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_AVERAGE},
				aggFields:  []string{"revenue"},
				groupTypes: []types.GroupType{"", ""},
				groupField: []string{"region", "country"},
				filterAt:   -1,
			},
		},
		{
			name:  "by two fields with comma",
			query: "sum sessions by region, country",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_SUM},
				aggFields:  []string{"sessions"},
				groupTypes: []types.GroupType{"", ""},
				groupField: []string{"region", "country"},
				filterAt:   -1,
			},
		},
		{
			name:  "over time default",
			query: "sum revenue over time",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_SUM},
				aggFields:  []string{"revenue"},
				groupTypes: []types.GroupType{types.GROUP_DATE},
				groupField: []string{"signup_date"},
				filterAt:   -1,
			},
		},
		{
			name:  "over time month",
			query: "sum revenue over time month",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_SUM},
				aggFields:  []string{"revenue"},
				groupTypes: []types.GroupType{types.GROUP_DATE},
				groupField: []string{"signup_date"},
				filterAt:   -1,
			},
		},
		{
			name:  "top N field by count",
			query: "top 5 region by count",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_COUNT},
				aggFields:  []string{"region"},
				groupTypes: []types.GroupType{types.GROUP_CATEGORY},
				groupField: []string{"region"},
				sortField:  "n",
				filterAt:   -1,
			},
		},
		{
			name:  "bare count",
			query: "count",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_COUNT},
				// schema first field after sort is "country" alphabetically — verify below
				filterAt: -1,
			},
		},
		{
			name:  "count field",
			query: "count revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_COUNT},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "percentile",
			query: "percentile 95 revenue",
			want: want{
				aggTypes:  []types.AggregationType{types.AGG_PERCENTILE},
				aggFields: []string{"revenue"},
				filterAt:  -1,
			},
		},
		{
			name:  "correlate two fields",
			query: "correlate score with score2",
			want: want{
				testType:   types.TEST_PEARSON_R,
				testField:  "score",
				testField2: "score2",
				filterAt:   -1,
			},
		},
		{
			name:  "where equality",
			query: "average revenue where region us",
			want: want{
				aggTypes:    []types.AggregationType{types.AGG_AVERAGE},
				aggFields:   []string{"revenue"},
				filterAt:    0,
				filterType:  types.FILTER_INCLUDE,
				filterField: "region",
			},
		},
		{
			name:  "where greater-equal",
			query: "average revenue where revenue >= 100",
			want: want{
				aggTypes:    []types.AggregationType{types.AGG_AVERAGE},
				aggFields:   []string{"revenue"},
				filterAt:    0,
				filterType:  types.FILTER_RANGE,
				filterField: "revenue",
			},
		},
		{
			name:  "where not-equal",
			query: "sum sessions where region != us",
			want: want{
				aggTypes:    []types.AggregationType{types.AGG_SUM},
				aggFields:   []string{"sessions"},
				filterAt:    0,
				filterType:  types.FILTER_EXCLUDE,
				filterField: "region",
			},
		},
		{
			name:  "with lag",
			query: "sum revenue over time with lag 1",
			want: want{
				aggTypes:   []types.AggregationType{types.AGG_SUM},
				aggFields:  []string{"revenue"},
				groupTypes: []types.GroupType{types.GROUP_DATE},
				groupField: []string{"signup_date"},
				winType:    types.WIN_LAG,
				filterAt:   -1,
			},
		},
	}

	schema := fixtureSchema()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Parse(c.query, schema, ParseOptions{File: "test.pulse"})
			if res.Request == nil {
				t.Fatalf("nil Request")
			}

			// Check aggregations.
			if len(c.want.aggTypes) > 0 {
				if len(res.Request.Aggregations) < len(c.want.aggTypes) {
					t.Fatalf("got %d aggregations, want %d", len(res.Request.Aggregations), len(c.want.aggTypes))
				}
				for i, w := range c.want.aggTypes {
					if res.Request.Aggregations[i].Type != w {
						t.Errorf("Aggregations[%d].Type = %q, want %q", i, res.Request.Aggregations[i].Type, w)
					}
				}
				for i, w := range c.want.aggFields {
					if i >= len(res.Request.Aggregations) {
						break
					}
					if res.Request.Aggregations[i].Field != w {
						t.Errorf("Aggregations[%d].Field = %q, want %q", i, res.Request.Aggregations[i].Field, w)
					}
				}
			}

			// Check groups.
			if len(c.want.groupTypes) > 0 {
				if len(res.Request.Groups) < len(c.want.groupTypes) {
					t.Fatalf("got %d groups, want %d (query=%q)", len(res.Request.Groups), len(c.want.groupTypes), c.query)
				}
				for i, w := range c.want.groupTypes {
					if res.Request.Groups[i].Type != w {
						t.Errorf("Groups[%d].Type = %q, want %q", i, res.Request.Groups[i].Type, w)
					}
				}
				for i, w := range c.want.groupField {
					if i >= len(res.Request.Groups) {
						break
					}
					if res.Request.Groups[i].Field != w {
						t.Errorf("Groups[%d].Field = %q, want %q", i, res.Request.Groups[i].Field, w)
					}
				}
			}

			// Check filterers.
			if c.want.filterAt >= 0 {
				if len(res.Request.Filterers) <= c.want.filterAt {
					t.Fatalf("got %d filterers, want at least %d", len(res.Request.Filterers), c.want.filterAt+1)
				}
				f := res.Request.Filterers[c.want.filterAt]
				if f.Type != c.want.filterType {
					t.Errorf("Filterers[%d].Type = %q, want %q", c.want.filterAt, f.Type, c.want.filterType)
				}
				if f.Field != c.want.filterField {
					t.Errorf("Filterers[%d].Field = %q, want %q", c.want.filterAt, f.Field, c.want.filterField)
				}
			}

			// Tests slot.
			if c.want.testType != "" {
				if len(res.Request.Tests) == 0 {
					t.Fatalf("expected a Test entry, got none")
				}
				if res.Request.Tests[0].Type != c.want.testType {
					t.Errorf("Tests[0].Type = %q, want %q", res.Request.Tests[0].Type, c.want.testType)
				}
				if res.Request.Tests[0].Field != c.want.testField {
					t.Errorf("Tests[0].Field = %q, want %q", res.Request.Tests[0].Field, c.want.testField)
				}
				if res.Request.Tests[0].Field2 != c.want.testField2 {
					t.Errorf("Tests[0].Field2 = %q, want %q", res.Request.Tests[0].Field2, c.want.testField2)
				}
			}

			// Windows slot.
			if c.want.winType != "" {
				if len(res.Request.Windows) == 0 {
					t.Fatalf("expected a Window entry, got none")
				}
				if res.Request.Windows[0].Type != c.want.winType {
					t.Errorf("Windows[0].Type = %q, want %q", res.Request.Windows[0].Type, c.want.winType)
				}
			}

			// Sort.
			if c.want.sortField != "" {
				if len(res.Request.Sort) == 0 {
					t.Fatalf("expected a Sort entry, got none")
				}
				if res.Request.Sort[0].Field != c.want.sortField {
					t.Errorf("Sort[0].Field = %q, want %q", res.Request.Sort[0].Field, c.want.sortField)
				}
				if !res.Request.Sort[0].Desc {
					t.Errorf("Sort[0].Desc = false, want true")
				}
			}

			// All cases must produce non-zero confidence unless they
			// explicitly emit UNRESOLVED.
			hasUnresolved := false
			for _, iss := range res.Issues {
				if iss.Code == errors.PULSE_QUERY_UNRESOLVED {
					hasUnresolved = true
				}
			}
			if !hasUnresolved && res.Confidence <= 0 {
				t.Errorf("Confidence = %v, want > 0", res.Confidence)
			}
		})
	}
}

func TestNaturalQuery_FieldFuzzyMatching(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		},
	}

	// Distance-1 typo on revenue resolves.
	res := Parse("average revneue", schema, ParseOptions{File: "t.pulse"})
	if len(res.Request.Aggregations) == 0 || res.Request.Aggregations[0].Field != "revenue" {
		t.Errorf("expected fuzzy match revneue->revenue; got %+v", res.Request.Aggregations)
	}
	if res.Confidence >= 1.0 {
		t.Errorf("expected confidence < 1 for fuzzy match, got %v", res.Confidence)
	}

	// Distance-2 typo on region resolves.
	res2 := Parse("count regionn", schema, ParseOptions{File: "t.pulse"})
	if len(res2.Request.Aggregations) == 0 || res2.Request.Aggregations[0].Field != "region" {
		t.Errorf("expected fuzzy match regionn->region; got %+v", res2.Request.Aggregations)
	}

	// No ambiguous warning when there is only one near match.
	for _, iss := range res.Issues {
		if iss.Code == errors.PULSE_QUERY_AMBIGUOUS {
			t.Errorf("unexpected ambiguous warning: %v", iss)
		}
	}
}

func TestNaturalQuery_FieldFuzzyMatching_Ambiguous(t *testing.T) {
	// Two fields equidistant from the supplied token.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score1", Type: encoding.FieldTypeF64},
			{Name: "score2", Type: encoding.FieldTypeF64},
		},
	}
	res := Parse("average scoreX", schema, ParseOptions{File: "t.pulse"})
	// At distance 1, "scoreX" → score1 and score2 both match. Parser
	// emits PULSE_QUERY_AMBIGUOUS and picks lexically first.
	hasAmb := false
	for _, iss := range res.Issues {
		if iss.Code == errors.PULSE_QUERY_AMBIGUOUS {
			hasAmb = true
		}
	}
	if !hasAmb {
		t.Errorf("expected PULSE_QUERY_AMBIGUOUS warning; issues=%+v", res.Issues)
	}
	if len(res.Request.Aggregations) == 0 || res.Request.Aggregations[0].Field != "score1" {
		t.Errorf("expected lexically-first pick score1; got %+v", res.Request.Aggregations)
	}
}

func TestNaturalQuery_UnresolvedReturnsCode(t *testing.T) {
	schema := fixtureSchema()
	res := Parse("blargh wobbly snarf", schema, ParseOptions{File: "t.pulse"})

	hasUnresolved := false
	for _, iss := range res.Issues {
		if iss.Code == errors.PULSE_QUERY_UNRESOLVED {
			hasUnresolved = true
		}
	}
	if !hasUnresolved {
		t.Errorf("expected PULSE_QUERY_UNRESOLVED; got issues=%+v", res.Issues)
	}
	if res.Confidence != 0 {
		t.Errorf("Confidence = %v, want 0 for unresolved query", res.Confidence)
	}
}

func TestNaturalQuery_EmptyQuery(t *testing.T) {
	res := Parse("   ", fixtureSchema(), ParseOptions{File: "t.pulse"})
	if len(res.Issues) == 0 || res.Issues[0].Code != errors.PULSE_QUERY_UNRESOLVED {
		t.Errorf("expected PULSE_QUERY_UNRESOLVED for empty query, got %+v", res.Issues)
	}
}

func TestNaturalQuery_OverTimeWithoutDate(t *testing.T) {
	// Schema without any date field.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "revenue", Type: encoding.FieldTypeF64},
		},
	}
	res := Parse("sum revenue over time", schema, ParseOptions{File: "t.pulse"})
	found := false
	for _, iss := range res.Issues {
		if iss.Code == errors.PULSE_QUERY_UNRESOLVED && strings.Contains(iss.Message, "over time") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected over-time-without-date unresolved warning; issues=%+v", res.Issues)
	}
}

func TestNaturalQuery_TopNStructure(t *testing.T) {
	schema := fixtureSchema()
	res := Parse("top 10 region by count", schema, ParseOptions{File: "t.pulse"})

	if len(res.Request.Aggregations) != 1 || res.Request.Aggregations[0].Type != types.AGG_COUNT {
		t.Fatalf("expected one AGG_COUNT; got %+v", res.Request.Aggregations)
	}
	if res.Request.Aggregations[0].Label != "n" {
		t.Errorf("Aggregations[0].Label = %q, want %q", res.Request.Aggregations[0].Label, "n")
	}
	if len(res.Request.Groups) != 1 || res.Request.Groups[0].Type != types.GROUP_CATEGORY {
		t.Errorf("expected one GROUP_CATEGORY; got %+v", res.Request.Groups)
	}
	if len(res.Request.Sort) != 1 || !res.Request.Sort[0].Desc {
		t.Errorf("expected desc sort by n; got %+v", res.Request.Sort)
	}
}
