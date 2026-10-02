package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func fieldRefSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	_, _ = dict.Add("a")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeF64},
		{Name: "m", Type: encoding.FieldTypeF64},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
}

// refusedNames returns the "field"-ish detail of every refusal, in order.
func refusedNames(t *testing.T, req *types.Request) []string {
	t.Helper()
	var out []string
	for _, ce := range FieldRefRefusals(req, fieldRefSchema(), nil) {
		if ce.Code != errors.SERVICE_VALIDATION && ce.Code != errors.PULSE_WINDOW_INVALID {
			t.Fatalf("unexpected code %s", ce.Code)
		}
		for _, k := range []string{"field", "field2", "split_by", "subject_field"} {
			if v, ok := ce.Details[k]; ok {
				out = append(out, v.(string))
				break
			}
		}
	}
	return out
}

func sameNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("refused %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("refused %q, want %q", got, want)
		}
	}
}

// TestFieldRefRefusals_PipelineOrderAndVisibility: each stage sees only
// what the stages before it produce, and the refusals come back in
// pipeline order.
func TestFieldRefRefusals_PipelineOrderAndVisibility(t *testing.T) {
	req := &types.Request{
		Features: []*types.Feature{
			{Type: types.FEAT_LOG, Field: "f_unknown"},
			{Type: types.FEAT_SQRT, Field: "LOG_f_unknown"}, // earlier feature output: visible
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "SQRT_LOG_f_unknown"}, // feature output: visible
			{Type: types.FILTER_RANGE, Field: "x"},                  // attribute label: NOT visible yet
			{Type: types.FILTER_EXPRESSION, Expression: "n > 1"},    // no field: skipped
		},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "y", Label: "zy"}, // later label: not visible
			{Type: types.ATTR_FORMULA, Label: "x", Expression: "n"},
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "y"}, // earlier label: visible
		},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "zy"}}, // every attribute label visible
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "agg_unknown"}},
		Sort:         []types.OrderKey{{Field: "AGG_SUM_agg_unknown"}, {Field: "n"}}, // n is not an output column
	}
	sameNames(t, refusedNames(t, req), "f_unknown", "x", "y", "agg_unknown", "n")
}

func TestFieldRefRefusals_OutputColumns(t *testing.T) {
	t.Run("windows see earlier windows; sort and post-tests see all", func(t *testing.T) {
		req := &types.Request{
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n"}},
			Windows: []*types.Window{
				{Type: types.WIN_LAG, Field: "WIN_LAG_AGG_SUM_n", OrderBy: []types.OrderKey{{Field: "AGG_SUM_n"}}}, // later label
				{Type: types.WIN_LAG, Field: "AGG_SUM_n", OrderBy: []types.OrderKey{{Field: "cat"}}, PartitionBy: []string{"m"}},
				{Type: types.WIN_RANK, Field: "ignored", OrderBy: []types.OrderKey{{Field: "WIN_LAG_AGG_SUM_n"}}},
			},
			Sort:      []types.OrderKey{{Field: "WIN_LAG_WIN_LAG_AGG_SUM_n"}},
			PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "WIN_RANK_ignored", OrderBy: []types.OrderKey{{Field: "n"}}}},
		}
		sameNames(t, refusedNames(t, req), "WIN_LAG_AGG_SUM_n", "m", "n")
		post := FieldRefRefusals(req, fieldRefSchema(), nil)
		if post[len(post)-1].Details["post_test_index"] != 0 {
			t.Fatalf("post-test refusal details = %v, want post_test_index", post[len(post)-1].Details)
		}
	})
	t.Run("without groups or aggregations the rows are the records", func(t *testing.T) {
		req := &types.Request{
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x", Expression: "n"}},
			Windows:    []*types.Window{{Type: types.WIN_LAG, Field: "x", OrderBy: []types.OrderKey{{Field: "m"}}}},
		}
		sameNames(t, refusedNames(t, req))
	})
	t.Run("a crosstab's post-tests are not judged", func(t *testing.T) {
		req := &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c_unknown"}},
				Cell:               &types.Aggregation{Type: types.AGG_SUM, Field: "n"},
				MarginAggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "ma_unknown"}},
			},
			PostTests: []*types.Test{{Type: types.TEST_TREND, Field: "cell_value"}},
			Sort:      []types.OrderKey{{Field: "m"}}, // record column on a crosstab
		}
		sameNames(t, refusedNames(t, req), "c_unknown", "ma_unknown")
	})
}

