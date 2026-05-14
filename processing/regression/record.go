package regression

// Record is the minimal contract the regression engines need from the
// parent processing.Record. Defining it locally keeps this subpackage
// free of the processing import and mirrors the pattern used by
// processing/feature.
//
// processing.Record satisfies this interface; the orchestrator passes
// records through unchanged.
type Record interface {
	// NumericValue returns the value and true if present and non-null.
	// Returns (0, false) when the named field is null or absent on the
	// record.
	NumericValue(name string) (float64, bool)
}
