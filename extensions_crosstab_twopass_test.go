package pulse_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// twoPassCrosstabRequest is a fusable-shaped crosstab (two streamable
// axes, a mergeable summable cell) whose cell folds the output of a
// two-pass attribute. Nothing but the attribute keeps it off the fused
// arm.
func twoPassCrosstabRequest(at types.AttributeType) *types.Request {
	return &types.Request{
		Cohort:     &types.Cohort{Filename: "parity.pulse"},
		Attributes: []*types.Attribute{{Type: at, Field: "score", Label: "derived"}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_DATE, Field: "day"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "derived", Label: "total"},
		},
	}
}

// TestExtensions_TwoPassAttributeCrosstabNotFused pins that a
// two_pass extension attribute keeps a crosstab off the fused arm,
// exactly as its built-in twin does. The fused walk runs row-local
// attributes inline and never drives a PrePass, so a fused two-pass
// attribute values every row against empty population stats — a wrong
// number, not an error. The gate, its defensive echo and the buffered
// result must all agree with ATTR_ZSCORE, on every cohort shape the
// parity harness covers (single file, >threshold single file with
// parallel decode, shard archive).
func TestExtensions_TwoPassAttributeCrosstabNotFused(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	for _, mode := range parityModes(large) {
		if mode.decorate != nil {
			// Request decoration is an aggregation-response device;
			// the undecorated small cohort is already covered.
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			probe := &parityProbe{}
			p, path := mode.open(t, attributeParitySuite().register(probe))
			ctx := context.Background()
			schema := paritySchema(t)
			reg := pulse.ServiceForTest(p).Extensions()

			bReq, eReq := twoPassCrosstabRequest(types.ATTR_ZSCORE), twoPassCrosstabRequest(attrParityZScore)
			bReq.Cohort.Filename, eReq.Cohort.Filename = path, path
			if ok, _ := processing.CanFuseCrosstab(bReq, schema, reg); ok {
				t.Error("CanFuseCrosstab(built-in ATTR_ZSCORE) = true, want false")
			}
			if ok, _ := processing.CanFuseCrosstab(eReq, schema, reg); ok {
				t.Errorf("CanFuseCrosstab(two_pass extension %s) = true, want false", attrParityZScore)
			}

			builtin, err := p.Process(ctx, bReq)
			if err != nil {
				t.Fatalf("built-in ATTR_ZSCORE crosstab: %v", err)
			}
			probe.reset()
			ext, err := p.Process(ctx, eReq)
			if err != nil {
				t.Fatalf("extension two_pass crosstab: %v", err)
			}
			if probe.buffered.Load() == 0 {
				t.Errorf("extension two_pass attribute was not driven through its buffered Compute (online=%d buffered=%d)",
					probe.online.Load(), probe.buffered.Load())
			}
			b, e := mustJSON(t, builtin.Crosstab), mustJSON(t, ext.Crosstab)
			if b != e {
				t.Errorf("crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
			}
			if !strings.Contains(b, `"present":true`) {
				t.Fatalf("built-in crosstab has no present cell; parity over nothing proves nothing: %s", b)
			}
			if bc, ec := mustJSON(t, builtin.Components), mustJSON(t, ext.Components); bc != ec {
				t.Errorf("Components differ\nbuiltin:   %s\nextension: %s", bc, ec)
			}
		})
	}
}
