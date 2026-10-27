package pulse_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// archiveTwin imports two cohorts of rows[0] and rows[1] records (numeric
// a, b, c and a categorical cat over x / y / z), packs them into the
// shard archive "arch.pulse", and imports the same records as ONE
// single file — the archive's twin. It returns the twin's path.
func archiveTwin(t *testing.T, fs afero.Fs, rows [2]int) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	line := func(b *strings.Builder, i int) {
		fmt.Fprintf(b, "%d,%d,%d,%s\n", i%200, (i*7)%13, 40-i%40, []string{"x", "y", "z"}[i%3])
	}
	var all strings.Builder
	all.WriteString("a,b,c,cat\n")
	var paths []string
	next := 0
	for s, n := range rows {
		var b strings.Builder
		b.WriteString("a,b,c,cat\n")
		for range n {
			line(&b, next)
			line(&all, next)
			next++
		}
		name := fmt.Sprintf("shard%d.csv", s)
		if err := afero.WriteFile(fs, name, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name})
		if err != nil {
			t.Fatalf("ImportFile: %v", err)
		}
		paths = append(paths, res.Path)
	}
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if err := afero.WriteFile(fs, "twin.csv", []byte(all.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "twin.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return res.Path
}

func archiveFacet(cohort string) *pulse.FacetRequest {
	return &pulse.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat", "a"}}
}

// TestPredictRoots_ArchiveMatchesSingleFile: PredictFacet and
// PredictChain judge a shard archive through its canonical schema and
// per-shard header counts, as Predict does — the verdict and schema of
// the archive equal its single-file twin's (they used to read the zip
// bytes as a .pulse header and refuse ENCODING_INVALID). Under a
// memory limit each root's certain finding over the archive is the
// error its run refuses with, the archive terms (central directory,
// largest shard) charged alike.
func TestPredictRoots_ArchiveMatchesSingleFile(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	twin := archiveTwin(t, fs, [2]int{40, 25})
	ctx := context.Background()
	p := limitsPulse(t, fs, pulse.Limits{})

	facet := func(path string) *pulse.FacetPredictResult {
		t.Helper()
		env, err := p.PredictFacet(ctx, archiveFacet(path))
		if err != nil {
			t.Fatalf("PredictFacet(%s): %v", path, err)
		}
		if len(env.Errors) > 0 {
			t.Fatalf("PredictFacet(%s) errors: %+v", path, env.Errors)
		}
		return env.Data.(*pulse.FacetPredictResult)
	}
	chain := func(path string) *pulse.ChainPredictResult {
		t.Helper()
		env, err := p.PredictChain(ctx, chainOf(path, 2))
		if err != nil {
			t.Fatalf("PredictChain(%s): %v", path, err)
		}
		if len(env.Errors) > 0 {
			t.Fatalf("PredictChain(%s) errors: %+v", path, env.Errors)
		}
		return env.Data.(*pulse.ChainPredictResult)
	}
	if a, s := facet("arch.pulse"), facet(twin); !a.Valid || !reflect.DeepEqual(a.SchemaInfo, s.SchemaInfo) {
		t.Fatalf("facet: archive valid=%v schema=%+v, twin schema=%+v", a.Valid, a.SchemaInfo, s.SchemaInfo)
	}
	if a, s := chain("arch.pulse"), chain(twin); !a.Valid || !reflect.DeepEqual(a.SchemaInfo, s.SchemaInfo) || !reflect.DeepEqual(a.StageSchemas, s.StageSchemas) {
		t.Fatalf("chain: archive valid=%v schema=%+v stages=%+v, twin %+v %+v", a.Valid, a.SchemaInfo, a.StageSchemas, s.SchemaInfo, s.StageSchemas)
	}

	p = limitsPulse(t, fs, pulse.Limits{MaxEstimatedMemory: 1})
	env, err := p.PredictFacet(ctx, archiveFacet("arch.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	_, perr := p.FacetSchema(ctx, archiveFacet("arch.pulse"))
	d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
	if fs := env.Data.(*pulse.FacetPredictResult).LimitFindings; len(fs) != 1 || d["observed"] != fs[0].Estimated {
		t.Fatalf("facet: runtime observed %v, predict findings %+v", d["observed"], fs)
	}
	sameEntry(t, env, perr)

	env, err = p.PredictChain(ctx, chainOf("arch.pulse", 2))
	if err != nil {
		t.Fatal(err)
	}
	_, perr = p.ProcessChain(ctx, chainOf("arch.pulse", 2))
	d = requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
	if fs := env.Data.(*pulse.ChainPredictResult).LimitFindings; len(fs) != 1 || d["observed"] != fs[0].Estimated {
		t.Fatalf("chain: runtime observed %v, predict findings %+v", d["observed"], fs)
	}
	sameEntry(t, env, perr)
}

// TestArchivePredict_ReadsHeadersOnly (#248): every no-execute surface
// over a shard archive reads the zip central directory, the canonical
// schema entry and each shard's header — never a shard's records, never
// the archive twice over. An anchored `arch.pulse#shard` reads the
// directory and that one entry's header. PredictBytes (bytes in hand)
// still judges the archive.
func TestArchivePredict_ReadsHeadersOnly(t *testing.T) {
	t.Parallel()

	mem := afero.NewMemMapFs()
	archiveTwin(t, mem, [2]int{50_000, 50_000})
	fi, err := mem.Stat("arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	size := fi.Size()
	cfs := newReadCountingFs(mem)
	p, err := pulse.New(pulse.Options{FS: cfs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	shards, err := p.ListShards(ctx, "arch.pulse")
	if err != nil || len(shards) != 2 {
		t.Fatalf("ListShards: %v %+v", err, shards)
	}
	anchor := "arch.pulse#" + shards[1].Filename
	req := func(path string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "a", Label: "m"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		}
	}
	type op struct {
		name string
		run  func(path string) error
	}
	ops := []op{
		{"Predict", func(path string) error {
			res, err := p.Predict(ctx, req(path))
			if err == nil && !res.Valid {
				err = fmt.Errorf("invalid: %+v", res.Errors)
			}
			return err
		}},
		{"InspectEnvelope", func(path string) error {
			env, err := p.InspectEnvelope(ctx, path, nil)
			if err == nil && len(env.Errors) > 0 {
				err = fmt.Errorf("errors: %+v", env.Errors)
			}
			return err
		}},
		{"Recommend", func(path string) error {
			res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: "describe", Cohort: &types.Cohort{Filename: path}})
			if err == nil && len(res.Recommendations) == 0 {
				err = fmt.Errorf("no recommendation survived predict")
			}
			return err
		}},
		{"Explain", func(path string) error {
			_, err := p.Explain(ctx, descriptor.ExplainRequest{Request: req(path)})
			return err
		}},
		{"PredictFacet", func(path string) error {
			env, err := p.PredictFacet(ctx, archiveFacet(path))
			if err == nil && len(env.Errors) > 0 {
				err = fmt.Errorf("errors: %+v", env.Errors)
			}
			return err
		}},
		{"PredictChain", func(path string) error {
			env, err := p.PredictChain(ctx, chainOf(path, 2))
			if err == nil && len(env.Errors) > 0 {
				err = fmt.Errorf("errors: %+v", env.Errors)
			}
			return err
		}},
	}
	// A shard's records are ~half the archive; a header-only read of
	// the whole archive is a few KB.
	budget := size / 64
	for _, o := range ops {
		for _, path := range []string{"arch.pulse", anchor} {
			cfs.reset()
			if err := o.run(path); err != nil {
				t.Fatalf("%s(%s): %v", o.name, path, err)
			}
			got := cfs.read("arch.pulse")
			t.Logf("%s(%s): read %d of %d archive bytes", o.name, path, got, size)
			if got > budget {
				t.Errorf("%s(%s) read %d of the archive's %d bytes, want <= %d (central directory and headers only)", o.name, path, got, size, budget)
			}
		}
	}

	whole, err := afero.ReadFile(mem, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.PredictBytes(ctx, whole, req("arch.pulse"))
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictBytes: %v %+v", err, env)
	}
	if got := env.Data.(*descriptor.PredictResult).RecordCount; got != 100_000 {
		t.Fatalf("PredictBytes record count = %d, want 100000", got)
	}
}
