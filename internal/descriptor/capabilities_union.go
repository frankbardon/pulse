package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// unionSurfaces maps each entry point that reads a union to the feature
// offering it; an empty feature is a core surface (always present).
var unionSurfaces = []struct{ name, feature string }{
	{"compose", featCompose},
	{"facet_schema", featFacet},
	{"join_left", featJoins},
	{"lookup", featLookup},
	{"materialize", featMaterialize},
	{"predict", ""},
	{"process", featProcess},
	{"process_chain", featProcessChain},
	{"process_stream", featStream},
	{"sample", featSample},
}

// unionCapability returns the canonical UnionCapability entry, its
// surfaces narrowed to the features on reports enabled.
func unionCapability(on func(string) bool) descriptor.UnionCapability {
	var surfaces []string
	for _, s := range unionSurfaces {
		if s.feature == "" || on(s.feature) {
			surfaces = append(surfaces, s.name)
		}
	}
	slices.Sort(surfaces)
	return descriptor.UnionCapability{
		Name:               "cohort_union",
		DefaultSourceField: types.DefaultUnionSourceField,
		MaxMembers:         encx.MaxArchiveShards,
		Surfaces:           surfaces,
		Rules: []string{
			"union replaces cohort on the root that carries it; both set is PULSE_UNION_COHORT_CONFLICT.",
			"Every member needs a cohort and a non-empty label unique within the union (PULSE_UNION_MEMBER_INVALID / PULSE_UNION_LABEL_INVALID).",
			"Member schemas merge by the shard-archive rules on the logical view: the first member is canonical, set dictionaries widen, nullability and the default zone field never; a mismatch is PULSE_UNION_SCHEMA_MISMATCH naming the member.",
			"The virtual source field (union_source_field, default _source) is appended last: a non-nullable categorical whose dictionary is the labels in declared order; a member field of that name is PULSE_UNION_SOURCE_FIELD_INVALID.",
			"Records stream member by member in declared order; a shard-archive member flattens into its shards, and more than max_members flattened members is PULSE_UNION_MEMBER_LIMIT.",
			"A union is never a join's right side (PULSE_UNION_JOIN_RIGHT) and never nests (PULSE_UNION_NESTED); a process chain reads its root union as stage 0's source.",
			"A point lookup over a union always scans its members: no index is read or written.",
		},
	}
}
