package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// sem_predict_parity_test.go pins MAT_SEM's mean-structure and
// multi-group surfaces across predict, the manifest and the runtime:
// predict's level list (an upper bound, unknown on an integer field)
// against the levels a run fits, every emitted output key against the
// manifest's declared keys (per-level keys by their <level> pattern),
// and a feature profile hiding capability:sem_meanstructure /
// capability:sem_groups: the params keys refused exactly as keys that
// never existed (predict == runtime), the model syntax they own
// refused, the manifest dropping their params, output keys and prose.

// semDeclaredKeys are MAT_SEM's declared output keys on instance p, by
// MatrixResult part.
func semDeclaredKeys(t *testing.T, p *pulse.Pulse) map[string][]string {
	t.Helper()
	for _, m := range p.Manifest(context.Background()).Matrices {
		if m.Name == string(types.MAT_SEM) {
			return map[string][]string{"auxiliary": m.OutputKeys.Auxiliary, "vectors": m.OutputKeys.Vectors, "scalars": m.OutputKeys.Scalars}
		}
	}
	t.Fatal("manifest lists no MAT_SEM")
	return nil
}

// declaredMatch reports whether key matches a declared key: equal, or
// the key a <level> pattern spells for some level.
func declaredMatch(declared []string, key string) bool {
	return slices.ContainsFunc(declared, func(d string) bool {
		pre, post, ok := strings.Cut(d, "<level>")
		if !ok {
			return d == key
		}
		return len(key) > len(pre)+len(post) && strings.HasPrefix(key, pre) && strings.HasSuffix(key, post)
	})
}

// checkSEMKeysDeclared fails on any key res carries that its operator
// does not declare.
func checkSEMKeysDeclared(t *testing.T, declared map[string][]string, res types.MatrixResult) {
	t.Helper()
	for part, keys := range map[string][]string{
		"auxiliary": semKeysOf(res.Auxiliary), "vectors": semKeysOf(res.Vectors), "scalars": semKeysOf(res.Scalars),
	} {
		for _, k := range keys {
			if !declaredMatch(declared[part], k) {
				t.Errorf("%s.%s is emitted but not declared (%v)", part, k, declared[part])
			}
		}
	}
}

func semKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// runLevels are a grouped result's fitted levels in order (the
// distinct vectors.group entries).
func runLevels(t *testing.T, res types.MatrixResult) []string {
	t.Helper()
	groups, ok := res.Vectors["group"].([]any)
	if !ok {
		t.Fatalf("vectors.group is %T", res.Vectors["group"])
	}
	var out []string
	for _, v := range groups {
		g, ok := v.(string)
		if !ok {
			continue // a := row: no group
		}
		if len(out) == 0 || out[len(out)-1] != g {
			out = append(out, g)
		}
	}
	return out
}

