package encoding

import (
	"bytes"
	stderrors "errors"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// errFault is the storage failure faultReader injects.
var errFault = stderrors.New("injected storage fault")

// faultReader serves n bytes of data, then fails every read with
// errFault — a storage error partway through a record, which no decoder
// may mistake for the cohort's clean end.
type faultReader struct {
	data []byte
	n    int
}

func (f *faultReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errFault
	}
	k := min(len(p), f.n, len(f.data))
	copy(p, f.data[:k])
	f.data, f.n = f.data[k:], f.n-k
	if k == 0 {
		return 0, errFault
	}
	return k, nil
}

// decodeEntryPoints is every record decoder over one reader: the map
// decoders (full, projected, plan), and the reuse decoders by record
// family (name-keyed, index-keyed, run-skip), each with and without a
// plan.
func decodeEntryPoints(t *testing.T, schema *encoding.Schema, subset []string) map[string]func(rr *RecordReader) error {
	t.Helper()
	plan, err := BuildDecodePlan(schema, subset)
	if err != nil {
		t.Fatal(err)
	}
	keep := keepFromNames(subset)
	maps := func() (map[string]float64, map[string]bool, map[string]any) {
		return map[string]float64{}, map[string]bool{}, map[string]any{}
	}
	return map[string]func(rr *RecordReader) error{
		"ReadRecordWithWide": func(rr *RecordReader) error {
			v, n, w := maps()
			return rr.ReadRecordWithWide(v, n, w)
		},
		"ReadRecordWithWideProjected": func(rr *RecordReader) error {
			v, n, w := maps()
			return rr.ReadRecordWithWideProjected(v, n, w, keep)
		},
		"ReadRecordWithWidePlan": func(rr *RecordReader) error {
			v, n, w := maps()
			return rr.ReadRecordWithWidePlan(v, n, w, keep, plan)
		},
		"ReadRecordReused/name": func(rr *RecordReader) error { return rr.ReadRecordReused(newReusableTestRecord()) },
		"ReadRecordReused/indexed": func(rr *RecordReader) error {
			return rr.ReadRecordReused(&dualRecord{indexedTestRecord: newIndexedTestRecord(schema.Logical())})
		},
		"ReadRecordReused/run-skip": func(rr *RecordReader) error { return rr.ReadRecordReused(newRunSkipTestRecord(schema.Logical())) },
		"ReadRecordReusedWithPlan/name": func(rr *RecordReader) error {
			return rr.ReadRecordReusedWithPlan(newReusableTestRecord(), keep, plan)
		},
		"ReadRecordReusedWithPlan/indexed": func(rr *RecordReader) error {
			return rr.ReadRecordReusedWithPlan(&dualRecord{indexedTestRecord: newIndexedTestRecord(schema.Logical())}, keep, plan)
		},
		"ReadRecordReusedWithPlan/run-skip": func(rr *RecordReader) error {
			return rr.ReadRecordReusedWithPlan(newRunSkipTestRecord(schema.Logical()), keep, plan)
		},
	}
}

// TestDecode_StorageFaultNeverReadsAsEOF: on every decoder, a storage
// error at ANY byte of the first two records surfaces as that error —
// never as io.EOF (a clean end, which would silently drop the rest of
// the cohort) and never as success. Flat and grouped (0x02) cohorts,
// full and projected decodes.
func TestDecode_StorageFaultNeverReadsAsEOF(t *testing.T) {
	t.Parallel()

	big := bigMixedSchema()
	recs := generateBigSchemaRecords(t, big, 0xFA017)[:2]
	flat := bytes.Join(recs, nil)
	stride := len(recs[0])

	type cohort struct {
		name   string
		schema *encoding.Schema
		region []byte
		span   int // bytes of the region the sweep covers
	}
	cohorts := []cohort{{name: "flat", schema: big, region: flat, span: 2 * stride}}
	for _, f := range groupFixtures(t)[:1] {
		data, _ := groupedTwin(t, flatCohort(t, f.schema, f.rows), f.specs)
		s, region := recordRegion(t, data)
		cohorts = append(cohorts, cohort{name: "grouped/" + f.name, schema: s, region: region, span: 2 * s.RecordByteSize()})
	}
	subsets := [][]string{nil, {"u4_hi", "amount", "tags_u128", "u64_b"}}
	for _, c := range cohorts {
		for _, subset := range subsets {
			retained := subset
			if retained == nil {
				retained = allFieldNames(c.schema.Logical())
			}
			for name, decode := range decodeEntryPoints(t, c.schema, retained) {
				for budget := 0; budget < c.span; budget++ {
					rr := NewRecordReader(&faultReader{data: c.region, n: budget}, c.schema)
					var err error
					for i := 0; i < 2 && err == nil; i++ {
						err = decode(rr)
					}
					if !stderrors.Is(err, errFault) {
						t.Fatalf("%s %s (subset %v): fault after %d bytes returned %v, want the storage fault", c.name, name, subset, budget, err)
					}
				}
			}
		}
	}
}

// TestDecode_TruncatedTailReadsAsEOF is the other half of the contract:
// a cohort that simply ENDS partway through a record (a truncated tail,
// which Inspect warns about and CountRecords floors) reads as io.EOF on
// every decoder, after every whole record decoded.
func TestDecode_TruncatedTailReadsAsEOF(t *testing.T) {
	t.Parallel()

	big := bigMixedSchema()
	recs := generateBigSchemaRecords(t, big, 0x7A11)[:2]
	stride := len(recs[0])
	for name, decode := range decodeEntryPoints(t, big, allFieldNames(big)) {
		for cut := stride; cut < 2*stride; cut++ {
			rr := NewRecordReader(bytes.NewReader(bytes.Join(recs, nil)[:cut]), big)
			if err := decode(rr); err != nil {
				t.Fatalf("%s: whole first record (cut %d) refused: %v", name, cut, err)
			}
			if err := decode(rr); err != io.EOF {
				t.Fatalf("%s: tail cut at %d/%d: err = %v, want io.EOF", name, cut-stride, stride, err)
			}
		}
	}
}
