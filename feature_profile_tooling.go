package pulse

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Feature-profile tooling: root functions that need no *Pulse, so an
// embedder (or the `pulse features` CLI) can write, check, upgrade and
// describe a feature profile in CI. Every result type is JSON-tagged and
// carries plain strings — the feature model itself stays internal.

// Sources reported by FeatureDescription.Source.
const (
	featureSourceBuiltin   = "builtin"
	featureSourceExtension = "extension"
	featureSourceUnknown   = "unknown"
)

// featureUnknownUnverifiedExtension is the "reason" detail of the
// warning CheckFeatureProfile raises in offline mode for a name that
// follows the extension naming policy but is not registered.
const featureUnknownUnverifiedExtension = "unverified_extension"

// unverifiedExtensionMessage is the prose of that warning.
const unverifiedExtensionMessage = "unverified extension name, check in-process"

// listBuiltinFeatureNames lists the built-in feature table. A package
// variable only so tests can inject a row (alongside
// lookupBuiltinFeature); production always reads the internal table.
var listBuiltinFeatureNames = descx.FeatureNames

// FeatureProfileCheckOptions configures CheckFeatureProfile.
type FeatureProfileCheckOptions struct {
	// Extensions is the same value handed to pulse.New through
	// Options.Extensions. Its operator registrations are features a
	// profile may list; the registrations are validated exactly as
	// pulse.New validates them.
	Extensions Extensions

	// Offline checks a profile without the embedder's extensions in
	// hand (the `pulse features check` CLI). A listed name that is not
	// registered but follows the extension naming policy — a
	// non-SYNTH operator category, a non-reserved namespace — is
	// reported as a warning ("unverified extension name, check
	// in-process") instead of PULSE_FEATURE_PROFILE_UNKNOWN; every other
	// unknown name stays an error. For the dependency class such a name
	// counts as enabled and needs only a request host, like an extension
	// with no DependsOn.
	Offline bool
}

// FeatureProfileCheck is the report CheckFeatureProfile returns.
type FeatureProfileCheck struct {
	// Profile and WrittenWith echo the checked profile.
	Profile     string `json:"profile,omitempty"`
	WrittenWith string `json:"written_with,omitempty"`

	// Version is the running Pulse version the profile was checked
	// against (pulse.Version()).
	Version string `json:"version"`

	// Valid is true when the profile passes every validation class.
	Valid bool `json:"valid"`

	// Features is the number of feature names the profile lists.
	Features int `json:"features"`

	// Unverified lists, sorted, the names an offline check accepted
	// without verifying them. Never null.
	Unverified []string `json:"unverified"`

	// Warnings carries one entry per unverified name, coded
	// PULSE_FEATURE_PROFILE_UNKNOWN with details {name, reason:
	// "unverified_extension"} — ready for an envelope's warnings. Never
	// null.
	Warnings []*descriptor.EnvelopeEntry `json:"warnings"`
}

// FeatureProfileUnknownName explains why a listed name does not resolve
// against the running Pulse. Reason is one of the
// PULSE_FEATURE_PROFILE_UNKNOWN reasons: unregistered, pattern,
// wrong_kind (DidYouMean names the valid spelling), core_surface,
// newer_than_running (Since names the release that introduces it).
type FeatureProfileUnknownName struct {
	Name       string `json:"name"`
	Reason     string `json:"reason"`
	DidYouMean string `json:"did_you_mean,omitempty"`
	Since      string `json:"since,omitempty"`
}

// FeatureProfileMissing is a feature the running Pulse offers that a
// profile does not list.
type FeatureProfileMissing struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Category string `json:"category,omitempty"`
	// Since is the release that introduced a built-in feature; empty for
	// an extension operator.
	Since string `json:"since,omitempty"`
	// New is true when Since is later than the profile's written_with —
	// the feature did not exist when the profile was written. Always
	// false when written_with is absent or has no release core.
	New bool `json:"new"`
}

// FeatureProfileDiff is the report DiffFeatureProfile returns.
type FeatureProfileDiff struct {
	Profile     string `json:"profile,omitempty"`
	WrittenWith string `json:"written_with,omitempty"`
	Version     string `json:"version"`

	// Missing lists, in canonical profile order, every feature the
	// running Pulse offers (built-ins it has reached plus the given
	// extension operators) that the profile does not list. Never null.
	Missing []FeatureProfileMissing `json:"missing"`

	// Unknown lists, sorted by name, the profile's names the running
	// Pulse does not resolve. Reported, not fatal. Never null.
	Unknown []FeatureProfileUnknownName `json:"unknown"`
}

