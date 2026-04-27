package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestHeaderWrite_MagicBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	data := buf.Bytes()
	if len(data) < len(MagicBytes) {
		t.Fatalf("header too short: %d bytes", len(data))
	}
	for i, b := range MagicBytes {
		if data[i] != b {
			t.Errorf("magic byte[%d] = 0x%02x, want 0x%02x", i, data[i], b)
		}
	}
}

func TestHeaderWrite_FormatVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	data := buf.Bytes()
	if len(data) < HeaderSize {
		t.Fatalf("header too short: %d bytes", len(data))
	}
	if data[len(MagicBytes)] != FormatVersion {
		t.Errorf("version byte = 0x%02x, want 0x%02x", data[len(MagicBytes)], FormatVersion)
	}
}

func TestHeaderRead_ValidHeader(t *testing.T) {
	var buf bytes.Buffer
	WriteHeader(&buf)

	if err := ReadHeader(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("ReadHeader on valid header: %v", err)
	}
}

func TestHeaderRead_TruncatedHeader(t *testing.T) {
	data := []byte{0x50, 0x55} // too short
	err := ReadHeader(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error on truncated header")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

func TestHeaderRead_WrongMagic(t *testing.T) {
	data := make([]byte, HeaderSize)
	data[0] = 0xFF // wrong magic
	err := ReadHeader(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error on wrong magic")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

func TestHeaderRead_WrongVersion(t *testing.T) {
	var buf bytes.Buffer
	WriteHeader(&buf)
	data := buf.Bytes()
	data[len(MagicBytes)] = 0xFF // wrong version

	err := ReadHeader(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error on wrong version")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}
