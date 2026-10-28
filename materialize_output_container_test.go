package pulse

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The output slot's container keys (types.MaterializeOutput.Shards /
// ElideConstants): a shard archive output and constant elision, each
// decoding exactly as the single-file, non-elided twin.

// shardSizes returns the record count of every shard of the archive at
// path, in archive order.
func shardSizes(t *testing.T, p *Pulse, path string) []int64 {
	t.Helper()
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open %s: %v", path, err)
	}
	var sizes []int64
	for _, s := range c.Shards() {
		sizes = append(sizes, s.RecordCount)
	}
	return sizes
}

// TestMaterializeOutput_ShardsRoundTrip: output.shards writes a shard
// archive pulse.Open reads, cut into consecutive max_records-row shards
// (the last one partial) at every boundary — one row per shard, one
// short of the total, exactly the total, past it — whose rows, in order,
// equal the single-file output's cell for cell.
func TestMaterializeOutput_ShardsRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	if _, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{}), "flat.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	flatSchema, flatRows := readAll(t, p, "flat.pulse")

	for _, tc := range []struct {
		max   int
		sizes []int64
	}{
		{1, []int64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}},
		{5, []int64{5, 5, 2}},
		{11, []int64{11, 1}},
		{12, []int64{12}},
		{13, []int64{12}},
	} {
		out := "arch-" + string(rune('a'+tc.max)) + ".pulse"
		res, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{
			Shards: &types.MaterializeShards{MaxRecords: tc.max},
		}), out, MaterializeOptions{})
		if err != nil {
			t.Fatalf("max_records %d: %v", tc.max, err)
		}
		if len(res.Shards) != len(tc.sizes) || res.Shards[0] != "part-00001.pulse" {
			t.Errorf("max_records %d: Shards = %v, want %d part-NNNNN entries", tc.max, res.Shards, len(tc.sizes))
		}
		if res.Records != 12 {
			t.Errorf("max_records %d: Records = %d, want 12", tc.max, res.Records)
		}
		if got := shardSizes(t, p, out); !slices.Equal(got, tc.sizes) {
			t.Errorf("max_records %d: shard sizes = %v, want %v", tc.max, got, tc.sizes)
		}
		schema, rows := readAll(t, p, out)
		if !slices.Equal(fieldNames(schema), fieldNames(flatSchema)) {
			t.Errorf("max_records %d: fields = %v, want %v", tc.max, fieldNames(schema), fieldNames(flatSchema))
		}
		if !reflect.DeepEqual(rows, flatRows) {
			t.Errorf("max_records %d: rows differ from the single-file output\n got %v\nwant %v", tc.max, rows, flatRows)
		}
	}
}

// constantRequest keeps the north region only, so region and price hold
// one value on every written row (as does tags: every north row holds
// {a, c}).
func constantRequest(out *types.MaterializeOutput) *Request {
	req := matOutputReq(out)
	req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}
	return req
}

// TestMaterializeOutput_ElideConstantsDecodesIdentically: elide_constants
// stores the columns constant over the written rows once — a smaller
// 0x02 file naming them in ElidedConstants — and decodes to the
// non-elided twin's schema and rows exactly; with shards the elision is
// decided over the whole archive and every shard decodes the same.
func TestMaterializeOutput_ElideConstantsDecodesIdentically(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	plain, err := p.Materialize(ctx, constantRequest(&types.MaterializeOutput{}), "plain.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.ElidedConstants) != 0 {
		t.Fatalf("ElidedConstants without the key = %v", plain.ElidedConstants)
	}
	elided, err := p.Materialize(ctx, constantRequest(&types.MaterializeOutput{ElideConstants: true}), "elided.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"region", "price", "tags"}; !slices.Equal(elided.ElidedConstants, want) {
		t.Fatalf("ElidedConstants = %v, want %v", elided.ElidedConstants, want)
	}
	plainRaw, _ := afero.ReadFile(fsys, "plain.pulse")
	elidedRaw, _ := afero.ReadFile(fsys, "elided.pulse")
	if len(elidedRaw) >= len(plainRaw) {
		t.Errorf("elided file %d bytes, want fewer than the plain %d", len(elidedRaw), len(plainRaw))
	}
	if v := elidedRaw[len(encoding.MagicBytes)]; v != encoding.FormatVersionV2 {
		t.Errorf("elided format version %#x, want 0x02", v)
	}
	if v := plainRaw[len(encoding.MagicBytes)]; v != encoding.FormatVersionV1 {
		t.Errorf("plain format version %#x, want 0x01", v)
	}
	plainSchema, plainRows := readAll(t, p, "plain.pulse")
	elidedSchema, elidedRows := readAll(t, p, "elided.pulse")
	if !slices.Equal(fieldNames(elidedSchema), fieldNames(plainSchema)) {
		t.Errorf("elided fields = %v, want %v", fieldNames(elidedSchema), fieldNames(plainSchema))
	}
	if !reflect.DeepEqual(elidedRows, plainRows) {
		t.Errorf("elided rows differ\n got %v\nwant %v", elidedRows, plainRows)
	}

	arch, err := p.Materialize(ctx, constantRequest(&types.MaterializeOutput{
		ElideConstants: true, Shards: &types.MaterializeShards{MaxRecords: 3},
	}), "elided-arch.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(arch.ElidedConstants, elided.ElidedConstants) || len(arch.Shards) != 2 {
		t.Errorf("sharded elision: ElidedConstants %v, Shards %v", arch.ElidedConstants, arch.Shards)
	}
	_, archRows := readAll(t, p, "elided-arch.pulse")
	if !reflect.DeepEqual(archRows, plainRows) {
		t.Errorf("sharded elided rows differ\n got %v\nwant %v", archRows, plainRows)
	}
}

