package service

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Weighted reference fixtures for the inferential overlays
// (weighting-inferential E3-S3). TestWeightReferenceValues/overlays
// pins every overlay kind the manifest advertises weight_kinds for, per
// weight configuration, to figures an EXTERNAL tool computed offline
// over the significance-test fixture (weightRefTestRows): kind
// frequency is stock R on the rep()-expanded rows (test_reference.R,
// "ov_*" — chisq.test on the table and as a goodness-of-fit per row /
// column / target, fisher.test per cell, t.test, prop.test, and the
// normal tail on R's mean / var for the z kinds); kind probability is
// the closed form of the one formula rule on each leg or table
// (gen_weight_reference.py ov_closed_form), which must reproduce R on
// the frequency configuration before a row is written. Never
// hand-edited: regenerate with the generator.
//
// A case is a Process request fragment (a per-Request crosstab host,
// Request.Overlays) or a whole ComposedRequest (the Compose host; the
// case weight is set on every slot), plus figures keyed
// "<layer>/<path>": a wire path from the layer's root, where
// `cell[<row>|<col>]` addresses a matrix cell by its comma-joined axis
// keys (`#i` one element of a vector cell) and `entry[<key>]` a series
// entry. warnings lists the overlay warning codes the run must carry
// (the generator derives them from the same expected counts).
//
// Tolerances (relative): 1e-10 for p-values — every matrix cell is
// one, as is a series entry's statistic on the T / Z vs-ref kinds —
// and 1e-12 for statistics and parameters (weightRefOverlayTol).

type weightRefOverlayCase struct {
	weight, kind, name, request string
	figures                     map[string]float64
	warnings                    []string
	source                      string
}

func (c weightRefOverlayCase) compose() bool {
	return strings.HasPrefix(c.request, `{"requests"`)
}

