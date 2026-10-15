package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
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
}

// TestParamsDeclaredMatchRead is the declared-vs-read gate behind
// strict params decode (U44 #301): the manifest's Param lists are the
// allowlist a slot's params object is refused against
// (descx.ParamsAllowlist / ParamsRefusal), so a key an operator READS
// that its list does not declare would be a false refusal of a
// working request, and a declared params key it never reads would be
// accepted and silently ignored.
//
// It runs the whole embedded example library — every root, Predict and
// execution — under a paramread.Recording (every built-in params read
// goes through internal/paramread, TestParamsReadThroughParamread) and
// holds, per built-in operator:
//
//   - every key read is a declared params key (no Slot mark);
//   - every operator declaring params keys was exercised, and read each
//     of them (a struct decode records its whole key set);
//   - every Slot-marked declaration is a json field of the slot object;
//   - no example is refused for an unknown params key.
func TestParamsDeclaredMatchRead(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	buildExampleFixtures(t, p, fs, tmp)

	rec := paramread.Start()
	runAllExamples(t, p, tmp)
	runParamsProbes(t, p, tmp)
	read := rec.Stop()

	allow := descx.ParamsAllowlist(nil)
	var problems []string
	for k, keys := range read {
		decl, ok := allow[k]
		if !ok {
			problems = append(problems, string(k.Category)+" "+k.Op+": reads params "+strings.Join(keys, ", ")+" but is not in the allowlist")
			continue
		}
		for _, key := range keys {
			if !slices.Contains(decl.Keys, key) && !slices.Contains(decl.OwnRefusal, key) {
				problems = append(problems, string(k.Category)+" "+k.Op+": reads params."+key+", which its manifest Param list does not declare (a params key, no Slot mark)")
			}
		}
	}
	for k, decl := range allow {
		for _, s := range decl.Slot {
			st := paramsSlotTypes[k.Category]
			if st == nil || !jsonFieldOf(st, s) {
				problems = append(problems, string(k.Category)+" "+k.Op+": declares "+s+" as a Slot param, which is not a field of the slot object")
			}
		}
		if len(decl.Keys) == 0 {
			continue
		}
		got, exercised := read[k]
		if !exercised {
			problems = append(problems, string(k.Category)+" "+k.Op+": declares params "+strings.Join(decl.Keys, ", ")+" but no example run read its params (exercise it, or mark a slot field Slot)")
			continue
		}
		for _, key := range decl.Keys {
			if !slices.Contains(got, key) {
				problems = append(problems, string(k.Category)+" "+k.Op+": declares params."+key+" but the runtime never reads it (a slot field: mark it Slot; otherwise drop it)")
			}
		}
	}
	sort.Strings(problems)
	for _, pr := range problems {
		t.Error(pr)
	}
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
