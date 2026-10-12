package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The #428 fit rule (U46 E4-S1): a data-dependent grouper
// (GROUP_QUANTILE, GROUP_KMEANS) takes params.fit "global" (default —
// fitted once over the whole filtered set, whatever its position) or
// "within" (refitted inside each bucket of the groupers before it).
// Plain groups, crosstab (rows then columns) and Compose agree on both.

// gfFit returns g with params.fit set (merged into existing params).
func gfFit(t *testing.T, g *types.Group, fit string) *types.Group {
	t.Helper()
	m := map[string]any{}
	if len(g.Params) > 0 {
		if err := json.Unmarshal(g.Params, &m); err != nil {
			t.Fatal(err)
		}
	}
	m["fit"] = fit
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	c := *g
	c.Params = b
	return &c
}

// gfPlainCounts runs groups [outer, inner] and renders "outer|inner" →
// count, in Data order.
func gfPlainCounts(t *testing.T, p *pulse.Pulse, f *mgFixture, outer, inner *types.Group) []string {
	t.Helper()
	resp, err := p.Process(context.Background(), mgRequest(f, []*types.Group{outer, inner}))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return mgRender(t, resp.Data, []string{mgKeyColumn(outer), mgKeyColumn(inner)}, "n")
}

// gfCrosstabCounts runs a crosstab and renders every present cell as
// "rowtuple|coltuple" folded to the composite [rows..., cols...] key
// with the sex column summed out, sorted.
func gfCrosstabCounts(t *testing.T, resp *types.Response, dropCol bool) []string {
	t.Helper()
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatalf("no crosstab matrix: %+v", resp)
	}
	m := resp.Crosstab.Matrix
	sum := map[string]float64{}
	for r, rk := range m.RowKeys {
		for c, ck := range m.ColumnKeys {
			cell := m.Cells[r][c]
			if !cell.Present {
				continue
			}
			parts := make([]string, 0, len(rk)+len(ck))
			for _, v := range rk {
				parts = append(parts, fmt.Sprint(v))
			}
			if !dropCol {
				for _, v := range ck {
					parts = append(parts, fmt.Sprint(v))
				}
			}
			sum[strings.Join(parts, "|")] += cell.Scalar()
		}
	}
	return gfSortedCounts(sum)
}

func gfSortedCounts(sum map[string]float64) []string {
	out := make([]string, 0, len(sum))
	for k, v := range sum {
		out = append(out, fmt.Sprintf("%s|%v", k, v))
	}
	sort.Strings(out)
	return out
}

func gfSorted(rows []string) []string {
	out := append([]string(nil), rows...)
	sort.Strings(out)
	return out
}

// gfCrosstab is a COUNT crosstab over the fixture.
func gfCrosstab(f *mgFixture, rows, cols []*types.Group) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: f.cohort},
		Crosstab: &types.CrosstabSpec{
			Rows: rows, Columns: cols,
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		},
	}
}

// gfPerSegment runs inner alone over each outer segment (a
// FILTER_INCLUDE on the region) and renders "region|inner|n": what
// "within" must equal.
func gfPerSegment(t *testing.T, p *pulse.Pulse, f *mgFixture, inner *types.Group) []string {
	t.Helper()
	var out []string
	for _, region := range []string{"north", "south", "west"} {
		req := mgRequest(f, []*types.Group{inner})
		req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{region}}}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process %s: %v", region, err)
		}
		for _, row := range mgRender(t, resp.Data, []string{mgKeyColumn(inner)}, "n") {
			out = append(out, region+"|"+row)
		}
	}
	return out
}

