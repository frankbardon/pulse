package io

import (
	"archive/zip"
	"bytes"
	stderrors "errors"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// u8Cohort is a single-file cohort of one u8 field holding vals.
func u8Cohort(t *testing.T, vals ...byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	s := &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeU8}}}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	buf.Write(vals)
	return buf.Bytes()
}

// TestOpenCohortRecords_Anchored: an anchored path reads that one
// shard's records under the canonical schema (Archive, Entry set); the
// reserved entry (even into a missing archive) and an unknown one keep
// pulse.Open's codes; a missing
// archive is the plain read error export has always returned.
func TestOpenCohortRecords_Anchored(t *testing.T) {
	t.Parallel()

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for _, e := range []struct {
		name    string
		payload []byte
	}{{encx.ReservedSchemaName, u8Cohort(t)}, {"a.pulse", u8Cohort(t, 1, 2)}, {"b.pulse", u8Cohort(t, 7, 8, 9)}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(e.payload)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "arch.pulse", zbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := OpenCohortRecords(fsys, "arch.pulse#b.pulse")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(c.Reader)
	if !bytes.Equal(got, []byte{7, 8, 9}) || c.Records != 3 || !c.Archive || c.Entry != "b.pulse" {
		t.Errorf("anchored read = %v records %d archive %v entry %q, want b.pulse's 3 records", got, c.Records, c.Archive, c.Entry)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	for path, code := range map[string]errors.Code{
		"arch.pulse#" + encx.ReservedSchemaName: errors.PULSE_SHARD_RESERVED_NAME,
		"arch.pulse#nope.pulse":                 errors.PULSE_SHARD_MISSING,
		// Refused on the name alone, before the archive is opened.
		"gone.pulse#" + encx.ReservedSchemaName: errors.PULSE_SHARD_RESERVED_NAME,
	} {
		_, err := OpenCohortRecords(fsys, path)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != code {
			t.Errorf("%s: %v, want %s", path, err, code)
		}
	}
	if _, err := OpenCohortRecords(fsys, "gone.pulse#a.pulse"); err == nil {
		t.Error("an anchor into a missing archive opened")
	}
}
