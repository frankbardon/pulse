package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_grouped_test.go is U16 E4-S1: matrices follow Request.Groups —
// one MatrixResult per non-empty bucket, spec-major then bucket, in the
// grouped Data order, each the bucket's own matrix (bit-equal to the
// ungrouped matrix over exactly that bucket's rows), on the streaming,
// buffered, shard and decode paths alike. The exhaustive grouped
// worker-invariance matrix is E4-S2's.

// groupedMatrixSchema: a categorical region (bucket "d" holds a single
// row — the thin bucket), a set-valued tags column (the fan-out
// grouper), three nullable members and a probability weight.
func groupedMatrixSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	region := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	tags := encoding.NewDictionary()
	for _, v := range []string{"T0", "T1", "T2", "T3"} {
		if _, err := tags.Add(v); err != nil {
			t.Fatalf("tags dict.Add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: region},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tags},
		{Name: "x1", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2, Nullable: true},
		{Name: "x2", Type: encoding.FieldTypeF64, ByteOffset: 10, CsvColumnIdx: 3, Nullable: true},
		{Name: "x3", Type: encoding.FieldTypeF64, ByteOffset: 18, CsvColumnIdx: 4, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 26, CsvColumnIdx: 5, Nullable: true},
	}}
}

// groupedMatrixCohort writes n rows spanning several merge blocks:
// regions a / b / c round-robin, except row 5 alone in d; tags a
// non-empty mask fanning into one to four labels; nulls on x1 / x2.
func groupedMatrixCohort(t *testing.T, n int) *fs.Config {
	t.Helper()
	rows := matrixRows(n)
	recs := make([][]uint64, n)
	for i, r := range rows {
		region := uint64(i % 3)
		if i == 5 {
			region = 3
		}
		mask := uint64(i%15) + 1
		recs[i] = []uint64{region, mask, 0, 0, math.Float64bits(r[2]), math.Float64bits(r[4])}
		if !math.IsNaN(r[0]) {
			recs[i][2] = math.Float64bits(r[0])
		}
		if !math.IsNaN(r[1]) {
			recs[i][3] = math.Float64bits(r[1])
		}
	}
	data := writeNullablePulse(t, groupedMatrixSchema(t), recs, func(r, f int) bool {
		return (f == 2 || f == 3) && math.IsNaN(rows[r][f-2])
	})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "g.pulse", data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

// groupedMatrixSpecs: a weighted covariance and a pairwise correlation
// with top_pairs, so per-bucket auxiliary.n, top_pairs and the weighted
// Components floor all ride the comparison.
func groupedMatrixSpecs() []types.MatrixSpec {
	return []types.MatrixSpec{
		{Name: "cov", Type: types.MAT_COVARIANCE, Fields: []string{"x1", "x2", "x3"},
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})},
		{Name: "cor", Type: types.MAT_CORRELATION, Fields: []string{"x1", "x2", "x3"},
			Params: json.RawMessage(`{"missing": "pairwise", "summary": {"top_pairs": 2}}`)},
	}
}

func groupedMatrixRequest(groups ...*types.Group) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "g.pulse"},
		Groups:       groups,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x3", Label: "s"}},
		Matrices:     groupedMatrixSpecs(),
	}
}

// matrixResultWords flattens everything numeric a matrix result
// carries — every primary cell, auxiliary.n, the determinant and the
// top_pairs r values — to bits.
func matrixResultWords(m types.MatrixResult) []uint64 {
	var out []uint64
	for _, row := range m.Primary.Values {
		for _, v := range row {
			out = append(out, math.Float64bits(v))
		}
	}
	if n := m.Auxiliary["n"]; n != nil {
		for _, row := range n.Values {
			for _, v := range row {
				out = append(out, math.Float64bits(v))
			}
		}
	}
	out = append(out, math.Float64bits(m.Scalars["determinant"]))
	if pairs, ok := m.Vectors["top_pairs"].([]types.MatrixPair); ok {
		for _, p := range pairs {
			out = append(out, math.Float64bits(p.R), uint64(p.N))
		}
	}
	return out
}

