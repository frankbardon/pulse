package types

import (
	"encoding/json"
	"testing"
)

// The panel's n_source enum. Three properties are load-bearing and
// each is asserted separately: the empty string is the legacy default
// and must stay valid; the enum is closed; and the two derived
// predicates (does it read components, does it carry the legacy
// cell-value fallback) must disagree with each other, because a mode
// that reads components must NOT inherit the fallback.

func TestValidPanelNSource(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", true},
		{PanelNSourceRowMarginN, true},
		{PanelNSourceCellNUnweighted, true},

		// Rejected. The first three are the crosstab family's modes:
		// PairwiseOverlayParams and PanelOverlayParams are separate
		// types precisely so each host offers only the modes it can
		// actually serve, and a shared struct would have leaked these.
		{PairwiseNSourceColumnMarginN, false},
		{PairwiseNSourceNWithin, false},
		{PairwiseNSourceRowMarginDistinct, false},
		{"ROW_MARGIN_N", false},
		{"row_margin", false},
		{"nonsense", false},
	} {
		if got := ValidPanelNSource(tc.in); got != tc.want {
			t.Errorf("ValidPanelNSource(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// PanelNSources feeds the "valid: …" half of the predict and runtime
// diagnostics, so it must list every mode ValidPanelNSource accepts
// except the empty string — which is the ABSENCE of a mode, not one of
// them. Suggesting "" to a caller who mistyped would tell them to
// delete the key when they meant to fix it.
func TestPanelNSources_MatchesValidatorMinusEmpty(t *testing.T) {
	got := PanelNSources()
	if len(got) == 0 {
		t.Fatal("PanelNSources() is empty")
	}
	for _, s := range got {
		if s == "" {
			t.Error("PanelNSources() lists the empty string; it is the absence of a mode, not a mode")
		}
		if !ValidPanelNSource(s) {
			t.Errorf("PanelNSources() lists %q, which ValidPanelNSource rejects", s)
		}
	}
	want := map[string]bool{PanelNSourceRowMarginN: true, PanelNSourceCellNUnweighted: true}
	if len(got) != len(want) {
		t.Fatalf("PanelNSources() = %v, want exactly %d entries", got, len(want))
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("PanelNSources() carries unexpected mode %q", s)
		}
		delete(want, s)
	}
	for s := range want {
		t.Errorf("PanelNSources() omits %q", s)
	}
	// Default first: the diagnostic reads as a list and the default
	// belongs at its head.
	if got[0] != PanelNSourceRowMarginN {
		t.Errorf("PanelNSources()[0] = %q, want the default %q", got[0], PanelNSourceRowMarginN)
	}
}

// The returned slice is a COPY. A caller mutating a diagnostic's
// Details["valid_n_sources"] must not be able to redefine the enum for
// every later request in the process.
func TestPanelNSources_ReturnsACopy(t *testing.T) {
	first := PanelNSources()
	first[0] = "clobbered"
	second := PanelNSources()
	if second[0] != PanelNSourceRowMarginN {
		t.Fatalf("PanelNSources() leaked its backing array: second call = %v", second)
	}
}

// Only the counted mode reads components, and only the legacy default
// carries the <= 0 cell-value fallback. The two predicates must be
// DISJOINT over the enum: a mode that reads a counted figure and then
// substitutes a cell VALUE for it would reintroduce exactly the
// substitution the counted mode exists to remove.
func TestPanelNSourcePredicates_AreDisjoint(t *testing.T) {
	for _, mode := range append([]string{""}, PanelNSources()...) {
		reads := PanelNSourceReadsComponents(mode)
		falls := PanelNSourceFallsBackToCellValue(mode)
		if reads && falls {
			t.Errorf("mode %q both reads components and falls back to the cell value", mode)
		}
	}
	if !PanelNSourceFallsBackToCellValue("") {
		t.Error("the empty (legacy) mode must keep the cell-value fallback")
	}
	if !PanelNSourceFallsBackToCellValue(PanelNSourceRowMarginN) {
		t.Error("row_margin_n IS the legacy mode and must keep the cell-value fallback")
	}
	if PanelNSourceFallsBackToCellValue(PanelNSourceCellNUnweighted) {
		t.Error("cell_n_unweighted must NOT inherit the legacy cell-value fallback")
	}
	if PanelNSourceReadsComponents("") {
		t.Error("the empty (legacy) mode must not demand components")
	}
	if PanelNSourceReadsComponents(PanelNSourceRowMarginN) {
		t.Error("row_margin_n reads the payload margin and must not demand components")
	}
	if !PanelNSourceReadsComponents(PanelNSourceCellNUnweighted) {
		t.Error("cell_n_unweighted must read components")
	}
}

// Both carriers reach the same field. The COMPOSE host hands a
// map[string]any and the per-Request host a json.RawMessage; a mode
// that decoded on one arm and not the other would refuse at predict
// and run the default at runtime, or the reverse.
func TestDecodePanelParams_NSourceBothCarriers(t *testing.T) {
	raw, err := DecodePanelParams(json.RawMessage(`{"n_source":"cell_n_unweighted"}`))
	if err != nil {
		t.Fatalf("DecodePanelParams: %v", err)
	}
	if raw.NSource != PanelNSourceCellNUnweighted {
		t.Errorf("raw carrier NSource = %q, want %q", raw.NSource, PanelNSourceCellNUnweighted)
	}
	m, err := DecodePanelParamsMap(map[string]any{"n_source": "cell_n_unweighted"})
	if err != nil {
		t.Fatalf("DecodePanelParamsMap: %v", err)
	}
	if m.NSource != raw.NSource {
		t.Errorf("map carrier NSource = %q, raw carrier = %q; the two hosts disagree", m.NSource, raw.NSource)
	}
}

// An absent or empty params blob decodes to the legacy default on both
// carriers. This is the type-level half of the byte-identity pin.
func TestDecodePanelParams_AbsentMeansLegacyDefault(t *testing.T) {
	for name, got := range map[string]PanelOverlayParams{
		"nil raw":   mustDecodeRaw(t, nil),
		"empty obj": mustDecodeRaw(t, json.RawMessage(`{}`)),
		"nil map":   mustDecodeMap(t, nil),
		"empty map": mustDecodeMap(t, map[string]any{}),
	} {
		if got.NSource != "" {
			t.Errorf("%s: NSource = %q, want the empty legacy default", name, got.NSource)
		}
		if !PanelNSourceFallsBackToCellValue(got.NSource) {
			t.Errorf("%s: the decoded zero value lost the legacy fallback", name)
		}
		if PanelNSourceReadsComponents(got.NSource) {
			t.Errorf("%s: the decoded zero value demands components", name)
		}
	}
}

// An unknown n_source VALUE decodes cleanly and is caught by the
// validator, not the decoder. Unknown params KEYS stay accepted
// (forward compatibility against an older binary); an unknown value in
// a key this binary DOES know is a different failure and belongs to
// ValidPanelNSource, so the diagnostic can name the valid set.
func TestDecodePanelParams_UnknownValueDecodesAndValidatorRefuses(t *testing.T) {
	p, err := DecodePanelParamsMap(map[string]any{"n_source": "row_margin_distinct"})
	if err != nil {
		t.Fatalf("an unknown n_source VALUE must decode cleanly, got %v", err)
	}
	if ValidPanelNSource(p.NSource) {
		t.Fatalf("ValidPanelNSource accepted %q", p.NSource)
	}
	if _, err := DecodePanelParamsMap(map[string]any{"not_a_panel_param": 1}); err != nil {
		t.Fatalf("an unknown params KEY must still decode cleanly, got %v", err)
	}
}

// A non-string n_source is a TYPE mismatch the decoder owns — it can
// never become a valid mode, so it must not reach the validator as an
// empty string and silently run the default.
func TestDecodePanelParams_NonStringNSourceRefused(t *testing.T) {
	if _, err := DecodePanelParamsMap(map[string]any{"n_source": 7}); err == nil {
		t.Fatal("a numeric n_source must not decode to the empty default")
	}
}

func mustDecodeRaw(t *testing.T, raw json.RawMessage) PanelOverlayParams {
	t.Helper()
	p, err := DecodePanelParams(raw)
	if err != nil {
		t.Fatalf("DecodePanelParams(%s): %v", raw, err)
	}
	return p
}

func mustDecodeMap(t *testing.T, m map[string]any) PanelOverlayParams {
	t.Helper()
	p, err := DecodePanelParamsMap(m)
	if err != nil {
		t.Fatalf("DecodePanelParamsMap(%v): %v", m, err)
	}
	return p
}
