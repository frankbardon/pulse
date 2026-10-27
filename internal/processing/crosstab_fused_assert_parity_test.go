package processing

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestAssertCanFuse_RejectionsDeclinedByGate walks every condition
// FusedCrosstabState.AssertCanFuse refuses (the fused walk's defensive
// PROCESSING_INTERNAL echo) through the static gate: CanFuseCrosstab —
// crosstabfuse.Decide, the body predict's crosstab_fusable shares —
// must decline each one first, so a valid request never reaches the
// fused walk's PROCESSING_INTERNAL. The happy-path crosstab both admit.
func TestAssertCanFuse_RejectionsDeclinedByGate(t *testing.T) {
	schema := fusedCrosstabSchema(t)
	base := func() *types.Request {
		return &types.Request{Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "c"},
			Shape:   types.CrosstabShapeLong,
		}}
	}
	cases := []struct {
		name string
		mut  func(*types.Request)
	}{
		{"tests", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: types.TEST_T, Field: "value"}}
		}},
		{"post-tests", func(r *types.Request) {
			r.PostTests = []*types.Test{{Type: types.TEST_CHISQ}}
		}},
		{"features", func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "value"}}
		}},
		{"regressions", func(r *types.Request) {
			r.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "value", Predictors: []string{"region"}}}
		}},
		{"windows", func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "value", Label: "lag_v"}}
		}},
		{"joins", func(r *types.Request) {
			r.Joins = []*types.JoinSpec{{Right: "right.pulse", On: []types.OnPair{{LeftField: "value", RightField: "value"}}}}
		}},
		{"two-pass attribute", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "value", Label: "z"}}
		}},
		{"total() formula", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "share", Expression: "value / total(value)"}}
		}},
	}
	exts := &ExtensionRegistry{}
	if ok, reason := CanFuseCrosstab(base(), schema, exts); !ok {
		t.Fatalf("the happy-path crosstab declined fusion: %s", reason)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := base()
			c.mut(req)
			state, err := NewFusedCrosstabState(req.Crosstab, schema, exts)
			if err != nil {
				t.Fatalf("NewFusedCrosstabState: %v", err)
			}
			var ce *errors.CodedError
			if err := state.AssertCanFuse(req); !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
				t.Fatalf("AssertCanFuse = %v, want its PROCESSING_INTERNAL refusal (the case must exercise it)", err)
			}
			if ok, _ := CanFuseCrosstab(req, schema, exts); ok {
				t.Fatalf("CanFuseCrosstab admitted a request AssertCanFuse refuses: the fused arm would fail PROCESSING_INTERNAL")
			}
		})
	}
}
