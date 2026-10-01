package pulse_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// singleKeyCrosstabRequest is a fusable crosstab whose two axes are both
// keyed by gt: rows on the categorical region, columns on the nullable
// f64 score (every seventh row is null, so the null-key drop is
// exercised on the fused arm).
func singleKeyCrosstabRequest(gt types.GroupType) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "parity.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: gt, Field: "region"}},
			Columns: []*types.Group{{Type: gt, Field: "score"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "total"},
		},
	}
}

// TestExtensions_SingleKeyGrouperFusesCrosstab pins that a single-key
// extension grouper (extend.StreamingGrouper, KeyForRow only) takes the
// fused crosstab arm exactly like its built-in twin GROUP_CATEGORY: the
// gate admits it, the engine drives it per record through KeyForRow and
// never through the buffered Group, and the matrix — null keys
// included — is identical to the built-in's.
func TestExtensions_SingleKeyGrouperFusesCrosstab(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	for _, mode := range parityModes(large) {
		if mode.decorate != nil {
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			probe := &parityProbe{}
			p, path := mode.open(t, grouperParitySuite().register(probe))
			ctx := context.Background()
			schema := paritySchema(t)
			reg := pulse.ServiceForTest(p).Extensions()

			bReq, eReq := singleKeyCrosstabRequest(types.GROUP_CATEGORY), singleKeyCrosstabRequest(grpParityCategory)
			bReq.Cohort.Filename, eReq.Cohort.Filename = path, path
			if ok, why := processing.CanFuseCrosstab(bReq, schema, reg); !ok {
				t.Fatalf("CanFuseCrosstab(built-in GROUP_CATEGORY) = false (%s), want true", why)
			}
			if ok, why := processing.CanFuseCrosstab(eReq, schema, reg); !ok {
				t.Errorf("CanFuseCrosstab(extension %s) = false (%s), want true", grpParityCategory, why)
			}

			builtin, err := p.Process(ctx, bReq)
			if err != nil {
				t.Fatalf("built-in crosstab: %v", err)
			}
			probe.reset()
			ext, err := p.Process(ctx, eReq)
			if err != nil {
				t.Fatalf("extension crosstab: %v", err)
			}
			if probe.online.Load() == 0 || probe.buffered.Load() != 0 {
				t.Errorf("extension grouper path: KeyForRow=%d Group=%d, want the fused per-record arm only",
					probe.online.Load(), probe.buffered.Load())
			}
			// The axis header echoes the operator type; that is the only
			// permitted difference.
			b := mustJSON(t, builtin.Crosstab)
			e := strings.ReplaceAll(mustJSON(t, ext.Crosstab), `"`+string(grpParityCategory)+`"`, `"`+string(types.GROUP_CATEGORY)+`"`)
			if b != e {
				t.Errorf("crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
			}
			if !strings.Contains(b, `"present":true`) || !strings.Contains(b, `"north"`) || !strings.Contains(b, `"3.5"`) {
				t.Fatalf("built-in crosstab lacks the expected cells; parity over nothing proves nothing: %s", b)
			}
			bm, em := mustJSON(t, builtin.Metadata), mustJSON(t, ext.Metadata)
			if bm != em {
				t.Errorf("Metadata differs\nbuiltin:   %s\nextension: %s", bm, em)
			}
		})
	}
}
