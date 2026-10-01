package iocore

import "fmt"

// DefaultSetDelimiter is the delimiter assumed by convertValue when no
// per-column delimiter has been recorded (explicit-schema imports that
// skipped the inference pass). Always |.
const DefaultSetDelimiter = "|"

// EmptySetCell is the external form of a PRESENT set_* cell with no
// element selected — a bare DefaultSetDelimiter, carrying no token.
//
// A set column has three states, not two: some elements selected, NO
// element selected, and null. The empty string cannot spell the middle
// one, because isNullToken consumes it before any dictionary is
// consulted; a cell that means "answered, ticked nothing" would
// re-import as "never answered" and quietly shrink the denominator of
// every rate computed over the column.
//
// One marker serves every adapter — flat text (csv / tsv / excel) and
// JSON alike — so the convention is stated once and cannot drift
// between formats. It survives a third-party round trip because it is
// ordinary cell TEXT: unlike CSV's `,,` versus `,"",`, a spreadsheet or
// a generic CSV writer has nothing to normalise away. The structured
// adapters additionally carry the distinction natively (an Arrow or
// Parquet LIST cell is null via its validity bit and empty via a
// zero-length list; ndjson / jsonarray accept a JSON `[]`), and the
// readers map those spellings back onto this marker so the shared
// import path sees one form.
//
// It costs nothing at the other two states: a null cell is still "" and
// a NON-EMPTY cell is still its delimiter-joined tokens, byte for byte.
//
// The composition that makes it work is documented on SchemaAwareReader
// in io.go and must be kept: isNullToken does not recognise "|", and
// splitSetTokens drops empty tokens, so "|" yields zero tokens — mask
// 0, with no dictionary mutation.
const EmptySetCell = DefaultSetDelimiter

// errStopIteration is a sentinel used internally to stop row iteration early.
var errStopIteration = fmt.Errorf("stop iteration")

// ErrStopIteration returns the stop iteration sentinel for use by readers.
func ErrStopIteration() error {
	return errStopIteration
}
