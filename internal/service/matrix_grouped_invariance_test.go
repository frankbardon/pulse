package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_grouped_invariance_test.go is the U16 E4-S2 gate: grouped
// matrices are worker-invariant PER BUCKET. For every built-in operator
// × listwise / pairwise × unweighted / weighted, under a plain grouper
// with a thin bucket and a fan-out grouper, the serial run, every
// DecodeWorkers count (2/3/7/8) on a single file above the parallel
// threshold and every ShardWorkers count (2/3/8) on an archive return
// the same bucket keys in the same order and, per bucket, the same
// bits — every primary cell, auxiliary.n, the determinant, top_pairs
// (row, col, r bits, n) — the same matrix warnings (the thin bucket's
// PULSE_MATRIX_INSUFFICIENT_N, listwise's summed drop count), the same
// Components.Matrices and Components.Run. Sort variants (Request.Sort
// ascending and descending on an aggregate) move the buckets into the
// sorted Data order on every arm and leave the bits alone. The vehicle aggregation
// beside the matrix counts its per-bucket instances, which proves the
// parallel arm really partitioned the rows.

// groupedInvSchema is coMomentSchema plus a categorical region (a / b /
// c round-robin, "thin" on global row 5 alone) and a set-valued tags
// column (a non-empty mask over four labels — the fan-out grouper).
func groupedInvSchema(t testing.TB) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	s := coMomentSchema()
	s.Fields = append(s.Fields,
		encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 32, CsvColumnIdx: 4, Dictionary: dict("a", "b", "c", "thin")},
		encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 33, CsvColumnIdx: 5, Dictionary: dict("T0", "T1", "T2", "T3")},
	)
	return s
}

// groupedInvThinRow is the one global row in region "thin".
const groupedInvThinRow = 5

// groupedInvRows is coMomentRows (offset..offset+n-1 of the one global
// sequence) plus the region and tags columns.
func groupedInvRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs, nullAt := coMomentRows(n, offset)
	for i := range recs {
		g := offset + i
		region := uint64(g % 3)
		if g == groupedInvThinRow {
			region = 3
		}
		recs[i] = append(recs[i], region, uint64(g%15)+1)
	}
	return recs, nullAt
}

func groupedInvArchive(t *testing.T, schema *encoding.Schema, shardRows []int) []byte {
	t.Helper()
	var doc bytes.Buffer
	total, offset := 0, 0
	for _, n := range shardRows {
		total += n
	}
	if err := encx.WriteSchemaDoc(&doc, schema, uint64(total), uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for i, n := range shardRows {
		recs, nullAt := groupedInvRows(n, offset)
		write(fmt.Sprintf("s%d.pulse", i), writeNullablePulse(t, schema, recs, nullAt))
		offset += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// groupedInvGrouper is one grouper of the gate with the bucket keys it
// must emit, how many of them every partition sees (common) and how
// many only the partition holding row 5 sees (thin) — so a run over k
// partitions feeds exactly k·common + thin vehicle instances.
type groupedInvGrouper struct {
	name   string
	group  *types.Group
	keys   []any
	common int64
	thin   int64
}

var groupedInvGroupers = []groupedInvGrouper{
	{"category_with_thin_bucket", &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}, []any{"a", "b", "c", "thin"}, 3, 1},
	{"set_fan_out", &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}, []any{"T0", "T1", "T2", "T3"}, 4, 0},
}

// groupedInvVariant is one grouped request shape: the ungrouped
// variant's operator / missing mode / weighting / vehicle, extended
// with grouped-only request knobs (a new knob is a field here, applied
// in groupedInvRequest and named in String).
type groupedInvVariant struct {
	matrixInvarianceVariant
	// sort is an explicit Request.Sort over the per-bucket sum of x3
	// (an AGG_SUM labelled "sum" rides the request; the fixtures' bucket
	// sums are far apart, so the order is the same under any
	// partitioning and moves every bucket list off its unsorted order in
	// both directions): matrices follow the sorted Data rows, so the
	// sorted serial run is the reference.
	sort []types.OrderKey
}

func (v groupedInvVariant) String() string {
	s := v.matrixInvarianceVariant.String()
	for _, k := range v.sort {
		dir := "asc"
		if k.Desc {
			dir = "desc"
		}
		s += "_sort_" + k.Field + "_" + dir
	}
	return s
}

// groupedInvVariants: every built-in operator × missing mode ×
// weighting, beside the vehicle (the engagement proof).
var groupedInvVariants = func() []groupedInvVariant {
	var out []groupedInvVariant
	for _, typ := range types.AllMatrixTypes() {
		for _, pairwise := range []bool{false, true} {
			for _, weighted := range []bool{false, true} {
				out = append(out, groupedInvVariant{matrixInvarianceVariant: matrixInvarianceVariant{weighted: weighted, withVehicle: true, typ: typ, pairwise: pairwise}})
			}
		}
		// Request.Sort ascending and descending on an aggregate: the
		// buckets move, the bits do not.
		for _, desc := range []bool{false, true} {
			out = append(out, groupedInvVariant{
				matrixInvarianceVariant: matrixInvarianceVariant{weighted: true, withVehicle: true, typ: typ, pairwise: desc},
				sort:                    []types.OrderKey{{Field: "sum", Desc: desc}},
			})
		}
	}
	return out
}()

func groupedInvRequest(path string, v groupedInvVariant, g groupedInvGrouper) *types.Request {
	req := matrixInvarianceRequest(path, v.matrixInvarianceVariant)
	grp := *g.group
	req.Groups = []*types.Group{&grp}
	if len(v.sort) > 0 {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "x3", Label: "sum"})
		req.Sort = v.sort
	}
	return req
}

