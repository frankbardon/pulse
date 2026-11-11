package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// #434 (U66 E5-S2): a params.fit "within" position after the first
// reports one Components.Groupers[i].buckets entry per outer bucket —
// that refit's own floor and operator keys — beside the slot-level
// whole-set figures; a degenerate refit fails its bucket alone.

// gfJSON is v's wire form, so figures compare as the caller reads them.
func gfJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// gfProcess runs req, failing on an error.
func gfProcess(t *testing.T, p *pulse.Pulse, req *types.Request) *types.Response {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// gfGroupers is resp's Components.Groupers, failing when absent.
func gfGroupers(t *testing.T, resp *types.Response) []types.GrouperComponents {
	t.Helper()
	if resp.Components == nil || len(resp.Components.Groupers) == 0 {
		t.Fatalf("no grouper components: %+v", resp.Components)
	}
	return resp.Components.Groupers
}

// gfRegionOnly is req filtered to one region.
func gfRegionOnly(req *types.Request, region string) *types.Request {
	req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{region}}}
	return req
}

// gfBucketOf returns the operator `buckets` entry keyed key.
func gfBucketOf(t *testing.T, op map[string]any, key string) map[string]any {
	t.Helper()
	bs, _ := op["buckets"].([]map[string]any)
	for _, b := range bs {
		if b["key"] == key {
			return b
		}
	}
	t.Fatalf("no bucket %q in %v", key, op)
	return nil
}

// TestGroupFit_Buckets_PerRefit: each entry is the refit a standalone
// run of the grouper over that region reports (floor and operator
// keys), in Data order; the slot-level entry stays the whole-set fit,
// byte-identical to "global"'s; the outer slot carries no buckets.
func TestGroupFit_Buckets_PerRefit(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, inner := range []struct {
		name string
		g    func() *types.Group
	}{{"quantile", mgQuartile}, {"kmeans", mgKMeans}} {
		t.Run(inner.name, func(t *testing.T) {
			within := gfGroupers(t, gfProcess(t, f.p, mgRequest(f, []*types.Group{mgRegion(), gfFit(t, inner.g(), "within")})))
			if within[0].Buckets != nil {
				t.Fatalf("outer slot carries buckets: %+v", within[0].Buckets)
			}
			got := within[1].Buckets
			regions := []string{"north", "south", "west"}
			if len(got) != len(regions) {
				t.Fatalf("buckets = %s, want one per region", gfJSON(t, got))
			}
			for i, region := range regions {
				b := got[i]
				if !reflect.DeepEqual(b.GroupKey, types.AxisKey{region}) || b.Error != nil {
					t.Fatalf("bucket %d = %s, want group_key [%s] and no error", i, gfJSON(t, b), region)
				}
				seg := gfGroupers(t, gfProcess(t, f.p, gfRegionOnly(mgRequest(f, []*types.Group{inner.g()}), region)))[0]
				if b.TotalN != seg.TotalN || b.NNull != seg.NNull || gfJSON(t, b.Operator) != gfJSON(t, seg.Operator) {
					t.Fatalf("%s bucket = %s, standalone fit over the region = %s", region, gfJSON(t, b), gfJSON(t, seg))
				}
				if b.TotalN == 0 || b.Operator == nil {
					t.Fatalf("%s bucket carries no figures: %s", region, gfJSON(t, b))
				}
			}

			// Buckets follow the outer segments' Data order (the outer
			// grouper's include order here), not record order.
			ordered := &types.Group{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"west", "north", "south"}}
			var keys []string
			for _, b := range gfGroupers(t, gfProcess(t, f.p, mgRequest(f, []*types.Group{ordered, gfFit(t, inner.g(), "within")})))[1].Buckets {
				keys = append(keys, b.GroupKey[0].(string))
			}
			if !reflect.DeepEqual(keys, ordered.Include) {
				t.Fatalf("bucket order %v, want the Data order %v", keys, ordered.Include)
			}

			// The slot-level floor is the whole-set fit's: equal to the
			// "global" run's entry once the buckets are set aside.
			global := gfGroupers(t, gfProcess(t, f.p, mgRequest(f, []*types.Group{mgRegion(), gfFit(t, inner.g(), "global")})))
			slot := within[1]
			slot.Buckets = nil
			if gfJSON(t, slot) != gfJSON(t, global[1]) {
				t.Fatalf("within slot floor %s != global %s", gfJSON(t, slot), gfJSON(t, global[1]))
			}
		})
	}
}

