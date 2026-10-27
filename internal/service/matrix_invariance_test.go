package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_invariance_test.go is the matrix family on the shared
// worker-invariance harness (internal/testutil/invariance): every
// built-in MAT_* registration — and the CoMoment proof vehicle beside it
// — answers the same bits serially and under every knob. A shape is ONE
// request carrying every spec variant (operator × listwise / pairwise ×
// unweighted / weighted, plus the GROUP_ROUNDED-between distance), so a
// cell is one Process call; the harness compares the WHOLE response
// (LayoutFree: every cell, determinant, auxiliary, scalar, vector,
// warning, Data row, Components entry), bitwise:
//
//   - archive: the shard archive (edges inside merge blocks; the heavy
//     tier's 8 shards hold a 123-row and a block-sized one) under
//     ShardWorkers 1/2/3/8 (and DecodeWorkers 7) ≡ its single-file twin
//     ≡ the forced buffered arm (AGG_MEDIAN, stripped again) — a smoke
//     subset in the fast tier (TestMatrixInvariance_Archive), every
//     shape in the heavy one;
//   - decode / decode_aligned / one_shard (heavy,
//     matrix_invariance_heavy_test.go): DecodeWorkers 2/3/7/8 over
//     105,177- and 102,400-record files (above the parallel-decode
//     threshold, ending mid-block and on a block boundary) ≡ serial, and
//     a one-shard archive ≡ its single file.
//
// Engagement: every parallel cell must report its arm (the harness),
// and the vehicle aggregations' instance count must equal one per
// partition (per bucket under a grouper) — so a parallel cell never
// compares serial against serial. The reference is anchored: each
// covariance / correlation / partial cell agrees with a straight
// single-pass CoMoment fold to rounding (referenceMatrixWords), the
// vehicle state with referenceCoMomentWords.
//
// What the harness cannot express stays dedicated:
// TestMatrixOnly_EqualsBesideAggregation (request-shape invariance),
// TestMatrixOnly_RunComponents (exact counters), the rank-method serial
// fallback (TestMatrixRankCorrelation_SerialUnderParallelKnobs) and the
// ordinal SEM (TestMatrixSEM_OrdinalWorkersBitIdentical).

// Extra layouts of the matrix family.
const (
	matInvBigAligned invariance.Layout = "big_file_aligned"
	matInvOneFile    invariance.Layout = "one_shard_file"
	matInvOneArchive invariance.Layout = "one_shard_archive"
)

// matInvShardRows: the heavy tier's archive split — a 123-row shard, a
// block-sized one, edges inside merge blocks; matInvSmokeShardRows the
// fast tier's (edges inside merge blocks, 13,995 rows).
var (
	matInvShardRows      = []int{3 * linalg.MergeBlockSize, 5000, linalg.MergeBlockSize, 123, 9000, 2*linalg.MergeBlockSize + 1, 777, 6000}
	matInvSmokeShardRows = []int{linalg.MergeBlockSize + 3, 700, 2*linalg.MergeBlockSize + 1, 999}
)

// matInvCohort is one layout's cohort.
type matInvCohort struct {
	cfg    *fs.Config
	path   string
	rows   int
	shards int
}

// matInvFixture maps layouts to cohorts over groupedInvSchema (the
// co-moment gate's columns plus region / tags), so ungrouped and
// grouped shapes read the same rows.
type matInvFixture struct {
	schema  *encoding.Schema
	cohorts map[invariance.Layout]matInvCohort
}

