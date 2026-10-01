// Package synth produces synthetic .pulse cohorts from either a schema
// declaration ("from-schema") or a statistical profile of a real cohort
// ("from-profile"). The generator is deterministic given a seed and writes
// directly into the .pulse binary format using the encoding package, so
// outputs are byte-identical for the same (spec, seed) pair.
//
// Two top-level entry points are exported by this package:
//
//	Synth(spec Spec, opts Options) (*Result, error)
//	Profile(schema, records, opts ProfileOptions) (*Profile, error)
//
// Pulse embedders should use the higher-level pulse.Pulse.Synth and
// pulse.Pulse.Profile facade methods instead.
package synth