// weightRefOverlayRun runs c under its weight and returns the layers'
// wire form and the overlay warning codes it carried (deduplicated,
// sorted).
func weightRefOverlayRun(t *testing.T, svc *Service, c weightRefOverlayCase) ([]any, []string) {
	t.Helper()
	spec := types.WeightSpec{Field: c.weight, Kind: weightRefKind(c.kind)}
	var (
		layers []types.OverlayLayer
		codes  []string
	)
	if c.compose() {
		var req types.ComposedRequest
		if err := json.Unmarshal([]byte(c.request), &req); err != nil {
			t.Fatalf("%s: request: %v", c.name, err)
		}
		for _, r := range req.Requests {
			r.Weight = &spec
		}
		resp, err := svc.Compose(context.Background(), &req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		layers = resp.Overlays
		for _, l := range layers {
			for _, w := range l.Warnings {
				codes = append(codes, w.Code)
			}
		}
	} else {
		req := &types.Request{Cohort: &types.Cohort{Filename: "ref_tests.pulse"}, Weight: &spec}
		if err := json.Unmarshal([]byte(c.request), req); err != nil {
			t.Fatalf("%s: request: %v", c.name, err)
		}
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		layers = resp.Overlays
		for _, w := range resp.Warnings {
			if strings.HasPrefix(w.Code, "PULSE_OVERLAY_") {
				codes = append(codes, w.Code)
			}
		}
	}
	slices.Sort(codes)
	var wire []any
	if err := json.Unmarshal(mustMarshal(t, layers), &wire); err != nil {
		t.Fatal(err)
	}
	return wire, slices.Compact(codes)
}

// overlayFigure resolves one figure key (see the file comment).
func overlayFigure(layers []any, key string) (any, error) {
	li, path, ok := strings.Cut(key, "/")
	i, err := strconv.Atoi(li)
	if !ok || err != nil || i < 0 || i >= len(layers) {
		return nil, pathError(key + ": no such layer")
	}
	layer := layers[i]
	join := func(k any) string {
		parts, _ := k.([]any)
		s := make([]string, len(parts))
		for i, p := range parts {
			s[i], _ = p.(string)
		}
		return strings.Join(s, ",")
	}
	switch {
	case strings.HasPrefix(path, "cell["):
		sel, elem, _ := strings.Cut(strings.TrimPrefix(path, "cell["), "]")
		row, col, _ := strings.Cut(sel, "|")
		mx, err := wirePath(layer, "payload.matrix")
		if err != nil {
			return nil, err
		}
		m, _ := mx.(map[string]any)
		rows, _ := m["row_keys"].([]any)
		cols, _ := m["column_keys"].([]any)
		r := slices.IndexFunc(rows, func(k any) bool { return join(k) == row })
		c := slices.IndexFunc(cols, func(k any) bool { return join(k) == col })
		if r < 0 || c < 0 {
			return nil, pathError(key + ": no such cell")
		}
		cell := m["cells"].([]any)[r].([]any)[c].(map[string]any)
		if present, _ := cell["present"].(bool); !present {
			return nil, pathError(key + ": cell absent")
		}
		v := cell["value"]
		if elem != "" {
			k, err := strconv.Atoi(strings.TrimPrefix(elem, "#"))
			vs, _ := v.([]any)
			if err != nil || k >= len(vs) {
				return nil, pathError(key + ": no such element")
			}
			v = vs[k]
		}
		return v, nil
	case strings.HasPrefix(path, "entry["):
		sel, rest, _ := strings.Cut(strings.TrimPrefix(path, "entry["), "].")
		es, err := wirePath(layer, "payload.series.entries")
		if err != nil {
			return nil, err
		}
		entries, _ := es.([]any)
		e := slices.IndexFunc(entries, func(e any) bool { return join(e.(map[string]any)["key"]) == sel })
		if e < 0 {
			return nil, pathError(key + ": no such entry")
		}
		return wirePath(entries[e], rest)
	}
	return wirePath(layer, path)
}

// weightRefOverlayTol is a figure's relative tolerance (file comment).
func weightRefOverlayTol(key string) float64 {
	_, path, _ := strings.Cut(key, "/")
	switch {
	case strings.HasPrefix(path, "cell["), strings.HasSuffix(path, "p_value"),
		strings.HasPrefix(path, "entry[") && strings.HasSuffix(path, "summary.statistic") && !strings.Contains(path, "parameters"):
		return 1e-10
	}
	return 1e-12
}

// weightRefOverlayKinds is the overlay kinds a case's request runs.
func weightRefOverlayKinds(t *testing.T, c weightRefOverlayCase) []string {
	t.Helper()
	var frag struct {
		Overlays []struct {
			Kind string `json:"kind"`
		} `json:"overlays"`
	}
	if err := json.Unmarshal([]byte(c.request), &frag); err != nil || len(frag.Overlays) == 0 {
		t.Fatalf("%s: request %s holds no overlay", c.name, c.request)
	}
	out := make([]string, len(frag.Overlays))
	for i, o := range frag.Overlays {
		out[i] = o.Kind
	}
	return out
}

// assertWeightRefOverlayCoverage: every overlay kind the manifest
// weights has a reference case under each kind it advertises — the
// probability kind under BOTH probability configurations — and every
// layer a case runs has at least one figure.
func assertWeightRefOverlayCoverage(t *testing.T) {
	t.Helper()
	have := map[string][]types.WeightKind{}
	configs := map[string]map[string]bool{}
	for _, c := range weightRefOverlayCases {
		kinds := weightRefOverlayKinds(t, c)
		for li, op := range kinds {
			k := weightRefKind(c.kind)
			if !slices.Contains(have[op], k) {
				have[op] = append(have[op], k)
			}
			if configs[op] == nil {
				configs[op] = map[string]bool{}
			}
			configs[op][c.weight+"_"+c.kind] = true
			figured := false
			for key := range c.figures {
				figured = figured || strings.HasPrefix(key, strconv.Itoa(li)+"/")
			}
			if !figured {
				t.Errorf("%s %s_%s: layer %d (%s) has no figure", c.name, c.weight, c.kind, li, op)
			}
		}
	}
	assertWeightKindCoverage(t, weightSurfaceOverlays, have, "OV_CASES in testdata/weight_reference/gen_weight_reference.py, then regenerate")
	for op, kinds := range manifestWeightKinds(weightSurfaceOverlays) {
		if slices.Contains(kinds, types.WeightKindProbability) && !(configs[op]["w_probability"] && configs[op]["p_probability"]) {
			t.Errorf("%s: probability reference cases %v, want both w_probability and p_probability", op, configs[op])
		}
	}
}

// testWeightRefOverlays is TestWeightReferenceValues' overlay half.
func testWeightRefOverlays(t *testing.T) {
	t.Run("coverage", assertWeightRefOverlayCoverage)
	svc := weightRefTestService(t)
	for _, c := range weightRefOverlayCases {
		t.Run(c.weight+"_"+c.kind+"/"+c.name, func(t *testing.T) {
			if c.source == "" || len(c.figures) == 0 {
				t.Fatal("case has no provenance source or no figures")
			}
			layers, codes := weightRefOverlayRun(t, svc, c)
			if want := slices.Compact(slices.Sorted(slices.Values(c.warnings))); !slices.Equal(codes, want) {
				t.Errorf("overlay warnings %v, expected %v", codes, want)
			}
			for _, key := range sortedNames(c.figures) {
				want := c.figures[key]
				got, err := overlayFigure(layers, key)
				if err != nil {
					t.Error(err)
					continue
				}
				g, ok := got.(float64)
				if !ok || !relClose(g, want, weightRefOverlayTol(key)) {
					t.Errorf("%s = %v, reference %.17g (rel %.3g, tol %g)", key, got, want,
						math.Abs(g-want)/math.Abs(want), weightRefOverlayTol(key))
				}
			}
		})
	}
}
