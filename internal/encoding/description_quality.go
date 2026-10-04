package encoding

import (
	"slices"
	"strings"
)

// lowQualityDescriptions are descriptions too generic to tell a reader
// anything about the field, compared after trimming and lowercasing.
var lowQualityDescriptions = []string{"n/a", "na", "none", "tbd", "todo", "unknown", "field", "data", "value", "column"}

// IsLowQualityDescription reports whether a field description is empty,
// shorter than 10 bytes once trimmed, or one of the generic
// placeholders — the PULSE_FIELD_DESCRIPTION_LOW_QUALITY rule. It is
// the one predicate predict's description check and the cohort
// builder's schema check share, so the two cannot disagree on which
// fields draw the finding.
func IsLowQualityDescription(desc string) bool {
	if desc == "" {
		return true
	}
	trimmed := strings.TrimSpace(desc)
	if len(trimmed) < 10 {
		return true
	}
	return slices.Contains(lowQualityDescriptions, strings.ToLower(trimmed))
}
