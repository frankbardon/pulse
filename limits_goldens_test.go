package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/types"
)

// exampleHost is the entry an embedded example's body runs through,
// read from its top-level keys: `requests` is a Compose batch,
// `stages` a ProcessChain, `fields` a FacetSchema call, anything else
// a single Request.
type exampleHost string

const (
	hostRequest exampleHost = "request"
	hostCompose exampleHost = "compose"
	hostChain   exampleHost = "chain"
	hostFacet   exampleHost = "facet"
)

func classifyExample(body map[string]json.RawMessage) exampleHost {
	switch {
	case body["requests"] != nil:
		return hostCompose
	case body["stages"] != nil:
		return hostChain
	case body["fields"] != nil:
		return hostFacet
	}
	return hostRequest
}

// rebaseDataDirs rewrites every `data_dir` key anywhere in v — the
// host cohort, each Compose slot's, an overlay's population cohort — to
// dir, so the body resolves against the imported fixtures.
func rebaseDataDirs(v any, dir string) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if k == "data_dir" {
				x[k] = dir
				continue
			}
			rebaseDataDirs(c, dir)
		}
	case []any:
		for _, c := range x {
			rebaseDataDirs(c, dir)
		}
	}
}

// rebasePopulationCohorts rewrites every overlay `population.cohort`
// reference anywhere in v — a bare filename with no data_dir — to its
// path under dir, recording each name in out.
func rebasePopulationCohorts(v any, dir string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if pop, ok := x["population"].(map[string]any); ok {
			if c, ok := pop["cohort"].(string); ok {
				out[c] = true
				pop["cohort"] = filepath.Join(dir, c)
			}
		}
		for _, c := range x {
			rebasePopulationCohorts(c, dir, out)
		}
	case []any:
		for _, c := range x {
			rebasePopulationCohorts(c, dir, out)
		}
	}
}

// seedPopulationCohort makes a FACET-host overlay's comparison cohort
// resolvable: `<fixture>_population.pulse` is a copy of the imported
// `<fixture>.pulse` (the unfiltered population of the same schema).
func seedPopulationCohort(t *testing.T, fs afero.Fs, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if ok, _ := afero.Exists(fs, path); ok {
		return
	}
	base, found := strings.CutSuffix(name, "_population.pulse")
	if !found {
		t.Fatalf("population cohort %q is not a <fixture>_population.pulse name", name)
	}
	data, err := afero.ReadFile(fs, filepath.Join(dir, base+".pulse"))
	if err != nil {
		t.Fatalf("population cohort %q: %v", name, err)
	}
	if err := afero.WriteFile(fs, path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// limitTripIn reports a PULSE_LIMIT_EXCEEDED anywhere in a run's
// outcome: the returned error, or the code anywhere in the marshalled
// response (a Compose slot's or an overlay layer's warnings).
func limitTripIn(t *testing.T, err error, resp any) string {
	t.Helper()
	var ce *errors.CodedError
	if stderrors.As(err, &ce) && ce.Code == errors.PULSE_LIMIT_EXCEEDED {
		return ce.Error()
	}
	if resp == nil {
		return ""
	}
	b, merr := json.Marshal(resp)
	if merr != nil {
		t.Fatalf("marshal response: %v", merr)
	}
	if bytes.Contains(b, []byte(errors.PULSE_LIMIT_EXCEEDED)) {
		return "response carries " + string(errors.PULSE_LIMIT_EXCEEDED)
	}
	return ""
}

// TestLimitsDefaultsNeverTripGoldens is the FR-29 gate: every embedded
// example — every category, including the Compose-, chain- and
// Facet-host overlays and facet/, which TestExamples_RunEndToEnd does
// not drive — runs clean on an instance with the built-in limit
// defaults. Predict reports no LimitFindings for any Request (a single
// Request, every Compose slot, a chain's stage 0), and no run trips a
// limit at runtime. Lowering a default is breaking; this gate is what
// catches it.
func TestLimitsDefaultsNeverTripGoldens(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	want := pulse.Limits{
		RequestTimeout:     pulse.DefaultRequestTimeout,
		MaxGroups:          pulse.DefaultMaxGroups,
		MaxCrosstabCells:   pulse.DefaultMaxCrosstabCells,
		MaxEstimatedMemory: pulse.DefaultMaxEstimatedMemory,
		MaxMatrixDim:       pulse.DefaultMaxMatrixDim,
		MaxComposeSlots:    pulse.DefaultMaxComposeSlots,
		MaxChainStages:     pulse.DefaultMaxChainStages,
		MaxJoinBuildRows:   pulse.DefaultMaxJoinBuildRows,
	}
	if got := p.Limits(); got != want {
		t.Fatalf("effective limits %+v, want the built-in defaults %+v", got, want)
	}
	buildExampleFixtures(t, p, fs, tmp)
	ctx := context.Background()

	noFindings := func(t *testing.T, where string, req *types.Request) {
		t.Helper()
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatalf("%s: Predict: %v", where, err)
		}
		if len(res.LimitFindings) != 0 {
			t.Fatalf("%s: predict reports limit findings under the defaults: %+v", where, res.LimitFindings)
		}
	}

	all := examples.All()
	if len(all) != examples.Count() {
		t.Fatalf("examples.All() = %d, Count() = %d", len(all), examples.Count())
	}
	hosts := map[exampleHost]int{}
	for _, ex := range all {
		t.Run(ex.Category+"/"+ex.Name, func(t *testing.T) {
			var tree any
			if err := json.Unmarshal(ex.Body, &tree); err != nil {
				t.Fatalf("parse body: %v", err)
			}
			rebaseDataDirs(tree, tmp)
			pops := map[string]bool{}
			rebasePopulationCohorts(tree, tmp, pops)
			for name := range pops {
				seedPopulationCohort(t, fs, tmp, name)
			}
			body, err := json.Marshal(tree)
			if err != nil {
				t.Fatal(err)
			}
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(body, &keys); err != nil {
				t.Fatal(err)
			}
			host := classifyExample(keys)
			hosts[host]++

			var resp any
			switch host {
			case hostCompose:
				var req types.ComposedRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse compose: %v", err)
				}
				for i, slot := range req.Requests {
					noFindings(t, "slot "+strconv.Itoa(i), slot)
				}
				resp, err = p.Compose(ctx, &req)
			case hostChain:
				var req types.ChainRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse chain: %v", err)
				}
				stage0 := *req.Stages[0].Request
				stage0.Cohort = req.Cohort
				noFindings(t, "stage 0", &stage0)
				resp, err = p.ProcessChain(ctx, &req)
			case hostFacet:
				var req types.FacetRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse facet: %v", err)
				}
				resp, err = p.FacetSchema(ctx, &req)
			default:
				var req pulse.Request
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("parse request: %v", err)
				}
				noFindings(t, "request", &req)
				resp, err = p.Process(ctx, &req)
			}
			if trip := limitTripIn(t, err, resp); trip != "" {
				t.Fatalf("%s host tripped a limit under the defaults: %s", host, trip)
			}
			if err != nil {
				t.Fatalf("%s host: %v", host, err)
			}
		})
	}
	// Every host the catalog carries must have been driven — a host
	// that silently stopped matching would otherwise drop out.
	for _, h := range []exampleHost{hostRequest, hostCompose, hostChain, hostFacet} {
		if hosts[h] == 0 {
			t.Errorf("no example ran through the %s host", h)
		}
	}
}
