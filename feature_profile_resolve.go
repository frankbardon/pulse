package pulse

import (
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Reasons carried on each "unknown" entry of a
// PULSE_FEATURE_PROFILE_UNKNOWN error.
const (
	featureUnknownUnregistered = "unregistered"
	featureUnknownPattern      = "pattern"
	featureUnknownWrongKind    = "wrong_kind"
	featureUnknownCoreSurface  = "core_surface"
	featureUnknownNewer        = "newer_than_running"
)

// lookupBuiltinFeature resolves a built-in feature row. A package
// variable only so tests can inject a row whose Since is newer than the
// running build; production always reads the internal feature table.
var lookupBuiltinFeature = descx.LookupFeature

// featureUniverse is the set of feature names one instance can resolve:
// the built-in table (filtered by the running version) plus the
// registered extension operators. Extensions carry no Since.
type featureUniverse struct {
	version   string
	extDeps   map[string][]string // extension operator name -> DependsOn
	extOrigin map[string]string   // extension operator name -> category
	// unverified marks names an offline CheckFeatureProfile accepted as
	// extension operators without a registration (withUnverified).
	unverified map[string]bool
	// skills are the validated Extensions.Skills files
	// (validateExtensionUniverse); pulse.New hands them to the snapshot.
	skills []descx.ExtensionSkill
}

func newFeatureUniverse(ext Extensions, version string) featureUniverse {
	u := featureUniverse{
		version:   version,
		extDeps:   map[string][]string{},
		extOrigin: map[string]string{},
	}
	add := func(cat extensionCategory, name string, deps []string) {
		u.extDeps[name] = deps
		u.extOrigin[name] = string(cat)
	}
	for _, r := range ext.Aggregators {
		add(categoryAggregator, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Attributes {
		add(categoryAttribute, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Filterers {
		add(categoryFilterer, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Groupers {
		add(categoryGrouper, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Windows {
		add(categoryWindow, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Features {
		add(categoryFeature, string(r.Name), r.DependsOn)
	}
	for _, r := range ext.Tests {
		add(categoryTest, string(r.Name), r.DependsOn)
	}
	// Synth distributions are not features (capability:synth gates
	// them), so an extension SYNTH_* name never resolves.
	return u
}

// known reports whether name resolves to a feature this instance offers.
func (u featureUniverse) known(name string) bool {
	_, ok := u.dependencies(name)
	return ok
}

// dependencies returns the dependency groups (AND of any-of sets) of a
// resolvable feature. ok is false when name does not resolve.
func (u featureUniverse) dependencies(name string) ([][]string, bool) {
	if f, ok := lookupBuiltinFeature(name); ok {
		if !sinceReached(f.Since, u.version) {
			return nil, false
		}
		return f.DependsOn, true
	}
	deps, ok := u.extDeps[name]
	if !ok {
		return nil, false
	}
	// An extension operator needs a request host exactly like a
	// built-in non-overlay operator (extensions cannot register
	// overlays); its DependsOn entries add single-name AND groups.
	groups := [][]string{descx.RequestHostCapabilities()}
	for _, d := range deps {
		groups = append(groups, []string{d})
	}
	return groups, true
}

// classify explains why name does not resolve. It returns one "unknown"
// detail entry.
func (u featureUniverse) classify(name string) map[string]any {
	entry := map[string]any{"name": name}
	if f, ok := lookupBuiltinFeature(name); ok {
		// The only way a built-in row fails to resolve.
		entry["reason"] = featureUnknownNewer
		entry["since"] = f.Since
		return entry
	}
	if strings.ContainsAny(name, "*?[") {
		entry["reason"] = featureUnknownPattern
		return entry
	}
	bare := name
	if i := strings.Index(name, ":"); i >= 0 {
		bare = name[i+1:]
	}
	if descx.IsCoreSurface(bare) {
		entry["reason"] = featureUnknownCoreSurface
		return entry
	}
	for _, k := range descx.AllFeatureKinds() {
		if cand := descx.FeatureName(k, bare); cand != name && u.known(cand) {
			entry["reason"] = featureUnknownWrongKind
			entry["did_you_mean"] = cand
			return entry
		}
	}
	entry["reason"] = featureUnknownUnregistered
	return entry
}

// validateFeatureProfileNames runs the UNKNOWN class: every listed
// name must resolve. Every failing name is reported, sorted.
func validateFeatureProfileNames(fp *FeatureProfile, u featureUniverse, path string) error {
	var unknown []map[string]any
	for _, name := range fp.Features {
		if !u.known(name) {
			unknown = append(unknown, u.classify(name))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sortEntriesByName(unknown)
	details := map[string]any{}
	if path != "" {
		details["path"] = path
	}
	return featureUnknownError("feature profile", unknown, u.version, details)
}

// validateFeatureProfileDependencies runs the DEPENDENCY class: for
// every enabled feature, every dependency group must have at least one
// enabled member. Every unmet group is reported.
func validateFeatureProfileDependencies(fp *FeatureProfile, u featureUniverse, path string) error {
	enabled := make(map[string]bool, len(fp.Features))
	for _, name := range fp.Features {
		enabled[name] = true
	}
	names := append([]string(nil), fp.Features...)
	sort.Strings(names)

	var unmet []map[string]any
	var features []string
	var lines []string
	for _, name := range names {
		groups, _ := u.dependencies(name)
		failed := false
		for _, g := range groups {
			met := false
			for _, member := range g {
				if enabled[member] {
					met = true
					break
				}
			}
			if met {
				continue
			}
			failed = true
			unmet = append(unmet, map[string]any{
				"feature":         name,
				"requires_any_of": append([]string(nil), g...),
			})
			lines = append(lines, fmt.Sprintf("%s requires one of [%s]", name, strings.Join(g, ", ")))
		}
		if failed {
			features = append(features, name)
		}
	}
	if len(unmet) == 0 {
		return nil
	}
	details := map[string]any{
		"unmet":    unmet,
		"features": features,
	}
	if path != "" {
		details["path"] = path
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_DEPENDENCY,
		"feature profile: unmet dependencies: "+strings.Join(lines, "; "),
		details)
}

// validateExtensionDependsOn checks every extension registration's
// DependsOn entries name a feature this build knows. It runs whether or
// not a profile is set: a typo must fail at pulse.New, not when a
// profile first enables the extension. The refusal reuses
// PULSE_FEATURE_PROFILE_UNKNOWN — the fault is an unknown feature name,
// and the per-entry classification is identical to a profile's; each
// entry additionally names the "extension" and its "category".
func validateExtensionDependsOn(u featureUniverse) error {
	var unknown []map[string]any
	exts := make([]string, 0, len(u.extDeps))
	for name := range u.extDeps {
		exts = append(exts, name)
	}
	sort.Strings(exts)
	for _, ext := range exts {
		for _, dep := range u.extDeps[ext] {
			if u.known(dep) {
				continue
			}
			entry := u.classify(dep)
			entry["extension"] = ext
			entry["category"] = u.extOrigin[ext]
			unknown = append(unknown, entry)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return featureUnknownError("extension DependsOn", unknown, u.version, map[string]any{})
}

func featureUnknownError(source string, unknown []map[string]any, version string, details map[string]any) error {
	names := make([]string, len(unknown))
	parts := make([]string, len(unknown))
	for i, e := range unknown {
		names[i] = e["name"].(string)
		parts[i] = fmt.Sprintf("%q (%s)", names[i], e["reason"])
	}
	details["unknown"] = unknown
	details["names"] = names
	details["version"] = version
	return errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_UNKNOWN,
		fmt.Sprintf("%s: unknown feature names: %s", source, strings.Join(parts, ", ")),
		details)
}

func sortEntriesByName(entries []map[string]any) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i]["name"].(string) < entries[j]["name"].(string)
	})
}

// sinceReached reports whether a feature introduced in since is
// available in the running build version (descx.SinceReached).
func sinceReached(since, running string) bool {
	return descx.SinceReached(since, running)
}
