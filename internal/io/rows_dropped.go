package io

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// Convert stages a PULSE_IMPORT_ROWS_DROPPED raised by ConvertJob names
// in details.stage: rows whose per-row source zone (SourceTZField) could
// not be resolved, dropped before the target; rows the target Writer
// refused; or rows the KeepPulseAt intermediate import skipped.
const (
	DroppedStageSource    = "source"
	DroppedStageTarget    = "target"
	DroppedStageKeepPulse = "keep_pulse"
)

// rowsDropped builds the PULSE_IMPORT_ROWS_DROPPED entry for a PARTIAL
// pass — imported rows written, len(rowErrors) skipped — and returns nil
// when nothing was dropped. Total failure (imported == 0) never reaches
// here: totalRowFailure returns its fatal error first.
//
// details: dropped, imported, first_failure {row, column, reason}; stage
// when non-empty (convert only). The first failure's column and reason
// come from the row error's own details when it is coded
// (PULSE_IMPORT_ROW_ERROR carries both); otherwise column is "" and
// reason is the error text.
func rowsDropped(imported int, rowErrors []RowError, stage string) *errors.CodedError {
	if len(rowErrors) == 0 {
		return nil
	}
	first := rowErrors[0]
	column, reason := "", "no reason recorded"
	if first.Err != nil {
		reason = first.Err.Error()
		var ce *errors.CodedError
		if stderrors.As(first.Err, &ce) {
			if c, ok := ce.Details["column"].(string); ok {
				column = c
			}
			if r, ok := ce.Details["reason"].(string); ok && r != "" {
				reason = r
			}
		}
	}
	details := map[string]any{
		"dropped":  len(rowErrors),
		"imported": imported,
		"first_failure": map[string]any{
			"row":    first.Row,
			"column": column,
			"reason": reason,
		},
	}
	what := "import"
	if stage != "" {
		details["stage"] = stage
		what = "convert (" + stage + ")"
	}
	where := ""
	if column != "" {
		where = fmt.Sprintf(", column %q", column)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_ROWS_DROPPED,
		fmt.Sprintf("%s dropped %d of %d rows; first failure at row %d%s: %s",
			what, len(rowErrors), imported+len(rowErrors), first.Row, where, reason),
		details)
}

// strictRowsDropped is the StrictRows verdict: the rowsDropped entry as
// the job's fatal error, or nil when nothing was dropped or strict is off.
func strictRowsDropped(strict bool, w *errors.CodedError) error {
	if !strict || w == nil {
		return nil
	}
	return errors.NewCodedErrorWithDetails(w.Code, w.Message+" (strict: the partial import is refused and nothing is written)", w.Details)
}

// droppedList wraps a possibly-nil warning as the report's slice.
func droppedList(w *errors.CodedError) []*errors.CodedError {
	if w == nil {
		return nil
	}
	return []*errors.CodedError{w}
}
