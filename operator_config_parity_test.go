package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// operator_config_parity_test.go pins the operator config faults the
// constructors refuse with PROCESSING_CONFIG (#781) to predict: every
// case below is refused by Predict with the runtime's code, message and
// details, and its well-formed twin passes both.

const opConfigCohort = "opconfig.pulse"

const opConfigCSV = "region,quarter,age,on,tags\n" +
	"north,q1,20,true,a|b\n" +
	"north,q2,27,false,b\n" +
	"north,q3,34,true,\n" +
	"south,q1,41,false,a|c\n" +
	"south,q2,48,true,c\n" +
	"south,q3,55,false,a\n" +
	"east,q1,62,true,b|c\n" +
	"east,q2,24,false,a\n" +
	"east,q3,31,true,a|b|c\n" +
	"west,q1,38,false,\n" +
	"west,q2,45,true,c\n" +
	"west,q3,52,false,b\n"

// opConfigPulse imports the parity cohort: two categoricals (every
// region × quarter cell present, so a partition's series is its whole
// quarter dictionary), a float, a packed_bool and a set_u8 whose
// dictionary is {a, b, c}.
func opConfigPulse(t *testing.T) *Pulse {
	t.Helper()
	p, _ := memBuilderEngine(t)
	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "quarter", Type: encoding.FieldTypeCategoricalU8},
		{Name: "age", Type: encoding.FieldTypeF64},
		{Name: "on", Type: encoding.FieldTypePackedBool},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Operator-config parity column " + fields[i].Name + "."
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	s := encoding.Schema{Fields: fields}
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(opConfigCSV), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(src, opConfigCohort)
	job.Schema = &s
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return p
}

type opConfigCase struct {
	name string
	// refuse is false for the well-formed twin, which both sides run.
	refuse bool
	build  func(r *Request)
}

func opConfigAgg(op types.AggregationType, field, params string) func(r *Request) {
	return func(r *Request) {
		a := &types.Aggregation{Type: op, Field: field, Label: "v"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		r.Aggregations = []*types.Aggregation{a}
	}
}

func opConfigAttr(op types.AttributeType, field, params string) func(r *Request) {
	return func(r *Request) {
		a := &types.Attribute{Type: op, Field: field, Label: "flag"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		r.Attributes = []*types.Attribute{a}
	}
}

func opConfigFilter(op types.FiltererType, field string, values ...string) func(r *Request) {
	return func(r *Request) {
		r.Filterers = []*types.Filterer{{Type: op, Field: field, Values: values}}
	}
}

func opConfigBaseline(kind types.OverlayKind, position int, groups ...string) func(r *Request) {
	return func(r *Request) {
		r.Groups = nil
		for _, g := range groups {
			r.Groups = append(r.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: g})
		}
		r.Overlays = []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: position}}}}
	}
}

