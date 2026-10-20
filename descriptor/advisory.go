package descriptor

import "github.com/frankbardon/pulse/types"

// Advisory is one entry of PredictResult.Advisories: a coded,
// non-blocking note that the chosen analysis may not fit the data — a
// two-group test over a many-level grouping, many uncorrected p-values.
// It is not a warning: a warning reports a data or engine problem, an
// advisory questions an analysis choice, and the two ride separate
// slots so a caller can filter them independently. Strict mode never
// promotes an advisory to an error, and a predict advisory never changes
// what the request executes.
//
// It is types.Advisory, the one shape a result carrying an advisory
// beside an undefined figure (types.RegressionResult.Advisories) also
// uses. Code is a PULSE_ADVISORY_* error code (pulse errors lookup CODE
// serves its fixup text). Message is one plain sentence. Details
// carries the slot it concerns and the figures that fired the rule,
// and — when a replacement exists on this instance — a "suggested"
// key: a replacement operator name or a request patch, never a whole
// rewritten request.
type Advisory = types.Advisory
