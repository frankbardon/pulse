package synth

import (
	"strings"
	"testing"
)

// TestClassifyWarning_EveryProducerIsClassified is the drift gate on the
// taxonomy. Every warning string synth can emit has to land in a named
// kind; anything that falls through to "other" is a message someone
// reworded (or added) without touching warningKinds, and the whole point
// of the table is that such a line stops being counted with its peers.
//
// Warnings built by a named helper are produced by CALLING that helper,
// so a reworded format string is caught here rather than in a literal
// that was copied at the same time. The remainder are inline
// fmt.Sprintf sites; each literal below names its source.
func TestClassifyWarning_EveryProducerIsClassified(t *testing.T) {
	cases := []struct {
		name      string
		warning   string
		wantKind  string
		attention bool
	}{
		{
			name:      "thinPairWarning/numeric",
			warning:   thinPairWarning("numeric", "a", "b", 3, MinPairObservations),
			wantKind:  "thin numeric pair",
			attention: false,
		},
		{
			name:      "thinPairWarning/categorical",
			warning:   thinPairWarning("categorical", "a", "b", 3, MinPairObservations),
			wantKind:  "thin categorical pair",
			attention: false,
		},
		{
			name:      "thinPairWarning/set",
			warning:   thinPairWarning("set", "a", "b", 3, MinPairObservations),
			wantKind:  "thin set pair",
			attention: false,
		},
		{
			name:      "thinPairWarning/residual",
			warning:   thinPairWarning("residual", "a", "b", 3, MinPairObservations),
			wantKind:  "thin residual pair",
			attention: false,
		},
		{
			name:      "thinSupportWarning/model level",
			warning:   thinSupportWarning("model level", "brand=6", "nps", 3, minLevelObservations, "shrunk"),
			wantKind:  "thin model level",
			attention: false,
		},
		{
			name:      "modelSkipWarning",
			warning:   modelSkipWarning("nps", "design matrix would be 2420 columns wide"),
			wantKind:  "model skipped",
			attention: true,
		},
		{
			name:      "modelNoPredictorWarning",
			warning:   modelNoPredictorWarning("nps"),
			wantKind:  "model carries no predictors",
			attention: false,
		},
		{
			name:      "unmodelledResidualWarning",
			warning:   unmodelledResidualWarning(map[string]bool{"nps": true}),
			wantKind:  "residual correlation dropped",
			attention: true,
		},
		{
			// synth/profile.go, SpecFromProfile's model-drop channel.
			// This is the E2-S5 defect's own line: 35 of these named a
			// mis-serialised predictor kind and nothing printed them.
			name:      "SpecFromProfile/not applied",
			warning:   `model for numeric field "regard" not applied: predictor "brand" carries unsupported kind "numeric"`,
			wantKind:  "model not applied",
			attention: true,
		},
		{
			// Same site, but the reason is the zero-predictor outcome.
			// It must NOT be a fault: see TestGroupWarnings_ZeroPredictorModelIsNotAFault.
			name:      "SpecFromProfile/not applied because no predictors",
			warning:   `model for numeric field "clarity" not applied: model carries no predictors`,
			wantKind:  "model carries no predictors",
			attention: false,
		},
		{
			// synth/model_draw.go, buildModelDrawers.
			name:      "buildModelDrawers/undeclared target",
			warning:   `model not applied: target field "nps" is not declared in the schema`,
			wantKind:  "model not applied",
			attention: true,
		},
		{
			// synth/conflict.go, conflict().
			name:      "resolveConflicts/conflict",
			warning:   `conditional relationship conflict: field "dma" is already claimed by categorical pair (wave -> dma); dropping categorical pair (study -> dma)`,
			wantKind:  "conditional relationship conflict",
			attention: false,
		},
		{
			// synth/conflict.go, the modelled-participant exclusion.
			name:      "resolveConflicts/correlation names a modelled field",
			warning:   `pairwise correlation naming field "nps" is not applied: the field is drawn from its linear model, which owns its value`,
			wantKind:  "correlation not applied (the model owns the field)",
			attention: false,
		},
		{
			// synth/copula.go, buildCorrelator.
			name:      "buildCorrelator/assumed pairs",
			warning:   "correlation matrix completed by assumption: 3 of 10 pair(s) among 5 field(s) were never supplied",
			wantKind:  "correlation matrix completed by assumption",
			attention: true,
		},
		{
			name:      "buildCorrelator/ridge",
			warning:   "correlation matrix is not positive definite as requested and was ridge-regularized (diagonal jitter 1e-08) to factorize",
			wantKind:  "correlation matrix ridge-regularized",
			attention: true,
		},
		{
			// synth/residual_corr.go, residualCaptureWarnings.
			name:      "residualCaptureWarnings/unmeasured",
			warning:   "residual correlations: 104 of 5460 pair(s) among 105 modelled field(s) could not be measured",
			wantKind:  "residual correlation pairs unmeasured",
			attention: true,
		},
		{
			// synth/profile.go, the flag-combination notice.
			name:      "profile/residual correlations without --fit-models",
			warning:   "residual correlations requested without --fit-models: residuals come from fitted models",
			wantKind:  "residual correlations not captured",
			attention: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.warning == "" {
				t.Fatal("producer returned an empty warning; the fixture no longer exercises it")
			}
			kind, attention := classifyWarning(tc.warning)
			if kind != tc.wantKind {
				t.Errorf("classifyWarning(%q) kind = %q, want %q", tc.warning, kind, tc.wantKind)
			}
			if attention != tc.attention {
				t.Errorf("classifyWarning(%q) attention = %v, want %v", tc.warning, attention, tc.attention)
			}
		})
	}
}