// groupedInvBucket is one bucket's result, flattened to what a reducer
// could get wrong.
type groupedInvBucket struct {
	key      types.AxisKey
	words    []uint64
	pairs    []types.MatrixPair
	warnings []*types.ResponseWarning
}

type groupedInvRun struct {
	buckets   []groupedInvBucket
	warnings  []*types.ResponseWarning
	run       types.RunComponents
	comps     []types.MatrixComponents
	instances int64
}

func runGroupedInv(t *testing.T, cfg *fs.Config, req *types.Request, g groupedInvGrouper, decodeWorkers, shardWorkers int) groupedInvRun {
	t.Helper()
	stats := &vehicleStats{}
	svc := New(cfg)
	svc.SetExtensions(coMomentVehicleRegistry(stats))
	svc.SetDecodeWorkers(decodeWorkers)
	svc.SetShardWorkers(shardWorkers)
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process(decode=%d, shard=%d): %v", decodeWorkers, shardWorkers, err)
	}
	if len(resp.Matrices) != len(g.keys) || len(resp.Data) != len(g.keys) {
		t.Fatalf("Process(decode=%d, shard=%d): %d matrices over %d buckets, want %d", decodeWorkers, shardWorkers, len(resp.Matrices), len(resp.Data), len(g.keys))
	}
	if resp.Components == nil || resp.Components.Run == nil || len(resp.Components.Matrices) != len(resp.Matrices) {
		t.Fatalf("Process(decode=%d, shard=%d): Components.Run / Components.Matrices missing", decodeWorkers, shardWorkers)
	}
	out := groupedInvRun{warnings: resp.Warnings, run: *resp.Components.Run, comps: resp.Components.Matrices, instances: stats.instancesWithRows.Load()}
	// The buckets are g.keys, in the final Data order (Request.Sort
	// included); results and Components entries mirror that order.
	dataOrder := make([]any, len(resp.Data))
	for i, row := range resp.Data {
		dataOrder[i] = row[g.group.Field]
	}
	if sorted := len(req.Sort) > 0; sorted == reflect.DeepEqual(dataOrder, g.keys) {
		t.Fatalf("Data keys %v under sort %v: want the unsorted order %v moved iff sorted", dataOrder, req.Sort, g.keys)
	}
	if !sameKeySet(dataOrder, g.keys) {
		t.Fatalf("Data keys %v, want a permutation of %v", dataOrder, g.keys)
	}
	for i, m := range resp.Matrices {
		if !reflect.DeepEqual(m.GroupKey, types.AxisKey{dataOrder[i]}) || !reflect.DeepEqual(resp.Components.Matrices[i].GroupKey, types.AxisKey{dataOrder[i]}) {
			t.Fatalf("result %d: group_key %v / components %v, want [%v] (Data order)", i, m.GroupKey, resp.Components.Matrices[i].GroupKey, dataOrder[i])
		}
		b := groupedInvBucket{key: m.GroupKey, words: matrixResultWords(m), warnings: m.Warnings}
		if pairs, ok := m.Vectors["top_pairs"].([]types.MatrixPair); ok {
			b.pairs = pairs
		} else if m.Type == types.MAT_CORRELATION {
			t.Fatalf("bucket %v: no top_pairs", m.GroupKey)
		}
		thin := m.GroupKey[0] == "thin"
		if thin != (findWarning(m, errors.PULSE_MATRIX_INSUFFICIENT_N) != nil) {
			t.Fatalf("bucket %v: PULSE_MATRIX_INSUFFICIENT_N present = %v (warnings %v)", m.GroupKey, !thin, matWarningCodes(m))
		}
		// A populated bucket's off-diagonal cells are real figures, so
		// equality below is never null == null.
		if !thin && math.IsNaN(m.Primary.Values[0][len(m.Primary.Values[0])-1]) {
			t.Fatalf("bucket %v: populated bucket has an undefined cell", m.GroupKey)
		}
		out.buckets = append(out.buckets, b)
	}
	return out
}