func opConfigCases() []opConfigCase {
	var cs []opConfigCase
	add := func(name string, refuse bool, b func(r *Request)) {
		cs = append(cs, opConfigCase{name: name, refuse: refuse, build: b})
	}
	// AGG_DISTINCT_SUM params.distinct_by.
	add("distinct_sum/ok", false, opConfigAgg(types.AGG_DISTINCT_SUM, "age", `{"distinct_by":"region"}`))
	add("distinct_sum/no_params", true, opConfigAgg(types.AGG_DISTINCT_SUM, "age", ""))
	add("distinct_sum/empty", true, opConfigAgg(types.AGG_DISTINCT_SUM, "age", `{"distinct_by":""}`))
	// AGG_CI_LOWER / AGG_CI_UPPER method and confidence.
	for _, op := range []types.AggregationType{types.AGG_CI_LOWER, types.AGG_CI_UPPER} {
		n := string(op)
		add(n+"/ok", false, opConfigAgg(op, "age", `{"method":"normal","confidence":0.9}`))
		add(n+"/bootstrap", true, opConfigAgg(op, "age", `{"method":"bootstrap"}`))
		add(n+"/method_unknown", true, opConfigAgg(op, "age", `{"method":"exact"}`))
		add(n+"/confidence_high", true, opConfigAgg(op, "age", `{"confidence":1.5}`))
		add(n+"/confidence_negative", true, opConfigAgg(op, "age", `{"confidence":-0.2}`))
	}
	// The same rules in a crosstab cell and a margin aggregation.
	cell := func(op types.AggregationType, params string, margin bool) func(r *Request) {
		return func(r *Request) {
			a := &types.Aggregation{Type: op, Field: "age", Params: json.RawMessage(params)}
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Cell: a, Margins: types.CrosstabMargins{Grand: true}}
			if margin {
				r.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "age"}
				r.Crosstab.MarginAggregations = []*types.Aggregation{a}
			}
		}
	}
	add("crosstab_cell/ci_bootstrap", true, cell(types.AGG_CI_LOWER, `{"method":"bootstrap"}`, false))
	add("crosstab_cell/distinct_sum", true, cell(types.AGG_DISTINCT_SUM, `{}`, false))
	add("crosstab_margin/ci_bootstrap", true, cell(types.AGG_CI_UPPER, `{"method":"bootstrap"}`, true))
	// ATTR_SET_HAS params.label.
	add("set_has/ok", false, opConfigAttr(types.ATTR_SET_HAS, "tags", `{"label":"b"}`))
	add("set_has/no_params", true, opConfigAttr(types.ATTR_SET_HAS, "tags", ""))
	add("set_has/empty_label", true, opConfigAttr(types.ATTR_SET_HAS, "tags", `{"label":""}`))
	add("set_has/unknown_label", true, opConfigAttr(types.ATTR_SET_HAS, "tags", `{"label":"zz"}`))
	add("set_has/label_wrong_type", true, opConfigAttr(types.ATTR_SET_HAS, "tags", `{"label":5}`))
	add("ci/confidence_wrong_type", true, opConfigAgg(types.AGG_CI_LOWER, "age", `{"confidence":"high"}`))
	add("distinct_sum/wrong_type", true, opConfigAgg(types.AGG_DISTINCT_SUM, "age", `{"distinct_by":5}`))
	// FILTER_NULL values mode.
	add("filter_null/ok", false, opConfigFilter(types.FILTER_NULL, "tags", "is_not_null"))
	add("filter_null/missing", true, opConfigFilter(types.FILTER_NULL, "tags"))
	add("filter_null/two", true, opConfigFilter(types.FILTER_NULL, "tags", "is_null", "is_not_null"))
	add("filter_null/unknown", true, opConfigFilter(types.FILTER_NULL, "tags", "null"))
	// FILTER_TRUE / FILTER_FALSE values mode.
	for _, op := range []types.FiltererType{types.FILTER_TRUE, types.FILTER_FALSE} {
		n := string(op)
		add(n+"/ok", false, opConfigFilter(op, "on", "strict"))
		add(n+"/unknown", true, opConfigFilter(op, "on", "loose"))
		add(n+"/two", true, opConfigFilter(op, "on", "truthy", "strict"))
	}
	// FILTER_RANGE value count and bounds.
	add("filter_range/ok", false, opConfigFilter(types.FILTER_RANGE, "age", "20", "50"))
	add("filter_range/one", true, opConfigFilter(types.FILTER_RANGE, "age", "20"))
	add("filter_range/three", true, opConfigFilter(types.FILTER_RANGE, "age", "1", "2", "3"))
	add("filter_range/min_nan", true, opConfigFilter(types.FILTER_RANGE, "age", "low", "50"))
	add("filter_range/max_nan", true, opConfigFilter(types.FILTER_RANGE, "age", "20", "high"))
	// FILTER_SET_* labels.
	for _, op := range []types.FiltererType{types.FILTER_SET_CONTAINS_ANY, types.FILTER_SET_CONTAINS_ALL,
		types.FILTER_SET_CONTAINS_NONE, types.FILTER_SET_EQUALS} {
		n := string(op)
		add(n+"/ok", false, opConfigFilter(op, "tags", "a", "c"))
		add(n+"/unknown_label", true, opConfigFilter(op, "tags", "a", "zz"))
	}
	// *_vs_baseline ref.baseline_index.position over categorical groupers.
	for _, kind := range []types.OverlayKind{types.OverlayKindDeltaVsBaseline, types.OverlayKindIndexVsBaseline} {
		n := string(kind)
		add(n+"/ok", false, opConfigBaseline(kind, 3, "region"))
		add(n+"/past_dictionary", true, opConfigBaseline(kind, 4, "region"))
		add(n+"/partitioned_ok", false, opConfigBaseline(kind, 2, "region", "quarter"))
		add(n+"/partitioned_past_series", true, opConfigBaseline(kind, 3, "region", "quarter"))
	}
	return cs
}

