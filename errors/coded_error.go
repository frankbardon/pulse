package errors

import (
	"encoding/json"
	"errors"
	"maps"
)

// CodedError wraps an error code with context. It is the primary structured
// error type for Pulse, supporting JSON serialization for --json CLI output
// and error chain traversal via Unwrap.
type CodedError struct {
	// Code identifies the error category.
	Code Code

	// Message provides a human-readable description.
	Message string

	// Details holds arbitrary key-value context (row number, field name, etc.).
	Details map[string]any

	// Cause is the underlying error, if any.
	Cause error
}

// Error implements the error interface.
// Format: "CODE: message"
func (e *CodedError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// Unwrap returns the underlying cause for errors.Is / errors.As traversal.
func (e *CodedError) Unwrap() error {
	return e.Cause
}

// codedErrorJSONWithDetails is the JSON shape when Details is non-nil.
type codedErrorJSONWithDetails struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

// codedErrorJSONNoDetails is the JSON shape when Details is nil.
type codedErrorJSONNoDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MarshalJSON implements json.Marshaler, producing the --json envelope shape.
// When Details is nil, the "details" key is omitted entirely.
// When Details is non-nil (even if empty), the "details" key is included.
func (e *CodedError) MarshalJSON() ([]byte, error) {
	if e.Details == nil {
		return json.Marshal(codedErrorJSONNoDetails{
			Code:    string(e.Code),
			Message: e.Message,
		})
	}
	return json.Marshal(codedErrorJSONWithDetails{
		Code:    string(e.Code),
		Message: e.Message,
		Details: e.Details,
	})
}

// NewCodedError creates a new CodedError with no details or cause.
func NewCodedError(code Code, message string) *CodedError {
	return &CodedError{
		Code:    code,
		Message: message,
	}
}

// NewCodedErrorWithDetails creates a new CodedError with pre-populated details.
// The details map is defensively copied.
func NewCodedErrorWithDetails(code Code, message string, details map[string]any) *CodedError {
	cp := make(map[string]any, len(details))
	maps.Copy(cp, details)
	return &CodedError{
		Code:    code,
		Message: message,
		Details: cp,
	}
}

// WrapCodedError wraps an existing error with a CodedError layer.
func WrapCodedError(err error, code Code, message string) *CodedError {
	return &CodedError{
		Code:    code,
		Message: message,
		Cause:   err,
	}
}

// HasCode traverses the error chain to determine if any CodedError
// in the chain carries the specified code.
// Returns false if err is nil or no CodedError in the chain matches.
func HasCode(err error, code Code) bool {
	if err == nil {
		return false
	}

	var ce *CodedError
	for {
		if !errors.As(err, &ce) {
			return false
		}
		if ce.Code == code {
			return true
		}
		err = ce.Cause
		if err == nil {
			return false
		}
	}
}
