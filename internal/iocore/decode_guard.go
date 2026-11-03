package iocore

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// RecoverDecode converts a panic raised inside a third-party decoder
// into a coded error. A format adapter defers it at every boundary
// where it hands untrusted bytes to a library it does not own (the
// Arrow IPC reader, the Parquet reader, the xlsx workbook parser):
//
//	func (r *Reader) init() (err error) {
//		defer iocore.RecoverDecode("arrow", &err)
//		...
//	}
//
// A recovered panic becomes DATA_FILE — the source's layout is invalid
// — with details.format naming the adapter and details.panic carrying
// the recovered value as text. A nil recover leaves *err untouched.
func RecoverDecode(format string, err *error) {
	if r := recover(); r != nil {
		*err = DecodePanicError(format, r)
	}
}

// DecodePanicError is the coded error RecoverDecode returns for a
// recovered panic value r.
func DecodePanicError(format string, r any) *errors.CodedError {
	return &errors.CodedError{
		Code:    errors.DATA_FILE,
		Message: fmt.Sprintf("%s: the %s decoder failed on a malformed source: %v", format, format, r),
		Details: map[string]any{"format": format, "panic": fmt.Sprint(r)},
	}
}

// SourceFileError codes a fault opening or decoding a format adapter's
// source: DATA_FILE, message msg, the library or filesystem error kept
// as Cause so errors.Is / errors.As still reach it.
func SourceFileError(format, msg string, cause error) *errors.CodedError {
	ce := &errors.CodedError{
		Code:    errors.DATA_FILE,
		Message: msg,
		Details: map[string]any{"format": format},
		Cause:   cause,
	}
	if cause != nil {
		ce.Message = fmt.Sprintf("%s: %v", msg, cause)
	}
	return ce
}
