package processing

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Record.Uint64Value (#427): every reuse-decoder path — full stride
// (fresh, and one reused record across the stream so run-skip keeps
// unchanged fields), the plan decoder into a projected binding, and the
// grouped (0x02) decoder with k a parent-group member — hands a u64
// field's exact on-wire word to the record, so two values above 2^53
// that share a float64 echo read back distinct. A value write, a null
// and a record copy keep the word honest.

var exactU64Vals = []uint64{
	1 << 53, 1<<53 + 1, 1<<53 + 1, math.MaxUint64, math.MaxUint64 - 1, 7, 0, math.MaxUint64 - 1,
}

// exactU64Null marks the rows whose k is null.
func exactU64Null(r int) bool { return r%5 == 3 }

func exactU64Schema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 4, Nullable: true},
		{Name: "grp", Type: encoding.FieldTypeU8, ByteOffset: 12},
	}}
}

func exactU64Flat(t *testing.T, rows int) []byte {
	t.Helper()
	s := exactU64Schema()
	var b bytes.Buffer
	if err := encx.WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rows; r++ {
		rec := make([]byte, 13+s.BitmapByteSize())
		binary.LittleEndian.PutUint32(rec[0:], uint32(r))
		if exactU64Null(r) {
			encoding.BitmapSetNull(rec[13:], 1)
		} else {
			binary.LittleEndian.PutUint64(rec[4:], exactU64Vals[r%len(exactU64Vals)])
		}
		rec[12] = byte(r % len(exactU64Vals))
		b.Write(rec)
	}
	return b.Bytes()
}

// splitCohort returns the schema and the record region of a cohort.
func splitCohort(t *testing.T, data []byte) (*encoding.Schema, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	s, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, data[len(data)-r.Len():]
}

