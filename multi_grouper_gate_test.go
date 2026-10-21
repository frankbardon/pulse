package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/observe"
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

// mgFixture is the gate's engine over the 30-row cohort: f.p reads the
// single file; the parallel modes read the same rows as a shard archive
// (f.shard) or interleaved with filtered-out filler rows on disk
// (f.decoder, built on first use).
type mgFixture struct {
	p      *pulse.Pulse
	fs     afero.Fs
	cohort string
	opts   pulse.Options

	// shard reads archive (the 30 rows split 1 / 0 / 7 / 12 / 10)
	// with ShardWorkers 3.
	shard   *pulse.Pulse
	archive string

	// tempDir is the creating test's (outlives every subtest).
	tempDir      func() string
	decodeOnce   sync.Once
	decoder      *pulse.Pulse
	decodeCohort string

	// arm is the execution arm of the last operation a parallel-mode
	// engine ran (observe.OperationResult.Arm).
	armMu sync.Mutex
	arm   observe.Arm
}

// mgShardBounds splits mgRows into a 1-row shard, an EMPTY shard and
// three uneven ones.
var mgShardBounds = []int{0, 1, 1, 8, 20, 30}

func newMGFixture(t *testing.T, opts pulse.Options) *mgFixture {
	t.Helper()
	fsys := afero.NewMemMapFs()
	f := &mgFixture{fs: fsys, cohort: "mg.pulse", archive: "mg_archive.pulse", opts: opts, tempDir: t.TempDir}
	f.p = f.engine(t, fsys, 0, 0)
	mgWriteCohort(t, f.p, f.cohort, mgRows, 0)
	var shards []string
	for i := 0; i+1 < len(mgShardBounds); i++ {
		path := fmt.Sprintf("mg_s%d.pulse", i)
		mgWriteCohort(t, f.p, path, mgRows[mgShardBounds[i]:mgShardBounds[i+1]], 0)
		shards = append(shards, path)
	}
	if _, err := f.p.CreateShardArchive(context.Background(), f.archive, shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	f.shard = f.engine(t, fsys, 3, 1)
	return f
}

// engine is a pulse instance over fsys carrying the fixture's options,
// the given worker knobs and an arm-recording hook.
func (f *mgFixture) engine(t *testing.T, fsys afero.Fs, shardWorkers, decodeWorkers int) *pulse.Pulse {
	t.Helper()
	opts := f.opts
	opts.FS = fsys
	opts.ShardWorkers, opts.DecodeWorkers = shardWorkers, decodeWorkers
	opts.Hooks = &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
		f.armMu.Lock()
		f.arm = r.Arm
		f.armMu.Unlock()
	}}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// mgDecodeFiller is the filler record count: above the parallel-decode
// threshold (100,000 records), so DecodeWorkers engages.
const mgDecodeFiller = 102_400

// decodeEngine returns the DecodeWorkers=3 engine over an on-disk
// cohort holding mgRows interleaved with mgDecodeFiller filler rows
// (id >= 1,000,000 — mgDecodeFilter drops them), so every worker's
// segment carries real rows. Built once per fixture.
func (f *mgFixture) decodeEngine(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	f.decodeOnce.Do(func() {
		dir := f.tempDir()
		fsys := afero.NewBasePathFs(afero.NewOsFs(), dir)
		f.decoder = f.engine(t, fsys, 1, 3)
		f.decodeCohort = "mg_decode.pulse"
		mgWriteCohort(t, f.decoder, f.decodeCohort, mgRows, mgDecodeFiller)
	})
	if f.decoder == nil {
		t.Fatal("decode fixture failed to build")
	}
	return f.decoder, f.decodeCohort
}

// mgDecodeFilter keeps the 30 real rows (ids 1-30) and drops the filler.
func mgDecodeFilter() *types.Filterer {
	return &types.Filterer{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "1000"}}
}

