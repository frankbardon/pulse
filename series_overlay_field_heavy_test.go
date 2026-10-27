//go:build heavy

package pulse_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/types"
)

// TestSeriesOverlayField_ScoresNamedAggregation: params.field names an
// aggregation by OUTPUT LABEL and the overlay scores it. On every one
// of the thirteen kinds and every arm, field "orders" over [n, orders]
// equals the same overlay over [orders] alone, and absent field scores
// aggregations[0] (equal to [n] alone). The named figure must differ
// from the default on every kind (YoY runs annual, so its second point
// has a prior), so the equality is not two copies of the default.
func TestSeriesOverlayField_ScoresNamedAggregation(t *testing.T) {
	t.Parallel()

	// 13 kinds score any aggregation here; the three prior-period tests
	// (U47 #328) score only their measure family, and their params.field
	// is pinned by root TestVsPrior_* instead.
	if got := len(seriesfield.Kinds()); got != 16 {
		t.Fatalf("seriesfield.Kinds() = %d kinds, want 16", got)
	}
	if got := len(sfSpecs()); got != 13 {
		t.Fatalf("sfSpecs() = %d kinds, want 13", got)
	}
	f := newMGFixture(t, pulse.Options{})
	n := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_COUNT, Field: "id", Label: "n"} }
	orders := func() *types.Aggregation {
		return &types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "orders"}
	}
	for _, s := range sfSpecs() {
		if !seriesfield.Applies(s.spec.Kind) {
			t.Fatalf("%s missing from seriesfield", s.spec.Kind)
		}
		for _, mode := range sfModes() {
			t.Run(string(s.spec.Kind)+"/"+mode.name, func(t *testing.T) {
				run := func(spec types.OverlaySpec, aggs ...*types.Aggregation) string {
					req := mgRequest(f, []*types.Group{s.group()}, aggs...)
					req.Overlays = []types.OverlaySpec{spec}
					if env := predictEnvelope(t, f.p, f.fs, f.cohort, req); len(env.Errors) > 0 {
						t.Fatalf("predict: %+v", env.Errors)
					}
					resp, err := mode.run(t, f, req)
					if err != nil {
						t.Fatalf("run: %v", err)
					}
					return sfRenderLayer(t, resp)
				}
				named := run(withField(t, s.spec, "orders"), n(), orders())
				alone := run(s.spec, orders())
				if named != alone {
					t.Errorf("field orders over [n, orders]:\n %s\nwant [orders] alone:\n %s", named, alone)
				}
				def := run(s.spec, n(), orders())
				if want := run(s.spec, n()); def != want {
					t.Errorf("absent field over [n, orders]:\n %s\nwant [n] alone:\n %s", def, want)
				}
				if named == def {
					t.Errorf("field orders scored the same figures as aggregations[0]: %s", named)
				}
			})
		}
	}
}
