package encoding

import (
	"bytes"
	stderrors "errors"
	"reflect"
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

// TestHeaderWrite_FormatVersion pins that the plain WriteHeader still
// emits the 0x01 baseline — the byte every pre-0x02 file carries.
func TestHeaderWrite_FormatVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	data := buf.Bytes()
	if len(data) < HeaderSize {
		t.Fatalf("header too short: %d bytes", len(data))
	}
	if data[len(MagicBytes)] != 0x01 {
		t.Errorf("version byte = 0x%02x, want 0x01", data[len(MagicBytes)])
	}
}

// TestHeaderRead_ReturnsVersion: every supported version is accepted and
// handed back to the caller rather than discarded.
func TestHeaderRead_ReturnsVersion(t *testing.T) {
	for _, v := range []byte{0x01, 0x02} {
		var buf bytes.Buffer
		if err := writeHeaderVersion(&buf, v); err != nil {
			t.Fatalf("writeHeaderVersion(0x%02x): %v", v, err)
		}
		got, err := ReadHeader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("ReadHeader(0x%02x): %v", v, err)
		}
		if got != v {
			t.Errorf("ReadHeader returned version 0x%02x, want 0x%02x", got, v)
		}
	}
}

func TestSupportedFormatVersions(t *testing.T) {
	if got := SupportedFormatVersions(); !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Fatalf("SupportedFormatVersions = %v, want [1 2]", got)
	}
	for v := 0; v < 256; v++ {
		want := v == 0x01 || v == 0x02
		if IsSupportedFormatVersion(byte(v)) != want {
			t.Errorf("IsSupportedFormatVersion(0x%02x) = %v, want %v", v, !want, want)
		}
	}
	if MaxFormatVersion != 0x02 || FormatVersion != 0x01 {
		t.Fatalf("MaxFormatVersion/FormatVersion = 0x%02x/0x%02x, want 0x02/0x01", MaxFormatVersion, FormatVersion)
	}
}

func TestHeaderRead_TruncatedHeader(t *testing.T) {
	data := []byte{0x50, 0x55} // too short
	_, err := ReadHeader(bytes.NewReader(data))
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
	_, err := ReadHeader(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error on wrong magic")
	}
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("expected ENCODING_INVALID, got: %v", err)
	}
}

// assertUnsupportedVersion checks the coded shape every unsupported
// version must carry: ENCODING_INVALID, the offending byte under
// details["version"], and the accepted set under
// details["supported_versions"] — so the refusal is actionable.
func assertUnsupportedVersion(t *testing.T, err error, v byte) {
	t.Helper()
	if err == nil {
		t.Fatalf("version 0x%02x: expected an error", v)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("version 0x%02x: error is not coded: %v", v, err)
	}
	if ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("version 0x%02x: code = %s, want ENCODING_INVALID", v, ce.Code)
	}
	if got, ok := ce.Details["version"].(byte); !ok || got != v {
		t.Errorf("version 0x%02x: details[version] = %#v, want the offending byte", v, ce.Details["version"])
	}
	if got := ce.Details["supported_versions"]; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("version 0x%02x: details[supported_versions] = %#v, want [1 2]", v, got)
	}
}

// TestHeaderRead_UnsupportedVersion: anything outside {0x01, 0x02} —
// including 0x00 and the next version up — stays ENCODING_INVALID,
// never a silent misparse.
func TestHeaderRead_UnsupportedVersion(t *testing.T) {
	for _, v := range []byte{0x00, 0x03, 0x7F, 0xFF} {
		var buf bytes.Buffer
		if err := WriteHeader(&buf); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		data[len(MagicBytes)] = v
		_, err := ReadHeader(bytes.NewReader(data))
		assertUnsupportedVersion(t, err, v)

		if err := writeHeaderVersion(&bytes.Buffer{}, v); err == nil {
			t.Errorf("writeHeaderVersion(0x%02x) succeeded; an unsupported version must never be written", v)
		}
		if _, err := ReadSchema(bytes.NewReader(nil), v); err == nil {
			t.Errorf("ReadSchema(version 0x%02x) succeeded; want ENCODING_INVALID", v)
		} else {
			assertUnsupportedVersion(t, err, v)
		}
	}
}
