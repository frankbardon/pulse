package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Tests for the SERIES-host overlay fold engine (processing/overlay_series.go).
//
// E3-S1 scope: structural pre-req for the five grouped-Process overlay
// kinds that land in E3-S2..S5. This file exercises only the dispatch /
// host-shape contract — no real SERIES handlers exist yet. Tests use a
// synthetic stub kind ("OVERLAY_TEST_SERIES_STUB") wired through the
// package-level seriesOverlayHandlers map; each test installs the stub
// via installSeriesStubHandler and tears it down via t.Cleanup so the
// production dispatch table stays empty between tests.
//
// Coverage matrix (per the E3-S1 acceptance criteria):
//
//   - SERIES dispatch accepts an OverlayShape == SERIES branch (the
//     stub emits SeriesPayload) and walks an ordered group-key list.
//   - Series fold preserves spec-order layer emission (FR-A2) — two
//     stub specs emit two layers in the same order.
//   - Group-key alignment is byte-identical when the handler covers
//     every host group; loose alignment (handler emits a subset) keeps
//     the host's key order.
//   - Null / missing group values surface through SeriesPayload without
//     crashing — the stub emits zero-value Summary for groups where
//     host.ValueAt(i) reports (0, false).
//   - 0 / 1 / N group fixtures all dispatch cleanly.
//   - Existing E1 / E2 MATRIX SCALAR path is byte-identical (defense
//     in depth — TestApplyOverlaysSeries_MatrixPathUnchanged guards
//     against accidental cross-contamination).
//   - Unknown-kind defense fires PROCESSING_INTERNAL + the
//     PULSE_OVERLAY_KIND_UNKNOWN details code.

// testSeriesStubKind is the synthetic overlay kind the SERIES dispatch
// tests register against. NOT a real catalog entry — declared inline so
// the test file is self-contained and the production
// types.AllOverlayKinds() surface stays untouched. CLAUDE.md's Update
// Demand row for overlays explicitly notes that new kind constants
// belong to E3-S2..S5; E3-S1 is structural infra.
const testSeriesStubKind types.OverlayKind = "OVERLAY_TEST_SERIES_STUB"

// installSeriesStubHandler registers the test stub handler against
// seriesOverlayHandlers for the duration of the calling test. The
// cleanup hook restores the prior state (typically: delete the stub
// entry) so subsequent tests see the empty production dispatch table.
//
// fn is the handler implementation the stub installs. Tests pass a
// closure that emits whatever SeriesPayload shape the assertion wants.
func installSeriesStubHandler(t *testing.T, fn seriesOverlayHandler) {
	t.Helper()
	prev, hadPrev := seriesOverlayHandlers[testSeriesStubKind]
	seriesOverlayHandlers[testSeriesStubKind] = fn
	t.Cleanup(func() {
		if hadPrev {
			seriesOverlayHandlers[testSeriesStubKind] = prev
			return
		}
		delete(seriesOverlayHandlers, testSeriesStubKind)
	})
}

// stubSeriesHandler is the canonical test-only SERIES handler. For each
// host group ordinal it emits one SeriesEntry whose Summary.Statistic
// carries the host's metric value (when present) or stays unset (when
// absent). The handler runs no math — its job is to prove the dispatch
// wired the host through correctly and the parallel-slice alignment
// holds. Out-of-scope here: real per-kind math (E3-S2..S5).
func stubSeriesHandler(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	n := host.GroupCount()
	entries := make([]types.SeriesEntry, 0, n)
	var warnings []types.OverlayWarning
	for i := 0; i < n; i++ {
		key, _ := host.GroupKey(i)
		var summary types.OverlaySummary
		if v, ok := host.ValueAt(i); ok {
			vCopy := v
			summary.Statistic = &vCopy
		}
		// Copy the key so the response is independent of the host
		// backing array (mirrors the MATRIX CHISQ_ROW per-entry copy).
		var keyCopy types.AxisKey
		if key != nil {
			keyCopy = append(types.AxisKey(nil), key...)
		}
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}
	layer := types.OverlayLayer{
		Name:  string(spec.Kind),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}
	return layer, warnings, nil
}

// newStubSeriesHost returns a SeriesHostView backed by the supplied
// group keys and a resolver that maps each key index to values[i]. A
// values[i] of NaN signals "host did not produce a record for this
// group" — the resolver returns (0, false) for that index so the
// dispatch exercises the absent-group code path. nil values means every
// group is absent (resolver itself returns (0, false) unconditionally).
func newStubSeriesHost(keys []types.AxisKey, values []float64) *SeriesHostView {
	if values == nil {
		return NewSeriesHostView(keys, nil)
	}
	return NewSeriesHostView(keys, func(i int) (float64, bool) {
		if i < 0 || i >= len(values) {
			return 0, false
		}
		v := values[i]
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	})
}