// TestGroupFit_Buckets_ThreeWayParity: the buffered Process exit, a
// Compose slot and the terminal ProcessStream chunk carry the same
// grouper Components (buckets included; no mid-stream chunk carries
// buckets), and a nested crosstab row axis reads each row's key
// component off that row's refit — the bucket the plain-groups entry
// carries — rather than the whole-set fit's bucket of the same key.
func TestGroupFit_Buckets_ThreeWayParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	for _, inner := range []struct {
		name string
		g    func() *types.Group
	}{{"quantile", mgQuartile}, {"kmeans", mgKMeans}} {
		t.Run(inner.name, func(t *testing.T) {
			g := gfFit(t, inner.g(), "within")
			req := func() *types.Request { return mgRequest(f, []*types.Group{mgRegion(), g}) }
			buffered := gfGroupers(t, gfProcess(t, f.p, req()))
			want := gfJSON(t, buffered)
			if len(buffered[1].Buckets) == 0 {
				t.Fatal("no buckets on the buffered exit")
			}

			cr, err := f.p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{req()}})
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if got := gfJSON(t, gfGroupers(t, cr.Responses[0])); got != want {
				t.Fatalf("Compose slot groupers %s != Process %s", got, want)
			}

			res, err := f.p.ProcessStreamResult(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			var chunks []pulse.StreamChunk[pulse.Row]
			for ch := range res.Chunks {
				chunks = append(chunks, ch)
			}
			if term := <-res.Done; term.Status != pulse.StreamCompleted {
				t.Fatalf("terminator %+v", term)
			}
			if len(chunks) < 2 {
				t.Fatalf("stream gave %d chunks, want several", len(chunks))
			}
			for i, ch := range chunks[:len(chunks)-1] {
				if ch.Components != nil {
					for _, g := range ch.Components.Groupers {
						if g.Buckets != nil {
							t.Fatalf("mid-stream chunk %d carries buckets", i)
						}
					}
				}
			}
			last := chunks[len(chunks)-1].Components
			if last == nil {
				t.Fatal("terminal chunk carries no components")
			}
			if got := gfJSON(t, last.Groupers); got != want {
				t.Fatalf("terminal stream groupers %s != Process %s", got, want)
			}

			// Crosstab rows [region, inner] x [sex]: every row's inner
			// key component is its region's refit bucket.
			ct := gfProcess(t, f.p, gfCrosstab(f, []*types.Group{mgRegion(), g}, []*types.Group{mgSex()}))
			byRegion := map[string]map[string]any{}
			for _, b := range buffered[1].Buckets {
				byRegion[b.GroupKey[0].(string)] = b.Operator
			}
			rowKeys := ct.Crosstab.Matrix.RowKeys
			rkc := ct.Components.Crosstab.RowKeyComponents
			if len(rkc) != len(rowKeys) || len(rowKeys) == 0 {
				t.Fatalf("row key components %d for %d rows", len(rkc), len(rowKeys))
			}
			moved := false
			for r, rk := range rowKeys {
				region, key := rk[0].(string), rk[1].(string)
				axes, _ := rkc[r]["axes"].([]map[string]any)
				if len(axes) != 2 {
					t.Fatalf("row %v axes = %v", rk, rkc[r])
				}
				wantBucket := gfBucketOf(t, byRegion[region], key)
				if got := gfJSON(t, axes[1]["bucket"]); got != gfJSON(t, wantBucket) {
					t.Fatalf("crosstab row %v bucket %s != refit bucket %s", rk, got, gfJSON(t, wantBucket))
				}
				if gfJSON(t, wantBucket) != gfJSON(t, gfBucketOf(t, buffered[1].Operator, key)) {
					moved = true
				}
			}
			if !moved {
				t.Fatal("every refit bucket equals the whole-set bucket: the fixture cannot tell the fits apart")
			}
		})
	}
}

