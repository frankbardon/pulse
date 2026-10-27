package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_matrix_invariance_test.go: the worker-invariance family
// for extension MAT_* operators (U30 E1-S4, roadmap #156) on the shared
// harness (internal/testutil/invariance) — the public-registration twin
// of the service matrix family (TestMatrixInvariance_*). An operator
// registered through Options.Extensions.MatrixOps only (no engine hook)
// renders every raw figure of the merged co-moment it is handed; the
// family proves the whole response (LayoutFree) carries the same BITS
// serially, under every ShardWorkers count over a shard archive of
// uneven shards (a 1-row shard, an empty shard, edges inside merge
// blocks) against its single-file twin, and (heavy tier) under every
// DecodeWorkers count over a >100K-record file — listwise and pairwise
// in one request, weighted and unweighted, grouped and ungrouped. Every
// parallel cell must take its parallel arm (the harness), so no knob
// compares serial against serial. Two serial cells force the other
// paths: extInvBufferedPath puts the co-moment tier on the buffered
// path (an AGG_MEDIAN slot, stripped again) and invariance.Buffered
// registers the buffered tier (Streamable: false), which runs serially
// under any knob and lands on the co-moment tier's bits (its
// row_digest scalar, an order-sensitive digest of the rows it saw,
// checked present and stripped).

// extInvRows is above the parallel-decode threshold (100,000 records)
// and ends mid-block, as the service matrix family's BigFile cohort.
const extInvRows = 25*linalg.MergeBlockSize + 2777

// extInvTwinRows is the archive's single-file twin: shard fan-out has
// no record threshold.
const extInvTwinRows = 3*linalg.MergeBlockSize + 777

// extInvShardBounds splits the twin's rows into a 1-row shard, an
// EMPTY shard and uneven shards whose edges fall inside merge blocks.
var extInvShardBounds = []int{0, 1, 2, 2, 4097, 4098, 9000, extInvTwinRows}