// TestClassifyWarning_RollupsJoinTheirOwnGroup pins that the bounded
// "+N further …" summaries the thin paths already emit are counted with
// the lines they continue, not as a kind of their own.
func TestClassifyWarning_RollupsJoinTheirOwnGroup(t *testing.T) {
	cases := map[string]string{
		"+69 further thin model level(s) shrunk toward their reference level (below 50 supporting observation(s))": "thin model level",
		"+12 further thin residual pair(s) below 30 supporting observation(s)":                                     "thin residual pair",
	}
	for w, want := range cases {
		kind, attention := classifyWarning(w)
		if kind != want {
			t.Errorf("classifyWarning(%q) kind = %q, want %q", w, kind, want)
		}
		if attention {
			t.Errorf("classifyWarning(%q) marked as needing attention; a bounded roll-up is not a fault", w)
		}
	}
}

// TestClassifyWarning_UnknownNeedsAttention is the inverse of the
// v0.32.x lesson: an unrecognised warning must not be quietly filed as
// expected. A benign default is how a mis-serialised predictor kind
// disabled 80% of --fit-models without a signal anywhere.
func TestClassifyWarning_UnknownNeedsAttention(t *testing.T) {
	kind, attention := classifyWarning("a message no rule in warningKinds has ever seen")
	if kind != otherWarningKind {
		t.Errorf("kind = %q, want %q", kind, otherWarningKind)
	}
	if !attention {
		t.Error("an unclassified warning must need attention, not be filed as an expected outcome")
	}
}

// TestGroupWarnings_ZeroPredictorModelIsNotAFault is the acceptance bar
// for "expected outcomes are not reported as faults": on the motivating
// cohort 50 targets carry a complete zero-predictor model, and counting
// those as faults would put the one real finding 50 lines down a list
// nobody then reads.
func TestGroupWarnings_ZeroPredictorModelIsNotAFault(t *testing.T) {
	var ws []string
	for i := 0; i < 50; i++ {
		ws = append(ws, modelNoPredictorWarning("field"+string(rune('a'+i%26))))
		ws = append(ws, `model for numeric field "x" not applied: model carries no predictors`)
	}
	ws = append(ws, `model for numeric field "regard" not applied: predictor "brand" carries unsupported kind "numeric"`)

	groups := GroupWarnings(ws)
	attention, info := CountWarningsNeedingAttention(groups)
	if attention != 1 {
		t.Errorf("attention count = %d, want 1 (only the unsupported-kind drop)", attention)
	}
	if info != 100 {
		t.Errorf("informational count = %d, want 100", info)
	}
	if groups[0].Kind != "model not applied" || !groups[0].Attention {
		t.Errorf("first group = %+v, want the attention-needing 'model not applied' group first", groups[0])
	}
}

// TestGroupWarnings_AttentionFirstThenByCount pins the ordering the
// terminal summary depends on: a rendering that shows only the first few
// groups must still show every group that needs attention, even when a
// thousand expected-outcome lines outnumber them.
func TestGroupWarnings_AttentionFirstThenByCount(t *testing.T) {
	var ws []string
	for i := 0; i < 1000; i++ {
		ws = append(ws, thinPairWarning("categorical", "a", "b", 3, MinPairObservations))
	}
	for i := 0; i < 5; i++ {
		ws = append(ws, thinPairWarning("numeric", "a", "b", 3, MinPairObservations))
	}
	ws = append(ws, modelSkipWarning("nps", "rank deficient"))

	groups := GroupWarnings(ws)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	if groups[0].Kind != "model skipped" {
		t.Errorf("groups[0].Kind = %q, want the attention group first despite being the smallest", groups[0].Kind)
	}
	if groups[1].Kind != "thin categorical pair" || groups[1].Count() != 1000 {
		t.Errorf("groups[1] = %q x%d, want thin categorical pair x1000", groups[1].Kind, groups[1].Count())
	}
	if groups[2].Kind != "thin numeric pair" || groups[2].Count() != 5 {
		t.Errorf("groups[2] = %q x%d, want thin numeric pair x5", groups[2].Kind, groups[2].Count())
	}
	if !strings.HasPrefix(groups[1].Members[0], "thin categorical pair ") {
		t.Errorf("member text lost: %q", groups[1].Members[0])
	}
}

// TestGroupWarnings_EmptyIsNil lets every caller render unconditionally.
func TestGroupWarnings_EmptyIsNil(t *testing.T) {
	if got := GroupWarnings(nil); got != nil {
		t.Errorf("GroupWarnings(nil) = %v, want nil", got)
	}
	if got := GroupWarnings([]string{}); got != nil {
		t.Errorf("GroupWarnings(empty) = %v, want nil", got)
	}
	attention, info := CountWarningsNeedingAttention(nil)
	if attention != 0 || info != 0 {
		t.Errorf("counts over no groups = (%d, %d), want (0, 0)", attention, info)
	}
}
