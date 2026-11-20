package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// unionOracleLabels are the oracle fixture's member labels, in
// declared order.
var unionOracleLabels = []string{"wave1", "wave2", "wave3"}

// unionOracleSchema is a member schema: tags at rung tagsType (the
// second member widens the union's rung), and — for the oracle's
// labelled twins — a trailing _source categorical pre-seeded with the
// labels in declared order.
func unionOracleSchema(tagsType encoding.FieldType, labelled bool) encoding.Schema {
	s := encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeF64, Nullable: true, Description: "Order amount in dollars"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Sales region of the order"},
		{Name: "tags", Type: tagsType, Nullable: true, Description: "Marketing tags on the order"},
		{Name: "units", Type: encoding.FieldTypeU16, Description: "Units shipped on the order"},
	}}
	if labelled {
		d := encoding.NewDictionary()
		for _, l := range unionOracleLabels {
			if _, err := d.Add(l); err != nil {
				panic(err)
			}
		}
		s.Fields = append(s.Fields, encoding.Field{Name: types.DefaultUnionSourceField, Type: encoding.FieldTypeCategoricalU8,
			Description: "Union source: the label of the member cohort the record was read from.", Dictionary: d})
	}
	return s
}

// unionOracleRows are member m's rows: regions and tags in a
// member-specific first-seen order (so every member's dictionaries
// differ and the merge remaps them), every fourth row null.
func unionOracleRows(m, n int) []CohortRow {
	regions := [][]string{{"north", "south"}, {"east", "south", "north"}, {"south", "west"}}[m]
	tags := [][]string{{"x", "y"}, {"z", "y", "x"}, {"y", "w"}}[m]
	rows := make([]CohortRow, 0, n)
	for i := 0; i < n; i++ {
		var amount, region, tag any = float64(m*100+i) * 1.5, regions[i%len(regions)], []string{tags[i%len(tags)], tags[(i+1)%len(tags)]}
		if i%4 == 3 {
			amount, region, tag = nil, nil, nil
		}
		rows = append(rows, CohortRow{amount, region, tag, uint64(1 + (m+i)%5)})
	}
	return rows
}