// FeatureDescription describes one feature a profile lists.
type FeatureDescription struct {
	Name string `json:"name"`
	// Kind is operator, capability, io_format or mcp_extra.
	Kind string `json:"kind"`
	// Category is an operator's category (AGG, GROUP, OVERLAY …);
	// empty for other kinds.
	Category string `json:"category,omitempty"`
	// Source is builtin, extension or unknown.
	Source string `json:"source"`
	// Since is the release that introduced a built-in; empty otherwise.
	Since string `json:"since,omitempty"`
	// DependsOn is the dependency edges: an AND of any-of groups — for
	// every group at least one member must be enabled. Never null.
	DependsOn [][]string `json:"depends_on"`
	// Unknown explains a name the running Pulse does not resolve; nil
	// for a resolvable one.
	Unknown *FeatureProfileUnknownName `json:"unknown,omitempty"`
}

// FeatureProfileDescription is the report DescribeFeatureProfile
// returns.
type FeatureProfileDescription struct {
	Profile     string `json:"profile,omitempty"`
	WrittenWith string `json:"written_with,omitempty"`
	Version     string `json:"version"`
	// Features describes every listed name, in canonical profile order.
	// Never null.
	Features []FeatureDescription `json:"features"`
}

// InitFeatureProfile generates a feature profile for the running Pulse.
//
// With from empty it lists every feature the running build offers —
// every built-in whose Since it has reached, plus the operator
// registrations of ext when given — by exact name. With from naming a
// published example (ExampleFeatureProfiles) it starts from that
// example's label, features and behaviour instead; extension names are
// not added to a seeded profile. Features are in canonical profile
// order (by kind, then operator category, then feature-table position).
//
// WrittenWith is the running version's release core (`major.minor.patch`,
// pre-release and build suffixes cut — the comparison Since uses); a
// build with no release core (`devel`) stamps the newest release line
// its feature table knows.
//
// ext is the value handed to pulse.New through Options.Extensions;
// several values are merged registration by registration. The result
// passes CheckFeatureProfile with the same ext, or InitFeatureProfile
// returns that check's coded error. An unknown example is
// PULSE_FEATURE_PROFILE_INVALID reason "unknown_example".
func InitFeatureProfile(from string, ext ...Extensions) (*FeatureProfile, error) {
	merged := mergeExtensions(ext)
	var fp *FeatureProfile
	if from != "" {
		ex, err := ExampleFeatureProfile(from)
		if err != nil {
			return nil, err
		}
		fp = ex
	} else {
		u := newFeatureUniverse(merged, Version())
		fp = &FeatureProfile{Features: offeredFeatureNames(u)}
	}
	fp.WrittenWith = descx.ProfileWrittenWith(Version())
	fp.Features = descx.SortFeatureNames(fp.Features)
	if _, err := CheckFeatureProfile(fp, FeatureProfileCheckOptions{Extensions: merged}); err != nil {
		return nil, err
	}
	return fp, nil
}

// CheckFeatureProfile validates fp exactly as pulse.New does — the
// extension registrations in opts first, then the profile's classes in
// order: structural (PULSE_FEATURE_PROFILE_INVALID), name resolution
// (PULSE_FEATURE_PROFILE_UNKNOWN), dependencies
// (PULSE_FEATURE_PROFILE_DEPENDENCY), stopping at the first failing
// class. It returns the report (never nil) and that coded error, nil
// when the profile is valid. Offline relaxes only the name class (see
// FeatureProfileCheckOptions.Offline).
func CheckFeatureProfile(fp *FeatureProfile, opts FeatureProfileCheckOptions) (*FeatureProfileCheck, error) {
	report := &FeatureProfileCheck{
		Version:    Version(),
		Unverified: []string{},
		Warnings:   []*descriptor.EnvelopeEntry{},
	}
	if fp == nil {
		return report, featureProfileInvalid(featureProfileReasonMissingFeatures,
			"feature profile: no profile given", map[string]any{})
	}
	report.Profile = fp.Profile
	report.WrittenWith = fp.WrittenWith
	report.Features = len(fp.Features)

	u, err := validateExtensionUniverse(opts.Extensions)
	if err != nil {
		return report, err
	}
	if opts.Offline && fp.Features != nil {
		u = u.withUnverified(fp.Features)
		for _, name := range sortedKeys(u.unverified) {
			report.Unverified = append(report.Unverified, name)
			report.Warnings = append(report.Warnings, &descriptor.EnvelopeEntry{
				Code:    string(errors.PULSE_FEATURE_PROFILE_UNKNOWN),
				Message: name + ": " + unverifiedExtensionMessage,
				Details: map[string]any{"name": name, "reason": featureUnknownUnverifiedExtension},
			})
		}
	}
	if err := validateFeatureProfile(fp, u, ""); err != nil {
		return report, err
	}
	report.Valid = true
	return report, nil
}

