// Package encoding is the module-internal twin of the public
// github.com/frankbardon/pulse/encoding package. The public package keeps
// the schema nouns and the ungrouped raw-byte primitives; everything else
// in the .pulse codec lives here: decode plans and record readers, parent
// group encode/decode, the read/write preamble, shard archives and
// cohesion, the sidecar point-lookup index and its manifest, set-field
// widening and the set ladder.
//
// This package imports the public one, never the reverse. The few
// unexported public-side helpers it shares are reached through
// internal/encodingbridge. Callers that import both name this one encx.
package encoding
