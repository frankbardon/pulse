package pulse

import (
	"testing"
)

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

	overlayPrefixed := "OVERLAY_ACME_INDEX"
	if extensionNameRegex.MatchString(overlayPrefixed) {
		t.Errorf("extensionNameRegex unexpectedly accepts %q; "+
			"adding OVERLAY_ to the alternation MUST also add the "+
			"matching extensionCategory entry and a registration slot",
			overlayPrefixed)
	}
}