// TestSEM_PredictGroupedMatchesRuntime: predict's group_levels for a
// grouped fit equal the fitted levels when every level holds rows and
// bound them (same order) when one does not; an integer field is
// "unknown". Predict's spec-level streamable / mergeable verdicts are
// the plan's, the joint fit is one result, and every key a mean
// structure or grouped fit emits is declared in the manifest.
func TestSEM_PredictGroupedMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		cohort brandGroupCohort
		params map[string]any
		basis  string
		levels []string // predict's
		fitted []string // the run's
	}{
		{"categorical", brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}, nil, "dictionary", []string{"1", "2"}, []string{"1", "2"}},
		// A dictionary entry without rows is no group: predict's list is
		// an upper bound.
		{"categorical_empty_level", brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, dict: []string{"1", "3", "2"}}, nil, "dictionary", []string{"1", "3", "2"}, []string{"1", "2"}},
		{"packed_bool", brandGroupCohort{csv: "brand", wave: encoding.FieldTypePackedBool}, nil, "boolean", []string{"0", "1"}, []string{"0", "1"}},
		{"u8", brandGroupCohort{csv: "brand", wave: encoding.FieldTypeU8}, nil, "unknown", nil, []string{"1", "2"}},
		{"ungrouped_meanstructure", brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}, map[string]any{"group": nil, "meanstructure": true}, "", nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, fsys := c.cohort.build(t, pulse.Options{})
			req := semGroupRequest(t, "brand_mgmis", c.params)
			env := predictEnvelope(t, p, fsys, "brand.pulse", semGroupRequest(t, "brand_mgmis", c.params))
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			pr := env.Data.(*descriptor.PredictResult)
			if len(pr.Matrices) != 1 {
				t.Fatalf("predict reports %d matrices", len(pr.Matrices))
			}
			mp := pr.Matrices[0]
			if mp.GroupLevelsBasis != c.basis || fmt.Sprint(mp.GroupLevels) != fmt.Sprint(c.levels) {
				t.Errorf("predict group levels %v (%q), want %v (%q)", mp.GroupLevels, mp.GroupLevelsBasis, c.levels, c.basis)
			}
			if !mp.Streamable || !mp.Mergeable || mp.Streamable != req.Matrices[0].Streamable() || mp.Mergeable != req.Matrices[0].Mergeable() {
				t.Errorf("predict streamable %v mergeable %v, the spec's %v / %v", mp.Streamable, mp.Mergeable, req.Matrices[0].Streamable(), req.Matrices[0].Mergeable())
			}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Matrices) != 1 || resp.Matrices[0].Error != nil {
				t.Fatalf("runtime %d matrices (%+v)", len(resp.Matrices), resp.Matrices)
			}
			res := resp.Matrices[0]
			if mp.EstimatedBuckets == nil || *mp.EstimatedBuckets != int64(len(resp.Matrices)) {
				t.Errorf("predict estimated_buckets %v, runtime %d results", mp.EstimatedBuckets, len(resp.Matrices))
			}
			if c.fitted != nil {
				got := runLevels(t, res)
				if fmt.Sprint(got) != fmt.Sprint(c.fitted) {
					t.Errorf("fitted levels %v, want %v", got, c.fitted)
				}
				// predict's list bounds the fitted levels, in order.
				if c.levels != nil {
					rest := c.levels
					for _, l := range got {
						i := slices.Index(rest, l)
						if i < 0 {
							t.Errorf("fitted level %q is not in predict's %v (in order)", l, c.levels)
							break
						}
						rest = rest[i+1:]
					}
				}
			} else if res.Auxiliary["implied_means"] == nil {
				t.Error("a mean structure emits auxiliary.implied_means")
			}
			checkSEMKeysDeclared(t, semDeclaredKeys(t, p), res)
		})
	}
}

// semHidingInstance is an instance over the brand cohort whose profile
// enables MAT_SEM and, when means, capability:sem_meanstructure
// (capability:sem_groups never).
func semHidingInstance(t *testing.T, means bool) (*pulse.Pulse, func(*types.Request) *descriptor.Envelope) {
	t.Helper()
	features := []string{"capability:process", "capability:matrices", "MAT_SEM"}
	if means {
		features = append(features, "capability:sem_meanstructure")
	}
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{FeatureProfile: &pulse.FeatureProfile{Features: features}})
	return p, func(r *types.Request) *descriptor.Envelope { return predictEnvelope(t, p, fsys, "brand.pulse", r) }
}

