package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// population_loader_test.go pins the instance's registered populations
// (E2-S4): Options.PopulationsDir / PULSE_POPULATIONS_DIR and
// Options.Extensions.Populations, frozen at pulse.New, the
// programmatic + disk collision PULSE_EXTENSION_DUPLICATE, every
// request seeing them bound (bit-identical to a `populations` load),
// predict judging the same names and labels, and the manifest listing
// them.

func writePopulationDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func popCodedErr(t *testing.T, err error, code perr.Code) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return ce
}

// TestLoadPopulations_Dir: every population file in the directory is
// registered under its stem; every other file (a label table, Pulse's
// own sidecars, a model file) is skipped unread, and the walk is not
// recursive.
func TestLoadPopulations_Dir(t *testing.T) {
	dir := writePopulationDir(t, map[string]string{
		"brand" + model.PopulationSuffix:       populationFileFixture,
		"country.json":                         `{"US": "United States"}`,
		"c.pulse.spss.json":                    "{nope",
		"m" + model.FileSuffix:                 modelFileFixture,
		"notes.txt":                            "x",
		"nested/deep" + model.PopulationSuffix: populationFileFixture,
		"broken.pulse-population.json.bak":     "{nope",
		"cohort.pulse":                         "",
	})
	opts := Options{PopulationsDir: dir}
	if err := loadPopulationsFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if len(opts.Extensions.Populations) != 1 {
		t.Fatalf("registered %v, want brand only", opts.Extensions.Populations)
	}
	if got := opts.Extensions.Populations["brand"].File; string(got) != populationFileFixture {
		t.Fatalf("brand bytes %q", got)
	}
}

// TestLoadPopulations_EnvAndPrecedence: the env var is honoured when the
// option is empty; the option beats it; no dir, a missing dir are
// no-ops; a file is not a directory.
func TestLoadPopulations_EnvAndPrecedence(t *testing.T) {
	envDir := writePopulationDir(t, map[string]string{"env" + model.PopulationSuffix: populationFileFixture})
	optDir := writePopulationDir(t, map[string]string{"opt" + model.PopulationSuffix: populationFileFixture})
	t.Setenv(envPopulationsDir, envDir)
	opts := Options{}
	if err := loadPopulationsFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Extensions.Populations["env"]; !ok || len(opts.Extensions.Populations) != 1 {
		t.Fatalf("env load: %v", opts.Extensions.Populations)
	}
	opts = Options{PopulationsDir: optDir}
	if err := loadPopulationsFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Extensions.Populations["opt"]; !ok || len(opts.Extensions.Populations) != 1 {
		t.Fatalf("option did not beat the env var: %v", opts.Extensions.Populations)
	}
	t.Setenv(envPopulationsDir, "")
	opts = Options{}
	if err := loadPopulationsFromDir(&opts); err != nil || opts.Extensions.Populations != nil {
		t.Fatalf("no dir: %v %v", err, opts.Extensions.Populations)
	}
	opts = Options{PopulationsDir: filepath.Join(optDir, "absent")}
	if err := loadPopulationsFromDir(&opts); err != nil || opts.Extensions.Populations != nil {
		t.Fatalf("missing dir: %v %v", err, opts.Extensions.Populations)
	}
	opts = Options{PopulationsDir: filepath.Join(optDir, "opt"+model.PopulationSuffix)}
	if err := loadPopulationsFromDir(&opts); err == nil {
		t.Fatal("a file accepted as the populations dir")
	}
}

