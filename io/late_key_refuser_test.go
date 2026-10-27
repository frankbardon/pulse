package io_test

import (
	"context"
	"fmt"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// An embedder's own keyed reader — a format whose columns are the FIRST
// record's keys — arms the explicit-schema late-key refusal by
// implementing the public pio.LateKeyRefuser, exactly as the built-in
// NDJSON and JSON-array readers do.

// keyedReader serves records as ordered key/value pairs; its header is
// the first record's keys. Armed, a later record carrying a key outside
// the header fails ReadRows with PULSE_IMPORT_SCHEMA_KEY_LATE; disarmed,
// the key is dropped.
type keyedReader struct {
	records [][][2]string
	refuse  bool
	armed   []bool // every RefuseLateKeys call, in order
}

func (r *keyedReader) RefuseLateKeys(on bool) {
	r.refuse = on
	r.armed = append(r.armed, on)
}

func (r *keyedReader) ReadHeader() ([]string, error) {
	var h []string
	for _, kv := range r.records[0] {
		h = append(h, kv[0])
	}
	return h, nil
}

func (r *keyedReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	header, _ := r.ReadHeader()
	pos := map[string]int{}
	for i, h := range header {
		pos[h] = i
	}
	for n, rec := range r.records {
		row := make([]string, len(header))
		for _, kv := range rec {
			i, ok := pos[kv[0]]
			if !ok {
				if r.refuse {
					return perrors.NewCodedErrorWithDetails(perrors.PULSE_IMPORT_SCHEMA_KEY_LATE,
						fmt.Sprintf("row %d: key %q is not among the first record's keys", n+1, kv[0]),
						map[string]any{"column": kv[0], "row": n + 1, "columns": header})
				}
				continue
			}
			row[i] = kv[1]
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

func (r *keyedReader) Reset() error { return nil }
func (r *keyedReader) Close() error { return nil }

var _ pio.LateKeyRefuser = (*keyedReader)(nil)
var _ pio.ResetReader = (*keyedReader)(nil)

func newKeyedReader() *keyedReader {
	return &keyedReader{records: [][][2]string{
		{{"a", "1"}},
		{{"a", "2"}},
		{{"a", "3"}, {"late", "x"}},
	}}
}

// TestLateKeyRefuser_ExternalReaderArmed: an explicit-schema import arms
// the embedder's reader, and its late key refuses the import with
// PULSE_IMPORT_SCHEMA_KEY_LATE.
func TestLateKeyRefuser_ExternalReaderArmed(t *testing.T) {
	t.Parallel()

	src := newKeyedReader()
	_, _, err := importCohort(t, src, catSchema("a"), false)
	ce := hasCode(t, err, perrors.PULSE_IMPORT_SCHEMA_KEY_LATE)
	if ce.Details["column"] != "late" || ce.Details["row"] != 3 {
		t.Errorf("details = %v, want column late, row 3", ce.Details)
	}
	if len(src.armed) != 1 || !src.armed[0] {
		t.Fatalf("RefuseLateKeys calls = %v, want [true]", src.armed)
	}
}

// TestLateKeyRefuser_ExternalReaderIgnoreExtra: IgnoreExtra disarms it
// explicitly and the late key is dropped.
func TestLateKeyRefuser_ExternalReaderIgnoreExtra(t *testing.T) {
	t.Parallel()

	src := newKeyedReader()
	_, rows, err := importCohort(t, src, catSchema("a"), true)
	if err != nil {
		t.Fatalf("ignore-extra: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
	if len(src.armed) != 1 || src.armed[0] {
		t.Fatalf("RefuseLateKeys calls = %v, want [false]", src.armed)
	}
}

// TestLateKeyRefuser_ExternalReaderInferenceUntouched: the inference
// path never arms it.
func TestLateKeyRefuser_ExternalReaderInferenceUntouched(t *testing.T) {
	t.Parallel()

	src := newKeyedReader()
	job := pio.NewImportJob(src, "i.pulse")
	job.FS = afero.NewMemMapFs()
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("inferred import: %v", err)
	}
	if len(src.armed) != 0 {
		t.Fatalf("RefuseLateKeys calls = %v on an inferred import, want none", src.armed)
	}
}
