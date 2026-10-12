package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// The #422 regression gate (U54): a plain `groups` list with two or
// more groupers yields one Data row per OBSERVED key combination, each
// row carrying every grouper's key column, ordered by each grouper's
// native key order outer → inner. Every expected count below is
// hand-computed from mgRows; the table is extended by later stories
// with the streaming, parallel-decode, shard, Compose, chain, sort and
// return modes (mgModes).

// mgRow is one fixture record: id 1..30, region × sex laid out in
// contiguous id blocks (north F 1-7, north M 8-12, south F 13-16,
// south M 17-22, west F 23-25, west M 26-30).
type mgRow struct {
	id     int
	region string
	sex    string
	tier   string   // "" = null
	tags   []string // nil = null, empty = no selection
	y      float64  // kmeans member: ~0 for ids 1-18, ~100 for 19-30
}

// mgRows is the 30-row repro. score = id; day = 2024-01-15 for odd ids,
// 2024-02-15 for even ids.
var mgRows = func() []mgRow {
	tier := map[int]string{
		1: "gold", 2: "gold", 3: "gold", 4: "silver", 5: "silver",
		8: "gold", 9: "gold", 10: "silver", 11: "silver", 12: "silver",
		13: "gold", 14: "silver", 15: "silver",
		17: "gold", 18: "gold", 19: "gold", 20: "silver", 21: "silver",
		23: "silver", 24: "silver", 25: "silver",
		26: "silver", 27: "silver",
	}
	tags := map[int][]string{
		1: {"a", "b"}, 2: {"a", "b"}, 3: {"a", "b"}, 4: {"a", "b"}, 5: {"a", "b"},
		13: {"a", "b"}, 14: {"a", "b"},
		8: {"a"}, 9: {"a"}, 17: {"a"}, 23: {"a"},
		26: {"b"},
		6:  nil, 7: nil,
	}
	out := make([]mgRow, 0, 30)
	for id := 1; id <= 30; id++ {
		r := mgRow{id: id, tier: tier[id]}
		switch {
		case id <= 7:
			r.region, r.sex = "north", "F"
		case id <= 12:
			r.region, r.sex = "north", "M"
		case id <= 16:
			r.region, r.sex = "south", "F"
		case id <= 22:
			r.region, r.sex = "south", "M"
		case id <= 25:
			r.region, r.sex = "west", "F"
		default:
			r.region, r.sex = "west", "M"
		}
		if ts, ok := tags[id]; ok {
			r.tags = ts
		} else {
			r.tags = []string{}
		}
		r.y = float64(id % 3)
		if id > 18 {
			r.y += 100
		}
		out = append(out, r)
	}
	return out
}()

func mgSchema() encoding.Schema {
	cat := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			_, _ = d.Add(v)
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("north", "south", "west")},
		{Name: "sex", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("F", "M")},
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: cat("gold", "silver")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: cat("a", "b")},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "day", Type: encoding.FieldTypeDate},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Multi-grouper gate column " + fields[i].Name + "."
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	return encoding.Schema{Fields: fields}
}

// mgFixture is the gate's engine over the 30-row cohort.
type mgFixture struct {
	p      *pulse.Pulse
	fs     afero.Fs
	cohort string
}

