// Package descriptor is the internal twin of the public descriptor
// package: the no-execute builders (manifest, payload JSON Schema,
// capability declarations), predict, inspect and the request validators.
// It imports the public package for the result and envelope types it
// returns; the public package never imports it back. Predict stays
// header- and schema-only and must not import the execution layer
// (TestPredictNoExecutionImports).
package descriptor