func TestFieldRefRefusals_SlotShapes(t *testing.T) {
	t.Run("empty names where a slot requires one", func(t *testing.T) {
		req := &types.Request{
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "x", Expression: "n"}, // optional
				{Type: types.ATTR_ZSCORE},                               // required
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT}},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE},                         // required
				{Type: types.FILTER_EXPRESSION, Expression: "n > 1"}, // optional
				{Type: "FILTER_ACME_NEAR"},                           // extension: its own business
			},
		}
		sameNames(t, refusedNames(t, req), "", "", "", "")
	})
	t.Run("an empty name is refused even once the set is open", func(t *testing.T) {
		req := &types.Request{
			Features:     []*types.Feature{{Type: "FEAT_ACME_EMBED", Field: "n"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT}, {Type: types.AGG_SUM, Field: "acme_vec_0"}},
		}
		sameNames(t, refusedNames(t, req), "")
	})
	t.Run("feature params and regression shapes", func(t *testing.T) {
		req := &types.Request{
			Features: []*types.Feature{
				{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[1],"stratify":"s_unknown"}`)},
				{Type: types.FEAT_TARGET_ENCODE, Field: "cat", Params: json.RawMessage(`{"target":"t_unknown"}`)},
				{Type: types.FEAT_LOG, Field: "split"}, // the split's output column
			},
			Attributes:  []*types.Attribute{{Type: types.ATTR_REG_FITTED, Target: "r_unknown", Predictors: []string{"n", "p_unknown"}}},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "ATTR_REG_FITTED_r_unknown", Predictors: []string{"q_unknown"}}},
		}
		sameNames(t, refusedNames(t, req), "s_unknown", "t_unknown", "r_unknown", "p_unknown", "q_unknown")
	})
	t.Run("tests read their own slots", func(t *testing.T) {
		req := &types.Request{Tests: []*types.Test{
			{Type: types.TEST_CHISQ, Rows: "cat", Cols: "c_unknown"},
			{Type: types.TEST_PROP_Z, Field: "pf_unknown", SplitBy: "cat"},
			{Type: types.TEST_PEARSON_R, Field: "n", Field2: "f2_unknown"},
			{Type: types.TEST_T, Field: "n", Field2: "not_read", SplitBy: "sb_unknown"},
			{Type: types.TEST_ANOVA_RM, Field: "n", SplitBy: "cat", SubjectField: "sf_unknown"},
		}}
		sameNames(t, refusedNames(t, req), "c_unknown", "pf_unknown", "f2_unknown", "sb_unknown", "sf_unknown")
	})
	t.Run("an extension feature opens the column set", func(t *testing.T) {
		req := &types.Request{
			Features:     []*types.Feature{{Type: "FEAT_ACME_EMBED", Field: "n"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "acme_vec_0"}},
			Sort:         []types.OrderKey{{Field: "anything"}},
		}
		sameNames(t, refusedNames(t, req))
	})
	t.Run("no schema, no judgement", func(t *testing.T) {
		req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "zz"}}}
		if FieldRefRefusals(req, nil, nil) != nil || FieldRefRefusal(req, nil, nil) != nil || FieldRefRefusals(nil, fieldRefSchema(), nil) != nil {
			t.Fatal("nil schema or request must yield no refusal")
		}
		err, ok := FieldRefRefusal(req, fieldRefSchema(), nil).(*errors.CodedError)
		if !ok || err.Message != "aggregation references unknown field: zz" {
			t.Fatalf("FieldRefRefusal = %v", err)
		}
	})
}

// TestFieldRefRefusals_ParamsAndFieldInputs: built-in params name
// columns on every aggregation slot and post-test, and an extension's
// FieldInputs declaration is judged at its own slot's pipeline point
// (a feature's before it opens the set, a filterer's before attribute
// labels exist, a window's against the output columns).
func TestFieldRefRefusals_ParamsAndFieldInputs(t *testing.T) {
	declares := func(names ...string) func(json.RawMessage) []string {
		return func(json.RawMessage) []string { return names }
	}
	snap := &ExtensionsSnapshot{FieldInputs: map[string]func(json.RawMessage) []string{
		"filterer|FILTER_ACME_NEAR":  declares("x", "n"), // x is an attribute label: not yet visible
		"attribute|ATTR_ACME_SCORE":  declares("x", "later"),
		"grouper|GROUP_ACME_BAND":    declares("g_unknown"),
		"aggregator|AGG_ACME_MEAN":   declares("m", "a_unknown"),
		"window|WIN_ACME_DECAY":      declares("n"), // a record column, not an output column
		"feature|FEAT_ACME_EMBED":    declares("fe_unknown"),
		"test|TEST_ACME_DRIFT":       declares("cat"),
		"aggregator|AGG_ACME_SILENT": nil,
	}}
	req := &types.Request{
		Filterers: []*types.Filterer{{Type: "FILTER_ACME_NEAR"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "x", Expression: "n"},
			{Type: "ATTR_ACME_SCORE", Field: "n", Label: "later"},
		},
		Tests:  []*types.Test{{Type: "TEST_ACME_DRIFT"}},
		Groups: []*types.Group{{Type: "GROUP_ACME_BAND", Field: "cat"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_WEIGHTED_MEAN, Field: "n", Params: json.RawMessage(`{"weight_field":"w_unknown"}`)},
			{Type: types.AGG_RATIO, Field: "n", Params: json.RawMessage(`{"numerator_field":"m","denominator_field":"x"}`)},
			{Type: "AGG_ACME_MEAN", Field: "n"},
			{Type: "AGG_ACME_SILENT", Field: "n", Params: json.RawMessage(`{"weight_field":"ignored"}`)},
		},
		Windows:   []*types.Window{{Type: "WIN_ACME_DECAY", OrderBy: []types.OrderKey{{Field: "n"}}}},
		PostTests: []*types.Test{{Type: types.TEST_TUKEY_HSD, Field: "AGG_RATIO_n", SplitBy: "n", Params: json.RawMessage(`{"n_column":"nc_unknown"}`)}},
	}
	// The "later" attribute input is its own label: not yet produced.
	names := func(all []*errors.CodedError) []string {
		var out []string
		for _, ce := range all {
			for _, k := range []string{"field", "split_by"} {
				if v, ok := ce.Details[k].(string); ok {
					out = append(out, v)
					break
				}
			}
		}
		return out
	}
	sameNames(t, names(FieldRefRefusals(req, fieldRefSchema(), snap)),
		"x", "later", "g_unknown", "w_unknown", "a_unknown", "n", "n", "n", "nc_unknown")
	// Without the snapshot only the built-in params are judged.
	sameNames(t, names(FieldRefRefusals(req, fieldRefSchema(), nil)), "w_unknown", "n", "n", "nc_unknown")

	// A feature's declaration is judged before it opens the set.
	feat := &types.Request{Features: []*types.Feature{{Type: "FEAT_ACME_EMBED", Field: "n"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "anything"}}}
	sameNames(t, names(FieldRefRefusals(feat, fieldRefSchema(), snap)), "fe_unknown")
}
