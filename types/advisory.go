package types

// Advisory is a coded, non-blocking note that the chosen analysis may
// not fit the data — a two-group test over a many-level grouping, many
// uncorrected p-values, a penalised fit whose inference is undefined.
// It is not a warning: a warning reports a data or engine problem, an
// advisory questions an analysis choice, and the two ride separate
// slots so a caller can filter them independently. Strict mode never
// promotes an advisory to an error.
//
// Predict lists them in PredictResult.Advisories (descriptor.Advisory
// is this type); a result that reports a figure as undefined because of
// the analysis choice carries the same advisory beside it
// (RegressionResult.Advisories). Options.SuppressAdvisories drops a
// code from both.
//
// Code is a PULSE_ADVISORY_* error code (pulse errors lookup CODE
// serves its fixup text). Message is one plain sentence. Details
// carries the slot it concerns and the figures that fired the rule,
// and — when a replacement exists on this instance — a "suggested"
// key: a replacement operator name or a request patch, never a whole
// rewritten request.
type Advisory struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}
