package types

import (
	"encoding/json"
	"testing"
)

// PanelOverlayParams.NWithinDepth (E4-S1).
//
// The representation is the story: depth 0 is a MEANINGFUL value — the
// coarsest real prefix, one dim fixed — so the field cannot be a plain
// int, whose zero value would be indistinguishable from "the caller
// never wrote the key". Every test below exists to keep the two
// states apart.

// Omitted and 0 must decode to DIFFERENT values, and the difference
// must be readable without guessing. A plain int would collapse them,
// and the collapse is silent: a caller asking for the first-dim slab
// would get the unsummed per-row margin, a smaller and entirely
// plausible number.
func TestDecodePanelParams_NWithinDepthOmittedIsNotZero(t *testing.T) {
	omitted, err := DecodePanelParams(json.RawMessage(`{"n_source":"n_within"}`))
	if err != nil {
		t.Fatalf("decode omitted: %v", err)
	}
	if omitted.NWithinDepth != nil {
		t.Fatalf("omitted n_within_depth decoded to %v, want nil", *omitted.NWithinDepth)
	}

	zero, err := DecodePanelParams(json.RawMessage(`{"n_source":"n_within","n_within_depth":0}`))
	if err != nil {
		t.Fatalf("decode zero: %v", err)
	}
	if zero.NWithinDepth == nil {
		t.Fatal("n_within_depth:0 decoded to nil; depth 0 is a meaningful value and must not read as absent")
	}
	if *zero.NWithinDepth != 0 {
		t.Fatalf("n_within_depth = %d, want 0", *zero.NWithinDepth)
	}

	two, err := DecodePanelParams(json.RawMessage(`{"n_source":"n_within","n_within_depth":2}`))
	if err != nil {
		t.Fatalf("decode two: %v", err)
	}
	if two.NWithinDepth == nil || *two.NWithinDepth != 2 {
		t.Fatalf("n_within_depth = %v, want 2", two.NWithinDepth)
	}
}

// Both carriers reach the field. The COMPOSE host hands a
// map[string]any and the per-Request host a json.RawMessage; a depth
// that decoded on one arm and not the other would refuse at predict
// and run unscoped at runtime, or the reverse. JSON numbers arrive as
// float64 through the map arm, which is exactly the coercion a second
// hand-written decoder would get wrong.
func TestDecodePanelParamsMap_NWithinDepthBothCarriers(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want int
	}{
		{"int zero", 0, 0},
		{"float64 zero", float64(0), 0},
		{"int two", 2, 2},
		{"float64 two", float64(2), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodePanelParamsMap(map[string]any{
				"n_source":       PanelNSourceNWithin,
				"n_within_depth": tc.val,
			})
			if err != nil {
				t.Fatalf("DecodePanelParamsMap: %v", err)
			}
			if got.NWithinDepth == nil {
				t.Fatal("n_within_depth decoded to nil through the map carrier")
			}
			if *got.NWithinDepth != tc.want {
				t.Fatalf("n_within_depth = %d, want %d", *got.NWithinDepth, tc.want)
			}
		})
	}
}

// A nil depth must MARSHAL away entirely. `omitempty` on a *int drops
// nil but keeps a pointer-to-zero, which is the only reason the wire
// form can carry the distinction at all — a plain int with omitempty
// would drop 0 and the round trip would lose it.
func TestPanelOverlayParams_NWithinDepthMarshalRoundTrip(t *testing.T) {
	zero := 0
	for _, tc := range []struct {
		name string
		in   PanelOverlayParams
		want string
	}{
		{"omitted", PanelOverlayParams{NSource: PanelNSourceNWithin}, `{"n_source":"n_within"}`},
		{"explicit zero", PanelOverlayParams{NSource: PanelNSourceNWithin, NWithinDepth: &zero},
			`{"n_source":"n_within","n_within_depth":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(raw) != tc.want {
				t.Fatalf("marshal = %s, want %s", raw, tc.want)
			}
			back, err := DecodePanelParams(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if (back.NWithinDepth == nil) != (tc.in.NWithinDepth == nil) {
				t.Fatalf("round trip lost the omitted/zero distinction: got %v, want %v",
					back.NWithinDepth, tc.in.NWithinDepth)
			}
			if back.NWithinDepth != nil && *back.NWithinDepth != *tc.in.NWithinDepth {
				t.Fatalf("round trip = %d, want %d", *back.NWithinDepth, *tc.in.NWithinDepth)
			}
		})
	}
}

// Exactly one mode consumes the depth. Both arms key their "depth set
// without a mode that reads it" refusal off this predicate, so a mode
// added later that forgets to join the set gets a hard refusal rather
// than an inert param.
func TestPanelNSourceUsesWithinDepth(t *testing.T) {
	if !PanelNSourceUsesWithinDepth(PanelNSourceNWithin) {
		t.Errorf("%q must consume n_within_depth", PanelNSourceNWithin)
	}
	for _, mode := range []string{"", PanelNSourceRowMarginValue, PanelNSourceCellNUnweighted, "nonsense"} {
		if PanelNSourceUsesWithinDepth(mode) {
			t.Errorf("mode %q must not consume n_within_depth", mode)
		}
	}
}

// n_within is a real member of the closed enum, not a value the
// validator merely tolerates. Without this, a mode wired into the
// runtime switch but omitted from ValidPanelNSource would be refused
// at both gates and never reachable — the failure E1-S4's own
// falsification found on the axis-pairing family.
func TestValidPanelNSource_AcceptsNWithin(t *testing.T) {
	if !ValidPanelNSource(PanelNSourceNWithin) {
		t.Fatalf("ValidPanelNSource(%q) = false", PanelNSourceNWithin)
	}
	if PanelNSourceNWithin != "n_within" {
		t.Fatalf("PanelNSourceNWithin = %q, want the wire value \"n_within\"", PanelNSourceNWithin)
	}
	var found bool
	for _, s := range PanelNSources() {
		if s == PanelNSourceNWithin {
			found = true
		}
	}
	if !found {
		t.Fatalf("PanelNSources() = %v, omits %q; the diagnostic would not suggest it",
			PanelNSources(), PanelNSourceNWithin)
	}
}
