package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E1-S23 end-to-end byte-equal parity gate for the pairwise consumer
// survey overlay surface against the native TEST_WELCH / TEST_Z_TWO_SAMPLE
// row tests.
//
// Mission. Stories S12 (Welch parity) and S22 (Z parity) already pin
// the in-package per-cell parity gates at processing/overlay_t_cell_
// parity_test.go and processing/overlay_z_cell_parity_test.go. Those
// gates drive synthesized welfordBucket triples through the overlay
// surface and the row tests directly — perfect for the math, but they
// do NOT prove the customer-shape pipeline (categorical respondent
// cohort → Crosstab + AGG_WELFORD per Compose slot → OVERLAY_T_CELL /
// OVERLAY_Z_CELL pair → byte-equal p-value vs TEST_WELCH /
// TEST_Z_TWO_SAMPLE) survives the full service-level wiring.
//
// This file closes that gap. The synthetic cohort mirrors the field
// shape from .planning/stat-test-overlay-parity/research/pairwise-test-
// examples.md fixtures 02 / 03 / 04:
//
//   - 8 fields: 7 categorical_u8 dimensions + 1 f64 metric.
//   - Field names: country_code, study_code, metric_code,
//     metric_brand_code, wave_date_label, option_code,
//     column_designator (the pair axis), metric_value (the f64
//     measurement under test).
//
// Brand-name policy. Any customer-brand references found in
// `.planning/` background docs are uncommitted — those references are
// scrubbed from the in-tree research fixture and from THIS file. Every
// identifier, comment, and dictionary value here uses neutral
// terminology: respondent cohort, consumer survey, pairwise survey,
// generic demographic-segment labels (TOTAL_POP, AFFLUENT,
// URBAN_MILLENNIALS, RURAL), country / study / metric codes (C0..C2,
// S0..S1, M0..M1). The Compose envelope mirrors the fixture 04 pattern
// without naming any customer.
//
// The two parity tests below:
//
//   1. TestPairwiseSurveyOverlay_WelchEndToEnd — one ComposedRequest
//      with two slots, each filtered to one column_designator value
//      under a fixed (country_code, study_code, metric_code,
//      metric_brand_code, wave_date_label, option_code) outer tuple.
//      Each slot is a Crosstab with AGG_WELFORD on metric_value
//      producing a 1×1 matrix carrying a WelfordTriple cell.
//      `applyComposeOverlays` runs OVERLAY_T_CELL; the resulting
//      per-cell p-value MUST byte-equal the p-value TEST_WELCH emits
//      when SplitBy=column_designator over the same row set.
//
//   2. TestPairwiseSurveyOverlay_ZEndToEnd — the same shape but using
//      OVERLAY_Z_CELL on the overlay side and TEST_Z_TWO_SAMPLE on the
//      native side. Mirrors fixture 03.
//
//   3. TestPairwiseSurveyOverlay_MultiSlotOrderPreserved — mirrors
//      fixture 04 (C(k,2) pairs within one outer group). The
//      ComposedRequest carries multiple OVERLAY_T_CELL specs (one per
//      unordered pair); the layer slice MUST emit in spec order
//      regardless of the parallel worker dispatch.

// pairwiseSurveySchema builds the 8-field respondent-cohort schema the
// e2e tests share. The categorical fields use small dictionaries so a
// u8 column index suffices for every code; the f64 metric column
// carries the numeric measurement under test.
func pairwiseSurveySchema(t *testing.T) *encoding.Schema {
	t.Helper()

	mkDict := func(labels []string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, lbl := range labels {
			if _, err := d.Add(lbl); err != nil {
				t.Fatalf("dict.Add(%q): %v", lbl, err)
			}
		}
		return d
	}

	return &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name: "country_code", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 0, CsvColumnIdx: 0,
				Dictionary: mkDict([]string{"C0", "C1", "C2"}),
			},
			{
				Name: "study_code", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 1, CsvColumnIdx: 1,
				Dictionary: mkDict([]string{"S0", "S1"}),
			},
			{
				Name: "metric_code", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 2, CsvColumnIdx: 2,
				Dictionary: mkDict([]string{"M0", "M1"}),
			},
			{
				Name: "metric_brand_code", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 3, CsvColumnIdx: 3,
				Dictionary: mkDict([]string{"B0", "B1"}),
			},
			{
				Name: "wave_date_label", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 4, CsvColumnIdx: 4,
				Dictionary: mkDict([]string{"W0", "W1"}),
			},
			{
				Name: "option_code", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 5, CsvColumnIdx: 5,
				// Single option keeps the outer tuple unambiguous —
				// the pair-axis test isolates column_designator
				// regardless of option fan-out.
				Dictionary: mkDict([]string{"O0"}),
			},
			{
				Name: "column_designator", Type: encoding.FieldTypeCategoricalU8,
				ByteOffset: 6, CsvColumnIdx: 6,
				// Four pair-axis segment levels mirror the fixture 04
				// shape. Neutral demographic-segment labels carry no
				// customer brand.
				Dictionary: mkDict([]string{"TOTAL_POP", "AFFLUENT", "URBAN_MILLENNIALS", "RURAL"}),
			},
			{
				Name: "metric_value", Type: encoding.FieldTypeF64,
				ByteOffset: 7, CsvColumnIdx: 7,
			},
		},
	}
}

