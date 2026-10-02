package pulse

import (
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// resolveFeatureSet computes the instance's resolved feature set once at
// pulse.New. The universe is every built-in feature whose Since the
// running build has reached plus every registered extension operator.
// Without a feature profile everything in the universe is enabled; with
// one, exactly the profile's (already validated) list is, and the rest
// of the universe is hidden. b is the EFFECTIVE behaviour — the
// profile's switches already ORed into Options.
func resolveFeatureSet(u featureUniverse, fp *FeatureProfile, b descx.FeatureBehaviour) descx.FeatureSet {
	universe := make([]string, 0, len(u.extDeps)+512)
	for _, name := range descx.FeatureNames() {
		if u.known(name) {
			universe = append(universe, name)
		}
	}
	for name := range u.extDeps {
		universe = append(universe, name)
	}
	if fp == nil {
		return descx.FeatureSet{Enabled: universe, Behaviour: b}
	}
	listed := make(map[string]struct{}, len(fp.Features))
	for _, name := range fp.Features {
		listed[name] = struct{}{}
	}
	var hidden []string
	for _, name := range universe {
		if _, ok := listed[name]; !ok {
			hidden = append(hidden, name)
		}
	}
	return descx.FeatureSet{
		Enabled:   append([]string{}, fp.Features...),
		Hidden:    hidden,
		Behaviour: b,
	}
}

// effectiveFeatureBehaviour reads the engine switches off opts AFTER
// applyFeatureProfileBehaviour has folded the profile in, plus the
// MCP-only cohort-scan switch the profile alone can set.
func effectiveFeatureBehaviour(opts Options, fp *FeatureProfile) descx.FeatureBehaviour {
	return descx.FeatureBehaviour{
		DisableDefaults:   opts.DisableDefaults,
		DisableComponents: opts.DisableComponents,
		// Mirrors the projection wiring in New: the deprecated
		// ProjectBufferedFields knob re-enables projection.
		DisableProjection: !(opts.ProjectBufferedFields || !opts.DisableProjection),
		DisableCohortScan: fp != nil && fp.Behaviour != nil && fp.Behaviour.DisableCohortScan,
	}
}

// withoutHiddenExtensions drops every extension operator registration
// the feature set hides, so a hidden extension is never registered at
// runtime nor projected into the snapshot. It runs AFTER
// validateExtensionDependsOn, which checks DependsOn against ALL
// registrations. Non-operator registrations (synth distributions, expr
// functions, named tables) are not features and pass through.
func withoutHiddenExtensions(ext Extensions, set descx.FeatureSet) Extensions {
	if len(set.Hidden) == 0 {
		return ext
	}
	hidden := make(map[string]struct{}, len(set.Hidden))
	for _, n := range set.Hidden {
		hidden[n] = struct{}{}
	}
	out := ext
	out.Aggregators = keepVisible(ext.Aggregators, hidden, func(r AggregatorRegistration) string { return string(r.Name) })
	out.Attributes = keepVisible(ext.Attributes, hidden, func(r AttributeRegistration) string { return string(r.Name) })
	out.Filterers = keepVisible(ext.Filterers, hidden, func(r FiltererRegistration) string { return string(r.Name) })
	out.Groupers = keepVisible(ext.Groupers, hidden, func(r GrouperRegistration) string { return string(r.Name) })
	out.Windows = keepVisible(ext.Windows, hidden, func(r WindowRegistration) string { return string(r.Name) })
	out.Features = keepVisible(ext.Features, hidden, func(r FeatureRegistration) string { return string(r.Name) })
	out.Tests = keepVisible(ext.Tests, hidden, func(r TestRegistration) string { return string(r.Name) })
	return out
}

func keepVisible[T any](in []T, hidden map[string]struct{}, name func(T) string) []T {
	if in == nil {
		return nil
	}
	out := make([]T, 0, len(in))
	for _, r := range in {
		if _, h := hidden[name(r)]; !h {
			out = append(out, r)
		}
	}
	return out
}

// FeatureSetDigest returns the instance's feature_set_digest:
// "fs1:" followed by the hex SHA-256 of its sorted enabled feature
// names and its effective behaviour switches (a feature profile's
// behaviour ORed with Options). Every instance has one, a profile-free
// instance included. Two instances offering the same features with the
// same switches share a digest — across processes too — so it can key
// caches of self-description output; any difference in either changes
// it.
func (p *Pulse) FeatureSetDigest() string {
	return p.svc.InstanceSnapshot().Digest()
}

// FeatureProfile returns a copy of the feature profile the instance was
// built with and true, or nil and false when it was built without one.
// Mutating the returned value does not affect the instance.
func (p *Pulse) FeatureProfile() (*FeatureProfile, bool) {
	if p.featureProfile == nil {
		return nil, false
	}
	return cloneFeatureProfile(p.featureProfile), true
}
