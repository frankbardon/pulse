package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// RowTest is a tier-1 statistical test folded row by row during the
// aggregation pass: UpdateRow once per row that passed the filters,
// then Finalize once. Finalize MUST be safe to call with no prior
// UpdateRow (the empty-input case).
type RowTest interface {
	UpdateRow(rec Record) error
	Finalize() (*types.TestResult, error)
}

// PostTest is a tier-2 statistical test run once over the materialized
// result rows (after the window stage). Each row is the column-name to
// value map the response would carry. Tier-2 tests are always
// buffered.
type PostTest interface {
	Run(rows []map[string]any) (*types.TestResult, error)
}

// RowTestFactory builds a fresh RowTest for one test slot of one
// request.
type RowTestFactory func(spec *types.Test, schema *encoding.Schema) (RowTest, error)

// PostTestFactory builds a fresh PostTest for one test slot of one
// request.
type PostTestFactory func(spec *types.Test, schema *encoding.Schema) (PostTest, error)
