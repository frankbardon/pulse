// Package synth produces synthetic .pulse cohorts from either a schema
// declaration ("from-schema") or a statistical profile of a real cohort
// ("from-profile"). The generator is deterministic given a seed, so
// outputs are byte-identical for the same (spec, seed) pair.
//
// This package is the public vocabulary: the Spec and Profile document
// types, Options / Result, the FidelityReport family, and the entry
// points Synth, SynthBytes, ProfileFile, ProfileBytes, SpecFromProfile,
// ParseSpec and WriteSpec. The implementation lives in internal/synth;
// every type here is an alias and every function a forward.
//
// Pulse embedders holding a *pulse.Pulse should prefer the facade
// methods (pulse.Pulse.Synth, pulse.Pulse.Profile).
package synth
