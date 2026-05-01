// Package feature implements the FEAT_* operators that run pre-filter to
// add derived columns to a record stream. Operators come in three flavors:
// per-row pure (LOG, SQRT), single-output transforms (BUCKETIZE), and
// global-pass operators that need a stats sweep before per-row write
// (FREQUENCY_ENCODE, TARGET_ENCODE). Multi-output operators (ONE_HOT,
// DATE_FEATURES) emit several columns from one feature spec.
//
// The package owns its own registry; per-operator init() functions register
// their factory. The parent processing package consumes Apply() at the
// pre-filter pipeline stage. To avoid an import cycle, Apply takes a
// minimal Record interface that processing.Record satisfies.
package feature

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Record is the minimal contract feature operators need from the parent
// processing.Record. Defining it locally lets this subpackage stay free of
// the processing import.
type Record interface {
	// NumericValue returns the value and true if present and non-null.
	NumericValue(name string) (float64, bool)

	// StringValue returns the resolved string value for categorical fields.
	StringValue(name string) (string, bool)

	// Set writes a derived non-null value. Implementations clear any prior
	// null marker and invalidate any cached views.
	Set(name string, value float64)

	// SetNull marks the named field null on this record.
	SetNull(name string)
}

// Output is one column's worth of derived values plus an aligned null mask.
// Values has one entry per input record; Nulls (when non-nil) has the same
// length and Nulls[i]==true means Values[i] is undefined for record i (the
// orchestrator writes a null marker instead of the numeric value).
type Output struct {
	Values []float64
	Nulls  []bool
}

// Computer produces one or more derived columns from a record set. The
// returned map is keyed by output column name; each Output's Values slice
// has length equal to len(records) (Apply enforces this).
type Computer interface {
	Compute(records []Record, field string) (map[string]Output, error)
}

// Factory constructs a Computer from a feature specification. The schema is
// the cohort schema before any feature output is added; Computers consult it
// to validate field types and resolve dictionaries.
type Factory func(feat *types.Feature, schema *encoding.Schema) (Computer, error)

// Options reserves room for the orchestrator to pass cross-feature context
// (e.g., prior split mask) once leakage detection ships.
type Options struct{}