// TestLoadPopulations_Refusals: a population file that does not decode
// fails with its path and registered name; a name declared
// programmatically too is PULSE_EXTENSION_DUPLICATE — through
// pulse.New alike.
func TestLoadPopulations_Refusals(t *testing.T) {
	bad := writePopulationDir(t, map[string]string{"bad" + model.PopulationSuffix: "{nope"})
	opts := Options{PopulationsDir: bad}
	ce := popCodedErr(t, loadPopulationsFromDir(&opts), perr.PULSE_MODEL_FILE_INVALID)
	if ce.Details["registered"] != "bad" || ce.Details["path"] != filepath.Join(bad, "bad"+model.PopulationSuffix) || ce.Details["reason"] != model.ReasonFileJSON {
		t.Fatalf("invalid details %+v", ce.Details)
	}
	v9 := writePopulationDir(t, map[string]string{"v9" + model.PopulationSuffix: `{"population_format_version": 9}`})
	_, err := New(Options{FS: afero.NewMemMapFs(), PopulationsDir: v9})
	if ce := popCodedErr(t, err, perr.PULSE_MODEL_VERSION_UNSUPPORTED); ce.Details["registered"] != "v9" {
		t.Fatalf("version details %+v", ce.Details)
	}

	dir := writePopulationDir(t, map[string]string{"brand" + model.PopulationSuffix: populationFileFixture})
	ext := Extensions{Populations: map[string]Population{"brand": {File: []byte(populationFileFixture)}}}
	opts = Options{PopulationsDir: dir, Extensions: ext}
	path := filepath.Join(dir, "brand"+model.PopulationSuffix)
	assertDirCollision(t, loadPopulationsFromDir(&opts), "population", "brand", path)
	_, err = New(Options{FS: afero.NewMemMapFs(), PopulationsDir: dir, Extensions: ext})
	assertDirCollision(t, err, "population", "brand", path)
}

// TestDecodeRegisteredPopulations: programmatic entries decode at New; a
// blank name, a malformed document and a future version are refused,
// located by details.registered.
func TestDecodeRegisteredPopulations(t *testing.T) {
	cases := []struct {
		name string
		pops map[string]Population
		code perr.Code
		reg  string
	}{
		{"blank name", map[string]Population{" ": {File: []byte(populationFileFixture)}}, perr.PULSE_MODEL_REF_INVALID, " "},
		{"malformed", map[string]Population{"ok": {File: []byte(populationFileFixture)}, "x": {File: []byte(`{"population_format_version": 1}`)}}, perr.PULSE_MODEL_FILE_INVALID, "x"},
		{"version", map[string]Population{"v": {File: []byte(`{"population_format_version": 2}`)}}, perr.PULSE_MODEL_VERSION_UNSUPPORTED, "v"},
		{"empty", map[string]Population{"e": {}}, perr.PULSE_MODEL_FILE_INVALID, "e"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{Populations: tc.pops}})
			if ce := popCodedErr(t, err, tc.code); ce.Details["registered"] != tc.reg {
				t.Fatalf("details %+v, want registered %q", ce.Details, tc.reg)
			}
		})
	}
	got, err := decodeRegisteredPopulations(map[string]Population{"brand": {File: []byte(populationFileFixture)}})
	if err != nil || got["brand"] == nil || !reflect.DeepEqual(got["brand"].Labels, []string{"q1", "q2"}) {
		t.Fatalf("decode: %v %+v", err, got)
	}
}

