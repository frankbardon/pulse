package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFieldRefs_FacetFiltererRefusedLikeValidator: a FacetSchema
// filterer naming an unknown field is refused at runtime — with and
// without FACET-host overlays — with exactly the code, message and
// details ValidateFacet reports (the shared FieldRefRefusals filterer
// rule). Before, the validator refused it and the runtime filtered on
// an all-null column and kept nothing.
func TestFieldRefs_FacetFiltererRefusedLikeValidator(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	catParam := json.RawMessage(`{"field":"cat"}`)
	cases := map[string]func() *types.FacetRequest{
		"plain": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "zz", Values: []string{"a"}}}}
		},
		"with overlay": func() *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
				Filterers: []*types.Filterer{{Type: types.FILTER_RANGE, Field: "zz", Values: []string{"0", "10"}}},
				Overlays: []types.OverlaySpec{{Name: "ix", Kind: types.OverlayKindIndexVsPop, Scope: types.OverlayScopeGroup,
					Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: cohort}}, Params: catParam}}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.FacetSchema(ctx, mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Message != "filter references unknown field: zz" {
				t.Fatalf("runtime message = %q", ce.Message)
			}
			sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), mk(), opts), rerr)
		})
	}
	// A known field still runs on both sides.
	ok := &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}}}}
	if _, err := p.FacetSchema(ctx, ok); err != nil {
		t.Fatalf("runtime refused a known field: %v", err)
	}
	if env := descx.ValidateFacetWithOptions(bytes.NewReader(data), ok, opts); len(env.Errors) != 0 {
		t.Fatalf("validator refused a known field: %+v", env.Errors)
	}
}

// TestFieldRefs_EmptyFilterFieldRefusedLikePredict: every built-in
// filterer except FILTER_EXPRESSION reads Field, so an empty one is a
// field-reference refusal — the shared rule's code, message and details
// on Process, ProcessStream, predict, FacetSchema / ValidateFacet and,
// located, inside Compose. Before, predict accepted it and the runtime
// failed later with an operator-specific PROCESSING_CONFIG (or kept no
// rows). FILTER_EXPRESSION without a Field still runs on both sides.
func TestFieldRefs_EmptyFilterFieldRefusedLikePredict(t *testing.T) {
	fs, cohort := zoneCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	for _, typ := range types.AllFiltererTypes() {
		if typ == types.FILTER_EXPRESSION {
			continue
		}
		fil := func() []*types.Filterer {
			return []*types.Filterer{{Type: typ, Values: []string{"a"}}}
		}
		mk := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Filterers: fil(), Aggregations: countAgg()}
		}
		t.Run(string(typ), func(t *testing.T) {
			_, rerr := p.Process(ctx, mk())
			ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
			if ce.Message != "filter references unknown field: " || ce.Details["filter"] != string(typ) {
				t.Fatalf("runtime = %q %v", ce.Message, ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, mk()), rerr)
			_, serr := p.ProcessStream(ctx, mk())
			if se := requireCode(t, serr, errors.SERVICE_VALIDATION); se.Message != ce.Message {
				t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
			}

			facet := func() *types.FacetRequest {
				return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"}, Filterers: fil()}
			}
			_, ferr := p.FacetSchema(ctx, facet())
			requireCode(t, ferr, errors.SERVICE_VALIDATION)
			sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), facet(), opts), ferr)

			compose := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{
					{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()}, mk(),
				}}
			}
			_, cerr := p.Compose(ctx, compose())
			if cc := requireCode(t, cerr, errors.SERVICE_VALIDATION); cc.Details["request"] != 1 {
				t.Fatalf("details = %v, want request=1", cc.Details)
			}
			sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), cerr)
		})
	}
	t.Run("FILTER_EXPRESSION needs no field", func(t *testing.T) {
		req := func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "n > 1"}}}
		}
		if _, err := p.Process(ctx, req()); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := predictEnvelope(t, p, fs, cohort, req()); len(env.Errors) != 0 {
			t.Fatalf("predict: %+v", env.Errors)
		}
	})
}

