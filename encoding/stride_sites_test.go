package encoding

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// Fixed-stride regression suite, encoding half. A deduped (0x02) cohort
// changes the stride VALUE and keeps the fixed-stride PROPERTY; these
// tests pin the sites that turn a stride into a byte position — the
// reader's one-read-per-record walk and the decode plan's whole-record
// skip — against both a parent-grouped cohort and a constant-elided one
// (a single constant group, which is what ImportJob.ElideConstants
// writes). TestGroupedCohort_EveryDecodePathMatchesFlat already proves
// the DECODED values match; what it cannot see is how many physical
// bytes each record consumed, which is where a wrong stride hides until
// the NEXT record lands mid-row.

// strideFixtures is groupFixtures plus a constant-only (elided) twin.
func strideFixtures(t *testing.T) []groupFixture {
	t.Helper()
	fxs := groupFixtures(t)
	big := bigMixedSchema()
	recs := generateBigSchemaRecords(t, big, 0x57A1DE)
	specs := []GroupSpec{{Kind: GroupKindConstant, Members: []string{"u8_c", "tags_u64", "amount"}}}
	return append(fxs, groupFixture{
		name: "elided_constants", schema: big, specs: specs,
		rows:    parentChildRows(t, big, recs, specs, []int{1}, 150, 0x57A1D),
		subsets: [][]string{{"u8_c", "u16_c"}},
	})
}

// countingReader counts the bytes pulled from the physical region.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// TestStrideSites_ReaderConsumesOnePhysicalStridePerRecord: every
// record decode — the single-ReadFull full-stride walk, the run-skip
// walk, and the plan walk (some fields, and the empty plan) — consumes
// EXACTLY one physical stride (Schema.RecordByteSize) of the underlying
// region per record, on the 0x01 cohort and on each deduped twin. A
// reader that over- or under-reads by one byte still decodes record 0
// correctly and every later record from the wrong offset.
func TestStrideSites_ReaderConsumesOnePhysicalStridePerRecord(t *testing.T) {
	for _, fx := range strideFixtures(t) {
		flat := flatCohort(t, fx.schema, fx.rows)
		grouped, gs := groupedTwin(t, flat, fx.specs)
		if gs.RecordByteSize() >= fx.schema.RecordByteSize() {
			t.Fatalf("%s: deduped stride %d is not smaller than flat %d", fx.name, gs.RecordByteSize(), fx.schema.RecordByteSize())
		}
		for _, tw := range []struct {
			name string
			data []byte
		}{{"0x01", flat}, {"0x02", grouped}} {
			s, region := recordRegion(t, tw.data)
			stride := s.RecordByteSize()
			type step func(rr *RecordReader) error
			plan := func(retained []string) step {
				p, err := s.BuildDecodePlan(retained)
				if err != nil {
					t.Fatal(err)
				}
				keep := keepFromNames(retained)
				rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(s.Logical())}
				return func(rr *RecordReader) error { return rr.ReadRecordReusedWithPlan(rec, keep, p) }
			}
			steps := map[string]step{
				"full_stride": func() step {
					rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(s.Logical())}
					return func(rr *RecordReader) error { return rr.ReadRecordReused(rec) }
				}(),
				"run_skip": func() step {
					rec := newRunSkipTestRecord(s.Logical())
					return func(rr *RecordReader) error { return rr.ReadRecordReused(rec) }
				}(),
				"map": func(rr *RecordReader) error {
					return rr.ReadRecordWithWide(map[string]float64{}, map[string]bool{}, map[string]any{})
				},
				"plan_subset": plan(fx.subsets[0]),
				"plan_empty":  plan(nil),
			}
			for name, st := range steps {
				t.Run(fmt.Sprintf("%s/%s/%s", fx.name, tw.name, name), func(t *testing.T) {
					cr := &countingReader{r: bytes.NewReader(region)}
					rr := NewRecordReader(cr, s)
					for k := 1; k <= len(fx.rows); k++ {
						if err := st(rr); err != nil {
							t.Fatalf("record %d: %v", k-1, err)
						}
						if cr.n != k*stride {
							t.Fatalf("after record %d the reader consumed %d physical bytes, want %d (%d x stride %d)",
								k-1, cr.n, k*stride, k, stride)
						}
					}
					if err := st(rr); err != io.EOF {
						t.Fatalf("after %d records: err = %v, want io.EOF", len(fx.rows), err)
					}
				})
			}
		}
	}
}

// TestStrideSites_EmptyPlanSkipsTheLogicalStride pins decode_plan.go's
// empty-retained-set fast path on a deduped schema. The E3-S3 story
// text says the empty plan is "a single SkipBytes covering the full
// reduced stride"; that is SUPERSEDED by E3-S2's design: a grouped
// schema's plan is the plan of its Logical() schema (plans walk the
// logical stream the RecordReader decodes), so the empty plan is ONE
// SkipBytes of Logical().RecordByteSize(). The reduced (physical)
// stride is what that skip costs underneath: the grouped record stream
// turns a whole-logical-row skip into a whole-physical-row advance
// without expanding the row — asserted here by the bytes consumed and
// by GroupIndex reporting the row was never expanded.
func TestStrideSites_EmptyPlanSkipsTheLogicalStride(t *testing.T) {
	for _, fx := range strideFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			flat := flatCohort(t, fx.schema, fx.rows)
			grouped, gs := groupedTwin(t, flat, fx.specs)
			_, region := recordRegion(t, grouped)

			for _, retained := range [][]string{nil, {}} {
				want := &DecodePlan{Segments: []Segment{SkipBytes{N: gs.Logical().RecordByteSize()}}}
				p, err := gs.BuildDecodePlan(retained)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(p, want) {
					t.Fatalf("BuildDecodePlan(%v) on the deduped schema = %+v, want one SkipBytes of the logical stride %d (physical %d)",
						retained, p.Segments, gs.Logical().RecordByteSize(), gs.RecordByteSize())
				}
				flatPlan, _ := fx.schema.BuildDecodePlan(retained)
				if !reflect.DeepEqual(p, flatPlan) {
					t.Fatalf("deduped empty plan %+v differs from the 0x01 twin's %+v", p.Segments, flatPlan.Segments)
				}
			}

			p, _ := gs.BuildDecodePlan(nil)
			cr := &countingReader{r: bytes.NewReader(region)}
			rr := NewRecordReader(cr, gs)
			rec := &dualRecord{indexedTestRecord: newIndexedTestRecord(gs.Logical())}
			keep := keepFromNames(nil)
			for k := 1; k <= len(fx.rows); k++ {
				if err := rr.ReadRecordReusedWithPlan(rec, keep, p); err != nil {
					t.Fatalf("record %d: %v", k-1, err)
				}
				if cr.n != k*gs.RecordByteSize() {
					t.Fatalf("empty plan: consumed %d physical bytes after %d records, want %d", cr.n, k, k*gs.RecordByteSize())
				}
				for g := range gs.Groups {
					if _, ok := rr.GroupIndex(g); ok {
						t.Fatalf("empty plan expanded record %d (group %d reports an index); a whole-row skip must not expand", k-1, g)
					}
				}
			}
		})
	}
}