// TestGroupFit_Buckets_AbsentIsByteIdentical: without a "within"
// position after the first — absent fit, "global", an outermost
// "within" — no slot carries buckets and absent equals "global" byte
// for byte; Components off leaves Components nil under "within".
func TestGroupFit_Buckets_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, inner := range []func() *types.Group{mgQuartile, mgKMeans} {
		absent := gfProcess(t, f.p, mgRequest(f, []*types.Group{mgRegion(), inner()}))
		global := gfProcess(t, f.p, mgRequest(f, []*types.Group{mgRegion(), gfFit(t, inner(), "global")}))
		outermost := gfProcess(t, f.p, mgRequest(f, []*types.Group{gfFit(t, inner(), "within"), mgRegion()}))
		for name, resp := range map[string]*types.Response{"absent": absent, "global": global, "outermost within": outermost} {
			if hasGrouperBuckets(resp) {
				t.Fatalf("%s carries buckets: %s", name, gfJSON(t, resp.Components.Groupers))
			}
		}
		if gfJSON(t, absent) != gfJSON(t, global) {
			t.Fatalf("absent fit %s != global %s", gfJSON(t, absent), gfJSON(t, global))
		}
		off := true
		req := mgRequest(f, []*types.Group{mgRegion(), gfFit(t, inner(), "within")})
		req.DisableComponents = &off
		if resp := gfProcess(t, f.p, req); resp.Components != nil {
			t.Fatalf("components off: %+v", resp.Components)
		}
	}
}

func hasGrouperBuckets(resp *types.Response) bool {
	if resp.Components == nil {
		return false
	}
	for _, g := range resp.Components.Groupers {
		if g.Buckets != nil {
			return true
		}
	}
	return false
}

// gfSlotFailed renders resp's PULSE_SLOT_FAILED warnings' details.
func gfSlotFailed(t *testing.T, resp *types.Response) []string {
	t.Helper()
	var out []string
	for _, w := range resp.Warnings {
		if w.Code == string(perr.PULSE_SLOT_FAILED) {
			out = append(out, gfJSON(t, w.Details))
		}
	}
	return out
}

// TestGroupFit_DegenerateRefit: GROUP_KMEANS k = 4 within region fits
// the whole set (6 distinct y) and south (6) but not north or west (3
// each). Each failing region's bucket carries the refusal and its
// records join no row; the run warns PULSE_SLOT_FAILED per failing
// bucket — the same set on plain groups, both crosstab axes and a
// Compose slot — and the slot floor is the whole-set fit's. Strict
// fails with the refit's own code on every path.
func TestGroupFit_DegenerateRefit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	km := func(fit string) *types.Group {
		return gfFit(t, &types.Group{Type: types.GROUP_KMEANS, Fields: []string{"y"}, Params: json.RawMessage(`{"k":4}`)}, fit)
	}
	plainReq := func() *types.Request { return mgRequest(f, []*types.Group{mgRegion(), km("within")}) }
	resp := gfProcess(t, f.p, plainReq())

	rows := mgRender(t, resp.Data, []string{"region", "GROUP_KMEANS_y"}, "n")
	for _, r := range rows {
		if !strings.HasPrefix(r, "south|") {
			t.Fatalf("rows %v: only south refits", rows)
		}
	}
	if len(rows) != 4 {
		t.Fatalf("rows %v, want south's 4 clusters", rows)
	}

	grp := gfGroupers(t, resp)[1]
	if len(grp.Buckets) != 3 {
		t.Fatalf("buckets %s", gfJSON(t, grp.Buckets))
	}
	for i, want := range []struct {
		region string
		failed bool
		nNull  int
	}{{"north", true, 12}, {"south", false, 0}, {"west", true, 8}} {
		b := grp.Buckets[i]
		if !reflect.DeepEqual(b.GroupKey, types.AxisKey{want.region}) {
			t.Fatalf("bucket %d key %v", i, b.GroupKey)
		}
		if !want.failed {
			if b.Error != nil || b.TotalN != 10 || b.Operator == nil {
				t.Fatalf("south bucket %s", gfJSON(t, b))
			}
			continue
		}
		if b.Error == nil || b.Error.Code != string(perr.PROCESSING_CONFIG) || b.Error.Details["reason"] != "k_exceeds_distinct_points" ||
			b.NNull != want.nNull || b.TotalN != 0 || b.Operator != nil {
			t.Fatalf("%s bucket %s, want the k_exceeds_distinct_points error over %d records", want.region, gfJSON(t, b), want.nNull)
		}
	}
	// The slot floor is the whole-set fit's (it succeeded).
	global := gfGroupers(t, gfProcess(t, f.p, mgRequest(f, []*types.Group{mgRegion(), km("global")})))[1]
	slot := grp
	slot.Buckets = nil
	if gfJSON(t, slot) != gfJSON(t, global) {
		t.Fatalf("slot floor %s != whole-set fit %s", gfJSON(t, slot), gfJSON(t, global))
	}

	warns := gfSlotFailed(t, resp)
	if len(warns) != 2 || !strings.Contains(warns[0], `"group_key":["north"]`) || !strings.Contains(warns[1], `"group_key":["west"]`) ||
		!strings.Contains(warns[0], `"slot_kind":"groups"`) || !strings.Contains(warns[0], `"index":1`) || !strings.Contains(warns[0], `"code":"PROCESSING_CONFIG"`) {
		t.Fatalf("PULSE_SLOT_FAILED warnings %v", warns)
	}

	// Crosstab rows and the column axis: same rows, same warnings.
	for name, req := range map[string]*types.Request{
		"rows [region, kmeans]":   gfCrosstab(f, []*types.Group{mgRegion(), km("within")}, []*types.Group{mgSex()}),
		"[region] x [kmeans]":     gfCrosstab(f, []*types.Group{mgRegion()}, []*types.Group{km("within")}),
		"normalised rows [r, km]": withNormalize(gfCrosstab(f, []*types.Group{mgRegion(), km("within")}, []*types.Group{mgSex()})),
	} {
		ct := gfProcess(t, f.p, req)
		if got := gfSlotFailed(t, ct); !reflect.DeepEqual(got, warns) {
			t.Fatalf("crosstab %s warnings %v != plain %v", name, got, warns)
		}
		if req.Crosstab.Normalize != "" {
			continue // cells are shares, not counts
		}
		dropCol := len(req.Crosstab.Rows) == 2
		if got := gfCrosstabCounts(t, ct, dropCol); !reflect.DeepEqual(got, gfSorted(rows)) {
			t.Fatalf("crosstab %s counts %v != plain %v", name, got, gfSorted(rows))
		}
	}

	cr, err := f.p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{plainReq()}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got := gfSlotFailed(t, cr.Responses[0]); !reflect.DeepEqual(got, warns) {
		t.Fatalf("Compose warnings %v != Process %v", got, warns)
	}

	// Strict: the refit's own code, located by slot and outer bucket.
	strict := newMGFixture(t, pulse.Options{Strict: true})
	for name, req := range map[string]*types.Request{
		"plain":         mgRequest(strict, []*types.Group{mgRegion(), km("within")}),
		"crosstab rows": gfCrosstab(strict, []*types.Group{mgRegion(), km("within")}, []*types.Group{mgSex()}),
		"crosstab cols": gfCrosstab(strict, []*types.Group{mgRegion()}, []*types.Group{km("within")}),
	} {
		_, err := strict.p.Process(ctx, req)
		ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
		if ce.Details["reason"] != "k_exceeds_distinct_points" || ce.Details["slot_kind"] != "groups" || ce.Details["index"] != 1 ||
			!reflect.DeepEqual(ce.Details["group_key"], types.AxisKey{"north"}) {
			t.Fatalf("strict %s details %v", name, ce.Details)
		}
	}
}