// mgWriteCohort writes rows to path; filler > 0 interleaves that many
// filler rows, the real rows spread evenly across the file.
func mgWriteCohort(t *testing.T, p *pulse.Pulse, path string, rows []mgRow, filler int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, mgSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	day := func(id int) int32 {
		dayStr := "2024-01-15"
		if id%2 == 0 {
			dayStr = "2024-02-15"
		}
		d, err := encoding.ParseDate(dayStr)
		if err != nil {
			t.Fatal(err)
		}
		return int32(d)
	}
	appendRow := func(r mgRow) {
		var tier, tags any
		if r.tier != "" {
			tier = r.tier
		}
		if r.tags != nil {
			tags = r.tags
		}
		row := pulse.CohortRow{uint64(r.id), r.region, r.sex, tier, tags, float64(r.id), r.y, day(r.id)}
		if err := b.Append(row); err != nil {
			t.Fatalf("Append(%d): %v", r.id, err)
		}
	}
	total, every := len(rows)+filler, 1
	if len(rows) > 0 {
		every = total / len(rows)
	}
	next := 0
	for i := 0; i < total; i++ {
		if next < len(rows) && i == next*every {
			appendRow(rows[next])
			next++
			continue
		}
		appendRow(mgRow{id: 1_000_000 + i, region: "north", sex: "F", tags: []string{}})
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// lastArm is the arm of the last operation a fixture engine ran.
func (f *mgFixture) lastArm() observe.Arm {
	f.armMu.Lock()
	defer f.armMu.Unlock()
	return f.arm
}

// mgMergeable reports whether the parallel reducers admit req's
// groupers (the built-in mergeable set plus the gate's mergeable
// extension grouper), i.e. whether a parallel mode must fan out.
func mgMergeable(req *types.Request) bool {
	if len(req.Windows) > 0 || len(req.PostTests) > 0 || len(req.Tests) > 0 {
		return false // the merge gate excludes them, with one grouper as with several
	}
	for _, g := range req.Groups {
		if !g.Type.Mergeable() && g.Type != grpMrgFloor && g.Type != grpMGNul {
			return false
		}
	}
	return true
}

// mgRequireArm fails unless the last run took arm whenever req is
// mergeable — a parallel mode must never silently pass serially.
func mgRequireArm(t *testing.T, f *mgFixture, req *types.Request, arm observe.Arm) {
	t.Helper()
	if mgMergeable(req) && f.lastArm() != arm {
		t.Fatalf("mergeable request ran on arm %q, want %q", f.lastArm(), arm)
	}
}

// mgKeyColumn is the Data column g writes its key under (the
// vectors.GroupKeyField rule: Field, else "<TYPE>_<first fields entry>").
func mgKeyColumn(g *types.Group) string {
	if g.Field != "" {
		return g.Field
	}
	return string(g.Type) + "_" + g.Fields[0]
}

// mgRunChain runs req as stage 0 of a two-stage chain whose stage 1
// re-groups stage 0's rows by EVERY key column (GROUP_CATEGORY, the
// include list kept) and sums each aggregation column. Stage 1 must
// reproduce stage 0's rows key for key — the stage typing exposes
// every key column and a downstream stage can group on each — and
// stage 0's response is returned for the gate's checks.
func mgRunChain(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
	t.Helper()
	next := &types.Request{}
	var cols []string
	for _, g := range req.Groups {
		col := mgKeyColumn(g)
		cols = append(cols, col)
		ng := &types.Group{Type: types.GROUP_CATEGORY, Field: col}
		if g.Type == types.GROUP_CATEGORY {
			ng.Include = g.Include
		}
		next.Groups = append(next.Groups, ng)
	}
	var labels []string
	for _, a := range req.Aggregations {
		labels = append(labels, a.Label)
		next.Aggregations = append(next.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: a.Label, Label: a.Label})
	}
	stage0 := *req
	stage0.Cohort = nil
	out, err := f.p.ProcessChain(context.Background(), &types.ChainRequest{
		Cohort: req.Cohort,
		Stages: []*types.ChainStage{{Request: &stage0}, {Request: next}},
	})
	if err != nil {
		return nil, err
	}
	if len(out.Stages) != 2 {
		t.Fatalf("chain returned %d stages", len(out.Stages))
	}
	index := func(data []map[string]any) map[string]string {
		m := make(map[string]string, len(data))
		for i, row := range data {
			key := make([]string, 0, len(cols))
			for _, c := range cols {
				v, ok := row[c]
				if !ok {
					t.Fatalf("chain row %d carries no key column %q: %v", i, c, row)
				}
				key = append(key, fmt.Sprint(v))
			}
			val := make([]string, 0, len(labels))
			for _, l := range labels {
				val = append(val, fmt.Sprint(row[l]))
			}
			m[strings.Join(key, "|")] = strings.Join(val, "|")
		}
		return m
	}
	if got, want := index(out.Stages[1].Data), index(out.Stages[0].Data); !reflect.DeepEqual(got, want) {
		t.Fatalf("stage 1 re-grouped on every key column differs from stage 0:\n got  %v\n want %v", got, want)
	}
	return out.Stages[0], nil
}

// mgMode is one execution mode of the gate. run returns the response
// for req (a fresh request per call; the mode may add slots).
type mgMode struct {
	name string
	run  func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error)
	// mergeOnly marks a mode that admits only mergeable groupers: a
	// ProcessChain stage must merge (PULSE_CHAIN_NOT_MERGEABLE), with
	// one grouper as with several.
	mergeOnly bool
}

