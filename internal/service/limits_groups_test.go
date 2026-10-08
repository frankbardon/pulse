package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// requireMaxGroupsExceeded asserts err is PULSE_LIMIT_EXCEEDED on
// max_groups at configured. Parallel runs compare {code, limit,
// configured} only: their observed figure is the count at breach
// (>= configured+1) and depends on the worker interleaving.
func requireMaxGroupsExceeded(t *testing.T, err error, configured int64) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	if ce.Details["limit"] != string(limits.MaxGroups) || ce.Details["configured"] != configured {
		t.Fatalf("details = %v, want limit=max_groups configured=%d", ce.Details, configured)
	}
	if obs, _ := ce.Details["observed"].(int64); obs <= configured {
		t.Fatalf("observed %d is not past configured %d", obs, configured)
	}
}

func withMaxGroups(n int64) limits.Limits {
	l := limits.Defaults()
	l.MaxGroups = n
	return l
}

// groupsArchive builds a two-shard archive whose shards each carry three
// country keys and whose union carries four: A = {US, CA, MX},
// B = {US, CA, BR}.
func groupsArchive(t *testing.T) (*Service, string) {
	t.Helper()
	cfg := fs.NewMemMap()
	a := writeCategoricalShard(t, []string{"US", "CA", "MX"}, [][2]uint32{{1, 0}, {2, 1}, {3, 2}, {4, 0}})
	b := writeCategoricalShard(t, []string{"US", "CA", "BR"}, [][2]uint32{{5, 0}, {6, 1}, {7, 2}, {8, 1}})
	if err := afero.WriteFile(cfg.Fs(), "a.pulse", a, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(cfg.Fs(), "b.pulse", b, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return svc, "arch.pulse"
}

func countryCount(path string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	}
}

// TestMaxGroups_ParallelShardPartition: one shard's own key count
// breaches MaxGroups inside foldGroupedRow — the partition refuses
// without help from the merge.
func TestMaxGroups_ParallelShardPartition(t *testing.T) {
	svc, path := groupsArchive(t)
	ctx := context.Background()
	cohort, err := svc.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(svc.fs.Fs(), path)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	req := countryCount(path)
	shard := cohort.Shards()[0].Filename

	svc.SetLimits(withMaxGroups(3))
	if p, err := svc.processOneShard(ctx, req, cohort.Schema(), arch, 0, shard); err != nil || len(p.groups) != 3 {
		t.Fatalf("at the limit: partial %v, %v", p, err)
	}
	svc.SetLimits(withMaxGroups(2))
	p, err := svc.processOneShard(ctx, req, cohort.Schema(), arch, 0, shard)
	if p != nil {
		t.Fatalf("a tripped partition returned its partial: %+v", p)
	}
	requireMaxGroupsExceeded(t, err, 2)
}

// TestMaxGroups_ParallelShardRuns: end to end on the per-shard reducer.
// MaxGroups=2 trips inside a partition; MaxGroups=3 is the merge-only
// breach — each shard holds three keys (at the limit), their union
// four — and only mergeShardPartials' merged count can catch it. The
// serial arm (ShardWorkers=1) trips on the same requests.
func TestMaxGroups_ParallelShardRuns(t *testing.T) {
	svc, path := groupsArchive(t)
	ctx := context.Background()
	cohort, err := svc.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	req := countryCount(path)
	if !processing.CanMergeRequest(req, cohort.Schema()) {
		t.Fatal("request is not mergeable; the shard reducer would never fan out")
	}
	for _, workers := range []int{1, 2} {
		svc.SetShardWorkers(workers)
		if workers != 1 {
			if _, ok := svc.shouldFanOut(req, cohort); !ok {
				t.Fatal("shouldFanOut refused 2 workers")
			}
		}
		svc.SetLimits(withMaxGroups(4))
		if resp, err := svc.Process(ctx, req); err != nil || len(resp.Data) != 4 {
			t.Fatalf("workers=%d at the limit: %v, %v", workers, resp, err)
		}
		for _, n := range []int64{2, 3} {
			svc.SetLimits(withMaxGroups(n))
			resp, err := svc.Process(ctx, req)
			if resp != nil {
				t.Fatalf("workers=%d MaxGroups=%d: partial result %+v", workers, n, resp)
			}
			requireMaxGroupsExceeded(t, err, n)
		}
	}
}

// TestMaxGroups_ParallelDecode: the per-segment reducer (single file
// above the parallel-decode threshold) trips on the partition count it
// shares with the shard reducer, with no partial result; the serial
// arm trips identically.
func TestMaxGroups_ParallelDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the parallel-decode limit run in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, Dictionary: d},
	}}
	recs := make([][]uint64, rowCount)
	for i := range recs {
		recs[i] = []uint64{uint64(i), uint64(i % 4)}
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/decode_groups.pulse"
	if err := afero.WriteFile(osFs, path, writePulseFile(t, schema, recs), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	req := countryCount(path)
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("request is not mergeable; the parallel decode reducer would never engage")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	for _, workers := range []int{1, 4} {
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		svc.SetLimits(withMaxGroups(4))
		if resp, err := svc.Process(context.Background(), req); err != nil || len(resp.Data) != 4 {
			t.Fatalf("DecodeWorkers=%d at the limit: %v, %v", workers, resp, err)
		}
		svc.SetLimits(withMaxGroups(3))
		resp, err := svc.Process(context.Background(), req)
		if resp != nil {
			t.Fatalf("DecodeWorkers=%d: partial result %+v", workers, resp)
		}
		requireMaxGroupsExceeded(t, err, 3)
	}
}

// TestMaxGroups_GroupedMatrixRun: a grouped request carrying a matrix
// trips MaxGroups on the serial and both parallel-shard arms.
func TestMaxGroups_GroupedMatrixRun(t *testing.T) {
	svc, path := groupsArchive(t)
	req := countryCount(path)
	req.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"id"}}}
	for _, workers := range []int{1, 2} {
		svc.SetShardWorkers(workers)
		svc.SetLimits(withMaxGroups(4))
		resp, err := svc.Process(context.Background(), req)
		if err != nil || resp.Matrices == nil {
			t.Fatalf("workers=%d at the limit: %v, %v", workers, resp, err)
		}
		svc.SetLimits(withMaxGroups(3))
		resp, err = svc.Process(context.Background(), req)
		if resp != nil {
			t.Fatalf("workers=%d: partial result %+v", workers, resp)
		}
		requireMaxGroupsExceeded(t, err, 3)
	}
}

