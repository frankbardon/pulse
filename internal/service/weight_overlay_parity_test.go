package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Inferential-overlay parity harness (weighting-inferential E3-S3,
// .claude/reference/weighting.md "Overlays"). Every overlay kind the
// manifest advertises weight_kinds for runs on a weighted host through
// the shared arms of the aggregator / test harnesses:
//
//   - Unity (TestWeightOverlayUnityParity): an all-1.0 weight answers
//     BYTE-identically to no weight once the layers' sum_weights /
//     n_eff are checked (n_eff = sum_weights under probability, absent
//     under frequency) and shed — on every host the kind runs on.
//   - Expansion (TestWeightOverlayFrequencyExpansionParity): integer
//     frequency weights answer like the physically duplicated rows run
//     unweighted, and differently from the unweighted host over the
//     same rows (the weight reached the layer).
//   - Scale (TestWeightOverlayProbabilityScaleInvariance): kind
//     probability has no expansion identity (N* is Kish n_eff), so its
//     arm is scale invariance: weights c·f answer like f, the layer's
//     sum_weights moves by c and its n_eff does not — and the layer
//     differs from the frequency layer on the same weights (n_eff, not
//     Σw, reached it).
//
// Hosts: the per-Request crosstab (Request.Overlays) on both crosstab
// arms — fused where the unweighted twin fuses, buffered via the
// always-true FILTER_EXPRESSION steer — and the Compose overlay host.
// Weight sources: the request (each Compose slot's), the slot (the
// crosstab cell / series aggregations — the slot-only host the
// pre-U12 code ran on raw row counts) and Options.DefaultWeight. The
// case table is held total over the manifest's overlay weight_kinds by
// the shared coverage helper (weight_coverage_test.go).

// overlayParityCase is one host request carrying overlays under test.
type overlayParityCase struct {
	name  string
	kinds []types.OverlayKind
	// req is a types.Request (compose false) or types.ComposedRequest
	// as JSON with a %COHORT% placeholder.
	req     string
	compose bool
	// weightedOnly: the kinds read weighted moments and have no
	// unweighted form — no unity arm, and the expansion arm compares
	// with the expanded cohort under a unity frequency weight.
	weightedOnly bool
	// noFloor: the host carries no weighted floor for the kinds to read
	// (a series row holds a scalar mean; variance and n come from the
	// spec's params), so the weight reaches the layer only through the
	// host's weighted means: no Parameters, and the probability and
	// frequency layers coincide.
	noFloor bool
	// noParams: the kind keeps its own n_basis contract and stamps no
	// weight Parameters on its summary.
	noParams bool
	// totals, per layer index, are summary keys that echo the host's Σw
	// cells (a χ² layer's observed min / max): they move by c when
	// every weight is scaled by c.
	totals map[int][]string
}

func overlayParityCases() []overlayParityCase {
	xt := func(rows, cols, cell string, extra string) string {
		return `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"` + rows + `"}],"columns":[{"type":"GROUP_CATEGORY","field":"` + cols + `"}],` +
			`"cell":` + cell + `,"margins":{"rows":true,"columns":true,"grand":true}` + extra + `}`
	}
	const welford = `{"type":"AGG_WELFORD","field":"x","label":"cell"}`
	const count = `{"type":"AGG_COUNT","field":"o","label":"cell"}`
	const average = `{"type":"AGG_AVERAGE","field":"x","label":"cell"}`
	const seriesParams = `{"variance_target":80,"variance_ref":80,"sample_size_target":150,"sample_size_ref":180}`
	process := func(body, overlays string) string {
		return `{"cohort":{"filename":"%COHORT%"},` + body + `,"overlays":[` + overlays + `]}`
	}
	slot := func(label, filter, body string) string {
		f := ""
		if filter != "" {
			f = `"filterers":[{"type":"FILTER_EXPRESSION","expression":"` + filter + `"}],`
		}
		return `{"label":"` + label + `","cohort":{"filename":"%COHORT%"},` + f + body + `}`
	}
	compose := func(slots []string, overlays string) string {
		return `{"requests":[` + strings.Join(slots, ",") + `],"overlays":[` + overlays + `]}`
	}
	return []overlayParityCase{
		{
			name:  "pairwise_welch",
			kinds: []types.OverlayKind{types.OverlayKindPairwiseWelchT},
			req:   process(xt("k", "h", welford, ""), `{"name":"pw","kind":"OVERLAY_PAIRWISE_WELCH_T","scope":"row"}`),
		},
		{
			name: "chisq",
			kinds: []types.OverlayKind{types.OverlayKindChiSqMatrix, types.OverlayKindChiSqRow,
				types.OverlayKindChiSqCol},
			totals: map[int][]string{0: {"min", "max"}},
			req: process(xt("g", "o", count, ""),
				`{"name":"m","kind":"OVERLAY_CHISQ_MATRIX","scope":"matrix"},`+
					`{"name":"r","kind":"OVERLAY_CHISQ_ROW","scope":"row"},`+
					`{"name":"c","kind":"OVERLAY_CHISQ_COL","scope":"column"}`),
		},
		{
			name:  "fisher",
			kinds: []types.OverlayKind{types.OverlayKindFisherExactCell},
			req:   process(xt("h", "o", count, ""), `{"name":"f","kind":"OVERLAY_FISHER_EXACT_CELL","scope":"cell"}`),
		},
		{
			// Row-normalized Σw shares (p̂ = Σw_cell/Σw_row), n = the
			// cell's N* (n_source omitted).
			name:  "pairwise_prop",
			kinds: []types.OverlayKind{types.OverlayKindPairwisePropZ},
			req: process(xt("g", "o", count, `,"normalize":"row"`),
				`{"name":"pz","kind":"OVERLAY_PAIRWISE_PROP_Z","scope":"column","params":{"p_source":"cell_value"}}`),
		},
		{
			name:         "pairwise_weighted_z",
			kinds:        []types.OverlayKind{types.OverlayKindPairwiseWeightedTwoMeansZ},
			weightedOnly: true,
			noParams:     true,
			req: process(xt("k", "h", `{"type":"AGG_AVERAGE","field":"y","label":"cell"}`, ""),
				`{"name":"wz","kind":"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z","scope":"row","params":{"n_basis":"%NBASIS%"}}`),
		},
		{
			name:    "compose_mean_cells",
			compose: true,
			kinds:   []types.OverlayKind{types.OverlayKindTCell, types.OverlayKindZCell},
			req: compose([]string{slot("total", "", xt("k", "h", welford, "")), slot("sub", "id % 5 != 0", xt("k", "h", welford, ""))},
				`{"name":"t","kind":"OVERLAY_T_CELL","scope":"cell","reference":"total","targets":["sub"]},`+
					`{"name":"z","kind":"OVERLAY_Z_CELL","scope":"cell","reference":"total","targets":["sub"]}`),
		},
		{
			// A grouped Process emits AGG_WELFORD as a Go struct the
			// series reader does not take (the layer comes back empty,
			// weighted or not), so the series kinds run on their
			// scalar-mean arm: a weighted AGG_AVERAGE, spread and n from
			// params.
			name:    "compose_mean_series",
			compose: true,
			noFloor: true,
			kinds:   []types.OverlayKind{types.OverlayKindTVsRef, types.OverlayKindZVsRef},
			req: compose([]string{
				slot("total", "", `"groups":[{"type":"GROUP_CATEGORY","field":"k"}],"aggregations":[`+average+`]`),
				slot("sub", "id % 5 != 0", `"groups":[{"type":"GROUP_CATEGORY","field":"k"}],"aggregations":[`+average+`]`)},
				`{"name":"t","kind":"OVERLAY_T_VS_REF","scope":"group","reference":"total","targets":["sub"],"params":`+seriesParams+`},`+
					`{"name":"z","kind":"OVERLAY_Z_VS_REF","scope":"group","reference":"total","targets":["sub"],"params":`+seriesParams+`}`),
		},
		{
			name:    "compose_proportions",
			compose: true,
			kinds: []types.OverlayKind{types.OverlayKindChiSqVsRef, types.OverlayKindPropZCell,
				types.OverlayKindPropZPanel},
			req: compose([]string{slot("total", "", xt("h", "o", count, "")), slot("a", "id % 5 != 0", xt("h", "o", count, "")),
				slot("b", "id % 7 != 0", xt("h", "o", count, ""))},
				`{"name":"cv","kind":"OVERLAY_CHISQ_VS_REF","scope":"matrix","reference":"total","targets":["a"]},`+
					`{"name":"pc","kind":"OVERLAY_PROP_Z_CELL","scope":"cell","reference":"total","targets":["a"]},`+
					`{"name":"pp","kind":"OVERLAY_PROP_Z_PANEL","scope":"cell","reference":"total","targets":["a","b"]}`),
		},
	}
}

// overlayParitySource is how the weight reaches the overlay's host.
type overlayParitySource struct {
	name string
	kind types.WeightKind
	// apply weights one host request (a Process request or a Compose
	// slot) or the service.
	apply func(req *types.Request, s *Service, spec types.WeightSpec)
}

func overlayParitySources() []overlayParitySource {
	request := func(req *types.Request, _ *Service, spec types.WeightSpec) { req.Weight = &spec }
	slot := func(req *types.Request, _ *Service, spec types.WeightSpec) {
		if req.Crosstab != nil {
			req.Crosstab.Cell.Weight = types.SlotWeightOf(spec)
		}
		for _, a := range req.Aggregations {
			if !strings.HasPrefix(a.Label, paritySteer) {
				a.Weight = types.SlotWeightOf(spec)
			}
		}
	}
	options := func(_ *types.Request, s *Service, spec types.WeightSpec) { s.SetDefaultWeight(&spec) }
	var out []overlayParitySource
	for _, k := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		out = append(out,
			overlayParitySource{name: "request_" + string(k), kind: k, apply: request},
			overlayParitySource{name: "slot_" + string(k), kind: k, apply: slot},
			overlayParitySource{name: "options_" + string(k), kind: k, apply: options})
	}
	return out
}