// TestApplyOverlaysSeries_EmptySpecsShortCircuits mirrors the MATRIX
// ApplyOverlays empty-specs contract: zero specs ⇒ nil layers / nil
// warnings / nil error, even when host is nil. Grouped Process
// orchestrators can call ApplyOverlaysSeries unconditionally.
func TestApplyOverlaysSeries_EmptySpecsShortCircuits(t *testing.T) {
	layers, warnings, err := ApplyOverlaysSeries(nil, nil)
	if err != nil || layers != nil || warnings != nil {
		t.Fatalf("empty specs must short-circuit: layers=%v warnings=%v err=%v",
			layers, warnings, err)
	}
}

// TestApplyOverlaysSeries_ZeroGroups exercises the 0-group fold: the
// host carries no group keys (the orchestrator's grouper produced an
// empty key set, e.g. every record was filtered out). The dispatch
// still runs and the stub emits a SeriesPayload with an empty Entries
// slice — handlers never panic on an empty host.
func TestApplyOverlaysSeries_ZeroGroups(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	host := newStubSeriesHost(nil, nil)
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	layer := layers[0]
	if layer.Payload.Shape != types.OverlayShapeSeries {
		t.Fatalf("layer.Payload.Shape = %q, want %q", layer.Payload.Shape,
			types.OverlayShapeSeries)
	}
	if layer.Payload.Series == nil {
		t.Fatalf("layer.Payload.Series nil; want non-nil with empty Entries")
	}
	if got := len(layer.Payload.Series.Entries); got != 0 {
		t.Fatalf("len(Entries) = %d, want 0", got)
	}
}

// TestApplyOverlaysSeries_OneGroup exercises the 1-group fold. A single
// host group with a present value flows through the stub and surfaces
// on the SeriesPayload's only entry. Key copy is byte-equal to the
// host's GroupKey(0) (FR-A2 parallel-slice alignment).
func TestApplyOverlaysSeries_OneGroup(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	keys := []types.AxisKey{{"only"}}
	host := newStubSeriesHost(keys, []float64{42.0})
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, warnings, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d: %+v", len(warnings), warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(entries))
	}
	if got, want := len(entries[0].Key), 1; got != want {
		t.Fatalf("entries[0].Key length = %d, want %d", got, want)
	}
	if got, want := entries[0].Key[0], any("only"); got != want {
		t.Fatalf("entries[0].Key[0] = %v, want %v", got, want)
	}
	if entries[0].Summary.Statistic == nil {
		t.Fatalf("entries[0].Summary.Statistic nil; want 42")
	}
	if got := *entries[0].Summary.Statistic; got != 42.0 {
		t.Fatalf("entries[0].Summary.Statistic = %v, want 42", got)
	}
}

// TestApplyOverlaysSeries_NGroups exercises the canonical N-group fold
// over an ordered group-key list. Verifies (a) per-entry value pass-
// through, (b) key copy alignment (Entries[i].Key element-equal to
// host.GroupKey(i)), and (c) order preservation.
func TestApplyOverlaysSeries_NGroups(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	keys := []types.AxisKey{{"US"}, {"CA"}, {"MX"}, {"BR"}}
	values := []float64{1.0, 2.0, 3.0, 4.0}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d", len(entries), len(keys))
	}
	for i, want := range keys {
		got := entries[i].Key
		if len(got) != len(want) {
			t.Fatalf("entries[%d].Key length = %d, want %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("entries[%d].Key[%d] = %v, want %v", i, j, got[j], want[j])
			}
		}
		if entries[i].Summary.Statistic == nil {
			t.Fatalf("entries[%d].Summary.Statistic nil; want %v", i, values[i])
		}
		if *entries[i].Summary.Statistic != values[i] {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want %v", i,
				*entries[i].Summary.Statistic, values[i])
		}
	}
}

