package service

import (
	"bytes"
	"context"
	"encoding/binary"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pfs "github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Fixtures
//
// Three fields, so a widen's effect on the columns AROUND the set column is
// observable at this layer too (the engine proves the byte identity; this
// layer proves the orchestration hands it the right file at all):
//
//	id     u32    — prefix field, offset never moves
//	picks  set_*  — the widened column
//	score  f64    — suffix field, offset moves by the width delta
// ---------------------------------------------------------------------------

type widenSvcRow struct {
	id    uint32
	bits  []int
	score float64
}

func widenSvcSchema(t *testing.T, setType encoding.FieldType, dictEntries int) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for i := 0; i < dictEntries; i++ {
		if _, err := d.Add(string(rune('a'+i%26)) + string(rune('0'+i/26))); err != nil {
			t.Fatalf("dict add %d: %v", i, err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "picks", Type: setType, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4 + setType.ByteSize(), CsvColumnIdx: 2},
		},
	}
}

// widenSvcCohortBytes emits a complete single-file .pulse cohort at any set
// rung. It does not use writePulseFile because that goes through the uint64
// WriteFieldValue API, which refuses the wide rungs by contract
// (ENCODING_TYPE_MISMATCH) — the wide slots are written through the
// normative SetMask wire API instead.
func widenSvcCohortBytes(t *testing.T, schema *encoding.Schema, rows []widenSvcRow) []byte {
	t.Helper()
	setType := schema.Fields[1].Type
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range rows {
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], r.id)
		buf.Write(id[:])

		slot := make([]byte, setType.ByteSize())
		var m encoding.SetMask
		for _, b := range r.bits {
			m = m.WithBit(b)
		}
		if setType.IsWideSet() {
			if err := encoding.PutSetMask(slot, setType, m); err != nil {
				t.Fatalf("PutSetMask: %v", err)
			}
		} else {
			low, _ := m.Uint64()
			var word [8]byte
			binary.LittleEndian.PutUint64(word[:], low)
			copy(slot, word[:len(slot)])
		}
		buf.Write(slot)

		var sc [8]byte
		binary.LittleEndian.PutUint64(sc[:], math.Float64bits(r.score))
		buf.Write(sc[:])
	}
	return buf.Bytes()
}

// widenSvcReadBack decodes a cohort written by widenSvcCohortBytes at
// whatever rung its schema now carries.
func widenSvcReadBack(t *testing.T, data []byte) (*encoding.Schema, []widenSvcRow) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	payload := make([]byte, r.Len())
	if _, err := r.Read(payload); err != nil && r.Len() > 0 {
		t.Fatalf("reading payload: %v", err)
	}
	stride := schema.RecordByteSize()
	setType := schema.Fields[1].Type
	setOff := 4
	scoreOff := 4 + setType.ByteSize()

	var out []widenSvcRow
	for off := 0; off+stride <= len(payload); off += stride {
		rec := payload[off : off+stride]
		m, err := widenSvcMask(rec[setOff:setOff+setType.ByteSize()], setType)
		if err != nil {
			t.Fatalf("decoding set slot: %v", err)
		}
		var bits []int
		for b := range m.Bits() {
			bits = append(bits, b)
		}
		out = append(out, widenSvcRow{
			id:    binary.LittleEndian.Uint32(rec[0:4]),
			bits:  bits,
			score: math.Float64frombits(binary.LittleEndian.Uint64(rec[scoreOff : scoreOff+8])),
		})
	}
	return schema, out
}

func widenSvcMask(slot []byte, ft encoding.FieldType) (encoding.SetMask, error) {
	if ft.IsWideSet() {
		return encoding.SetMaskFromBytes(ft, slot)
	}
	var word [8]byte
	copy(word[:], slot)
	return encoding.SetMaskFromUint64(binary.LittleEndian.Uint64(word[:])), nil
}