// TestMaterializeOutput_ShardsMaxRecordsRefused: max_records ≤ 0 is a
// static PULSE_MATERIALIZE_OUTPUT_INVALID (reason shards_max_records)
// before anything is read — nothing at out — and predict refuses it with
// the same code and details.
func TestMaterializeOutput_ShardsMaxRecordsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	for _, n := range []int{0, -3} {
		req := matOutputReq(&types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: n}})
		_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
		ce := assertCode(t, err, perrors.PULSE_MATERIALIZE_OUTPUT_INVALID)
		if ce.Details["reason"] != "shards_max_records" || ce.Details["path"] != "output.shards.max_records" || ce.Details["max_records"] != n {
			t.Errorf("max_records %d: details = %v", n, ce.Details)
		}
		assertAbsent(t, fsys, "out.pulse")

		env, err := p.PredictMaterialize(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
			t.Errorf("max_records %d: predict errors = %+v, want the run's %s %v", n, env.Errors, ce.Code, ce.Details)
		}
	}
}

// TestMaterializeOutput_ShardsFailureLeavesNothing: a sharded run that
// fails mid-scan (a nullable: false column meeting a null) leaves no
// archive, shard or staging directory at out, and an existing cohort
// there keeps its bytes.
func TestMaterializeOutput_ShardsFailureLeavesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	bad := matOutputReq(&types.MaterializeOutput{
		Shards:  &types.MaterializeShards{MaxRecords: 2},
		Columns: []types.MaterializeOutputColumn{{From: "id"}, {From: "spend", Nullable: boolPtr(false)}},
	})
	_, err := p.Materialize(ctx, bad, "out.pulse", MaterializeOptions{})
	assertCode(t, err, perrors.PULSE_MATERIALIZE_OUTPUT_NULL)
	assertAbsent(t, fsys, "out.pulse")

	if _, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{}), "keep.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "keep.pulse")
	_, err = p.Materialize(ctx, bad, "keep.pulse", MaterializeOptions{Overwrite: true})
	assertCode(t, err, perrors.PULSE_MATERIALIZE_OUTPUT_NULL)
	if after, _ := afero.ReadFile(fsys, "keep.pulse"); !bytes.Equal(before, after) {
		t.Error("a failed sharded overwrite changed the existing cohort")
	}
}

// TestMaterializeOutput_ShardsOverwriteReplacesArchive: Overwrite
// replaces an existing archive with the new one, whole.
func TestMaterializeOutput_ShardsOverwriteReplacesArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	if _, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: 2}}), "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: 2}}), "out.pulse", MaterializeOptions{}); err == nil {
		t.Fatal("an existing archive was replaced without Overwrite")
	}
	res, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: 5}}), "out.pulse", MaterializeOptions{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := shardSizes(t, p, "out.pulse"); !slices.Equal(got, []int64{5, 5, 2}) || len(res.Shards) != 3 {
		t.Errorf("overwritten archive shard sizes = %v (Shards %v), want [5 5 2]", got, res.Shards)
	}
}

// TestMaterializeOutput_ShardCountLimit: a row that would open shard
// 65,536 fails the run with PULSE_SHARD_COUNT_LIMIT, nothing written;
// predict flags the same code as a risk over that source.
func TestMaterializeOutput_ShardCountLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	b, err := p.NewCohortBuilder(ctx, "big.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeU8, Description: "A small counter value per row"},
	}}, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1<<16 + 1 {
		if err := b.Append(CohortRow{uint64(i % 7)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	req := &Request{Cohort: &types.Cohort{Filename: "big.pulse"},
		Output: &types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: 1}}}

	env, err := p.PredictMaterialize(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*descriptor.MaterializePredictResult)
	if len(res.Risks) != 1 || res.Risks[0].Code != string(perrors.PULSE_SHARD_COUNT_LIMIT) {
		t.Errorf("predict risks = %+v, want one PULSE_SHARD_COUNT_LIMIT", res.Risks)
	}

	_, err = p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	ce := assertCode(t, err, perrors.PULSE_SHARD_COUNT_LIMIT)
	if ce.Details["row"] != int64(65535) || ce.Details["limit"] != 65535 {
		t.Errorf("details = %v", ce.Details)
	}
	assertAbsent(t, fsys, "out.pulse")
}