// newMatInvFixture writes File and Archive (hermetic; the smoke split
// in the fast tier, the full one in the heavy tier); heavy adds the
// decode-threshold files (a real directory: parallel decode needs mmap)
// and the one-shard pair.
func newMatInvFixture(t *testing.T, heavy bool) *matInvFixture {
	t.Helper()
	schema := groupedInvSchema(t)
	f := &matInvFixture{schema: schema, cohorts: map[invariance.Layout]matInvCohort{}}
	mem := fs.NewMemMap()
	single := func(cfg *fs.Config, osFs afero.Fs, path string, n int) {
		recs, nullAt := groupedInvRows(n, 0)
		if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	shardRows := matInvSmokeShardRows
	if heavy {
		shardRows = matInvShardRows
	}
	total := 0
	for _, n := range shardRows {
		total += n
	}
	single(mem, mem.Fs(), "twin.pulse", total)
	if err := afero.WriteFile(mem.Fs(), "archive.pulse", groupedInvArchive(t, schema, shardRows), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f.cohorts[invariance.File] = matInvCohort{mem, "twin.pulse", total, 0}
	f.cohorts[invariance.Archive] = matInvCohort{mem, "archive.pulse", total, len(shardRows)}
	if !heavy {
		return f
	}
	B := linalg.MergeBlockSize
	one := 3*B + 55
	single(mem, mem.Fs(), "one_twin.pulse", one)
	if err := afero.WriteFile(mem.Fs(), "one.pulse", groupedInvArchive(t, schema, []int{one}), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f.cohorts[matInvOneFile] = matInvCohort{mem, "one_twin.pulse", one, 0}
	f.cohorts[matInvOneArchive] = matInvCohort{mem, "one.pulse", one, 1}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	disk, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	for layout, n := range map[invariance.Layout]int{invariance.BigFile: 25*B + 2777, matInvBigAligned: 25 * B} {
		if n < parallelDecodeRecordThreshold {
			t.Fatalf("%s: %d records is below the parallel-decode threshold", layout, n)
		}
		path := fmt.Sprintf("%s/%s.pulse", dir, layout)
		single(disk, osFs, path, n)
		f.cohorts[layout] = matInvCohort{disk, path, n, 0}
	}
	return f
}

// matInvSpec is one matrix spec of a shape and what its reference must
// show.
type matInvSpec struct {
	v matrixInvarianceVariant
	// rounded marks the GROUP_ROUNDED-between distance, whose buckets
	// are GROUP_RANGE's at the same width: its matrix equals the
	// unweighted listwise MAT_DISTANCE spec's bit for bit.
	rounded bool
	// pair narrows the members to the two nullable ones (x1, x2), so a
	// pairwise run also tallies rows null on EVERY member: the integer
	// tallies behind Components.Matrices (listwise drops, pairwise
	// all-null rows) are summed across partitions, and both are non-zero.
	pair bool
}

// matInvSpecs: every built-in operator × missing mode × weighting (the
// matrixInvarianceVariants set without the vehicle flag) plus the
// GROUP_ROUNDED-between distance.
func matInvSpecs() []matInvSpec {
	var out []matInvSpec
	for _, v := range matrixInvarianceVariants {
		if !v.withVehicle {
			out = append(out, matInvSpec{v: v})
		}
	}
	out = append(out, matInvSpec{v: matrixInvarianceVariant{typ: types.MAT_DISTANCE}, rounded: true})
	for _, pairwise := range []bool{false, true} {
		out = append(out, matInvSpec{v: matrixInvarianceVariant{weighted: true, typ: types.MAT_COVARIANCE, pairwise: pairwise}, pair: true})
	}
	return out
}

// matInvSpecOf renders spec i of the batch.
func matInvSpecOf(path string, i int, s matInvSpec) types.MatrixSpec {
	spec := matrixInvarianceRequest(path, s.v).Matrices[0]
	spec.Name = fmt.Sprintf("m%02d", i)
	if s.pair {
		spec.Fields = []string{"x1", "x2"}
	}
	if s.rounded {
		spec.Params = json.RawMessage(strings.Replace(string(spec.Params), string(types.GROUP_RANGE), string(types.GROUP_ROUNDED), 1))
	}
	return spec
}

// matInvVehicles: the CoMoment vehicle listwise / pairwise × unweighted
// / weighted, one aggregation each (the BlockMerger gate's variants).
func matInvVehicles() []*types.Aggregation {
	var out []*types.Aggregation
	for _, v := range coMomentVariants {
		a := coMomentRequest("", v.mode, v.weighted).Aggregations[0]
		a.Label = fmt.Sprintf("cm_%s_%v", v.mode, v.weighted)
		out = append(out, a)
	}
	return out
}

// matInvShape is one request of the family.
type matInvShape struct {
	name  string
	specs []matInvSpec
	// vehicles: the vehicle aggregations ride the request.
	vehicles bool
	grouper  *groupedInvGrouper
	sort     []types.OrderKey
	// vectors: the computed / standardized vector shape (build owns the
	// specs).
	vectors bool
}

func (s matInvShape) build(path string) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: path}}
	if s.vectors {
		return matInvVectorRequest(path)
	}
	for i, spec := range s.specs {
		req.Matrices = append(req.Matrices, matInvSpecOf(path, i, spec))
	}
	if s.vehicles {
		req.Aggregations = matInvVehicles()
	}
	if s.grouper != nil {
		grp := *s.grouper.group
		req.Groups = []*types.Group{&grp}
	}
	if len(s.sort) > 0 {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "x3", Label: "sum"})
		req.Sort = s.sort
	}
	return req
}

