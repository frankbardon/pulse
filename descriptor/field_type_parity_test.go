package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Field-type declaration parity.
//
// `accepts_types` is the only machine-readable statement Pulse makes
// about which columns an operator will take, and `cohort_types` is what
// docs/src/format/field-types.md tells a reader to consult "for the live
// list at runtime". Both are assembled from the hand-written lists in
// descriptor/capabilities_*.go, so neither is derived from
// encoding's FieldType registry — which is how the manifest came to
// advertise five `nullable_*` names no registry entry has ever carried
// while omitting `u4`, which one has.
//
// Every gate below derives its expectation from the registry
// (FieldType.IsKnown / String / ParseFieldType). A gate that restated
// the type names would be the next thing to go stale, which is the whole
// defect these tests exist to close.

// capabilityOperatorTables returns every built-in Operator table keyed
// by its category label. Walked rather than enumerated per-test so a
// newly added category is covered by every gate at once.
func capabilityOperatorTables() map[string][]Operator {
	return map[string][]Operator{
		"aggregator": aggregatorCapabilities(),
		"attribute":  attributeCapabilities(),
		"filterer":   filtererCapabilities(),
		"grouper":    grouperCapabilities(),
		"window":     windowCapabilities(),
		"feature":    featureCapabilities(),
	}
}

// decimalTypeName returns the registered name of the decimal field type,
// read off the registry rather than spelled out.
func decimalTypeName(t *testing.T) string {
	t.Helper()
	for _, name := range registeredFieldTypeNames(t) {
		ft, ok := encoding.ParseFieldType(name)
		if ok && ft.IsDecimal() {
			return name
		}
	}
	t.Fatal("registry carries no decimal field type")
	return ""
}

// TestCapabilities_AcceptsTypesAreRegisteredNames is the parity gate: a
// name in any operator's AcceptsTypes must round-trip through
// encoding.ParseFieldType. An unparseable name is not a narrower claim,
// it is a claim about nothing — it reaches the manifest, appears under
// `accepts_types`, matches no cohort_types entry, and tells a caller
// bootstrapping from pulse_manifest that a column shape exists which the
// codec will reject on sight.
//
// Regressions carry their own AcceptsTypes list in a separate file and
// are checked in the same pass for the same reason.
func TestCapabilities_AcceptsTypesAreRegisteredNames(t *testing.T) {
	for kind, ops := range capabilityOperatorTables() {
		for _, op := range ops {
			if len(op.AcceptsTypes) == 0 {
				t.Errorf("%s %s declares no accepted field types", kind, op.Name)
			}
			for _, name := range op.AcceptsTypes {
				if _, ok := encoding.ParseFieldType(name); !ok {
					t.Errorf("%s %s declares %q, which encoding.ParseFieldType rejects — no such field type exists",
						kind, op.Name, name)
				}
			}
		}
	}
	for _, reg := range regressionCapabilities() {
		for _, name := range reg.AcceptsTypes {
			if _, ok := encoding.ParseFieldType(name); !ok {
				t.Errorf("regression %s declares %q, which encoding.ParseFieldType rejects — no such field type exists",
					reg.Name, name)
			}
		}
	}
}

// TestCapabilities_NoNullableTypeNames pins the orthogonality invariant
// from CLAUDE.md's byte-layout section as a declaration rule:
// nullability is a per-field FLAG (encoding.Field.Nullable) plus the
// per-record null bitmap, never a type variant. A `nullable_<x>`
// spelling in a capability list is therefore always a stale alias for
// `<x>` — it was never a separate type and no operator ever meant
// anything by it except "x, which may be null", which every type is.
//
// Subsumed by the ParseFieldType gate above today, but stated
// separately so the failure message names the real mistake rather than
// reporting a generic unknown name.
func TestCapabilities_NoNullableTypeNames(t *testing.T) {
	check := func(kind, op string, names []string) {
		for _, name := range names {
			if len(name) >= 9 && name[:9] == "nullable_" {
				t.Errorf("%s %s declares %q; nullability is Field.Nullable, not a type — the operator means %q",
					kind, op, name, name[9:])
			}
		}
	}
	for kind, ops := range capabilityOperatorTables() {
		for _, op := range ops {
			check(kind, op.Name, op.AcceptsTypes)
		}
	}
	for _, reg := range regressionCapabilities() {
		check("regression", reg.Name, reg.AcceptsTypes)
	}
}

