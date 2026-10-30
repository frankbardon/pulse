package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vector_invariance_test.go is the vector family on the shared
// worker-invariance harness: the vector aggregates (AGG_VEC_MEAN / SUM /
// SD, weighted and not) and covariance matrices over the `b*` members,
// ungrouped and grouped, and a crosstab whose element cell and
// auxiliary read non-dyadic members spanning several merge blocks
// (unweighted and under a request weight), answer the same whole
// response (LayoutFree), bitwise:
//
//   - archive: the bit cohort split unevenly (two 1-row shards, an empty
//     shard, edges inside merge blocks) under ShardWorkers 1/2/3/8 and
//     DecodeWorkers 7 ≡ its single-file twin — blocks key by the global
//     record ordinal, so twin and archive fold the same blocks in the
//     same tree; the crosstab shapes (read serially over an archive —
//     the crosstab path never takes the shard reducer) also on the
//     buffered crosstab arm (DisableCrosstabFusion) ≡ the fused one;
//   - decode (heavy, vector_invariance_heavy_test.go): DecodeWorkers
//     2/3/7/8 over a 103,634-record file ≡ serial (a crosstab's
//     parallel segment decode is its buffered arm's; the fused arm
//     stays serial).

// vecInvBounds / vecInvSmokeBounds: the heavy and the fast tier's shard
// edges (21,714 and 9,426 records).
var (
	vecInvBounds      = []int{0, 1, 2, 4097, 4097, 9000, 9001, 14000, 14000 + 4096, 5*4096 + 1234}
	vecInvSmokeBounds = []int{0, 1, 2, 4097, 4097, 6000, 2*4096 + 1234}
)

// newVecInvFixture writes the twin and the archive (hermetic); heavy
// adds the decode-threshold file on disk.
func newVecInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	bounds := vecInvSmokeBounds
	if heavy {
		bounds = vecInvBounds
	}
	mem := afero.NewMemMapFs()
	writeVaggBitsCohort(t, mem, "single.pulse", 0, bounds[len(bounds)-1])
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("s%d.pulse", i)
		writeVaggBitsCohort(t, mem, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	w, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	out := map[invariance.Layout]invCohort{
		invariance.File:    {mem, "single.pulse"},
		invariance.Archive: {mem, "archive.pulse"},
	}
	if heavy {
		disk := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
		writeVaggBitsCohort(t, disk, "big.pulse", 0, 25*4096+1234)
		out[invariance.BigFile] = invCohort{disk, "big.pulse"}
	}
	return out
}

// vecInvCrosstab is the element-cell crosstab, weighted or not.
func vecInvCrosstab(cohort string, weighted bool) *types.Request {
	r := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
		Cell:    &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m", Params: json.RawMessage(`{"element":"b2"}`)},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		MarginAggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_SUM, Fields: []string{"b*"}, Label: "s", Params: json.RawMessage(`{"element":"b3"}`)},
		},
	}}
	if weighted {
		r.Weight = &types.WeightSpec{Field: "score"}
	}
	return r
}

// vecInvShape is one request shape of the family.
type vecInvShape struct {
	name     string
	crosstab bool
	build    func(cohort string) *types.Request
}

func vecInvShapes() []vecInvShape {
	return []vecInvShape{
		{"aggregates", false, func(c string) *types.Request { return vaggBitsRequest(c, false) }},
		{"aggregates_grouped", false, func(c string) *types.Request { return vaggBitsRequest(c, true) }},
		{"crosstab_element", true, func(c string) *types.Request { return vecInvCrosstab(c, false) }},
		{"crosstab_element_weighted", true, func(c string) *types.Request { return vecInvCrosstab(c, true) }},
	}
}