func newMGFixture(t *testing.T, opts pulse.Options) *mgFixture {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "mg.pulse", mgSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range mgRows {
		var tier, tags any
		if r.tier != "" {
			tier = r.tier
		}
		if r.tags != nil {
			tags = r.tags
		}
		dayStr := "2024-01-15"
		if r.id%2 == 0 {
			dayStr = "2024-02-15"
		}
		d, err := encoding.ParseDate(dayStr)
		if err != nil {
			t.Fatal(err)
		}
		row := pulse.CohortRow{uint64(r.id), r.region, r.sex, tier, tags, float64(r.id), r.y, int32(d)}
		if err := b.Append(row); err != nil {
			t.Fatalf("Append(%d): %v", r.id, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &mgFixture{p: p, fs: fsys, cohort: "mg.pulse"}
}

// mgMode is one execution mode of the gate. run returns the response
// for req (a fresh request per call; the mode may add slots).
type mgMode struct {
	name string
	run  func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error)
}

// mgModes: buffered Process (a non-streamable AGG_MEDIAN slot forces
// the buffered arm), streaming Process and ProcessStream (every slot
// streamable; a request whose groupers all stream must ride the
// streaming arm — a non-streamable grouper, GROUP_DATE / GROUP_QUANTILE
// / GROUP_KMEANS, routes buffered by design). E2-S1 / E2-S2 append
// their modes.
var mgModes = []mgMode{
	{"Buffered", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score", Label: "med"})
		return f.p.Process(context.Background(), req)
	}},
	{"Streaming", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		mgRequireStreamable(t, f, req)
		return f.p.Process(context.Background(), req)
	}},
	{"ProcessStream", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		mgRequireStreamable(t, f, req)
		iter, err := f.p.ProcessStream(context.Background(), req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = iter.Close() }()
		resp := &types.Response{}
		for {
			row, ok, err := iter.Next(context.Background())
			if err != nil {
				return nil, err
			}
			if !ok {
				break
			}
			resp.Data = append(resp.Data, row)
		}
		resp.Components = iter.Components()
		return resp, nil
	}},
}

// mgRequireStreamable fails unless predict routes req to the streaming
// arm whenever every grouper is streamable (predict's Streamable
// mirrors the runtime route: TestPredict_Streamable_MatchesRuntime), so
// a streaming mode cannot silently pass on the buffered arm.
func mgRequireStreamable(t *testing.T, f *mgFixture, req *types.Request) {
	t.Helper()
	for _, g := range req.Groups {
		if !g.Type.Streamable() {
			return
		}
	}
	pr, err := f.p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !pr.Streamable {
		t.Fatalf("streaming mode: request routes buffered (%v)", pr.StreamableReasons)
	}
}

func mgGroup(typ types.GroupType, field string) *types.Group {
	return &types.Group{Type: typ, Field: field}
}

var (
	mgRegion   = func() *types.Group { return mgGroup(types.GROUP_CATEGORY, "region") }
	mgSex      = func() *types.Group { return mgGroup(types.GROUP_CATEGORY, "sex") }
	mgTier     = func() *types.Group { return mgGroup(types.GROUP_CATEGORY, "tier") }
	mgTags     = func() *types.Group { return mgGroup(types.GROUP_SET_PER_ELEMENT, "tags") }
	mgDay      = func() *types.Group { return mgGroup(types.GROUP_DATE, "day") }
	mgScoreRng = func() *types.Group { return &types.Group{Type: types.GROUP_RANGE, Field: "score", Interval: 10} }
	mgQuartile = func() *types.Group { return &types.Group{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4} }
	mgKMeans   = func() *types.Group {
		return &types.Group{Type: types.GROUP_KMEANS, Fields: []string{"y"}, Params: json.RawMessage(`{"k":2}`)}
	}
)

// mgCase is one gate row: the groups, the key columns every Data row
// must carry, and the expected rows ("col=val|...|n=count") in order.
type mgCase struct {
	name   string
	groups []*types.Group
	cols   []string
	want   []string
}