// matInvVectorRequest batches the computed-member and standardize
// shapes: a row-local formula member (null above 9.9e5), raw and
// z-scored, under MAT_COVARIANCE (probability-weighted) and MAT_SEM; a
// z-scored plain vector under both. The standardized state is a
// function of the merged co-moment, so every path emits the same bits.
func matInvVectorRequest(path string) *types.Request {
	w := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	listwise := json.RawMessage(`{"max_drop_share": 0}`)
	return &types.Request{
		Cohort:     &types.Cohort{Filename: path},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "x3 > 9.9e5 ? nil : x3 / 1e3 + x3 * x3 / 1e9"}},
		Vectors: []types.VectorSpec{
			{Name: "zc", Fields: []string{"x1", "f", "x3"}},
			{Name: "zcs", Fields: []string{"x1", "f", "x3"}, Standardize: types.VectorStandardizeZScore},
			{Name: "zs", Fields: []string{"x1", "x2", "x3"}, Standardize: types.VectorStandardizeZScore},
			{Name: "zsem", Fields: []string{"x1", "x3"}, Standardize: types.VectorStandardizeZScore},
		},
		Matrices: []types.MatrixSpec{
			{Name: "computed", Type: types.MAT_COVARIANCE, Vector: "zc", Params: listwise, Weight: w},
			{Name: "computed_z", Type: types.MAT_COVARIANCE, Vector: "zcs", Params: listwise, Weight: w},
			{Name: "computed_sem", Type: types.MAT_SEM, Vector: "zcs", Params: json.RawMessage(`{"max_drop_share": 0, "model": "x1 ~ f + x3"}`)},
			{Name: "standardized", Type: types.MAT_COVARIANCE, Vector: "zs", Params: listwise, Weight: w},
			{Name: "standardized_sem", Type: types.MAT_SEM, Vector: "zsem", Params: json.RawMessage(`{"max_drop_share": 0, "model": "x1 ~ x3"}`)},
		},
	}
}

// matInvRangeGrouper buckets x3 (never null) by GROUP_RANGE: four
// buckets every partition sees.
var matInvRangeGrouper = groupedInvGrouper{"range", &types.Group{Type: types.GROUP_RANGE, Field: "x3", Interval: 250000}, nil, 4, 0}

// matInvSortSpecs: per operator one spec under Request.Sort — ascending
// weighted listwise, descending weighted pairwise (where the operator has
// the mode and weighting) — sorting is an ordering property, so one spec
// per operator carries it.
func matInvSortSpecs(desc bool) []matInvSpec {
	var out []matInvSpec
	for _, typ := range types.AllMatrixTypes() {
		out = append(out, matInvSpec{v: matrixInvarianceVariant{weighted: typ != types.MAT_SEM, typ: typ,
			pairwise: desc && typ != types.MAT_DISTANCE && typ != types.MAT_SEM}})
	}
	return out
}

// matInvSmokeSpecs: the fast tier's spec set — per operator its first
// variant (unweighted listwise), plus both two-member tallies and the
// GROUP_ROUNDED-between distance.
func matInvSmokeSpecs() []matInvSpec {
	var out []matInvSpec
	seen := map[types.MatrixType]bool{}
	for _, s := range matInvSpecs() {
		if s.pair || s.rounded || !seen[s.v.matrixType()] {
			seen[s.v.matrixType()] = true
			out = append(out, s)
		}
	}
	return out
}

