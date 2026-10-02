package descriptor

import (
	"github.com/frankbardon/pulse/errors"
)

// errorCodeNames returns every registered error code as a string,
// alphabetized. Source of truth: errors.SortedCodeNames().
//
// Per-code Message + Fixup prose is intentionally NOT embedded in the
// manifest. Callers fetch per-code detail on demand via the
// `pulse_errors_lookup` MCP tool or `pulse errors lookup CODE` CLI
// leaf. Manifest stays lean; errors are reactive lookup, not bootstrap
// reference.
//
// The manifest lists the instance's visible subset (errorCodeNamesFor,
// errors_instance.go); count and domains derive from that subset.
//
// TestManifest_ErrorCodesSlim asserts length parity with
// errors.AllCodes() and alphabetical ordering. TestCodesHaveFixups
// (in errors/) enforces that every listed code carries metadata.
func errorCodeNames() []string {
	return errors.SortedCodeNames()
}