func mgCases() []mgCase {
	return []mgCase{
		{
			// The #422 repro: 3 regions × 2 sexes, all six observed.
			name: "CategoryXCategory", groups: []*types.Group{mgRegion(), mgSex()},
			cols: []string{"region", "sex"},
			want: []string{
				"north|F|7", "north|M|5",
				"south|F|4", "south|M|6",
				"west|F|3", "west|M|5",
			},
		},
		{
			// Outer → inner follows list order, not field order.
			name: "CategoryXCategoryReversed", groups: []*types.Group{mgSex(), mgRegion()},
			cols: []string{"sex", "region"},
			want: []string{
				"F|north|7", "F|south|4", "F|west|3",
				"M|north|5", "M|south|6", "M|west|5",
			},
		},
		{
			// [0,10) ids 1-9, [10,20) 10-19, [20,30) 20-29, [30,40) 30.
			name: "RangeXCategory", groups: []*types.Group{mgScoreRng(), mgRegion()},
			cols: []string{"score", "region"},
			want: []string{
				"0-10|north|9",
				"10-20|north|3", "10-20|south|7",
				"20-30|south|3", "20-30|west|7",
				"30-40|west|1",
			},
		},
		{
			// Odd ids in January, even in February.
			name: "DateXCategory", groups: []*types.Group{mgDay(), mgSex()},
			cols: []string{"day", "sex"},
			want: []string{
				"2024-01|F|8", "2024-01|M|7",
				"2024-02|F|6", "2024-02|M|9",
			},
		},
		{
			// A {a,b} record lands in both a-rows; empty masks and nulls
			// (ids 6, 7) land nowhere.
			name: "SetPerElementFanOutXCategory", groups: []*types.Group{mgTags(), mgSex()},
			cols: []string{"tags", "sex"},
			want: []string{
				"a|F|8", "a|M|3",
				"b|F|7", "b|M|1",
			},
		},
		{
			// west×gold is never observed, so it is never emitted; null
			// tier (ids 6, 7, 16, 22, 28, 29, 30) drops the record.
			name: "EmptyCombinationAndInnerNull", groups: []*types.Group{mgRegion(), mgTier()},
			cols: []string{"region", "tier"},
			want: []string{
				"north|gold|5", "north|silver|5",
				"south|gold|4", "south|silver|4",
				"west|silver|5",
			},
		},
		{
			name: "OuterNull", groups: []*types.Group{mgTier(), mgSex()},
			cols: []string{"tier", "sex"},
			want: []string{
				"gold|F|4", "gold|M|5",
				"silver|F|7", "silver|M|7",
			},
		},
		{
			// Quartiles fitted over the WHOLE set (bucket = rank·4/30:
			// Q1 ids 1-8, Q2 9-15, Q3 16-23, Q4 24-30), not per region
			// (a per-region refit would split north 3/3/3/3).
			name: "QuantileNotFirst", groups: []*types.Group{mgRegion(), mgQuartile()},
			cols: []string{"region", "score"},
			want: []string{
				"north|Q1|8", "north|Q2|4",
				"south|Q2|3", "south|Q3|7",
				"west|Q3|1", "west|Q4|7",
			},
		},
		{
			// k=2 over y fitted once: cluster_1 (ids 1-18, the larger)
			// and cluster_2 (ids 19-30).
			name: "KMeansNotFirst", groups: []*types.Group{mgSex(), mgKMeans()},
			cols: []string{"sex", "GROUP_KMEANS_y"},
			want: []string{
				"F|cluster_1|11", "F|cluster_2|3",
				"M|cluster_1|7", "M|cluster_2|9",
			},
		},
		{
			// Native order is each grouper's include order, not the
			// alphabetical order of the composite key; south (not
			// included) lands in no row.
			name: "IncludeOrderBothPositions", groups: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"west", "north"}},
				{Type: types.GROUP_CATEGORY, Field: "sex", Include: []string{"M", "F"}},
			},
			cols: []string{"region", "sex"},
			want: []string{"west|M|5", "west|F|3", "north|M|5", "north|F|7"},
		},
		{
			name: "ThreeCategories", groups: []*types.Group{mgRegion(), mgSex(), mgTier()},
			cols: []string{"region", "sex", "tier"},
			want: []string{
				"north|F|gold|3", "north|F|silver|2",
				"north|M|gold|2", "north|M|silver|3",
				"south|F|gold|1", "south|F|silver|2",
				"south|M|gold|3", "south|M|silver|2",
				"west|F|silver|3", "west|M|silver|2",
			},
		},
		{
			// Three groupers with an outer null and a middle fan-out.
			name: "ThreeWithNullAndFanOut", groups: []*types.Group{mgTier(), mgTags(), mgSex()},
			cols: []string{"tier", "tags", "sex"},
			want: []string{
				"gold|a|F|4", "gold|a|M|3", "gold|b|F|4",
				"silver|a|F|4", "silver|b|F|3", "silver|b|M|1",
			},
		},
	}
}