// matInvShapes is the family: ungrouped matrix-only and beside the
// vehicles, the vector shape, and per grouper (a category with a
// one-row thin bucket, a set fan-out) unsorted and sorted ascending /
// descending on an aggregate (the buckets move, the bits do not), plus
// the GROUP_RANGE grouper (correlation and pairwise covariance).
func matInvShapes() []matInvShape {
	specs := matInvSpecs()
	out := []matInvShape{
		{name: "ungrouped_matrix_only", specs: specs},
		{name: "ungrouped_vehicles", specs: specs, vehicles: true},
		{name: "vectors", vectors: true},
	}
	for _, g := range groupedInvGroupers {
		g := g
		out = append(out,
			matInvShape{name: g.name, specs: specs, vehicles: true, grouper: &g},
			matInvShape{name: g.name + "_sort_asc", specs: matInvSortSpecs(false), vehicles: true, grouper: &g, sort: []types.OrderKey{{Field: "sum"}}},
			matInvShape{name: g.name + "_sort_desc", specs: matInvSortSpecs(true), vehicles: true, grouper: &g, sort: []types.OrderKey{{Field: "sum", Desc: true}}},
		)
	}
	return append(out, matInvShape{name: "range", vehicles: true, grouper: &matInvRangeGrouper, specs: []matInvSpec{
		{v: matrixInvarianceVariant{weighted: true, typ: types.MAT_CORRELATION}},
		{v: matrixInvarianceVariant{pairwise: true, typ: types.MAT_COVARIANCE}},
	}})
}

// matInvSmokeShapes are the fast tier's: the smoke specs ungrouped
// beside the vehicles, and a weighted correlation plus the pairwise
// two-member tally under the thin-bucket category grouper.
func matInvSmokeShapes() []matInvShape {
	g := groupedInvGroupers[0]
	var grouped []matInvSpec
	for _, s := range matInvSpecs() {
		if s.v == (matrixInvarianceVariant{weighted: true, typ: types.MAT_CORRELATION}) || s.pair && s.v.pairwise {
			grouped = append(grouped, s)
		}
	}
	return []matInvShape{
		{name: "smoke_ungrouped_vehicles", specs: matInvSmokeSpecs(), vehicles: true},
		{name: "smoke_" + g.name, specs: grouped, vehicles: true, grouper: &g},
	}
}

// matInvFamily is shape s over groups on fixture f.
func matInvFamily(f *matInvFixture, s matInvShape, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   s.name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			return f.run(t, s, k)
		},
		Check: func(t *testing.T, k invariance.Knobs, ref *types.Response) {
			f.check(t, s, k, ref)
		},
	}
}

// matInvMedian is the forced buffered arm's slot (non-streamable, stripped
// from the answer again).
const matInvMedian = "inv_median"

func (f *matInvFixture) run(t *testing.T, s matInvShape, k invariance.Knobs) invariance.Result[*types.Response] {
	t.Helper()
	c, ok := f.cohorts[k.Layout]
	if !ok {
		t.Fatalf("layout %s not built in this tier", k.Layout)
	}
	req := s.build(c.path)
	if k.Mode == invariance.Buffered {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "x3", Label: matInvMedian})
	}
	stats := &vehicleStats{}
	svc := New(c.cfg)
	svc.SetExtensions(coMomentVehicleRegistry(stats))
	svc.SetDecodeWorkers(k.DecodeWorkers)
	svc.SetShardWorkers(k.ShardWorkers)
	ei := NewExecInfo(time.Now())
	resp, err := svc.Process(WithExecInfo(context.Background(), ei), req)
	if err != nil {
		t.Fatalf("%s: Process: %v", k, err)
	}
	arm := ei.Snapshot().Arm
	if s.vehicles {
		// One instance per vehicle per partition (per bucket: each
		// partition sees g.common buckets, the one holding row 5 also
		// the thin one).
		parts := int64(1)
		switch {
		case arm == observe.ArmShardParallel:
			parts = int64(c.shards)
		case arm == observe.ArmParallelDecode:
			parts = int64(k.DecodeWorkers)
		}
		common, thin := int64(1), int64(0)
		if s.grouper != nil {
			common, thin = s.grouper.common, s.grouper.thin
		}
		if want := int64(len(coMomentVariants)) * (parts*common + thin); stats.instancesWithRows.Load() != want {
			t.Fatalf("%s (arm %s): %d vehicle instances, want %d — the arm did not partition the rows", k, arm, stats.instancesWithRows.Load(), want)
		}
	}
	if k.Mode == invariance.Buffered {
		resp = matInvStripMedian(t, resp)
	}
	return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm}
}