// kinds is the weight kinds every overlay of the case advertises.
func (c overlayParityCase) weightKinds() []types.WeightKind {
	adv := manifestWeightKinds(weightSurfaceOverlays)
	var out []types.WeightKind
	for _, k := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		all := true
		for _, ok := range c.kinds {
			all = all && slices.Contains(adv[string(ok)], k)
		}
		if all {
			out = append(out, k)
		}
	}
	return out
}

func (c overlayParityCase) sources(kind types.WeightKind) []overlayParitySource {
	var out []overlayParitySource
	for _, s := range overlayParitySources() {
		if s.kind == kind && slices.Contains(c.weightKinds(), kind) {
			out = append(out, s)
		}
	}
	return out
}

// overlayArm is one Process crosstab arm, or the Compose host.
type overlayArm struct {
	name  string
	fused bool
}

func (c overlayParityCase) arms() []overlayArm {
	if c.compose {
		return []overlayArm{{name: "compose"}}
	}
	return []overlayArm{{name: "buffered"}, {name: "fused", fused: true}}
}

// runOverlayCase runs c over path on arm with the weight src (nil:
// unweighted) of field "w" and kind; returns its layers, or nil when
// the arm does not apply to the unweighted request (fused only).
func runOverlayCase(t *testing.T, store *parityStore, c overlayParityCase, arm overlayArm, src *overlayParitySource, kind types.WeightKind) []types.OverlayLayer {
	t.Helper()
	svc := New(store.mem)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	nBasis := "weights"
	if kind == types.WeightKindProbability {
		nBasis = "kish"
	}
	raw := strings.NewReplacer("%COHORT%", store.paths[paritySingleFile], "%NBASIS%", nBasis).Replace(c.req)
	spec := types.WeightSpec{Field: "w", Kind: kind}
	if c.compose {
		var req types.ComposedRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if src != nil {
			for _, r := range req.Requests {
				src.apply(r, svc, spec)
			}
		}
		resp, err := svc.Compose(context.Background(), &req)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		return resp.Overlays
	}
	var req types.Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !arm.fused {
		req.Filterers = append(req.Filterers, crosstabBufferedSteer())
	} else if ok, _ := processing.CanFuseCrosstab(&req, paritySchema(), svc.extensions); !ok {
		return nil // the unweighted twin never fuses: not applicable
	}
	var def *types.WeightSpec
	if src != nil {
		src.apply(&req, svc, spec)
		def = svc.defaultWeight
	}
	fused, why := processing.CanFuseCrosstab(processing.StampWeights(&req, def), paritySchema(), svc.extensions)
	switch {
	case fused == arm.fused:
	default:
		t.Fatalf("the request does not take the %s arm (fusable=%v, %q)", arm.name, fused, why)
	}
	resp, err := svc.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process (%s): %v", arm.name, err)
	}
	return resp.Overlays
}