// admits reports whether the mode can run groups at all.
func (m mgMode) admits(groups []*types.Group) bool {
	return m.admitsRequest(&types.Request{Groups: groups})
}

// admitsRequest is admits over a whole request: a chain stage also
// refuses windows and post-tests (PULSE_CHAIN_NOT_MERGEABLE), with one
// grouper as with several.
func (m mgMode) admitsRequest(req *types.Request) bool {
	return !m.mergeOnly || mgMergeable(req)
}

// mgModes: buffered Process (a non-streamable AGG_MEDIAN slot forces
// the buffered arm), streaming Process and ProcessStream (every slot
// streamable; a request whose groupers all stream must ride the
// streaming arm — a non-streamable grouper, GROUP_DATE / GROUP_QUANTILE
// / GROUP_KMEANS, routes buffered by design); the shard archive
// (ShardWorkers 3 over an uneven split with a 1-row and an empty shard)
// and parallel decode (DecodeWorkers 3), each required to take its
// parallel arm whenever the groupers merge; Compose, serial and
// parallel; and stage 0 of a ProcessChain whose stage 1 re-groups on
// every key column. Then the consumers over composite rows: an
// explicit `sort` on every key column (descending) on the shard
// archive, a `sort` on the aggregate (descending, key columns breaking
// ties) on the plain engine, and a `return` include allowlist of the
// key and aggregate columns (a dropped AGG_MEDIAN slot must be pruned
// from every row). A sort mode's expected rows are the hand-computed
// rows re-ordered by its sort (mgMode.want).
var mgModes = []mgMode{
	{"Buffered", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score", Label: "med"})
		return f.p.Process(context.Background(), req)
	}, false},
	{"Streaming", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		mgRequireStreamable(t, f, req)
		return f.p.Process(context.Background(), req)
	}, false},
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
	}, false},
	{"ShardArchive", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		req.Cohort = &types.Cohort{Filename: f.archive}
		resp, err := f.shard.Process(context.Background(), req)
		if err == nil {
			mgRequireArm(t, f, req, observe.ArmShardParallel)
		}
		return resp, err
	}, false},
	{"ParallelDecode", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		p, cohort := f.decodeEngine(t)
		req.Cohort = &types.Cohort{Filename: cohort}
		req.Filterers = append(req.Filterers, mgDecodeFilter())
		resp, err := p.Process(context.Background(), req)
		if err == nil {
			mgRequireArm(t, f, req, observe.ArmParallelDecode)
		}
		return resp, err
	}, false},
	{"Compose", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		out, err := f.p.Compose(context.Background(), &types.ComposedRequest{Requests: []*types.Request{req}})
		if err != nil {
			return nil, err
		}
		return out.Responses[0], nil
	}, false},
	{"ComposeParallel", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		var twin types.Request
		if b, err := json.Marshal(req); err != nil || json.Unmarshal(b, &twin) != nil {
			t.Fatalf("clone request: %v", err)
		}
		out, err := f.p.ComposeParallel(context.Background(), &types.ComposedRequest{Requests: []*types.Request{req, &twin}},
			pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(out.Responses[0].Data, out.Responses[1].Data) {
			t.Fatalf("parallel Compose slots disagree:\n %v\n %v", out.Responses[0].Data, out.Responses[1].Data)
		}
		return out.Responses[0], nil
	}, false},
	{"ProcessChain", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		return mgRunChain(t, f, req)
	}, true},
	{"SortKeys", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		for _, g := range req.Groups {
			req.Sort = append(req.Sort, types.OrderKey{Field: mgKeyColumn(g), Desc: true})
		}
		req.Cohort = &types.Cohort{Filename: f.archive}
		resp, err := f.shard.Process(context.Background(), req)
		if err == nil {
			mgRequireArm(t, f, req, observe.ArmShardParallel)
		}
		return resp, err
	}, false},
	{"SortAggregate", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		req.Sort = []types.OrderKey{{Field: req.Aggregations[0].Label, Desc: true}}
		for _, g := range req.Groups {
			req.Sort = append(req.Sort, types.OrderKey{Field: mgKeyColumn(g)})
		}
		return f.p.Process(context.Background(), req)
	}, false},
	{"Return", func(t *testing.T, f *mgFixture, req *types.Request) (*types.Response, error) {
		keep := map[string]bool{}
		ret := &types.Return{Include: []string{"components", "matrices", "post_tests", "overlays"}}
		col := func(c string) {
			keep[c] = true
			ret.Include = append(ret.Include, "data[*]."+c)
		}
		for _, g := range req.Groups {
			col(mgKeyColumn(g))
		}
		for _, a := range req.Aggregations {
			col(a.Label)
		}
		for _, w := range req.Windows {
			col(w.Label)
		}
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score", Label: "med"})
		req.Return = ret
		resp, err := f.p.Process(context.Background(), req)
		if err != nil {
			return nil, err
		}
		for i, row := range resp.Data {
			for c := range row {
				if !keep[c] {
					t.Fatalf("Data[%d] carries column %q outside the return allowlist: %v", i, c, row)
				}
			}
		}
		return resp, nil
	}, false},
}

