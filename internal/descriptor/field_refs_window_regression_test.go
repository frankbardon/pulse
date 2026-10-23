package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// field_refs_window_regression_test.go: a regression slot may name a
// same-request window label (transformed-predictors E1-S1) — only where
// the windows run over the record rows (vectors.WindowsOverRecords). A
// grouped / aggregated / crosstab shape refuses it with
// PULSE_REGRESSION_WINDOW_UNAVAILABLE; a weight never reads one.

func lagWindow(label string) *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "n", Label: label, OrderBy: []types.OrderKey{{Field: "m"}}}
}

// TestWindowPredictor_WalkAcceptsWindowLabelsOverRecords: every
// regression name slot reads a window label (explicit or default) in the
// record-row shape, with no refusal.
func TestWindowPredictor_WalkAcceptsWindowLabelsOverRecords(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{
			lagWindow("lag_n"),
			{Type: types.WIN_LAG, Field: "m", OrderBy: []types.OrderKey{{Field: "n"}}}, // default label WIN_LAG_m
		},
		Regressions: []*types.RegressionSpec{{
			Type: types.REG_OLS, Target: "WIN_LAG_m", Predictors: []string{"lag_n", "n"},
			Unpenalized: []string{"lag_n"},
			Covariance:  &types.RegressionCovariance{Cluster: "lag_n", OrderBy: "WIN_LAG_m"},
			JackknifeBy: "lag_n",
		}},
	}
	if got := FieldRefRefusals(req, fieldRefSchema(), nil); len(got) != 0 {
		t.Fatalf("window labels in a regression over records refused: %v", got)
	}
}

// TestWindowPredictor_WindowsSeeOnlyEarlierColumns (FR-2): admitting a
// window label to the regressions moves nothing for the windows
// themselves — a window reading a LATER window's label is still refused.
func TestWindowPredictor_WindowsSeeOnlyEarlierColumns(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "later", Label: "first", OrderBy: []types.OrderKey{{Field: "m"}}},
			lagWindow("later"),
		},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "n", Predictors: []string{"first", "later"}}},
	}
	sameNames(t, refusedNames(t, req), "later")
}

// TestWindowPredictor_GroupedShapeRefused: with groups, aggregations or
// a crosstab a window runs over output rows, so a regression naming its
// label is PULSE_REGRESSION_WINDOW_UNAVAILABLE {regression, field,
// reason: grouped_shape}; a truly unknown name keeps its message — and an
// extension feature's open column set does not admit the window label.
func TestWindowPredictor_GroupedShapeRefused(t *testing.T) {
	reg := func() []*types.RegressionSpec {
		return []*types.RegressionSpec{{Type: types.REG_OLS, Target: "n", Predictors: []string{"lag_n", "q_unknown"},
			Covariance: &types.RegressionCovariance{Cluster: "lag_n"}}}
	}
	shapes := map[string]*types.Request{
		"groups":       {Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}},
		"aggregations": {Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}}},
		"crosstab": {Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		}},
		"open walk": {Features: []*types.Feature{{Type: "FEAT_ACME_EMBED", Field: "n"}},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}},
	}
	for name, req := range shapes {
		t.Run(name, func(t *testing.T) {
			req.Windows = []*types.Window{lagWindow("lag_n")}
			req.Regressions = reg()
			var unavailable []*errors.CodedError
			unknown := false
			for _, ce := range FieldRefRefusals(req, fieldRefSchema(), nil) {
				switch {
				case ce.Code == errors.PULSE_REGRESSION_WINDOW_UNAVAILABLE:
					unavailable = append(unavailable, ce)
				case ce.Message == "regression references unknown predictor field: q_unknown":
					unknown = true
				}
			}
			if len(unavailable) != 2 {
				t.Fatalf("want 2 PULSE_REGRESSION_WINDOW_UNAVAILABLE (predictor, cluster), got %v", unavailable)
			}
			for _, ce := range unavailable {
				d := ce.Details
				if d["regression"] != string(types.REG_OLS) || d["field"] != "lag_n" || d["reason"] != "grouped_shape" {
					t.Errorf("details = %v", d)
				}
			}
			if unknown != (name != "open walk") {
				t.Errorf("unknown-name refusal present = %v (open walk accepts it)", unknown)
			}
		})
	}
}

// TestWindowPredictor_WeightRefused: a slot weight never reads a window
// label — refused in every shape with a message naming the window.
func TestWindowPredictor_WeightRefused(t *testing.T) {
	req := &types.Request{
		Windows: []*types.Window{lagWindow("lag_n")},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "n", Predictors: []string{"m"},
			Weight: types.SlotWeightOf(types.WeightSpec{Field: "lag_n", Kind: types.WeightKindFrequency})}},
	}
	got := FieldRefRefusals(req, fieldRefSchema(), nil)
	if len(got) != 1 || got[0].Code != errors.SERVICE_VALIDATION ||
		!strings.Contains(got[0].Message, "window output lag_n") || got[0].Details["window"] != "lag_n" {
		t.Fatalf("window-label weight refusal = %v", got)
	}
}

// TestWindowPredictor_LabelHelpersAgree (FR-8): predict's window label,
// the computed-column model's and the one derivation they share
// (windowparams.Label) name every window alike.
func TestWindowPredictor_LabelHelpersAgree(t *testing.T) {
	for _, w := range []*types.Window{
		{Type: types.WIN_LAG, Field: "x", Label: "lag"},
		{Type: types.WIN_LAG, Field: "x"},
		{Type: types.WIN_ROW_NUMBER},
		{Type: "WIN_ACME_CONST", Field: "x"},
	} {
		want := windowparams.Label(w)
		if got := windowLabel(w); got != want {
			t.Errorf("descriptor windowLabel(%+v) = %q, want %q", w, got, want)
		}
		if got := vectors.WindowLabel(w); got != want {
			t.Errorf("vectors.WindowLabel(%+v) = %q, want %q", w, got, want)
		}
	}
	if got := windowparams.Label(&types.Window{Type: types.WIN_LAG, Field: "x"}); got != "WIN_LAG_x" {
		t.Errorf("default label = %q, want WIN_LAG_x", got)
	}
}