// DiffFeatureProfile compares fp with the running Pulse: the features
// the running build offers that fp does not list (flagged New when
// introduced after fp's written_with), and the names fp lists that the
// running build does not resolve. Unknown names are reported, never
// fatal. ext is the value handed to pulse.New through
// Options.Extensions. Only a nil profile or a nil Features list is an
// error (PULSE_FEATURE_PROFILE_INVALID).
func DiffFeatureProfile(fp *FeatureProfile, ext ...Extensions) (*FeatureProfileDiff, error) {
	if err := requireFeatureList(fp); err != nil {
		return nil, err
	}
	u := newFeatureUniverse(mergeExtensions(ext), Version())
	listed := make(map[string]bool, len(fp.Features))
	for _, name := range fp.Features {
		listed[name] = true
	}

	out := &FeatureProfileDiff{
		Profile:     fp.Profile,
		WrittenWith: fp.WrittenWith,
		Version:     u.version,
		Missing:     []FeatureProfileMissing{},
		Unknown:     []FeatureProfileUnknownName{},
	}
	var missing []string
	for _, name := range offeredFeatureNames(u) {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range descx.SortFeatureNames(missing) {
		m := FeatureProfileMissing{
			Name:     name,
			Kind:     string(descx.FeatureKindOfName(name)),
			Category: descx.FeatureCategoryOf(name),
		}
		if f, ok := lookupBuiltinFeature(name); ok {
			m.Since = f.Since
			// An absent or unparseable written_with has no release
			// core, which SinceReached treats as newest: nothing is new.
			m.New = !descx.SinceReached(f.Since, fp.WrittenWith)
		}
		out.Missing = append(out.Missing, m)
	}
	seen := map[string]bool{}
	for _, name := range fp.Features {
		if seen[name] || u.known(name) {
			continue
		}
		seen[name] = true
		out.Unknown = append(out.Unknown, unknownName(u.classify(name)))
	}
	sort.Slice(out.Unknown, func(i, j int) bool { return out.Unknown[i].Name < out.Unknown[j].Name })
	return out, nil
}

// DescribeFeatureProfile describes every feature fp lists: kind,
// operator category, source, Since and dependency edges, in canonical
// profile order. A name the running Pulse does not resolve is described
// with what is known and an Unknown explanation; it is not an error.
// ext is the value handed to pulse.New through Options.Extensions. Only
// a nil profile or a nil Features list is an error
// (PULSE_FEATURE_PROFILE_INVALID).
func DescribeFeatureProfile(fp *FeatureProfile, ext ...Extensions) (*FeatureProfileDescription, error) {
	if err := requireFeatureList(fp); err != nil {
		return nil, err
	}
	u := newFeatureUniverse(mergeExtensions(ext), Version())
	out := &FeatureProfileDescription{
		Profile:     fp.Profile,
		WrittenWith: fp.WrittenWith,
		Version:     u.version,
		Features:    []FeatureDescription{},
	}
	for _, name := range descx.SortFeatureNames(fp.Features) {
		d := FeatureDescription{
			Name:      name,
			Kind:      string(descx.FeatureKindOfName(name)),
			Category:  descx.FeatureCategoryOf(name),
			Source:    featureSourceUnknown,
			DependsOn: [][]string{},
		}
		if f, ok := lookupBuiltinFeature(name); ok {
			d.Source = featureSourceBuiltin
			d.Since = f.Since
			d.DependsOn = nonNilGroups(f.DependsOn)
		} else if _, ok := u.extDeps[name]; ok {
			d.Source = featureSourceExtension
			groups, _ := u.dependencies(name)
			d.DependsOn = nonNilGroups(groups)
		}
		if !u.known(name) {
			un := unknownName(u.classify(name))
			d.Unknown = &un
		}
		out.Features = append(out.Features, d)
	}
	return out, nil
}

// validateExtensionUniverse validates extension registrations exactly
// as pulse.New does — naming and shape, factory probes, DependsOn — and
// returns the feature universe they open. Shared by pulse.New and
// CheckFeatureProfile.
func validateExtensionUniverse(ext Extensions) (featureUniverse, error) {
	if err := validateExtensions(ext); err != nil {
		return featureUniverse{}, err
	}
	if err := probeExtensions(ext); err != nil {
		return featureUniverse{}, err
	}
	u := newFeatureUniverse(ext, Version())
	if err := validateExtensionDependsOn(u); err != nil {
		return featureUniverse{}, err
	}
	if err := validateExtensionGuidance(ext); err != nil {
		return featureUniverse{}, err
	}
	return u, nil
}

// withUnverified returns a copy of u in which every listed name that
// does not resolve but follows the extension naming policy resolves as
// an extension operator with no DependsOn. The accepted names are
// recorded under unverified.
func (u featureUniverse) withUnverified(names []string) featureUniverse {
	out := u
	out.extDeps = make(map[string][]string, len(u.extDeps))
	for k, v := range u.extDeps {
		out.extDeps[k] = v
	}
	out.unverified = map[string]bool{}
	for _, name := range names {
		if u.known(name) || !looksLikeExtensionName(name) {
			continue
		}
		if _, builtin := lookupBuiltinFeature(name); builtin {
			continue // a built-in past the running version stays an error
		}
		out.extDeps[name] = nil
		out.unverified[name] = true
	}
	return out
}

// looksLikeExtensionName reports whether name follows the extension
// operator naming policy with a non-reserved namespace. SYNTH names are
// not features, so they never qualify.
func looksLikeExtensionName(name string) bool {
	g := extensionNameRegex.FindStringSubmatch(name)
	if g == nil || g[1] == "SYNTH" {
		return false
	}
	_, reserved := reservedExtensionNamespaces[g[2]]
	return !reserved
}

// offeredFeatureNames lists every feature the universe resolves:
// reached built-ins in table order, then extension operators by name.
func offeredFeatureNames(u featureUniverse) []string {
	var out []string
	for _, name := range listBuiltinFeatureNames() {
		if u.known(name) {
			out = append(out, name)
		}
	}
	return append(out, sortedKeys(u.extDeps)...)
}

// mergeExtensions folds several Extensions values into one,
// concatenating each registration slice and merging the named tables
// (a later value wins on a repeated table name).
func mergeExtensions(ext []Extensions) Extensions {
	if len(ext) == 1 {
		return ext[0]
	}
	var out Extensions
	for _, e := range ext {
		out.Aggregators = append(out.Aggregators, e.Aggregators...)
		out.Attributes = append(out.Attributes, e.Attributes...)
		out.Filterers = append(out.Filterers, e.Filterers...)
		out.Groupers = append(out.Groupers, e.Groupers...)
		out.Windows = append(out.Windows, e.Windows...)
		out.Features = append(out.Features, e.Features...)
		out.Tests = append(out.Tests, e.Tests...)
		out.SynthDistributions = append(out.SynthDistributions, e.SynthDistributions...)
		out.ExprFunctions = append(out.ExprFunctions, e.ExprFunctions...)
		out.LookupTables = mergeTables(out.LookupTables, e.LookupTables)
		out.LabelTables = mergeTables(out.LabelTables, e.LabelTables)
		out.RangeTables = mergeTables(out.RangeTables, e.RangeTables)
	}
	return out
}

func mergeTables[V any](dst, src map[string]V) map[string]V {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]V, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// requireFeatureList refuses a nil profile or a nil Features list, the
// structural minimum a diff or description needs.
func requireFeatureList(fp *FeatureProfile) error {
	if fp == nil || fp.Features == nil {
		return featureProfileInvalid(featureProfileReasonMissingFeatures,
			"feature profile: Features is required (an empty, non-nil slice is allowed)",
			map[string]any{})
	}
	return nil
}

func unknownName(entry map[string]any) FeatureProfileUnknownName {
	out := FeatureProfileUnknownName{}
	out.Name, _ = entry["name"].(string)
	out.Reason, _ = entry["reason"].(string)
	out.DidYouMean, _ = entry["did_you_mean"].(string)
	out.Since, _ = entry["since"].(string)
	return out
}

func nonNilGroups(groups [][]string) [][]string {
	out := make([][]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, append([]string{}, g...))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
