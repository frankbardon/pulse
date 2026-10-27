package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_matrix_invariance_test.go: the worker-invariance gate for
// extension MAT_* operators (U30 E1-S4, roadmap #156) — the
// public-registration twin of the service matrix family on the shared
// harness (TestMatrixInvariance_*). An operator registered through
// Options.Extensions.MatrixOps only (no engine hook) renders every raw
// figure of the merged co-moment it is handed; the gate proves those
// figures carry the same BITS serially, under every DecodeWorkers count
// over a >100K-record file, and under every ShardWorkers count over a
// shard archive of uneven shards (a 1-row shard, an empty shard, edges
// inside merge blocks) — listwise and pairwise, weighted and unweighted,
// grouped and ungrouped. Every parallel run must take its parallel arm
// (observe.Hooks), so no knob compares serial against serial. The
// buffered tier (Streamable: false) runs serially under any knob and
// lands on the co-moment tier's bits.

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

// extInvEngine is one Pulse instance per (tier, knobs), recording the
// arm of its last run.
type extInvEngine struct {
	p   *Pulse
	mu  sync.Mutex
	arm observe.Arm
}

func newExtInvEngine(t *testing.T, fsys afero.Fs, streamable bool, shardWorkers, decodeWorkers int) *extInvEngine {
	t.Helper()
	e := &extInvEngine{}
	p, err := New(Options{
		FS:            fsys,
		ShardWorkers:  shardWorkers,
		DecodeWorkers: decodeWorkers,
		Extensions:    Extensions{MatrixOps: []MatrixOpRegistration{extMomentsReg(streamable)}},
		Hooks: &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
			e.mu.Lock()
			e.arm = r.Arm
			e.mu.Unlock()
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.p = p
	return e
}

func (e *extInvEngine) run(t *testing.T, req *types.Request) (*types.Response, observe.Arm) {
	t.Helper()
	resp, err := e.p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return resp, e.arm
}

type extInvVariant struct {
	missing           string
	weighted, grouped bool
}

func (v extInvVariant) String() string {
	return fmt.Sprintf("%s_weighted=%v_grouped=%v", v.missing, v.weighted, v.grouped)
}

func extInvRequest(cohort string, v extInvVariant) *types.Request {
	spec := types.MatrixSpec{Type: "MAT_ACME_MOMENTS", Name: "m", Fields: []string{"a", "b", "c"},
		Params: json.RawMessage(`{"missing":"` + v.missing + `"}`)}
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Matrices: []types.MatrixSpec{spec}}
	if v.grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}
	}
	if v.weighted {
		req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	}
	return req
}

// extInvDiff describes the first bitwise difference between two runs'
// matrix results and matrix components ("" when they agree): group
// keys, every primary / auxiliary cell, the vectors, the scalars (keys
// in drop are ignored), the warnings and the components entries
// (float JSON is shortest round-trip, so equal text is equal bits).
func extInvDiff(got, want *types.Response, drop ...string) string {
	if len(got.Matrices) != len(want.Matrices) {
		return fmt.Sprintf("%d results, want %d", len(got.Matrices), len(want.Matrices))
	}
	values := func(where string, g, w *types.MatrixValues) string {
		if g == nil || w == nil {
			if g != w {
				return where + ": present on one side only"
			}
			return ""
		}
		if !slices.Equal(g.RowKeys, w.RowKeys) || !slices.Equal(g.ColumnKeys, w.ColumnKeys) || len(g.Values) != len(w.Values) {
			return where + ": shape differs"
		}
		for r := range w.Values {
			if len(g.Values[r]) != len(w.Values[r]) {
				return fmt.Sprintf("%s row %d: width differs", where, r)
			}
			for c := range w.Values[r] {
				if math.Float64bits(g.Values[r][c]) != math.Float64bits(w.Values[r][c]) {
					return fmt.Sprintf("%s cell (%d,%d) = %v, want %v", where, r, c, g.Values[r][c], w.Values[r][c])
				}
			}
		}
		return ""
	}
	for k := range want.Matrices {
		g, w := got.Matrices[k], want.Matrices[k]
		at := fmt.Sprintf("result %d (group %v)", k, w.GroupKey)
		if g.Name != w.Name || !reflect.DeepEqual(g.GroupKey, w.GroupKey) {
			return fmt.Sprintf("%s: name/group %q %v", at, g.Name, g.GroupKey)
		}
		if d := values(at+" primary", g.Primary, w.Primary); d != "" {
			return d
		}
		keys := func(m map[string]*types.MatrixValues) []string {
			out := make([]string, 0, len(m))
			for k := range m {
				out = append(out, k)
			}
			sort.Strings(out)
			return out
		}
		if !slices.Equal(keys(g.Auxiliary), keys(w.Auxiliary)) {
			return fmt.Sprintf("%s: auxiliary keys %v, want %v", at, keys(g.Auxiliary), keys(w.Auxiliary))
		}
		for _, key := range keys(w.Auxiliary) {
			if d := values(at+" auxiliary."+key, g.Auxiliary[key], w.Auxiliary[key]); d != "" {
				return d
			}
		}
		gm, _ := g.Vectors["mean"].([]float64)
		wm, _ := w.Vectors["mean"].([]float64)
		if len(gm) != len(wm) {
			return at + ": vectors.mean shape differs"
		}
		for i := range wm {
			if math.Float64bits(gm[i]) != math.Float64bits(wm[i]) {
				return fmt.Sprintf("%s vectors.mean[%d] = %v, want %v", at, i, gm[i], wm[i])
			}
		}
		for _, sk := range []string{"n", "w", "n_eff", "row_digest"} {
			if slices.Contains(drop, sk) {
				continue
			}
			gv, gok := g.Scalars[sk]
			wv, wok := w.Scalars[sk]
			if gok != wok || math.Float64bits(gv) != math.Float64bits(wv) {
				return fmt.Sprintf("%s scalars.%s = %v (%v), want %v (%v)", at, sk, gv, gok, wv, wok)
			}
		}
		if gc, wc := matrixWarningCodes(g.Warnings), matrixWarningCodes(w.Warnings); !slices.Equal(gc, wc) {
			return fmt.Sprintf("%s: warnings %v, want %v", at, gc, wc)
		}
	}
	var gc, wc []types.MatrixComponents
	if got.Components != nil {
		gc = got.Components.Matrices
	}
	if want.Components != nil {
		wc = want.Components.Matrices
	}
	gj, _ := json.Marshal(gc)
	wj, _ := json.Marshal(wc)
	if string(gj) != string(wj) {
		return fmt.Sprintf("components.matrices %s, want %s", gj, wj)
	}
	return ""
}

// TestExtensionMatrix_WorkerInvariantGateBites is the gate's
// falsification: the same rows folded block by block through the
// engine's fixed merge tree (linalg.MergeTree) and through a test
// double that merges the same blocks in another order give results the
// gate's comparator tells apart — so an engine whose merge order
// followed the worker partition could not pass
// TestExtensionMatrix_WorkerInvariant.
func TestExtensionMatrix_WorkerInvariantGateBites(t *testing.T) {
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
		if d := extInvDiff(render(again), render(tree)); d != "" {
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
		if d := extInvDiff(render(acc), render(tree)); d == "" {
			t.Fatalf("mode %v: a reordered merge renders the same bits — the gate's comparator cannot see a merge-order break", mode)
		}
	}
}