// dataKeys returns the grouped Data rows' bucket keys in row order.
func dataKeys(t *testing.T, resp *types.Response, field string) []string {
	t.Helper()
	keys := make([]string, len(resp.Data))
	for i, row := range resp.Data {
		k, ok := row[field].(string)
		if !ok {
			t.Fatalf("data row %d: %s = %#v, want a string key", i, field, row[field])
		}
		keys[i] = k
	}
	return keys
}

// assertBucketLayout: len(Matrices) == specs × buckets, spec-major then
// bucket, each result and Components entry keyed by its Data row's key
// and headed by the executed grouper.
func assertBucketLayout(t *testing.T, resp *types.Response, specs []types.MatrixSpec, header types.AxisHeader) []string {
	t.Helper()
	keys := dataKeys(t, resp, header.Fields[0])
	if len(keys) == 0 {
		t.Fatal("no grouped data rows")
	}
	if got, want := len(resp.Matrices), len(specs)*len(keys); got != want {
		t.Fatalf("%d matrices, want %d (specs %d × buckets %d)", got, want, len(specs), len(keys))
	}
	if resp.Components == nil || len(resp.Components.Matrices) != len(resp.Matrices) {
		t.Fatalf("Components.Matrices does not carry one entry per result")
	}
	for i, spec := range specs {
		for k, key := range keys {
			at := i*len(keys) + k
			m, c := resp.Matrices[at], resp.Components.Matrices[at]
			if m.Name != spec.Name || c.Name != spec.Name {
				t.Errorf("result %d: name %q / components %q, want %q (spec-major)", at, m.Name, c.Name, spec.Name)
			}
			if !reflect.DeepEqual(m.GroupKey, types.AxisKey{key}) || !reflect.DeepEqual(c.GroupKey, types.AxisKey{key}) {
				t.Errorf("result %d (%s): group_key %v / components %v, want [%s] (Data order)", at, spec.Name, m.GroupKey, c.GroupKey, key)
			}
			if m.GroupHeader == nil || !reflect.DeepEqual(*m.GroupHeader, header) {
				t.Errorf("result %d: group_header %+v, want %+v", at, m.GroupHeader, header)
			}
		}
	}
	return keys
}

