package pulse

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// argumentError is a public entry's refusal of a nil or invalid
// argument (a nil request, a missing required part, a negative knob):
// SERVICE_VALIDATION with msg as its message, so a caller (and an
// observability hook) sees a registered code, never an uncoded error.
func argumentError(msg string) *errors.CodedError {
	return errors.NewCodedError(errors.SERVICE_VALIDATION, msg)
}

// keepCodedOr returns err wrapped under prefix when its chain already
// carries a *errors.CodedError (that code stays the one errors.As
// finds), else a new coded error under code: message
// "<prefix>: <err>", details (copied; may be nil), err kept as Cause so
// errors.Is / errors.As still reach it.
func keepCodedOr(code errors.Code, prefix string, details map[string]any, err error) error {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	out := errors.NewCodedErrorWithDetails(code, prefix+": "+err.Error(), details)
	out.Cause = err
	return out
}

// tableFileError is a pulse.New table-directory loader fault (label,
// range, lookup tables dir; populations dir): the directory cannot be
// stat'd or listed, is not a directory, or one of its files cannot be
// read or parsed. DATA_FILE with details.path (the directory or the
// file) and details.kind (label_table, range_table, lookup_table,
// population); cause, when non-nil, is kept so errors.Is / errors.As
// reach the fs or encoding/json error.
func tableFileError(kind, path, msg string, cause error) *errors.CodedError {
	if cause != nil {
		msg += ": " + cause.Error()
	}
	ce := errors.NewCodedErrorWithDetails(errors.DATA_FILE, msg, map[string]any{"path": path, "kind": kind})
	ce.Cause = cause
	return ce
}
