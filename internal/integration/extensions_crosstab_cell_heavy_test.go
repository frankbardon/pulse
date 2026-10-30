//go:build heavy

package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testhook"
	"github.com/frankbardon/pulse/types"
)

// TestExtensions_DeclaredMarginCellFusesCrosstab pins that an extension
// cell aggregator declaring a non-recompute MarginReducibility takes
// the fused crosstab arm (gate admits it; the engine drives it only
// through UpdateRow, never the buffered Aggregate) and that the matrix,
// margins, auxiliary margin figures, Components and Metadata equal the
// built-in twin's in every execution mode.
func TestExtensions_DeclaredMarginCellFusesCrosstab(t *testing.T) {
	t.Parallel()

	large := parityLargeCohort(t.TempDir())
	for _, mode := range parityModes(large) {
		if mode.decorate != nil {
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			probe := &parityProbe{}
			p, path := mode.open(t, crosstabCellExtensions(probe))
			ctx := context.Background()
			schema := paritySchema(t)
			reg := testhook.Service(p).Extensions()
			for _, c := range crosstabCellCases() {
				t.Run(c.name, func(t *testing.T) {
					bReq, eReq := c.request(c.builtin, types.AGG_SUM), c.request(c.ext, aggXtSum)
					bReq.Cohort.Filename, eReq.Cohort.Filename = path, path
					if ok, why := processing.CanFuseCrosstab(bReq, schema, reg); ok != c.builtinFuses {
						t.Fatalf("CanFuseCrosstab(built-in %s) = %v (%s), want %v", c.builtin, ok, why, c.builtinFuses)
					}
					if ok, why := processing.CanFuseCrosstab(eReq, schema, reg); !ok {
						t.Errorf("CanFuseCrosstab(extension %s) = false (%s), want true", c.ext, why)
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
					if probe.online.Load() == 0 || probe.buffered.Load() != 0 || probe.merges.Load() != 0 {
						t.Errorf("extension cell path: online=%d buffered=%d merges=%d, want the fused per-record arm only",
							probe.online.Load(), probe.buffered.Load(), probe.merges.Load())
					}
					if c.field == "amount" && probe.decimalReads.Load() == 0 {
						t.Error("decimal cell: extension never reached DecimalValue")
					}
					b, e := mustJSON(t, builtin.Crosstab), renameXt(mustJSON(t, ext.Crosstab), c)
					if b != e {
						t.Errorf("Crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
					}
					for _, sub := range c.mustContain {
						if !strings.Contains(b+mustJSON(t, builtin.Components), sub) {
							t.Fatalf("built-in crosstab lacks %s; parity over nothing proves nothing: %s", sub, b)
						}
					}
					bc, ec := mustJSON(t, builtin.Components), renameXt(mustJSON(t, ext.Components), c)
					if bc != ec {
						t.Errorf("Components differ\nbuiltin:   %s\nextension: %s", bc, ec)
					}
					if bm, em := mustJSON(t, builtin.Metadata), mustJSON(t, ext.Metadata); bm != em {
						t.Errorf("Metadata differs\nbuiltin:   %s\nextension: %s", bm, em)
					}
				})
			}
		})
	}
}