// mgReorder re-orders a mode's expected rows ("k1|...|kN|n") the way
// its sort does; a mode absent here keeps the native order.
var mgReorder = map[string]func(rows []string, nKeys int) []string{
	"SortKeys": func(rows []string, nKeys int) []string {
		return mgSorted(rows, func(a, b []string) int {
			for k := 0; k < nKeys; k++ {
				if c := strings.Compare(b[k], a[k]); c != 0 {
					return c
				}
			}
			return 0
		})
	},
	"SortAggregate": func(rows []string, nKeys int) []string {
		return mgSorted(rows, func(a, b []string) int {
			na, _ := strconv.ParseFloat(a[nKeys], 64)
			nb, _ := strconv.ParseFloat(b[nKeys], 64)
			switch {
			case na > nb:
				return -1
			case na < nb:
				return 1
			}
			for k := 0; k < nKeys; k++ {
				if c := strings.Compare(a[k], b[k]); c != 0 {
					return c
				}
			}
			return 0
		})
	},
}

// mgSorted stable-sorts "|"-joined rows by cmp over their parts.
func mgSorted(rows []string, cmp func(a, b []string) int) []string {
	out := append([]string(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		return cmp(strings.Split(out[i], "|"), strings.Split(out[j], "|")) < 0
	})
	return out
}

// want is the mode's expected rendering of the hand-computed rows.
func (m mgMode) want(rows []string, nKeys int) []string {
	if re := mgReorder[m.name]; re != nil {
		return re(rows, nKeys)
	}
	return rows
}

