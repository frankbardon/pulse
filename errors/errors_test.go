package errors_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// TestCodedError_Error verifies the Error() string formatting.
func TestCodedError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *perr.CodedError
		expected string
	}{
		{
			name: "simple error without details",
			err: &perr.CodedError{
				Code:    perr.ENCODING_INVALID,
				Message: "invalid header",
			},
			expected: "ENCODING_INVALID: invalid header",
		},
		{
			name: "error with details",
			err: &perr.CodedError{
				Code:    perr.DATA_FILE,
				Message: "file not found",
				Details: map[string]any{"path": "/data/test.csv"},
			},
			expected: "DATA_FILE: file not found",
		},
		{
			name: "pulse-specific code",
			err: &perr.CodedError{
				Code:    perr.PULSE_IMPORT_ROW_ERROR,
				Message: "row 42 failed",
				Details: map[string]any{"row": 42, "field": "age"},
			},
			expected: "PULSE_IMPORT_ROW_ERROR: row 42 failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Errorf("Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestCodedError_MarshalJSON verifies JSON serialization.
func TestCodedError_MarshalJSON(t *testing.T) {
	t.Run("basic marshaling", func(t *testing.T) {
		err := &perr.CodedError{
			Code:    perr.ENCODING_INVALID,
			Message: "invalid header",
		}

		data, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatalf("json.Marshal failed: %v", marshalErr)
		}

		var parsed map[string]any
		if parseErr := json.Unmarshal(data, &parsed); parseErr != nil {
			t.Fatalf("json.Unmarshal failed: %v", parseErr)
		}

		if parsed["code"] != "ENCODING_INVALID" {
			t.Errorf("code = %v, want ENCODING_INVALID", parsed["code"])
		}
		if parsed["message"] != "invalid header" {
			t.Errorf("message = %v, want 'invalid header'", parsed["message"])
		}
	})

	t.Run("with details", func(t *testing.T) {
		err := &perr.CodedError{
			Code:    perr.PULSE_IMPORT_ROW_ERROR,
			Message: "row parse failed",
			Details: map[string]any{
				"row":   42,
				"field": "age",
				"value": "abc",
			},
		}

		data, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatalf("json.Marshal failed: %v", marshalErr)
		}

		var parsed map[string]any
		if parseErr := json.Unmarshal(data, &parsed); parseErr != nil {
			t.Fatalf("json.Unmarshal failed: %v", parseErr)
		}

		if parsed["code"] != "PULSE_IMPORT_ROW_ERROR" {
			t.Errorf("code = %v, want PULSE_IMPORT_ROW_ERROR", parsed["code"])
		}

		details, ok := parsed["details"].(map[string]any)
		if !ok {
			t.Fatal("details should be a map")
		}
		if details["field"] != "age" {
			t.Errorf("details[field] = %v, want 'age'", details["field"])
		}
	})

	t.Run("nil details omitted", func(t *testing.T) {
		err := &perr.CodedError{
			Code:    perr.CLI_INPUT,
			Message: "invalid flag",
		}

		data, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatalf("json.Marshal failed: %v", marshalErr)
		}

		// details should not appear in JSON when nil
		if strings.Contains(string(data), "details") {
			t.Errorf("nil details should be omitted from JSON, got: %s", data)
		}
	})

	t.Run("empty details included", func(t *testing.T) {
		err := &perr.CodedError{
			Code:    perr.CLI_INPUT,
			Message: "invalid flag",
			Details: map[string]any{},
		}

		data, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatalf("json.Marshal failed: %v", marshalErr)
		}

		// Empty map should still appear
		if !strings.Contains(string(data), "details") {
			t.Errorf("empty details should appear in JSON, got: %s", data)
		}
	})
}

// TestCodedError_Unwrap verifies error chain compatibility.
func TestCodedError_Unwrap(t *testing.T) {
	t.Run("without cause returns nil", func(t *testing.T) {
		err := &perr.CodedError{
			Code:    perr.ENCODING_INVALID,
			Message: "test",
		}
		if err.Unwrap() != nil {
			t.Error("Unwrap() should return nil without cause")
		}
	})

	t.Run("with cause returns it", func(t *testing.T) {
		cause := errors.New("root cause")
		err := &perr.CodedError{
			Code:    perr.ENCODING_INVALID,
			Message: "test",
			Cause:   cause,
		}
		if err.Unwrap() != cause {
			t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), cause)
		}
	})

	t.Run("errors.Is traverses chain", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		err := &perr.CodedError{
			Code:    perr.DATA_FILE,
			Message: "file error",
			Cause:   sentinel,
		}
		if !errors.Is(err, sentinel) {
			t.Error("errors.Is should find sentinel through Unwrap")
		}
	})
}

// TestNewCodedError verifies the constructor.
func TestNewCodedError(t *testing.T) {
	t.Run("basic construction", func(t *testing.T) {
		err := perr.NewCodedError(perr.ENCODING_INVALID, "test message")
		if err.Code != perr.ENCODING_INVALID {
			t.Errorf("Code = %v, want %v", err.Code, perr.ENCODING_INVALID)
		}
		if err.Message != "test message" {
			t.Errorf("Message = %q, want %q", err.Message, "test message")
		}
		if err.Details != nil {
			t.Errorf("Details should be nil, got %v", err.Details)
		}
		if err.Cause != nil {
			t.Errorf("Cause should be nil, got %v", err.Cause)
		}
	})
}