func extInvSchema() encoding.Schema {
	region := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east"} {
		_, _ = region.Add(v)
	}
	fields := []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "c", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region},
		{Name: "w", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Extension matrix invariance gate column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// extInvValues is row r's members (NaN = null) and weight: irrational
// spreads so no float sum is exact and a different fold order shows in
// the low bits.
func extInvValues(r int) (x [3]float64, w float64) {
	f := float64(r)
	x[0] = 10*math.Sin(f*0.37) + float64(r%11)*0.1
	x[1] = 3*math.Cos(f*0.11) + 0.25*x[0] + float64(r%5)/3
	x[2] = math.Sqrt(f+1)/7 - float64((r*13)%17)/9
	if r%29 == 0 {
		x[0] = math.NaN()
	}
	if r%37 == 5 {
		x[1] = math.NaN()
	}
	if r%31 == 3 {
		x[2] = math.NaN()
	}
	return x, 0.5 + float64(r%7)/3 + math.Abs(math.Sin(f))/5
}

func extInvWrite(t *testing.T, p *Pulse, path string, from, to int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, extInvSchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	regions := []string{"north", "south", "east"}
	for r := from; r < to; r++ {
		x, w := extInvValues(r)
		row := CohortRow{nil, nil, nil, regions[(r/3)%3], w}
		for i, v := range x {
			if !math.IsNaN(v) {
				row[i] = v
			}
		}
		if err := b.Append(row); err != nil {
			t.Fatalf("Append(%d): %v", r, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// extMomentsFinalize renders every raw figure of the merged co-moment:
// the per-pair co-moment (primary), pair counts and weight sums
// (auxiliary), the means (vectors), N / W / NEff (scalars) and, on the
// buffered tier, an order-sensitive digest of the admitted rows.
func extMomentsFinalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
	cm := in.CoMoment
	out := extend.MatrixOutput{Primary: in.Square(cm.PairM2)}
	trace := 0.0
	for i := 0; i < cm.P(); i++ {
		trace += cm.PairM2(i, i)
	}
	out.Components = map[string]any{"trace_m2": trace}
	if in.WantAuxiliary {
		out.Auxiliary = map[string]*types.MatrixValues{
			"pair_n": in.Square(func(i, j int) float64 { return float64(cm.PairN(i, j)) }),
			"pair_w": in.Square(cm.PairW),
		}
	}
	if in.WantVectors {
		out.Vectors = map[string]any{"mean": cm.Mean().Slice()}
	}
	if in.WantScalars {
		out.Scalars = map[string]float64{"n": float64(cm.N()), "w": cm.W(), "n_eff": cm.NEff()}
		if in.Rows != nil {
			h := 0.0
			for r := 0; r < in.Rows.Len(); r++ {
				for _, v := range in.Rows.Row(r) {
					if !math.IsNaN(v) {
						h = h*1.0000001 + v*in.Rows.Weight(r)
					}
				}
			}
			out.Scalars["row_digest"] = h
		}
	}
	return out, nil
}

// extMomentsReg registers MAT_ACME_MOMENTS on the given tier.
func extMomentsReg(streamable bool) MatrixOpRegistration {
	return MatrixOpRegistration{
		Name:        "MAT_ACME_MOMENTS",
		Description: "Raw co-moment figures (worker-invariance gate operator).",
		Factory: func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
			return probeFuncMatrixOp(extMomentsFinalize), nil
		},
		Streamable:  streamable,
		WeightAware: true,
		OutputKeys: descriptor.MatrixOutputKeys{
			Primary:   "pair_m2",
			Auxiliary: []string{"pair_n", "pair_w"},
			Vectors:   []string{"mean"},
			Scalars:   []string{"n", "w", "n_eff", "row_digest"},
		},
		ComponentSchema: descriptor.ComponentSchema{
			Keys:         []descriptor.ComponentKey{{Name: "trace_m2", Type: "float64", Description: "Sum of the per-member co-moments."}},
			Mergeability: descriptor.Mergeable,
		},
	}
}

// extInvBufferedPath forces the co-moment tier onto the buffered path.
const extInvBufferedPath invariance.Mode = "buffered_path"

// extInvCohort is one layout's cohort.
type extInvCohort struct {
	fs   afero.Fs
	path string
}

// newExtInvFixture writes the twin and the archive on a MemMapFs; heavy
// adds the decode-threshold file on a real directory (parallel decode
// needs mmap).
func newExtInvFixture(t *testing.T, heavy bool) map[invariance.Layout]extInvCohort {
	t.Helper()
	mem := afero.NewMemMapFs()
	w, err := New(Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	extInvWrite(t, w, "twin.pulse", 0, extInvTwinRows)
	var shards []string
	for i := 0; i+1 < len(extInvShardBounds); i++ {
		path := fmt.Sprintf("inv_s%d.pulse", i)
		extInvWrite(t, w, path, extInvShardBounds[i], extInvShardBounds[i+1])
		shards = append(shards, path)
	}
	if _, err := w.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]extInvCohort{invariance.File: {mem, "twin.pulse"}, invariance.Archive: {mem, "archive.pulse"}}
	if heavy {
		disk := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
		dw, err := New(Options{FS: disk})
		if err != nil {
			t.Fatal(err)
		}
		extInvWrite(t, dw, "big.pulse", 0, extInvRows)
		out[invariance.BigFile] = extInvCohort{disk, "big.pulse"}
	}
	return out
}

// extInvEngine is a Pulse over c under k's knobs with the operator on
// the tier k.Mode selects, and the arm of its last run.
func extInvEngine(t *testing.T, c extInvCohort, k invariance.Knobs) (*Pulse, func() observe.Arm) {
	t.Helper()
	var mu sync.Mutex
	var arm observe.Arm
	p, err := New(Options{
		FS:            c.fs,
		ShardWorkers:  k.ShardWorkers,
		DecodeWorkers: k.DecodeWorkers,
		Extensions:    Extensions{MatrixOps: []MatrixOpRegistration{extMomentsReg(k.Mode != invariance.Buffered)}},
		Hooks: &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
			mu.Lock()
			arm = r.Arm
			mu.Unlock()
		}},
	})
	if err != nil {
		t.Fatalf("New(%s): %v", k, err)
	}
	return p, func() observe.Arm {
		mu.Lock()
		defer mu.Unlock()
		return arm
	}
}

type extInvVariant struct {
	weighted, grouped bool
}

func (v extInvVariant) String() string {
	return fmt.Sprintf("weighted=%v_grouped=%v", v.weighted, v.grouped)
}

func extInvVariants() []extInvVariant {
	return []extInvVariant{{false, false}, {true, false}, {false, true}, {true, true}}
}

// extInvRequest is variant v: the operator listwise and pairwise.
func extInvRequest(cohort string, v extInvVariant) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort}}
	for _, missing := range []string{"listwise", "pairwise"} {
		req.Matrices = append(req.Matrices, types.MatrixSpec{Type: "MAT_ACME_MOMENTS", Name: missing, Fields: []string{"a", "b", "c"},
			Params: json.RawMessage(`{"missing":"` + missing + `"}`)})
	}
	if v.grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}
	}
	if v.weighted {
		req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	}
	return req
}

