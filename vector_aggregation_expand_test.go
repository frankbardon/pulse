package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Vector aggregation expand (U27 E2-S3): params.expand on AGG_VEC_*
// replaces the array value with one scalar column per member,
// "<label>[<element label>]". The keys are the same in every execution
// mode; predict, return planning and chain stage columns name them. An
// element column is an ordinary nullable f64 output column — Sort,
// windows and post-tests read it (U30 E4-S1) — while the bare array
// label stays refused there, and every overlay host still refuses both
// (PROCESSING_CONFIG, reason array_valued).

func vaggExpandParams() json.RawMessage { return json.RawMessage(`{"expand": true}`) }

// vaggExpandRequest is vaggRequest with both vector slots expanded.
func vaggExpandRequest(cohort string) *types.Request {
	req := vaggRequest(cohort)
	req.Aggregations[0].Params = vaggExpandParams()
	req.Aggregations[1].Params = vaggExpandParams()
	return req
}

var (
	vaggExpandM = []string{"m[one]", "m[two]", "m[three]"}
	vaggExpandS = []string{"s[b4]", "s[b2]"}
)

// vaggCheckExpanded asserts row carries cols (in member order) equal to
// want and not label.
func vaggCheckExpanded(t *testing.T, row map[string]any, label string, cols []string, want []float64) {
	t.Helper()
	if v, ok := row[label]; ok {
		t.Errorf("expanded row still carries %s = %v", label, v)
	}
	for i, c := range cols {
		v, ok := row[c]
		if !ok {
			t.Errorf("row lacks %s (keys %v)", c, vaggKeys(row))
			continue
		}
		f, ok := v.(float64)
		if !ok {
			t.Errorf("%s = %v (%T), want a float64", c, v, v)
			continue
		}
		if !vexprClose(f, want[i]) {
			t.Errorf("%s = %v, want %v", c, f, want[i])
		}
	}
}

