package descriptor

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// field_refs_post_window_test.go: the walk judges a row-local attribute
// reading a window label after the windows (transformed-predictors
// E2-S1), by the classification the engine orders by
// (vectors.PostWindowAttributes); any other window-dependent attribute
// is PULSE_ATTRIBUTE_WINDOW_ORDER — also under an extension feature's
// open column set (FR-13).

func postFormula(label, expr string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Expression: expr}
}

// windowOrderRefusals returns the PULSE_ATTRIBUTE_WINDOW_ORDER details
// of refusals, in order, and fails on any other refusal.
func windowOrderRefusals(t *testing.T, refusals []*errors.CodedError) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ce := range refusals {
		if ce.Code != errors.PULSE_ATTRIBUTE_WINDOW_ORDER {
			t.Fatalf("unexpected refusal %s: %s", ce.Code, ce.Message)
		}
		out = append(out, ce.Details)
	}
	return out
}

// TestWindowPredictor_WalkJudgesPostWindowAttributesAfterWindows: hill()
// over a window label, and a formula over that post-window label beside
// a pre-window one, are admitted; their labels reach a regression and a
// sort key. A window still cannot read a post-window label, a
// post-window formula's unknown name is still refused, and a post-window
// label shadowing a window label is refused.
func TestWindowPredictor_WalkJudgesPostWindowAttributesAfterWindows(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{lagWindow("lag_n")},
		Attributes: []*types.Attribute{
			postFormula("sat", "hill(lag_n, 3, 2)"),
			postFormula("pre", "n * 2"),
			postFormula("sat2", "sat * pre"),
		},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "m", Predictors: []string{"sat2", "sat", "pre", "lag_n"}}},
		Sort:        []types.OrderKey{{Field: "sat2"}},
	}
	if got := FieldRefRefusals(req, fieldRefSchema(), nil); len(got) != 0 {
		t.Fatalf("post-window attributes refused: %v", got)
	}

	// A window reading a post-window label: the label is not yet a column.
	late := *req
	late.Windows = []*types.Window{lagWindow("lag_n"), {Type: types.WIN_LAG, Field: "sat", Label: "lag_sat", OrderBy: []types.OrderKey{{Field: "m"}}}}
	sameNames(t, refusedNames(t, &late), "sat")

	// An unknown name in a post-window formula is still the compile's
	// refusal, judged after the windows.
	unknown := &types.Request{Windows: req.Windows, Attributes: []*types.Attribute{postFormula("bad", "lag_n + nope")}}
	got := FieldRefRefusals(unknown, fieldRefSchema(), nil)
	if len(got) != 1 || got[0].Code != errors.PULSE_FORMULA_COMPILE_ERROR {
		t.Fatalf("unknown name in a post-window formula: %v", got)
	}

	// A post-window label equal to a window label shadows it.
	shadow := &types.Request{Windows: []*types.Window{lagWindow("lag_n"), lagWindow("dup")}, Attributes: []*types.Attribute{postFormula("dup", "lag_n * 2")}}
	if got := FieldRefRefusals(shadow, fieldRefSchema(), nil); len(got) != 1 || got[0].Details["label"] != "dup" {
		t.Fatalf("shadowing post-window label: %v", got)
	}
}