// sameKeySet reports whether a and b hold the same keys, any order.
func sameKeySet(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[any]int, len(a))
	for _, k := range a {
		seen[k]++
	}
	for _, k := range b {
		if seen[k]--; seen[k] < 0 {
			return false
		}
	}
	return true
}

func assertGroupedInvEqual(t *testing.T, label string, got, want groupedInvRun) {
	t.Helper()
	for i := range want.buckets {
		g, w := got.buckets[i], want.buckets[i]
		if !reflect.DeepEqual(g.key, w.key) {
			t.Fatalf("%s: bucket %d key %v, serial %v", label, i, g.key, w.key)
		}
		if j := firstWordDiff(g.words, w.words); j != -1 {
			t.Errorf("%s: bucket %v differs from serial at word %d", label, w.key, j)
		}
		if !reflect.DeepEqual(g.pairs, w.pairs) {
			t.Errorf("%s: bucket %v top_pairs %+v, serial %+v", label, w.key, g.pairs, w.pairs)
		}
		if !reflect.DeepEqual(g.warnings, w.warnings) {
			t.Errorf("%s: bucket %v warnings %s, serial %s", label, w.key, mustJSON(t, g.warnings), mustJSON(t, w.warnings))
		}
	}
	if !reflect.DeepEqual(got.warnings, want.warnings) {
		t.Errorf("%s: response warnings %s, serial %s", label, mustJSON(t, got.warnings), mustJSON(t, want.warnings))
	}
	if got.run != want.run {
		t.Errorf("%s: Components.Run %+v, serial %+v", label, got.run, want.run)
	}
	if !reflect.DeepEqual(got.comps, want.comps) {
		t.Errorf("%s: Components.Matrices %s, serial %s", label, mustJSON(t, got.comps), mustJSON(t, want.comps))
	}
}

// TestMatrixGrouped_WorkerInvariant is the grouped worker-invariance
// gate (the per-bucket twin of TestMatrixCovariance_WorkerInvariant).
func TestMatrixGrouped_WorkerInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record grouped matrix worker-invariance gate in -short mode")
	}
	schema := groupedInvSchema(t)
	B := linalg.MergeBlockSize

	for _, g := range groupedInvGroupers {
		for _, v := range groupedInvVariants {
			v.withVehicle = false
			if !processing.CanMergeRequest(groupedInvRequest("x.pulse", v, g), schema) {
				t.Fatalf("%s %s: the merge gate refuses a grouped matrix-only request", g.name, v)
			}
		}
	}

	t.Run("DecodeWorkers", func(t *testing.T) {
		dir := t.TempDir()
		osFs := afero.NewOsFs()
		cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
		if err != nil {
			t.Fatalf("fs.New: %v", err)
		}
		n := 25*B + 2777
		if n < parallelDecodeRecordThreshold {
			t.Fatalf("cohort of %d records is below the parallel-decode threshold", n)
		}
		recs, nullAt := groupedInvRows(n, 0)
		path := dir + "/grouped_inv.pulse"
		if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, g := range groupedInvGroupers {
			for _, v := range groupedInvVariants {
				t.Run(g.name+"_"+v.String(), func(t *testing.T) {
					req := groupedInvRequest(path, v, g)
					serial := runGroupedInv(t, cfg, req, g, 1, 0)
					if want := g.common + g.thin; serial.instances != want {
						t.Fatalf("serial fed %d vehicle instances, want %d", serial.instances, want)
					}
					for _, workers := range []int{2, 3, 7, 8} {
						got := runGroupedInv(t, cfg, req, g, workers, 0)
						if want := int64(workers)*g.common + g.thin; got.instances != want {
							t.Fatalf("DecodeWorkers=%d fed %d vehicle instances, want %d — the decode reducer did not engage", workers, got.instances, want)
						}
						assertGroupedInvEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), got, serial)
					}
				})
			}
		}
	})

	t.Run("ShardWorkers", func(t *testing.T) {
		shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "archive.pulse", groupedInvArchive(t, schema, shardRows), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, g := range groupedInvGroupers {
			for _, v := range groupedInvVariants {
				t.Run(g.name+"_"+v.String(), func(t *testing.T) {
					req := groupedInvRequest("archive.pulse", v, g)
					serial := runGroupedInv(t, cfg, req, g, 0, 1)
					for _, workers := range []int{2, 3, 8} {
						got := runGroupedInv(t, cfg, req, g, 0, workers)
						if want := int64(len(shardRows))*g.common + g.thin; got.instances != want {
							t.Fatalf("ShardWorkers=%d fed %d vehicle instances, want %d — the shard reducer did not engage", workers, got.instances, want)
						}
						assertGroupedInvEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), got, serial)
					}
				})
			}
		}
	})
}
