package descriptor

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FeatureSetDigestPrefix versions the feature_set_digest algorithm. A
// change to what the digest covers or how it is serialised bumps it.
const FeatureSetDigestPrefix = "fs1:"

// FeatureBehaviour is the EFFECTIVE set of engine switches an instance
// runs with: a feature profile's behaviour ORed with pulse.Options. It
// feeds the feature_set_digest so two instances that differ only in a
// switch are told apart.
type FeatureBehaviour struct {
	DisableDefaults   bool
	DisableComponents bool
	DisableProjection bool
	DisableCohortScan bool
}

// lines renders the switches in a fixed order for the digest payload.
func (b FeatureBehaviour) lines() []string {
	return []string{
		"behaviour:disable_cohort_scan=" + strconv.FormatBool(b.DisableCohortScan),
		"behaviour:disable_components=" + strconv.FormatBool(b.DisableComponents),
		"behaviour:disable_defaults=" + strconv.FormatBool(b.DisableDefaults),
		"behaviour:disable_projection=" + strconv.FormatBool(b.DisableProjection),
	}
}

// FeatureSet is the resolved feature set of one instance, computed once
// at pulse.New. Enabled is every feature the instance offers (no feature
// profile: every built-in whose Since is reached plus every registered
// extension operator; with one: exactly the profile's list). Hidden is
// the rest of the resolvable universe — names that WOULD resolve on a
// default instance but this one does not offer.
type FeatureSet struct {
	Enabled   []string
	Hidden    []string
	Behaviour FeatureBehaviour
}

// FeatureSetDigest returns "fs1:" + sha256hex over the sorted enabled
// names, newline-joined, followed by the effective behaviour switches.
// The order of enabled is irrelevant; duplicates are collapsed.
func FeatureSetDigest(enabled []string, b FeatureBehaviour) string {
	names := sortedUnique(enabled)
	payload := strings.Join(append(names, b.lines()...), "\n")
	sum := sha256.Sum256([]byte(payload))
	return FeatureSetDigestPrefix + hex.EncodeToString(sum[:])
}

func sortedUnique(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, n := range in {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// InstanceSnapshot is the immutable, no-execute view of ONE instance's
// feature scoping: the extension projection it was built with plus its
// resolved feature set and feature_set_digest. pulse.New builds it once
// and installs it on the service; manifest, predict, payload schema and
// the runtime lookup sites read it to decide what this instance offers.
//
// A nil *InstanceSnapshot is UNSCOPED: every name is enabled, nothing is
// hidden, there are no extensions and the digest is empty. Engine code
// constructed outside pulse.New (tests, internal tools) therefore keeps
// its pre-feature-profile behaviour.
type InstanceSnapshot struct {
	ext       *ExtensionsSnapshot
	scoped    bool
	enabled   map[string]struct{}
	hidden    map[string]struct{}
	names     []string
	behaviour FeatureBehaviour
	digest    string

	// discovery is the instance's skill / example prune, built on first
	// use (see Discovery).
	discoveryOnce sync.Once
	discovery     *Discovery
}

// NewInstanceSnapshot builds the scoped snapshot pulse.New installs.
// ext may be nil (no extension registrations survive).
func NewInstanceSnapshot(ext *ExtensionsSnapshot, set FeatureSet) *InstanceSnapshot {
	names := sortedUnique(set.Enabled)
	enabled := make(map[string]struct{}, len(names))
	for _, n := range names {
		enabled[n] = struct{}{}
	}
	hidden := make(map[string]struct{}, len(set.Hidden))
	for _, n := range set.Hidden {
		if _, on := enabled[n]; !on {
			hidden[n] = struct{}{}
		}
	}
	return &InstanceSnapshot{
		ext:       ext,
		scoped:    true,
		enabled:   enabled,
		hidden:    hidden,
		names:     names,
		behaviour: set.Behaviour,
		digest:    FeatureSetDigest(names, set.Behaviour),
	}
}

// UnscopedInstanceSnapshot wraps an extension projection with no feature
// scoping: Enabled is true for every name, Hidden is always false and
// the digest is empty. For callers that install extensions on a service
// directly, outside pulse.New.
func UnscopedInstanceSnapshot(ext *ExtensionsSnapshot) *InstanceSnapshot {
	if ext == nil {
		return nil
	}
	return &InstanceSnapshot{ext: ext}
}

// Extensions returns the extension projection, or nil when the instance
// has no (visible) extension registrations. Nil-safe.
func (s *InstanceSnapshot) Extensions() *ExtensionsSnapshot {
	if s == nil {
		return nil
	}
	return s.ext
}

// Scoped reports whether the snapshot carries a resolved feature set
// (built by pulse.New). An unscoped or nil snapshot enables everything.
func (s *InstanceSnapshot) Scoped() bool {
	return s != nil && s.scoped
}

// Enabled reports whether the instance offers the feature spelled name
// (operators bare, other kinds `<kind>:<name>`). O(1). On a scoped
// snapshot a name outside the resolved set — hidden, unregistered, or a
// core surface (core surfaces are not features) — is false. A nil or
// unscoped snapshot answers true for every name.
func (s *InstanceSnapshot) Enabled(name string) bool {
	if !s.Scoped() {
		return true
	}
	_, ok := s.enabled[name]
	return ok
}

// Hidden reports whether name is a feature this build resolves (a
// built-in whose Since is reached, or a registered extension operator)
// that the instance's feature profile does not offer. O(1). A hidden
// name must behave exactly like a never-registered one; this is the
// predicate native lookup sites consult. A name that is merely unknown
// is NOT hidden — it already fails on its own. Always false without a
// feature profile.
func (s *InstanceSnapshot) Hidden(name string) bool {
	if !s.Scoped() {
		return false
	}
	_, ok := s.hidden[name]
	return ok
}

// EnabledNames returns the resolved enabled set, sorted, as a fresh
// copy. Nil on an unscoped snapshot.
func (s *InstanceSnapshot) EnabledNames() []string {
	if !s.Scoped() {
		return nil
	}
	return append([]string(nil), s.names...)
}

// HiddenNames returns the hidden set, sorted, as a fresh copy.
func (s *InstanceSnapshot) HiddenNames() []string {
	if !s.Scoped() {
		return nil
	}
	out := make([]string, 0, len(s.hidden))
	for n := range s.hidden {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Behaviour returns the effective behaviour switches the digest covers.
func (s *InstanceSnapshot) Behaviour() FeatureBehaviour {
	if s == nil {
		return FeatureBehaviour{}
	}
	return s.behaviour
}

// Digest returns the instance's feature_set_digest ("fs1:<sha256hex>"),
// or "" on a nil / unscoped snapshot.
func (s *InstanceSnapshot) Digest() string {
	if s == nil {
		return ""
	}
	return s.digest
}