// TestGroupFit_Buckets_PredictParity: predict's refit_buckets echo is
// true exactly where the run emits buckets.
func TestGroupFit_Buckets_PredictParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newMGFixture(t, pulse.Options{})
	for name, groups := range map[string][]*types.Group{
		"within inner":       {mgRegion(), gfFit(t, mgQuartile(), "within")},
		"within kmeans":      {mgRegion(), gfFit(t, mgKMeans(), "within")},
		"global inner":       {mgRegion(), gfFit(t, mgQuartile(), "global")},
		"absent":             {mgRegion(), mgQuartile()},
		"outermost within":   {gfFit(t, mgQuartile(), "within"), mgRegion()},
		"three, within last": {mgRegion(), mgSex(), gfFit(t, mgQuartile(), "within")},
	} {
		t.Run(name, func(t *testing.T) {
			req := mgRequest(f, groups)
			pr, err := f.p.Predict(ctx, req)
			if err != nil || !pr.Valid {
				t.Fatalf("predict: %v %+v", err, pr)
			}
			got := gfGroupers(t, gfProcess(t, f.p, mgRequest(f, groups)))
			if len(pr.Groups) != len(got) {
				t.Fatalf("predict %d groups, run %d", len(pr.Groups), len(got))
			}
			for i := range got {
				if pr.Groups[i].RefitBuckets != (got[i].Buckets != nil) {
					t.Fatalf("groups[%d]: predict refit_buckets %v, run buckets %s", i, pr.Groups[i].RefitBuckets, gfJSON(t, got[i].Buckets))
				}
			}
		})
	}
}