// pairwiseSurveyRow describes one respondent row in the synthetic
// cohort. The integer fields hold dictionary indices; metricValue is
// the f64 measurement under test.
type pairwiseSurveyRow struct {
	country     uint64
	study       uint64
	metric      uint64
	brand       uint64
	wave        uint64
	option      uint64
	designator  uint64
	metricValue float64
}

// pairwiseSurveyCohort synthesizes a ~500-row respondent cohort. The
// distribution is deterministic so the byte-equal parity assertion is
// reproducible: every (country, study, metric, brand, wave, option,
// designator) tuple receives `perTuple` rows whose metric_value is a
// centered arithmetic progression around a designator-keyed mean.
//
// The pair-axis mean spread is large enough that the Welch t-test
// rejects the null on the headline tuple — the test exercises a
// non-trivial p-value, not a degenerate equal-means case.
func pairwiseSurveyCohort() []pairwiseSurveyRow {
	const perTuple = 50 // 50 respondents per (outer × designator) bucket

	// Per-designator (mean, stddev) tuples. Spread is deliberate so the
	// pair tests reject the null on (TOTAL_POP vs AFFLUENT) and the
	// non-trivial (URBAN_MILLENNIALS vs RURAL) pair too.
	type designatorParams struct {
		mu float64
		sd float64
	}
	designatorTable := []designatorParams{
		{mu: 50.0, sd: 8.0}, // TOTAL_POP
		{mu: 65.0, sd: 7.0}, // AFFLUENT
		{mu: 55.0, sd: 9.0}, // URBAN_MILLENNIALS
		{mu: 45.0, sd: 10.0}, // RURAL
	}

	// Outer keys exercised by the synthetic cohort. The headline
	// parity tests filter to the first outer tuple (country=C0,
	// study=S0, metric=M0, brand=B0, wave=W0, option=O0); the
	// remaining rows under different outer tuples confirm the
	// Filterer narrowing actually engages (without them every row in
	// the cohort would land in the headline tuple's Welford bucket).
	type outerKey struct {
		country, study, metric, brand, wave uint64
	}
	outerKeys := []outerKey{
		{country: 0, study: 0, metric: 0, brand: 0, wave: 0},
		{country: 1, study: 1, metric: 1, brand: 1, wave: 1},
	}

	out := make([]pairwiseSurveyRow, 0, len(outerKeys)*len(designatorTable)*perTuple)
	for _, ok := range outerKeys {
		for did, dparams := range designatorTable {
			step := dparams.sd
			if perTuple > 1 {
				// Uniform-distribution std rule (matches the parity
				// test's makeParityStream helper at
				// processing/overlay_t_cell_parity_test.go).
				step = dparams.sd * math.Sqrt(12.0/float64(perTuple*perTuple-1))
			}
			mid := float64(perTuple-1) / 2.0
			for i := 0; i < perTuple; i++ {
				v := dparams.mu + (float64(i)-mid)*step
				out = append(out, pairwiseSurveyRow{
					country:     ok.country,
					study:       ok.study,
					metric:      ok.metric,
					brand:       ok.brand,
					wave:        ok.wave,
					option:      0,
					designator:  uint64(did),
					metricValue: v,
				})
			}
		}
	}
	return out
}