func widenSvcRows() []widenSvcRow {
	return []widenSvcRow{
		{id: 1, bits: []int{0, 3, 63}, score: 10.5},
		{id: 2, bits: nil, score: -2.25},
		{id: 3, bits: []int{7}, score: 1e12},
	}
}

// widenSvcSetup writes a set_u64 cohort at path in a fresh MemMapFs and
// returns the config plus the original bytes (for byte-identity assertions).
func widenSvcSetup(t *testing.T, path string, setType encoding.FieldType) (*pfs.Config, []byte) {
	t.Helper()
	cfg := pfs.NewMemMap()
	data := widenSvcCohortBytes(t, widenSvcSchema(t, setType, 8), widenSvcRows())
	if err := afero.WriteFile(cfg.Fs(), path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg, data
}

func widenSvcCode(t *testing.T, err error) errors.Code {
	t.Helper()
	if err == nil {
		t.Fatal("want a coded error, got nil")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v does not carry a *errors.CodedError; `pulse errors lookup` cannot be used on it", err)
	}
	return ce.Code
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestServiceWidenSetField_WidensSingleFileCohort(t *testing.T) {
	cfg, before := widenSvcSetup(t, "cohort.pulse", encoding.FieldTypeSetU64)
	svc := New(cfg)

	rep, err := svc.WidenSetField(context.Background(), "cohort.pulse", "picks", encoding.FieldTypeSetU128)
	if err != nil {
		t.Fatalf("WidenSetField: %v", err)
	}
	if rep == nil {
		t.Fatal("nil report on a successful widen")
	}
	if rep.Field != "picks" || rep.From != encoding.FieldTypeSetU64 || rep.To != encoding.FieldTypeSetU128 {
		t.Errorf("report identities = {%s %s->%s}, want {picks set_u64->set_u128}", rep.Field, rep.From, rep.To)
	}
	if rep.Records != 3 {
		t.Errorf("report Records = %d, want 3", rep.Records)
	}
	if rep.StrideAfter-rep.StrideBefore != 8 {
		t.Errorf("stride delta = %d, want 8 (set_u64 -> set_u128)", rep.StrideAfter-rep.StrideBefore)
	}

	after, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile after widen: %v", err)
	}
	if bytes.Equal(after, before) {
		t.Fatal("cohort bytes unchanged after a widen that reports 3 rewritten records")
	}

	schema, rows := widenSvcReadBack(t, after)
	if got := schema.Fields[1].Type; got != encoding.FieldTypeSetU128 {
		t.Errorf("widened field type = %s, want set_u128", got)
	}
	want := widenSvcRows()
	if len(rows) != len(want) {
		t.Fatalf("read back %d records, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i].id != want[i].id || rows[i].score != want[i].score {
			t.Errorf("record %d neighbours = {%d %v}, want {%d %v}",
				i, rows[i].id, rows[i].score, want[i].id, want[i].score)
		}
		if len(rows[i].bits) != len(want[i].bits) {
			t.Errorf("record %d bits = %v, want %v", i, rows[i].bits, want[i].bits)
			continue
		}
		for b := range want[i].bits {
			if rows[i].bits[b] != want[i].bits[b] {
				t.Errorf("record %d bits = %v, want %v", i, rows[i].bits, want[i].bits)
				break
			}
		}
	}
}

// A shard archive must never be widened as if it were a single file. The
// engine would refuse it at ReadHeader, so the bytes would survive either
// way; what this asserts is that the caller is told the TRUE reason —
// SERVICE_VALIDATION naming the archive layout — rather than "invalid pulse
// file", and that the archive is still openable afterwards.
func TestServiceWidenSetField_RefusesShardArchive(t *testing.T) {
	cfg := pfs.NewMemMap()
	shard := widenSvcCohortBytes(t, widenSvcSchema(t, encoding.FieldTypeSetU64, 8), widenSvcRows())
	for _, name := range []string{"a.pulse", "b.pulse"} {
		if err := afero.WriteFile(cfg.Fs(), name, shard, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	svc := New(cfg)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "archive.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	before, err := afero.ReadFile(cfg.Fs(), "archive.pulse")
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}

	_, werr := svc.WidenSetField(ctx, "archive.pulse", "picks", encoding.FieldTypeSetU128)
	if got := widenSvcCode(t, werr); got != errors.SERVICE_VALIDATION {
		t.Errorf("archive refusal code = %s, want SERVICE_VALIDATION", got)
	}

	after, err := afero.ReadFile(cfg.Fs(), "archive.pulse")
	if err != nil {
		t.Fatalf("ReadFile archive after refusal: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("shard archive bytes changed under a refused widen")
	}
	cohort, err := svc.Open(ctx, "archive.pulse")
	if err != nil {
		t.Fatalf("archive no longer opens after a refused widen: %v", err)
	}
	if len(cohort.Shards()) != 2 {
		t.Errorf("archive has %d shards after a refused widen, want 2", len(cohort.Shards()))
	}
}

func TestServiceWidenSetField_RefusesAnchoredShardPath(t *testing.T) {
	cfg, _ := widenSvcSetup(t, "cohort.pulse", encoding.FieldTypeSetU64)
	svc := New(cfg)

	_, err := svc.WidenSetField(context.Background(), "archive.pulse#a.pulse", "picks", encoding.FieldTypeSetU128)
	if got := widenSvcCode(t, err); got != errors.SERVICE_VALIDATION {
		t.Errorf("anchored-path refusal code = %s, want SERVICE_VALIDATION", got)
	}
}

func TestServiceWidenSetField_MissingCohortIsCoded(t *testing.T) {
	cfg := pfs.NewMemMap()
	svc := New(cfg)

	_, err := svc.WidenSetField(context.Background(), "nope.pulse", "picks", encoding.FieldTypeSetU128)
	if got := widenSvcCode(t, err); got != errors.SERVICE_RESOURCE {
		t.Errorf("missing-cohort refusal code = %s, want SERVICE_RESOURCE", got)
	}
}

// Every refusal the engine owns must reach the caller CODED, and must leave
// the cohort byte-identical — a refused widen is not a partial widen.
func TestServiceWidenSetField_RefusalsAreCodedAndNonDestructive(t *testing.T) {
	cases := []struct {
		name    string
		setType encoding.FieldType
		field   string
		target  encoding.FieldType
		want    errors.Code
	}{
		{"missing field", encoding.FieldTypeSetU64, "absent", encoding.FieldTypeSetU128, errors.ENCODING_INVALID},
		{"non-set field", encoding.FieldTypeSetU64, "score", encoding.FieldTypeSetU128, errors.ENCODING_TYPE_MISMATCH},
		{"narrower target", encoding.FieldTypeSetU64, "picks", encoding.FieldTypeSetU16, errors.ENCODING_TYPE_MISMATCH},
		{"equal target", encoding.FieldTypeSetU64, "picks", encoding.FieldTypeSetU64, errors.ENCODING_TYPE_MISMATCH},
		{"non-set target", encoding.FieldTypeSetU64, "picks", encoding.FieldTypeU32, errors.ENCODING_TYPE_MISMATCH},
		{"past the widest rung", encoding.FieldTypeSetU256, "picks", encoding.FieldTypeSetU256, errors.ENCODING_TYPE_MISMATCH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, before := widenSvcSetup(t, "cohort.pulse", tc.setType)
			svc := New(cfg)

			_, err := svc.WidenSetField(context.Background(), "cohort.pulse", tc.field, tc.target)
			if got := widenSvcCode(t, err); got != tc.want {
				t.Errorf("refusal code = %s, want %s", got, tc.want)
			}
			after, rerr := afero.ReadFile(cfg.Fs(), "cohort.pulse")
			if rerr != nil {
				t.Fatalf("ReadFile after refusal: %v", rerr)
			}
			if !bytes.Equal(before, after) {
				t.Error("cohort bytes changed under a refused widen")
			}
		})
	}
}
