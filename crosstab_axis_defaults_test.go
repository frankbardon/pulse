package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A crosstab axis grouper naming a field but no type takes the smart
// default of the field's schema type, exactly as a top-level group does
// — on predict and at runtime, which share descriptor.ResolveDefaults.
// Before, predict accepted the typeless axis and the runtime refused it
// ("unknown group type: ").

// typelessAxisCrosstab is a crosstab over the union oracle fixture whose
// row grouper is rowType (empty: typeless) on rowField, and whose column
// grouper is a typeless — or, under explicit, GROUP_CATEGORY — grouper
// on the union source field.
func typelessAxisCrosstab(rowField string, rowType types.GroupType, interval float64, explicit bool) *Request {
	col := &types.Group{Field: types.DefaultUnionSourceField}
	if explicit {
		col.Type = types.GROUP_CATEGORY
	}
	return &Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: rowType, Field: rowField, Interval: interval}},
		Columns: []*types.Group{col},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "units", Label: "u"},
	}}
}

// predictCodes is the predict result's error codes, in order.
func predictCodes(res *descriptor.PredictResult) []string {
	var out []string
	for _, e := range res.Errors {
		out = append(out, e.Code)
	}
	return out
}

func TestCrosstabAxisDefaults_RunLikeExplicitOnCohortAndUnion(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	cases := []struct {
		name     string
		field    string
		rowType  types.GroupType
		interval float64
	}{
		{"categorical row", "region", types.GROUP_CATEGORY, 0},
		// f64 → GROUP_RANGE with the default interval the rule fills.
		{"numeric row", "amount", types.GROUP_RANGE, 10},
	}
	roots := map[string]func(*Request) *Request{"cohort": oracleOver, "union": unionOver}
	for _, tc := range cases {
		for root, over := range roots {
			// Fresh requests per run: the runtime writes its defaults
			// into the request it is handed, as for top-level groups.
			explicit := func() *Request { return over(typelessAxisCrosstab(tc.field, tc.rowType, tc.interval, true)) }
			want, err := p.Process(ctx, explicit())
			if err != nil {
				t.Fatalf("%s/%s: explicit: %v", tc.name, root, err)
			}
			req := over(typelessAxisCrosstab(tc.field, "", 0, false))
			res, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatalf("%s/%s: predict: %v", tc.name, root, err)
			}
			if !res.Valid || len(res.Errors) > 0 {
				t.Errorf("%s/%s: predict refused the typeless axes: %q", tc.name, root, predictCodes(res))
			}
			wantPaths := map[string]string{"crosstab.rows.0.type": string(tc.rowType), "crosstab.columns.0.type": string(types.GROUP_CATEGORY)}
			for _, d := range res.DefaultsApplied {
				key := d.Path[0] + "." + d.Path[1] + "." + d.Path[2] + "." + d.Path[3]
				if typ, ok := wantPaths[key]; ok && d.Type == typ && d.Category == "grouper" {
					delete(wantPaths, key)
				}
			}
			if len(wantPaths) > 0 {
				t.Errorf("%s/%s: DefaultsApplied %+v misses %v", tc.name, root, res.DefaultsApplied, wantPaths)
			}
			// Predict judged the defaulted request: the dispatch verdicts
			// equal the explicit request's.
			exp, err := p.Predict(ctx, explicit())
			if err != nil {
				t.Fatal(err)
			}
			if res.Streamable != exp.Streamable || !reflect.DeepEqual(res.CrosstabFusable, exp.CrosstabFusable) {
				t.Errorf("%s/%s: predict verdicts %v/%v, explicit %v/%v", tc.name, root, res.Streamable, res.CrosstabFusable, exp.Streamable, exp.CrosstabFusable)
			}
			// Predict's default pass never writes the caller's request.
			if req.Crosstab.Rows[0].Type != "" || req.Crosstab.Columns[0].Type != "" {
				t.Errorf("%s/%s: predict wrote a default into the caller's axes", tc.name, root)
			}
			got, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("%s/%s: typeless axes: %v", tc.name, root, err)
			}
			if g, w := comparableJSON(t, got), comparableJSON(t, want); g != w {
				t.Errorf("%s/%s: typeless != explicit\ntypeless: %s\nexplicit: %s", tc.name, root, g, w)
			}
		}
	}
}

