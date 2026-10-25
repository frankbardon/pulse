package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_score_parity_test.go pins ATTR_SEM_SCORE's refusals across
// predict and the runtime: a config refusal (an unknown latent, a group
// field the schema lacks) and, on an instance hiding
// capability:sem_meanstructure / capability:sem_groups, the owned
// params keys refused exactly as keys that never existed and the owned
// model syntax refused like MAT_SEM's — predict ≡ runtime throughout.

// semScoreRequest scores latent "value" of the brand_ms model's
// measurement part over brandMembers, averaged cohort-wide; params
// override (nil deletes) the slot's params.
func semScoreRequest(model string, params map[string]any) *types.Request {
	all := map[string]any{"model": model, "latent": "value"}
	for k, v := range params {
		if v == nil {
			delete(all, k)
			continue
		}
		all[k] = v
	}
	raw, _ := json.Marshal(all)
	return &types.Request{
		Cohort:       &types.Cohort{Filename: "brand.pulse"},
		Attributes:   []*types.Attribute{{Type: types.ATTR_SEM_SCORE, Label: "sc", Fields: brandMembers, Params: raw}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "sc"}},
	}
}

// semScoreCFA is a three-factor CFA over brandMembers' ratings.
const semScoreCFA = "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3 + recommend"

// TestSEMScore_ConfigRefusalsPredictMatchesRuntime: a latent the model
// lacks and a group field the schema lacks are refused by the runtime
// before any record is read, and predict returns the same entry.
func TestSEMScore_ConfigRefusalsPredictMatchesRuntime(t *testing.T) {
	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	for _, c := range []struct {
		name   string
		params map[string]any
		reason string
	}{
		{"latent_unknown", map[string]any{"latent": "loyalty"}, "latent_unknown"},
		{"latent_observed", map[string]any{"latent": "q1"}, "latent_unknown"},
		{"group_missing", map[string]any{"group": "nope"}, ""},
		{"method", map[string]any{"method": "ml"}, "bad_params"},
		{"fit_and_model", map[string]any{"fit": "brand"}, "fit_and_model"},
		{"fit_and_fit_on", map[string]any{"fit": "brand", "model": nil, "latent": nil, "fit_on": []map[string]any{{"type": "FILTER_TRUE"}}}, "fit_and_fit_on"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, rerr := p.Process(context.Background(), semScoreRequest(semScoreCFA, c.params))
			var ce *errors.CodedError
			if !asCoded(rerr, &ce) {
				t.Fatalf("runtime accepted %v: %v", c.params, rerr)
			}
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Errorf("reason %v, want %s (%s)", ce.Details["reason"], c.reason, ce.Message)
			}
			sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", semScoreRequest(semScoreCFA, c.params)), rerr)
		})
	}
}

// TestSEMScore_HiddenSurfacesRefuseLikeUnknown: on an instance hiding
// capability:sem_groups (and, second, capability:sem_meanstructure
// too) each owned ATTR_SEM_SCORE params key is refused exactly as a key
// the operator never read, and the owned model syntax as MAT_SEM
// refuses it — predict says the same. The covariance-only score runs.
func TestSEMScore_HiddenSurfacesRefuseLikeUnknown(t *testing.T) {
	ctx := context.Background()
	for _, means := range []bool{true, false} {
		features := []string{"capability:process", "ATTR_SEM_SCORE", "AGG_AVERAGE"}
		if means {
			features = append(features, "capability:sem_meanstructure")
		}
		p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{FeatureProfile: &pulse.FeatureProfile{Features: features}})
		_, unknownErr := p.Process(ctx, semScoreRequest(semScoreCFA, map[string]any{"zz_never": "wave"}))
		var unknown *errors.CodedError
		if !asCoded(unknownErr, &unknown) {
			t.Fatalf("a made-up key: %v", unknownErr)
		}
		unknownDetails, _ := json.Marshal(unknown.Details)
		// capability:sem_ordinal is never enabled here (U59 E4-S4: its
		// keys ride ATTR_SEM_SCORE's inline fit too).
		hiddenKeys := []string{"group", "group_reference", "group_equal", "group_partial", "estimator", "ordered", "parameterization"}
		if !means {
			hiddenKeys = append(hiddenKeys, "meanstructure")
		}
		for _, k := range hiddenKeys {
			t.Run(fmt.Sprintf("means=%v/%s", means, k), func(t *testing.T) {
				var v any = "wave"
				switch k {
				case "meanstructure":
					v = true
				case "estimator":
					v = "wlsmv"
				case "ordered":
					v = []string{"q1"}
				case "parameterization":
					v = "delta"
				}
				req := func() *types.Request { return semScoreRequest(semScoreCFA, map[string]any{k: v}) }
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
				sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req()), rerr)
			})
		}
		syntax := map[string]string{
			"c(...)": "quality =~ q1 + q2 + q3\nvalue =~ v1 + c(a, b)*v2 + v3\nservice =~ s1 + s2 + s3 + recommend",
			":=":     "quality =~ q1 + q2 + q3\nvalue =~ v1 + a*v2 + b*v3\nservice =~ s1 + s2 + s3 + recommend\nd := a*b",
		}
		if !means {
			syntax["~ 1"] = semScoreCFA + "\nq2 ~ 1"
		}
		for name, model := range syntax {
			t.Run(fmt.Sprintf("means=%v/%s", means, name), func(t *testing.T) {
				req := func() *types.Request { return semScoreRequest(model, nil) }
				_, rerr := p.Process(ctx, req())
				var ce *errors.CodedError
				if !asCoded(rerr, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != "model" || ce.Details["attribute"] == nil {
					t.Fatalf("hidden %s: %v (%v)", name, rerr, semDetailsOf(ce))
				}
				for _, word := range []string{"group", "meanstructure", "mean structure", "defined"} {
					if strings.Contains(ce.Message, word) {
						t.Errorf("the refusal names the hidden surface (%q): %s", word, ce.Message)
					}
				}
				sameEntry(t, predictEnvelope(t, p, fsys, "brand.pulse", req()), rerr)
			})
		}
		t.Run(fmt.Sprintf("means=%v/covariance", means), func(t *testing.T) {
			resp, err := p.Process(ctx, semScoreRequest(semScoreCFA, nil))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(resp.Data)
			var rows []map[string]any
			if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
				t.Fatalf("data %s", raw)
			}
			for k, v := range rows[0] {
				if f, ok := v.(float64); ok && strings.Contains(k, "sc") && !(math.Abs(f) < 1e-9) {
					t.Errorf("%s = %g, want 0: a covariance-only score is centered at the fit rows", k, f)
				}
			}
		})
	}
}
