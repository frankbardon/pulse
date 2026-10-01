package service

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
)

// Parent-width sweep for grouped (0x02) decode (E3-S7).
//
// A synthetic parent/child cohort whose PARENT block width is swept
// (10 / 40 / 80 member fields) against a FIXED 29-field child block,
// every parent fanning out to 12 or 13 child rows (mean 12.5x). The
// grouped twin puts the whole parent block (key included) in one indexed
// group, so the physical row is a u32 index plus the child block. The
// claim under measurement: grouped per-row decode cost is roughly FLAT
// as the parent block widens, where the 0x01 twin pays for every parent
// field on every row.
//
// Every value is a pure function of (groupWidthSeed, width): no name,
// dictionary value or cell value from any real cohort. Generated only
// inside -bench runs.

const groupWidthSeed uint64 = 0x0E357A1DEC0DE

// groupWidthSchema is the flat schema for a parent block of width
// parentW (p_00 is the key) plus the fixed child block.
func groupWidthSchema(parentW int) *encoding.Schema {
	var fields []encoding.Field
	add := func(f encoding.Field) {
		f.CsvColumnIdx = len(fields)
		fields = append(fields, f)
	}
	cat := func(name string, ft encoding.FieldType, card int, nullable bool) {
		add(encoding.Field{Name: name, Type: ft, Dictionary: joinShapeDict(name, card), Nullable: nullable})
	}
	add(encoding.Field{Name: "p_00", Type: encoding.FieldTypeU32})
	for i := 1; i < parentW; i++ {
		name := fmt.Sprintf("p_%02d", i)
		switch i % 10 {
		case 1, 2, 3:
			cat(name, encoding.FieldTypeCategoricalU8, 3+i%17, i%7 == 3)
		case 4:
			cat(name, encoding.FieldTypeCategoricalU16, 300+i, false)
		case 5:
			add(encoding.Field{Name: name, Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Nullable: i%20 == 15})
		case 6, 7:
			add(encoding.Field{Name: name, Type: encoding.FieldTypePackedBool})
		case 8:
			add(encoding.Field{Name: name, Type: encoding.FieldTypeU64, Nullable: i%3 == 0})
		case 9:
			cat(name, encoding.FieldTypeSetU8, 6, false)
		default:
			add(encoding.Field{Name: name, Type: encoding.FieldTypeF64})
		}
	}
	for i := range 12 {
		cat(fmt.Sprintf("c_cat8_%02d", i), encoding.FieldTypeCategoricalU8, 3+(i*7)%19, i%4 == 2)
	}
	cat("c_cat16_00", encoding.FieldTypeCategoricalU16, 500, false)
	for i := range 8 {
		add(encoding.Field{Name: fmt.Sprintf("c_flag_%02d", i), Type: encoding.FieldTypePackedBool})
	}
	for i := range 5 {
		add(encoding.Field{Name: fmt.Sprintf("c_u64_%02d", i), Type: encoding.FieldTypeU64, Nullable: i == 1})
	}
	cat("c_set_00", encoding.FieldTypeSetU8, 5, false)
	cat("c_set_01", encoding.FieldTypeSetU16, 14, true)
	cat("c_set_02", encoding.FieldTypeSetU32, 24, false)
	return &encoding.Schema{Fields: fields}
}

// groupWidthKeep4 retains two parent fields (a categorical and a
// decimal) and two child fields: a typical projected request.
func groupWidthKeep4(name string) bool {
	switch name {
	case "p_02", "p_05", "c_cat8_00", "c_u64_00":
		return true
	}
	return false
}

// groupWidthCell writes one drawn value of f.
func groupWidthCell(buf *bytes.Buffer, rng *rand.Rand, f *encoding.Field) (null bool, err error) {
	null = f.Nullable && rng.IntN(10) == 0
	switch {
	case f.Type.IsBitPacked():
		return null, buf.WriteByte(byte(rng.IntN(2)))
	case f.Type == encoding.FieldTypeDecimal128:
		return null, encoding.WriteDecimal128(buf, encoding.NewDecimal128FromInt(rng.Int64N(1_000_000_000)-500_000_000))
	case f.Type == encoding.FieldTypeF64:
		return null, encoding.WriteFieldValue(buf, f.Type, rng.Uint64()>>12|0x3FF0_0000_0000_0000)
	case f.Type.HasDictionary():
		n := uint64(f.Dictionary.Count())
		if f.Type.IsSet() {
			return null, encoding.WriteFieldValue(buf, f.Type, rng.Uint64()&(1<<n-1))
		}
		return null, encoding.WriteFieldValue(buf, f.Type, rng.Uint64N(n))
	default:
		return null, encoding.WriteFieldValue(buf, f.Type, rng.Uint64N(1<<40))
	}
}

