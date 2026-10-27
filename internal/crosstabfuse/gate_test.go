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
	bufAttrs    map[types.AttributeType]bool   // extension name → buffered-only
	extGroupers map[types.GroupType]bool       // name → keyable
	fieldInputs map[string]bool
	hidden      map[string]bool
	slotTwoPass func(*types.Attribute) bool
	slotBound   func(*types.Attribute) bool
	slotClass   func(*types.Aggregation) (types.MarginReducibility, bool)
}

func (f fakeFacts) Hidden(name string) bool { return f.hidden[name] }

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
func (f fakeFacts) AggregatorSlotMarginClass(a *types.Aggregation) (types.MarginReducibility, bool) {
	if f.slotClass != nil {
		return f.slotClass(a)
	}
	return "", false
}
func (f fakeFacts) AttributeTwoPass(t types.AttributeType) bool {
	return t == types.ATTR_ZSCORE || f.extAttrs[t]
}
func (f fakeFacts) AttributeSlotTwoPass(a *types.Attribute, _ []types.VectorSpec, _ *encoding.Schema) bool {
	return f.slotTwoPass != nil && f.slotTwoPass(a)
}
func (f fakeFacts) AttributeBuffered(t types.AttributeType) bool {
	if f.hidden[string(t)] {
		return false
	}
	return (t == types.ATTR_PERCENTILE && !t.Streamable()) || f.bufAttrs[t]
}
func (f fakeFacts) AttributeSlotBound(a *types.Attribute) bool {
	return f.slotBound != nil && f.slotBound(a)
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
	t.Parallel()

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
		{"windows", func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "score", Label: "lag"}}
		}, []string{"windows force buffered"}},
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
	t.Parallel()

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

// TestDecide_HiddenNameKeyedBails pins that the two name-keyed bails
// answer a name the instance hides exactly as a never-registered one:
// no decline (the request is refused before dispatch anyway), so
// predict on a profiled instance cannot reveal a hidden operator.
func TestDecide_HiddenNameKeyedBails(t *testing.T) {
	t.Parallel()

	req := happy()
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score * 2"}}
	req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION}}
	hidden := fakeFacts{hidden: map[string]bool{
		string(types.ATTR_FORMULA): true, string(types.FILTER_EXPRESSION): true,
	}}
	if ok, why := crosstabfuse.Decide(req, gateSchema(), hidden); !ok {
		t.Fatalf("hidden formula / expression declined: %q", why)
	}
	if ok, _ := crosstabfuse.Decide(req, gateSchema(), fakeFacts{}); ok {
		t.Fatal("visible formula / expression fused")
	}
}

// TestDecide_BoundSlotIsRowLocal: a slot the facts report bound to a
// prebuilt fit (AttributeSlotBound) never declines as two-pass, whatever
// its type's or its metric's two-pass fact says; the same slot unbound
// declines.
func TestDecide_BoundSlotIsRowLocal(t *testing.T) {
	t.Parallel()

	req := happy()
	req.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Label: "bound"}, {Type: types.ATTR_VEC_DISTANCE, Label: "bound"}}
	slot := func(a *types.Attribute) bool { return a.Type == types.ATTR_VEC_DISTANCE }
	bound := fakeFacts{slotTwoPass: slot, slotBound: func(a *types.Attribute) bool { return a.Label == "bound" }}
	if ok, why := crosstabfuse.Decide(req, gateSchema(), bound); !ok {
		t.Fatalf("bound slots declined: %q", why)
	}
	ok, why := crosstabfuse.Decide(req, gateSchema(), fakeFacts{slotTwoPass: slot})
	want := []string{"two-pass attribute (ATTR_ZSCORE)", "two-pass attribute (ATTR_VEC_DISTANCE)"}
	if ok || !reflect.DeepEqual(why, want) {
		t.Fatalf("unbound: Decide = (%v, %q), want %q", ok, why, want)
	}
}

// TestDecide_SlotMarginClass: a cell whose SLOT class the facts report
// (a vector element slot, AggregatorSlotMarginClass) fuses on that
// class; the same type without the slot class declines on its own.
func TestDecide_SlotMarginClass(t *testing.T) {
	t.Parallel()

	req := happy()
	req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"score"}, Label: "elem"}
	slot := fakeFacts{slotClass: func(a *types.Aggregation) (types.MarginReducibility, bool) {
		return types.MarginMeanReducible, a.Label == "elem"
	}}
	if ok, why := crosstabfuse.Decide(req, gateSchema(), slot); !ok {
		t.Fatalf("element slot declined: %q", why)
	}
	ok, why := crosstabfuse.Decide(req, gateSchema(), fakeFacts{})
	want := []string{"recompute-margin cell aggregator (AGG_VEC_MEAN)"}
	if ok || !reflect.DeepEqual(why, want) {
		t.Fatalf("no slot class: Decide = (%v, %q), want %q", ok, why, want)
	}
}

// TestDecide_BufferedAttributeDeclines: a buffered-only attribute (the
// built-in ATTR_PERCENTILE, an extension declared not streamable) has
// no per-record arm, so the fused crosstab cannot score it — it declines
// once per slot, in slot order beside the two-pass rule; a hidden one
// answers as never-registered (no decline).
func TestDecide_BufferedAttributeDeclines(t *testing.T) {
	t.Parallel()

	req := happy()
	req.Attributes = []*types.Attribute{
		{Type: types.ATTR_PERCENTILE, Label: "p"},
		{Type: types.ATTR_ZSCORE, Label: "z"},
		{Type: "ATTR_ACME_BUF", Label: "b"},
	}
	facts := fakeFacts{bufAttrs: map[types.AttributeType]bool{"ATTR_ACME_BUF": true}}
	ok, why := crosstabfuse.Decide(req, gateSchema(), facts)
	want := []string{"buffered attribute (ATTR_PERCENTILE)", "two-pass attribute (ATTR_ZSCORE)", "buffered attribute (ATTR_ACME_BUF)"}
	if ok || !reflect.DeepEqual(why, want) {
		t.Fatalf("Decide = (%v, %q), want %q", ok, why, want)
	}
	req.Attributes = req.Attributes[:1]
	hidden := fakeFacts{hidden: map[string]bool{string(types.ATTR_PERCENTILE): true}}
	if ok, why := crosstabfuse.Decide(req, gateSchema(), hidden); !ok {
		t.Fatalf("hidden ATTR_PERCENTILE declined: %q", why)
	}
}