// paramFieldRequests is one request per built-in slot whose PARAMS name
// a column, each naming the unknown "zz", with the runtime's expected
// message. Unjudged, the runtime read each name as an all-null column:
// AGG_WEIGHTED_MEAN / AGG_RATIO / AGG_DISTINCT_SUM answered a confident
// empty result and the post-test failed per row.
func paramFieldRequests(cohort string) map[string]struct {
	message string
	req     func() *types.Request
} {
	co := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	cat := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}} }
	agg := func(typ types.AggregationType, params string) *types.Aggregation {
		return &types.Aggregation{Type: typ, Field: "n", Label: "v", Params: json.RawMessage(params)}
	}
	xt := func(cell *types.Aggregation, margins ...*types.Aggregation) *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns:            []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Params: json.RawMessage(`{"interval":20}`)}},
			Cell:               cell,
			MarginAggregations: margins,
			Margins:            types.CrosstabMargins{Grand: true},
		}
	}
	type entry = struct {
		message string
		req     func() *types.Request
	}
	return map[string]entry{
		"weighted mean weight_field": {"aggregation AGG_WEIGHTED_MEAN: params.weight_field references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(), Aggregations: []*types.Aggregation{agg(types.AGG_WEIGHTED_MEAN, `{"weight_field":"zz"}`)}}
		}},
		"ratio denominator_field": {"aggregation AGG_RATIO: params.denominator_field references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(), Aggregations: []*types.Aggregation{agg(types.AGG_RATIO, `{"numerator_field":"n","denominator_field":"zz"}`)}}
		}},
		"distinct sum distinct_by": {"aggregation AGG_DISTINCT_SUM: params.distinct_by references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(), Aggregations: []*types.Aggregation{agg(types.AGG_DISTINCT_SUM, `{"distinct_by":"zz"}`)}}
		}},
		"crosstab cell": {"crosstab cell aggregation AGG_WEIGHTED_MEAN: params.weight_field references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Crosstab: xt(agg(types.AGG_WEIGHTED_MEAN, `{"weight_field":"zz"}`))}
		}},
		"crosstab margin aggregation": {"crosstab margin aggregation AGG_RATIO: params.numerator_field references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Crosstab: xt(&types.Aggregation{Type: types.AGG_SUM, Field: "n"},
				agg(types.AGG_RATIO, `{"numerator_field":"zz","denominator_field":"n"}`))}
		}},
		"post-test n_col": {"TEST_ANOVA_F: params.n_col references unknown field zz", func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "n", Label: "m"}, {Type: types.AGG_COUNT, Field: "n", Label: "c"}, {Type: types.AGG_VARIANCE, Field: "n", Label: "var"}},
				PostTests:    []*types.Test{{Type: types.TEST_ANOVA_F, Field: "m", SplitBy: "cat", Params: json.RawMessage(`{"n_col":"zz","variance_col":"var"}`)}}}
		}},
	}
}

// TestFieldRefs_ParamFieldsRefusedLikePredict: a column named inside a
// built-in operator's params is judged by the shared rule — Process
// (both crosstab arms), ProcessStream, predict and, located, Compose
// return the same code, message and details.
func TestFieldRefs_ParamFieldsRefusedLikePredict(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for name, c := range paramFieldRequests(cohort) {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", name, disable), func(t *testing.T) {
				_, rerr := p.Process(ctx, c.req())
				ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
				if ce.Message != c.message || ce.Details["field"] != "zz" {
					t.Fatalf("runtime = %q %v, want %q", ce.Message, ce.Details, c.message)
				}
				sameEntry(t, predictEnvelope(t, p, fs, cohort, c.req()), rerr)
				_, serr := p.ProcessStream(ctx, c.req())
				if se := requireCode(t, serr, errors.SERVICE_VALIDATION); se.Message != ce.Message {
					t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
				}
				compose := func() *types.ComposedRequest {
					return &types.ComposedRequest{Requests: []*types.Request{
						{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg()}, c.req(),
					}}
				}
				_, cerr := p.Compose(ctx, compose())
				if cc := requireCode(t, cerr, errors.SERVICE_VALIDATION); cc.Details["request"] != 1 {
					t.Fatalf("details = %v, want request=1", cc.Details)
				}
				sameEntry(t, descx.ValidateComposeWithOptions(compose(), opts), cerr)
			})
		}
	}
}

// TestFieldRefs_ParamFieldsDerivedNamesAccepted: a params name the
// pipeline produces — an attribute label as a weight, an aggregation
// label as a post-test column — is not unknown; both sides run it.
func TestFieldRefs_ParamFieldsDerivedNamesAccepted(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	co := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	cat := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	cases := map[string]func() *types.Request{
		"attribute label as weight": func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat,
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "w", Expression: "n + 1"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_WEIGHTED_MEAN, Field: "n", Params: json.RawMessage(`{"weight_field":"w"}`)}}}
		},
		"aggregation labels as post-test columns": func() *types.Request {
			return &types.Request{Cohort: co(), Groups: cat,
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "n", Label: "m"}, {Type: types.AGG_COUNT, Field: "n", Label: "c"}, {Type: types.AGG_VARIANCE, Field: "n", Label: "var"}},
				PostTests:    []*types.Test{{Type: types.TEST_ANOVA_F, Field: "m", SplitBy: "cat", Params: json.RawMessage(`{"n_col":"c","variance_col":"var"}`)}}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, mk()); err != nil {
				t.Fatalf("runtime refused a derived name: %v", err)
			}
			if env := predictEnvelope(t, p, fs, cohort, mk()); len(env.Errors) != 0 {
				t.Fatalf("predict refused a derived name: %+v", env.Errors)
			}
		})
	}
}