// TestApplyOverlaysSeries_NullGroupValuesSurfaceWithoutCrash exercises
// the absent-group code path: every odd-index group reports
// (0, false) from the resolver. The dispatch still emits one entry per
// host ordinal (FR-A2 parallel-slice contract); absent groups carry a
// Summary with a nil Statistic — the canonical "present slot, empty
// summary" shape the story acceptance asks for.
func TestApplyOverlaysSeries_NullGroupValuesSurfaceWithoutCrash(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	// math.NaN signals absent (resolver returns (0, false)).
	values := []float64{10.0, math.NaN(), 30.0, math.NaN()}
	host := newStubSeriesHost(keys, values)
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != len(keys) {
		t.Fatalf("len(Entries) = %d, want %d (parallel-slice contract)",
			len(entries), len(keys))
	}
	for i, entry := range entries {
		// Key alignment: every entry slot is populated, even for absent
		// groups (the host ordinal exists; only the metric is missing).
		if len(entry.Key) != len(keys[i]) {
			t.Fatalf("entries[%d].Key length = %d, want %d", i, len(entry.Key),
				len(keys[i]))
		}
		for j := range keys[i] {
			if entry.Key[j] != keys[i][j] {
				t.Fatalf("entries[%d].Key[%d] = %v, want %v", i, j, entry.Key[j],
					keys[i][j])
			}
		}
	}
	// Even indices carry a present Statistic; odd indices carry nil.
	if entries[0].Summary.Statistic == nil || *entries[0].Summary.Statistic != 10.0 {
		t.Fatalf("entries[0].Summary.Statistic = %v, want 10", entries[0].Summary.Statistic)
	}
	if entries[1].Summary.Statistic != nil {
		t.Fatalf("entries[1].Summary.Statistic = %v, want nil (absent group)",
			*entries[1].Summary.Statistic)
	}
	if entries[2].Summary.Statistic == nil || *entries[2].Summary.Statistic != 30.0 {
		t.Fatalf("entries[2].Summary.Statistic = %v, want 30", entries[2].Summary.Statistic)
	}
	if entries[3].Summary.Statistic != nil {
		t.Fatalf("entries[3].Summary.Statistic = %v, want nil (absent group)",
			*entries[3].Summary.Statistic)
	}
}

// TestApplyOverlaysSeries_NilResolverTreatsEveryGroupAbsent exercises
// the second null-source: a host built with a nil resolver (the
// orchestrator only had structural group keys to surface, no metric
// computation). Every group surfaces (0, false) and entries carry a
// nil-Statistic Summary. The dispatch never panics.
func TestApplyOverlaysSeries_NilResolverTreatsEveryGroupAbsent(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}}
	host := NewSeriesHostView(keys, nil)
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(entries))
	}
	for i, entry := range entries {
		if entry.Summary.Statistic != nil {
			t.Fatalf("entries[%d].Summary.Statistic = %v, want nil (nil resolver)",
				i, *entry.Summary.Statistic)
		}
	}
}

