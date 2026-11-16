package regression

import (
	"math"

	"github.com/frankbardon/pulse/internal/model"
)

// Record is the minimal contract the regression engines need from the
// parent processing.Record. Defining it locally keeps this subpackage
// free of the processing import and mirrors the pattern used by
// internal/processing/feature.
//
// processing.Record satisfies this interface; the orchestrator passes
// records through unchanged.
type Record interface {
	// NumericValue returns the value and true if present and non-null.
	// Returns (0, false) when the named field is null or absent on the
	// record.
	NumericValue(name string) (float64, bool)
}

// InputValue is THE numeric read of a regression input — target,
// predictor, covariance order_by / numeric cluster key, the refit and
// resample row reads and the scorer's columns: (v, true) only when the
// value is present, non-null AND finite. A NaN or ±Inf is missing data
// and drops its row listwise exactly like a null (the row never enters
// the fit or n_obs; a scored row reads a null score). Every row read in
// this package goes through it (TestNonFiniteInput_ReadsGoThroughInputValue)
// except the weight read, which classifies a non-finite weight itself
// (weighting.Classify: NaNInf, counted as an invalid weight).
func InputValue(rec Record, name string) (float64, bool) {
	v, ok := rec.NumericValue(name)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// carriedRecord is a scored row with its channels read as their carried
// adstock (Carrier.Row, carryover.go) — a Record pass-through, so it
// lives beside InputValue, the one raw numeric read; the categorical and code readers a geo-panel scorer and the
// level reads use pass through.
type carriedRecord struct {
	Record
	vals map[string]float64
	// lags are each carried channel's spend at lags 0..max_lag (NaN at
	// a lag the sum skipped), absent for a channel scored null.
	lags map[string][]float64
}

var _ model.AdstockLagRow = carriedRecord{}

// AdstockLags satisfies model.AdstockLagRow: channel's spend at the
// lags its carried adstock summed.
func (r carriedRecord) AdstockLags(channel string) ([]float64, bool) {
	l, ok := r.lags[channel]
	return l, ok
}

// NumericValue reads a channel's carried adstock (absent when null),
// any other column off the row.
func (r carriedRecord) NumericValue(name string) (float64, bool) {
	if v, ok := r.vals[name]; ok {
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	}
	return r.Record.NumericValue(name)
}

// StringValue is the row's categorical label (a geo-panel scorer's
// reader), read checked: a corrupt code reads as absent here, and the
// fit's own level reads (LevelFault) have refused it already.
func (r carriedRecord) StringValue(name string) (string, bool) {
	if lr, ok := r.Record.(LabeledRecord); ok {
		if s, ok, err := lr.StringValueChecked(name); err == nil {
			return s, ok
		}
	}
	return "", false
}

// Uint64Value passes the row's unsigned code reader through.
func (r carriedRecord) Uint64Value(name string) (uint64, bool) {
	if s, ok := r.Record.(interface {
		Uint64Value(string) (uint64, bool)
	}); ok {
		return s.Uint64Value(name)
	}
	return 0, false
}

// StringValueChecked passes the row's checked categorical reader
// through.
func (r carriedRecord) StringValueChecked(name string) (string, bool, error) {
	if lr, ok := r.Record.(LabeledRecord); ok {
		return lr.StringValueChecked(name)
	}
	return "", false, nil
}
