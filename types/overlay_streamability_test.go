package types

import "testing"

// TestStreamability_OverlaysKnown asserts every overlay kind returns
// the documented streamability value AND is enumerated in
// AllOverlayKinds(). Adding a new overlay kind requires extending the
// OverlayStreamability map in overlay_streamability.go, appending the
// constant to AllOverlayKinds() in overlay.go, AND adding an expected
// row here.
//
// Joins the TestStreamability_*Known family listed under CLAUDE.md
// "Non-Skippable CI Gates". A missing row, a stale row, or a divergent
// value all fail the gate so the table cannot silently drift.
func TestStreamability_OverlaysKnown(t *testing.T) {
	expected := map[OverlayKind]bool{
		// CHISQ_COL is inferential — mechanical column-axis twin of
		// CHISQ_ROW; each per-column goodness-of-fit test reads the
		// column's observed row distribution AND every row margin to
		// drive the expected-count recurrence, all of which require the
		// buffered host crosstab matrix.
		OverlayKindChiSqCol: false,
		// CHISQ_MATRIX is inferential and consumes the whole-matrix
		// contingency table — the host crosstab path is buffered and
		// the chi-square test itself walks the materialised matrix.
		OverlayKindChiSqMatrix: false,
		// CHISQ_ROW is inferential — each per-row goodness-of-fit test
		// reads the row's observed column distribution AND every column
		// margin to drive the expected-count recurrence, all of which
		// require the buffered host crosstab matrix.
		OverlayKindChiSqRow: false,
		// DELTA_VS_MARGIN shares INDEX_VS_MARGIN's buffered footprint —
		// its margin centerpoint is recomputed by the buffered crosstab
		// orchestrator before ApplyOverlays runs.
		OverlayKindDeltaVsMargin: false,
		// INDEX_VS_MARGIN rides on the buffered crosstab path — margins
		// are always recomputed from raw rows, so the host operator is
		// inherently buffered (see CLAUDE.md "Execution modes" →
		// Crosstab).
		OverlayKindIndexVsMargin: false,
		// SHARE_OF_COL shares INDEX_VS_MARGIN's buffered footprint — its
		// column-margin denominator is recomputed by the buffered
		// crosstab orchestrator before ApplyOverlays runs.
		OverlayKindShareOfCol: false,
		// SHARE_OF_ROW shares INDEX_VS_MARGIN's buffered footprint — its
		// row-margin denominator is recomputed by the buffered crosstab
		// orchestrator before ApplyOverlays runs.
		OverlayKindShareOfRow: false,
		// SHARE_OF_TOTAL shares INDEX_VS_MARGIN's buffered footprint —
		// its grand-total denominator is recomputed by the buffered
		// crosstab orchestrator before ApplyOverlays runs. The
		// streamable series-shape variant under Process context is
		// deferred to E3.
		OverlayKindShareOfTotal: false,
		// ZSCORE_VS_MARGIN shares INDEX_VS_MARGIN's buffered footprint —
		// both the per-slice Welford recurrence and the margin
		// centerpoint require a fully-materialised matrix, so the host
		// crosstab path stays buffered.
		OverlayKindZScoreVsMargin: false,
	}

	for _, k := range AllOverlayKinds() {
		want, ok := expected[k]
		if !ok {
			t.Fatalf("overlay kind %s missing from streamability table — declare it in types/overlay_streamability.go and add an entry here", k)
		}
		got, known := OverlayStreamable(k)
		if !known {
			t.Errorf("OverlayStreamable(%s) returned known=false; AllOverlayKinds() entry has no row in OverlayStreamability", k)
		}
		if got != want {
			t.Errorf("OverlayStreamable(%s) = %v, want %v", k, got, want)
		}
	}

	if len(expected) != len(AllOverlayKinds()) {
		t.Fatalf("overlay streamability table size mismatch: %d expected entries, %d kinds in AllOverlayKinds()", len(expected), len(AllOverlayKinds()))
	}
	if len(OverlayStreamability) != len(AllOverlayKinds()) {
		t.Fatalf("OverlayStreamability map size mismatch: %d rows, %d kinds in AllOverlayKinds()", len(OverlayStreamability), len(AllOverlayKinds()))
	}
}

// TestOverlayStreamable_UnknownKind asserts that an unknown overlay
// kind returns (false, false) — the contract callers (validator,
// predict gate) rely on to surface PULSE_OVERLAY_UNKNOWN-style
// diagnostics rather than silently streaming.
func TestOverlayStreamable_UnknownKind(t *testing.T) {
	streamable, known := OverlayStreamable("OVERLAY_DOES_NOT_EXIST")
	if known {
		t.Errorf("OverlayStreamable(unknown) known = true, want false")
	}
	if streamable {
		t.Errorf("OverlayStreamable(unknown) streamable = true, want false")
	}
}

// TestOverlayStreamable_KnownIndexVsMargin asserts the explicit
// acceptance-criterion case from E1-S2: OVERLAY_INDEX_VS_MARGIN must
// return (false, true).
func TestOverlayStreamable_KnownIndexVsMargin(t *testing.T) {
	streamable, known := OverlayStreamable(OverlayKindIndexVsMargin)
	if !known {
		t.Errorf("OverlayStreamable(OVERLAY_INDEX_VS_MARGIN) known = false, want true")
	}
	if streamable {
		t.Errorf("OverlayStreamable(OVERLAY_INDEX_VS_MARGIN) streamable = true, want false (host crosstab is buffered)")
	}
}
