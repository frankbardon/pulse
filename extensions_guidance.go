package pulse

import (
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// guidanceEntry is one registration's optional guidance, tagged for
// validation messages.
type guidanceEntry struct {
	category       extensionCategory
	name           string
	index          int
	purpose        *descriptor.Purpose
	interpretation []descriptor.Interpretation
}

// guidanceEntries lists every registration in validation order:
// per category (the validateExtensions order), then by slice index.
func guidanceEntries(ext Extensions) []guidanceEntry {
	var out []guidanceEntry
	for i, r := range ext.Aggregators {
		out = append(out, guidanceEntry{categoryAggregator, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Attributes {
		out = append(out, guidanceEntry{categoryAttribute, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Filterers {
		out = append(out, guidanceEntry{categoryFilterer, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Groupers {
		out = append(out, guidanceEntry{categoryGrouper, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Windows {
		out = append(out, guidanceEntry{categoryWindow, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Features {
		out = append(out, guidanceEntry{categoryFeature, string(r.Name), i, r.Purpose, nil})
	}
	for i, r := range ext.Tests {
		out = append(out, guidanceEntry{categoryTest, string(r.Name), i, r.Purpose, r.Interpretation})
	}
	for i, r := range ext.SynthDistributions {
		out = append(out, guidanceEntry{categoryDistribution, r.Name, i, r.Purpose, nil})
	}
	return out
}

// extensionPurposeResolver resolves a NotFor.Use target against the
// instance registry: every built-in surface and feature-table row
// (descx.BuiltinPurposeResolver) plus every operator name ext
// registers, so one extension may point at another.
func extensionPurposeResolver(ext Extensions) descx.PurposeResolver {
	builtin := descx.BuiltinPurposeResolver()
	names := map[string]bool{}
	for _, e := range guidanceEntries(ext) {
		names[e.name] = true
	}
	return func(use string) bool {
		return names[use] || builtin(use)
	}
}

// validateExtensionGuidance checks every registration's optional
// Purpose with the built-in tier's rules (descx.ValidatePurpose) plus
// alias uniqueness (a KnownAs alias no built-in or earlier extension
// declares — extensionKnownAsCollisions), and a test registration's Interpretation for structure only
// (descx.ValidateInterpretations with a nil resolver: output keys are
// not probed). The first registration with a violation — validation
// order, Purpose before Interpretation — fails with
// PULSE_EXTENSION_PURPOSE_INVALID naming it, the first failing rule and
// every violation. It runs at every pulse.New, after DependsOn.
func validateExtensionGuidance(ext Extensions) error {
	resolve := extensionPurposeResolver(ext)
	aliases := descx.BuiltinKnownAs()
	for _, e := range guidanceEntries(ext) {
		if e.purpose != nil {
			vs := descx.ValidatePurpose(e.name, *e.purpose, resolve)
			vs = append(vs, extensionKnownAsCollisions(e.name, e.purpose.KnownAs, aliases)...)
			if len(vs) > 0 {
				lines := make([]string, len(vs))
				rules := make([]string, len(vs))
				for i, v := range vs {
					lines[i] = v.String()
					rules[i] = string(v.Rule)
				}
				return guidanceError(e, "purpose", rules, lines)
			}
		}
		if len(e.interpretation) > 0 {
			if vs := descx.ValidateInterpretations(e.name, e.interpretation, nil); len(vs) > 0 {
				lines := make([]string, len(vs))
				rules := make([]string, len(vs))
				for i, v := range vs {
					lines[i] = v.String()
					rules[i] = string(v.Rule)
				}
				return guidanceError(e, "interpretation", rules, lines)
			}
		}
	}
	return nil
}

// extensionKnownAsCollisions reports every alias of the extension
// operator name already claimed — by a built-in (the full registry,
// profile-blind) or by an extension earlier in validation order — then
// claims the rest in taken, so aliases stay unique across the instance.
func extensionKnownAsCollisions(name string, known []string, taken map[string]string) []descx.PurposeViolation {
	var out []descx.PurposeViolation
	for _, a := range known {
		f := descx.FoldAlias(a)
		if f == "" {
			continue
		}
		if owner, ok := taken[f]; ok && owner != name {
			out = append(out, descx.PurposeViolation{Name: name, Rule: descx.PurposeRuleKnownAs,
				Detail: fmt.Sprintf("KnownAs %q is already declared by %s", a, owner)})
			continue
		}
		taken[f] = name
	}
	return out
}

func guidanceError(e guidanceEntry, part string, rules, lines []string) error {
	return errors.NewCodedErrorWithDetails(
		errors.PULSE_EXTENSION_PURPOSE_INVALID,
		fmt.Sprintf("extension %s %q: invalid %s: %s", e.category, e.name, part, strings.Join(lines, "; ")),
		map[string]any{
			"category":   string(e.category),
			"name":       e.name,
			"index":      e.index,
			"part":       part,
			"rule":       rules[0],
			"rules":      rules,
			"violations": lines,
		},
	)
}

// extensionGuidance returns the snapshot's Purpose and Interpretation
// maps, keyed by registered name; nil when no registration declares
// any.
func extensionGuidance(ext Extensions) (map[string]descriptor.Purpose, map[string][]descriptor.Interpretation) {
	var purposes map[string]descriptor.Purpose
	var interps map[string][]descriptor.Interpretation
	for _, e := range guidanceEntries(ext) {
		if e.purpose != nil {
			if purposes == nil {
				purposes = map[string]descriptor.Purpose{}
			}
			purposes[e.name] = *e.purpose
		}
		if len(e.interpretation) > 0 {
			if interps == nil {
				interps = map[string][]descriptor.Interpretation{}
			}
			interps[e.name] = append([]descriptor.Interpretation(nil), e.interpretation...)
		}
	}
	return purposes, interps
}
