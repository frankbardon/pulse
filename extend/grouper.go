package extend

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ErrGrouperKeyNull is the null-key sentinel for grouper key methods. A
// StreamingGrouper.KeyForRow or MultiKeyStreamingGrouper.KeysForRow
// that returns an error matching it (errors.Is) is treated exactly like
// ok=false: the row lands in no bucket and the run continues. It exists
// because an empty-string key is a legitimate bucket name, so "no key"
// cannot be signalled in-band; returning ok=false is equivalent.
var ErrGrouperKeyNull = stderrors.New("extend: grouper key null")

// Grouper partitions the rows of one buffered call into named buckets.
// It is the base interface every extension grouper implements.
//
// Group returns, per bucket key, the INDICES into rows (0 <= i <
// rows.Len()) of the rows that land in that bucket. A row may appear in
// more than one bucket only when the registration declares FansOut. An
// out-of-range index is refused with a PROCESSING_INTERNAL coded error;
// it never panics.
type Grouper interface {
	Group(rows Rows, field string) (map[string][]int, error)
}

// StreamingGrouper is the optional streaming sibling of Grouper: it
// derives one bucket key from a single row. When a registration
// declares Streamable: true the factory MUST return a value
// implementing it (or MultiKeyStreamingGrouper for a fan-out grouper);
// the engine then keys rows one at a time instead of buffering them.
// ok=false (or an ErrGrouperKeyNull error) skips the row.
type StreamingGrouper interface {
	KeyForRow(rec Record, field string) (key string, ok bool, err error)
}

// MultiKeyStreamingGrouper is the optional fan-out sibling: one row
// lands in every returned bucket. A registration whose factory returns
// it MUST declare FansOut: true, and one declaring FansOut: true MUST
// return it; pulse.New refuses either mismatch with
// PULSE_EXTENSION_FANOUT_MISMATCH. ok=false, an empty key slice, or an
// ErrGrouperKeyNull error skips the row.
type MultiKeyStreamingGrouper interface {
	KeysForRow(rec Record, field string) (keys []string, ok bool, err error)
}

// GrouperFactory builds a fresh Grouper for one group slot of one
// request. During probe-validation at pulse.New it is called once with
// a spec carrying only the operator name and an empty schema, and must
// tolerate both.
type GrouperFactory func(spec *types.Group, schema *encoding.Schema) (Grouper, error)