// pairwiseSurveyRecords flattens a pairwiseSurveyRow slice into the
// [][]uint64 shape `writePulseFile` consumes. The field order matches
// pairwiseSurveySchema.
func pairwiseSurveyRecords(rows []pairwiseSurveyRow) [][]uint64 {
	out := make([][]uint64, len(rows))
	for i, r := range rows {
		out[i] = []uint64{
			r.country,
			r.study,
			r.metric,
			r.brand,
			r.wave,
			r.option,
			r.designator,
			math.Float64bits(r.metricValue),
		}
	}
	return out
}

// pairwiseSurveyComposeSlot builds one Compose slot for the synthetic
// pairwise-survey cohort: a Crosstab Request that filters to one outer
// tuple plus one column_designator value and aggregates metric_value
// via AGG_WELFORD on the (single-cell) intersection. The resulting
// 1×1 matrix carries a WelfordTriple cell — the exact shape the
// triple-aware OVERLAY_T_CELL / OVERLAY_Z_CELL handlers consume.
func pairwiseSurveyComposeSlot(label string, designator string, country, study, metric, brand, wave string) *types.Request {
	return &types.Request{
		Label:  label,
		Cohort: &types.Cohort{Filename: "respondent_cohort.pulse"},
		Filterers: []*types.Filterer{
			// Outer-tuple narrowing — mirrors the fixture-02 / 03
			// per-Request filter list.
			{Type: types.FILTER_INCLUDE, Field: "country_code", Values: []string{country}},
			{Type: types.FILTER_INCLUDE, Field: "study_code", Values: []string{study}},
			{Type: types.FILTER_INCLUDE, Field: "metric_code", Values: []string{metric}},
			{Type: types.FILTER_INCLUDE, Field: "metric_brand_code", Values: []string{brand}},
			{Type: types.FILTER_INCLUDE, Field: "wave_date_label", Values: []string{wave}},
			// Pair-axis narrowing — restricts the slot to one
			// designator level so the Crosstab cell aligns with the
			// native test's per-SplitBy-group bucket.
			{Type: types.FILTER_INCLUDE, Field: "column_designator", Values: []string{designator}},
		},
		Crosstab: &types.CrosstabSpec{
			// Row + column groupers both pin to wave_date_label —
			// every filtered row carries the same wave value so the
			// resulting matrix is 1×1 with the WelfordTriple in the
			// only (W0, W0) cell. Mirrors the production Compose-slot
			// shape where a single outer tuple collapses to a
			// single-cell crosstab.
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave_date_label"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "wave_date_label"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "metric_value", Label: "welford"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// pairwiseSurveyNativeWelchP runs the native TEST_WELCH row-test surface
// over the in-memory cohort filtered to the outer tuple. SplitBy is
// column_designator narrowed to exactly two designator levels (the
// pair under test). Returns the p-value the row test emits.
func pairwiseSurveyNativeWelchP(t *testing.T, svc *Service, country, study, metric, brand, wave string, pair [2]string) (float64, bool) {
	t.Helper()
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "respondent_cohort.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "country_code", Values: []string{country}},
			{Type: types.FILTER_INCLUDE, Field: "study_code", Values: []string{study}},
			{Type: types.FILTER_INCLUDE, Field: "metric_code", Values: []string{metric}},
			{Type: types.FILTER_INCLUDE, Field: "metric_brand_code", Values: []string{brand}},
			{Type: types.FILTER_INCLUDE, Field: "wave_date_label", Values: []string{wave}},
			{Type: types.FILTER_INCLUDE, Field: "column_designator", Values: pair[:]},
		},
		Tests: []*types.Test{{
			Type:    types.TEST_WELCH,
			Field:   "metric_value",
			SplitBy: "column_designator",
			Alpha:   0.05,
		}},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("native TEST_WELCH Process: %v", err)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("native TEST_WELCH: len(Tests) = %d, want 1", len(resp.Tests))
	}
	return resp.Tests[0].PValue, true
}

// pairwiseSurveyNativeZP runs the native TEST_Z_TWO_SAMPLE row-test
// surface over the in-memory cohort. Mirrors pairwiseSurveyNativeWelchP
// for the z-on-means surface.
func pairwiseSurveyNativeZP(t *testing.T, svc *Service, country, study, metric, brand, wave string, pair [2]string) (float64, bool) {
	t.Helper()
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "respondent_cohort.pulse"},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_INCLUDE, Field: "country_code", Values: []string{country}},
			{Type: types.FILTER_INCLUDE, Field: "study_code", Values: []string{study}},
			{Type: types.FILTER_INCLUDE, Field: "metric_code", Values: []string{metric}},
			{Type: types.FILTER_INCLUDE, Field: "metric_brand_code", Values: []string{brand}},
			{Type: types.FILTER_INCLUDE, Field: "wave_date_label", Values: []string{wave}},
			{Type: types.FILTER_INCLUDE, Field: "column_designator", Values: pair[:]},
		},
		Tests: []*types.Test{{
			Type:    types.TEST_Z_TWO_SAMPLE,
			Field:   "metric_value",
			SplitBy: "column_designator",
			Alpha:   0.05,
		}},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("native TEST_Z_TWO_SAMPLE Process: %v", err)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("native TEST_Z_TWO_SAMPLE: len(Tests) = %d, want 1", len(resp.Tests))
	}
	return resp.Tests[0].PValue, true
}