func TestRecord_Uint64ValueExactOnEveryDecodePath(t *testing.T) {
	const rows = 40
	flat := exactU64Flat(t, rows)
	var grouped bytes.Buffer
	if _, _, err := encx.DedupCohort(&grouped, bytes.NewReader(flat),
		[]encx.GroupSpec{{Kind: encoding.GroupKindIndexed, Members: []string{"k", "grp"}}}); err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []struct {
		name string
		data []byte
	}{{"flat", flat}, {"grouped", grouped.Bytes()}} {
		schema, region := splitCohort(t, cohort.data)
		if (cohort.name == "grouped") != schema.HasGroups() {
			t.Fatalf("%s: HasGroups = %v", cohort.name, schema.HasGroups())
		}
		type path struct {
			name string
			next func() (*Record, error)
		}
		full := func(fresh bool) func() (*Record, error) {
			rr := encx.NewRecordReader(bytes.NewReader(region), schema)
			reused := NewReusableRecord(schema)
			return func() (*Record, error) {
				rec := reused
				if fresh {
					rec = NewReusableRecord(schema)
				}
				return rec, rr.ReadRecordReused(rec)
			}
		}
		planned := func(retained []string, reuse bool) func() (*Record, error) {
			plan, err := encx.BuildDecodePlan(schema, retained)
			if err != nil {
				t.Fatal(err)
			}
			keep := func(n string) bool {
				for _, r := range retained {
					if r == n {
						return true
					}
				}
				return false
			}
			binding := BindRecords(schema, keep)
			rr := encx.NewRecordReader(bytes.NewReader(region), schema)
			reused := binding.NewRecord()
			return func() (*Record, error) {
				rec := reused
				if !reuse {
					rec = binding.NewRecord()
				}
				return rec, rr.ReadRecordReusedWithPlan(rec, keep, plan)
			}
		}
		paths := []path{
			{"full-stride-reused", full(false)},
			{"full-stride-fresh", full(true)},
			{"plan-k-only-fresh", planned([]string{"k"}, false)},
			{"plan-k-only-reused", planned([]string{"k"}, true)},
			{"plan-id-k-reused", planned([]string{"id", "k"}, true)},
		}
		for _, p := range paths {
			t.Run(cohort.name+"/"+p.name, func(t *testing.T) {
				for r := 0; ; r++ {
					rec, err := p.next()
					if err == io.EOF {
						if r != rows {
							t.Fatalf("decoded %d rows, want %d", r, rows)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					got, ok := rec.Uint64Value("k")
					if exactU64Null(r) {
						if ok {
							t.Fatalf("row %d: Uint64Value = %d, true on a null", r, got)
						}
						continue
					}
					want := exactU64Vals[r%len(exactU64Vals)]
					if !ok || got != want {
						t.Fatalf("row %d: Uint64Value = %d, %v; want %d (exact)", r, got, ok, want)
					}
					if v, _ := rec.NumericValue("k"); v != float64(want) {
						t.Fatalf("row %d: NumericValue = %v, want the echo %v", r, v, float64(want))
					}
				}
			})
		}
	}
}

// A value written after decode, a null, and a copy keep the exact word
// honest; with no word the echo converts, saturating at 2^64.
func TestRecord_Uint64ValueWritesNullsCopiesFallback(t *testing.T) {
	schema := exactU64Schema()
	flat := exactU64Flat(t, 8)
	_, region := splitCohort(t, flat)
	decodeRow := func(row int) *Record {
		rr := encx.NewRecordReader(bytes.NewReader(region), schema)
		rec := NewReusableRecord(schema)
		for r := 0; r <= row; r++ {
			if err := rr.ReadRecordReused(rec); err != nil {
				t.Fatal(err)
			}
		}
		return rec
	}
	const lossy = 1 // row 1 holds 2^53+1, echo 2^53

	rec := decodeRow(lossy)
	if u, _ := rec.Uint64Value("k"); u != 1<<53+1 {
		t.Fatalf("decoded Uint64Value = %d", u)
	}
	// Copy (the join / chain path) carries the word.
	dst := NewReusableRecord(schema)
	rec.copyStateInto(dst, func(s string) string { return s }, 0, true)
	if u, ok := dst.Uint64Value("k"); !ok || u != 1<<53+1 {
		t.Errorf("copied Uint64Value = %d, %v; want %d", u, ok, uint64(1<<53+1))
	}
	// A name-keyed SetExactU64 (the join build side) stores it too.
	byName := NewRecord(schema, map[string]float64{"k": float64(uint64(1<<53 + 1))})
	byName.SetExactU64("k", 1<<53+1)
	if u, _ := byName.Uint64Value("k"); u != 1<<53+1 {
		t.Errorf("SetExactU64 Uint64Value = %d", u)
	}
	// It is a mutation: a reused record withdraws from run-skip, so
	// decoding the same row again restores the decoded word.
	{
		rr := encx.NewRecordReader(bytes.NewReader(append(append([]byte(nil), region[14:28]...), region[14:28]...)), schema)
		reused := NewReusableRecord(schema)
		if err := rr.ReadRecordReused(reused); err != nil {
			t.Fatal(err)
		}
		reused.SetExactU64("k", 999)
		if err := rr.ReadRecordReused(reused); err != nil {
			t.Fatal(err)
		}
		if u, _ := reused.Uint64Value("k"); u != 1<<53+1 {
			t.Errorf("SetExactU64 then the same row again: Uint64Value = %d, want %d (run-skip kept the mutation)", u, uint64(1<<53+1))
		}
	}
	// ... but never for a field that holds no value.
	empty := NewReusableRecord(schema)
	empty.SetExactU64("k", 9)
	if u, ok := empty.Uint64Value("k"); ok {
		t.Errorf("SetExactU64 on an empty field: Uint64Value = %d, true", u)
	}
	// A value write retires the word.
	rec.Set("k", 5)
	if u, ok := rec.Uint64Value("k"); !ok || u != 5 {
		t.Errorf("after Set: Uint64Value = %d, %v; want 5", u, ok)
	}
	// A copy of a record without a word must not keep the destination's.
	stale := decodeRow(lossy)
	plain := NewRecord(schema, map[string]float64{"k": 11})
	plain.copyStateInto(stale, func(s string) string { return s }, 0, true)
	if u, _ := stale.Uint64Value("k"); u != 11 {
		t.Errorf("copy over a stale word: Uint64Value = %d, want 11", u)
	}
	// A null hides it.
	rec = decodeRow(lossy)
	rec.SetNull("k")
	if u, ok := rec.Uint64Value("k"); ok {
		t.Errorf("after SetNull: Uint64Value = %d, true", u)
	}
	rec = decodeRow(lossy)
	rec.SetNullFieldAt(1)
	rec.ClearNullAt(1)
	rec.SetNumericAt(1, 3)
	if u, _ := rec.Uint64Value("k"); u != 3 {
		t.Errorf("decoded null then echo only: Uint64Value = %d, want 3 (no stale word)", u)
	}
	// Fallback: the echo converts; one at 2^64 saturates.
	for _, tc := range []struct {
		echo float64
		want uint64
	}{{42, 42}, {1 << 53, 1 << 53}, {1 << 64, math.MaxUint64}, {float64(uint64(math.MaxUint64 - 1)), math.MaxUint64}} {
		r := NewRecord(schema, map[string]float64{"k": tc.echo, "id": tc.echo})
		for _, f := range []string{"k", "id"} {
			if u, ok := r.Uint64Value(f); !ok || u != tc.want {
				t.Errorf("fallback %s echo %v: Uint64Value = %d, %v; want %d", f, tc.echo, u, ok, tc.want)
			}
		}
	}
	if _, ok := NewRecord(schema, nil).Uint64Value("k"); ok {
		t.Error("absent field: Uint64Value ok")
	}
}