// TestMatrixGrouped_OneResultPerBucket: a grouped request returns one
// matrix per non-empty bucket in Data order, spec-major; each bucket's
// matrix is bit-equal to the ungrouped matrix over exactly its rows
// (FILTER_INCLUDE on its key) — the per-bucket merge tree over the
// bucket's own blocks; the thin bucket is emitted with null cells and
// PULSE_MATRIX_INSUFFICIENT_N; an ungrouped result omits group_key /
// group_header on the wire.
func TestMatrixGrouped_OneResultPerBucket(t *testing.T) {
	cfg := groupedMatrixCohort(t, 3*linalg.MergeBlockSize+700)
	specs := groupedMatrixSpecs()
	resp := processMatrices2(t, cfg, groupedMatrixRequest(&types.Group{Type: types.GROUP_CATEGORY, Field: "region"}))
	keys := assertBucketLayout(t, resp, specs, types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}})
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("bucket keys %v, want %v", keys, want)
	}

	for i, spec := range specs {
		for k, key := range keys {
			got := resp.Matrices[i*len(keys)+k]
			filtered := &types.Request{
				Cohort:    &types.Cohort{Filename: "g.pulse"},
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{key}}},
				Matrices:  []types.MatrixSpec{spec},
			}
			wantResp := processMatrices2(t, cfg, filtered)
			want := wantResp.Matrices[0]
			if w, g := matrixResultWords(want), matrixResultWords(got); !reflect.DeepEqual(w, g) {
				t.Errorf("%s bucket %s: differs from the ungrouped matrix over the bucket's rows", spec.Name, key)
			}
			if !reflect.DeepEqual(got.Warnings, want.Warnings) {
				t.Errorf("%s bucket %s: warnings %v, want %v", spec.Name, key, got.Warnings, want.Warnings)
			}
			gc, wc := resp.Components.Matrices[i*len(keys)+k], wantResp.Components.Matrices[0]
			gc.GroupKey = nil
			if !reflect.DeepEqual(gc, wc) {
				t.Errorf("%s bucket %s: components %s, want %s", spec.Name, key, mustJSON(t, gc), mustJSON(t, wc))
			}
		}
	}

	// The thin bucket (one row) is emitted for every spec with
	// PULSE_MATRIX_INSUFFICIENT_N; its correlation cells are null and
	// render as null on the wire. (A weighted covariance over one row
	// is the degenerate 0 / (W − ddof), finite when W > 1 — the E3
	// finalizer's semantics, flagged by the same warning.)
	for i, name := range []string{"cov", "cor"} {
		thin := resp.Matrices[i*len(keys)+3]
		if thin.Name != name || !reflect.DeepEqual(thin.GroupKey, types.AxisKey{"d"}) {
			t.Fatalf("result %d = %s %v, want %s [d]", i*len(keys)+3, thin.Name, thin.GroupKey, name)
		}
		if findWarning(thin, errors.PULSE_MATRIX_INSUFFICIENT_N) == nil {
			t.Errorf("%s thin bucket carries no PULSE_MATRIX_INSUFFICIENT_N (warnings %v)", name, matWarningCodes(thin))
		}
	}
	thin := resp.Matrices[len(keys)+3]
	for _, row := range thin.Primary.Values {
		for _, v := range row {
			if !math.IsNaN(v) {
				t.Errorf("thin bucket correlation cell %v, want null", v)
			}
		}
	}
	if b, _ := json.Marshal(thin); !strings.Contains(string(b), `"values":[[null,null,null],[null,null,null],[null,null,null]]`) {
		t.Errorf("thin bucket cells do not render null: %s", b)
	}
	if b, _ := json.Marshal(thin); !strings.Contains(string(b), `"group_key":["d"]`) || !strings.Contains(string(b), `"group_header":{"fields":["region"],"types":["GROUP_CATEGORY"]}`) {
		t.Errorf("grouped wire form lacks group_key / group_header: %s", b)
	}

	ungrouped := processMatrices2(t, cfg, groupedMatrixRequest())
	if len(ungrouped.Matrices) != len(specs) {
		t.Fatalf("ungrouped: %d matrices, want %d", len(ungrouped.Matrices), len(specs))
	}
	for _, m := range ungrouped.Matrices {
		b, _ := json.Marshal(m)
		if strings.Contains(string(b), "group_key") || strings.Contains(string(b), "group_header") {
			t.Errorf("ungrouped %s carries a group key on the wire: %s", m.Name, b)
		}
	}
	b, _ := json.Marshal(ungrouped.Components.Matrices)
	if strings.Contains(string(b), "group_key") {
		t.Errorf("ungrouped Components.Matrices carry a group key: %s", b)
	}
}

// processMatrices2 runs req and checks each spec produced a result.
func processMatrices2(t *testing.T, cfg *fs.Config, req *types.Request) *types.Response {
	t.Helper()
	resp, err := New(cfg).Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Matrices) < len(req.Matrices) {
		t.Fatalf("Process: %d matrices for %d specs", len(resp.Matrices), len(req.Matrices))
	}
	return resp
}