// TestSEM_HiddenSurfacesRefuseLikeUnknown: on an instance hiding
// capability:sem_groups (and, second, capability:sem_meanstructure
// too) each owned params key is refused exactly as a key no MAT_SEM
// ever read — the default instance's refusal of a made-up key, with
// the name swapped — and so is the owned model syntax; predict says
// the same. The legacy covariance fit runs unchanged.
func TestSEM_HiddenSurfacesRefuseLikeUnknown(t *testing.T) {
	ctx := context.Background()
	full, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	legacy := semGroupRun(t, full, semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil}))

	for _, means := range []bool{true, false} {
		p, predict := semHidingInstance(t, means)
		// The instance's refusal of a key MAT_SEM never read.
		_, unknownErr := p.Process(ctx, semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "zz_never": "wave"}))
		var unknown *errors.CodedError
		if !asCoded(unknownErr, &unknown) || unknown.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
			t.Fatalf("a made-up key: %v", unknownErr)
		}
		unknownDetails, _ := json.Marshal(unknown.Details)
		// capability:sem_bootstrap is never enabled here either.
		hiddenKeys := []string{"group", "group_reference", "group_equal", "group_partial", "resample", "bootstrap_iters", "rng_seed", "ci_level"}
		if !means {
			hiddenKeys = append(hiddenKeys, "meanstructure")
		}
		for _, k := range hiddenKeys {
			t.Run(fmt.Sprintf("means=%v/%s", means, k), func(t *testing.T) {
				var v any = "wave"
				switch k {
				case "meanstructure":
					v = true
				case "resample":
					v = "bootstrap"
				case "bootstrap_iters", "rng_seed":
					v = 10
				case "ci_level":
					v = 0.9
				}
				req := func() *types.Request {
					return semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, k: v})
				}
				_, rerr := p.Process(ctx, req())
				var ce *errors.CodedError
				if !asCoded(rerr, &ce) {
					t.Fatalf("hidden %s: %v", k, rerr)
				}
				want := strings.ReplaceAll(unknown.Message, "zz_never", k)
				details, _ := json.Marshal(ce.Details)
				wantDetails := strings.ReplaceAll(string(unknownDetails), "zz_never", k)
				if ce.Code != unknown.Code || ce.Message != want || string(details) != wantDetails {
					t.Errorf("hidden %s: %s %q %s, want the unknown-key refusal %s %q %s", k, ce.Code, ce.Message, details, unknown.Code, want, wantDetails)
				}
				if slices.Contains(ce.Details["valid_keys"].([]string), k) {
					t.Errorf("valid_keys offers the hidden %s", k)
				}
				sameEntry(t, predict(req()), rerr)
			})
		}
		syntax := map[string]string{
			"c(...)": "f =~ q1 + c(a, b)*q2 + q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend",
			// capability:sem_defined is never enabled here.
			":=": "f =~ q1 + a*q2 + b*q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend\nd := a*b",
		}
		if !means {
			syntax["~ 1"] = "f =~ q1 + q2 + q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend\nq2 ~ 1"
		} else {
			// The mean structure is offered: a ~ 1 line fits.
			res := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "model": "f =~ q1 + q2 + q3 + v1 + v2 + v3 + s1 + s2 + s3 + recommend\nq2 ~ 1"}))
			if res.Error != nil || res.Auxiliary["implied_means"] == nil {
				t.Errorf("a ~ 1 line on an instance offering the mean structure: %+v", res.Error)
			}
		}
		for name, model := range syntax {
			t.Run(fmt.Sprintf("means=%v/%s", means, name), func(t *testing.T) {
				req := func() *types.Request {
					return semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "model": model})
				}
				_, rerr := p.Process(ctx, req())
				var ce *errors.CodedError
				if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != "model" {
					t.Fatalf("hidden %s: %v (%v)", name, rerr, semDetailsOf(ce))
				}
				for _, word := range []string{"group", "meanstructure", "mean structure", "defined"} {
					if strings.Contains(ce.Message, word) {
						t.Errorf("the refusal names the hidden surface (%q): %s", word, ce.Message)
					}
				}
				sameEntry(t, predict(req()), rerr)
			})
		}
		t.Run(fmt.Sprintf("means=%v/legacy", means), func(t *testing.T) {
			res := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil}))
			a, _ := json.Marshal(res)
			b, _ := json.Marshal(legacy)
			if string(a) != string(b) {
				t.Error("the legacy covariance fit differs on the hiding instance")
			}
		})
	}
}

// TestSEM_HiddenBootstrapUnderWeight: with capability:sem_bootstrap
// hidden and capability:weighting offered, a weighted slot carrying
// params.resample is refused as the unknown key it is — never as the
// bootstrap's weight refusal, which would name the hidden surface —
// predict ≡ runtime.
func TestSEM_HiddenBootstrapUnderWeight(t *testing.T) {
	features := []string{"capability:process", "capability:matrices", "capability:weighting", "MAT_SEM"}
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, weight: func(int) int { return 1 }}.build(t, pulse.Options{FeatureProfile: &pulse.FeatureProfile{Features: features}})
	req := func() *types.Request {
		r := semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "resample": "bootstrap"})
		r.Matrices[0].Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		return r
	}
	_, rerr := p.Process(context.Background(), req())
	var ce *errors.CodedError
	if !asCoded(rerr, &ce) || ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("got %v (%v), want the unknown-key refusal", rerr, semDetailsOf(ce))
	}
	sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req()), rerr)
}

