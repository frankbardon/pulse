package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestReferenceOracle_ComposeWelfordSeriesVsRef (U36 E3-S1): Compose
// OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF over an ENGINE AGG_WELFORD series
// — two grouped slots on a real cohort, each row's leg read from the
// slot's per-group components (mean, variance, n) — against R:
// t.test(sub, total)$p.value and the Welch-SE normal tail per group
// (internal/processing/testdata/reference/overlay_mean_vs_ref.json,
// scripts/reference/gen_overlays.R), relative 1e-10. The weighted
// series (N* off sum_weights / n_eff, the variance off m2) is pinned by
// the weight reference fixture's ov_compose_series_welford case; a
// slot built with components disabled is refused
// (TestComposeOverlay_WelfordComponentsDisabledRefused).
func TestReferenceOracle_ComposeWelfordSeriesVsRef(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("internal", "processing", "testdata", "reference", "overlay_mean_vs_ref.json"))
	if err != nil {
		t.Fatal(err)
	}
	if i := bytes.LastIndex(raw, []byte("\n// golden-hash: ")); i >= 0 {
		raw = raw[:i]
	}
	var doc struct {
		RVersion string `json:"r_version"`
		Cases    []struct {
			Name   string    `json:"name"`
			G      []string  `json:"g"`
			Seg    []string  `json:"seg"`
			X      []float64 `json:"x"`
			Groups []struct {
				G  string  `json:"g"`
				TP float64 `json:"t_p"`
				ZP float64 `json:"z_p"`
			} `json:"groups"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Cases) == 0 || doc.RVersion == "" {
		t.Fatalf("overlay_mean_vs_ref.json: %v (regenerate with `make reference`)", err)
	}
	ctx := context.Background()
	for _, k := range doc.Cases {
		t.Run(k.Name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			p, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			b, err := p.NewCohortBuilder(ctx, "wv.pulse", welfordSeriesSchema(), pulse.CohortBuilderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for i := range k.X {
				if err := b.Append(pulse.CohortRow{k.G[i], k.Seg[i], k.X[i]}); err != nil {
					t.Fatalf("Append(%d): %v", i, err)
				}
			}
			if _, err := b.Close(); err != nil {
				t.Fatal(err)
			}
			slot := func(label string, filt []*types.Filterer) *types.Request {
				return &types.Request{Label: label, Cohort: &types.Cohort{Filename: "wv.pulse"}, Filterers: filt,
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "x", Label: "m"}}}
			}
			spec := func(name string, kind types.OverlayKind) types.ComposeOverlaySpec {
				return types.ComposeOverlaySpec{Name: name, Kind: kind, Scope: types.OverlayScopeGroup,
					Reference: "total", Targets: []string{"sub"}}
			}
			out, err := p.Compose(ctx, &pulse.ComposedRequest{
				Requests: []*types.Request{slot("total", nil),
					slot("sub", []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "seg", Values: []string{"in"}}})},
				Overlays: []types.ComposeOverlaySpec{spec("t", types.OverlayKindTVsRef), spec("z", types.OverlayKindZVsRef)},
			})
			if err != nil {
				t.Fatalf("Compose: %v", err)
			}
			if len(out.Overlays) != 2 {
				t.Fatalf("overlays = %d, want 2", len(out.Overlays))
			}
			for li, layer := range out.Overlays {
				if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != len(k.Groups) {
					t.Fatalf("layer %s: %+v, want %d series entries", layer.Name, layer.Payload, len(k.Groups))
				}
				got := map[string]*float64{}
				for _, e := range layer.Payload.Series.Entries {
					got[fmt.Sprint(e.Key[0])] = e.Summary.Statistic
				}
				for _, g := range k.Groups {
					want := g.TP
					if li == 1 {
						want = g.ZP
					}
					v, ok := got["g="+g.G]
					if !ok || v == nil {
						t.Errorf("layer %s group %s: no p (entries %v)", layer.Name, g.G, got)
						continue
					}
					if math.IsNaN(*v) || math.Abs(*v-want) > 1e-10*math.Abs(want) {
						t.Errorf("layer %s group %s: p %.17g, R %.17g", layer.Name, g.G, *v, want)
					}
				}
			}
		})
	}
}

// welfordSeriesSchema is the oracle cohort: g and seg categorical, x f64.
func welfordSeriesSchema() encoding.Schema {
	cat := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			_, _ = d.Add(v)
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("east", "north", "west")},
		{Name: "seg", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat("in", "out")},
		{Name: "x", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Welford series oracle column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}