// TestMatrixGrouped_BucketOrderFollowsData: Group.Include ordering, a
// fan-out grouper (GROUP_SET_PER_ELEMENT — one row in several buckets)
// and a multi-entry Groups request (the engine executes Groups[0]; the
// matrices key off exactly what the Data rows do) all emit the matrix
// buckets in the Data rows' order with their keys.
func TestMatrixGrouped_BucketOrderFollowsData(t *testing.T) {
	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	specs := groupedMatrixSpecs()
	region := types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}}
	cases := []struct {
		name   string
		groups []*types.Group
		header types.AxisHeader
		want   []string
	}{
		{"include order", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"c", "a", "d"}}}, region, []string{"c", "a", "d"}},
		{"fan-out", []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			types.AxisHeader{Fields: []string{"tags"}, Types: []string{"GROUP_SET_PER_ELEMENT"}}, []string{"T0", "T1", "T2", "T3"}},
		{"multi-entry groups", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"b", "a"}}, {Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}}, region, []string{"b", "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := processMatrices2(t, cfg, groupedMatrixRequest(c.groups...))
			keys := assertBucketLayout(t, resp, specs, c.header)
			if !reflect.DeepEqual(keys, c.want) {
				t.Fatalf("Data keys %v, want %v", keys, c.want)
			}
		})
	}

	// A fan-out bucket is the matrix over every row selecting that label
	// — FILTER_SET_CONTAINS_ANY on it.
	resp := processMatrices2(t, cfg, groupedMatrixRequest(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}))
	for k, label := range []string{"T0", "T1", "T2", "T3"} {
		want := processMatrices2(t, cfg, &types.Request{
			Cohort:    &types.Cohort{Filename: "g.pulse"},
			Filterers: []*types.Filterer{{Type: types.FILTER_SET_CONTAINS_ANY, Field: "tags", Values: []string{label}}},
			Matrices:  specs[:1],
		}).Matrices[0]
		if !reflect.DeepEqual(matrixResultWords(resp.Matrices[k]), matrixResultWords(want)) {
			t.Errorf("fan-out bucket %s differs from the matrix over its rows", label)
		}
	}
}

