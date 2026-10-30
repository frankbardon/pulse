package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// paramsSlotTypes is the slot object each params category's Slot-marked
// declarations must be a json field of (descriptor.Param.Slot).
var paramsSlotTypes = map[paramread.Category]reflect.Type{
	paramread.Aggregator: reflect.TypeFor[types.Aggregation](),
	paramread.Attribute:  reflect.TypeFor[types.Attribute](),
	paramread.Filterer:   reflect.TypeFor[types.Filterer](),
	paramread.Grouper:    reflect.TypeFor[types.Group](),
	paramread.Window:     reflect.TypeFor[types.Window](),
	paramread.Feature:    reflect.TypeFor[types.Feature](),
	paramread.Test:       reflect.TypeFor[types.Test](),
	paramread.PostTest:   reflect.TypeFor[types.Test](),
	paramread.Matrix:     reflect.TypeFor[types.MatrixSpec](),
	paramread.Overlay:    reflect.TypeFor[types.OverlaySpec](),
	paramread.Regression: reflect.TypeFor[types.RegressionSpec](),
}

// jsonFieldOf reports whether name is a json key of struct t.
func jsonFieldOf(t reflect.Type, name string) bool {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == name || (tag == "" && f.Name == name) {
			return true
		}
	}
	return false
}

// runAllExamples runs every embedded example (synth specs aside)
// through its root's predict and execution entry points, its cohort
// data_dir and any save path rebased under dataDir. Faults are the
// examples suites' business — except an unknown params key, which
// fails here: an example the allowlist refuses never reaches the
// reads this gate records.
func runAllExamples(t *testing.T, p *pulse.Pulse, dataDir string) {
	t.Helper()
	ctx := context.Background()
	matches, err := filepath.Glob(filepath.Join("internal", "examples", "*", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	refusedForParams := func(path string, err error) {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) && ce.Code == errors.PULSE_REQUEST_UNKNOWN_FIELD {
			t.Errorf("%s: refused: %v", path, err)
		}
	}
	for _, path := range matches {
		if strings.Contains(path, string(filepath.Separator)+"fixtures"+string(filepath.Separator)) ||
			strings.HasSuffix(path, ".synth.json") || strings.Contains(path, string(filepath.Separator)+"synth"+string(filepath.Separator)) {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		delete(doc, "_meta")
		cheapenHeavyExample(t, path, doc)
		rebaseExamplePaths(doc, dataDir)
		body, _ = json.Marshal(doc)
		switch {
		case doc["requests"] != nil || doc["sweep"] != nil:
			var req pulse.ComposedRequest
			if json.Unmarshal(body, &req) != nil {
				continue
			}
			if env, err := p.PredictCompose(ctx, &req); err != nil {
				refusedForParams(path, err)
			} else if env != nil {
				for _, e := range env.Errors {
					if e.Code == string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
						t.Errorf("%s: predict refused: %s", path, e.Message)
					}
				}
			}
			_, err := p.Compose(ctx, &req)
			refusedForParams(path, err)
		case doc["stages"] != nil:
			var req pulse.ChainRequest
			if json.Unmarshal(body, &req) != nil {
				continue
			}
			_, _ = p.PredictChain(ctx, &req)
			_, err := p.ProcessChain(ctx, &req)
			refusedForParams(path, err)
		case doc["fields"] != nil:
			var req pulse.FacetRequest
			if json.Unmarshal(body, &req) != nil {
				continue
			}
			_, _ = p.PredictFacet(ctx, &req)
			_, err := p.FacetSchema(ctx, &req)
			refusedForParams(path, err)
		default:
			var req pulse.Request
			if json.Unmarshal(body, &req) != nil {
				continue
			}
			if res, err := p.Predict(ctx, &req); err != nil {
				refusedForParams(path, err)
			} else if res != nil {
				for _, e := range res.Errors {
					if e.Code == string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
						t.Errorf("%s: predict refused: %s", path, e.Message)
					}
				}
			}
			_, err := p.Process(ctx, &req)
			refusedForParams(path, err)
		}
	}
}

// cheapenHeavyExample shrinks a heavyExamples request in place to its
// smallest run that still decodes every params key the example carries
// (the read set is the example's keys, not its sizes): TestExamples_RunEndToEnd
// runs the full-size ones. REG_MMM_BAYES samples 1 chain of 5 + 5, the
// MAT_SEM bootstrap slot 12 replicates (sem.BootstrapMinSuccesses is 10).
func cheapenHeavyExample(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	if !isHeavyExampleFile(path) {
		return
	}
	slotParams := func(slots string, i int) map[string]any {
		list, _ := doc[slots].([]any)
		if i >= len(list) {
			t.Fatalf("%s: no %s[%d] to cheapen", path, slots, i)
		}
		params, _ := list[i].(map[string]any)["params"].(map[string]any)
		if params == nil {
			t.Fatalf("%s: %s[%d] carries no params", path, slots, i)
		}
		return params
	}
	switch filepath.ToSlash(path)[strings.LastIndex(filepath.ToSlash(path), "/")+1:] {
	case "20_mmm_bayes.json":
		pp := slotParams("regressions", 0)
		pp["chains"], pp["warmup"], pp["draws"] = 1, 5, 5
	case "17_sem_contribution.json":
		slotParams("matrices", 1)["bootstrap_iters"] = 12
	default:
		t.Fatalf("%s: a heavy example with no cheapened twin", path)
	}
}