// pairwiseSurveyOverlayP runs a 2-slot Compose against the respondent
// cohort and extracts the per-cell p-value the named overlay kind emits
// for the reference→target pair. Mirrors the fixture-02 / 03 envelope
// shape: one reference slot + one target slot, both filtered to one
// designator value, the overlay decorates the target cell with the
// (target_triple, ref_triple) p-value.
func pairwiseSurveyOverlayP(t *testing.T, svc *Service, kind types.OverlayKind, pair [2]string) (float64, bool) {
	t.Helper()
	const (
		country = "C0"
		study   = "S0"
		metric  = "M0"
		brand   = "B0"
		wave    = "W0"
	)
	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			pairwiseSurveyComposeSlot("reference", pair[0], country, study, metric, brand, wave),
			pairwiseSurveyComposeSlot("target", pair[1], country, study, metric, brand, wave),
		},
		Overlays: []types.ComposeOverlaySpec{{
			Name:      "pair_overlay",
			Kind:      kind,
			Scope:     types.OverlayScopeCell,
			Reference: "reference",
			Targets:   []string{"target"},
		}},
	}
	responses, err := svc.Compose(context.Background(), composed)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("Compose returned %d responses, want 2", len(responses))
	}
	// The Compose facade discards the layer slice (facade rewire
	// deferred to E7-S15) so we invoke applyComposeOverlays directly
	// against the materialised responses to extract the layer the
	// orchestrator hook produced.
	requests, err := applyComposeLabelDefaults(composed)
	if err != nil {
		t.Fatalf("applyComposeLabelDefaults: %v", err)
	}
	layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
	if err != nil {
		t.Fatalf("applyComposeOverlays: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %d, want 1", len(layers))
	}
	mx := layers[0].Payload.Matrix
	if mx == nil {
		t.Fatalf("layer payload missing matrix; layer = %+v", layers[0])
	}
	if len(mx.Cells) == 0 || len(mx.Cells[0]) == 0 {
		t.Fatalf("layer matrix empty; cells = %+v", mx.Cells)
	}
	cell := mx.Cells[0][0]
	if !cell.Present {
		return math.NaN(), false
	}
	v, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("layer cell value = %T, want float64", cell.Value)
	}
	if math.IsNaN(v) {
		return math.NaN(), false
	}
	return v, true
}

// pairwiseSurveyServiceSetup writes the synthetic cohort to an in-
// memory FS and returns the configured Service. No disk I/O occurs —
// fs.NewMemMap() backs the entire test surface.
func pairwiseSurveyServiceSetup(t *testing.T) *Service {
	t.Helper()
	schema := pairwiseSurveySchema(t)
	rows := pairwiseSurveyCohort()
	records := pairwiseSurveyRecords(rows)
	cfg := setupTestFS(t, "respondent_cohort.pulse", schema, records)
	return New(cfg)
}