// TestMatrixGrouped_BucketOrderFollowsSort: an explicit Request.Sort
// reorders the per-bucket matrices with the Data rows — spec-major, each
// spec's buckets in the sorted rows' key order, Components.Matrices
// alike, ties in the stable order window.Sort gives the rows — on the
// streaming and the buffered grouped arms. Only positions move: every
// bucket's matrix, warnings and Components entry equal the unsorted
// run's for the same key. (The DecodeWorkers / ShardWorkers arms ride
// the sort variants of TestMatrixGrouped_WorkerInvariant; Compose and
// chain stage 0, TestMatrices_ComposeAndChainStageZero.)
func TestMatrixGrouped_BucketOrderFollowsSort(t *testing.T) {
	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	schema := groupedMatrixSchema(t)
	specs := groupedMatrixSpecs()
	region := types.AxisHeader{Fields: []string{"region"}, Types: []string{"GROUP_CATEGORY"}}
	// 8223 rows round-robin over a / b / c with row 5 moved to d:
	// counts a 2741, b 2741, c 2740, d 1 — a and b tie.
	cases := []struct {
		name string
		sort []types.OrderKey
		want []string
	}{
		{"count asc (tie keeps a before b)", []types.OrderKey{{Field: "n"}}, []string{"d", "c", "a", "b"}},
		{"count desc (tie keeps a before b)", []types.OrderKey{{Field: "n", Desc: true}}, []string{"a", "b", "c", "d"}},
		{"group key desc", []types.OrderKey{{Field: "region", Desc: true}}, []string{"d", "c", "b", "a"}},
		{"count asc then key desc", []types.OrderKey{{Field: "n"}, {Field: "region", Desc: true}}, []string{"d", "c", "b", "a"}},
	}
	arms := []struct {
		name   string
		aggs   []*types.Aggregation
		stream bool
	}{
		{"streaming", []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x3", Label: "n"}}, true},
		{"buffered", []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x3", Label: "n"}, {Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}, false},
	}
	for _, arm := range arms {
		base := groupedMatrixRequest(&types.Group{Type: types.GROUP_CATEGORY, Field: "region"})
		base.Aggregations = arm.aggs
		if got := processing.CanStreamRequest(base, schema); got != arm.stream {
			t.Fatalf("%s: CanStreamRequest = %v — the arm is not exercised", arm.name, got)
		}
		unsorted := processMatrices2(t, cfg, base)
		unsortedKeys := assertBucketLayout(t, unsorted, specs, region)
		at := make(map[string]int, len(unsortedKeys))
		for k, key := range unsortedKeys {
			at[key] = k
		}
		for _, c := range cases {
			t.Run(arm.name+"/"+c.name, func(t *testing.T) {
				req := groupedMatrixRequest(&types.Group{Type: types.GROUP_CATEGORY, Field: "region"})
				req.Aggregations = arm.aggs
				req.Sort = c.sort
				resp := processMatrices2(t, cfg, req)
				keys := assertBucketLayout(t, resp, specs, region)
				if !reflect.DeepEqual(keys, c.want) {
					t.Fatalf("sorted Data keys %v, want %v", keys, c.want)
				}
				for i := range specs {
					for k, key := range keys {
						got, gc := resp.Matrices[i*len(keys)+k], resp.Components.Matrices[i*len(keys)+k]
						u := i*len(unsortedKeys) + at[key]
						want, wc := unsorted.Matrices[u], unsorted.Components.Matrices[u]
						if !reflect.DeepEqual(matrixResultWords(got), matrixResultWords(want)) || !reflect.DeepEqual(got.Warnings, want.Warnings) {
							t.Errorf("%s bucket %s: differs from the unsorted run's matrix for the same bucket", got.Name, key)
						}
						if !reflect.DeepEqual(gc, wc) {
							t.Errorf("%s bucket %s: components %s, unsorted %s", got.Name, key, mustJSON(t, gc), mustJSON(t, wc))
						}
					}
				}
			})
		}
	}
}

// TestMatrixGrouped_StreamingEqualsBuffered: the grouped streaming path
// and the grouped buffered path (an AGG_MEDIAN forces it) return the
// same per-bucket matrices bit for bit, with the same warnings and
// Components entries — and a matrix-only grouped request streams too.
func TestMatrixGrouped_StreamingEqualsBuffered(t *testing.T) {
	cfg := groupedMatrixCohort(t, 3*linalg.MergeBlockSize+9)
	schema := groupedMatrixSchema(t)
	for _, grp := range []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "region"},
		{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"},
	} {
		t.Run(string(grp.Type), func(t *testing.T) {
			streamReq := groupedMatrixRequest(grp)
			streamReq.Aggregations = nil
			bufReq := groupedMatrixRequest(grp)
			bufReq.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
			if !processing.CanStreamRequest(streamReq, schema) {
				t.Fatal("grouped matrix-only request does not stream")
			}
			if processing.CanStreamRequest(bufReq, schema) {
				t.Fatal("AGG_MEDIAN request streams; the buffered arm is not exercised")
			}
			streamed := processMatrices2(t, cfg, streamReq)
			buffered := processMatrices2(t, cfg, bufReq)
			if len(streamed.Matrices) != len(buffered.Matrices) || len(streamed.Matrices) == 0 {
				t.Fatalf("streamed %d matrices, buffered %d", len(streamed.Matrices), len(buffered.Matrices))
			}
			for i := range streamed.Matrices {
				a, b := streamed.Matrices[i], buffered.Matrices[i]
				if !reflect.DeepEqual(a.GroupKey, b.GroupKey) || a.Name != b.Name {
					t.Fatalf("result %d: streamed %s %v, buffered %s %v", i, a.Name, a.GroupKey, b.Name, b.GroupKey)
				}
				if !reflect.DeepEqual(matrixResultWords(a), matrixResultWords(b)) {
					t.Errorf("result %d (%s %v): streamed and buffered differ", i, a.Name, a.GroupKey)
				}
				if !reflect.DeepEqual(a.Warnings, b.Warnings) {
					t.Errorf("result %d: warnings %v vs %v", i, a.Warnings, b.Warnings)
				}
			}
			if !reflect.DeepEqual(streamed.Components.Matrices, buffered.Components.Matrices) {
				t.Errorf("Components.Matrices: streamed %s, buffered %s", mustJSON(t, streamed.Components.Matrices), mustJSON(t, buffered.Components.Matrices))
			}
		})
	}
}

// groupedInvarianceRequest is a grouped matrixInvarianceRequest: the
// coMoment cohort bucketed by GROUP_RANGE over x3 (never null), beside
// the vehicle aggregation whose instance count proves the parallel arm
// ran.
func groupedInvarianceRequest(path string, v matrixInvarianceVariant) *types.Request {
	req := matrixInvarianceRequest(path, v)
	req.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "x3", Interval: 250000}}
	return req
}

// groupedRun captures every per-bucket figure a reducer could get
// wrong.
type groupedRun struct {
	keys      []any
	words     [][]uint64
	warnings  [][]*types.ResponseWarning
	comps     []types.MatrixComponents
	instances int64
}

