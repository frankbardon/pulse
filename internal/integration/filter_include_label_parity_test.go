package integration_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// filter_include_label_parity_test.go pins E1-S10: a FILTER_INCLUDE /
// FILTER_EXCLUDE value naming a label a categorical field's dictionary
// does not hold (or a value that does not parse on a numeric field) was
// accepted by predict and refused PROCESSING_CONFIG by the runtime
// constructor. Both sides now call valuematch.FilterKeys over the full
// schema dictionary — on a shard archive the canonical one, the union of
// every shard's.

func TestFilterIncludeLabel_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	req := func(ft types.FiltererType, field string, values ...string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort},
			Filterers:    []*types.Filterer{{Type: ft, Field: field, Values: values}},
			Aggregations: countAgg()}
	}
	refused := []struct {
		name string
		req  *types.Request
	}{
		{"include unknown label", req(types.FILTER_INCLUDE, "cat", "a", "zz")},
		{"exclude unknown label", req(types.FILTER_EXCLUDE, "cat", "zz")},
		{"include unparseable number", req(types.FILTER_INCLUDE, "n", "abc")},
		{"exclude unparseable number", req(types.FILTER_EXCLUDE, "n", "1", "x")},
	}
	for _, c := range refused {
		_, err := p.Process(ctx, c.req)
		requireCode(t, err, errors.PROCESSING_CONFIG)
		env := predictEnvelope(t, p, fs, cohort, c.req)
		if len(env.Errors) != 1 {
			t.Fatalf("%s: predict errors = %+v, want exactly one", c.name, env.Errors)
		}
		sameEntry(t, env, err)
	}
	ce := requireCode(t, func() error { _, err := p.Process(ctx, req(types.FILTER_INCLUDE, "cat", "zz")); return err }(), errors.PROCESSING_CONFIG)
	if ce.Details["field"] != "cat" || ce.Details["value"] != "zz" || ce.Details["filterer"] != string(types.FILTER_INCLUDE) {
		t.Fatalf("details = %v, want filterer/field/value", ce.Details)
	}

	for _, ok := range []*types.Request{
		req(types.FILTER_INCLUDE, "cat", "a", "c"),
		req(types.FILTER_EXCLUDE, "cat", "b"),
		req(types.FILTER_INCLUDE, "n", "3", "4"),
	} {
		if _, err := p.Process(ctx, ok); err != nil {
			t.Fatalf("runtime refused known values %v: %v", ok.Filterers[0].Values, err)
		}
		if env := predictEnvelope(t, p, fs, cohort, ok); len(env.Errors) != 0 {
			t.Fatalf("predict refused known values %v: %+v", ok.Filterers[0].Values, env.Errors)
		}
	}
}

// A label only one shard's dictionary carries is known: the archive's
// canonical schema holds the union, and predict reads that schema.
func TestFilterIncludeLabel_ShardArchive_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	imp := func(stem, body string) string {
		if err := afero.WriteFile(fs, stem+".csv", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: stem + ".csv"})
		if err != nil {
			t.Fatalf("ImportFile %s: %v", stem, err)
		}
		return res.Path
	}
	a := imp("lab_a", "cat,n\na,1\nb,2\na,3\n")
	b := imp("lab_b", "cat,n\nonly_b,4\nb,5\n")
	const archive = "lab_archive.pulse"
	if _, err := p.CreateShardArchive(ctx, archive, []string{a, b}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	req := func(values ...string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: archive},
			Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: values}},
			Aggregations: countAgg()}
	}

	resp, err := p.Process(ctx, req("only_b"))
	if err != nil {
		t.Fatalf("runtime refused a second-shard label: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0]["count"] != float64(1) {
		t.Fatalf("data = %v, want count 1", resp.Data)
	}
	env, err := p.PredictEnvelope(ctx, req("only_b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused a second-shard label: %+v", env.Errors)
	}

	_, rerr := p.Process(ctx, req("zz"))
	requireCode(t, rerr, errors.PROCESSING_CONFIG)
	env, err = p.PredictEnvelope(ctx, req("zz"))
	if err != nil {
		t.Fatal(err)
	}
	sameEntry(t, env, rerr)
}

// Facet's base filterers and a later chain stage: the facet refuses an
// unknown label on both sides; a chain stage's synthesised categorical
// column carries an empty dictionary the runtime fills from the previous
// stage's rows, so predict leaves its labels to the runtime.
func TestFilterIncludeLabel_FacetAndChain_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()

	facet := func(v string) *types.FacetRequest {
		return &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"n"},
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{v}}}}
	}
	_, ferr := p.FacetSchema(ctx, facet("zz"))
	requireCode(t, ferr, errors.PROCESSING_CONFIG)
	fenv, err := p.PredictFacet(ctx, facet("zz"))
	if err != nil {
		t.Fatal(err)
	}
	sameEntry(t, fenv, ferr)
	if fenv, err := p.PredictFacet(ctx, facet("a")); err != nil || len(fenv.Errors) != 0 {
		t.Fatalf("predict facet refused a known label: %v %+v", err, fenv.Errors)
	}

	chain := func(v string) *types.ChainRequest {
		return &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort}, Stages: []*types.ChainStage{
			{Request: &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}}}},
			{Request: &types.Request{Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{v}}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "sum"}}}},
		}}
	}
	if _, err := p.ProcessChain(ctx, chain("a")); err != nil {
		t.Fatalf("runtime chain refused a known stage-1 label: %v", err)
	}
	cenv, err := p.PredictChain(ctx, chain("a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cenv.Errors) != 0 {
		t.Fatalf("predict chain refused a stage-1 label the runtime accepts: %+v", cenv.Errors)
	}
}
