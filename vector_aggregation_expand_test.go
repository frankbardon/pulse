package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
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
// mode; predict, return planning and chain stage columns name them; and
// the columns stay presentation only — Sort, windows, post-tests and
// every overlay host refuse them (PROCESSING_CONFIG, reason
// array_valued).

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

// TestVecAggExpand_PresentationOnly: the expanded columns — and the
// array label — are refused as a Sort key, a window's field / order_by
// / partition_by, a post-test field and a request overlay's host, by
// Process and predict alike; a duplicate element label is refused
// under expand only.
func TestVecAggExpand_PresentationOnly(t *testing.T) {
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
		{"sort expanded column", grouped(true, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m[b1]"}} }), perr.PROCESSING_CONFIG, "array_valued"},
		{"sort label of expanded slot", grouped(true, func(r *types.Request) { r.Sort = []types.OrderKey{{Field: "m"}} }), perr.PROCESSING_CONFIG, "array_valued"},
		{"window field expanded column", grouped(true, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "m[b2]", OrderBy: order, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window field array", grouped(false, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "m", OrderBy: order, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window order_by expanded column", grouped(true, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "n", OrderBy: []types.OrderKey{{Field: "m[b1]"}}, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"window partition_by expanded column", grouped(true, func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "n", OrderBy: order, PartitionBy: []string{"m[b1]"}, Label: "lag"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
		{"post-test field expanded column", grouped(true, func(r *types.Request) {
			r.PostTests = []*types.Test{{Type: types.TEST_ANOVA_F, Field: "m[b1]", SplitBy: "qty"}}
		}), perr.PROCESSING_CONFIG, "array_valued"},
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
}

// TestVecAggExpand_ComposeAndChain: Compose slots keep their expanded
// keys; a ProcessChain's final stage writes them and PredictChain's
// stage columns name them; Compose and whole-chain overlays reading a
// slot / stage with a vector aggregation — expanded or not — are
// refused by the runtime and predict alike.
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
		// The vector stage is the last (it may not feed another), so a
		// one-stage chain: its stage is both the result and the
		// overlay's target and reference.
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