// TestWindowPredictor_WalkRefusesWindowOrder: a non-row-local attribute,
// a total() formula, an attribute reading a post-window label, a
// fit-referenced ATTR_REG_FITTED over a window term, and any attribute
// reading a window label outside the record-row shape are
// PULSE_ATTRIBUTE_WINDOW_ORDER {attribute, field}.
func TestWindowPredictor_WalkRefusesWindowOrder(t *testing.T) {
	win := []*types.Window{lagWindow("lag_n")}
	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Target: "m", Terms: []string{"lag_n"}, Kind: types.FitKindRegression}}
	cases := map[string]struct {
		req  *types.Request
		want []map[string]any
	}{
		"not row-local": {&types.Request{Windows: win, Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "lag_n", Label: "z"}}},
			[]map[string]any{{"attribute": "ATTR_ZSCORE", "field": "lag_n"}}},
		"total()": {&types.Request{Windows: win, Attributes: []*types.Attribute{postFormula("share", "lag_n / total(lag_n)")}},
			[]map[string]any{{"attribute": "ATTR_FORMULA", "field": "lag_n"}}},
		"through a post-window label": {&types.Request{Windows: win, Attributes: []*types.Attribute{
			postFormula("sat", "hill(lag_n, 3, 2)"),
			{Type: types.ATTR_PERCENTILE, Field: "sat", Label: "pct"},
			postFormula("after", "pct * 2"), // reads a pre-window label: judged normally
		}}, []map[string]any{{"attribute": "ATTR_PERCENTILE", "field": "sat"}}},
		"fit-referenced fitted value": {&types.Request{Windows: win, Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_FITTED, Label: "f", Params: json.RawMessage(`{"fit":"m"}`)},
		}}, []map[string]any{{"attribute": "ATTR_REG_FITTED", "field": "lag_n"}}},
		"grouped shape": {&types.Request{Windows: []*types.Window{{Type: types.WIN_LAG, Field: "n", Label: "lag_n", OrderBy: []types.OrderKey{{Field: "cat"}}}}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "n"}},
			Attributes:   []*types.Attribute{postFormula("f", "lag_n * 2")}},
			[]map[string]any{{"attribute": "ATTR_FORMULA", "field": "lag_n"}}},
		"crosstab shape": {&types.Request{Windows: win, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		}, Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}}},
			[]map[string]any{{"attribute": "ATTR_REG_PREDICT", "field": "lag_n"}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			refs := fieldRefRefusalsWithModels(c.req, fieldRefSchema(), nil, nil, cat)
			if c.req.Crosstab != nil {
				// The crosstab reads no window output, so its window is
				// also refused (crosstab_window_unread, after the
				// attribute's refusal — the one the runtime raises first).
				last := refs[len(refs)-1]
				if last.Code != errors.PULSE_WINDOW_INVALID || last.Details["reason"] != "crosstab_window_unread" {
					t.Fatalf("crosstab refusals %v, want crosstab_window_unread last", refs)
				}
				refs = refs[:len(refs)-1]
			}
			got := windowOrderRefusals(t, refs)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("refusals %v, want %v", got, c.want)
			}
		})
	}
}

// TestWindowPredictor_WalkScoresWindowTermModel: ATTR_REG_PREDICT over a
// bound model whose term is a window label is admitted once the slot
// declares that window (post-window), as is coef() in a post-window
// formula; without the window the term is missing, as before.
func TestWindowPredictor_WalkScoresWindowTermModel(t *testing.T) {
	cat := model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Target: "m", Terms: []string{"lag_n"}, Kind: types.FitKindRegression}}
	attrs := []*types.Attribute{
		{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)},
		postFormula("c", `coef("m", "(intercept)") + coef("m", "lag_n") * lag_n`),
	}
	req := &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Attributes: attrs,
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "m", Predictors: []string{"p", "c"}}}}
	if got := fieldRefRefusalsWithModels(req, fieldRefSchema(), nil, nil, cat); len(got) != 0 {
		t.Fatalf("window-term scoring refused: %v", got)
	}
	pw := vectors.PostWindowAttributes(req, fieldRefSchema(), nil, cat)
	if !reflect.DeepEqual(pw.Post, []bool{true, true}) || !reflect.DeepEqual(pw.WindowReads, []string{"lag_n"}) {
		t.Fatalf("plan %+v, want both slots post-window reading lag_n", pw)
	}
	bare := &types.Request{Attributes: attrs[:1]}
	got := fieldRefRefusalsWithModels(bare, fieldRefSchema(), nil, nil, cat)
	if len(got) != 1 || got[0].Code != errors.PULSE_MODEL_PREDICTOR_MISSING {
		t.Fatalf("scoring with no window: %v", got)
	}
}