func mgRequest(f *mgFixture, groups []*types.Group, aggs ...*types.Aggregation) *types.Request {
	if len(aggs) == 0 {
		aggs = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
	}
	return &types.Request{Cohort: &types.Cohort{Filename: f.cohort}, Groups: groups, Aggregations: aggs}
}

// mgRender renders Data as "k1|k2|...|n" lines, failing on a row that
// lacks a key column.
func mgRender(t *testing.T, data []map[string]any, cols []string, metric string) []string {
	t.Helper()
	out := make([]string, 0, len(data))
	for i, row := range data {
		parts := make([]string, 0, len(cols)+1)
		for _, c := range cols {
			v, ok := row[c]
			if !ok {
				t.Fatalf("Data[%d] carries no key column %q: %v", i, c, row)
			}
			parts = append(parts, fmt.Sprint(v))
		}
		parts = append(parts, fmt.Sprint(row[metric]))
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

// TestMultiGrouper_Gate is the #422 gate: every case × every mode
// matches the hand-computed rows exactly (count, order, key columns).
func TestMultiGrouper_Gate(t *testing.T) {
	for _, mode := range mgModes {
		f := newMGFixture(t, pulse.Options{})
		for _, c := range mgCases() {
			t.Run(mode.name+"/"+c.name, func(t *testing.T) {
				resp, err := mode.run(t, f, mgRequest(f, c.groups))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got := mgRender(t, resp.Data, c.cols, "n"); !reflect.DeepEqual(got, c.want) {
					t.Fatalf("rows\n got  %q\n want %q", got, c.want)
				}
			})
		}
	}
}

// TestMultiGrouper_ComponentsCarryTuple: buffered grouped Components
// key every per-group entry by the full key tuple (one element per
// grouper) in Data order, and Components.Groupers has one entry per
// grouper slot with its own floor.
func TestMultiGrouper_ComponentsCarryTuple(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for _, mode := range mgModes {
		t.Run(mode.name, func(t *testing.T) {
			resp, err := mode.run(t, f, mgRequest(f, []*types.Group{mgRegion(), mgTier()}))
			if err != nil {
				t.Fatal(err)
			}
			if resp.Components == nil || len(resp.Components.Aggregations) == 0 {
				t.Fatalf("no aggregation components: %+v", resp.Components)
			}
			groups := resp.Components.Aggregations[0].Groups
			if len(groups) != len(resp.Data) {
				t.Fatalf("%d groups for %d Data rows", len(groups), len(resp.Data))
			}
			for i, g := range groups {
				want := types.AxisKey{resp.Data[i]["region"], resp.Data[i]["tier"]}
				if !reflect.DeepEqual(g.GroupKey, want) {
					t.Errorf("groups[%d].group_key = %v, want %v", i, g.GroupKey, want)
				}
			}
			// region: no nulls, every record bucketed. tier: 7 nulls.
			want := []struct {
				field         string
				totalN, nNull int
			}{{"region", 30, 0}, {"tier", 23, 7}}
			if len(resp.Components.Groupers) != len(want) {
				t.Fatalf("%d grouper entries, want %d", len(resp.Components.Groupers), len(want))
			}
			for i, w := range want {
				g := resp.Components.Groupers[i]
				if g.Field != w.field || g.TotalN != w.totalN || g.NNull != w.nNull {
					t.Errorf("groupers[%d] = {%s %d %d}, want {%s %d %d}", i, g.Field, g.TotalN, g.NNull, w.field, w.totalN, w.nNull)
				}
			}
		})
	}
}

// TestMultiGrouper_DataDependentKeyIndependentOfPosition: a QUANTILE /
// KMEANS grouper's key for a record is the key its single-grouper run
// gives it, whatever position it holds: the per-(key, region) sums of
// id agree between [g, region] and [region, g], and summed over region
// they equal the single-grouper run's per-key sum.
func TestMultiGrouper_DataDependentKeyIndependentOfPosition(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	sum := &types.Aggregation{Type: types.AGG_SUM, Field: "id", Label: "s"}
	for _, mode := range mgModes {
		for _, c := range []struct {
			name string
			g    func() *types.Group
			col  string
		}{
			{"Quantile", mgQuartile, "score"},
			{"KMeans", mgKMeans, "GROUP_KMEANS_y"},
		} {
			t.Run(mode.name+"/"+c.name, func(t *testing.T) {
				run := func(groups ...*types.Group) []map[string]any {
					resp, err := mode.run(t, f, mgRequest(f, groups, sum))
					if err != nil {
						t.Fatal(err)
					}
					return resp.Data
				}
				pairs := func(data []map[string]any) map[string]float64 {
					out := map[string]float64{}
					for _, row := range data {
						out[fmt.Sprint(row[c.col])+"|"+fmt.Sprint(row["region"])] = row["s"].(float64)
					}
					return out
				}
				inner := pairs(run(mgRegion(), c.g()))
				outer := pairs(run(c.g(), mgRegion()))
				if !reflect.DeepEqual(inner, outer) {
					t.Fatalf("key depends on position:\n [region, g] %v\n [g, region] %v", inner, outer)
				}
				marg := map[string]float64{}
				for k, v := range inner {
					marg[strings.SplitN(k, "|", 2)[0]] += v
				}
				single := map[string]float64{}
				for _, row := range run(c.g()) {
					single[fmt.Sprint(row[c.col])] = row["s"].(float64)
				}
				if !reflect.DeepEqual(marg, single) {
					t.Fatalf("composite margins %v differ from the single-grouper run %v", marg, single)
				}
			})
		}
	}
}

// TestMultiGrouper_MaxGroupsCountsCompositeKeys: the runtime max_groups
// check counts observed composite keys — [region, sex] observes 6
// though neither grouper alone exceeds 3.
func TestMultiGrouper_MaxGroupsCountsCompositeKeys(t *testing.T) {
	for _, mode := range mgModes {
		t.Run(mode.name, func(t *testing.T) {
			over := newMGFixture(t, pulse.Options{Limits: pulse.Limits{MaxGroups: 5}})
			if _, err := mode.run(t, over, mgRequest(over, []*types.Group{mgRegion(), mgSex()})); err == nil {
				t.Fatal("6 observed composite keys passed max_groups=5")
			} else {
				requireLimitExceeded(t, err, "max_groups", 5, "Options.Limits.MaxGroups")
			}
			at := newMGFixture(t, pulse.Options{Limits: pulse.Limits{MaxGroups: 6}})
			resp, err := mode.run(t, at, mgRequest(at, []*types.Group{mgRegion(), mgSex()}))
			if err != nil || len(resp.Data) != 6 {
				t.Fatalf("at the limit: %v, %v", resp, err)
			}
		})
	}
}

// TestMultiGrouper_GrouperComponentsPerSlot: Components.Groupers has
// one entry per grouper slot, each equal to that grouper's own
// single-grouper run in the same mode — every grouper is driven once per
// record, even after an earlier position's null key (tier is null for 7
// records; sex still sees all 30) — and the entries agree across modes.
func TestMultiGrouper_GrouperComponentsPerSlot(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	groups := func() []*types.Group { return []*types.Group{mgTier(), mgTags(), mgSex()} }
	floors := []struct {
		field         string
		totalN, nNull int
	}{{"tier", 23, 7}, {"tags", 19, 11}, {"sex", 30, 0}}
	var first []types.GrouperComponents
	for _, mode := range mgModes {
		t.Run(mode.name, func(t *testing.T) {
			resp, err := mode.run(t, f, mgRequest(f, groups()))
			if err != nil {
				t.Fatal(err)
			}
			if resp.Components == nil || len(resp.Components.Groupers) != len(floors) {
				t.Fatalf("grouper entries: %+v, want %d", resp.Components, len(floors))
			}
			got := resp.Components.Groupers
			for i, g := range groups() {
				w := floors[i]
				if got[i].Field != w.field || got[i].TotalN != w.totalN || got[i].NNull != w.nNull {
					t.Errorf("groupers[%d] = {%s %d %d}, want {%s %d %d}", i, got[i].Field, got[i].TotalN, got[i].NNull, w.field, w.totalN, w.nNull)
				}
				single, err := mode.run(t, f, mgRequest(f, []*types.Group{g}))
				if err != nil {
					t.Fatal(err)
				}
				if single.Components == nil || len(single.Components.Groupers) != 1 {
					t.Fatalf("single-grouper run %s: %+v", w.field, single.Components)
				}
				if want := single.Components.Groupers[0]; !reflect.DeepEqual(got[i], want) {
					t.Errorf("groupers[%d] differs from its single-grouper run:\n got  %+v\n want %+v", i, got[i], want)
				}
			}
			if first == nil {
				first = got
			} else if !reflect.DeepEqual(got, first) {
				t.Errorf("groupers differ from mode %s:\n got  %+v\n want %+v", mgModes[0].name, got, first)
			}
		})
	}
}

// TestMultiGrouper_ExtensionGrouperFloorPerSlot: a bucket-less
// extension grouper (no components payload, so TotalN is the engine's
// own per-grouper assignment count) behind a null-bearing grouper still
// counts every record it keyed — its own count, not the composite
// rows' and not another grouper's.
func TestMultiGrouper_ExtensionGrouperFloorPerSlot(t *testing.T) {
	f := newMGFixture(t, pulse.Options{Extensions: pulse.Extensions{Groupers: floorGrouperRegistrations(&parityProbe{})}})
	for _, mode := range mgModes {
		t.Run(mode.name, func(t *testing.T) {
			req := mgRequest(f, []*types.Group{mgTier(), mgGroup(grpMrgFloor, "sex")})
			resp, err := mode.run(t, f, req)
			if err != nil {
				t.Fatal(err)
			}
			if got := mgRender(t, resp.Data, []string{"tier", "sex"}, "n"); !reflect.DeepEqual(got, []string{"gold|F|4", "gold|M|5", "silver|F|7", "silver|M|7"}) {
				t.Fatalf("rows %q", got)
			}
			if resp.Components == nil || len(resp.Components.Groupers) != 2 {
				t.Fatalf("grouper entries: %+v", resp.Components)
			}
			if g := resp.Components.Groupers[1]; g.Field != "sex" || g.TotalN != 30 || g.NNull != 0 {
				t.Fatalf("extension grouper floor = {%s %d %d}, want {sex 30 0}", g.Field, g.TotalN, g.NNull)
			}
		})
	}
}

// TestMultiGrouper_StreamChunkComponents: ProcessStreamResult's chunk
// components over composite rows — every chunk carries one grouper
// entry per slot; a mid-stream chunk drops the per-group figures and
// only the None grouper's operator map (GROUP_QUANTILE, slot 1), and the
// terminal chunk carries each Data row's full key tuple.
func TestMultiGrouper_StreamChunkComponents(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	for _, c := range []struct {
		name      string
		groups    []*types.Group
		cols      []string
		noneSlots map[int]bool
	}{
		{"Streaming", []*types.Group{mgRegion(), mgTier()}, []string{"region", "tier"}, nil},
		{"QuantileInner", []*types.Group{mgRegion(), mgQuartile()}, []string{"region", "score"}, map[int]bool{1: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := f.p.ProcessStreamResult(context.Background(), mgRequest(f, c.groups))
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
				t.Fatalf("%d chunks; the fixture needs a mid-stream chunk", len(chunks))
			}
			for i, ch := range chunks {
				comp := ch.Components
				if comp == nil || len(comp.Groupers) != len(c.groups) {
					t.Fatalf("chunk %d: grouper entries %+v, want %d", i, comp, len(c.groups))
				}
				terminal := i == len(chunks)-1
				for s, g := range comp.Groupers {
					if wantNil := !terminal && c.noneSlots[s]; (g.Operator == nil) != wantNil {
						t.Errorf("chunk %d groupers[%d] operator nil = %v, want %v", i, s, g.Operator == nil, wantNil)
					}
				}
				groups := comp.Aggregations[0].Groups
				if !terminal {
					if groups != nil {
						t.Errorf("chunk %d: mid-stream per-group figures %v", i, groups)
					}
					continue
				}
				if len(groups) != len(chunks) {
					t.Fatalf("terminal: %d groups for %d rows", len(groups), len(chunks))
				}
				for r, ch := range chunks {
					want := make(types.AxisKey, len(c.cols))
					for k, col := range c.cols {
						want[k] = ch.Data[col]
					}
					if !reflect.DeepEqual(groups[r].GroupKey, want) {
						t.Errorf("groups[%d].group_key = %v, want %v", r, groups[r].GroupKey, want)
					}
				}
			}
		})
	}
}

// TestMultiGrouper_MatricesCarryTuple: a grouped matrix over composite
// rows is keyed by its Data row's full key tuple (never the composite
// string) under a header naming every grouper, in Data order, on the
// buffered and streaming arms alike. (ProcessStream's row iterator
// carries no matrices.)
func TestMultiGrouper_MatricesCarryTuple(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	header := types.AxisHeader{Fields: []string{"region", "sex"}, Types: []string{"GROUP_CATEGORY", "GROUP_CATEGORY"}}
	for _, mode := range mgModes {
		if mode.name == "ProcessStream" {
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
			req.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"score", "y"}}}
			resp, err := mode.run(t, f, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Data) != 6 || len(resp.Matrices) != len(resp.Data) {
				t.Fatalf("%d matrices for %d rows", len(resp.Matrices), len(resp.Data))
			}
			for i, m := range resp.Matrices {
				want := types.AxisKey{resp.Data[i]["region"], resp.Data[i]["sex"]}
				if !reflect.DeepEqual(m.GroupKey, want) {
					t.Errorf("matrices[%d].group_key = %q, want %q", i, m.GroupKey, want)
				}
				if m.GroupHeader == nil || !reflect.DeepEqual(*m.GroupHeader, header) {
					t.Errorf("matrices[%d].group_header = %+v, want %+v", i, m.GroupHeader, header)
				}
			}
		})
	}
}

