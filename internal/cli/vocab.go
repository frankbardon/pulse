package cli

import "strings"

// Closed flag vocabularies are never written down here: each enumerated
// flag's validator, its Usage line (and its shell completion) read the
// library's own lister — pio.FloatWidths, pio.DSTPolicies,
// pio.SPSSMissingModes, pio.Formats, types.AllLookupMultiplicities,
// types.AllLabelModes, types.AllReturnPresets, descriptor.Levels,
// descriptor.ExplainRequestRoots, descriptor.ExplainDetails,
// pulse.RakeMissingPolicies, pulse.LimitNames,
// pulse.WidenSetFieldTargets, errors.AllDomains. The one CLI-owned
// vocabulary is the root logging flags' (logLevelNames /
// logFormatNames): the CLI builds the logger, so it owns the spellings.

// strs converts a slice of string-kinded enum values to []string.
func strs[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}

// orList spells values as "a, b or c" (a lone value as itself).
func orList[T ~string](vs []T) string {
	s := strs(vs)
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " or " + s[len(s)-1]
}

// pipeList spells values as "a|b|c".
func pipeList[T ~string](vs []T) string { return strings.Join(strs(vs), "|") }

// commaList spells values as "a, b, c".
func commaList[T ~string](vs []T) string { return strings.Join(strs(vs), ", ") }
