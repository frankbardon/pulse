package integration_test

import (
	"bytes"
	"context"
	stdcsv "encoding/csv"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// bitpacked_legacy_parity_test.go pins #372: a LEGACY cohort whose
// schema stores a nonzero BitPosition for a bit-packed field (every
// current writer lays out BitPosition 0, see LayoutFields) reads the
// same values on every reader — export (CSV), the engine (Process,
// projected and full decode), SPSS export and point lookup — with no
// re-import. Export used to read the whole on-wire byte, ignoring the
// stored BitPosition.

const legacyBitsCohort = "legacy_bits.pulse"

// legacyBitsRow is row i's stored values: flag (packed_bool at bit 1),
// hi (u4 in the high nibble) and lo (u4, low nibble, the control).
func legacyBitsRow(i int) (flag bool, hi, lo uint8) {
	return i%3 == 0, uint8((i*5 + 1) % 16), uint8((i * 7) % 16)
}

const legacyBitsRows = 12

// writeLegacyBitsCohort lays the cohort out by hand: id u8, flag
// packed_bool at BitPosition 1, hi u4 at BitPosition 4, lo u4 at 0.
// The flag byte of a FALSE row carries a stray bit 0 and a hi byte a
// stray low nibble — bits the stored BitPosition says are not the
// field's — so a reader that ignores BitPosition reads a different
// value.
func writeLegacyBitsCohort(t *testing.T, fsys afero.Fs) {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 1, BitPosition: 1},
		{Name: "hi", Type: encoding.FieldTypeU4, ByteOffset: 2, BitPosition: 4},
		{Name: "lo", Type: encoding.FieldTypeU4, ByteOffset: 3},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < legacyBitsRows; i++ {
		flag, hi, lo := legacyBitsRow(i)
		fb := byte(0x01) // stray bit 0: not the field's bit
		if flag {
			fb = 0x02
		}
		buf.Write([]byte{byte(i + 1), fb, hi<<4 | 0x0A, lo})
	}
	if err := afero.WriteFile(fsys, legacyBitsCohort, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func legacyBitsNum(t *testing.T, s string) float64 {
	t.Helper()
	switch s {
	case "true":
		return 1
	case "false":
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("cell %q: %v", s, err)
	}
	return f
}

// exportedRows reads every exported row back as id -> column -> value.
func exportedRows(t *testing.T, header []string, rows [][]string) map[int]map[string]float64 {
	t.Helper()
	out := make(map[int]map[string]float64, len(rows))
	for _, r := range rows {
		m := make(map[string]float64, len(r))
		for c, s := range r {
			m[header[c]] = legacyBitsNum(t, s)
		}
		out[int(m["id"])] = m
	}
	return out
}

func wantLegacyBits() map[int]map[string]float64 {
	want := make(map[int]map[string]float64, legacyBitsRows)
	for i := 0; i < legacyBitsRows; i++ {
		flag, hi, lo := legacyBitsRow(i)
		f := 0.0
		if flag {
			f = 1
		}
		want[i+1] = map[string]float64{"id": float64(i + 1), "flag": f, "hi": float64(hi), "lo": float64(lo)}
	}
	return want
}

func TestLegacyBitPosition_ExportEngineSPSSLookupAgree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeLegacyBitsCohort(t, fsys)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	want := wantLegacyBits()
	check := func(reader string, got map[int]map[string]float64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d rows, want %d", reader, len(got), len(want))
		}
		for id, w := range want {
			for col, v := range w {
				if got[id][col] != v {
					t.Errorf("%s: id %d %s = %v, want %v (the engine's read)", reader, id, col, got[id][col], v)
				}
			}
		}
	}

	// Export (CSV, the row loop).
	cw, err := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewExportJob(legacyBitsCohort, cw)
	job.FS = fsys
	if _, err := p.Export(ctx, job); err != nil {
		t.Fatalf("Export csv: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	recs, err := stdcsv.NewReader(bytes.NewReader(cw.Bytes())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	check("export csv", exportedRows(t, recs[0], recs[1:]))

	// SPSS export (the cohort writer), read back through the SPSS reader.
	sw, err := pio.NewWriter(pio.FormatSPSS, fsys, "legacy_bits.sav", pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sjob := pio.NewExportJob(legacyBitsCohort, sw)
	sjob.FS = fsys
	if _, err := p.Export(ctx, sjob); err != nil {
		t.Fatalf("Export spss: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatal(err)
	}
	sr, err := pio.NewReader(pio.FormatSPSS, fsys, "legacy_bits.sav", pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := sr.ReadHeader()
	if err != nil {
		t.Fatal(err)
	}
	var srows [][]string
	if err := sr.ReadRows(ctx, func(row []string) error {
		srows = append(srows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = sr.Close()
	check("export spss", exportedRows(t, sh, srows))

	// Point lookup (full engine record decode).
	if _, err := p.BuildIndex(ctx, legacyBitsCohort, []string{"id"}); err != nil {
		t.Fatal(err)
	}
	lk := make(map[int]map[string]float64, legacyBitsRows)
	for id := 1; id <= legacyBitsRows; id++ {
		res, err := p.Lookup(ctx, &pulse.LookupRequest{
			Cohort: &types.Cohort{Filename: legacyBitsCohort}, Field: "id", Value: strconv.Itoa(id),
		})
		if err != nil {
			t.Fatalf("Lookup id %d: %v", id, err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("Lookup id %d: %d rows", id, len(res.Rows))
		}
		m := map[string]float64{}
		for k, v := range res.Rows[0] {
			m[k] = lookupNum(t, v)
		}
		lk[id] = m
	}
	check("lookup", lk)

	// Process (projected decode): per-field sums equal the stored ones.
	var req pulse.Request
	body := `{"cohort":{"filename":"` + legacyBitsCohort + `"},"aggregations":[
		{"type":"AGG_SUM","field":"flag","label":"flag"},
		{"type":"AGG_SUM","field":"hi","label":"hi"},
		{"type":"AGG_SUM","field":"lo","label":"lo"}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(ctx, &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	sums := map[string]float64{}
	for _, w := range want {
		for col, v := range w {
			sums[col] += v
		}
	}
	for _, col := range []string{"flag", "hi", "lo"} {
		if got := lookupNum(t, resp.Data[0][col]); got != sums[col] {
			t.Errorf("process: sum(%s) = %v, want %v", col, got, sums[col])
		}
	}
}

func lookupNum(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	case uint8:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		return legacyBitsNum(t, x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Fatalf("unhandled value %T %v", v, v)
	return 0
}