// overlayWire is a layer slice's wire form with every warning reduced
// to its code (a warning's prose may print counts).
func overlayWire(t *testing.T, layers []types.OverlayLayer) any {
	t.Helper()
	v := toJSONValue(t, layers)
	for _, l := range v.([]any) {
		m := l.(map[string]any)
		if ws, ok := m["warnings"].([]any); ok {
			codes := make([]any, len(ws))
			for i, w := range ws {
				codes[i] = w.(map[string]any)["code"]
			}
			m["warnings"] = codes
		}
	}
	return v
}

// weightParams is one Parameters map's weight keys as read.
type weightParams struct{ sumW, nEff float64 }

// shedOverlayWeightKeys removes sum_weights / n_eff from every
// `parameters` map of the wire layers (dropping a map they leave
// empty) and returns what it found, path → keys. A map holding n_eff
// without sum_weights is a defect.
func shedOverlayWeightKeys(t *testing.T, v any) map[string]weightParams {
	t.Helper()
	found := map[string]weightParams{}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if p, ok := x["parameters"].(map[string]any); ok {
				sw, hasSW := p["sum_weights"].(float64)
				ne, hasNE := p["n_eff"].(float64)
				if hasNE && !hasSW {
					t.Errorf("%s: n_eff without sum_weights: %v", path, p)
				}
				if hasSW {
					found[path] = weightParams{sumW: sw, nEff: ne}
					if !hasNE {
						found[path] = weightParams{sumW: sw, nEff: math.NaN()}
					}
				}
				delete(p, "sum_weights")
				delete(p, "n_eff")
				if len(p) == 0 {
					delete(x, "parameters")
				}
			}
			for _, k := range sortedNames(x) {
				walk(path+"."+k, x[k])
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), e)
			}
		}
	}
	walk("", v)
	return found
}

