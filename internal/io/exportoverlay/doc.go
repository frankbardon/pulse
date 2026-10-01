// Package exportoverlay holds the higher-level integration tests for
// the ExportJob and ConvertJob overlay-embedding wiring.
//
// The per-format adapter packages (io/arrow, io/parquet, io/excel,
// io/ndjson, io/csv) carry unit tests for their Writer.SetOverlays /
// Reader.ReadOverlays surface directly. This package sits one level
// up: it constructs full ExportJob / ConvertJob values with a
// Response-side Overlays slate, runs them through ExportJob.Run /
// ConvertJob.Run, and verifies the on-disk artefact round-trips via the
// matching per-format Reader.
//
// The package is test-only (every .go file ends in _test.go); it lives
// in its own directory rather than the root io/ package because the
// root cannot import the format subpackages (import cycle — every
// format subpackage already imports io/ for the Writer interface
// definitions).
package exportoverlay