// TestOperatorConfig_PredictMatchesRuntime: every operator config fault
// in opConfigCases is refused by Predict with the runtime's first
// error — code, message and details — and every well-formed twin runs.
func TestOperatorConfig_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	for _, tc := range opConfigCases() {
		build := func() *Request {
			r := &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "v"}}}
			tc.build(r)
			return r
		}
		t.Run(tc.name, func(t *testing.T) {
			assertPredictRuntimeParity(t, p, build, tc.refuse)
		})
	}
}

// assertPredictRuntimeParity runs build's request through Predict and
// Process: a well-formed one (refuse false) passes both; otherwise
// Predict reports exactly the runtime's one refusal — code, message and
// details.
func assertPredictRuntimeParity(t *testing.T, p *Pulse, build func() *Request, refuse bool) {
	t.Helper()
	ctx := context.Background()
	res, err := p.Predict(ctx, build())
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := p.Process(ctx, build())
	if !refuse {
		if len(res.Errors) != 0 || rerr != nil {
			t.Fatalf("well-formed request refused: predict %+v runtime %v", res.Errors, rerr)
		}
		return
	}
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("runtime err = %v, want a coded refusal", rerr)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("predict accepted what the runtime refused: %s %q", ce.Code, ce.Message)
	}
	if len(res.Errors) != 1 {
		t.Errorf("predict reported %d errors, want the runtime's one: %+v", len(res.Errors), res.Errors)
	}
	pe := res.Errors[0]
	if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(normalizeDetails(ce.Details), normalizeDetails(pe.Details)) {
		t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
	}
}

// normalizeDetails round-trips details through JSON so a typed int on
// one side and a decoded float64 on the other compare equal.
func normalizeDetails(d map[string]any) map[string]any {
	if len(d) == 0 {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return d
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return d
	}
	return out
}

// TestOperatorConfig_FacetAndStreamMatchRuntime: the filterer config
// faults are refused by PredictFacet as FacetSchema refuses them, and
// the aggregation / filterer faults by ProcessStream as by Process.
func TestOperatorConfig_FacetAndStreamMatchRuntime(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	ctx := context.Background()
	filters := map[string]*types.Filterer{
		"null":  {Type: types.FILTER_NULL, Field: "tags", Values: []string{"null"}},
		"true":  {Type: types.FILTER_TRUE, Field: "on", Values: []string{"loose"}},
		"range": {Type: types.FILTER_RANGE, Field: "age", Values: []string{"1"}},
		"set":   {Type: types.FILTER_SET_CONTAINS_ANY, Field: "tags", Values: []string{"zz"}},
	}
	for name, f := range filters {
		t.Run("facet/"+name, func(t *testing.T) {
			req := &FacetRequest{Cohort: &types.Cohort{Filename: opConfigCohort}, Fields: []string{"region"},
				Filterers: []*types.Filterer{f}}
			env, err := p.PredictFacet(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := p.FacetSchema(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(rerr, &ce) {
				t.Fatalf("runtime err = %v, want a coded refusal", rerr)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
	for _, tc := range opConfigCases() {
		if !tc.refuse {
			continue
		}
		t.Run("stream/"+tc.name, func(t *testing.T) {
			build := func() *Request {
				r := &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "v"}}}
				tc.build(r)
				return r
			}
			if r := build(); r.Crosstab != nil || len(r.Overlays) > 0 || len(r.Attributes) > 0 {
				return
			}
			_, rerr := p.Process(ctx, build())
			_, serr := p.ProcessStream(ctx, build())
			var ce, se *perr.CodedError
			if !stderrors.As(rerr, &ce) || !stderrors.As(serr, &se) || ce.Code != se.Code || ce.Message != se.Message {
				t.Fatalf("Process %v, ProcessStream %v", rerr, serr)
			}
		})
	}
}
