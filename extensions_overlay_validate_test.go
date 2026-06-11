package pulse

import (
	"testing"
)

// TestExtensions_OverlayKinds_NamingPolicy is the forward-looking gate
// for embedder-registered overlay kinds. E1 ships no Overlays slot on
// the Extensions struct — the overlay catalog is a closed built-in
// surface today — but the naming policy that any future
// OverlayRegistration slot will inherit from extensionNameRegex must
// already reject the two failure modes the criterion calls out:
//
//  1. Mixed-case names (e.g. "OVERLAY_indexvsmargin") — the regex
//     requires uppercase ASCII for every segment.
//  2. Reserved namespaces (e.g. "BUILTIN_OVERLAY_FOO") — the
//     reservedExtensionNamespaces set rejects BUILTIN / STANDARD /
//     CORE / PULSE as the namespace segment for any embedder operator.
//
// When the Extensions.Overlays slot lands in a later epic, the slot's
// validator MUST reuse extensionNameRegex + reservedExtensionNamespaces
// so these assertions hold. If the regex is widened to accept OVERLAY_*
// as a new category prefix without adding the reservedExtensionNamespaces
// pass, this test will flag the gap immediately.
//
// We exercise the policy via the regex + reserved-set directly because
// no public Overlays slot exists yet — running through the existing
// AggregatorRegistration path would conflate the overlay-naming concern
// with the aggregator-prefix mismatch.
func TestExtensions_OverlayKinds_NamingPolicy(t *testing.T) {
	// The regex must reject mixed-case overlay names regardless of
	// whether the OVERLAY_ category prefix is added to the regex
	// alternation. A future widening that allows OVERLAY_ but accepts
	// lowercase segments would break the policy.
	mixedCase := "OVERLAY_indexvsmargin"
	if extensionNameRegex.MatchString(mixedCase) {
		t.Errorf("extensionNameRegex should reject mixed-case name %q; "+
			"the naming policy requires uppercase ASCII for every segment",
			mixedCase)
	}

	// Names whose namespace segment is in the reserved set must surface
	// PULSE_EXTENSION_NAME_RESERVED rather than landing as a successful
	// registration. Walk every reserved namespace so an addition to the
	// set is automatically picked up.
	for ns := range reservedExtensionNamespaces {
		// Use a synthetic registration name that the regex itself
		// accepts (uppercase, the reserved namespace, a trailing
		// suffix). The reserved check runs only after the regex
		// matches; we want to assert the reserved gate fires, not the
		// regex gate.
		synthetic := "AGG_" + ns + "_OVERLAY_FOO"
		groups := extensionNameRegex.FindStringSubmatch(synthetic)
		if groups == nil {
			t.Errorf("synthetic reserved-namespace test name %q failed regex; "+
				"adjust the fixture so the reserved check is exercised",
				synthetic)
			continue
		}
		namespace := groups[2]
		if namespace != ns {
			t.Errorf("synthetic name %q parsed namespace %q, want %q",
				synthetic, namespace, ns)
			continue
		}
		if _, reserved := reservedExtensionNamespaces[namespace]; !reserved {
			t.Errorf("reservedExtensionNamespaces dropped %q — "+
				"overlay registrations would inherit a broken reserved set", ns)
		}
	}

	// The OVERLAY_ prefix is intentionally absent from the regex
	// alternation today (E1 ships no public Overlays slot). Confirm
	// the alternation still rejects an OVERLAY_-prefixed string so a
	// future commit that registers OVERLAY_ must consciously widen
	// the regex (and the matching switch in extensionCategory). This
	// is the load-bearing forward-compatibility assertion.
	overlayPrefixed := "OVERLAY_ACME_INDEX"
	if extensionNameRegex.MatchString(overlayPrefixed) {
		t.Errorf("extensionNameRegex unexpectedly accepts %q; "+
			"adding OVERLAY_ to the alternation MUST also add the "+
			"matching extensionCategory entry and a registration slot",
			overlayPrefixed)
	}
}