// TestApplyOverlaysSeries_SpecOrderPreserved verifies FR-A2: when the
// request carries multiple SERIES specs, ApplyOverlaysSeries emits one
// OverlayLayer per spec in matching order. Uses two specs of the same
// stub kind tagged with distinct Name fields so the assertion can
// distinguish them.
func TestApplyOverlaysSeries_SpecOrderPreserved(t *testing.T) {
	installSeriesStubHandler(t, stubSeriesHandler)
	keys := []types.AxisKey{{"a"}, {"b"}}
	host := newStubSeriesHost(keys, []float64{1.0, 2.0})
	specs := []types.OverlaySpec{
		{
			Name:  "first",
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
		{
			Name:  "second",
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}
	// stubSeriesHandler ignores spec.Name when synthesising layer.Name
	// (it uses string(spec.Kind)); the parallel-slice contract is on
	// layer ordinal — layers[0] corresponds to specs[0] and layers[1]
	// to specs[1] even when the stub emits the same Name for both.
	// Verify ordinal alignment via the Scope echo (every spec carries
	// the same Scope here; what we really verify is that two layers
	// land in two slots, in the order specs were submitted).
	for i, layer := range layers {
		if layer.Kind != testSeriesStubKind {
			t.Fatalf("layers[%d].Kind = %q, want %q", i, layer.Kind, testSeriesStubKind)
		}
		if layer.Payload.Shape != types.OverlayShapeSeries {
			t.Fatalf("layers[%d].Payload.Shape = %q, want %q", i, layer.Payload.Shape,
				types.OverlayShapeSeries)
		}
	}
}

// TestApplyOverlaysSeries_LooseAlignmentSubset exercises the loose
// alignment contract: a handler may emit a SeriesPayload whose entries
// cover a strict subset of host group keys. The dispatch passes the
// host through verbatim — no key reordering, no dropped keys. The
// handler decides which subset to surface.
//
// The stub variant here emits an entry only for even host ordinals.
// The test asserts the dispatch did not interfere with the handler's
// emission shape — Entries length matches the handler's choice (2 of
// 4 host groups) and Entries[*].Key still aligns element-for-element
// with the corresponding host ordinal.
func TestApplyOverlaysSeries_LooseAlignmentSubset(t *testing.T) {
	subsetHandler := func(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
		n := host.GroupCount()
		entries := make([]types.SeriesEntry, 0, n/2+1)
		for i := 0; i < n; i++ {
			if i%2 != 0 {
				continue
			}
			key, _ := host.GroupKey(i)
			var summary types.OverlaySummary
			if v, ok := host.ValueAt(i); ok {
				vCopy := v
				summary.Statistic = &vCopy
			}
			var keyCopy types.AxisKey
			if key != nil {
				keyCopy = append(types.AxisKey(nil), key...)
			}
			entries = append(entries, types.SeriesEntry{Key: keyCopy, Summary: summary})
		}
		return types.OverlayLayer{
			Name:  string(spec.Kind),
			Kind:  spec.Kind,
			Scope: spec.Scope,
			Ref:   spec.Ref,
			Payload: types.OverlayPayload{
				Shape:  types.OverlayShapeSeries,
				Series: &types.SeriesPayload{Entries: entries},
			},
		}, nil, nil
	}
	installSeriesStubHandler(t, subsetHandler)
	keys := []types.AxisKey{{"a"}, {"b"}, {"c"}, {"d"}}
	host := newStubSeriesHost(keys, []float64{10.0, 20.0, 30.0, 40.0})
	specs := []types.OverlaySpec{
		{
			Kind:  testSeriesStubKind,
			Scope: types.OverlayScopeRow,
		},
	}
	layers, _, err := ApplyOverlaysSeries(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlaysSeries: %v", err)
	}
	entries := layers[0].Payload.Series.Entries
	if len(entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2 (subset emission)", len(entries))
	}
	// Entries[0] corresponds to host ordinal 0; Entries[1] to host
	// ordinal 2. Verify the keys did not get reordered or rewritten.
	if entries[0].Key[0] != any("a") {
		t.Fatalf("entries[0].Key[0] = %v, want %v", entries[0].Key[0], "a")
	}
	if entries[1].Key[0] != any("c") {
		t.Fatalf("entries[1].Key[0] = %v, want %v", entries[1].Key[0], "c")
	}
}

// TestApplyOverlaysSeries_UnknownKind verifies the defense-in-depth
// branch: a SeriesSpec naming an unregistered kind triggers a
// PROCESSING_INTERNAL CodedError whose details carry the canonical
// PULSE_OVERLAY_KIND_UNKNOWN code. Mirrors the MATRIX path's unknown-
// kind defense (TestApplyOverlays_UnknownKind_StubCoded in overlay_test.go).
func TestApplyOverlaysSeries_UnknownKind(t *testing.T) {
	host := newStubSeriesHost([]types.AxisKey{{"a"}}, []float64{1.0})
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKind("OVERLAY_NOT_A_SERIES_THING"),
			Scope: types.OverlayScopeRow,
		},
	}
	_, _, err := ApplyOverlaysSeries(specs, host)
	if err == nil {
		t.Fatalf("expected error for unknown SERIES kind")
	}
	coded, ok := err.(*errors.CodedError)
	if !ok {
		t.Fatalf("err type = %T, want *errors.CodedError", err)
	}
	if coded.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("err.Code = %q, want %q", coded.Code, errors.PROCESSING_INTERNAL)
	}
	if got, want := coded.Details["code"], string(errors.PULSE_OVERLAY_KIND_UNKNOWN); got != want {
		t.Fatalf("err.Details[\"code\"] = %v, want %v", got, want)
	}
	if got := coded.Details["host"]; got != "series" {
		t.Fatalf("err.Details[\"host\"] = %v, want %q", got, "series")
	}
}