// TestMaterializeOutput_PredictContainerPlan: predict reports the
// container — a file without the keys, a shard archive with its row
// split and the most shards the run writes (equal to the run's shard
// count when every record passes), the elision flag — and flags no
// shard-limit risk under the limit.
func TestMaterializeOutput_PredictContainerPlan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	plan := func(out *types.MaterializeOutput) *descriptor.MaterializePredictResult {
		t.Helper()
		env, err := p.PredictMaterialize(ctx, matOutputReq(out))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Fatalf("predict errors %v", env.Errors)
		}
		return env.Data.(*descriptor.MaterializePredictResult)
	}

	if got := plan(nil).Container; got == nil || *got != (descriptor.MaterializeContainer{Kind: "file"}) {
		t.Errorf("absent output container = %+v, want a file", got)
	}
	out := &types.MaterializeOutput{Shards: &types.MaterializeShards{MaxRecords: 5}, ElideConstants: true}
	pr := plan(out)
	c := pr.Container
	if c == nil || c.Kind != "shard_archive" || c.MaxRecords != 5 || c.MaxShards == nil || *c.MaxShards != 3 || !c.ElideConstants {
		t.Fatalf("container = %+v, want a 3-shard archive of 5 rows, elided", c)
	}
	if len(pr.Risks) != 0 {
		t.Errorf("risks = %+v, want none", pr.Risks)
	}
	res, err := p.Materialize(ctx, matOutputReq(out), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(res.Shards)) != *c.MaxShards {
		t.Errorf("run wrote %d shards, predict said %d", len(res.Shards), *c.MaxShards)
	}
}

// constantSavSpec is labelledSavSpec over 40 cases plus WAVE, one value
// on every case, so elide_constants stores it once.
func constantSavSpec() spsstest.Spec {
	spec := labelledSavSpec()
	spec.Vars = append(spec.Vars, spsstest.Var{Name: "WAVE", Label: "Survey wave", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}})
	base := spec.Cases
	spec.Cases = nil
	for i := range 40 {
		c := slices.Clone(base[i%len(base)])
		c[0] = spsstest.Num(float64(i + 1))
		spec.Cases = append(spec.Cases, append(c, spsstest.Num(3)))
	}
	return spec
}

// TestMaterializeOutput_ContainerCarriesSidecar: the source's SPSS
// sidecar is carried onto a container-shaped output exactly as onto a
// single file — beside an archive, fingerprinted over the archive's
// bytes (fresh, its weight suggested by Inspect), and beside an elided
// cohort, every variable kept (an elided column decodes unchanged) and
// round-tripping through .sav with its value labels.
func TestMaterializeOutput_ContainerCarriesSidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	afs := afero.NewMemMapFs()
	p := importSav(t, afs, constantSavSpec(), Options{})
	want := []string{"ID", "Q1", "WT", "WAVE", "Q1_1", "Q1_2", "rn", "prev_wt"}

	arch, err := p.Materialize(ctx, withOutput(absentOutputRequest(), &types.MaterializeOutput{
		Shards: &types.MaterializeShards{MaxRecords: 16},
	}), "derived-arch.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(arch.Warnings) != 0 || len(arch.Shards) != 3 {
		t.Fatalf("archive run: warnings %v, shards %v", arch.Warnings, arch.Shards)
	}
	sc, err := spss.LoadSidecar(afs, "derived-arch.pulse", spss.WriterOptions{})
	if err != nil || sc == nil || sc.Document == nil {
		t.Fatalf("no fresh sidecar beside the archive: %v", err)
	}
	var got []string
	for _, v := range sc.Document.Payload.Variables {
		got = append(got, v.Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("archive sidecar variables = %v, want %v", got, want)
	}
	insp, err := p.Inspect(ctx, "derived-arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if insp.SuggestedWeight == nil || insp.SuggestedWeight.Field != "WT" {
		t.Errorf("Inspect of the archive suggests %+v, want WT from the carried sidecar", insp.SuggestedWeight)
	}

	el, err := p.Materialize(ctx, withOutput(absentOutputRequest(), &types.MaterializeOutput{ElideConstants: true}), "derived-el.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(el.Warnings) != 0 || !slices.Contains(el.ElidedConstants, "WAVE") {
		t.Fatalf("elided run: warnings %v, elided %v", el.Warnings, el.ElidedConstants)
	}
	names, labels := savRoundTrip(t, p, afs, "derived-el.pulse")
	if !slices.Equal(names, want) {
		t.Errorf("exported variables = %v, want %v", names, want)
	}
	if q1 := labels["Q1"]; !reflect.DeepEqual(q1, map[float64]string{1: "Low", 2: "High"}) {
		t.Errorf("Q1 value labels after the round trip = %v", q1)
	}
}

// withOutput returns req carrying out.
func withOutput(req *Request, out *types.MaterializeOutput) *Request {
	req.Output = out
	return req
}