// extInvStrip removes what only one path adds: the buffered path's
// AGG_MEDIAN column (a row it alone filled, its Components entry), the
// buffered tier's
// row_digest (failing when that tier saw no rows). The median reads
// the never-null w, so Run.NullRecords keeps the slot-free figure.
func extInvStrip(t *testing.T, k invariance.Knobs, resp *types.Response) *types.Response {
	t.Helper()
	out := invariance.LayoutFree(resp)
	if k.Mode == extInvBufferedPath {
		var rows []map[string]any
		for _, r := range out.Data {
			if _, ok := r["med"]; !ok {
				t.Fatalf("%s: buffered-path row lacks med: %v", k, r)
			}
			kept := map[string]any{}
			for key, v := range r {
				if key != "med" {
					kept[key] = v
				}
			}
			if len(kept) > 0 {
				rows = append(rows, kept)
			}
		}
		out.Data = rows
		if out.Components != nil {
			c := *out.Components
			var aggs []types.AggregationComponents
			for _, a := range c.Aggregations {
				if a.Label != "med" {
					aggs = append(aggs, a)
				}
			}
			c.Aggregations = aggs
			out.Components = &c
		}
	}
	if k.Mode == invariance.Buffered {
		ms := make([]types.MatrixResult, len(out.Matrices))
		for i, m := range out.Matrices {
			if _, ok := m.Scalars["row_digest"]; !ok {
				t.Fatalf("%s: buffered tier result %d: Finalize saw no rows", k, i)
			}
			sc := map[string]float64{}
			for key, v := range m.Scalars {
				if key != "row_digest" {
					sc[key] = v
				}
			}
			m.Scalars = sc
			ms[i] = m
		}
		out.Matrices = ms
	}
	return out
}

// extInvFamily is variant v over groups.
func extInvFamily(cohorts map[invariance.Layout]extInvCohort, v extInvVariant, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   v.String(),
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := extInvEngine(t, c, k)
			req := extInvRequest(c.path, v)
			if k.Mode == extInvBufferedPath {
				req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "w", Label: "med"})
			}
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			return invariance.Result[*types.Response]{Answer: extInvStrip(t, k, resp), Arm: arm()}
		},
		// The fixture is not degenerate: three buckets when grouped,
		// nulls thin the listwise N, weights move W.
		Check: func(t *testing.T, k invariance.Knobs, resp *types.Response) {
			if n := len(resp.Matrices); (v.grouped && n != 6) || (!v.grouped && n != 2) {
				t.Fatalf("%d matrix results", n)
			}
			s := resp.Matrices[0].Scalars
			if !v.grouped && s["n"] >= float64(cohortRowsOf(k)) {
				t.Fatalf("listwise n = %v — the nulls did not thin it", s["n"])
			}
			if v.weighted == (s["w"] == s["n"]) {
				t.Fatalf("weighted=%v but w = %v, n = %v", v.weighted, s["w"], s["n"])
			}
		},
	}
}

// cohortRowsOf is the record count of k's layout.
func cohortRowsOf(k invariance.Knobs) int {
	if k.Layout == invariance.BigFile {
		return extInvRows
	}
	return extInvTwinRows
}

// extInvSerialCell is a serial cell that names its arm.
func extInvSerialCell(l invariance.Layout, dw, sw int, m invariance.Mode, arm observe.Arm) invariance.Cell {
	return invariance.Cell{Knobs: invariance.Knobs{Layout: l, DecodeWorkers: dw, ShardWorkers: sw, Mode: m}, Arm: arm}
}

// extInvArchiveGroup is the archive group (plus ShardWorkers 8 ×
// DecodeWorkers 7) with the two forced serial paths, on the twin and on
// the archive under the widest knobs.
func extInvArchiveGroup() invariance.Group {
	g := invariance.ArchiveGroup()
	g.Cells = append(g.Cells,
		invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.Archive, DecodeWorkers: 7, ShardWorkers: 8}, Arm: observe.ArmShardParallel},
		extInvSerialCell(invariance.File, 1, 1, extInvBufferedPath, observe.ArmBuffered),
		extInvSerialCell(invariance.File, 1, 1, invariance.Buffered, observe.ArmBuffered),
		extInvSerialCell(invariance.Archive, 8, 8, invariance.Buffered, observe.ArmBuffered))
	return g
}

