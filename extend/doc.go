// Package extend is the public contract for authoring Pulse extension
// operators. An embedder implements the interfaces here, wraps them in
// the matching registration struct on the root package (for example
// pulse.AggregatorRegistration) and passes them through
// pulse.Options.Extensions. Pulse adapts every extend operator onto its
// internal engine at pulse.New time; predict, manifest, MCP and the
// runtime then treat it exactly like a built-in.
//
// The package is a leaf contract: it imports only the public encoding,
// types and errors packages plus the standard library, never the
// engine. Nothing here executes a request.
//
// # Reuse contract
//
// The engine decodes rows into reusable buffers. A Record handed to an
// operator method, and a Rows view handed to a buffered call, are valid
// ONLY for the duration of that call:
//
//   - never retain a Record or a Rows past the call that received it —
//     copy out the values you need instead (encoding.SetMask and
//     encoding.Decimal128 are plain values and safe to keep);
//   - never mutate anything reachable from a Record, including the
//     *encoding.Schema returned by Record.Schema;
//   - Record and Rows are consumer-only interfaces. Pulse implements
//     them; embedders call them. Methods may be added to either in a
//     minor release, so an embedder-side implementation (outside tests)
//     is not covered by the compatibility promise.
//
// # Reading values
//
// Record accessors never return an error; every "no value here" answer
// is a false second return. The silent cases worth knowing:
//
//   - NumericValue is false for a null field, a missing or projected-out
//     field, AND every set-typed field (set_u8 .. set_u256). Read sets
//     through SetMaskValue.
//   - A categorical field's NumericValue is its dictionary INDEX, not a
//     label; StringValue resolves the label.
//   - date is epoch DAYS; datetime is epoch SECONDS. Both arrive through
//     NumericValue and are never interchangeable.
//   - u64 values travel as a float64 echo, so values above 2^53 lose
//     precision through NumericValue.
//   - decimal128 values need DecimalValue for exact precision;
//     NumericValue returns only a rounded float echo.
//
// # Optional siblings
//
// A factory returns the category's base interface (Aggregator, …). The
// engine type-asserts the returned value for optional siblings
// (OnlineAggregator, RichAggregator, …) and takes the faster or richer
// path when they are present. A registration that declares a streaming
// tier (Streamable: true, or an attribute Mode of row_local / two_pass)
// MUST return a value implementing the streaming sibling; pulse.New
// probes aggregator, grouper and attribute factories and rejects a
// mismatch with PULSE_EXTENSION_STREAMABLE_MISMATCH.
//
// Engine-only capabilities are deliberately absent: component emission
// rides a registration's ComponentsFunc (or the operator's own
// Components() method), never a Meta* interface, and the engine's
// merge, fused-crosstab and registry-injection hooks are not part of
// the contract.
package extend
