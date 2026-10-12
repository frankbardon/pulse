package types

// ReferenceVectorTable is a named set of reference profiles an embedder
// registers through pulse.Options.Extensions.ReferenceVectors: target
// vectors a row is compared to — an ideal brand profile, a segment
// centroid, a benchmark battery. An expression reads one with
// ref("table", key); the comparison operators read one through their
// `reference` param.
//
// Labels name the elements, in the order every profile lists its values.
// A profile is matched to the vector it is compared with BY LABEL, never
// by position: the compared vector's labels must be exactly this set
// (any order), else PULSE_VECTOR_REFERENCE_MISMATCH.
//
// Validated at pulse.New (PULSE_EXTENSION_PARAM_INVALID): a non-empty
// table name, at least one label, labels non-empty and unique, at least
// one profile, every profile key non-empty, every profile exactly one
// value per label, every value finite. Read-only after pulse.New — the
// instance keeps its own copy.
type ReferenceVectorTable struct {
	// Description is an optional human-readable summary surfaced in the
	// manifest projection.
	Description string
	// Labels are the element labels, one per profile value.
	Labels []string
	// Profiles maps a profile key to its values, index-aligned with
	// Labels.
	Profiles map[string][]float64
}