// unionOracleFixture writes the union members and their oracle:
//
//   - m0.pulse: a single file (7 rows, tags set_u8);
//   - m1.pulse: a single file (6 rows, tags set_u16 — the union widens);
//   - m2.pulse: a shard archive of 3 shards (7 rows split 3/3/1);
//   - oracle.pulse: a shard archive of the same records, in the same
//     shard order, each carrying a real _source column of its member's
//     label — what the union reads, written to disk.
func unionOracleFixture(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	build := func(path string, schema encoding.Schema, rows []CohortRow, split int) {
		t.Helper()
		opts := CohortBuilderOptions{}
		if split > 0 {
			opts.Shards = &ShardSplit{MaxRecords: split}
		}
		b, err := p.NewCohortBuilder(ctx, path, schema, opts)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, r := range rows {
			if err := b.Append(r); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	sizes := []int{7, 6, 7}
	rungs := []encoding.FieldType{encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU8}
	var oracleShards []string
	for m := range sizes {
		rows := unionOracleRows(m, sizes[m])
		split := 0
		if m == 2 {
			split = 3
		}
		build(fmt.Sprintf("m%d.pulse", m), unionOracleSchema(rungs[m], false), rows, split)
		labelled := make([]CohortRow, len(rows))
		for i, r := range rows {
			labelled[i] = append(append(CohortRow(nil), r...), unionOracleLabels[m])
		}
		chunk := len(labelled)
		if split > 0 {
			chunk = split
		}
		for start, k := 0, 0; start < len(labelled); start, k = start+chunk, k+1 {
			path := fmt.Sprintf("oracle_m%d_%d.pulse", m, k)
			build(path, unionOracleSchema(rungs[m], true), labelled[start:min(start+chunk, len(labelled))], 0)
			oracleShards = append(oracleShards, path)
		}
	}
	if _, err := p.CreateShardArchive(ctx, "oracle.pulse", oracleShards); err != nil {
		t.Fatalf("oracle archive: %v", err)
	}
	return fsys
}

// unionOver returns req reading the three members as a union.
func unionOver(req *Request) *Request {
	out := *req
	out.Cohort = nil
	for m, l := range unionOracleLabels {
		out.Union = append(out.Union, types.UnionMember{Cohort: &types.Cohort{Filename: fmt.Sprintf("m%d.pulse", m)}, Label: l})
	}
	return &out
}

// oracleOver returns req reading the oracle archive.
func oracleOver(req *Request) *Request {
	out := *req
	out.Union = nil
	out.Cohort = &types.Cohort{Filename: "oracle.pulse"}
	return &out
}

// comparableJSON is resp's wire form without Metadata (which names the
// cohort file the union does not have).
func comparableJSON(t *testing.T, resp *Response) string {
	t.Helper()
	c := *resp
	c.Metadata = nil
	b, err := json.Marshal(&c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unionOracleRequests are the request shapes the oracle compares: the
// source field as a grouper, a filter, a crosstab axis (under an
// overlay) and a test split; remapped categoricals and widened sets as
// groupers; a weight; a regression.
func unionOracleRequests() map[string]*Request {
	src := types.DefaultUnionSourceField
	return map[string]*Request{
		"by_source": {
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "amount", Label: "n"},
				{Type: types.AGG_SUM, Field: "amount", Label: "total"},
				{Type: types.AGG_AVERAGE, Field: "units", Label: "avg_units"},
			},
		},
		"ungrouped_streamable": {
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "amount", Label: "total"},
				{Type: types.AGG_MAX, Field: "units", Label: "hi"},
			},
		},
		"source_by_region": {
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}, {Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "units", Label: "n"}, {Type: types.AGG_MEDIAN, Field: "amount", Label: "med"}},
		},
		"widened_set": {
			Groups:       []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "units", Label: "n"}},
		},
		"filtered_source": {
			Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: src, Values: []string{"wave2", "wave3"}}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "units", Label: "u"}},
		},
		"weighted": {
			Weight:       &types.WeightSpec{Field: "units", Kind: types.WeightKindFrequency},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "amount", Label: "wavg"}},
		},
		"tests": {
			Tests: []*types.Test{
				{Type: types.TEST_CHISQ, Rows: "region", Cols: src, Label: "chi"},
				{Type: types.TEST_ANOVA_F, Field: "amount", SplitBy: src, Label: "anova"},
			},
		},
		"regression": {
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "amount", Predictors: []string{"units"}}},
		},
		"overlay": {
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "units", Label: "u"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsMargin, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}}},
		},
		"crosstab": {
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "units", Label: "n"},
			},
		},
	}
}

