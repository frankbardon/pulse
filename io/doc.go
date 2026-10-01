// Package io is Pulse's public tabular I/O surface: the Reader / Writer
// contracts and their optional interfaces, schema inference, and the job
// types (ImportJob, ExportJob, ConvertJob, the transfer and dedup jobs).
//
// The implementation lives in internal/io and the adapter contracts in
// internal/iocore; this package re-exports them under their established
// names, so every type here is an alias and every function a forward.
package io