func vaggKeys(row map[string]any) []string {
	out := make([]string, 0, len(row))
	for k := range row {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestVecAggExpand_EveryMode: every execution mode, ungrouped and
// grouped — the per-element columns equal the oracle, the arrays are
// absent, the row key set is exactly the expanded set, and predict
// names the same columns.
func TestVecAggExpand_EveryMode(t *testing.T) {
	ctx := context.Background()
	all := []int{0, 1, 2}
	pair := []int{3, 1}
	for _, m := range vexprModes() {
		t.Run(m.name, func(t *testing.T) {
			p, cohort := m.open(t)
			type variant struct{ grouped, twoPass bool }
			variants := []variant{{false, false}, {true, false}}
			if m.name == "streaming" {
				// A fitted attribute routes the streaming run through
				// the two-pass arm (its own row builder).
				variants = append(variants, variant{false, true})
			}
			for _, v := range variants {
				grouped := v.grouped
				t.Run(fmt.Sprintf("grouped=%v/two_pass=%v", grouped, v.twoPass), func(t *testing.T) {
					req := vaggExpandRequest(cohort)
					if grouped {
						req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}}
					}
					if m.buffered {
						req.Attributes = append(req.Attributes,
							&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "score", Label: "score_pct"})
					}
					if v.twoPass {
						req.Attributes = append(req.Attributes,
							&types.Attribute{Type: types.ATTR_ZSCORE, Field: "score", Label: "score_z"})
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					wantKeys := append(append([]string{"s_qty"}, vaggExpandM...), vaggExpandS...)
					if grouped {
						wantKeys = append(wantKeys, "qty")
					}
					sort.Strings(wantKeys)
					if !grouped {
						if len(resp.Data) != 1 {
							t.Fatalf("rows = %d", len(resp.Data))
						}
						row := resp.Data[0]
						vaggCheckExpanded(t, row, "m", vaggExpandM, vaggCompute(m.rows, all, nil).mean)
						vaggCheckExpanded(t, row, "s", vaggExpandS, vaggCompute(m.rows, pair, nil).sum)
						if got := vaggKeys(row); !reflect.DeepEqual(got, wantKeys) {
							t.Errorf("row keys = %v, want %v", got, wantKeys)
						}
						// Components are untouched by expand.
						if c := vaggComponents(resp, "m"); c == nil || fmt.Sprint(c.Operator["labels"]) != "[one two three]" {
							t.Errorf("m components = %+v", c)
						}
					} else {
						if len(resp.Data) != 7 {
							t.Fatalf("grouped rows = %d, want 7", len(resp.Data))
						}
						for _, row := range resp.Data {
							key := fmt.Sprint(row["qty"])
							keep := func(r int) bool { return fmt.Sprint(r%7) == key }
							vaggCheckExpanded(t, row, "m", vaggExpandM, vaggCompute(m.rows, all, keep).mean)
							vaggCheckExpanded(t, row, "s", vaggExpandS, vaggCompute(m.rows, pair, keep).sum)
							if got := vaggKeys(row); !reflect.DeepEqual(got, wantKeys) {
								t.Errorf("row keys = %v, want %v", got, wantKeys)
							}
						}
					}
					res, err := p.Predict(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Valid {
						t.Fatalf("predict refuses a request Process runs")
					}
					if got := res.Aggregations[0].Columns; !reflect.DeepEqual(got, vaggExpandM) {
						t.Errorf("predict m columns = %v, want %v", got, vaggExpandM)
					}
					if got := res.Aggregations[1].Columns; !reflect.DeepEqual(got, vaggExpandS) {
						t.Errorf("predict s columns = %v, want %v", got, vaggExpandS)
					}
					if got := res.Aggregations[2].Columns; got != nil {
						t.Errorf("scalar slot columns = %v, want omitted", got)
					}
				})
			}
		})
	}
}

// TestVecAggExpand_PredictOmitsColumnsWithoutExpand: an array slot
// (no expand) reports no columns — its one column is the label.
func TestVecAggExpand_PredictOmitsColumnsWithoutExpand(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Predict(ctx, vaggRequest("v.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range res.Aggregations {
		if a.Columns != nil {
			t.Errorf("aggregations[%d] columns = %v, want omitted", i, a.Columns)
		}
	}
}

// vaggRefusalCase is one request both Process and predict must refuse
// with code (and reason, when set).
type vaggRefusalCase struct {
	name   string
	req    func() *types.Request
	code   perr.Code
	reason string
}

func vaggCheckRefusals(t *testing.T, p *pulse.Pulse, data []byte, cases []vaggRefusalCase) {
	t.Helper()
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, perrRun := p.Process(ctx, c.req())
			ce := codeOf(t, perrRun)
			if ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Fatalf("Process = %s %v, want %s %s", ce.Code, ce.Details, c.code, c.reason)
			}
			env, err := p.PredictBytes(ctx, data, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) ||
				(c.reason != "" && env.Errors[0].Details["reason"] != c.reason) {
				t.Fatalf("predict = %+v, want %s %s", env.Errors, c.code, c.reason)
			}
		})
	}
}

