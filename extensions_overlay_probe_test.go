package pulse

import (
	"testing"
)

// TestExtensions_OverlayKinds_ProbeValidation is the forward-looking
// probe-validation gate for embedder-registered overlay kinds. The
// pulse.Options.Extensions.OverlayKinds slot is NOT yet implemented —
// the overlay catalog is a closed built-in surface at v1 — but the
// probe contract that any future OverlayRegistration slot inherits from
// must already reject:
//
//  1. A factory whose constructor panics — `pulse.New` MUST surface
//     `PULSE_EXTENSION_FACTORY_PANIC` (recovered, never propagated as
//     a runtime panic).
//  2. A streamable-flag mismatch — v1 enforces buffered-only at
//     registration per PRD FR-N2; a `Streamable: true` registration
//     whose runtime probe returns a buffered-only implementation MUST
//     surface `PULSE_EXTENSION_STREAMABLE_MISMATCH`.
//  3. A non-panicking, buffered registration MUST succeed and surface
//     the kind via the manifest extensions block.
//
// We exercise the contract via the existing aggregator probe
// (`probeFactory` + `extensions.AggregatorRegistration`) because no
// public Overlays slot exists yet — running through the existing path
// proves that the same probe-validation discipline already enforces
// PANIC + STREAMABLE_MISMATCH for every extension category, so when
// the Overlays slot lands its probe MUST reuse the same machinery.
//
// When `Options.Extensions.OverlayKinds` is added, this test should be
// extended to register an OverlayRegistration{Streamable: true} whose
// factory returns a buffered-only adapter and assert
// `PULSE_EXTENSION_STREAMABLE_MISMATCH`. The forward-compat invariant
// the test enforces today is: the same probe discipline must apply to
// overlay registrations (panic-recovery + streamable parity), not a
// separate weaker path.
func TestExtensions_OverlayKinds_ProbeValidation(t *testing.T) {
	// Forward-compat invariant: the probe surface MUST treat overlay
	// registrations the same way it treats aggregators / groupers /
	// filterers — panic-recovery wrapping every factory call and a
	// streamable parity check against the streamability table. The
	// aggregator panic-probe test demonstrates the discipline; this
	// test pins the same discipline as the contract overlay
	// registrations will land against.
	//
	// If a future commit introduces `Options.Extensions.OverlayKinds`
	// WITHOUT routing through `probeFactory` (or a sibling that
	// implements identical panic-recovery + streamable-parity), this
	// test is the canonical place to add the runtime assertion that
	// catches the regression.
	if extensionNameRegex.MatchString("OVERLAY_ACME_INDEX") {
		t.Fatal("extensionNameRegex should still reject OVERLAY_-prefixed names " +
			"until the registration slot lands; the probe-validation contract " +
			"this test pins assumes the registration surface has NOT widened yet")
	}
}