// matInvStripMedian removes the buffered arm's AGG_MEDIAN column (and a
// row it alone filled) and its Components entry.
func matInvStripMedian(t *testing.T, resp *types.Response) *types.Response {
	t.Helper()
	out := *resp
	out.Data = nil
	for _, row := range resp.Data {
		if _, ok := row[matInvMedian]; !ok {
			t.Fatalf("buffered row lacks %s: %v", matInvMedian, row)
		}
		r := make(map[string]any, len(row))
		for k, v := range row {
			if k != matInvMedian {
				r[k] = v
			}
		}
		if len(r) > 0 {
			out.Data = append(out.Data, r)
		}
	}
	if c := resp.Components; c != nil {
		cc := *c
		cc.Aggregations = nil
		for _, a := range c.Aggregations {
			if a.Label != matInvMedian {
				cc.Aggregations = append(cc.Aggregations, a)
			}
		}
		out.Components = &cc
	}
	return &out
}

// check guards the reference: every spec answered (per bucket under a
// grouper, in the final Data order), listwise carries its drop warning
// and pairwise its auxiliary.n, the anchored operators agree with a
// straight fold, the vehicles with theirs, and every Components entry
// counts null rows (so the summed tallies are not zero == zero).
func (f *matInvFixture) check(t *testing.T, s matInvShape, k invariance.Knobs, resp *types.Response) {
	t.Helper()
	n := f.cohorts[k.Layout].rows
	if resp.Components == nil || resp.Components.Run == nil || len(resp.Components.Matrices) != len(resp.Matrices) {
		t.Fatalf("Components.Run / Components.Matrices missing")
	}
	if s.vectors {
		if len(resp.Matrices) != 5 {
			t.Fatalf("vector shape: %d matrices, want 5", len(resp.Matrices))
		}
		for _, m := range resp.Matrices {
			if m.Primary == nil || math.IsNaN(m.Primary.Values[0][len(m.Primary.Values[0])-1]) {
				t.Fatalf("%s: undefined matrix", m.Name)
			}
		}
		return
	}
	buckets := 1
	var keys []any
	if s.grouper != nil {
		for _, row := range resp.Data {
			keys = append(keys, row[s.grouper.group.Field])
		}
		buckets = len(keys)
		if want := s.grouper.keys; want != nil {
			if !sameKeySet(keys, want) {
				t.Fatalf("Data keys %v, want a permutation of %v", keys, want)
			}
			if sorted := len(s.sort) > 0; sorted == reflect.DeepEqual(keys, want) {
				t.Fatalf("Data keys %v under sort %v: want the unsorted order %v moved iff sorted", keys, s.sort, want)
			}
		} else if buckets != int(s.grouper.common) {
			t.Fatalf("%d buckets, want %d", buckets, s.grouper.common)
		}
	}
	if len(resp.Matrices) != len(s.specs)*buckets {
		t.Fatalf("%d matrices, want %d specs × %d buckets", len(resp.Matrices), len(s.specs), buckets)
	}
	for i, spec := range s.specs {
		for b := 0; b < buckets; b++ {
			m := resp.Matrices[i*buckets+b]
			if m.Primary == nil || m.Type != spec.v.matrixType() {
				t.Fatalf("result %d: %s %v, want %s", i*buckets+b, m.Name, m.Type, spec.v.matrixType())
			}
			thin := false
			if s.grouper != nil {
				if !reflect.DeepEqual(m.GroupKey, types.AxisKey{keys[b]}) || !reflect.DeepEqual(resp.Components.Matrices[i*buckets+b].GroupKey, m.GroupKey) {
					t.Fatalf("%s bucket %d: group_key %v, want [%v] (Data order)", m.Name, b, m.GroupKey, keys[b])
				}
				thin = keys[b] == "thin"
				if thin != (findWarning(m, errors.PULSE_MATRIX_INSUFFICIENT_N) != nil) {
					t.Fatalf("%s bucket %v: PULSE_MATRIX_INSUFFICIENT_N present = %v (warnings %v)", m.Name, keys[b], !thin, matWarningCodes(m))
				}
				if !thin && math.IsNaN(m.Primary.Values[0][len(m.Primary.Values[0])-1]) {
					t.Fatalf("%s bucket %v: populated bucket has an undefined cell", m.Name, keys[b])
				}
			}
			if thin {
				continue
			}
			matInvCheckSpec(t, spec, m, s.grouper != nil && s.grouper.group.Field == "x3")
			if c := resp.Components.Matrices[i*buckets+b]; (spec.pair || !spec.v.pairwise) && c.NNull == 0 {
				t.Fatalf("%s %v: Components.Matrices n_null is 0; the fixture's nulls are gone", c.Name, c.GroupKey)
			}
		}
		if s.grouper == nil && !spec.rounded && !spec.pair {
			// 1e-6 for MAT_PARTIAL_CORRELATION, whose inversion of these
			// near-collinear members (x2 ≈ x1 / 2, cond ≈ 1e8) amplifies
			// the blocked-vs-straight co-moment rounding.
			tol := 1e-9
			if spec.v.matrixType() == types.MAT_PARTIAL_CORRELATION {
				tol = 1e-6
			}
			matInvAnchor(t, resp.Matrices[i], referenceMatrixWords(t, n, spec.v), tol)
		}
		if spec.rounded && s.grouper == nil {
			// The GROUP_RANGE-between twin (its cells; the bucket labels
			// differ) is the unweighted listwise
			// MAT_DISTANCE spec of the same batch.
			for j, other := range s.specs {
				if other.v == (matrixInvarianceVariant{typ: types.MAT_DISTANCE}) && !other.rounded {
					if d := invariance.BitExact(resp.Matrices[i].Primary.Values, resp.Matrices[j].Primary.Values); d != nil {
						t.Fatalf("GROUP_ROUNDED between differs from its GROUP_RANGE twin: %v", d)
					}
				}
			}
		}
	}
	if s.vehicles && s.grouper == nil {
		if len(resp.Data) != 1 {
			t.Fatalf("%d Data rows, want 1", len(resp.Data))
		}
		for _, v := range coMomentVariants {
			label := fmt.Sprintf("cm_%s_%v", v.mode, v.weighted)
			words, ok := resp.Data[0][label].([]uint64)
			if !ok {
				t.Fatalf("%s = %T, want the vehicle's rich []uint64", label, resp.Data[0][label])
			}
			assertWordsClose(t, words, referenceCoMomentWords(t, n, v.mode, v.weighted))
		}
	}
	if !s.vehicles && len(resp.Data) != 0 {
		t.Fatalf("matrix-only request emitted %d Data rows", len(resp.Data))
	}
}