// TestVecAggExpand_BareLabelRefused: the array label — expanded or
// not — is refused as a Sort key, a window's field / order_by /
// partition_by and a post-test field, and an expanded slot as a
// request overlay's host, by Process and predict alike; a duplicate
// element label is refused under expand only. An element column is
// NOT refused (TestVecAggExpand_ElementColumnsDownstream).
func TestVecAggExpand_BareLabelRefused(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	vec := func(expand bool) *types.Aggregation {
		a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m"}
		if expand {
			a.Params = vaggExpandParams()
		}
		return a
	}
	grouped := func(expand bool, mut func(r *types.Request)) func() *types.Request {
		return func() *types.Request {
			r := &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
				Aggregations: []*types.Aggregation{vec(expand), {Type: types.AGG_COUNT, Field: "qty", Label: "n"}}}
			mut(r)
			return r
		}
	}
	order := []types.OrderKey{{Field: "qty"}}
	cases := []vaggRefusalCase{
		{"sort label of expanded slot", grouped(true, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m"}} }), perr.PROCESSING_CONFIG, "array_valued"},
		{"sort array", grouped(false, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m"}} }), perr.PROCESSING_CONFIG, "array_valued"},
		{"window field label of expanded slot", grouped(true, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "m", OrderBy: order, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window field array", grouped(false, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "m", OrderBy: order, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window order_by array", grouped(false, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "n", OrderBy: []types.OrderKey{{Field: "m"}}, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window partition_by label of expanded slot", grouped(true, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "n", OrderBy: order, PartitionBy: []string{"m"}, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"post-test field array", grouped(false, func(r *types.Request) {
			r.PostTests = []*types.Test{{Type: types.TEST_PAIRED_T, Field: "m", Field2: "n"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"post-test field label of expanded slot", grouped(true, func(r *types.Request) {
			r.PostTests = []*types.Test{{Type: types.TEST_PAIRED_T, Field: "n", Field2: "m"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		// An element label the slot does not write is an unknown column,
		// refused with the generic message — not array_valued.
		{"sort unknown element", grouped(true, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m[b9]"}} }), perr.SERVICE_VALIDATION, ""},
		{"sort element of unexpanded slot", grouped(false, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m[b1]"}} }), perr.SERVICE_VALIDATION, ""},
		{"overlay host expanded", grouped(true, func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"duplicate element labels", grouped(true, func(r *types.Request) {
			r.Aggregations[0].Params = json.RawMessage(`{"expand": true, "labels": ["x", "x"]}`)
		}), perr.SERVICE_VALIDATION, "expand_duplicate_labels"},
		{"expand not a bool", grouped(true, func(r *types.Request) {
			r.Aggregations[0].Params = json.RawMessage(`{"expand": "yes"}`)
		}), perr.SERVICE_VALIDATION, "bad_params"},
	}
	vaggCheckRefusals(t, p, data, cases)

	// Duplicate labels without expand are fine (the array keeps order).
	req := grouped(false, func(r *types.Request) {
		r.Aggregations[0].Params = json.RawMessage(`{"labels": ["x", "x"]}`)
	})()
	if _, err := p.Process(context.Background(), req); err != nil {
		t.Fatalf("duplicate labels on an array slot refused: %v", err)
	}
}

// vaggElementCohort writes e.pulse — rows 1..7 of the vexpr battery —
// and v.pulse (rows 0..39). e.pulse is grouped by qty
// (one row per qty bucket): per bucket m[b1] / m[b2] are
//
//	qty   0     1     2     3     4     5     6
//	b1    7     1     2     3     4     NaN   6
//	b2    8.25  2.25  3.25  4.25  NaN   6.25  7.25
//
// — qty 5's b1 and qty 4's b2 are elements no row answered (NaN, null
// on the wire).
func vaggElementCohort(t *testing.T) (*pulse.Pulse, map[string][]byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "e.pulse", 1, 8)
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data := map[string][]byte{}
	for _, name := range []string{"e.pulse", "v.pulse"} {
		b, err := afero.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		data[name] = b
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

// vaggElementRun runs req through Process and asserts predict accepts
// it too (predict ≡ runtime: neither refuses an element column).
func vaggElementRun(t *testing.T, p *pulse.Pulse, data []byte, req func() *types.Request) *types.Response {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictBytes(ctx, data, req())
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused an element column: %+v", env.Errors)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatalf("Process refused an element column: %v", err)
	}
	return resp
}

// vaggQtyColumn reads the qty bucket key of every row, in row order.
func vaggQtyColumn(t *testing.T, rows []map[string]any, col string) []string {
	t.Helper()
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r[col])
	}
	return out
}

// TestVecAggExpand_ElementColumnsDownstream (U30 E4-S1): an expanded
// element column <label>[<elem>] is an ordinary nullable f64 output
// column — a Sort key, a window's field / order_by / partition_by and a
// post-test's field read it, predict and Process alike. An element no
// row answered (NaN) is a null there: it sorts last in both directions
// and a value-bearing window skips it.
func TestVecAggExpand_ElementColumnsDownstream(t *testing.T) {
	p, cohorts := vaggElementCohort(t)
	data := cohorts["e.pulse"]
	grouped := func(mut func(r *types.Request)) func() *types.Request {
		return func() *types.Request {
			r := &types.Request{Cohort: &types.Cohort{Filename: "e.pulse"},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m", Params: vaggExpandParams()},
					{Type: types.AGG_COUNT, Field: "qty", Label: "n"}}}
			mut(r)
			return r
		}
	}

	t.Run("sort", func(t *testing.T) {
		for _, c := range []struct {
			desc bool
			want []string
		}{
			{false, []string{"1", "2", "3", "4", "6", "0", "5"}},
			{true, []string{"0", "6", "4", "3", "2", "1", "5"}},
		} {
			resp := vaggElementRun(t, p, data, grouped(func(r *types.Request) {
				r.Sort = []types.OrderKey{{Field: "m[b1]", Desc: c.desc}}
			}))
			if got := vaggQtyColumn(t, resp.Data, "qty"); !slices.Equal(got, c.want) {
				t.Errorf("sort m[b1] desc=%v: qty order %v, want %v (the NaN element last)", c.desc, got, c.want)
			}
		}
	})

	t.Run("window field skips a NaN element", func(t *testing.T) {
		resp := vaggElementRun(t, p, data, grouped(func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_RUNNING_SUM, Field: "m[b1]", Label: "cum",
				OrderBy: []types.OrderKey{{Field: "qty"}}, Frame: &types.FrameSpec{Mode: "rows", Following: new(int)}}}
			r.Sort = []types.OrderKey{{Field: "qty"}}
		}))
		want := []float64{7, 8, 10, 13, 17, 17, 23}
		for i, row := range resp.Data {
			if got, ok := row["cum"].(float64); !ok || got != want[i] {
				t.Errorf("row %d (qty %v): cum = %v, want %v", i, row["qty"], row["cum"], want[i])
			}
		}
	})

	t.Run("window order_by orders a NaN element last", func(t *testing.T) {
		resp := vaggElementRun(t, p, data, grouped(func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn",
				OrderBy: []types.OrderKey{{Field: "m[b2]", Desc: true}}}}
			r.Sort = []types.OrderKey{{Field: "rn"}}
		}))
		want := []string{"0", "6", "5", "3", "2", "1", "4"}
		if got := vaggQtyColumn(t, resp.Data, "qty"); !slices.Equal(got, want) {
			t.Errorf("row_number over m[b2] desc: qty order %v, want %v", got, want)
		}
	})

	t.Run("window partition_by", func(t *testing.T) {
		resp := vaggElementRun(t, p, data, grouped(func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn",
				PartitionBy: []string{"m[b1]"}, OrderBy: []types.OrderKey{{Field: "qty"}}}}
		}))
		// Every m[b1] is distinct (one NaN): each partition holds one row.
		for _, row := range resp.Data {
			if row["rn"] != int64(1) && row["rn"] != 1 && row["rn"] != 1.0 {
				t.Errorf("qty %v: rn = %v (%T), want 1", row["qty"], row["rn"], row["rn"])
			}
		}
	})

	t.Run("post-test", func(t *testing.T) {
		post := func(cohort, f1, f2 string, aggs []*types.Aggregation) func() *types.Request {
			return func() *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
					Aggregations: aggs,
					PostTests:    []*types.Test{{Type: types.TEST_PAIRED_T, Field: f1, Field2: f2}}}
			}
		}
		elemAggs := []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m", Params: vaggExpandParams()}}
		// Every element answered (v.pulse): m[b1] / m[b2] are AGG_AVERAGE
		// of b1 / b2 per bucket, so the post-test equals the same
		// post-test over the scalar columns, bit for bit.
		elem := vaggElementRun(t, p, cohorts["v.pulse"], post("v.pulse", "m[b1]", "m[b2]", elemAggs))
		scalar := vaggElementRun(t, p, cohorts["v.pulse"], post("v.pulse", "a1", "a2", []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "b1", Label: "a1"},
			{Type: types.AGG_AVERAGE, Field: "b2", Label: "a2"}}))
		if len(elem.PostTests) != 1 || len(scalar.PostTests) != 1 {
			t.Fatalf("post-tests: element %d, scalar %d results", len(elem.PostTests), len(scalar.PostTests))
		}
		e, s := elem.PostTests[0], scalar.PostTests[0]
		if math.IsNaN(e.Statistic) || math.IsNaN(e.PValue) {
			t.Errorf("element post-test undefined: (%v, %v)", e.Statistic, e.PValue)
		}
		if !sameFloat(e.Statistic, s.Statistic) || !sameFloat(e.PValue, s.PValue) {
			t.Errorf("element post-test = (%v, %v), scalar twin = (%v, %v)", e.Statistic, e.PValue, s.Statistic, s.PValue)
		}
		// A NaN element (e.pulse) reaches the post-test like any other
		// undefined f64 figure: it runs, and its statistic is undefined
		// (null on the wire) — never refused, never read as 0.
		nan := vaggElementRun(t, p, data, post("e.pulse", "m[b1]", "m[b2]", elemAggs))
		if len(nan.PostTests) != 1 || !math.IsNaN(nan.PostTests[0].Statistic) {
			t.Errorf("post-test over a NaN element = %+v, want one result with an undefined statistic", nan.PostTests)
		}
		// So does a scalar aggregate's undefined figure (E4-S5, owner
		// decision: keep undefined): AGG_AVERAGE of b1 over the qty 5
		// bucket no row answered is NaN, the bucket's row is NOT
		// dropped, and the post-test's statistic and p-value are
		// undefined exactly as the element twin's — null on the wire.
		nanScalar := vaggElementRun(t, p, data, post("e.pulse", "a1", "a2", []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "b1", Label: "a1"},
			{Type: types.AGG_AVERAGE, Field: "b2", Label: "a2"}}))
		if len(nanScalar.PostTests) != 1 {
			t.Fatalf("post-tests over an undefined scalar column: %d results, want 1", len(nanScalar.PostTests))
		}
		ns := nanScalar.PostTests[0]
		if !math.IsNaN(ns.Statistic) || !math.IsNaN(ns.PValue) {
			t.Errorf("post-test over an undefined AGG_AVERAGE cell = (%v, %v), want undefined", ns.Statistic, ns.PValue)
		}
		if !sameFloat(ns.Statistic, nan.PostTests[0].Statistic) || !sameFloat(ns.PValue, nan.PostTests[0].PValue) {
			t.Errorf("scalar post-test = (%v, %v), element twin = (%v, %v)", ns.Statistic, ns.PValue, nan.PostTests[0].Statistic, nan.PostTests[0].PValue)
		}
		b, err := types.MarshalFinite(ns)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(b, &wire); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"statistic", "p_value"} {
			if v, ok := wire[k]; !ok || v != nil {
				t.Errorf("post-test %s on the wire = %v (present %v), want null", k, v, ok)
			}
		}
	})
}