// TestFieldRefs_ExtensionFieldInputsJudged: the names an extension
// declares through its registration's FieldInputs hook ride the
// ExtensionsSnapshot, so predict and the runtime judge them with the
// same refusal; a declared name that exists runs on both sides.
func TestFieldRefs_ExtensionFieldInputsJudged(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	reg := parityMeanRegistration(&parityProbe{}, "AGG_ACME_BYMEAN")
	reg.FieldInputs = func(raw json.RawMessage) []string {
		var p struct {
			By string `json:"by"`
		}
		_ = json.Unmarshal(raw, &p)
		return []string{p.By}
	}
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(by string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: "AGG_ACME_BYMEAN", Field: "n", Label: "v", Params: json.RawMessage(`{"by":"` + by + `"}`)}}}
	}
	_, rerr := p.Process(ctx, mk("zz"))
	ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
	if ce.Message != "aggregator AGG_ACME_BYMEAN: FieldInputs references unknown field zz" || ce.Details["field_inputs"] != true {
		t.Fatalf("runtime = %q %v", ce.Message, ce.Details)
	}
	sameEntry(t, predictEnvelope(t, p, fs, cohort, mk("zz")), rerr)
	if _, err := p.Process(ctx, mk("cat")); err != nil {
		t.Fatalf("runtime refused a declared name that exists: %v", err)
	}
	if env := predictEnvelope(t, p, fs, cohort, mk("cat")); len(env.Errors) != 0 {
		t.Fatalf("predict refused a declared name that exists: %+v", env.Errors)
	}

	// A hook that panics is treated as undeclared, never a crash.
	reg.Name = "AGG_ACME_PANICMEAN"
	reg.FieldInputs = func(json.RawMessage) []string { panic("boom") }
	pp, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}})
	if err != nil {
		t.Fatal(err)
	}
	req := mk("zz")
	req.Aggregations[0].Type = "AGG_ACME_PANICMEAN"
	data, _ := afero.ReadFile(fs, cohort)
	if env, err := pp.PredictBytes(ctx, data, req); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict over a panicking hook = %v %+v", err, env)
	}
}

// TestWindowOrderBy_CategoricalOrdersByLabel: a window order_by on a
// categorical column orders by the dictionary LABEL (byte-wise, nulls
// last — the Request.Sort comparator), never by the dictionary index,
// which differs across imports and shards. That is well defined, so
// predict accepts it as the runtime does. The cohort's dictionary is
// in encounter order c, a, b; the row numbers follow a, b, c.
func TestWindowOrderBy_CategoricalOrdersByLabel(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "o.csv", []byte("cat,n\nc,1\na,2\nb,3\nc,4\na,5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "o.csv"})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := p.Inspect(ctx, res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if d := ins.Fields[0].Dictionary; d == nil || fmt.Sprint(d.Values) != "[c a b]" {
		t.Fatalf("dictionary = %+v, want encounter order [c a b] — the test would prove nothing", d)
	}
	rowNumber := []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "cat"}}}}
	cases := map[string]struct {
		req  func() *types.Request
		want map[string][]float64 // label -> row numbers in output order
	}{
		"grouped output": {func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: res.Path},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
				Windows:      rowNumber}
		}, map[string][]float64{"a": {1}, "b": {2}, "c": {3}}},
		"record rows": {func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: res.Path}, Windows: rowNumber}
		}, map[string][]float64{"a": {1, 2}, "b": {3}, "c": {4, 5}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp, err := p.Process(ctx, c.req())
			if err != nil {
				t.Fatalf("runtime: %v", err)
			}
			b, _ := json.Marshal(resp.Data)
			var rows []map[string]any
			if err := json.Unmarshal(b, &rows); err != nil {
				t.Fatal(err)
			}
			got := map[string][]float64{}
			for _, r := range rows {
				got[r["cat"].(string)] = append(got[r["cat"].(string)], r["rn"].(float64))
			}
			for label, want := range c.want {
				if fmt.Sprint(got[label]) != fmt.Sprint(want) {
					t.Fatalf("row numbers = %v, want %v (label order)", got, c.want)
				}
			}
			if env := predictEnvelope(t, p, fs, res.Path, c.req()); len(env.Errors) != 0 {
				t.Fatalf("predict refused what the runtime orders by label: %+v", env.Errors)
			}
		})
	}
}
