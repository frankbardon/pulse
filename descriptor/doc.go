// Package descriptor holds the result and envelope types of Pulse's
// no-execute self-description surface: the --json Envelope, the
// manifest (Manifest and its capability blocks), InspectResult and
// PredictResult, and the per-operator ComponentSchema contract. The
// builders that produce them (manifest, payload schema, predict,
// inspect) are internal; reach them through the root pulse facade.
package descriptor
