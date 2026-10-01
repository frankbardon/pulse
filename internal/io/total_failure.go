package io

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// totalRowFailure classifies the outcome of a row pass and, for total
// failure only, returns the coded error the job should return instead of
// a report.
//
// Total failure is rowsOK == 0 AND rowsRead > 0 AND at least one row
// error. All three conjuncts are load-bearing:
//
//   - rowsOK == 0 alone is not enough. A genuinely empty source — zero
//     data rows, zero row errors — produces an empty cohort, and an empty
//     cohort is a legitimate outcome, not a failure. (It is also what the
//     NDJSON reader currently hands a one-record file, since ReadHeader
//     consumes the first object; that is a separate bug and must not be
//     escalated here.)
//   - rowsRead > 0 is what distinguishes "nothing to do" from "nothing
//     worked".
//   - at least one row error means the verdict always has evidence to
//     carry: the caller gets the first failure's row index and message,
//     not a bare count.
//
// Partial failure — some rows through, some errored — is deliberately NOT
// total failure and keeps returning a report plus a nil error. The
// per-row detail stays on Report.RowErrors where it has always been.
//
// The code is taken from the first row error when that error is itself
// coded, so the verdict resolves through `pulse errors lookup` to the
// reason the rows failed rather than to a generic wrapper. fallback
// supplies the code for an uncoded row error, which is the ordinary case
// on the export side where the failure originates in a target Writer.
//
// Returns nil for every non-total outcome, so call sites read as a plain
// `if err := totalRowFailure(...); err != nil { return nil, err }`.
func totalRowFailure(verb string, rowsOK, rowsRead int, rowErrors []RowError, fallback errors.Code) error {
	if rowsOK != 0 || rowsRead == 0 || len(rowErrors) == 0 {
		return nil
	}

	first := rowErrors[0]
	code := fallback
	var ce *errors.CodedError
	if stderrors.As(first.Err, &ce) {
		code = ce.Code
	}

	reason := "no reason recorded"
	if first.Err != nil {
		reason = first.Err.Error()
	}

	return errors.NewCodedErrorWithDetails(
		code,
		fmt.Sprintf("every row failed to %s: 0 of %d rows %sed, %d row errors. First failure at row %d: %s",
			verb, rowsRead, verb, len(rowErrors), first.Row, reason),
		map[string]any{
			"rows_read":    rowsRead,
			"rows_failed":  len(rowErrors),
			"first_row":    first.Row,
			"first_error":  reason,
			"rows_written": rowsOK,
		},
	)
}