// registeredInstance is an instance over fsys registering the saved
// facade population under "brand" (programmatically, or from a
// directory when viaDir).
func registeredInstance(t *testing.T, fsys afero.Fs, viaDir bool, extra ...func(*Options)) *Pulse {
	t.Helper()
	raw, err := afero.ReadFile(fsys, facadePopulation)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{FS: fsys}
	if viaDir {
		opts.PopulationsDir = writePopulationDir(t, map[string]string{"brand" + model.PopulationSuffix: string(raw)})
	} else {
		opts.Extensions.Populations = map[string]Population{"brand": {File: raw}}
	}
	for _, f := range extra {
		f(&opts)
	}
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func responseBytes(t *testing.T, resp any) []byte {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRegisteredPopulation_ScoresLikeALoad: a request binding the
// registered population with params.fit alone — no `populations` entry
// — is byte-identical to the same request loading the file, on Process
// (a fitted attribute and a MAT_* slot), through a Compose slot and a
// chain stage; registered from Extensions or from the directory alike;
// predict is clean.
func TestRegisteredPopulation_ScoresLikeALoad(t *testing.T) {
	fsys := populationFacadeFS(t)
	ctx := context.Background()
	plain, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	loadedMatrix := vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"brand"}`)
	loadedMatrix.Populations = []types.PopulationRef{popRef("brand", facadePopulation)}
	cases := []struct {
		name           string
		loaded, viaReg func() *Request
	}{
		{"attribute", func() *Request { return populationScoring(popRef("brand", facadePopulation)) }, func() *Request { return populationScoring() }},
		{"matrix", func() *Request { return loadedMatrix }, func() *Request { return vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"brand"}`) }},
	}
	for _, viaDir := range []bool{false, true} {
		p := registeredInstance(t, fsys, viaDir)
		for _, tc := range cases {
			want, err := plain.Process(ctx, tc.loaded())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Process(ctx, tc.viaReg())
			if err != nil {
				t.Fatalf("%s (dir %v): %v", tc.name, viaDir, err)
			}
			if !bytes.Equal(responseBytes(t, got), responseBytes(t, want)) {
				t.Fatalf("%s (dir %v): registered differs from a load\n got %s\nwant %s", tc.name, viaDir, responseBytes(t, got), responseBytes(t, want))
			}
			env, err := p.PredictBytes(ctx, cohort, tc.viaReg())
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("%s: predict %v %+v", tc.name, err, env)
			}
			// Without the registration the same request is unknown.
			if _, err := plain.Process(ctx, tc.viaReg()); err == nil {
				t.Fatalf("%s: vacuous — an unregistered instance binds brand", tc.name)
			}
		}
		// A Compose slot and a ProcessChain stage see it too.
		wantC, err := plain.Compose(ctx, &ComposedRequest{Requests: []*Request{populationScoring(popRef("brand", facadePopulation))}})
		if err != nil {
			t.Fatal(err)
		}
		gotC, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{populationScoring()}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(responseBytes(t, gotC), responseBytes(t, wantC)) {
			t.Fatal("compose slot: registered differs from a load")
		}
		if env, err := p.PredictCompose(ctx, &ComposedRequest{Requests: []*Request{populationScoring()}}); err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict compose: %v %+v", err, env)
		}
		mkChain := func(pops ...types.PopulationRef) *ChainRequest {
			s0 := vecFacadeMatrix(types.MAT_COVARIANCE, `{"fit":"brand"}`)
			s0.Populations = pops
			s0.Cohort = nil
			s0.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}
			s1 := &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "rows"}}}
			return &ChainRequest{
				Cohort: &types.Cohort{Filename: vecFacadeCohort},
				Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: s1}},
			}
		}
		wantCh, err := plain.ProcessChain(ctx, mkChain(popRef("brand", facadePopulation)))
		if err != nil {
			t.Fatal(err)
		}
		gotCh, err := p.ProcessChain(ctx, mkChain())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(responseBytes(t, gotCh), responseBytes(t, wantCh)) {
			t.Fatal("chain stage: registered differs from a load")
		}
		if env, err := p.PredictChain(ctx, mkChain()); err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict chain: %v %+v", err, env)
		}
	}
}