// mgRequireStreamable fails unless predict routes req to the streaming
// arm whenever every grouper is streamable (predict's Streamable
// mirrors the runtime route: TestPredict_Streamable_MatchesRuntime), so
// a streaming mode cannot silently pass on the buffered arm.
func mgRequireStreamable(t *testing.T, f *mgFixture, req *types.Request) {
	t.Helper()
	if len(req.Windows) > 0 || len(req.PostTests) > 0 {
		return // windows and post-tests run over the post-aggregate rows: buffered by design
	}
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
				if !mode.admits(c.groups) {
					t.Skip("mode admits mergeable groupers only")
				}
				resp, err := mode.run(t, f, mgRequest(f, c.groups))
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if got, want := mgRender(t, resp.Data, c.cols, "n"), mode.want(c.want, len(c.cols)); !reflect.DeepEqual(got, want) {
					t.Fatalf("rows\n got  %q\n want %q", got, want)
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
				if !mode.admits([]*types.Group{c.g()}) {
					t.Skip("mode admits mergeable groupers only")
				}
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
			if got, want := mgRender(t, resp.Data, []string{"tier", "sex"}, "n"), mode.want([]string{"gold|F|4", "gold|M|5", "silver|F|7", "silver|M|7"}, 2); !reflect.DeepEqual(got, want) {
				t.Fatalf("rows %q, want %q", got, want)
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
			if !mode.admits(c.groups) {
				continue
			}
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
		// Mergeable (it carries no components state), so the shard and
		// decode modes key it on their parallel arms.
		Mergeable: true,
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
			if len(resp.Data) != 2 || (resp.Data[0]["sex"] != "F\x00x" && resp.Data[1]["sex"] != "F\x00x") {
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

// mgBitsRow is record r of the bit-exactness cohorts: mgRows[r%30]'s
// keys with a fractional score (still inside the row's GROUP_RANGE
// bucket; also the weight) and a noisy y, so a reassociated Welford or
// co-moment merge moves bits.
func mgBitsRow(r int) mgRow {
	row := mgRows[r%len(mgRows)]
	z := uint64(r) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	u := float64(z>>11) / float64(1<<53)
	row.y += u*1000/3 - 100
	row.id = r + 1
	return row
}

// mgWriteBitsCohort writes records [from, to) of the bit cohort.
func mgWriteBitsCohort(t *testing.T, p *pulse.Pulse, path string, from, to int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, mgSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for r := from; r < to; r++ {
		row := mgBitsRow(r)
		base := mgRows[r%len(mgRows)]
		var tier, tags any
		if row.tier != "" {
			tier = row.tier
		}
		if row.tags != nil {
			tags = row.tags
		}
		score := float64(base.id) + float64(r%97)/97
		day := int32(19737 + base.id%2*31)
		if err := b.Append(pulse.CohortRow{uint64(row.id), row.region, row.sex, tier, tags, score, row.y, day}); err != nil {
			t.Fatalf("Append(%d): %v", r, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// mgBitsRequest is c's groups over a count and the merge-block slots
// (vector sum / mean, covariance) — every float slot weighted by score
// when weighted. Those merge through processing.BlockMerger's fixed
// tree, so their bits cannot depend on the partition; a scalar
// AGG_SUM / AGG_AVERAGE merges binned reproducible state through
// MergeOnline and is bit-identical too (TestBitExact_*), while
// AGG_STDDEV's Chan-Welford merge is ULP-bounded across worker counts
// by design, with one grouper as with several
// (.claude/reference/execution-modes/shards-and-merge.md, Blocked merge).
func mgBitsRequest(cohort string, groups []*types.Group, weighted bool) *types.Request {
	var w types.SlotWeight
	if weighted {
		w = types.SlotWeightField("score")
	}
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Groups: groups,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
			{Type: types.AGG_VEC_SUM, Fields: []string{"score", "y"}, Label: "s", Weight: w},
			{Type: types.AGG_VEC_MEAN, Fields: []string{"score", "y"}, Label: "m", Weight: w},
		},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"score", "y"}, Weight: w}},
	}
}

// mgBits is the part of a response every worker count must reproduce
// bit for bit: rows, per-slot and per-group Components, per-grouper
// Components and the grouped matrices (Run.shard_count and the cohort
// path legitimately differ between a file and its archive). The
// aggregation floor's weighted keys (sum_weights, n_eff) ride the
// bitwise comparison too: Σw / Σw² are binned reproducible sums
// (processing.WeightFloor, merged by processing.MergeSlotFloors) — with
// one grouper as with several.
type mgBits struct {
	Data     []map[string]any
	Aggs     []types.AggregationComponents
	Groupers []types.GrouperComponents
	Matrices []types.MatrixResult
	MatComps []types.MatrixComponents
	Arm      observe.Arm
}

func mgRunBits(t *testing.T, p *pulse.Pulse, f *mgFixture, req *types.Request) mgBits {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	out := mgBits{Data: resp.Data, Matrices: resp.Matrices, Arm: f.lastArm()}
	if c := resp.Components; c != nil {
		out.Aggs, out.Groupers, out.MatComps = c.Aggregations, c.Groupers, c.Matrices
	}
	if len(out.Data) == 0 || len(out.Groupers) != len(req.Groups) {
		t.Fatalf("degenerate run: %d rows, %d grouper entries", len(out.Data), len(out.Groupers))
	}
	return out
}

func mgSameBits(t *testing.T, label string, got, want mgBits) {
	t.Helper()
	for _, part := range []struct {
		name      string
		got, want any
	}{
		{"Data", got.Data, want.Data},
		{"Components.Aggregations", got.Aggs, want.Aggs},
		{"Components.Groupers", got.Groupers, want.Groupers},
		{"Matrices", got.Matrices, want.Matrices},
		{"Components.Matrices", got.MatComps, want.MatComps},
	} {
		if !reflect.DeepEqual(part.got, part.want) {
			t.Errorf("%s: %s differ from the serial single-file run\n got  %v\n want %v", label, part.name, part.got, part.want)
		}
	}
}

// TestMultiGrouper_BitExactAcrossWorkers: every mergeable #422 case
// answers the SAME BITS — rows, Components (per-slot, per-group,
// per-grouper) and grouped matrices — serially over a single file,
// under every DecodeWorkers count over that file, and over a shard
// archive of the same rows split unevenly (1-row shards, an empty
// shard, edges inside merge blocks) under every ShardWorkers ×
// DecodeWorkers count; weighted and unweighted. Each parallel run must
// take its parallel arm, so no knob compares serial against serial.
func TestMultiGrouper_BitExactAcrossWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record multi-grouper worker-invariance gate in -short mode")
	}
	const big = mgDecodeFiller + 1234
	f := newMGFixture(t, pulse.Options{})
	dir := t.TempDir()
	osFs := afero.NewBasePathFs(afero.NewOsFs(), dir)
	serial := f.engine(t, osFs, 1, 1)
	mgWriteBitsCohort(t, serial, "big.pulse", 0, big)
	// The archive's twin: a smaller file (shard fan-out has no record
	// threshold) and the same rows as uneven shards.
	const small = 3*4096 + 777
	mgWriteBitsCohort(t, serial, "small.pulse", 0, small)
	bounds := []int{0, 1, 2, 2, 4097, 4098, 9000, small}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("bits_s%d.pulse", i)
		mgWriteBitsCohort(t, serial, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	if _, err := serial.CreateShardArchive(context.Background(), "bits_archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	decoders := map[int]*pulse.Pulse{}
	for _, dw := range []int{2, 3, 7} {
		decoders[dw] = f.engine(t, osFs, 1, dw)
	}
	sharders := map[[2]int]*pulse.Pulse{}
	for _, sw := range []int{1, 2, 3, 4} {
		for _, dw := range []int{1, 2, 3, 7} {
			sharders[[2]int{sw, dw}] = f.engine(t, osFs, sw, dw)
		}
	}
	for _, c := range mgCases() {
		if !mgMergeable(&types.Request{Groups: c.groups}) {
			continue
		}
		for _, weighted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/weighted=%v", c.name, weighted), func(t *testing.T) {
				want := mgRunBits(t, serial, f, mgBitsRequest("big.pulse", c.groups, weighted))
				if hasW := len(want.Aggs) > 0 && want.Aggs[1].SumWeights != nil; weighted != hasW {
					t.Fatalf("weighted=%v run carries weighted floor keys: %v", weighted, hasW)
				}
				for _, dw := range []int{2, 3, 7} {
					got := mgRunBits(t, decoders[dw], f, mgBitsRequest("big.pulse", c.groups, weighted))
					if got.Arm != observe.ArmParallelDecode {
						t.Fatalf("DecodeWorkers=%d ran on arm %q", dw, got.Arm)
					}
					mgSameBits(t, fmt.Sprintf("DecodeWorkers=%d", dw), got, want)
				}
				twin := mgRunBits(t, serial, f, mgBitsRequest("small.pulse", c.groups, weighted))
				for _, sw := range []int{1, 2, 3, 4} {
					for _, dw := range []int{1, 2, 3, 7} {
						got := mgRunBits(t, sharders[[2]int{sw, dw}], f, mgBitsRequest("bits_archive.pulse", c.groups, weighted))
						if sw > 1 && got.Arm != observe.ArmShardParallel {
							t.Fatalf("ShardWorkers=%d ran on arm %q", sw, got.Arm)
						}
						mgSameBits(t, fmt.Sprintf("archive ShardWorkers=%d DecodeWorkers=%d", sw, dw), got, twin)
					}
				}
			})
		}
	}
}