// A union reads exactly what a shard archive of the same members'
// shards — each record carrying its member's label in a real column —
// reads: every request shape, serial and per-shard parallel, through
// Process, ProcessStream and a Compose slot, answers byte-identically
// (Metadata aside: it names the archive file). The members' dictionaries
// differ (the union remaps them), one member's set rung is wider (the
// union widens), and one member is itself a shard archive (flattened).
func TestUnion_ReadsAsShardArchiveOracle(t *testing.T) {
	t.Parallel()
	fsys := unionOracleFixture(t)
	ctx := context.Background()
	// The union writes nothing: no merged file, index or sidecar.
	files := func() string {
		var names []string
		_ = afero.Walk(fsys, ".", func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				names = append(names, fmt.Sprintf("%s:%d", path, info.Size()))
			}
			return nil
		})
		return fmt.Sprint(names)
	}
	before := files()
	if !strings.Contains(before, "oracle.pulse") {
		t.Fatalf("file snapshot %s misses the fixture", before)
	}
	defer func() {
		if after := files(); after != before {
			t.Errorf("a union run changed the file system:\nbefore %s\nafter  %s", before, after)
		}
	}()
	for _, workers := range []int{1, 4} {
		p, err := New(Options{FS: fsys, ShardWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		for name, req := range unionOracleRequests() {
			want, err := p.Process(ctx, oracleOver(req))
			if err != nil {
				t.Fatalf("%s/workers=%d: oracle: %v", name, workers, err)
			}
			got, err := p.Process(ctx, unionOver(req))
			if err != nil {
				t.Fatalf("%s/workers=%d: union: %v", name, workers, err)
			}
			if g, w := comparableJSON(t, got), comparableJSON(t, want); g != w {
				t.Errorf("%s/workers=%d: union != archive oracle\nunion:  %s\noracle: %s", name, workers, g, w)
			}
			// (A crosstab carries no run components, on an archive either.)
			if req.Crosstab == nil && (got.Components == nil || got.Components.Run == nil || got.Components.Run.ShardCount != 5 || got.Components.Run.TotalRecords != 20) {
				t.Errorf("%s/workers=%d: run components %+v, want 5 shards / 20 records", name, workers, got.Components.Run)
			}
		}
	}

	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := unionOracleRequests()["by_source"]
	want, err := p.Process(ctx, oracleOver(req))
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Data) != 3 || want.Data[0][types.DefaultUnionSourceField] != "wave1" || want.Data[2]["n"] == nil {
		t.Fatalf("oracle by_source rows %+v, want one per label", want.Data)
	}
	// ProcessStream: the same rows.
	iter, err := p.ProcessStream(ctx, unionOver(req))
	if err != nil {
		t.Fatalf("ProcessStream(union): %v", err)
	}
	var rows []Row
	for {
		row, ok, err := iter.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		rows = append(rows, row)
	}
	_ = iter.Close()
	gotRows, _ := json.Marshal(rows)
	wantRows, _ := json.Marshal(want.Data)
	if string(gotRows) != string(wantRows) {
		t.Errorf("ProcessStream(union) rows %s, want %s", gotRows, wantRows)
	}
	// Compose: a union slot beside the oracle slot answers alike.
	u, o := unionOver(req), oracleOver(req)
	u.Label, o.Label = "union", "oracle"
	composed, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{u, o}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	gotSlot, _ := json.Marshal(composed.Responses[0].Data)
	wantSlot, _ := json.Marshal(composed.Responses[1].Data)
	if string(gotSlot) != string(wantSlot) || string(wantSlot) != string(wantRows) {
		t.Errorf("Compose union slot %s, oracle slot %s", gotSlot, wantSlot)
	}
}