// TestGroupFit_CrossPath is the #428 equivalence gate: the same
// [region, inner] request gives identical keys and counts in plain
// groups, a nested crosstab row axis, a column axis under a region row
// axis and a Compose slot — under global and within, for quantile and
// k-means. global equals the whole-set (pre-U46 plain-groups) fit;
// within equals inner run separately over each region.
func TestGroupFit_CrossPath(t *testing.T) {
	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	p := f.p

	// Global quartiles over ids 1-30 (score = id): Q1 1-8, Q2 9-15,
	// Q3 16-23, Q4 24-30. Within: north 12 rows 3/3/3/3, south 10 rows
	// 3/2/3/2, west 8 rows 2/2/2/2.
	quartileWant := map[string][]string{
		"global": {"north|Q1|8", "north|Q2|4", "south|Q2|3", "south|Q3|7", "west|Q3|1", "west|Q4|7"},
		"within": {
			"north|Q1|3", "north|Q2|3", "north|Q3|3", "north|Q4|3",
			"south|Q1|3", "south|Q2|2", "south|Q3|3", "south|Q4|2",
			"west|Q1|2", "west|Q2|2", "west|Q3|2", "west|Q4|2",
		},
	}
	// Global k = 2 over y (~0 for ids 1-18, ~100 above): cluster_1 =
	// ids 1-18, cluster_2 = 19-30.
	kmeansGlobal := []string{"north|cluster_1|12", "south|cluster_1|6", "south|cluster_2|4", "west|cluster_2|8"}

	for _, inner := range []struct {
		name string
		g    func() *types.Group
	}{{"quantile", mgQuartile}, {"kmeans", mgKMeans}} {
		for _, fit := range []string{"", "global", "within"} {
			t.Run(inner.name+"/"+fit, func(t *testing.T) {
				g := inner.g()
				if fit != "" {
					g = gfFit(t, g, fit)
				}
				plain := gfPlainCounts(t, p, f, mgRegion(), g)

				switch {
				case fit == "within":
					if want := gfPerSegment(t, p, f, inner.g()); !reflect.DeepEqual(gfSorted(plain), gfSorted(want)) {
						t.Fatalf("plain within = %v, want the per-region fits %v", plain, want)
					}
					if inner.name == "quantile" && !reflect.DeepEqual(plain, quartileWant["within"]) {
						t.Fatalf("plain within = %v, want %v", plain, quartileWant["within"])
					}
				case inner.name == "quantile":
					if !reflect.DeepEqual(plain, quartileWant["global"]) {
						t.Fatalf("plain %q = %v, want %v", fit, plain, quartileWant["global"])
					}
				default:
					if !reflect.DeepEqual(plain, kmeansGlobal) {
						t.Fatalf("plain %q = %v, want %v", fit, plain, kmeansGlobal)
					}
				}
				// global spelled out equals the default byte for byte.
				if fit == "global" {
					if def := gfPlainCounts(t, p, f, mgRegion(), inner.g()); !reflect.DeepEqual(plain, def) {
						t.Fatalf("fit global %v != default %v", plain, def)
					}
				}

				// Nested row axis [region, inner] × [sex], sex summed out.
				nested, err := p.Process(ctx, gfCrosstab(f, []*types.Group{mgRegion(), g}, []*types.Group{mgSex()}))
				if err != nil {
					t.Fatalf("crosstab nested: %v", err)
				}
				if got := gfCrosstabCounts(t, nested, true); !reflect.DeepEqual(got, gfSorted(plain)) {
					t.Fatalf("crosstab rows [region, %s] = %v, plain groups = %v", inner.name, got, gfSorted(plain))
				}
				// Column axis [inner] under row axis [region].
				col, err := p.Process(ctx, gfCrosstab(f, []*types.Group{mgRegion()}, []*types.Group{g}))
				if err != nil {
					t.Fatalf("crosstab column: %v", err)
				}
				if got := gfCrosstabCounts(t, col, false); !reflect.DeepEqual(got, gfSorted(plain)) {
					t.Fatalf("crosstab [region] x [%s] = %v, plain groups = %v", inner.name, got, gfSorted(plain))
				}

				// Compose: both shapes as slots equal their Process runs.
				cr, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{
					mgRequest(f, []*types.Group{mgRegion(), g}),
					gfCrosstab(f, []*types.Group{mgRegion(), g}, []*types.Group{mgSex()}),
				}})
				if err != nil {
					t.Fatalf("Compose: %v", err)
				}
				if got := mgRender(t, cr.Responses[0].Data, []string{"region", mgKeyColumn(g)}, "n"); !reflect.DeepEqual(got, plain) {
					t.Fatalf("Compose plain slot = %v, Process = %v", got, plain)
				}
				if got := gfCrosstabCounts(t, cr.Responses[1], true); !reflect.DeepEqual(got, gfSorted(plain)) {
					t.Fatalf("Compose crosstab slot = %v, plain = %v", got, gfSorted(plain))
				}
			})
		}
	}
}