// TestExtensionMatrixInvariance_Archive is the fast-tier half of the
// extension matrix family; TestExtensionMatrixInvariance_Heavy adds the
// decode group. It also holds predict and the merge gate to the
// runtime: the co-moment tier is predicted streamable and Mergeable (and
// admitted by the merge gate), so it takes every parallel arm; the
// buffered tier is predicted neither (and refused), so it runs serially
// under every knob.
func TestExtensionMatrixInvariance_Archive(t *testing.T) {
	t.Parallel()

	cohorts := newExtInvFixture(t, false)
	for _, tier := range []struct {
		mode invariance.Mode
		want bool
	}{{invariance.Default, true}, {invariance.Buffered, false}} {
		k := invariance.Knobs{Layout: invariance.File, DecodeWorkers: 1, ShardWorkers: 1, Mode: tier.mode}
		p, _ := extInvEngine(t, cohorts[invariance.File], k)
		req := extInvRequest("twin.pulse", extInvVariant{weighted: true, grouped: true})
		res, err := p.Predict(context.Background(), req)
		if err != nil || !res.Valid || len(res.Matrices) != 2 {
			t.Fatalf("%s predict: err=%v res=%+v", k, err, res)
		}
		for _, m := range res.Matrices {
			if m.Mergeable != tier.want || m.Streamable != tier.want || res.Streamable != tier.want {
				t.Fatalf("%s predict: matrix streamable=%v mergeable=%v request streamable=%v, want %v", k, m.Streamable, m.Mergeable, res.Streamable, tier.want)
			}
		}
		if got := processing.CanMergeRequestWithExtensions(req, nil, p.svc.Extensions()); got != tier.want {
			t.Fatalf("%s: merge gate admits = %v, want %v", k, got, tier.want)
		}
	}
	for _, v := range extInvVariants() {
		invariance.Run(t, extInvFamily(cohorts, v, []invariance.Group{extInvArchiveGroup()}))
	}
}

// TestExtensionMatrixInvariance_ComparatorBites is the family's
// falsification: the same rows folded block by block through the
// engine's fixed merge tree (linalg.MergeTree) and through a test
// double that merges the same blocks in another order render results
// the harness's comparator (invariance.BitExact) tells apart — so an
// engine whose merge order followed the worker partition could not pass
// TestExtensionMatrixInvariance_{Archive,Heavy}.
func TestExtensionMatrixInvariance_ComparatorBites(t *testing.T) {
	t.Parallel()

	const blocks = 25
	for _, mode := range []linalg.CoMomentMode{linalg.Listwise, linalg.Pairwise} {
		parts := make([]*linalg.CoMoment, blocks)
		for k := range parts {
			c, err := linalg.NewCoMoment(3, mode)
			if err != nil {
				t.Fatal(err)
			}
			for r := k * linalg.MergeBlockSize; r < (k+1)*linalg.MergeBlockSize; r++ {
				x, w := extInvValues(r)
				c.Add(x[:], w)
			}
			parts[k] = c
		}
		clones := func() []*linalg.CoMoment {
			out := make([]*linalg.CoMoment, len(parts))
			for i, c := range parts {
				out[i] = c.Clone()
			}
			return out
		}
		render := func(cm *linalg.CoMoment) *types.Response {
			in := &extend.MatrixInput{Name: "m", CoMoment: cm, Members: []string{"a", "b", "c"},
				WeightKind: types.WeightKindProbability, WantAuxiliary: true, WantVectors: true, WantScalars: true}
			out, err := extMomentsFinalize(in)
			if err != nil {
				t.Fatal(err)
			}
			return &types.Response{Matrices: []types.MatrixResult{{Name: "m", Type: "MAT_ACME_MOMENTS",
				Primary: out.Primary, Auxiliary: out.Auxiliary, Vectors: out.Vectors, Scalars: out.Scalars}}}
		}
		tree, err := linalg.MergeTree(clones())
		if err != nil {
			t.Fatal(err)
		}
		again, err := linalg.MergeTree(clones())
		if err != nil {
			t.Fatal(err)
		}
		if d := invariance.BitExact(render(again), render(tree)); len(d) > 0 {
			t.Fatalf("mode %v: the merge tree is not deterministic: %s", mode, d)
		}
		// The test double: a left fold from the last block down — the
		// order a partition-following merge could take.
		reordered := clones()
		acc := reordered[blocks-1]
		for k := blocks - 2; k >= 0; k-- {
			if err := acc.Merge(reordered[k]); err != nil {
				t.Fatal(err)
			}
		}
		if d := invariance.BitExact(render(acc), render(tree)); len(d) == 0 {
			t.Fatalf("mode %v: a reordered merge renders the same bits — the gate's comparator cannot see a merge-order break", mode)
		}
	}
}