// TestPairwiseSurveyOverlay_WelchEndToEnd is the headline end-to-end
// byte-equal gate against the customer-shape pipeline:
//
//   - The respondent cohort lives entirely in memory (no disk I/O).
//   - Two Compose slots each carry a Crosstab with AGG_WELFORD on
//     metric_value, narrowed to one column_designator level under a
//     fixed outer tuple.
//   - OVERLAY_T_CELL decorates the target cell with the per-cell
//     Welch p-value vs the reference cell's WelfordTriple.
//   - The same row set is fed to a native TEST_WELCH Request with
//     SplitBy=column_designator restricted to the two pair members.
//   - Asserts math.Float64bits(p_overlay) == math.Float64bits(p_native).
//
// Mirrors the fixture-02 shape from
// .planning/stat-test-overlay-parity/research/pairwise-test-examples.md.
func TestPairwiseSurveyOverlay_WelchEndToEnd(t *testing.T) {
	svc := pairwiseSurveyServiceSetup(t)

	// Pairs to exercise — drawn from the 4-segment column_designator
	// axis. Each row in the table is one fixture-02-shape parity
	// check. Multiple pairs let the test catch drift in any single
	// designator-level pair.
	pairs := [][2]string{
		{"TOTAL_POP", "AFFLUENT"},
		{"TOTAL_POP", "URBAN_MILLENNIALS"},
		{"TOTAL_POP", "RURAL"},
		{"AFFLUENT", "URBAN_MILLENNIALS"},
		{"AFFLUENT", "RURAL"},
		{"URBAN_MILLENNIALS", "RURAL"},
	}

	const (
		country = "C0"
		study   = "S0"
		metric  = "M0"
		brand   = "B0"
		wave    = "W0"
	)
	for _, pair := range pairs {
		pair := pair
		t.Run(pair[0]+"_vs_"+pair[1], func(t *testing.T) {
			pNative, nativeOK := pairwiseSurveyNativeWelchP(t, svc, country, study, metric, brand, wave, pair)
			pOverlay, overlayOK := pairwiseSurveyOverlayP(t, svc, types.OverlayKindTCell, pair)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: native ok=%v overlay ok=%v", nativeOK, overlayOK)
			}
			if !nativeOK {
				return
			}
			bitsNative := math.Float64bits(pNative)
			bitsOverlay := math.Float64bits(pOverlay)
			if bitsNative != bitsOverlay {
				t.Errorf("byte-equal parity violated for pair %s vs %s: TEST_WELCH p=%v (bits=0x%x), OVERLAY_T_CELL p=%v (bits=0x%x)",
					pair[0], pair[1], pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

// TestPairwiseSurveyOverlay_ZEndToEnd mirrors the Welch end-to-end gate
// for the z-on-means surface. OVERLAY_Z_CELL must produce p-values
// byte-equal with the native TEST_Z_TWO_SAMPLE row test over the same
// row set. Mirrors the fixture-03 shape.
func TestPairwiseSurveyOverlay_ZEndToEnd(t *testing.T) {
	svc := pairwiseSurveyServiceSetup(t)

	pairs := [][2]string{
		{"TOTAL_POP", "AFFLUENT"},
		{"TOTAL_POP", "URBAN_MILLENNIALS"},
		{"TOTAL_POP", "RURAL"},
		{"AFFLUENT", "URBAN_MILLENNIALS"},
		{"AFFLUENT", "RURAL"},
		{"URBAN_MILLENNIALS", "RURAL"},
	}

	const (
		country = "C0"
		study   = "S0"
		metric  = "M0"
		brand   = "B0"
		wave    = "W0"
	)
	for _, pair := range pairs {
		pair := pair
		t.Run(pair[0]+"_vs_"+pair[1], func(t *testing.T) {
			pNative, nativeOK := pairwiseSurveyNativeZP(t, svc, country, study, metric, brand, wave, pair)
			pOverlay, overlayOK := pairwiseSurveyOverlayP(t, svc, types.OverlayKindZCell, pair)

			if nativeOK != overlayOK {
				t.Fatalf("availability divergence: native ok=%v overlay ok=%v", nativeOK, overlayOK)
			}
			if !nativeOK {
				return
			}
			bitsNative := math.Float64bits(pNative)
			bitsOverlay := math.Float64bits(pOverlay)
			if bitsNative != bitsOverlay {
				t.Errorf("byte-equal parity violated for pair %s vs %s: TEST_Z_TWO_SAMPLE p=%v (bits=0x%x), OVERLAY_Z_CELL p=%v (bits=0x%x)",
					pair[0], pair[1], pNative, bitsNative, pOverlay, bitsOverlay)
			}
		})
	}
}

// TestPairwiseSurveyOverlay_MultiSlotOrderPreserved mirrors the
// fixture-04 envelope: multiple OVERLAY_T_CELL specs in a single
// ComposedRequest, one per unordered pair within a single outer
// group. Asserts the layer slice the orchestrator returns matches the
// spec order in `composed.Overlays` regardless of worker scheduling.
//
// Compose layer order is the wire-shape guarantee the renderer relies
// on to align the per-pair p-value with the (colA, colB) label tuple
// the client derives from `pair[0]_vs_pair[1]` — a reorder would
// surface as a label mismatch downstream. The test exercises both the
// serial (Compose) and parallel (ComposeParallel) facades.
func TestPairwiseSurveyOverlay_MultiSlotOrderPreserved(t *testing.T) {
	svc := pairwiseSurveyServiceSetup(t)

	const (
		country = "C0"
		study   = "S0"
		metric  = "M0"
		brand   = "B0"
		wave    = "W0"
	)

	// One Compose slot per designator level. Each slot is a 1×1
	// Crosstab with AGG_WELFORD on metric_value, filtered to the
	// matching column_designator value. The pair iteration emits one
	// OVERLAY_T_CELL spec per unordered C(4, 2) pair → 6 layers
	// expected in the resulting slice.
	composed := &types.ComposedRequest{
		Requests: []*types.Request{
			pairwiseSurveyComposeSlot("seg_total_pop", "TOTAL_POP", country, study, metric, brand, wave),
			pairwiseSurveyComposeSlot("seg_affluent", "AFFLUENT", country, study, metric, brand, wave),
			pairwiseSurveyComposeSlot("seg_urban_millennials", "URBAN_MILLENNIALS", country, study, metric, brand, wave),
			pairwiseSurveyComposeSlot("seg_rural", "RURAL", country, study, metric, brand, wave),
		},
	}
	pairLabels := [][2]string{
		{"seg_total_pop", "seg_affluent"},
		{"seg_total_pop", "seg_urban_millennials"},
		{"seg_total_pop", "seg_rural"},
		{"seg_affluent", "seg_urban_millennials"},
		{"seg_affluent", "seg_rural"},
		{"seg_urban_millennials", "seg_rural"},
	}
	specNames := make([]string, 0, len(pairLabels))
	for _, pl := range pairLabels {
		name := pl[0] + "_vs_" + pl[1]
		specNames = append(specNames, name)
		composed.Overlays = append(composed.Overlays, types.ComposeOverlaySpec{
			Name:      name,
			Kind:      types.OverlayKindTCell,
			Scope:     types.OverlayScopeCell,
			Reference: pl[0],
			Targets:   []string{pl[1]},
		})
	}

	verifyOrder := func(t *testing.T, layers []types.OverlayLayer) {
		t.Helper()
		if len(layers) != len(specNames) {
			t.Fatalf("len(layers) = %d, want %d", len(layers), len(specNames))
		}
		for i, layer := range layers {
			if layer.Name != specNames[i] {
				t.Errorf("layers[%d].Name = %q, want %q", i, layer.Name, specNames[i])
			}
			if layer.Kind != types.OverlayKindTCell {
				t.Errorf("layers[%d].Kind = %q, want %q", i, layer.Kind, types.OverlayKindTCell)
			}
		}
	}

	t.Run("serial", func(t *testing.T) {
		responses, err := svc.Compose(context.Background(), composed)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		if len(responses) != 4 {
			t.Fatalf("len(responses) = %d, want 4", len(responses))
		}
		requests, err := applyComposeLabelDefaults(composed)
		if err != nil {
			t.Fatalf("applyComposeLabelDefaults: %v", err)
		}
		layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
		if err != nil {
			t.Fatalf("applyComposeOverlays: %v", err)
		}
		verifyOrder(t, layers)
	})

	t.Run("parallel", func(t *testing.T) {
		responses, err := svc.ComposeParallel(context.Background(), composed, ComposeOptions{MaxWorkers: 3})
		if err != nil {
			t.Fatalf("ComposeParallel: %v", err)
		}
		if len(responses) != 4 {
			t.Fatalf("len(responses) = %d, want 4", len(responses))
		}
		requests, err := applyComposeLabelDefaults(composed)
		if err != nil {
			t.Fatalf("applyComposeLabelDefaults: %v", err)
		}
		layers, _, err := svc.applyComposeOverlays(context.Background(), composed, requests, responses)
		if err != nil {
			t.Fatalf("applyComposeOverlays: %v", err)
		}
		verifyOrder(t, layers)
	})
}