// TestCapabilities_DecimalDeclaredOnlyWhereSupported guards the one
// substitution in this area that is not mechanical. The decimal claim
// used to ride the phantom `nullable_decimal128` on the analytics
// aggregators, so replacing it with `decimal128` hands seven operators a
// decimal128 declaration — and three of them (AGG_RANGE, AGG_MEDIAN,
// AGG_PERCENTILE) are outside decimalSupportedAggregations, which
// predict warns about with PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL. A
// manifest that offers what predict refuses is worse than the drift it
// replaced.
//
// Scoped to operators making a RESTRICTED claim: AGG_COUNT and friends
// take the universal list precisely because they ask only whether a
// field answered, and "every registered type" necessarily includes
// decimal128. Universality is computed from nonSetFieldTypes rather
// than matched by operator name.
func TestCapabilities_DecimalDeclaredOnlyWhereSupported(t *testing.T) {
	decimal := decimalTypeName(t)
	for _, op := range aggregatorCapabilities() {
		if !slices.Contains(op.AcceptsTypes, decimal) {
			continue
		}
		universal := true
		for _, n := range nonSetFieldTypes {
			if !slices.Contains(op.AcceptsTypes, n) {
				universal = false
				break
			}
		}
		if universal {
			continue
		}
		if !decimalSupportedAggregations[types.AggregationType(op.Name)] {
			t.Errorf("aggregator %s declares %q but is not in decimalSupportedAggregations — predict warns PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL on exactly this pairing",
				op.Name, decimal)
		}
	}
}

// TestManifest_AcceptsTypesResolveInCohortTypes is the end-to-end form
// of the parity claim, asserted on a built manifest rather than on the
// package-private lists: every `accepts_types` entry an LLM reads must
// resolve to a `cohort_types` entry in the same payload.
//
// docs/src/format/field-types.md sends readers to `cohort_types` for
// the live type list. That instruction is only true if the two blocks
// agree, and until this gate they did not.
func TestManifest_AcceptsTypesResolveInCohortTypes(t *testing.T) {
	m := BuildManifest()

	known := map[string]bool{}
	for _, ct := range m.CohortTypes {
		known[ct.Name] = true
	}
	// cohort_types itself must be the registry, no more and no less.
	registered := registeredFieldTypeNames(t)
	for _, name := range registered {
		if !known[name] {
			t.Errorf("manifest cohort_types omits registered field type %q", name)
		}
	}
	if len(m.CohortTypes) != len(registered) {
		t.Errorf("manifest cohort_types has %d entries, registry has %d", len(m.CohortTypes), len(registered))
	}

	groups := map[string][]Operator{
		"aggregators": m.Components.Aggregators,
		"attributes":  m.Components.Attributes,
		"filterers":   m.Components.Filterers,
		"groupers":    m.Components.Groupers,
		"windows":     m.Components.Windows,
		"features":    m.Components.Features,
	}
	for slot, ops := range groups {
		for _, op := range ops {
			for _, name := range op.AcceptsTypes {
				if !known[name] {
					t.Errorf("manifest components.%s[%s].accepts_types names %q, which is absent from cohort_types",
						slot, op.Name, name)
				}
			}
		}
	}
	for _, reg := range m.Regressions {
		for _, name := range reg.AcceptsTypes {
			if !known[name] {
				t.Errorf("manifest regressions[%s].accepts_types names %q, which is absent from cohort_types",
					reg.Name, name)
			}
		}
	}
}

// TestManifest_EveryRegisteredTypeHasSomeOperator is the other half of
// the same lie. `u4` was registered, decoded and stored all along, but
// no capability list carried the name, so every Compatible* slice on its
// cohort_types entry came back empty — a reader consulting the manifest
// as instructed would conclude Pulse can store a u4 column and do
// nothing whatsoever with it.
//
// A registered type with no operator at all is either a declaration bug
// or a type that should not be registered; either way it must not pass
// silently.
func TestManifest_EveryRegisteredTypeHasSomeOperator(t *testing.T) {
	for _, ct := range BuildManifest().CohortTypes {
		n := len(ct.CompatibleAggregators) + len(ct.CompatibleAttributes) +
			len(ct.CompatibleFilterers) + len(ct.CompatibleGroupers) +
			len(ct.CompatibleWindows) + len(ct.CompatibleFeatures)
		if n == 0 {
			t.Errorf("cohort type %q has no compatible operator in any category — the manifest says it can be stored and never used", ct.Name)
		}
	}
}