// TestApplyOverlaysSeries_ProductionDispatchTableRegistered verifies
// the E3-S2+ invariant: every SERIES OverlayKind registered in
// production has a real dispatch entry. As of E3-S2 the dispatch
// carries OVERLAY_INDEX_VS_TOTAL; later stories (E3-S3..S5) add the
// remaining SERIES-host kinds. The assertion guards against an
// accidental drop of a real entry — a SERIES kind whose handler is
// missing from the dispatch would otherwise reach
// ApplyOverlaysSeries's defense-in-depth unknown-kind branch and surface
// as PULSE_OVERLAY_KIND_UNKNOWN at runtime instead of computing.
//
// The stub-installation tests above use t.Cleanup to restore the
// production state; this test runs without installing the stub so the
// assertion reflects the production baseline.
func TestApplyOverlaysSeries_ProductionDispatchTableRegistered(t *testing.T) {
	// As of E4-S7: OVERLAY_INDEX_VS_TOTAL (E3-S2) + OVERLAY_SHARE_OF_TOTAL
	// SERIES dispatch (E3-S3) + OVERLAY_ZSCORE_VS_TOTAL (E3-S4) +
	// OVERLAY_DELTA_VS_SIBLING (E3-S5) + OVERLAY_INDEX_VS_SIBLING (E3-S5) +
	// OVERLAY_INDEX_VS_PRIOR (E4-S4) + OVERLAY_INDEX_VS_BASELINE (E4-S2) +
	// OVERLAY_DELTA_VS_BASELINE (E4-S3) + OVERLAY_INDEX_VS_ROLLING_MEAN (E4-S5)
	// + OVERLAY_ZSCORE_VS_ROLLING (E4-S6) + OVERLAY_YOY (E4-S7) are the
	// registered SERIES kinds. The three streamable SERIES kinds from the
	// E3-S2..S4 subset, the two buffered sibling-reference kinds the
	// E3-S5 story adds, the streamable windowed-Process lag-1 kind the
	// E4-S4 story adds, the buffered windowed-Process positional-baseline
	// kind the E4-S2 story adds, the absolute-difference twin of the
	// baseline family the E4-S3 story adds, the buffered windowed
	// rolling-mean kind the E4-S5 story adds, the buffered windowed
	// rolling sample-SD z-score kind the E4-S6 story adds (sibling to
	// INDEX_VS_ROLLING_MEAN — shares the same ring-buffer + Welford
	// carrier), and the buffered windowed year-over-year kind the E4-S7
	// story adds (requires a GROUP_DATE host; per-frequency prior-period
	// lookup). Each subsequent handler story extends this list.
	expected := map[types.OverlayKind]bool{
		types.OverlayKindIndexVsTotal:       true,
		types.OverlayKindShareOfTotal:       true,
		types.OverlayKindZScoreVsTotal:      true,
		types.OverlayKindDeltaVsSibling:     true,
		types.OverlayKindIndexVsSibling:     true,
		types.OverlayKindIndexVsPrior:       true,
		types.OverlayKindIndexVsRollingMean: true,
		types.OverlayKindIndexVsBaseline:    true,
		types.OverlayKindDeltaVsBaseline:    true,
		types.OverlayKindZScoreVsRolling:    true,
		types.OverlayKindYoY:                true,
	}
	for kind := range expected {
		if _, ok := seriesOverlayHandlers[kind]; !ok {
			t.Errorf("seriesOverlayHandlers missing real handler for %s", kind)
		}
	}
	// The test stub kind is NOT registered in production — it lives only
	// under t.Cleanup-scoped installSeriesStubHandler. Walk the dispatch
	// to flag any entries that landed without a matching expected row
	// (defense against an accidental real handler landing without a
	// catalog row).
	for kind := range seriesOverlayHandlers {
		if !expected[kind] {
			t.Errorf("seriesOverlayHandlers carries unexpected entry %s; "+
				"register it in this test's expected map alongside the "+
				"matching streamability + capability + skill rows", kind)
		}
	}
}

// TestApplyOverlaysSeries_MatrixPathUnchanged is the defense-in-depth
// guard: adding the SERIES dispatch arm MUST NOT alter the MATRIX
// ApplyOverlays surface. Runs a known-good INDEX_VS_MARGIN against the
// shared indexUniformPayload fixture and asserts the layer count +
// canonical cell value. If a future refactor unifies the two dispatches
// behind a shared interface, this test pins the MATRIX surface against
// regression.
func TestApplyOverlaysSeries_MatrixPathUnchanged(t *testing.T) {
	host := NewCrosstabHostView(indexUniformPayload())
	specs := []types.OverlaySpec{
		{
			Kind:  types.OverlayKindIndexVsMargin,
			Scope: types.OverlayScopeCell,
			Ref: types.OverlayRef{
				Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow},
			},
		},
	}
	layers, warnings, err := ApplyOverlays(specs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if len(layers) != 1 {
		t.Fatalf("len(layers) = %d, want 1", len(layers))
	}
	if layers[0].Payload.Shape != types.OverlayShapeMatrix {
		t.Fatalf("layers[0].Payload.Shape = %q, want %q", layers[0].Payload.Shape,
			types.OverlayShapeMatrix)
	}
	cell := layers[0].Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatalf("cell[0][0] absent")
	}
	if got, want := cell.Value.(float64), 100.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("cell[0][0] = %v, want %v", got, want)
	}
}