// matInvAnchor compares m's primary cells with the straight-fold
// reference words (nil: the operator has no FMA-free anchor) to a
// relative tol — the correctness anchor (wrong member, wrong weight,
// wrong operator) the blocked result must agree with to rounding.
func matInvAnchor(t *testing.T, m types.MatrixResult, ref []uint64, tol float64) {
	t.Helper()
	if ref == nil {
		return
	}
	var cells []float64
	for _, row := range m.Primary.Values {
		cells = append(cells, row...)
	}
	if len(cells) != len(ref) {
		t.Fatalf("%s: %d cells, reference %d", m.Name, len(cells), len(ref))
	}
	for i := range ref {
		x, y := cells[i], math.Float64frombits(ref[i])
		if d := math.Abs(x - y); !(d <= tol*math.Max(1, math.Max(math.Abs(x), math.Abs(y)))) {
			t.Errorf("%s cell %d: %v, reference %v", m.Name, i, x, y)
		}
	}
}

// matInvCheckSpec guards one populated result's operator outputs.
// byX3 marks a bucket of x3 itself, inside which a MAT_DISTANCE between
// over x3 holds one bucket.
func matInvCheckSpec(t *testing.T, spec matInvSpec, m types.MatrixResult, byX3 bool) {
	t.Helper()
	pairwise := spec.v.pairwise
	if (m.Auxiliary["n"] != nil) != pairwise {
		t.Fatalf("%s: auxiliary.n present = %v, pairwise = %v", m.Name, m.Auxiliary["n"] != nil, pairwise)
	}
	if !pairwise && findWarning(m, errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP) == nil {
		t.Fatalf("%s: listwise result carries no PULSE_MATRIX_LISTWISE_HEAVY_DROP (warnings %v)", m.Name, matWarningCodes(m))
	}
	vec := func(k string, n int) {
		if vs, ok := m.Vectors[k].([]float64); !ok || len(vs) != n {
			t.Fatalf("%s: vectors.%s = %#v, want %d values", m.Name, k, m.Vectors[k], n)
		}
	}
	switch m.Type {
	case types.MAT_CORRELATION:
		if pairs, ok := m.Vectors["top_pairs"].([]types.MatrixPair); !ok || len(pairs) != 3 {
			t.Fatalf("%s: top_pairs = %#v, want 3 pairs", m.Name, m.Vectors["top_pairs"])
		}
	case types.MAT_RELIABILITY:
		vec("item_total_r", 3)
	case types.MAT_PCA:
		vec("eigenvalues", 3)
		if m.Auxiliary["eigenvectors"] == nil {
			t.Fatalf("%s: no eigenvectors", m.Name)
		}
		if pairwise {
			vec("rotated_variance", 3)
		}
	case types.MAT_FACTOR:
		vec("communalities", 3)
	case types.MAT_COLLINEARITY:
		vec("vif", 3)
	case types.MAT_DISTANCE:
		if byX3 {
			vec("sizes", 1)
		} else {
			vec("sizes", 4)
		}
	case types.MAT_SEM:
		if est := m.Auxiliary["estimates"]; est == nil || len(est.Values) == 0 {
			t.Fatalf("%s: MAT_SEM carries no estimates", m.Name)
		}
	}
}

