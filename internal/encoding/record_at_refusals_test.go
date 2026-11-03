package encoding

import (
	"bytes"
	stderrors "errors"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// seekFailer is a ReadSeeker whose Seek always fails.
type seekFailer struct{ io.Reader }

var errSeek = stderrors.New("injected seek failure")

func (seekFailer) Seek(int64, int) (int64, error) { return 0, errSeek }

// TestReadRecordAt_Refusals: the random-access decoders refuse a nil
// reader and an index past the end with ENCODING_INVALID, report a seek
// failure as ENCODING_IO carrying the cause, and decode a valid index.
func TestReadRecordAt_Refusals(t *testing.T) {
	t.Parallel()

	s := bigMixedSchema()
	recs := generateBigSchemaRecords(t, s, 0x2EC0)[:3]
	cohort := flatCohort(t, s, recs)
	loc, err := encoding.NewRecordLocator(bytes.NewReader(cohort), s)
	if err != nil {
		t.Fatal(err)
	}
	maps := func() (map[string]float64, map[string]bool, map[string]any, map[string]uint64) {
		return map[string]float64{}, map[string]bool{}, map[string]any{}, map[string]uint64{}
	}
	at := func(r io.ReadSeeker, i uint64) error {
		v, n, w, _ := maps()
		return ReadRecordAt(loc, r, i, v, n, w, nil, nil)
	}
	raw := func(r io.ReadSeeker, i uint64) error {
		v, n, w, x := maps()
		return ReadRecordAtRaw(loc, r, i, v, n, w, x)
	}
	for name, read := range map[string]func(io.ReadSeeker, uint64) error{"ReadRecordAt": at, "ReadRecordAtRaw": raw} {
		if err := read(nil, 0); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s(nil reader): err = %v, want ENCODING_INVALID", name, err)
		}
		if err := read(bytes.NewReader(cohort), 3); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s(index 3 of 3): err = %v, want ENCODING_INVALID", name, err)
		}
		if err := read(seekFailer{bytes.NewReader(cohort)}, 1); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errSeek) {
			t.Errorf("%s(seek failure): err = %v, want ENCODING_IO wrapping the cause", name, err)
		}
		if err := read(bytes.NewReader(cohort), 2); err != nil {
			t.Errorf("%s(index 2): %v", name, err)
		}
	}
}

// TestFlattenCohortBytes_Refusals: a non-cohort is refused, an
// ungrouped cohort is returned as-is (the same bytes, no copy), and a
// grouped cohort whose payload ends mid-record is refused rather than
// flattened with its tail silently dropped.
func TestFlattenCohortBytes_Refusals(t *testing.T) {
	t.Parallel()

	if _, _, err := FlattenCohortBytes([]byte("not a cohort")); err == nil {
		t.Error("garbage flattened")
	}
	fx := groupFixtures(t)[0]
	flat := flatCohort(t, fx.schema, fx.rows)
	out, _, err := FlattenCohortBytes(flat)
	if err != nil || len(out) != len(flat) || &out[0] != &flat[0] {
		t.Errorf("ungrouped cohort was copied or changed: (%d bytes, %v)", len(out), err)
	}
	grouped, _ := groupedTwin(t, flat, fx.specs)
	if _, _, err := FlattenCohortBytes(grouped[:len(grouped)-1]); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("grouped cohort cut mid-record: err = %v, want ENCODING_INVALID", err)
	}
	back, _, err := FlattenCohortBytes(grouped)
	if err != nil || !bytes.Equal(back, flat) {
		t.Errorf("grouped cohort did not flatten back to its 0x01 original (%v)", err)
	}
}

// TestReadBucketByKey_Refusals: a nil meta and a seek failure are both
// ENCODING_INVALID (the sidecar cannot be addressed), never a panic or
// an empty bucket.
func TestReadBucketByKey_Refusals(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteIndex(&buf, sweepIndex()); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadIndexMeta(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBucketByKey(bytes.NewReader(buf.Bytes()), nil, []byte{1}); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("nil meta: err = %v, want ENCODING_INVALID", err)
	}
	if _, err := ReadBucketByKey(seekFailer{bytes.NewReader(buf.Bytes())}, meta, []byte{1}); !errors.HasCode(err, errors.ENCODING_INVALID) || !stderrors.Is(err, errSeek) {
		t.Errorf("seek failure: err = %v, want ENCODING_INVALID wrapping the cause", err)
	}
}

// TestRecordReader_GroupIndexUngrouped: an ungrouped reader has no group
// entry to report, before or after a record.
func TestRecordReader_GroupIndexUngrouped(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8}}}
	rr := NewRecordReader(bytes.NewReader([]byte{4}), s)
	if _, ok := rr.GroupIndex(0); ok {
		t.Error("GroupIndex ok before any record")
	}
	if err := rr.ReadRecord(map[string]float64{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rr.GroupIndex(0); ok {
		t.Error("GroupIndex ok on an ungrouped record")
	}
}