// assertOverlayWeightParams: the weighted layers report a weight floor
// on every layer, n_eff exactly when the kind is probability — none on
// a noFloor case.
func assertOverlayWeightParams(t *testing.T, c overlayParityCase, found map[string]weightParams, kind types.WeightKind) {
	t.Helper()
	if c.noFloor || c.noParams {
		if len(found) != 0 {
			t.Errorf("a floor-less host reports weight parameters %v", found)
		}
		return
	}
	layers := len(c.kinds)
	perLayer := map[string]bool{}
	for path, p := range found {
		layer, _, _ := strings.Cut(path, "]")
		perLayer[layer] = true
		if !(p.sumW > 0) {
			t.Errorf("%s: sum_weights %v", path, p.sumW)
		}
		if math.IsNaN(p.nEff) == (kind == types.WeightKindProbability) {
			t.Errorf("%s: n_eff %v under kind %s", path, p.nEff, kind)
		}
	}
	if len(perLayer) != layers {
		t.Errorf("weight floor on %d of %d layers: %v", len(perLayer), layers, found)
	}
}

func assertLayerKinds(t *testing.T, kinds []types.OverlayKind, layers []types.OverlayLayer) {
	t.Helper()
	if len(layers) != len(kinds) {
		t.Fatalf("%d layers, want %d (%v)", len(layers), len(kinds), kinds)
	}
	for i, k := range kinds {
		if layers[i].Kind != k {
			t.Fatalf("layer %d is %s, want %s", i, layers[i].Kind, k)
		}
	}
}