// TestMultiGrouper_ChainFiltersOnInnerKeyColumn: a downstream chain
// stage filters on stage 0's INNER key column and groups on its outer
// one — every key column is a typed stage-1 input, not only the first.
func TestMultiGrouper_ChainFiltersOnInnerKeyColumn(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	out, err := f.p.ProcessChain(context.Background(), &types.ChainRequest{
		Cohort: &types.Cohort{Filename: f.cohort},
		Stages: []*types.ChainStage{
			{Request: mgRequest(f, []*types.Group{mgRegion(), mgSex()})},
			{Request: &types.Request{
				Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "sex", Values: []string{"M"}}},
				Groups:       []*types.Group{mgRegion()},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "n"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ProcessChain: %v", err)
	}
	if got, want := mgRender(t, out.Final.Data, []string{"region"}, "n"), []string{"north|5", "south|6", "west|5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stage 1 rows %q, want %q", got, want)
	}
}

// TestMultiGrouper_WindowsAndPostTestsOverCompositeRows: windows and
// post-tests run over the composite rows in every mode. A WIN_LAG
// partitioned by the OUTER key column and ordered by the inner one
// lags within each region (F has no prior; M's prior is its region's F
// count), and a TEST_PEARSON_R post-test with partition_by [] over the
// six (region, sex) rows correlates their counts with their id sums — both hand-computed
// from mgRows. (ProcessStream's row iterator carries no post-tests.)
func TestMultiGrouper_WindowsAndPostTestsOverCompositeRows(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	// (region, sex) → the count's lag within region, ordered by sex.
	wantPrev := map[string]any{
		"north|F": nil, "north|M": 7.0,
		"south|F": nil, "south|M": 4.0,
		"west|F": nil, "west|M": 3.0,
	}
	// n = 7 5 4 6 3 5, s = Σid = 28 50 58 117 72 140 (Data order).
	const wantR, wantCov = -0.09622133710751231, -5.8
	for _, mode := range mgModes {
		t.Run(mode.name, func(t *testing.T) {
			req := mgRequest(f, []*types.Group{mgRegion(), mgSex()},
				&types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"},
				&types.Aggregation{Type: types.AGG_SUM, Field: "id", Label: "s"})
			req.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "n", Label: "prev",
				PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "sex"}}}}
			// partition_by [] runs the post-test once over every composite
			// row (absent, it would run once per region — U46 E3-S2).
			req.PostTests = []*types.Test{{Type: types.TEST_PEARSON_R, Field: "n", Field2: "s", Label: "r",
				Params: json.RawMessage(`{"partition_by": []}`)}}
			if !mode.admitsRequest(req) {
				t.Skip("mode admits mergeable stages only")
			}
			resp, err := mode.run(t, f, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(resp.Data) != len(wantPrev) {
				t.Fatalf("%d rows, want %d: %v", len(resp.Data), len(wantPrev), resp.Data)
			}
			for _, row := range resp.Data {
				key := fmt.Sprint(row["region"]) + "|" + fmt.Sprint(row["sex"])
				want, ok := wantPrev[key]
				if !ok {
					t.Fatalf("unexpected row %v", row)
				}
				got, has := row["prev"]
				if !has {
					t.Fatalf("row %s carries no window column: %v", key, row)
				}
				if want == nil {
					if got != nil {
						t.Errorf("prev(%s) = %v, want null (first in its region)", key, got)
					}
				} else if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("prev(%s) = %v, want %v", key, got, want)
				}
			}
			if mode.name == "ProcessStream" {
				return
			}
			if len(resp.PostTests) != 1 || resp.PostTests[0] == nil {
				t.Fatalf("post-tests %+v, want one result", resp.PostTests)
			}
			r := resp.PostTests[0]
			if math.Abs(r.Statistic-wantR) > 1e-12 {
				t.Errorf("pearson r = %v, want %v", r.Statistic, wantR)
			}
			if n := r.Details["n"]; fmt.Sprint(n) != "6" {
				t.Errorf("post-test n = %v, want the 6 composite rows", n)
			}
			if cov, _ := r.Details["covariance"].(float64); math.Abs(cov-wantCov) > 1e-12 {
				t.Errorf("covariance = %v, want %v", r.Details["covariance"], wantCov)
			}
		})
	}
}

