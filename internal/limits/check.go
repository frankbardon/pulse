package limits

import "github.com/frankbardon/pulse/errors"

// Grade is how sure a predict-time limit finding is that the run would
// breach the limit.
type Grade string

const (
	// Certain: the estimate is the exact figure the runtime will see
	// (a matrix dimension, a Compose slot count, a chain stage count).
	// A certain finding refuses the request — predict marks it invalid
	// and the process pre-flight raises PULSE_LIMIT_EXCEEDED before any
	// record is decoded.
	Certain Grade = "certain"
	// Possible: the estimate is an upper bound (a Cartesian product of
	// axis sizes, a dictionary size clamped by the record count). A
	// possible finding is a warning only: the request may still run.
	Possible Grade = "possible"
)

// Finding is one limit an estimate exceeds. Estimated is the figure
// predict (or the pre-flight) computed, in the limit's unit.
type Finding struct {
	Limit      Name
	Configured int64
	Estimated  int64
	Grade      Grade
}

// Err is the PULSE_LIMIT_EXCEEDED error a breach of f raises, with the
// estimate as the observed figure — what the runtime reports for a
// certain finding.
func (f Finding) Err() *errors.CodedError {
	return Exceeded(f.Limit, f.Configured, f.Estimated)
}

// Evaluate grades one estimate against the effective limit n in l: a
// finding (and true) when the limit is bounded and estimated exceeds
// it, else false. A limit equal to its bound is not exceeded.
func Evaluate(l Limits, n Name, estimated int64, g Grade) (Finding, bool) {
	configured := Value(l, n)
	if IsUnlimited(configured) || estimated <= configured {
		return Finding{}, false
	}
	return Finding{Limit: n, Configured: configured, Estimated: estimated, Grade: g}, true
}

// Check is the runtime rule for an exact count: PULSE_LIMIT_EXCEEDED
// when observed exceeds the effective limit n in l, nil otherwise
// (including when the limit is Unlimited).
func Check(l Limits, n Name, observed int64) *errors.CodedError {
	if f, ok := Evaluate(l, n, observed, Certain); ok {
		return f.Err()
	}
	return nil
}

// CheckMatrixDim refuses a matrix whose dimension p (its resolved
// member count) exceeds MaxMatrixDim.
func CheckMatrixDim(l Limits, p int) *errors.CodedError {
	return Check(l, MaxMatrixDim, int64(p))
}

// CheckComposeSlots refuses a Compose call carrying more than
// MaxComposeSlots requests.
func CheckComposeSlots(l Limits, slots int) *errors.CodedError {
	return Check(l, MaxComposeSlots, int64(slots))
}

// CheckChainStages refuses a ProcessChain call carrying more than
// MaxChainStages stages.
func CheckChainStages(l Limits, stages int) *errors.CodedError {
	return Check(l, MaxChainStages, int64(stages))
}

// CheckJoinBuildRows refuses a join whose build (right) side holds
// more than MaxJoinBuildRows records. The runtime calls it twice: on
// the header-only right-side count before the build decodes a record,
// and on the running decode count as a backstop for a miscount.
func CheckJoinBuildRows(l Limits, rows int64) *errors.CodedError {
	return Check(l, MaxJoinBuildRows, rows)
}

// FirstCertain returns the error of the first Certain finding in fs —
// the pre-flight refusal — or nil when none is certain.
func FirstCertain(fs []Finding) *errors.CodedError {
	for _, f := range fs {
		if f.Grade == Certain {
			return f.Err()
		}
	}
	return nil
}