// TestMultiGrouper_PredictBoundsRuntime is the predict/runtime parity
// row: predict's max_groups estimate for a multi-grouper request is the
// PRODUCT of every grouper's bound (a POSSIBLE finding — an upper
// bound), every mode's observed row count never exceeds it, and the two
// are equal when every combination is observed. A grouper whose keys
// depend on the data (range, date) leaves the request unsized: no
// finding, never a guess.
func TestMultiGrouper_PredictBoundsRuntime(t *testing.T) {
	// Hand-computed over mgSchema: region 3, sex 2, tier 2, tags 2,
	// quartiles 4, k-means k=2, an include list its length. 0 = unsized.
	estimate := map[string]int64{
		"CategoryXCategory":            6,
		"CategoryXCategoryReversed":    6,
		"RangeXCategory":               0,
		"DateXCategory":                0,
		"SetPerElementFanOutXCategory": 4,
		"EmptyCombinationAndInnerNull": 6,
		"OuterNull":                    4,
		"QuantileNotFirst":             12,
		"KMeansNotFirst":               4,
		"IncludeOrderBothPositions":    4,
		"ThreeCategories":              12,
		"ThreeWithNullAndFanOut":       8,
	}
	// Every combination observed: the bound is exact.
	exact := map[string]bool{
		"CategoryXCategory": true, "CategoryXCategoryReversed": true,
		"SetPerElementFanOutXCategory": true, "OuterNull": true,
		"KMeansNotFirst": true, "IncludeOrderBothPositions": true,
	}
	// MaxGroups 1 makes every sized estimate a reported finding; the
	// predict-only engine never runs a record.
	pf := newMGFixture(t, pulse.Options{Limits: pulse.Limits{MaxGroups: 1}})
	f := newMGFixture(t, pulse.Options{})
	for _, c := range mgCases() {
		want, ok := estimate[c.name]
		if !ok {
			t.Fatalf("case %s has no hand-computed estimate", c.name)
		}
		pr, err := pf.p.Predict(context.Background(), mgRequest(pf, c.groups))
		if err != nil {
			t.Fatalf("%s: Predict: %v", c.name, err)
		}
		var got int64
		for _, lf := range pr.LimitFindings {
			if lf.Limit == "max_groups" {
				if lf.Grade != descriptor.LimitGradePossible {
					t.Fatalf("%s: max_groups grade %s, want possible (an upper bound)", c.name, lf.Grade)
				}
				got = lf.Estimated
			}
		}
		if got != want {
			t.Fatalf("%s: predict max_groups estimate %d, want %d", c.name, got, want)
		}
		if want == 0 {
			continue
		}
		for _, mode := range mgModes {
			resp, err := mode.run(t, f, mgRequest(f, c.groups))
			if err != nil {
				t.Fatalf("%s/%s: %v", mode.name, c.name, err)
			}
			rows := int64(len(resp.Data))
			if rows > got {
				t.Errorf("%s/%s: %d rows above the predicted bound %d", mode.name, c.name, rows, got)
			}
			if exact[c.name] && rows != got {
				t.Errorf("%s/%s: every combination observed, yet %d rows != bound %d", mode.name, c.name, rows, got)
			}
		}
	}
}