// TestNewCodedErrorWithDetails verifies construction with details.
func TestNewCodedErrorWithDetails(t *testing.T) {
	details := map[string]any{"row": 42}
	err := perr.NewCodedErrorWithDetails(perr.PULSE_IMPORT_ROW_ERROR, "row failed", details)

	if err.Code != perr.PULSE_IMPORT_ROW_ERROR {
		t.Errorf("Code = %v, want %v", err.Code, perr.PULSE_IMPORT_ROW_ERROR)
	}
	if err.Details["row"] != 42 {
		t.Errorf("Details[row] = %v, want 42", err.Details["row"])
	}

	// Verify defensive copy
	details["row"] = 99
	if err.Details["row"] == 99 {
		t.Error("constructor should defensively copy details map")
	}
}

// TestWrapCodedError verifies wrapping an existing error.
func TestWrapCodedError(t *testing.T) {
	cause := errors.New("underlying")
	err := perr.WrapCodedError(cause, perr.DATA_FILE, "file operation failed")

	if err.Cause != cause {
		t.Error("Cause not set correctly")
	}
	if err.Code != perr.DATA_FILE {
		t.Errorf("Code = %v, want %v", err.Code, perr.DATA_FILE)
	}

	t.Run("wrap nil behaves like new", func(t *testing.T) {
		err := perr.WrapCodedError(nil, perr.DATA_FILE, "test")
		if err.Cause != nil {
			t.Errorf("Cause should be nil when wrapping nil, got %v", err.Cause)
		}
	})
}

// TestHasCode checks code detection through error chains.
func TestHasCode(t *testing.T) {
	t.Run("nil error returns false", func(t *testing.T) {
		if perr.HasCode(nil, perr.ENCODING_INVALID) {
			t.Error("HasCode should return false for nil error")
		}
	})

	t.Run("direct match", func(t *testing.T) {
		err := perr.NewCodedError(perr.ENCODING_INVALID, "test")
		if !perr.HasCode(err, perr.ENCODING_INVALID) {
			t.Error("HasCode should match direct code")
		}
	})

	t.Run("nested match", func(t *testing.T) {
		inner := perr.NewCodedError(perr.ENCODING_INVALID, "inner")
		outer := perr.WrapCodedError(inner, perr.DATA_FILE, "outer")

		if !perr.HasCode(outer, perr.ENCODING_INVALID) {
			t.Error("HasCode should find nested code")
		}
		if !perr.HasCode(outer, perr.DATA_FILE) {
			t.Error("HasCode should find outer code")
		}
	})

	t.Run("no match", func(t *testing.T) {
		err := perr.NewCodedError(perr.ENCODING_INVALID, "test")
		if perr.HasCode(err, perr.DATA_FILE) {
			t.Error("HasCode should return false for non-matching code")
		}
	})

	t.Run("non-CodedError returns false", func(t *testing.T) {
		err := errors.New("standard error")
		if perr.HasCode(err, perr.ENCODING_INVALID) {
			t.Error("HasCode should return false for standard errors")
		}
	})
}

// TestCodedError_ErrorsAs verifies errors.As compatibility.
func TestCodedError_ErrorsAs(t *testing.T) {
	t.Run("extracts CodedError", func(t *testing.T) {
		err := perr.NewCodedError(perr.ENCODING_INVALID, "test")
		var target *perr.CodedError
		if !errors.As(err, &target) {
			t.Fatal("errors.As should extract CodedError")
		}
		if target.Code != perr.ENCODING_INVALID {
			t.Errorf("Code = %v, want %v", target.Code, perr.ENCODING_INVALID)
		}
	})

	t.Run("extracts from chain", func(t *testing.T) {
		inner := perr.NewCodedError(perr.ENCODING_INVALID, "inner")
		outer := perr.WrapCodedError(inner, perr.DATA_FILE, "outer")

		var target *perr.CodedError
		if !errors.As(outer, &target) {
			t.Fatal("errors.As should extract CodedError from chain")
		}
		// Should get outermost
		if target.Code != perr.DATA_FILE {
			t.Errorf("Code = %v, want %v", target.Code, perr.DATA_FILE)
		}
	})
}

// TestCodedError_JSON_Envelope verifies the --json envelope shape.
func TestCodedError_JSON_Envelope(t *testing.T) {
	err := &perr.CodedError{
		Code:    perr.PULSE_IMPORT_SCHEMA_AMBIGUOUS,
		Message: "column 'status' type ambiguous",
		Details: map[string]any{
			"column":     "status",
			"candidates": []string{"categorical", "string"},
		},
	}

	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("json.Marshal failed: %v", marshalErr)
	}

	var envelope map[string]any
	if parseErr := json.Unmarshal(data, &envelope); parseErr != nil {
		t.Fatalf("json.Unmarshal failed: %v", parseErr)
	}

	// Verify envelope keys
	if _, ok := envelope["code"]; !ok {
		t.Error("envelope missing 'code' key")
	}
	if _, ok := envelope["message"]; !ok {
		t.Error("envelope missing 'message' key")
	}
	if _, ok := envelope["details"]; !ok {
		t.Error("envelope missing 'details' key")
	}
}