// rebaseExamplePaths points every cohort at dataDir and every save path
// under it, at any depth.
func rebaseExamplePaths(v any, dataDir string) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch k {
			case "cohort":
				if c, ok := val.(map[string]any); ok {
					c["data_dir"] = dataDir
				}
			case "save":
				if s, ok := val.(string); ok && s != "" {
					x[k] = filepath.Join(dataDir, s)
				}
			}
			rebaseExamplePaths(val, dataDir)
		}
	case []any:
		for _, e := range x {
			rebaseExamplePaths(e, dataDir)
		}
	}
}

// paramsProbes reach params reads no embedded example reaches: a series
// SHARE_OF_TOTAL, the Compose SERIES arm of the reference kinds, and
// every post-test family (PostTestDepth reads each one's partition_by;
// the ANOVA families decode n_col / variance_col). Like the examples,
// a probe's outcome is not judged — only what it read.
var paramsProbes = []string{
	`{"cohort":{"filename":"experiment.pulse"},"aggregations":[{"type":"AGG_SUM","field":"revenue","label":"revenue"}],
	  "groups":[{"type":"GROUP_CATEGORY","field":"region"}],
	  "overlays":[{"name":"share","kind":"OVERLAY_SHARE_OF_TOTAL","scope":"group"}]}`,
	`{"requests":[
	   {"label":"a","cohort":{"filename":"experiment.pulse"},"aggregations":[{"type":"AGG_SUM","field":"revenue","label":"revenue"}],"groups":[{"type":"GROUP_CATEGORY","field":"region"}]},
	   {"label":"b","cohort":{"filename":"experiment.pulse"},"aggregations":[{"type":"AGG_SUM","field":"revenue","label":"revenue"}],"groups":[{"type":"GROUP_CATEGORY","field":"region"}]}],
	  "overlays":[
	   {"name":"idx","kind":"OVERLAY_INDEX_VS_REF","scope":"group","reference":"a","targets":["b"]},
	   {"name":"dlt","kind":"OVERLAY_DELTA_VS_REF","scope":"group","reference":"a","targets":["b"]}]}`,
}

// postTestProbe is one request listing every post-test family over
// per-region summary rows.
func postTestProbe(families []string, anova bool) string {
	type post struct {
		Type    string         `json:"type"`
		Field   string         `json:"field"`
		SplitBy string         `json:"split_by"`
		OrderBy any            `json:"order_by,omitempty"`
		Params  map[string]any `json:"params"`
	}
	req := map[string]any{
		"cohort": map[string]any{"filename": "experiment.pulse"},
		"aggregations": []map[string]any{
			{"type": "AGG_AVERAGE", "field": "revenue", "label": "avg_revenue"},
			{"type": "AGG_VARIANCE", "field": "revenue", "label": "var_revenue"},
			{"type": "AGG_COUNT", "field": "revenue", "label": "n"},
		},
		"groups": []map[string]any{{"type": "GROUP_CATEGORY", "field": "region"}},
	}
	params := map[string]any{}
	if anova {
		params = map[string]any{"n_col": "n", "variance_col": "var_revenue"}
	}
	var posts []post
	for _, f := range families {
		posts = append(posts, post{Type: f, Field: "avg_revenue", SplitBy: "region", OrderBy: []map[string]any{{"field": "region"}},
			Params: params})
	}
	req["post_tests"] = posts
	b, _ := json.Marshal(req)
	return string(b)
}

// runParamsProbes runs paramsProbes and one post-test probe per family
// (a family's own params ride only its probe, so the ANOVA keys never
// reach a family that refuses them).
func runParamsProbes(t *testing.T, p *pulse.Pulse, dataDir string) {
	t.Helper()
	ctx := context.Background()
	probes := append([]string(nil), paramsProbes...)
	for _, pt := range p.Manifest(ctx).PostTests {
		anova := slices.ContainsFunc(pt.Params, func(pp descriptor.Param) bool { return pp.Name == "n_col" })
		probes = append(probes, postTestProbe([]string{pt.Family}, anova))
	}
	for _, body := range probes {
		var doc map[string]any
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("probe: %v", err)
		}
		rebaseExamplePaths(doc, dataDir)
		raw, _ := json.Marshal(doc)
		if doc["requests"] != nil {
			var req pulse.ComposedRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			_, _ = p.Compose(ctx, &req)
			continue
		}
		var req pulse.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		_, _ = p.Process(ctx, &req)
	}
}
