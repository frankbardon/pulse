package crosstabfuse_test

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/crosstabfuse"
	"github.com/frankbardon/pulse/types"
)

// fakeFacts answers built-ins from the types tables and the static
// keyability table, plus a configurable extension set — enough to drive
// every rule branch without the engine.
type fakeFacts struct {
	extAggs     map[types.AggregationType]bool // name → declared mergeable
	extAttrs    map[types.AttributeType]bool   // name → two-pass
	extGroupers map[types.GroupType]bool       // name → keyable
	fieldInputs map[string]bool
}

func (f fakeFacts) AggregatorMergeable(t types.AggregationType) bool {
	if m, ok := f.extAggs[t]; ok {
		return m
	}
	return t.Mergeable()
}
func (f fakeFacts) AggregatorMarginClass(t types.AggregationType) types.MarginReducibility {
	if _, ok := f.extAggs[t]; ok {
		return types.MarginSummable
	}
	return t.MarginReducibility()
}
func (f fakeFacts) AttributeTwoPass(t types.AttributeType) bool {
	return t == types.ATTR_ZSCORE || f.extAttrs[t]
}
func (f fakeFacts) GrouperKeyable(g *types.Group, _ *encoding.Schema) bool {
	if k, ok := f.extGroupers[g.Type]; ok {
		return k
	}
	k, _ := crosstabfuse.BuiltinGrouperKeyable(g.Type)
	return k
}
func (f fakeFacts) IsExtension(category, name string) bool {
	switch category {
	case "aggregator":
		_, ok := f.extAggs[types.AggregationType(name)]
		return ok
	case "attribute":
		_, ok := f.extAttrs[types.AttributeType(name)]
		return ok
	case "grouper":
		_, ok := f.extGroupers[types.GroupType(name)]
		return ok
	}
	return false
}
func (f fakeFacts) HasFieldInputs(category, name string) bool {
	return f.fieldInputs[category+"|"+name]
}

func gateSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
	}}
}

func happy() *types.Request {
	return &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
		Columns: []*types.Group{{Type: types.GROUP_DATE, Field: "ts"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
	}}
}

func TestDecide(t *testing.T) {
	const extAgg types.AggregationType = "AGG_ACME_SUM"
	const extGrp types.GroupType = "GROUP_ACME_KEY"
	facts := fakeFacts{
		extAggs:     map[types.AggregationType]bool{extAgg: true},
		extGroupers: map[types.GroupType]bool{extGrp: false},
		fieldInputs: map[string]bool{"aggregator|" + string(extAgg): true},
	}
	cases := []struct {
		name   string
		mutate func(*types.Request)
		want   []string
	}{
		{"happy path fuses", func(*types.Request) {}, nil},
		{"overlays never block", func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ}}
		}, nil},
		{"joins are terminal", func(r *types.Request) {
			r.Joins = []*types.JoinSpec{{}}
			r.Features = []*types.Feature{{}}
		}, []string{"joins force buffered"}},
		{"non-mergeable cell skips the margin-class check", func(r *types.Request) {
			r.Crosstab.Cell.Type = types.AGG_MEDIAN
		}, []string{"non-mergeable cell aggregator (AGG_MEDIAN)"}},
		{"built-in decimal cell", func(r *types.Request) {
			r.Crosstab.Cell.Field = "amount"
		}, []string{"decimal128 cell field (amount)"}},
		{"extension decimal cell is exempt", func(r *types.Request) {
			r.Crosstab.Cell = &types.Aggregation{Type: extAgg, Field: "amount"}
		}, nil},
		{"aux: malformed skipped, non-mergeable and decimal declined", func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{
				nil, {Field: "score"},
				{Type: types.AGG_MEDIAN, Field: "score"},
				{Type: types.AGG_SUM, Field: "amount"},
			}
		}, []string{"non-mergeable margin aggregation (AGG_MEDIAN)", "decimal128 margin aggregation field (amount)"}},
		{"every unkeyable grouper on both axes", func(r *types.Request) {
			r.Crosstab.Rows = append(r.Crosstab.Rows, &types.Group{Type: types.GROUP_QUANTILE}, nil)
			r.Crosstab.Columns = []*types.Group{{Type: extGrp}}
		}, []string{
			"non-streamable grouper on row axis (GROUP_QUANTILE)",
			"nil grouper on row axis",
			"non-streamable grouper on column axis (GROUP_ACME_KEY)",
			"extension grouper GROUP_ACME_KEY without FieldInputs",
		}},
		{"features, tests, two-pass, formula, expression in rule order", func(r *types.Request) {
			r.Features = []*types.Feature{{}}
			r.PostTests = []*types.Test{{}}
			r.Attributes = []*types.Attribute{
				{Type: types.ATTR_ZSCORE},
				{Type: types.ATTR_FORMULA, Expression: "score * 2"},
				{Type: types.ATTR_FORMULA, Expression: "score * 3"},
			}
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION}, {Type: types.FILTER_EXPRESSION}}
		}, []string{
			"features force buffered",
			"stat tests force buffered",
			"two-pass attribute (ATTR_ZSCORE)",
			"ATTR_FORMULA bail",
			"FILTER_EXPRESSION bail",
		}},
		{"missing cell", func(r *types.Request) { r.Crosstab.Cell = nil }, []string{"missing cell aggregator"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := happy()
			c.mutate(req)
			ok, got := crosstabfuse.Decide(req, gateSchema(), facts)
			if ok != (len(c.want) == 0) || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Decide = (%v, %q), want %q", ok, got, c.want)
			}
		})
	}
}

func TestDecide_Degenerate(t *testing.T) {
	for _, c := range []struct {
		name  string
		req   *types.Request
		facts crosstabfuse.Facts
		want  string
	}{
		{"nil request", nil, fakeFacts{}, "nil request"},
		{"no crosstab", &types.Request{}, fakeFacts{}, "no crosstab spec"},
		{"nil facts", happy(), nil, "no fusion facts"},
	} {
		ok, got := crosstabfuse.Decide(c.req, nil, c.facts)
		if ok || len(got) != 1 || got[0] != c.want {
			t.Errorf("%s: Decide = (%v, %q), want [%q]", c.name, ok, got, c.want)
		}
	}
	// A nil schema skips the decimal checks rather than panicking.
	req := happy()
	req.Crosstab.Cell.Field = "amount"
	if ok, got := crosstabfuse.Decide(req, nil, fakeFacts{}); !ok {
		t.Errorf("nil schema: Decide = (false, %q), want true", got)
	}
}
