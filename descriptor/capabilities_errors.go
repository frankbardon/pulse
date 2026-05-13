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
// TestManifest_ErrorCodesSlim asserts length parity with
// errors.AllCodes() and alphabetical ordering. TestCodesHaveFixups
// (in errors/) enforces that every listed code carries metadata.
func errorCodeNames() []string {
	return errors.SortedCodeNames()
}

// errorCodesCount returns the total registered code count. Convenience
// for callers that only need the headline number without parsing the
// names slice.
func errorCodesCount() int {
	return len(errors.AllCodes())
}

// errorDomains returns the alphabetized list of distinct domain
// prefixes registered today (CLI, DATA, ENCODING, PROCESSING, PULSE,
// SERVICE in v1).
func errorDomains() []string {
	return errors.AllDomains()
}
