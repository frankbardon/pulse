package types

// SlotError is a per-slot failure: the data-dependent runtime refusal
// that stopped ONE regression, test or matrix slot (a separated or
// unconverged GLM, a rank-deficient design, a zero-variance or
// too-small sample, a singular matrix) while every other slot of the
// same response survived. Code is the refusal's own error code
// (PROCESSING_REGRESSION_SEPARATION, PULSE_TEST_VARIANCE_ZERO,
// PULSE_MATRIX_SINGULAR, …), Message its message and Details its
// details — the {code, message, details} shape of an envelope error
// entry, so a caller reads a failed slot the way it reads a fatal run.
//
// A result carrying an Error keeps only its identity (label / name,
// type, group key) beside it; every numeric field is absent on the
// wire. A failed slot is never nil — a nil entry means the slot was
// skipped by `return`. Configuration faults are never per-slot: they
// stay fatal at validation. Under Options.Strict a failed slot fails
// the whole run with the slot's own code instead.
type SlotError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// slotFailureTest is the wire form of a TestResult carrying an Error:
// its identity and the error, no statistic.
type slotFailureTest struct {
	Label    string     `json:"label,omitempty"`
	Type     TestType   `json:"type"`
	GroupKey AxisKey    `json:"group_key,omitempty"`
	Error    *SlotError `json:"error"`
}

// slotFailureRegression is the wire form of a RegressionResult
// carrying an Error: its identity and the error, no figure.
type slotFailureRegression struct {
	Name  string         `json:"name,omitempty"`
	Type  RegressionType `json:"type"`
	Error *SlotError     `json:"error"`
}

// slotFailureMatrix is the wire form of a MatrixResult carrying an
// Error: its identity and the error, no matrix.
type slotFailureMatrix struct {
	Name        string      `json:"name"`
	Type        MatrixType  `json:"type"`
	GroupKey    AxisKey     `json:"group_key,omitempty"`
	GroupHeader *AxisHeader `json:"group_header,omitempty"`
	Error       *SlotError  `json:"error"`
}
