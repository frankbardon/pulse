package encoding

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestFieldDescription_WriteRead(t *testing.T) {
	desc := "Patient age in years at enrollment"
	var buf bytes.Buffer
	if err := WriteDescription(&buf, desc); err != nil {
		t.Fatalf("WriteDescription: %v", err)
	}

	got, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDescription: %v", err)
	}
	if got != desc {
		t.Errorf("round-trip = %q, want %q", got, desc)
	}
}

func TestFieldDescription_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteDescription(&buf, ""); err != nil {
		t.Fatalf("WriteDescription empty: %v", err)
	}

	got, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDescription empty: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFieldDescription_MaxLength(t *testing.T) {
	desc := strings.Repeat("a", MaxDescriptionBytes)
	var buf bytes.Buffer
	if err := WriteDescription(&buf, desc); err != nil {
		t.Fatalf("WriteDescription max: %v", err)
	}

	got, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDescription max: %v", err)
	}
	if len(got) != MaxDescriptionBytes {
		t.Errorf("len = %d, want %d", len(got), MaxDescriptionBytes)
	}
}

func TestFieldDescription_TooLong(t *testing.T) {
	desc := strings.Repeat("x", MaxDescriptionBytes+1)
	var buf bytes.Buffer
	err := WriteDescription(&buf, desc)
	if err == nil {
		t.Fatal("expected error for too-long description")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG) {
		t.Errorf("expected PULSE_IMPORT_DESCRIPTION_TOO_LONG, got: %v", err)
	}
}

func TestFieldDescription_UTF8(t *testing.T) {
	// Multi-byte UTF-8: "日本語" is 9 bytes.
	desc := "日本語テスト"
	var buf bytes.Buffer
	if err := WriteDescription(&buf, desc); err != nil {
		t.Fatalf("WriteDescription utf8: %v", err)
	}

	got, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDescription utf8: %v", err)
	}
	if got != desc {
		t.Errorf("round-trip = %q, want %q", got, desc)
	}
}

func TestFieldDescription_UTF8_ExactlyAtLimit(t *testing.T) {
	// Create a string that is exactly MaxDescriptionBytes in UTF-8.
	desc := strings.Repeat("é", MaxDescriptionBytes/2) // é is 2 bytes in UTF-8
	if len(desc) != MaxDescriptionBytes {
		t.Skipf("test assumption broken: len=%d", len(desc))
	}
	var buf bytes.Buffer
	if err := WriteDescription(&buf, desc); err != nil {
		t.Fatalf("WriteDescription: %v", err)
	}

	got, err := ReadDescription(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDescription: %v", err)
	}
	if got != desc {
		t.Errorf("round-trip mismatch for multi-byte at limit")
	}
}