// TestGroupFit_OutermostWithinIsNoOp: on the outermost composite
// position (groups[0], crosstab rows[0]) "within" has no outer segment
// and fits over the whole set exactly as "global" does.
func TestGroupFit_OutermostWithinIsNoOp(t *testing.T) {
	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	for _, inner := range []func() *types.Group{mgQuartile, mgKMeans} {
		g := inner()
		global, err := f.p.Process(ctx, mgRequest(f, []*types.Group{g, mgRegion()}))
		if err != nil {
			t.Fatal(err)
		}
		within, err := f.p.Process(ctx, mgRequest(f, []*types.Group{gfFit(t, g, "within"), mgRegion()}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(global.Data, within.Data) {
			t.Fatalf("%s outermost within %v != global %v", g.Type, within.Data, global.Data)
		}
		cg, err := f.p.Process(ctx, gfCrosstab(f, []*types.Group{g}, []*types.Group{mgRegion()}))
		if err != nil {
			t.Fatal(err)
		}
		cw, err := f.p.Process(ctx, gfCrosstab(f, []*types.Group{gfFit(t, g, "within")}, []*types.Group{mgRegion()}))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cg.Crosstab, cw.Crosstab) {
			t.Fatalf("%s crosstab rows[0] within != global", g.Type)
		}
	}
}

// TestGroupFit_RefusedIdentically: an unknown fit is refused by predict
// and at run time with the same code, message and details — plain
// groups and both crosstab axes, quantile and k-means — and fit never
// moves the fused-dispatch answer.
func TestGroupFit_RefusedIdentically(t *testing.T) {
	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	for _, inner := range []func() *types.Group{mgQuartile, mgKMeans} {
		bad := gfFit(t, inner(), "per_bucket")
		for name, req := range map[string]*types.Request{
			"groups":          mgRequest(f, []*types.Group{mgRegion(), bad}),
			"crosstab rows":   gfCrosstab(f, []*types.Group{mgRegion(), bad}, []*types.Group{mgSex()}),
			"crosstab column": gfCrosstab(f, []*types.Group{mgRegion()}, []*types.Group{bad}),
		} {
			t.Run(string(bad.Type)+"/"+name, func(t *testing.T) {
				_, err := f.p.Process(ctx, req)
				ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
				env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
				if len(env.Errors) != 1 {
					t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
				}
				pe := env.Errors[0]
				if pe.Code != string(ce.Code) || pe.Message != ce.Message || !reflect.DeepEqual(pe.Details, ce.Details) {
					t.Fatalf("predict %s %q %v != runtime %s %q %v", pe.Code, pe.Message, pe.Details, ce.Code, ce.Message, ce.Details)
				}
				if ce.Details["value"] != "per_bucket" || ce.Details["param"] != "fit" {
					t.Fatalf("details %v", ce.Details)
				}
			})
		}
	}

	// Fused dispatch: identical whatever the fit.
	var ref *bool
	var refReasons []string
	for i, fit := range []string{"", "global", "within"} {
		g := mgQuartile()
		if fit != "" {
			g = gfFit(t, g, fit)
		}
		pr, err := f.p.Predict(ctx, gfCrosstab(f, []*types.Group{mgRegion(), g}, []*types.Group{mgSex()}))
		if err != nil {
			t.Fatal(err)
		}
		if !pr.Valid || pr.CrosstabFusable == nil || *pr.CrosstabFusable {
			t.Fatalf("fit %q: valid %v fusable %v", fit, pr.Valid, pr.CrosstabFusable)
		}
		if i == 0 {
			ref, refReasons = pr.CrosstabFusable, pr.CrosstabFusionReasons
			continue
		}
		if *pr.CrosstabFusable != *ref || !reflect.DeepEqual(pr.CrosstabFusionReasons, refReasons) {
			t.Fatalf("fit %q fusion %v %v != default %v %v", fit, *pr.CrosstabFusable, pr.CrosstabFusionReasons, *ref, refReasons)
		}
	}
}

// gfWarnings renders resp's PULSE_MATRIX_NOT_CONVERGED warnings as
// sorted "group_key|message" lines ("-" = unstamped).
func gfWarnings(t *testing.T, resp *types.Response) []string {
	t.Helper()
	var out []string
	for _, w := range resp.Warnings {
		if w.Code != string(perr.PULSE_MATRIX_NOT_CONVERGED) {
			continue
		}
		key := "-"
		if gk, ok := w.Details["group_key"]; ok {
			b, err := json.Marshal(gk)
			if err != nil {
				t.Fatal(err)
			}
			key = string(b)
		}
		out = append(out, key+"|"+w.Message)
	}
	sort.Strings(out)
	return out
}

// gfKeys strips the message: the sorted group_key column.
func gfKeys(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l[:strings.Index(l, "|")]
	}
	return out
}

