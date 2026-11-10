package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_SYMMETRY refuses identically in predict and at runtime (code,
// message and details — index included) through the shared overlayhost
// refusals, runs end to end on weighted hosts, and leaves the host
// crosstab byte-identical to the overlay-free run.

func symmetryOverlay(params string) types.OverlaySpec {
	s := types.OverlaySpec{Name: "sym", Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}
	if params != "" {
		s.Params = json.RawMessage(params)
	}
	return s
}

func TestSymmetry_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	square := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
	twoCols := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}, {Type: types.GROUP_CATEGORY, Field: "subj"}}
	cellScope := symmetryOverlay("")
	cellScope.Scope = types.OverlayScopeCell
	withRef := symmetryOverlay("")
	withRef.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	for _, c := range []struct {
		name string
		req  *types.Request
		want errors.Code
	}{
		{"average cell", markovCrosstab(cohort, square, types.AGG_AVERAGE, nil, symmetryOverlay("")), errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED},
		{"columns carry two groupers", markovCrosstab(cohort, twoCols, types.AGG_COUNT, nil, symmetryOverlay("")), errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE},
		{"non-boolean correct", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"correct":1}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"unknown param", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"mid_p":true}`)), errors.PULSE_REQUEST_UNKNOWN_FIELD},
		{"non-boolean exact", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"exact":"yes"}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"conf_level without exact", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"conf_level":0.9}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"conf_level out of range", markovCrosstab(cohort, square, types.AGG_COUNT, nil, symmetryOverlay(`{"exact":true,"conf_level":1}`)), errors.PULSE_OVERLAY_PARAM_MISSING},
		{"exact on a probability-weighted cell", markovCrosstab(cohort, square, types.AGG_COUNT,
			&types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}, symmetryOverlay(`{"exact":true}`)), errors.PULSE_WEIGHT_UNSUPPORTED},
		{"cell scope", markovCrosstab(cohort, square, types.AGG_COUNT, nil, cellScope), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"ref set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, withRef), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"level set", markovCrosstab(cohort, square, types.AGG_COUNT, nil, func() types.OverlaySpec { s := symmetryOverlay(""); s.Level = 1; return s }()), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req)
			ce := requireCode(t, err, c.want)
			if !overlaySpecZeroLocated(ce) {
				t.Fatalf("runtime details %v lack the spec index", ce.Details)
			}
			if want, ok := confLevelReasons[c.name]; ok && (ce.Details["param"] != "conf_level" || ce.Details["reason"] != want) {
				t.Fatalf("runtime details %v, want param conf_level reason %s", ce.Details, want)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, c.req), err)
		})
	}
}

// confLevelReasons is the reason each conf_level refusal case of
// TestSymmetry_RefusalsMatchPredict carries (D31).
var confLevelReasons = map[string]string{
	"conf_level without exact": "needs_exact",
	"conf_level out of range":  "out_of_range",
}

// TestSymmetry_EndToEnd: a weighted and an unweighted g x g host (COUNT
// and SUM cells) run, predict accepts, the layer is a scalar χ² with its
// summary, and the host crosstab is byte-identical to the overlay-free
// run. g x subj (one grouper per axis, different keys) runs too: a
// one-axis state is a zero row / column, never a refusal.
func TestSymmetry_EndToEnd(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, cols := range [][]*types.Group{
		{{Type: types.GROUP_CATEGORY, Field: "g"}},
		{{Type: types.GROUP_CATEGORY, Field: "subj"}},
	} {
		for name, w := range map[string]*types.WeightSpec{
			"unweighted":  nil,
			"probability": {Field: "y", Kind: types.WeightKindProbability},
		} {
			t.Run(cols[0].Field+"/"+name, func(t *testing.T) {
				for _, cell := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM} {
					req := markovCrosstab(cohort, cols, cell, w, symmetryOverlay(""))
					if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
						t.Fatalf("%s: predict %+v", cell, env.Errors)
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("%s: %v", cell, err)
					}
					if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Shape != types.OverlayShapeScalar || resp.Overlays[0].Summary == nil ||
						resp.Overlays[0].Summary.PValue == nil {
						t.Fatalf("%s: overlays %+v", cell, resp.Overlays)
					}
					if df := resp.Overlays[0].Summary.Parameters["df"]; math.IsNaN(df) {
						t.Fatalf("%s: df %v", cell, df)
					}
					if _, err := json.Marshal(resp); err != nil {
						t.Fatalf("%s: marshal: %v", cell, err)
					}
					bare, err := p.Process(ctx, markovCrosstab(cohort, cols, cell, w))
					if err != nil {
						t.Fatal(err)
					}
					got, _ := json.Marshal(resp.Crosstab)
					want, _ := json.Marshal(bare.Crosstab)
					if string(got) != string(want) || bare.Overlays != nil {
						t.Fatalf("%s: the overlay moved the host crosstab", cell)
					}
				}
			})
		}
	}
}

// TestSymmetry_ExactEndToEnd: params.exact on a g x t_categorical_u8
// host (2 states, n_12 = n_21 = 12) runs the exact binomial McNemar —
// predict accepts, statistic n_12, p 1 (binom.test(12, 24)), exact 1, no
// df — under no weight and under a frequency weight (whole replication
// counts); on g x subj (more than 2 states) it runs the exact Bowker
// test — statistic the chi-square arm's uncorrected one, exact 1, no df,
// odds ratio, conf_level or continuity_corrected.
func TestSymmetry_ExactEndToEnd(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	two := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "t_categorical_u8"}}
	for name, w := range map[string]*types.WeightSpec{
		"unweighted": nil,
		"frequency":  {Field: "week", Kind: types.WeightKindFrequency},
	} {
		t.Run(name, func(t *testing.T) {
			req := markovCrosstab(cohort, two, types.AGG_COUNT, w, symmetryOverlay(`{"exact":true}`))
			if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
				t.Fatalf("predict %+v", env.Errors)
			}
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			s := resp.Overlays[0].Summary
			// Unweighted the pair is 12 / 12 (p 1); under the frequency
			// weight week = i + 1 it is the Σw pair 300 / 288 — R
			// binom.test(300, 588) = 0.65013033446921742
			stat, n, wantP := 12.0, 24.0, 1.0
			if w != nil {
				stat, n, wantP = 300, 588, 0.65013033446921742
			}
			if s.Parameters["exact"] != 1 || s.Parameters["states"] != 2 || math.Abs(*s.PValue-wantP) > 1e-13 ||
				*s.Statistic != stat || s.Parameters["discordant"] != n {
				t.Fatalf("summary %+v statistic %v p %v", s.Parameters, *s.Statistic, *s.PValue)
			}
			if _, ok := s.Parameters["df"]; ok {
				t.Fatalf("exact layer carries df: %v", s.Parameters)
			}
			if s.Parameters["conf_level"] != 0.95 {
				t.Fatalf("exact layer echoes conf_level %v, want the 0.95 default", s.Parameters["conf_level"])
			}
			if w != nil && s.Parameters["sum_weights"] == 0 {
				t.Fatalf("frequency host lacks the weighted floor: %v", s.Parameters)
			}
			// The discordant odds ratio n_12 / n_21 rides the frequency
			// host's whole Σw counts, inside its exact interval.
			if or := s.Parameters["odds_ratio"]; or != stat/(n-stat) ||
				!(s.Parameters["odds_ratio_ci_lower"] < or && or < s.Parameters["odds_ratio_ci_upper"]) {
				t.Fatalf("odds ratio %v, want %v inside its interval: %v", or, stat/(n-stat), s.Parameters)
			}
		})
	}

	// g x subj (more than 2 states): the exact Bowker test.
	subj := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}
	t.Run("k x k", func(t *testing.T) {
		req := markovCrosstab(cohort, subj, types.AGG_COUNT, nil, symmetryOverlay(`{"exact":true}`))
		if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
			t.Fatalf("predict %+v", env.Errors)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		s := resp.Overlays[0].Summary
		if s.Parameters["exact"] != 1 || !(s.Parameters["states"] > 2) || s.PValue == nil || !(*s.PValue >= 0 && *s.PValue <= 1) {
			t.Fatalf("k x k exact summary %+v", s)
		}
		for _, key := range []string{"df", "odds_ratio", "conf_level", "continuity_corrected"} {
			if _, ok := s.Parameters[key]; ok {
				t.Fatalf("k x k exact layer carries %s: %v", key, s.Parameters)
			}
		}
		chi, err := p.Process(ctx, markovCrosstab(cohort, subj, types.AGG_COUNT, nil, symmetryOverlay(`{"correct":false}`)))
		if err != nil {
			t.Fatal(err)
		}
		if *s.Statistic != *chi.Overlays[0].Summary.Statistic {
			t.Fatalf("k x k exact statistic %v, want the chi-square arm's %v", *s.Statistic, *chi.Overlays[0].Summary.Statistic)
		}
	})
}

// TestSymmetry_ExactStateRefusalsRuntimeOnly: the k x k exact refusals
// read the data's state count or support, so predict accepts and only
// the runtime refuses, located at the spec — an explicit conf_level with
// exact on more than 2 states (PULSE_OVERLAY_PARAM_MISSING {param:
// conf_level, reason: needs_2x2, states}; the same request on 2 states
// runs), and exact on the frequency-weighted g x subj host, whose Σw
// counts (week = i + 1) outgrow the convolution's work cap
// (PULSE_OVERLAY_PARAM_MISSING {param: exact, reason: exact_too_large,
// states, terms, limit}; the chi-square arm runs it).
func TestSymmetry_ExactStateRefusalsRuntimeOnly(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	subj := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}}
	params := `{"exact":true,"conf_level":0.9}`
	freq := &types.WeightSpec{Field: "week", Kind: types.WeightKindFrequency}
	for _, c := range []struct {
		name, param, reason string
		req                 *types.Request
	}{
		{"conf_level on k x k", "conf_level", "needs_2x2", markovCrosstab(cohort, subj, types.AGG_COUNT, nil, symmetryOverlay(params))},
		{"exact too large", "exact", "exact_too_large", markovCrosstab(cohort, subj, types.AGG_COUNT, freq, symmetryOverlay(`{"exact":true}`))},
	} {
		t.Run(c.name, func(t *testing.T) {
			if env := predictEnvelope(t, p, fs, cohort, c.req); len(env.Errors) != 0 {
				t.Fatalf("predict refused a data-dependent refusal: %+v", env.Errors)
			}
			_, err := p.Process(ctx, c.req)
			ce := requireCode(t, err, errors.PULSE_OVERLAY_PARAM_MISSING)
			if ce.Details["param"] != c.param || ce.Details["reason"] != c.reason || ce.Details["index"] != 0 || ce.Details["states"] == nil {
				t.Fatalf("details %v", ce.Details)
			}
		})
	}
	two := markovCrosstab(cohort, []*types.Group{{Type: types.GROUP_CATEGORY, Field: "t_categorical_u8"}}, types.AGG_COUNT, nil, symmetryOverlay(params))
	if _, err := p.Process(ctx, two); err != nil {
		t.Fatalf("conf_level on 2 states: %v", err)
	}
	if _, err := p.Process(ctx, markovCrosstab(cohort, subj, types.AGG_COUNT, freq, symmetryOverlay(""))); err != nil {
		t.Fatalf("chi-square arm on the large host: %v", err)
	}
}