// Under DisableDefaults the typeless axis is refused on both sides under
// one code and message, on a cohort and on a union.
func TestCrosstabAxisDefaults_DisabledRefusedAlikeByPredictAndRuntime(t *testing.T) {
	t.Parallel()
	_, fsys := unionSurfaceEngine(t)
	p, err := New(Options{FS: fsys, DisableDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reqs := map[string]func() *Request{
		"typeless row":    func() *Request { return typelessAxisCrosstab("region", "", 0, true) },
		"typeless column": func() *Request { return typelessAxisCrosstab("region", types.GROUP_CATEGORY, 0, false) },
	}
	roots := map[string]func(*Request) *Request{"cohort": oracleOver, "union": unionOver}
	for name, r := range reqs {
		for root, over := range roots {
			_, err := p.Process(ctx, over(r()))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("%s/%s: runtime = %v, want PROCESSING_CONFIG", name, root, err)
			}
			res, perr := p.Predict(ctx, over(r()))
			if perr != nil {
				t.Fatalf("%s/%s: predict: %v", name, root, perr)
			}
			if res.Valid || len(res.Errors) == 0 || res.Errors[0].Code != string(ce.Code) || res.Errors[0].Message != ce.Message {
				t.Errorf("%s/%s: predict %q (%+v), runtime %s %q", name, root, predictCodes(res), res.Errors, ce.Code, ce.Message)
			}
		}
	}
	// An explicit request stays valid on the defaults-off instance.
	if res, err := p.Predict(ctx, oracleOver(typelessAxisCrosstab("region", types.GROUP_CATEGORY, 0, true))); err != nil || !res.Valid {
		t.Errorf("explicit axes on the defaults-off instance: %+v, %v", res, err)
	}
}

// The same inference reaches every surface whose crosstab slots run
// through the shared request path: a Compose slot and a banner's rows
// and column groups (expanded into ordinary crosstab slots). A chain
// stage is never a crosstab (PULSE_CHAIN_NOT_MERGEABLE).
func TestCrosstabAxisDefaults_ComposeAndBanner(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	slot := func(explicit bool) *Request {
		r := unionOver(typelessAxisCrosstab("region", "", 0, explicit))
		if explicit {
			r.Crosstab.Rows[0].Type = types.GROUP_CATEGORY
		}
		r.Label = "x"
		return r
	}
	banner := func(explicit bool) *types.BannerSpec {
		col := &types.Group{Field: types.DefaultUnionSourceField}
		row := &types.Group{Field: "region"}
		if explicit {
			col.Type, row.Type = types.GROUP_CATEGORY, types.GROUP_CATEGORY
		}
		base := unionOver(&Request{Crosstab: &types.CrosstabSpec{Rows: []*types.Group{row}}})
		return &types.BannerSpec{
			Request:  base,
			Sections: []types.BannerSection{{Label: "units", Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "units", Label: "u"}}},
			Groups:   []types.BannerGroup{{Label: "wave", Columns: []*types.Group{col}}},
		}
	}
	compose := func(explicit bool) *ComposedRequest {
		return &ComposedRequest{Requests: []*Request{slot(explicit)}, Banner: banner(explicit)}
	}
	env, err := p.PredictCompose(ctx, compose(false))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict compose: %s, %v", wireOf(t, env.Errors), err)
	}
	got, err := p.Compose(ctx, compose(false))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	want, err := p.Compose(ctx, compose(true))
	if err != nil {
		t.Fatal(err)
	}
	for i := range want.Responses {
		if g, w := comparableJSON(t, got.Responses[i]), comparableJSON(t, want.Responses[i]); g != w {
			t.Errorf("compose slot %d: typeless != explicit\n%s\n%s", i, g, w)
		}
	}
	if wireOf(t, got.Banner) != wireOf(t, want.Banner) {
		t.Errorf("banner: typeless != explicit\n%s\n%s", wireOf(t, got.Banner), wireOf(t, want.Banner))
	}
	if len(got.Responses) != len(want.Responses) || len(want.Responses) < 2 || got.Banner == nil {
		t.Errorf("compose ran %d slots (explicit %d), banner %v", len(got.Responses), len(want.Responses), got.Banner != nil)
	}
}
