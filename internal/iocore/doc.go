// Package iocore is the leaf that holds the tabular I/O contracts every
// format adapter implements: Reader, Writer and the optional interfaces
// the shared import / export / convert paths discover by type assertion,
// plus the handful of cell conventions (set delimiter, empty-set marker,
// null-cell test, stop-iteration sentinel, set-width ladder) the adapters
// and the jobs must agree on.
//
// It exists to break an import cycle. The public io package builds any
// adapter from a typed Format (its factory imports every adapter), so an
// adapter cannot import io for these interfaces. Adapters import iocore;
// io and internal/io alias it, so a value satisfying iocore.Writer IS an
// io.Writer and every optional-interface assertion behaves exactly as it
// did before the split.
//
// iocore imports nothing from the module above encoding, errors and types.
package iocore