// assertOverlayParityCoverage: the case table covers every (overlay
// kind, weight kind) the manifest advertises, and nothing else.
func assertOverlayParityCoverage(t *testing.T) {
	t.Helper()
	have := map[string][]types.WeightKind{}
	for _, c := range overlayParityCases() {
		if len(c.weightKinds()) == 0 {
			t.Errorf("case %s: its kinds %v share no advertised weight kind", c.name, c.kinds)
		}
		for _, ok := range c.kinds {
			for _, k := range c.weightKinds() {
				if !slices.Contains(have[string(ok)], k) {
					have[string(ok)] = append(have[string(ok)], k)
				}
			}
		}
	}
	assertWeightKindCoverage(t, weightSurfaceOverlays, have, "overlayParityCases, weight_overlay_parity_test.go")
}

func TestWeightOverlayUnityParity(t *testing.T) {
	t.Run("coverage", assertOverlayParityCoverage)
	store := newParityStore(t, "ovp_unity", func(n int) []parityRow { return parityRows(n, unityWeight) })
	ran := map[string]int{}
	for _, c := range overlayParityCases() {
		if c.weightedOnly {
			continue // no unweighted form to be identical to
		}
		for _, arm := range c.arms() {
			t.Run(c.name+"/"+arm.name, func(t *testing.T) {
				base := runOverlayCase(t, store, c, arm, nil, "")
				if base == nil {
					t.Logf("the unweighted host never takes the %s arm; not applicable", arm.name)
					return
				}
				assertLayerKinds(t, c.kinds, base)
				bw := overlayWire(t, base)
				if found := shedOverlayWeightKeys(t, bw); len(found) != 0 {
					t.Fatalf("the unweighted host reports a weight floor: %v", found)
				}
				want := mustMarshal(t, bw)
				for _, k := range c.weightKinds() {
					for _, src := range c.sources(k) {
						t.Run(src.name, func(t *testing.T) {
							got := overlayWire(t, runOverlayCase(t, store, c, arm, &src, k))
							found := shedOverlayWeightKeys(t, got)
							assertOverlayWeightParams(t, c, found, k)
							for path, p := range found {
								if k == types.WeightKindProbability && p.nEff != p.sumW {
									t.Errorf("%s: unity n_eff %v, want sum_weights %v", path, p.nEff, p.sumW)
								}
							}
							if g := mustMarshal(t, got); !bytes.Equal(g, want) {
								t.Errorf("unity weight changed the layers:\n weighted   %s\n unweighted %s", g, want)
							}
							ran[arm.name]++
						})
					}
				}
			})
		}
	}
	for _, arm := range []string{"buffered", "fused", "compose"} {
		if ran[arm] == 0 {
			t.Errorf("no overlay ran weighted on the %s arm: that half is vacuous", arm)
		}
	}
}

// overlayClose compares two decoded wire values, numbers within tol
// relative (absolute near zero).
func overlayClose(t *testing.T, where string, got, want any, tol float64) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for _, k := range sortedNames(w) {
			overlayClose(t, where+"."+k, g[k], w[k], tol)
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for i := range w {
			overlayClose(t, fmt.Sprintf("%s[%d]", where, i), g[i], w[i], tol)
		}
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(g-w) > tol*math.Max(math.Abs(w), 1e-3) {
			t.Errorf("%s = %v, want %v", where, got, w)
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", where, got, want)
		}
	}
}

// layerFigures is a layer slice's wire payloads + summaries with the
// weight keys shed — what "the same answer" means across weights.
func layerFigures(t *testing.T, layers []types.OverlayLayer) (any, map[string]weightParams) {
	t.Helper()
	v := overlayWire(t, layers)
	return v, shedOverlayWeightKeys(t, v)
}