func runGroupedInvariance(t *testing.T, cfg *fs.Config, req *types.Request, decodeWorkers, shardWorkers int) groupedRun {
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
	if len(resp.Data) < 2 || len(resp.Matrices) != len(resp.Data) {
		t.Fatalf("Process(decode=%d, shard=%d): %d matrices over %d buckets", decodeWorkers, shardWorkers, len(resp.Matrices), len(resp.Data))
	}
	out := groupedRun{comps: resp.Components.Matrices, instances: stats.instancesWithRows.Load()}
	for _, m := range resp.Matrices {
		out.keys = append(out.keys, m.GroupKey...)
		out.words = append(out.words, matrixResultWords(m))
		out.warnings = append(out.warnings, m.Warnings)
	}
	return out
}

func assertGroupedRunsEqual(t *testing.T, label string, got, want groupedRun) {
	t.Helper()
	if !reflect.DeepEqual(got.keys, want.keys) {
		t.Fatalf("%s: bucket keys %v, serial %v", label, got.keys, want.keys)
	}
	for i := range want.words {
		if !reflect.DeepEqual(got.words[i], want.words[i]) {
			t.Errorf("%s: bucket %v differs from serial", label, want.keys[i])
		}
	}
	if !reflect.DeepEqual(got.warnings, want.warnings) {
		t.Errorf("%s: warnings differ from serial", label)
	}
	if !reflect.DeepEqual(got.comps, want.comps) {
		t.Errorf("%s: Components.Matrices %s, serial %s", label, mustJSON(t, got.comps), mustJSON(t, want.comps))
	}
}

// TestMatrixGrouped_ParallelArms: the merge gate admits a grouped
// request carrying matrices, and the shard and decode reducers return
// the serial per-bucket matrices bit for bit (E4-S2 carries the full
// grouped invariance matrix; this pins the wiring).
func TestMatrixGrouped_ParallelArms(t *testing.T) {
	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	v := matrixInvarianceVariant{weighted: true, withVehicle: true, typ: types.MAT_CORRELATION}
	if !processing.CanMergeRequest(groupedInvarianceRequest("x.pulse", matrixInvarianceVariant{}), schema) {
		t.Fatal("the merge gate refuses a grouped matrix-only request")
	}

	t.Run("ShardWorkers", func(t *testing.T) {
		cfg := fs.NewMemMap()
		shardRows := []int{B + 3, 700, 2*B + 1, 999}
		if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, pv := range []matrixInvarianceVariant{v, {pairwise: true, withVehicle: true, typ: types.MAT_COVARIANCE}} {
			req := groupedInvarianceRequest("archive.pulse", pv)
			serial := runGroupedInvariance(t, cfg, req, 0, 1)
			for _, workers := range []int{2, 3} {
				got := runGroupedInvariance(t, cfg, req, 0, workers)
				if got.instances <= serial.instances {
					t.Fatalf("ShardWorkers=%d fed %d vehicle instances, serial %d — the shard reducer did not engage", workers, got.instances, serial.instances)
				}
				assertGroupedRunsEqual(t, fmt.Sprintf("%s ShardWorkers=%d", pv, workers), got, serial)
			}
		}
	})

	t.Run("DecodeWorkers", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping the 100K-record grouped decode arm in -short mode")
		}
		dir := t.TempDir()
		osFs := afero.NewOsFs()
		cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
		if err != nil {
			t.Fatalf("fs.New: %v", err)
		}
		n := 25*B + 2777
		recs, nullAt := coMomentRows(n, 0)
		path := dir + "/grouped.pulse"
		if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		req := groupedInvarianceRequest(path, v)
		serial := runGroupedInvariance(t, cfg, req, 1, 0)
		for _, workers := range []int{3, 8} {
			got := runGroupedInvariance(t, cfg, req, workers, 0)
			if got.instances <= serial.instances {
				t.Fatalf("DecodeWorkers=%d fed %d vehicle instances, serial %d — the decode reducer did not engage", workers, got.instances, serial.instances)
			}
			assertGroupedRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), got, serial)
		}
	})
}