// TestMultiGrouper_ReturnKeyColumns: `return` include / exclude name
// each grouper's key column. Excluding the inner key column prunes it
// from every row and nothing else (the six combinations stay distinct
// rows and Components keep the full tuple); naming a column no grouper
// writes is refused PULSE_RETURN_PATH_UNKNOWN by runtime and predict
// alike, while the inner key column is accepted.
func TestMultiGrouper_ReturnKeyColumns(t *testing.T) {
	f := newMGFixture(t, pulse.Options{})
	ctx := context.Background()

	req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
	req.Return = &types.Return{Exclude: []string{"data[*].sex"}}
	resp, err := f.p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got, want := mgRender(t, resp.Data, []string{"region"}, "n"), []string{"north|7", "north|5", "south|4", "south|6", "west|3", "west|5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	for i, row := range resp.Data {
		if _, ok := row["sex"]; ok {
			t.Errorf("Data[%d] still carries the excluded key column: %v", i, row)
		}
	}
	groups := resp.Components.Aggregations[0].Groups
	for i, sex := range []string{"F", "M", "F", "M", "F", "M"} {
		if want := (types.AxisKey{resp.Data[i]["region"], sex}); !reflect.DeepEqual(groups[i].GroupKey, want) {
			t.Errorf("groups[%d].group_key = %v, want %v", i, groups[i].GroupKey, want)
		}
	}

	for _, c := range []struct {
		col     string
		refused bool
	}{{"sex", false}, {"tier", true}} {
		req := mgRequest(f, []*types.Group{mgRegion(), mgSex()})
		req.Return = &types.Return{Include: []string{"data[*].region", "data[*]." + c.col, "data[*].n"}}
		_, err := f.p.Process(ctx, req)
		env := predictEnvelope(t, f.p, f.fs, f.cohort, req)
		if !c.refused {
			if err != nil || len(env.Errors) > 0 {
				t.Errorf("include %s: runtime %v, predict %+v; want accepted", c.col, err, env.Errors)
			}
			continue
		}
		requireCodedError(t, err, perr.PULSE_RETURN_PATH_UNKNOWN)
		if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_RETURN_PATH_UNKNOWN) {
			t.Errorf("include %s: predict errors %+v, want PULSE_RETURN_PATH_UNKNOWN", c.col, env.Errors)
		}
	}
}