func TestWeightOverlayFrequencyExpansionParity(t *testing.T) {
	t.Run("coverage", assertOverlayParityCoverage)
	weighted := newParityStore(t, "ovp_weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "ovp_expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	ran := 0
	for _, c := range overlayParityCases() {
		for _, arm := range c.arms() {
			t.Run(c.name+"/"+arm.name, func(t *testing.T) {
				var want, raw []types.OverlayLayer
				if c.weightedOnly {
					// The expanded rows carry w = 1: a unity frequency
					// weight is the weighted-moment host of the
					// duplicated cohort.
					unity := overlayParitySources()[0]
					want = runOverlayCase(t, expanded, c, arm, &unity, types.WeightKindFrequency)
				} else {
					want = runOverlayCase(t, expanded, c, arm, nil, "")
					raw = runOverlayCase(t, weighted, c, arm, nil, "")
				}
				if want == nil {
					t.Logf("the unweighted host never takes the %s arm; not applicable", arm.name)
					return
				}
				wv, _ := layerFigures(t, want)
				for _, src := range c.sources(types.WeightKindFrequency) {
					t.Run(src.name, func(t *testing.T) {
						got := runOverlayCase(t, weighted, c, arm, &src, types.WeightKindFrequency)
						assertLayerKinds(t, c.kinds, got)
						gv, found := layerFigures(t, got)
						assertOverlayWeightParams(t, c, found, types.WeightKindFrequency)
						overlayClose(t, "layers", gv, wv, 1e-9)
						if raw != nil {
							rv, _ := layerFigures(t, raw)
							for _, i := range sameLayers(t, gv, rv) {
								t.Errorf("%s: the weighted layer equals the unweighted host's: the weight never reached it", c.kinds[i])
							}
						}
						ran++
					})
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no overlay ran under a frequency weight")
	}
}

// overlayScale is c in the scaled twin c·f.
const overlayScale = 0.37

// shedOverlayTotals checks the case's Σw-echo summary keys moved by
// overlayScale between the scaled (got) and base (want) wire layers,
// then removes them from both.
func shedOverlayTotals(t *testing.T, c overlayParityCase, got, want any) {
	t.Helper()
	for li, keys := range c.totals {
		gs := got.([]any)[li].(map[string]any)["summary"].(map[string]any)
		ws := want.([]any)[li].(map[string]any)["summary"].(map[string]any)
		for _, k := range keys {
			g, gok := gs[k].(float64)
			w, wok := ws[k].(float64)
			if !gok || !wok || math.Abs(g-overlayScale*w) > 1e-9*math.Abs(w) {
				t.Errorf("layer %d summary.%s = %v, want %v × %v", li, k, gs[k], ws[k], overlayScale)
			}
			delete(gs, k)
			delete(ws, k)
		}
	}
}

func TestWeightOverlayProbabilityScaleInvariance(t *testing.T) {
	t.Run("coverage", assertOverlayParityCoverage)
	base := newParityStore(t, "ovp_prob", func(n int) []parityRow { return parityRows(n, freqWeight) })
	scaled := newParityStore(t, "ovp_scaled", func(n int) []parityRow {
		return parityRows(n, func(i int) float64 { return freqWeight(i) * overlayScale })
	})
	ran := 0
	for _, c := range overlayParityCases() {
		srcs := c.sources(types.WeightKindProbability)
		if len(srcs) == 0 {
			continue // frequency-only kinds (the coverage subtest pins which)
		}
		for _, arm := range c.arms() {
			for _, src := range srcs {
				t.Run(c.name+"/"+arm.name+"/"+src.name, func(t *testing.T) {
					want := runOverlayCase(t, base, c, arm, &src, types.WeightKindProbability)
					if want == nil {
						return
					}
					wv, wp := layerFigures(t, want)
					assertOverlayWeightParams(t, c, wp, types.WeightKindProbability)
					got := runOverlayCase(t, scaled, c, arm, &src, types.WeightKindProbability)
					assertLayerKinds(t, c.kinds, got)
					gv, gp := layerFigures(t, got)
					shedOverlayTotals(t, c, gv, wv)
					overlayClose(t, "layers", gv, wv, 1e-9)
					for _, path := range sortedNames(wp) {
						g, w := gp[path], wp[path]
						if math.Abs(g.sumW-overlayScale*w.sumW) > 1e-9*w.sumW || math.Abs(g.nEff-w.nEff) > 1e-9*w.nEff {
							t.Errorf("%s: %+v vs %+v, want sum_weights × %v and n_eff unchanged", path, g, w, overlayScale)
						}
					}
					// The same weights read as frequency: n_eff, not Σw,
					// must be what the probability layers used.
					fsrc := src
					fsrc.kind = types.WeightKindFrequency
					if !c.noFloor && slices.Contains(c.weightKinds(), types.WeightKindFrequency) {
						fv, _ := layerFigures(t, runOverlayCase(t, base, c, arm, &fsrc, types.WeightKindFrequency))
						for _, i := range sameLayers(t, fv, wv) {
							t.Errorf("%s: the probability layer equals the frequency layer: n_eff never reached it", c.kinds[i])
						}
					}
					ran++
				})
			}
		}
	}
	if ran == 0 {
		t.Fatal("no overlay ran under a probability weight")
	}
}

// TestWeightOverlaySlotOnlyHostReadsNStar pins the slot-only-host fix
// (weighting-inferential E3-S1 / E3-S2): a host weighted ONLY by its
// own crosstab cell (or series aggregation) weight — the shape U11 let
// every inferential overlay run on, reading the Welford triple's raw n
// and the Σw cells as counts — now answers exactly as the same host
// weighted by the request: under kind probability its layers report
// n_eff < sum_weights and differ from the frequency reading of the same
// weights (N* is Kish n_eff, not Σw nor the row count); under kind
// frequency they equal the expanded cohort (the expansion harness
// above). Reverting either fix fails here.
func TestWeightOverlaySlotOnlyHostReadsNStar(t *testing.T) {
	store := newParityStore(t, "ovp_slot", func(n int) []parityRow { return parityRows(n, freqWeight) })
	srcs := map[string]overlayParitySource{}
	for _, s := range overlayParitySources() {
		srcs[s.name] = s
	}
	compared := 0
	for _, c := range overlayParityCases() {
		if c.noFloor || c.noParams {
			continue // the weight reaches these only through the host's means
		}
		for _, k := range c.weightKinds() {
			slot, request := srcs["slot_"+string(k)], srcs["request_"+string(k)]
			for _, arm := range c.arms() {
				t.Run(c.name+"/"+arm.name+"/"+string(k), func(t *testing.T) {
					got := runOverlayCase(t, store, c, arm, &slot, k)
					if got == nil {
						return
					}
					want := runOverlayCase(t, store, c, arm, &request, k)
					gv, gp := layerFigures(t, got)
					wv, wp := layerFigures(t, want)
					if !bytes.Equal(mustMarshal(t, gv), mustMarshal(t, wv)) {
						t.Errorf("slot-only host differs from the request-weighted host:\n slot    %s\n request %s",
							mustMarshal(t, gv), mustMarshal(t, wv))
					}
					assertOverlayWeightParams(t, c, gp, k)
					if fmt.Sprint(gp) != fmt.Sprint(wp) {
						t.Errorf("weight parameters %v, request-weighted %v", gp, wp)
					}
					if k != types.WeightKindProbability {
						compared++
						return
					}
					for path, p := range gp {
						if !(p.nEff < p.sumW) {
							t.Errorf("%s: n_eff %v not below sum_weights %v — N* is not Kish n_eff", path, p.nEff, p.sumW)
						}
					}
					if slices.Contains(c.weightKinds(), types.WeightKindFrequency) {
						fsrc := srcs["slot_frequency"]
						fv, _ := layerFigures(t, runOverlayCase(t, store, c, arm, &fsrc, types.WeightKindFrequency))
						for _, i := range sameLayers(t, fv, gv) {
							t.Errorf("%s: the slot-only probability layer reads like the frequency one: Σw, not n_eff, reached it", c.kinds[i])
						}
					}
					compared++
				})
			}
		}
	}
	if compared == 0 {
		t.Fatal("no slot-only host compared")
	}
}

// sameLayers returns the indices at which two wire layer slices carry
// byte-identical layers.
func sameLayers(t *testing.T, a, b any) []int {
	t.Helper()
	as, bs := a.([]any), b.([]any)
	var out []int
	for i := range as {
		if i < len(bs) && bytes.Equal(mustMarshal(t, as[i]), mustMarshal(t, bs[i])) {
			out = append(out, i)
		}
	}
	return out
}
