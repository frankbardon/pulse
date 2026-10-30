//go:build heavy

package integration_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestMultiGrouper_YoYPerPartition: OVERLAY_YOY finds its GROUP_DATE
// grouper wherever it sits and runs once per combination of every OTHER
// key; each segment's entries equal, bit for bit and in order, the same
// overlay run alone on that segment (a FILTER_INCLUDE per non-date key,
// grouped by the date alone). Shapes put the date first, last and in
// the middle; one case leaves frequency to the date grouper's Params,
// which is not groups[0]; an explicit partition_by naming the non-date
// keys in another order equals the implicit default. Every entry keeps
// its full composite key. Runs on every mode that returns layers.
func TestMultiGrouper_YoYPerPartition(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	dayAnnual := func() *types.Group {
		g := mgDay()
		g.Params = json.RawMessage(`{"frequency": "annual"}`)
		return g
	}
	cases := []struct {
		name   string
		groups func() []*types.Group
		date   int
		spec   types.OverlaySpec
		single types.OverlaySpec
	}{
		{"region×day", func() []*types.Group { return []*types.Group{mgRegion(), mgDay()} }, 1,
			yoySpec(t, "annual", nil), yoySpec(t, "annual", nil)},
		{"day×region", func() []*types.Group { return []*types.Group{mgDay(), mgRegion()} }, 0,
			yoySpec(t, "annual", nil), yoySpec(t, "annual", nil)},
		{"region×day×sex frequency from the date grouper", func() []*types.Group { return []*types.Group{mgRegion(), dayAnnual(), mgSex()} }, 1,
			yoySpec(t, "", nil), yoySpec(t, "", nil)},
		{"sex×region×day explicit partition_by", func() []*types.Group { return []*types.Group{mgSex(), mgRegion(), mgDay()} }, 2,
			yoySpec(t, "annual", map[string]any{"partition_by": []string{"region", "sex"}}), yoySpec(t, "annual", map[string]any{"partition_by": []string{}})},
	}
	var modes []mgMode
	for _, m := range mgModes {
		switch m.name {
		case "ProcessStream", "ProcessChain":
			continue
		}
		modes = append(modes, m)
	}
	for _, c := range cases {
		for _, mode := range modes {
			t.Run(c.name+"/"+mode.name, func(t *testing.T) {
				run := func(groups []*types.Group, spec types.OverlaySpec, filters ...*types.Filterer) *types.Response {
					t.Helper()
					req := mgRequest(f, groups, psYSum())
					req.Filterers = filters
					req.Overlays = []types.OverlaySpec{spec}
					if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
						t.Fatalf("predict: %+v", env.Errors)
					}
					resp, err := mode.run(t, f, req)
					if err != nil {
						t.Fatalf("run: %v", err)
					}
					if resp == nil || len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Series == nil {
						t.Fatalf("want one series layer, got %+v", resp)
					}
					return resp
				}
				groups := c.groups()
				resp := run(groups, c.spec)
				if n := len(resp.Overlays[0].Payload.Series.Entries); n != len(resp.Data) {
					t.Fatalf("%d entries for %d rows", n, len(resp.Data))
				}
				// segment (the non-date key values) → its entries in order.
				composite := map[string][]string{}
				segVals := map[string][]any{}
				compared := 0
				for _, e := range resp.Overlays[0].Payload.Series.Entries {
					if len(e.Key) != len(groups) {
						t.Fatalf("entry key %v is not the full composite key", e.Key)
					}
					var seg []any
					for i, v := range e.Key {
						if i != c.date {
							seg = append(seg, v)
						}
					}
					id := fmt.Sprint(seg)
					segVals[id] = seg
					composite[id] = append(composite[id], fmt.Sprint(e.Key[c.date])+"="+psStat(e.Summary.Statistic))
					if e.Summary.Statistic != nil && !math.IsNaN(*e.Summary.Statistic) {
						compared++
					}
				}
				if len(composite) < 3 || compared < 3 {
					t.Fatalf("%d segments, %d comparisons; the fixture must split into several", len(composite), compared)
				}
				for id, seg := range segVals {
					var filters []*types.Filterer
					j := 0
					for i, g := range groups {
						if i == c.date {
							continue
						}
						filters = append(filters, &types.Filterer{Type: types.FILTER_INCLUDE, Field: mgKeyColumn(g), Values: []string{fmt.Sprint(seg[j])}})
						j++
					}
					alone := run([]*types.Group{c.groups()[c.date]}, c.single, filters...)
					var want []string
					for _, e := range alone.Overlays[0].Payload.Series.Entries {
						want = append(want, fmt.Sprint(e.Key[0])+"="+psStat(e.Summary.Statistic))
					}
					if !reflect.DeepEqual(composite[id], want) {
						t.Errorf("segment %s:\n got  %v\n want %v (the segment run alone)", id, composite[id], want)
					}
				}
			})
		}
	}
}