// TestGroupFit_RefitWarnings (U46 E4-S5): every warning a "within"
// refit raises reaches Response.Warnings, one per outer bucket and
// code, details.group_key naming the outer segment — the same set on
// plain groups, both crosstab axes and Compose; the whole-set fit's
// own warning stays unstamped. "global", absent fit and an outermost
// "within" raise only the unstamped whole-set warning.
func TestGroupFit_RefitWarnings(t *testing.T) {
	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	p := f.p
	// max_iter 1 never converges (the first pass always reassigns), so
	// every fit — whole-set and each refit — warns.
	km := func(fit string) *types.Group {
		g := &types.Group{Type: types.GROUP_KMEANS, Fields: []string{"y"}, Params: json.RawMessage(`{"k":2,"max_iter":1}`)}
		if fit != "" {
			g = gfFit(t, g, fit)
		}
		return g
	}
	process := func(t *testing.T, req *types.Request) *types.Response {
		t.Helper()
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return resp
	}
	region, sex := mgRegion(), mgSex()

	for _, fit := range []string{"", "global", "within"} {
		t.Run("fit="+fit, func(t *testing.T) {
			g := km(fit)
			// Outer segment [region].
			plain := gfWarnings(t, process(t, mgRequest(f, []*types.Group{region, g})))
			wantKeys := []string{`-`}
			if fit == "within" {
				wantKeys = []string{`-`, `["north"]`, `["south"]`, `["west"]`}
			}
			if got := gfKeys(plain); !reflect.DeepEqual(got, wantKeys) {
				t.Fatalf("plain [region, kmeans] warning keys = %v, want %v (%v)", got, wantKeys, plain)
			}
			for name, req := range map[string]*types.Request{
				"crosstab rows [region, kmeans]":    gfCrosstab(f, []*types.Group{region, g}, []*types.Group{sex}),
				"crosstab [region] x [kmeans]":      gfCrosstab(f, []*types.Group{region}, []*types.Group{g}),
				"crosstab rows [region, kmeans] cn": withNormalize(gfCrosstab(f, []*types.Group{region, g}, []*types.Group{sex})),
			} {
				if got := gfWarnings(t, process(t, req)); !reflect.DeepEqual(got, plain) {
					t.Fatalf("%s warnings = %v, plain = %v", name, got, plain)
				}
			}

			// Outer segment [region, sex]: a column position's outer
			// segment is the row tuple plus its column prefix.
			plain2 := gfWarnings(t, process(t, mgRequest(f, []*types.Group{region, sex, g})))
			if fit == "within" && len(plain2) != 7 {
				t.Fatalf("plain [region, sex, kmeans] = %v, want the whole-set warning + 6 segments", plain2)
			}
			for name, req := range map[string]*types.Request{
				"crosstab [region] x [sex, kmeans]": gfCrosstab(f, []*types.Group{region}, []*types.Group{sex, g}),
				"crosstab [region, sex] x [kmeans]": gfCrosstab(f, []*types.Group{region, sex}, []*types.Group{g}),
				"crosstab [region, sex, kmeans]":    gfCrosstab(f, []*types.Group{region, sex, g}, []*types.Group{mgTier()}),
			} {
				if got := gfWarnings(t, process(t, req)); !reflect.DeepEqual(got, plain2) {
					t.Fatalf("%s warnings = %v, plain = %v", name, got, plain2)
				}
			}

			// Compose slots raise their Process set.
			cr, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{
				mgRequest(f, []*types.Group{region, g}),
				gfCrosstab(f, []*types.Group{region}, []*types.Group{g}),
			}})
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			for i, r := range cr.Responses {
				if got := gfWarnings(t, r); !reflect.DeepEqual(got, plain) {
					t.Fatalf("Compose slot %d warnings = %v, plain = %v", i, got, plain)
				}
			}

			// Outermost "within" is the whole-set fit: one unstamped
			// warning, as under "global".
			outer := gfWarnings(t, process(t, mgRequest(f, []*types.Group{g, region})))
			if got := gfKeys(outer); !reflect.DeepEqual(got, []string{`-`}) {
				t.Fatalf("outermost %q warnings = %v", fit, outer)
			}
			if got := gfWarnings(t, process(t, gfCrosstab(f, []*types.Group{g}, []*types.Group{region}))); !reflect.DeepEqual(got, outer) {
				t.Fatalf("crosstab rows[0] %q warnings = %v, plain = %v", fit, got, outer)
			}
		})
	}

	// global spelled out is byte-identical to absent, warnings and all.
	for _, req := range []func(g *types.Group) *types.Request{
		func(g *types.Group) *types.Request { return mgRequest(f, []*types.Group{region, g}) },
		func(g *types.Group) *types.Request {
			return gfCrosstab(f, []*types.Group{region}, []*types.Group{g})
		},
	} {
		def, glob := process(t, req(km(""))), process(t, req(km("global")))
		if !reflect.DeepEqual(def.Warnings, glob.Warnings) || !reflect.DeepEqual(def.Data, glob.Data) || !reflect.DeepEqual(def.Crosstab, glob.Crosstab) {
			t.Fatalf("fit global != absent: %v vs %v", glob.Warnings, def.Warnings)
		}
	}
}

// withNormalize sets a row normalisation at the outermost level, so the
// partial-margin pass refits too (and must stay silent).
func withNormalize(req *types.Request) *types.Request {
	lvl := 0
	req.Crosstab.Normalize = types.CrosstabNormalizeRow
	req.Crosstab.NormalizeLevel = &lvl
	return req
}