// TestWindowPredictor_PostWindowOpenWalk (FR-13): an extension feature
// opens the column set, yet the post-window classification — and with it
// the engine's ordering — is unchanged, a post-window formula and a
// regression on its label are admitted, and a window-dependent slot that
// cannot move is still PULSE_ATTRIBUTE_WINDOW_ORDER (judged before the
// open set admits the name).
func TestWindowPredictor_PostWindowOpenWalk(t *testing.T) {
	ext := []*types.Feature{{Type: "FEAT_ACME_EMBED", Field: "n"}}
	base := func() *types.Request {
		return &types.Request{
			Windows: []*types.Window{lagWindow("lag_n")},
			Attributes: []*types.Attribute{
				postFormula("sat", "hill(lag_n, 3, 2)"),
				postFormula("sat2", "sat + acme_out"),
				postFormula("other", "acme_out * 2"),
			},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "m", Predictors: []string{"sat2"}}},
		}
	}
	closed, open := base(), base()
	open.Features = ext
	if a, b := vectors.PostWindowAttributes(closed, fieldRefSchema(), nil, nil), vectors.PostWindowAttributes(open, fieldRefSchema(), nil, nil); !reflect.DeepEqual(a, b) {
		t.Fatalf("an open walk moved the plan: closed %+v, open %+v", a, b)
	} else if !reflect.DeepEqual(a.Post, []bool{true, true, false}) {
		t.Fatalf("plan %+v, want sat and sat2 post-window, other not", a)
	}
	if got := FieldRefRefusals(open, fieldRefSchema(), nil); len(got) != 0 {
		t.Fatalf("open walk refused a post-window chain: %v", got)
	}

	refused := base()
	refused.Features = ext
	refused.Attributes = append(refused.Attributes, &types.Attribute{Type: types.ATTR_ZSCORE, Field: "sat2", Label: "z"})
	got := windowOrderRefusals(t, FieldRefRefusals(refused, fieldRefSchema(), nil))
	if !reflect.DeepEqual(got, []map[string]any{{"attribute": "ATTR_ZSCORE", "field": "sat2"}}) {
		t.Fatalf("open walk: refusals %v", got)
	}

	grouped := base()
	grouped.Features = ext
	grouped.Regressions = nil
	grouped.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	got = windowOrderRefusals(t, FieldRefRefusals(grouped, fieldRefSchema(), nil))
	if !reflect.DeepEqual(got, []map[string]any{{"attribute": "ATTR_FORMULA", "field": "lag_n"}}) {
		t.Fatalf("open walk, grouped shape: refusals %v", got)
	}
}

// TestWindowPredictor_ExtensionAttributeWindowOrder: an extension
// attribute whose FieldInputs declare a window label (or a post-window
// label) is never row-local here — PULSE_ATTRIBUTE_WINDOW_ORDER, also
// under an open column set; a schema field it declares stays admitted.
func TestWindowPredictor_ExtensionAttributeWindowOrder(t *testing.T) {
	snap := &ExtensionsSnapshot{FieldInputs: map[string]func(json.RawMessage) []string{
		"attribute|ATTR_ACME_SCORE": func(json.RawMessage) []string { return []string{"n", "lag_n", "sat"} },
	}}
	for name, feats := range map[string][]*types.Feature{"closed": nil, "open": {{Type: "FEAT_ACME_EMBED", Field: "n"}}} {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{
				Features: feats,
				Windows:  []*types.Window{lagWindow("lag_n")},
				Attributes: []*types.Attribute{
					postFormula("sat", "hill(lag_n, 3, 2)"),
					{Type: "ATTR_ACME_SCORE", Field: "n", Label: "acme"},
				},
			}
			got := windowOrderRefusals(t, FieldRefRefusals(req, fieldRefSchema(), snap))
			want := []map[string]any{{"attribute": "ATTR_ACME_SCORE", "field": "lag_n"}, {"attribute": "ATTR_ACME_SCORE", "field": "sat"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("refusals %v, want %v", got, want)
			}
		})
	}
}