// matInvAdmitted fails unless the merge gate admits every shape (so a
// parallel cell's arm is the engine's choice, not a refusal).
func matInvAdmitted(t *testing.T, f *matInvFixture, shapes []matInvShape) {
	t.Helper()
	exts := coMomentVehicleRegistry(&vehicleStats{})
	for _, s := range shapes {
		req := s.build("x.pulse")
		if !processing.CanMergeRequestWithExtensions(req, f.schema, exts) || !processing.CanStreamRequestWithExtensions(req, f.schema, exts) {
			t.Fatalf("%s: the request must stream and merge", s.name)
		}
	}
}

// TestMatrixInvariance_Archive is the fast-tier smoke of the matrix
// family: matInvSmokeShapes over the smoke archive (ShardWorkers
// 1/2/3/8, DecodeWorkers 7, the single-file twin) and the buffered arm.
// TestMatrixInvariance_Heavy runs every shape on the full archive.
func TestMatrixInvariance_Archive(t *testing.T) {
	t.Parallel()

	f := newMatInvFixture(t, false)
	matInvAdmitted(t, f, matInvSmokeShapes())
	archive := invariance.ArchiveGroup()
	archive.Cells = append(archive.Cells, invariance.BufferedCell())
	for _, s := range matInvSmokeShapes() {
		invariance.Run(t, matInvFamily(f, s, []invariance.Group{archive}))
	}
}

// TestMatrixOnly_EqualsBesideAggregation: a matrix does not depend on
// what else the request carries — the matrix-only batch and the same
// batch beside the vehicle aggregations emit the same matrices, matrix
// warnings and Components.Matrices bit for bit.
func TestMatrixOnly_EqualsBesideAggregation(t *testing.T) {
	t.Parallel()

	n := 2*linalg.MergeBlockSize + 9
	cfg := fs.NewMemMap()
	recs, nullAt := groupedInvRows(n, 0)
	if err := afero.WriteFile(cfg.Fs(), "m.pulse", writeNullablePulse(t, groupedInvSchema(t), recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := func(vehicles bool) *types.Response {
		req := matInvShape{specs: matInvSpecs(), vehicles: vehicles}.build("m.pulse")
		svc := New(cfg)
		svc.SetExtensions(coMomentVehicleRegistry(&vehicleStats{}))
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if len(resp.Matrices) != len(req.Matrices) || resp.Components == nil {
			t.Fatalf("%d matrices for %d specs", len(resp.Matrices), len(req.Matrices))
		}
		return resp
	}
	alone, beside := run(false), run(true)
	if d := invariance.BitExact(alone.Matrices, beside.Matrices); d != nil {
		t.Errorf("matrix-only differs from matrix-beside-aggregation:\n  %s", strings.Join(d, "\n  "))
	}
	if d := invariance.BitExact(alone.Components.Matrices, beside.Components.Matrices); d != nil {
		t.Errorf("Components.Matrices differ:\n  %s", strings.Join(d, "\n  "))
	}
}
