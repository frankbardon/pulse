package integration_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
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
	// hostMaterialize is a request carrying the materialize-only
	// `output` slot: it runs through Pulse.Materialize, which every other
	// host refuses it (PULSE_MATERIALIZE_OUTPUT_ONLY).
	hostMaterialize exampleHost = "materialize"
)

func classifyExample(body map[string]json.RawMessage) exampleHost {
	switch {
	case body["requests"] != nil, body["sweep"] != nil:
		// A Compose batch may carry only a sweep (compose/08).
		return hostCompose
	case body["stages"] != nil:
		return hostChain
	case body["fields"] != nil:
		return hostFacet
	case body["output"] != nil:
		return hostMaterialize
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

// rebaseModelFiles rewrites every model / population file path in v —
// a fit's or regression's `save`, and each `models[].path` /
// `populations[].path` — to its path under dir. The test instance's
// filesystem root is the OS root, so a relative path would read from
// (or, for a save, write into) the working directory.
func rebaseModelFiles(v any, dir string) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if p, ok := c.(string); ok && k == "save" && p != "" && !filepath.IsAbs(p) {
				x[k] = filepath.Join(dir, p)
				continue
			}
			if refs, ok := c.([]any); ok && (k == "populations" || k == "models") {
				for _, r := range refs {
					if m, ok := r.(map[string]any); ok {
						if p, ok := m["path"].(string); ok && p != "" && !filepath.IsAbs(p) {
							m["path"] = filepath.Join(dir, p)
						}
					}
				}
			}
			rebaseModelFiles(c, dir)
		}
	case []any:
		for _, c := range x {
			rebaseModelFiles(c, dir)
		}
	}
}

// examplesDataDir is the data directory every example names: a
// cohort's `data_dir` and the prefix of an overlay's
// `population.cohort` path (which takes no data_dir of its own and
// resolves from the working directory, so the examples spell it
// `.data/<fixture>.pulse`).
const examplesDataDir = ".data"

// rebasePopulationCohorts rewrites every overlay `population.cohort`
// path anywhere in v from the examples' data directory to dir,
// recording each rewritten path in out.
func rebasePopulationCohorts(v any, dir string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		if pop, ok := x["population"].(map[string]any); ok {
			if c, ok := pop["cohort"].(string); ok {
				p := filepath.Join(dir, strings.TrimPrefix(filepath.ToSlash(c), examplesDataDir+"/"))
				out[p] = true
				pop["cohort"] = p
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

// requirePopulationCohort fails unless a FACET-host overlay's
// comparison cohort, rebased by rebasePopulationCohorts, is one of the
// imported fixtures: an example naming a cohort build.sh never writes
// would otherwise fail only for a reader.
func requirePopulationCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	if ok, _ := afero.Exists(fs, path); !ok {
		t.Fatalf("population cohort %s is not a built fixture", path)
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