// vecInvGroups are a shape's groups: the archive group (heavy adds the
// decode group). A crosstab never takes the shard reducer — the crosstab
// path reads an archive serially, shard by shard — and the fused arm
// never fans its decode out, so every crosstab cell names the crosstab
// arm it takes (fused, or buffered under DisableCrosstabFusion), except
// the buffered arm over the big file, which decodes in parallel
// segments.
func vecInvGroups(s vecInvShape, heavy bool) []invariance.Group {
	if !s.crosstab {
		gs := []invariance.Group{invariance.ArchiveGroup()}
		if heavy {
			gs = append(gs, invariance.DecodeGroup("decode", invariance.BigFile))
		}
		return gs
	}
	cell := func(l invariance.Layout, dw, sw int, m invariance.Mode, arm observe.Arm) invariance.Cell {
		return invariance.Cell{Knobs: invariance.Knobs{Layout: l, DecodeWorkers: dw, ShardWorkers: sw, Mode: m}, Arm: arm}
	}
	fused, buffered := observe.ArmFusedCrosstab, observe.ArmBuffered
	archive := invariance.Group{Name: "archive", Ref: cell(invariance.File, 1, 1, invariance.Default, fused)}
	for _, sw := range invariance.ArchiveShardWorkers {
		archive.Cells = append(archive.Cells, cell(invariance.Archive, 1, sw, invariance.Default, fused))
	}
	archive.Cells = append(archive.Cells,
		cell(invariance.Archive, 7, 3, invariance.Default, fused),
		cell(invariance.File, 1, 1, invariance.Buffered, buffered),
		cell(invariance.Archive, 1, 3, invariance.Buffered, buffered))
	gs := []invariance.Group{archive}
	if heavy {
		decode := invariance.Group{Name: "decode", Ref: cell(invariance.BigFile, 1, 1, invariance.Default, fused)}
		for _, dw := range invariance.BigFileDecoders {
			decode.Cells = append(decode.Cells,
				cell(invariance.BigFile, dw, 1, invariance.Default, fused),
				cell(invariance.BigFile, dw, 1, invariance.Buffered, observe.ArmParallelDecode))
		}
		gs = append(gs, decode)
	}
	return gs
}

// vecInvFamily is shape s over groups.
func vecInvFamily(cohorts map[invariance.Layout]invCohort, s vecInvShape, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   s.name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			co, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := invEngine(t, pulse.Options{DisableCrosstabFusion: k.Mode == invariance.Buffered}, co, k)
			resp, err := p.Process(context.Background(), s.build(co.path))
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			out := invariance.LayoutFree(resp)
			return invariance.Result[*types.Response]{Answer: out, Arm: arm()}
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if s.crosstab {
				if resp.Crosstab == nil || resp.Crosstab.Matrix == nil || len(vxCells(t, resp)) < 4 || len(vxAux(t, resp)) == 0 {
					t.Fatal("degenerate crosstab")
				}
				return
			}
			if len(resp.Data) == 0 || len(resp.Matrices) == 0 {
				t.Fatalf("degenerate run: %d rows, %d matrices", len(resp.Data), len(resp.Matrices))
			}
			for _, label := range []string{"m", "s", "wm", "sd", "wsd"} {
				if v, ok := vaggArray(resp.Data[0][label]); !ok || len(v) != 4 {
					t.Fatalf("%s = %#v, want a 4-member array", label, resp.Data[0][label])
				}
			}
		},
	}
}

// vecInvSmokeShapes run in the fast tier: the ungrouped and grouped
// aggregates (a weighted and an unweighted slot per operator, the
// covariance per bucket; ungrouped, Run.NullRecords is the vector-first
// slot's floor n_null on the shard reducer too) and the weighted
// crosstab (both crosstab arms).
var vecInvSmokeShapes = map[string]bool{"aggregates": true, "aggregates_grouped": true, "crosstab_element_weighted": true}

// TestVectorInvariance_Archive is the fast-tier smoke of the vector
// family (vecInvSmokeShapes); TestVectorInvariance_Heavy runs every
// shape on the full split and adds the parallel-decode group.
func TestVectorInvariance_Archive(t *testing.T) {
	t.Parallel()

	cohorts := newVecInvFixture(t, false)
	for _, s := range vecInvShapes() {
		if vecInvSmokeShapes[s.name] {
			invariance.Run(t, vecInvFamily(cohorts, s, vecInvGroups(s, false)))
		}
	}
}
