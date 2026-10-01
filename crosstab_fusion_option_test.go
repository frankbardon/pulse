package pulse

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// crosstabFusionReq is a crosstab the fusion gate admits (online AGG_SUM
// cell over two categorical axes), so the fused arm genuinely engages
// when fusion is enabled.
func crosstabFusionReq() *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "source"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "amount"},
		},
	}
}

// TestOptions_DisableCrosstabFusion: Options.DisableCrosstabFusion is
// wired to the service at New (the fused dispatch is switched off only
// when asked) and is output-transparent — the fused and buffered arms
// return byte-identical payloads for the same request.
func TestOptions_DisableCrosstabFusion(t *testing.T) {
	ctx := context.Background()
	fsys, _ := groupedTwinFS(t)
	var outs []string
	for _, disable := range []bool{false, true} {
		p, err := New(Options{FS: fsys, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if !disable {
			// Non-vacuity: the fused arm only means something while the
			// gate admits this request.
			schema, err := p.ResolveCanonicalSchema(ctx, "cohort.pulse")
			if err != nil {
				t.Fatalf("ResolveCanonicalSchema: %v", err)
			}
			if ok, why := processing.CanFuseCrosstab(crosstabFusionReq(), schema, nil); !ok {
				t.Fatalf("crosstab declined fusion (%s); the comparison would be vacuous", why)
			}
		}
		if got := p.svc.CrosstabFusionDisabled(); got != disable {
			t.Fatalf("Options{DisableCrosstabFusion: %v}: service fusion-disabled = %v", disable, got)
		}
		resp, err := p.Process(ctx, crosstabFusionReq())
		if err != nil {
			t.Fatalf("Process (disable=%v): %v", disable, err)
		}
		outs = append(outs, mustJSON(t, "crosstab", resp))
	}
	if outs[0] != outs[1] {
		t.Fatalf("fused vs DisableCrosstabFusion output differs:\n fused:    %s\n buffered: %s", outs[0], outs[1])
	}
}
