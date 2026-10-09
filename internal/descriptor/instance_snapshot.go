package descriptor

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
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

	// defaultReturn is the instance's `return` default — the layer a
	// request without its own block resolves through
	// (Options.DefaultReturn, else the feature profile's `return`).
	// Nil: the library default `full`. Not covered by the digest: it
	// shapes responses, never the feature set.
	defaultReturn *types.Return

	// limits is the instance's effective resource limits (resolved by
	// pulse.New: Options.Limits > feature profile > built-in default).
	// Nil: the built-in defaults. Not covered by the feature-set digest.
	limits *limits.Limits

	// suppressedAdvisories is Options.SuppressAdvisories (validated by
	// pulse.New): the PULSE_ADVISORY_* codes predict never reports on
	// this instance. Nil: none. Not covered by the feature-set digest.
	suppressedAdvisories map[string]struct{}

	// returnPlans memoizes Response-rooted `return` resolutions
	// (return_plan_cache.go). Allocated by buildOntology; shared by
	// WithDefaultReturn copies, whose visibility — the only instance
	// input to a resolution — is identical.
	returnPlans *returnPlanCache

	// ontology is the instance graph (base + extension nodes, pruned to
	// the feature set) and discovery the skill / example view walking
	// it — both built eagerly by the constructors (ontology_instance.go).
	ontology  *OntologyGraph
	discovery *Discovery
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
	s := &InstanceSnapshot{
		ext:       ext,
		scoped:    true,
		enabled:   enabled,
		hidden:    hidden,
		names:     names,
		behaviour: set.Behaviour,
		digest:    FeatureSetDigest(names, set.Behaviour),
	}
	s.buildOntology()
	return s
}

// buildOntology installs the instance graph and the discovery view
// walking it. Called once, by the constructors.
func (s *InstanceSnapshot) buildOntology() {
	s.returnPlans = newReturnPlanCache()
	s.ontology = instanceOntology(s.ext, s)
	s.discovery = fullDiscovery
	if s.Scoped() && len(s.hidden) > 0 || s.ext != nil && len(s.ext.Skills)+len(s.ext.Examples) > 0 {
		s.discovery = buildDiscovery(s, s.ontology)
	}
}

// Ontology returns the instance's pruned ontology graph (read-only; the
// base graph itself when the instance neither extends nor hides
// anything). A nil snapshot answers the base graph.
func (s *InstanceSnapshot) Ontology() *OntologyGraph {
	if s == nil || s.ontology == nil {
		return BaseOntology()
	}
	return s.ontology
}

// UnscopedInstanceSnapshot wraps an extension projection with no feature
// scoping: Enabled is true for every name, Hidden is always false and
// the digest is empty. For callers that install extensions on a service
// directly, outside pulse.New.
func UnscopedInstanceSnapshot(ext *ExtensionsSnapshot) *InstanceSnapshot {
	if ext == nil {
		return nil
	}
	s := &InstanceSnapshot{ext: ext}
	s.buildOntology()
	return s
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

// WithDefaultReturn returns a copy of the snapshot carrying r as the
// instance `return` default (nil clears it). r is cloned. pulse.New
// installs the validated default (Options.DefaultReturn, else the
// feature profile's `return`) so predict and the runtime resolve a
// request through the same layer. A nil receiver yields an unscoped
// snapshot carrying only the default.
func (s *InstanceSnapshot) WithDefaultReturn(r *types.Return) *InstanceSnapshot {
	var out InstanceSnapshot
	if s != nil {
		out = *s
	} else {
		out.buildOntology()
	}
	out.defaultReturn = cloneReturn(r)
	return &out
}

// DefaultReturn returns a copy of the instance `return` default, nil
// when the instance has none (the library default `full`). Nil-safe.
func (s *InstanceSnapshot) DefaultReturn() *types.Return {
	if s == nil {
		return nil
	}
	return cloneReturn(s.defaultReturn)
}

// WithLimits returns a copy of the snapshot carrying l as the
// instance's effective resource limits. pulse.New installs the resolved
// limits so predict, the runtime and the manifest read the same values.
// A nil receiver yields an unscoped snapshot carrying only the limits.
func (s *InstanceSnapshot) WithLimits(l limits.Limits) *InstanceSnapshot {
	var out InstanceSnapshot
	if s != nil {
		out = *s
	} else {
		out.buildOntology()
	}
	out.limits = &l
	return &out
}

// WithSuppressedAdvisories returns a copy of s whose predict omits
// every advisory whose code is in codes (Options.SuppressAdvisories,
// validated by ValidateSuppressAdvisories first). An empty list
// suppresses nothing. Nil-safe like WithLimits.
func (s *InstanceSnapshot) WithSuppressedAdvisories(codes []string) *InstanceSnapshot {
	var out InstanceSnapshot
	if s != nil {
		out = *s
	} else {
		out.buildOntology()
	}
	out.suppressedAdvisories = nil
	if len(codes) > 0 {
		out.suppressedAdvisories = make(map[string]struct{}, len(codes))
		for _, c := range codes {
			out.suppressedAdvisories[c] = struct{}{}
		}
	}
	return &out
}

// AdvisorySuppressed reports whether the instance suppresses the
// advisory code. Nil-safe: a nil snapshot suppresses nothing.
func (s *InstanceSnapshot) AdvisorySuppressed(code string) bool {
	if s == nil {
		return false
	}
	_, ok := s.suppressedAdvisories[code]
	return ok
}

// Limits returns the instance's effective resource limits — the
// built-in defaults when none were installed. Nil-safe; the result is a
// copy.
func (s *InstanceSnapshot) Limits() limits.Limits {
	if s == nil || s.limits == nil {
		return limits.Defaults()
	}
	return *s.limits
}

// cloneReturn deep-copies a Return block (nil stays nil).
func cloneReturn(r *types.Return) *types.Return {
	if r == nil {
		return nil
	}
	out := *r
	out.Include = append([]string(nil), r.Include...)
	out.Exclude = append([]string(nil), r.Exclude...)
	return &out
}

// Digest returns the instance's feature_set_digest ("fs1:<sha256hex>"),
// or "" on a nil / unscoped snapshot.
func (s *InstanceSnapshot) Digest() string {
	if s == nil {
		return ""
	}
	return s.digest
}