// grpMGNul keys like GROUP_CATEGORY but appends a NUL byte and a
// suffix to every key — the composite-key separator inside a key.
const grpMGNul types.GroupType = "GROUP_MGT_NUL"

type nulKeyGrouper struct{ inner *parityCategoryGrouper }

func (g nulKeyGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	b, err := g.inner.Group(rows, field)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]int, len(b))
	for k, v := range b {
		out[k+"\x00x"] = v
	}
	return out, nil
}

func (g nulKeyGrouper) KeyForRow(rec extend.Record, field string) (string, bool, error) {
	k, ok, err := g.inner.KeyForRow(rec, field)
	return k + "\x00x", ok, err
}

// TestMultiGrouper_SeparatorInKeyRefused: an extension grouper whose
// key carries the composite-key separator ("\x00") is refused with
// PROCESSING_GROUP under two or more groupers, at any position, on
// every mode — the composite string would be ambiguous — while the
// same grouper alone runs unchanged.
func TestMultiGrouper_SeparatorInKeyRefused(t *testing.T) {
	reg := pulse.GrouperRegistration{
		Name:       grpMGNul,
		Streamable: true,
		Factory: func(spec *types.Group, schema *encoding.Schema) (extend.Grouper, error) {
			return nulKeyGrouper{&parityCategoryGrouper{probe: &parityProbe{}, dict: fieldDict(schema, spec.Field), live: map[string]int{}}}, nil
		},
		FieldInputs: func(json.RawMessage) []string { return nil },
	}
	f := newMGFixture(t, pulse.Options{Extensions: pulse.Extensions{Groupers: []pulse.GrouperRegistration{reg}}})
	for _, mode := range mgModes {
		t.Run(mode.name+"/alone", func(t *testing.T) {
			resp, err := mode.run(t, f, mgRequest(f, []*types.Group{mgGroup(grpMGNul, "sex")}))
			if err != nil {
				t.Fatalf("single grouper refused: %v", err)
			}
			if len(resp.Data) != 2 || resp.Data[0]["sex"] != "F\x00x" {
				t.Fatalf("single grouper rows %v", resp.Data)
			}
		})
		for name, c := range map[string]struct {
			groups []*types.Group
			index  int
		}{
			"inner": {[]*types.Group{mgRegion(), mgGroup(grpMGNul, "sex")}, 1},
			"outer": {[]*types.Group{mgGroup(grpMGNul, "sex"), mgRegion()}, 0},
		} {
			t.Run(mode.name+"/"+name, func(t *testing.T) {
				resp, err := mode.run(t, f, mgRequest(f, c.groups))
				if resp != nil && len(resp.Data) > 0 {
					t.Fatalf("an ambiguous composite returned rows: %v", resp.Data)
				}
				ce := requireCodedError(t, err, perr.PROCESSING_GROUP)
				if ce.Details["reason"] != "key_contains_separator" || ce.Details["group_index"] != c.index {
					t.Fatalf("details = %v, want reason key_contains_separator at group_index %d", ce.Details, c.index)
				}
			})
		}
	}
}