// sameFloat is bit equality with NaN equal to NaN.
func sameFloat(a, b float64) bool {
	return math.Float64bits(a) == math.Float64bits(b) || (math.IsNaN(a) && math.IsNaN(b))
}

// TestVecAggExpand_ReturnPlanning: return paths are judged against the
// expanded keys — the bare label is not produced (refused, the valid
// list naming the element columns) and a prefix glob keeps every
// element column.
func TestVecAggExpand_ReturnPlanning(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(include string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m", Params: vaggExpandParams()},
				{Type: types.AGG_SUM, Field: "qty", Label: "q"},
			},
			Return: &types.Return{Include: []string{include}}}
	}
	_, err = p.Process(ctx, mk("data[*].m"))
	ce := codeOf(t, err)
	if ce.Code != perr.PULSE_RETURN_PATH_UNKNOWN {
		t.Fatalf("Process = %s %v, want PULSE_RETURN_PATH_UNKNOWN", ce.Code, ce.Details)
	}
	if valid := fmt.Sprint(ce.Details["valid"]); valid != "[m[b1] m[b2] q]" {
		t.Errorf("valid = %s, want the expanded columns", valid)
	}
	env, err := p.PredictBytes(ctx, data, mk("data[*].m"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_RETURN_PATH_UNKNOWN) {
		t.Fatalf("predict = %+v, want PULSE_RETURN_PATH_UNKNOWN", env.Errors)
	}

	resp, err := p.Process(ctx, mk("data[*].m*"))
	if err != nil {
		t.Fatalf("glob include: %v", err)
	}
	if got := vaggKeys(resp.Data[0]); !reflect.DeepEqual(got, []string{"m[b1]", "m[b2]"}) {
		t.Errorf("glob-kept keys = %v", got)
	}

	// A quoted segment names one element column: include keeps it
	// alone, exclude drops it alone, and an unknown element is refused
	// by predict and the runtime alike.
	resp, err = p.Process(ctx, mk(`data[*]."m[b1]"`))
	if err != nil {
		t.Fatalf("quoted include: %v", err)
	}
	if got := vaggKeys(resp.Data[0]); !reflect.DeepEqual(got, []string{"m[b1]"}) {
		t.Errorf("quoted-include keys = %v", got)
	}
	ex := mk("data")
	ex.Return.Exclude = []string{`data[*]."m[b2]"`}
	resp, err = p.Process(ctx, ex)
	if err != nil {
		t.Fatalf("quoted exclude: %v", err)
	}
	if got := vaggKeys(resp.Data[0]); !reflect.DeepEqual(got, []string{"m[b1]", "q"}) {
		t.Errorf("quoted-exclude keys = %v", got)
	}
	_, err = p.Process(ctx, mk(`data[*]."m[zz]"`))
	if ce := codeOf(t, err); ce.Code != perr.PULSE_RETURN_PATH_UNKNOWN || ce.Details["at"] != "m[zz]" {
		t.Fatalf("Process = %s %v, want PULSE_RETURN_PATH_UNKNOWN at m[zz]", ce.Code, ce.Details)
	}
	env, err = p.PredictBytes(ctx, data, mk(`data[*]."m[zz]"`))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_RETURN_PATH_UNKNOWN) {
		t.Fatalf("predict = %+v, want PULSE_RETURN_PATH_UNKNOWN", env.Errors)
	}
	env, err = p.PredictBytes(ctx, data, mk(`data[*]."m[b1]"`))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict of a quoted element include = %+v, want no error", env.Errors)
	}
}