// buildGroupWidthCohort returns the flat (0x01) payload of parents
// parents: each parent block is drawn once and repeated across its
// child rows, byte for byte.
func buildGroupWidthCohort(b testing.TB, parentW, parents int) (*encoding.Schema, []byte) {
	b.Helper()
	s := groupWidthSchema(parentW)
	var out bytes.Buffer
	if err := encoding.WritePreamble(&out, s); err != nil {
		b.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(groupWidthSeed, uint64(parentW)))
	bm := make([]byte, s.BitmapByteSize())
	var parent bytes.Buffer
	var parentNull []int
	for p := range parents {
		parent.Reset()
		parentNull = parentNull[:0]
		if err := encoding.WriteFieldValue(&parent, s.Fields[0].Type, uint64(p)); err != nil {
			b.Fatal(err)
		}
		for i := 1; i < parentW; i++ {
			null, err := groupWidthCell(&parent, rng, &s.Fields[i])
			if err != nil {
				b.Fatal(err)
			}
			if null {
				parentNull = append(parentNull, i)
			}
		}
		for range joinShapeFanout(p) {
			clear(bm)
			for _, i := range parentNull {
				encoding.BitmapSetNull(bm, i)
			}
			out.Write(parent.Bytes())
			for i := parentW; i < len(s.Fields); i++ {
				null, err := groupWidthCell(&out, rng, &s.Fields[i])
				if err != nil {
					b.Fatal(err)
				}
				if null {
					encoding.BitmapSetNull(bm, i)
				}
			}
			out.Write(bm)
		}
	}
	return s, out.Bytes()
}

// groupWidthTwins returns the flat and grouped schemas and their record
// regions, sorted and scattered (the same permutation of rows).
func groupWidthTwins(b testing.TB, parentW, parents int) (fs, gs *encoding.Schema, regions map[string][]byte) {
	b.Helper()
	fs, flat := buildGroupWidthCohort(b, parentW, parents)
	var members []string
	for i := range parentW {
		members = append(members, fs.Fields[i].Name)
	}
	var grouped bytes.Buffer
	gs, n, err := encoding.DedupCohort(&grouped, bytes.NewReader(flat), []encoding.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: members, Key: []string{"p_00"}},
	})
	if err != nil {
		b.Fatal(err)
	}
	rows := int(n)
	region := func(data []byte, stride int) []byte { return data[len(data)-rows*stride:] }
	fr, gr := region(flat, fs.RecordByteSize()), region(grouped.Bytes(), gs.RecordByteSize())
	perm := rand.New(rand.NewPCG(groupWidthSeed, 0x5CA7)).Perm(rows)
	scatter := func(r []byte, stride int) []byte {
		out := make([]byte, 0, len(r))
		for _, p := range perm {
			out = append(out, r[p*stride:(p+1)*stride]...)
		}
		return out
	}
	return fs, gs, map[string][]byte{
		"v1/sorted":    fr,
		"v2/sorted":    gr,
		"v1/scattered": scatter(fr, fs.RecordByteSize()),
		"v2/scattered": scatter(gr, gs.RecordByteSize()),
	}
}

// BenchmarkGroupedDecode_ParentWidth sweeps parent-block width and
// reports ns/row for the 0x01 twin and the grouped 0x02 cohort: reuse
// (one run-skip positional record, the streaming path) and buffered (a
// fresh bound record per row, the buffered Process path), full decode
// and a 4-field projection, sorted and scattered. Reported only; the
// deterministic gate is encoding's TestGroupedDecode_MemberWriteCounts.
func BenchmarkGroupedDecode_ParentWidth(b *testing.B) {
	const parents = 4800 // x12.5 = 60,000 rows
	for _, w := range []int{10, 40, 80} {
		fs, gs, regions := groupWidthTwins(b, w, parents)
		rows := joinShapeRows(parents)
		b.Logf("parent width %d: flat stride %d B, grouped stride %d B, %d entries x %d B",
			w, fs.RecordByteSize(), gs.RecordByteSize(), gs.GroupEntryCount(0), gs.GroupEntryWidth(0))
		for _, shape := range []string{"full", "proj4"} {
			for _, mode := range []string{"reuse", "buffered"} {
				for _, order := range []string{"sorted", "scattered"} {
					for _, v := range []string{"v1", "v2"} {
						s := fs
						if v == "v2" {
							s = gs
						}
						var keep encoding.FieldFilter
						var plan *encoding.DecodePlan
						if shape == "proj4" {
							keep = groupWidthKeep4
							p, err := s.BuildDecodePlan(retainedFromFilter(s, keep))
							if err != nil {
								b.Fatal(err)
							}
							plan = p
						}
						region := regions[v+"/"+order]
						scan := func() (int, error) {
							rr := encoding.NewRecordReader(bytes.NewReader(region), s)
							var reused *processing.Record
							var binding *processing.RecordBinding
							if mode == "reuse" {
								reused = processing.NewReusableRecord(s)
							} else {
								binding = recordBindingFor(s, plan, keep)
							}
							n := 0
							for {
								rec := reused
								if rec == nil {
									rec = binding.NewRecord()
								}
								var err error
								if plan != nil {
									err = rr.ReadRecordReusedWithPlan(rec, keep, plan)
								} else {
									err = rr.ReadRecordReused(rec)
								}
								if err == io.EOF {
									return n, nil
								}
								if err != nil {
									return n, err
								}
								n++
							}
						}
						b.Run(fmt.Sprintf("w%d/%s/%s/%s/%s", w, shape, mode, order, v), func(b *testing.B) {
							best := -1.0
							for b.Loop() {
								for range 3 {
									if d := timedRun(b, scan, rows); best < 0 || d < best {
										best = d
									}
								}
							}
							b.ReportMetric(best/float64(rows), "ns/row")
						})
					}
				}
			}
		}
	}
}