// TestMaxGroups_Facet: MaxGroups bounds each faceted field's distinct
// values — the rich categorical and boolean accumulators (base and
// additive) and the simple facet's numeric stream. The simple facet's
// categorical fast path returns the resident dictionary and mints
// nothing, so it is not counted.
func TestMaxGroups_Facet(t *testing.T) {
	svc, path := groupsArchive(t)
	ctx := context.Background()
	rich := func(req *types.FacetRequest) error {
		resp, err := svc.FacetSchema(ctx, req)
		if err != nil && resp != nil {
			t.Fatalf("partial facet result %+v", resp)
		}
		return err
	}
	base := &types.FacetRequest{Cohort: &types.Cohort{Filename: path}, Fields: []string{"country"}}
	additive := &types.FacetRequest{Cohort: &types.Cohort{Filename: path}, Fields: []string{"id"}, AdditiveFields: []string{"country"}}

	svc.SetLimits(withMaxGroups(4))
	for _, req := range []*types.FacetRequest{base, additive} {
		if err := rich(req); err != nil {
			t.Fatalf("rich facet at the limit: %v", err)
		}
	}
	svc.SetLimits(withMaxGroups(3))
	requireMaxGroupsExceeded(t, rich(base), 3)
	requireMaxGroupsExceeded(t, rich(additive), 3)

	// Simple facet: the numeric stream (8 distinct ids) is counted, the
	// categorical dictionary is not.
	svc.SetLimits(withMaxGroups(8))
	if vs, err := svc.Facet(ctx, path, "id"); err != nil || len(vs) != 8 {
		t.Fatalf("simple facet at the limit: %v, %v", vs, err)
	}
	svc.SetLimits(withMaxGroups(7))
	vs, err := svc.Facet(ctx, path, "id")
	if vs != nil {
		t.Fatalf("partial simple facet %v", vs)
	}
	requireMaxGroupsExceeded(t, err, 7)
	svc.SetLimits(withMaxGroups(1))
	if vs, err := svc.Facet(ctx, path, "country"); err != nil || len(vs) != 4 {
		t.Fatalf("categorical fast path: %v, %v", vs, err)
	}
}

// TestMaxGroups_FacetBool: the boolean accumulator counts its two
// values against MaxGroups.
func TestMaxGroups_FacetBool(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
	}}
	acc := newBoolAccumulator(&encoding.Field{Name: "flag", Type: encoding.FieldTypePackedBool}, withMaxGroups(1))
	for i, v := range []float64{1, 1, 0} {
		r := processing.NewRecord(schema, map[string]float64{"flag": v})
		err := acc.update(r, "flag")
		if i < 2 && err != nil {
			t.Fatalf("value %d under the limit: %v", i, err)
		}
		if i == 2 {
			requireMaxGroupsExceeded(t, err, 1)
		}
	}
}

// TestMaxGroups_PartitionMatricesCarryLimits: a parallel partition's
// grouped matrix state is built with the limits newShardPartial was
// handed (the reducers bypass newProcessor), so it refuses a mint past
// MaxGroups on its own.
func TestMaxGroups_PartitionMatricesCarryLimits(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0},
	}}
	req := &types.Request{
		Groups:   []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 1}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"x"}}},
	}
	sp := newShardPartial(req, nil, withMaxGroups(1))
	if err := sp.buildMatrices(req, schema, nil, true, processing.FullComputePlan()); err != nil || sp.groupedMats == nil {
		t.Fatalf("buildMatrices: %v (mats %v)", err, sp.groupedMats)
	}
	rec := func(pos int) *processing.Record {
		r := processing.NewRecord(schema, map[string]float64{"x": float64(pos)})
		r.SetMergePosition(0, pos)
		return r
	}
	if err := sp.groupedMats.UpdateRow("a", rec(0)); err != nil {
		t.Fatalf("first bucket: %v", err)
	}
	requireMaxGroupsExceeded(t, sp.groupedMats.UpdateRow("b", rec(1)), 1)
}