// Predict judges a union over the merged schema exactly as the run
// executes it: the summed record count, the flattened shard count and
// the streamability verdict all match what the run reports.
func TestUnion_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	fsys := unionOracleFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	var arm observe.Arm
	p, err := New(Options{FS: fsys, ShardWorkers: 1, Hooks: &observe.Hooks{OnOperationEnd: func(_ context.Context, info observe.OperationInfo, r observe.OperationResult) {
		if info.Kind == observe.OpProcess {
			mu.Lock()
			arm = r.Arm
			mu.Unlock()
		}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range unionOracleRequests() {
		res, err := p.Predict(ctx, unionOver(req))
		if err != nil || !res.Valid {
			t.Fatalf("%s: predict = %+v, %v", name, res, err)
		}
		resp, err := p.Process(ctx, unionOver(req))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mu.Lock()
		streamed := arm == observe.ArmStreaming
		mu.Unlock()
		if res.Streamable != streamed {
			t.Errorf("%s: predict streamable %v, runtime arm %q", name, res.Streamable, arm)
		}
		if req.Crosstab != nil {
			continue // a crosstab carries no run components
		}
		if resp.Components == nil || resp.Components.Run == nil {
			t.Errorf("%s: no run components", name)
			continue
		}
		shards := 0
		for _, m := range res.Union {
			shards += m.Shards
		}
		if res.RecordCount != resp.Components.Run.TotalRecords || shards != resp.Components.Run.ShardCount {
			t.Errorf("%s: predict %d records / %d shards, runtime %d / %d", name, res.RecordCount, shards,
				resp.Components.Run.TotalRecords, resp.Components.Run.ShardCount)
		}
	}
}

// A grouped (0x02) member reads as its ungrouped twin, an anchored
// shard as the single file of its rows, and a default zone field the
// members share is the union's: each union answers exactly what the
// same union over plain single files does.
func TestUnion_GroupedAnchoredAndZonedMembers(t *testing.T) {
	t.Parallel()
	p, _ := memBuilderEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField

	// Grouped: store determines store_region / store_size.
	storeSchema := encoding.Schema{Fields: []encoding.Field{
		{Name: "store", Type: encoding.FieldTypeCategoricalU8, Description: "Store identifier code"},
		{Name: "store_region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Region the store sits in"},
		{Name: "store_size", Type: encoding.FieldTypeU32, Description: "Store floor area in square metres"},
		{Name: "sales", Type: encoding.FieldTypeF64, Nullable: true, Description: "Sales amount of the visit"},
	}}
	storeRows := make([]CohortRow, 12)
	for i := range storeRows {
		s := i % 3
		var sales any = float64(i) * 2.5
		if i%5 == 4 {
			sales = nil
		}
		storeRows[i] = CohortRow{[]string{"s1", "s2", "s3"}[s], []string{"west", "east", "west"}[s], uint64(100 + 10*s), sales}
	}
	grouped := buildCohort(t, p, "grouped.pulse", storeSchema, CohortBuilderOptions{RatioFloor: 1,
		Groups: []pio.GroupDecl{{Key: []string{"store"}, Members: []string{"store_region", "store_size"}}}}, storeRows)
	if grouped.FormatVersion != encoding.FormatVersionV2 {
		t.Fatalf("grouped fixture written as format %#x, want a grouped 0x02 cohort", grouped.FormatVersion)
	}
	buildCohort(t, p, "flat.pulse", storeSchema, CohortBuilderOptions{}, storeRows)
	buildCohort(t, p, "split.pulse", storeSchema, CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}}, storeRows)
	buildCohort(t, p, "first.pulse", storeSchema, CohortBuilderOptions{}, storeRows[:5])

	// Zoned: per-row zones in tz, the cohort default.
	buildCohort(t, p, "zoned_a.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(0, 12))
	buildCohort(t, p, "zoned_b.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(12, 6))

	members := func(pairs ...string) []types.UnionMember {
		var out []types.UnionMember
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, types.UnionMember{Cohort: &types.Cohort{Filename: pairs[i]}, Label: pairs[i+1]})
		}
		return out
	}
	storeReq := func(u []types.UnionMember) *Request {
		return &Request{Union: u,
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}, {Type: types.GROUP_CATEGORY, Field: "store_region"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "store", Label: "n"},
				{Type: types.AGG_SUM, Field: "sales", Label: "s"},
				{Type: types.AGG_MAX, Field: "store_size", Label: "big"},
			}}
	}
	run := func(req *Request) string {
		t.Helper()
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if len(resp.Data) == 0 {
			t.Fatal("no rows")
		}
		return comparableJSON(t, resp)
	}
	if got, want := run(storeReq(members("grouped.pulse", "a", "flat.pulse", "b"))), run(storeReq(members("flat.pulse", "a", "flat.pulse", "b"))); got != want {
		t.Errorf("grouped member:\n got %s\nwant %s", got, want)
	}
	if got, want := run(storeReq(members("split.pulse#part-00001.pulse", "a", "flat.pulse", "b"))), run(storeReq(members("first.pulse", "a", "flat.pulse", "b"))); got != want {
		t.Errorf("anchored member:\n got %s\nwant %s", got, want)
	}

	// The members' shared default zone buckets each union row by its
	// own row zone, exactly as the member alone does.
	hours := func(req *Request) string {
		req.Groups = append(req.Groups, &types.Group{Type: types.GROUP_DATE, Field: "at", Params: json.RawMessage(`{"component":"hour"}`)})
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
		resp, err := p.Process(ctx, req)
		if err != nil || len(resp.Data) == 0 {
			t.Fatalf("Process = %+v, %v", resp, err)
		}
		b, _ := json.Marshal(resp.Data)
		return string(b)
	}
	got := hours(&Request{Union: members("zoned_a.pulse", "a", "zoned_b.pulse", "b"),
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: src, Values: []string{"a"}}}})
	want := hours(&Request{Cohort: &types.Cohort{Filename: "zoned_a.pulse"}})
	if got != want {
		t.Errorf("zoned union hours:\n got %s\nwant %s", got, want)
	}
}
