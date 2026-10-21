package pulse_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// recordRegion is a single-file cohort's records: the bytes after its
// header and schema block.
func recordRegion(t *testing.T, data []byte) []byte {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoding.ReadSchema(r, v); err != nil {
		t.Fatal(err)
	}
	return data[len(data)-r.Len():]
}

// TestFilterToFile_FlattenMatchesSingleFileTwin (U51 E4-S3, FR-37): the
// library Flatten option writes an archive's rows, in archive order,
// into one single-file cohort whose records are byte-equal to the
// single-file twin imported from the same rows; and a filtered flatten
// answers exactly as Process over the archive with the same filter.
func TestFilterToFile_FlattenMatchesSingleFileTwin(t *testing.T) {
	fs := afero.NewMemMapFs()
	twin := archiveTwin(t, fs, [2]int{40, 25})
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := p.FilterToFileWithOptions(ctx, "arch.pulse", "flat.pulse", pulse.FilterToFileOptions{Flatten: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Records != 65 || !rep.Flattened || rep.Output != "flat.pulse" {
		t.Fatalf("report = %+v, want 65 flattened records at flat.pulse", rep)
	}
	flat, _ := afero.ReadFile(fs, "flat.pulse")
	tw, _ := afero.ReadFile(fs, twin)
	if !bytes.Equal(recordRegion(t, flat), recordRegion(t, tw)) {
		t.Fatal("flattened records differ from the single-file twin's")
	}

	const expr = `cat == "y" || a > 30`
	if _, err := p.FilterToFileWithOptions(ctx, "arch.pulse", "some.pulse", pulse.FilterToFileOptions{Expression: expr, Flatten: true}); err != nil {
		t.Fatal(err)
	}
	req := func(cohort string, filter bool) *types.Request {
		r := &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}, {Type: types.AGG_SUM, Field: "b", Label: "s"}},
		}
		if filter {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
		}
		return r
	}
	got, err := p.Process(ctx, req("some.pulse", false))
	if err != nil {
		t.Fatal(err)
	}
	want, err := p.Process(ctx, req("arch.pulse", true))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Data, want.Data) {
		t.Fatalf("filtered flatten %v != Process over the filtered archive %v", got.Data, want.Data)
	}
}

// TestPredictRoots_ChainAcceptsArchive (U51 FR-40, TODO #360): stage 0
// of PredictChain reads a shard archive — valid, the same verdict and
// stage schemas as its single-file twin and as its flattened copy — and
// ProcessChain runs it with the answer of the twin (predict ≡ runtime).
func TestPredictRoots_ChainAcceptsArchive(t *testing.T) {
	fs := afero.NewMemMapFs()
	twin := archiveTwin(t, fs, [2]int{40, 25})
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FilterToFileWithOptions(ctx, "arch.pulse", "flat.pulse", pulse.FilterToFileOptions{Flatten: true}); err != nil {
		t.Fatal(err)
	}
	predict := func(path string) *pulse.ChainPredictResult {
		t.Helper()
		env, err := p.PredictChain(ctx, chainOf(path, 2))
		if err != nil {
			t.Fatalf("PredictChain(%s): %v", path, err)
		}
		if len(env.Errors) > 0 {
			t.Fatalf("PredictChain(%s) refused: %+v", path, env.Errors)
		}
		return env.Data.(*pulse.ChainPredictResult)
	}
	arch := predict("arch.pulse")
	if !arch.Valid {
		t.Fatalf("PredictChain over an archive is not valid: %+v", arch)
	}
	for _, other := range []string{twin, "flat.pulse"} {
		o := predict(other)
		if !reflect.DeepEqual(arch.SchemaInfo, o.SchemaInfo) || !reflect.DeepEqual(arch.StageSchemas, o.StageSchemas) {
			t.Fatalf("archive schema %+v stages %+v, %s %+v %+v", arch.SchemaInfo, arch.StageSchemas, other, o.SchemaInfo, o.StageSchemas)
		}
	}
	run := func(path string) any {
		t.Helper()
		res, err := p.ProcessChain(ctx, chainOf(path, 2))
		if err != nil {
			t.Fatalf("ProcessChain(%s): %v", path, err)
		}
		return res.Final.Data
	}
	if a, w := run("arch.pulse"), run(twin); !reflect.DeepEqual(a, w) {
		t.Fatalf("ProcessChain over the archive %v != twin %v", a, w)
	}
}