// TestRegisteredPopulation_PredictRuntimeParity: a request entry, a
// Compose fit or slot entry, or a chain stage entry rebinding the
// registered name is PULSE_MODEL_NAME_COLLISION, and a slot whose
// members differ from the registered labels is
// PULSE_VECTOR_REFERENCE_MISMATCH — predict refusing each exactly as the
// runtime does.
func TestRegisteredPopulation_PredictRuntimeParity(t *testing.T) {
	fsys := populationFacadeFS(t)
	p := registeredInstance(t, fsys, false)
	ctx := context.Background()
	cohort, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	twoMember := populationScoring()
	twoMember.Attributes[0].Fields = []string{"a", "b"}
	for _, tc := range []struct {
		name string
		req  *Request
		code perr.Code
	}{
		{"request rebinds", populationScoring(popRef("brand", facadePopulation)), perr.PULSE_MODEL_NAME_COLLISION},
		{"labels differ", twoMember, perr.PULSE_VECTOR_REFERENCE_MISMATCH},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictBytes(ctx, cohort, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, tc.code)
			_, rerr := p.Process(ctx, tc.req)
			assertSameRefusal(t, "Process", rerr, pe)
		})
	}
	fit := vecFacadeMatrix(types.MAT_CORRELATION, "")
	for _, tc := range []struct {
		name string
		c    *ComposedRequest
		at   string
	}{
		{"fit named brand", &ComposedRequest{Fits: []types.FitSpec{{Name: "brand", Request: fit}}, Requests: []*Request{vecFacadeMatrix(types.MAT_COVARIANCE, "")}}, "fit"},
		{"slot rebinds", &ComposedRequest{Requests: []*Request{populationScoring(popRef("brand", facadePopulation))}}, "request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.c)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, perr.PULSE_MODEL_NAME_COLLISION)
			if _, ok := pe.details[tc.at]; !ok {
				t.Fatalf("details %+v lack %s", pe.details, tc.at)
			}
			if src, _ := pe.details["sources"].([]any); len(src) != 2 || !reflect.DeepEqual(src[0], map[string]any{"slot": model.SlotRegistered}) {
				t.Fatalf("sources %+v", pe.details["sources"])
			}
			_, rerr := p.Compose(ctx, tc.c)
			assertSameRefusal(t, "Compose", rerr, pe)
			_, rerr = p.ComposeParallel(ctx, tc.c, ComposeOptions{MaxWorkers: 2})
			assertSameRefusal(t, "ComposeParallel", rerr, pe)
		})
	}
	s0 := vecFacadeRequest("")
	s0.Cohort = nil
	s0.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}}
	s1 := &Request{Populations: []types.PopulationRef{popRef("brand", facadePopulation)}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "rows"}}}
	chain := &ChainRequest{
		Cohort: &types.Cohort{Filename: vecFacadeCohort},
		Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: s1}},
	}
	env, err := p.PredictChain(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PULSE_MODEL_NAME_COLLISION)
	if pe.details["stage"] != 1 {
		t.Fatalf("chain details %+v", pe.details)
	}
	_, rerr := p.ProcessChain(ctx, chain)
	assertSameRefusal(t, "ProcessChain", rerr, pe)
}

// TestRegisteredPopulation_Manifest: the manifest lists each registered
// population's name, battery, basis, labels and n; an instance without
// any lists none; a feature profile hiding capability:models registers
// none.
func TestRegisteredPopulation_Manifest(t *testing.T) {
	fsys := populationFacadeFS(t)
	ctx := context.Background()
	p := registeredInstance(t, fsys, true)
	got := p.Manifest(ctx).Extensions.Populations
	f, err := model.DecodePopulationFile("x", mustRead(t, fsys, facadePopulation))
	if err != nil {
		t.Fatal(err)
	}
	want := []descriptor.PopulationMeta{{Name: "brand", Operator: f.Operator, Basis: string(f.Basis), Labels: f.Labels, N: f.N}}
	if !reflect.DeepEqual(got, want) || len(f.Labels) == 0 {
		t.Fatalf("manifest populations %+v, want %+v", got, want)
	}
	plain, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Manifest(ctx).Extensions.Populations; got == nil || len(got) != 0 {
		t.Fatalf("unregistered manifest populations %#v, want []", got)
	}
	feats := []string{"capability:process", string(types.AGG_SUM), string(types.ATTR_MAHALANOBIS)}
	h := registeredInstance(t, fsys, false, func(o *Options) {
		o.FeatureProfile = &FeatureProfile{Profile: "no-models", Features: feats}
	})
	if got := h.Manifest(ctx).Extensions.Populations; len(got) != 0 {
		t.Fatalf("hidden capability:models still lists %+v", got)
	}
	if h.svc.ExtensionsSnapshot().PopulationCatalog() != nil {
		t.Fatal("hidden capability:models still binds registered populations in predict")
	}
}

func mustRead(t *testing.T, fsys afero.Fs, path string) []byte {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
