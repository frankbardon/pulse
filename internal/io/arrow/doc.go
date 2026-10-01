// Package arrow provides Arrow IPC (Feather V2) import and export for the
// pulse I/O pipeline, plus shared Arrow<->Pulse type-mapping helpers used by
// both this package and io/parquet.
//
// Format scope:
//
//   - This package implements the Arrow IPC FILE format (random-access,
//     footer-indexed). The standard file extensions are .arrow and .feather;
//     Feather V2 is identical to the Arrow IPC file format on disk.
//   - The streaming variant (Arrow IPC stream, .arrows) is intentionally NOT
//     supported. The file format is sufficient for batch import/export and
//     trivially seekable, which lets the reader iterate batches without
//     speculative buffering.
//
// Memory model:
//
// The reader materializes the full table into memory at init time, mirroring
// the policy in io/parquet. Files larger than available memory are out of
// scope for v1; users that need bounded streaming should use NDJSON or CSV.
//
// Type policy:
//
// All values written by Writer are encoded as Arrow String. Type-promotion on
// write is intentionally deferred until a real consumer demands it; this
// matches the parquet writer policy and keeps the export path simple.
package arrow