// TestVecAggExpand_ComposeAndChain: Compose slots keep their expanded
// keys; a ProcessChain's final stage writes them and PredictChain's
// stage columns name them; Compose and whole-chain overlays scoring a
// vector aggregation by its bare label (here by default, as
// aggregations[0]) — expanded or not — are refused by the runtime and
// predict alike. Scoring an element column runs
// (TestComposeOverlay_ScoresElementColumn).
func TestVecAggExpand_ComposeAndChain(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	qty := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}} }
	slot := func(label string, expand bool) *types.Request {
		a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m"}
		if expand {
			a.Params = vaggExpandParams()
		}
		return &types.Request{Label: label, Cohort: &types.Cohort{Filename: "v.pulse"}, Groups: qty(),
			Aggregations: []*types.Aggregation{a}}
	}

	// Compose: each slot answers with the expanded keys.
	out, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{slot("a", true), slot("b", true)}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for i, r := range out.Responses {
		for _, row := range r.Data {
			if got := vaggKeys(row); !reflect.DeepEqual(got, []string{"m[b1]", "m[b2]", "qty"}) {
				t.Fatalf("slot %d keys = %v", i, got)
			}
		}
	}

	// Compose overlay over vector slots: refused.
	for _, expand := range []bool{false, true} {
		t.Run(fmt.Sprintf("compose_overlay/expand=%v", expand), func(t *testing.T) {
			req := &pulse.ComposedRequest{
				Requests: []*types.Request{slot("a", expand), slot("b", expand)},
				Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindIndexVsRef, Reference: "a", Targets: []string{"b"}}},
			}
			_, err := p.Compose(ctx, req)
			if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != "array_valued" {
				t.Fatalf("Compose = %s %v, want PROCESSING_CONFIG array_valued", ce.Code, ce.Details)
			}
			env, err := p.PredictCompose(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if !vaggHasReason(env, perr.PROCESSING_CONFIG, "array_valued") {
				t.Fatalf("PredictCompose = %+v, want PROCESSING_CONFIG array_valued", env.Errors)
			}
		})
	}

	// ProcessChain: the final stage writes the expanded keys and
	// predict names them as the stage's columns.
	chain := func(expand bool, overlay bool) *pulse.ChainRequest {
		// A one-stage chain: its vector stage is both the result and the
		// overlay's target and reference. (An expanding stage feeding
		// another: TestChain_ElementColumnsFeedNextStage.)
		final := slot("", expand)
		final.Cohort = nil
		req := &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Stages: []*types.ChainStage{
			{Name: "profile", Request: final},
		}}
		if overlay {
			zero := 0
			req.Overlays = []*types.ChainOverlaySpec{{Kind: types.OverlayKindIndexVsStage, Ref: types.StageRef{Index: &zero}}}
		}
		return req
	}
	cresp, err := p.ProcessChain(ctx, chain(true, false))
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	for _, row := range cresp.Final.Data {
		if got := vaggKeys(row); !reflect.DeepEqual(got, []string{"m[b1]", "m[b2]", "qty"}) {
			t.Fatalf("chain final keys = %v", got)
		}
	}
	cenv, err := p.PredictChain(ctx, chain(true, false))
	if err != nil || len(cenv.Errors) != 0 {
		t.Fatalf("PredictChain = %v %+v", err, cenv.Errors)
	}
	cres, ok := cenv.Data.(*pulse.ChainPredictResult)
	if !ok {
		t.Fatalf("PredictChain data = %T", cenv.Data)
	}
	if last := cres.StageSchemas[len(cres.StageSchemas)-1]; !slices.Equal(last, []string{"qty", "m[b1]", "m[b2]"}) {
		t.Errorf("final stage columns = %v", last)
	}

	// Whole-chain overlay over the vector stage: refused.
	for _, expand := range []bool{false, true} {
		t.Run(fmt.Sprintf("chain_overlay/expand=%v", expand), func(t *testing.T) {
			_, err := p.ProcessChain(ctx, chain(expand, true))
			if ce := codeOf(t, err); ce.Code != perr.PROCESSING_CONFIG || ce.Details["reason"] != "array_valued" {
				t.Fatalf("ProcessChain = %s %v, want PROCESSING_CONFIG array_valued", ce.Code, ce.Details)
			}
			env, err := p.PredictChain(ctx, chain(expand, true))
			if err != nil {
				t.Fatal(err)
			}
			if !vaggHasReason(env, perr.PROCESSING_CONFIG, "array_valued") {
				t.Fatalf("PredictChain = %+v, want PROCESSING_CONFIG array_valued", env.Errors)
			}
		})
	}
}

func vaggHasReason(env *descriptor.Envelope, code perr.Code, reason string) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) && e.Details["reason"] == reason {
			return true
		}
	}
	return false
}