// TestSEM_HiddenSurfacesLeaveTheManifest: a profile hiding the MAT_SEM
// surfaces drops their params, their output keys and every manifest
// sentence naming one of their wire tokens from the MAT_SEM entry,
// while the default instance lists them. The payload schema carries
// operator params opaquely (MatrixSpec.params), so it names none of
// them either way. A profile enabling capability:sem_groups without
// capability:sem_meanstructure is refused.
func TestSEM_HiddenSurfacesLeaveTheManifest(t *testing.T) {
	ctx := context.Background()
	entry := func(p *pulse.Pulse) descriptor.MatrixMeta {
		for _, m := range p.Manifest(ctx).Matrices {
			if m.Name == string(types.MAT_SEM) {
				return m
			}
		}
		t.Fatal("no MAT_SEM entry")
		return descriptor.MatrixMeta{}
	}
	params := func(m descriptor.MatrixMeta) []string {
		var out []string
		for _, p := range m.Params {
			out = append(out, p.Name)
		}
		return out
	}
	full, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	fm := entry(full)
	for _, k := range []string{"meanstructure", "group", "group_reference", "group_equal", "group_partial", "resample", "bootstrap_iters", "rng_seed", "ci_level"} {
		if !slices.Contains(params(fm), k) {
			t.Errorf("default manifest lacks params.%s", k)
		}
	}
	for _, c := range []struct {
		means  bool
		hidden []string // params and wire tokens absent from the entry
		kept   []string // params still listed
	}{
		{true, []string{"group", "group_reference", "group_equal", "group_partial", "chi2_group_", "n_group_", "PULSE_MATRIX_DEFINED_NONFINITE", ":=",
			"resample", "bootstrap_iters", "rng_seed", "ci_level", "bootstrap_requested", "bootstrap_succeeded"}, []string{"meanstructure", "model"}},
		{false, []string{"group", "group_reference", "group_equal", "group_partial", "meanstructure", "implied_means", "observed_means", "PULSE_MATRIX_DEFINED_NONFINITE", ":=",
			"resample", "bootstrap_iters", "rng_seed", "ci_level", "bootstrap_requested", "bootstrap_succeeded"}, []string{"model", "solver"}},
	} {
		t.Run(fmt.Sprintf("means=%v", c.means), func(t *testing.T) {
			p, _ := semHidingInstance(t, c.means)
			m := entry(p)
			raw, _ := json.Marshal(m)
			for _, k := range c.hidden {
				if slices.Contains(params(m), k) {
					t.Errorf("params.%s is listed", k)
				}
				if k != "group" && strings.Contains(string(raw), k) {
					t.Errorf("the MAT_SEM entry still names %q", k)
				}
			}
			for _, k := range c.kept {
				if !slices.Contains(params(m), k) {
					t.Errorf("params.%s was dropped", k)
				}
			}
			for _, ks := range [][]string{m.OutputKeys.Auxiliary, m.OutputKeys.Scalars} {
				for _, k := range ks {
					if strings.Contains(k, "<level>") || (!c.means && strings.HasSuffix(k, "_means")) {
						t.Errorf("output key %q of a hidden surface is declared", k)
					}
				}
			}
			if slices.Contains(m.OutputKeys.Vectors, "group") {
				t.Error("vectors.group is declared")
			}
			schema, err := p.PayloadSchema()
			if err != nil {
				t.Fatal(err)
			}
			// REG_RESAMPLE's RegressionSpec slots share the bootstrap's
			// names (resample, bootstrap_iters, rng_seed and the echoes),
			// so the schema names them for the regressions.
			regression := []string{"resample", "bootstrap_iters", "rng_seed", "bootstrap_requested", "bootstrap_succeeded"}
			for _, k := range c.hidden {
				if k != "group" && !slices.Contains(regression, k) && strings.Contains(string(schema), `"`+k) {
					t.Errorf("the payload schema names %q", k)
				}
			}
		})
	}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{Features: []string{
		"capability:process", "capability:matrices", "MAT_SEM", "capability:sem_groups",
	}}})
	if !errors.HasCode(err, errors.PULSE_FEATURE_PROFILE_DEPENDENCY) {
		t.Errorf("sem_groups without sem_meanstructure: %v, want PULSE_FEATURE_PROFILE_DEPENDENCY", err)
	}
}
