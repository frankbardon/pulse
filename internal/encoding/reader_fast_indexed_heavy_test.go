//go:build heavy

package encoding

import (
	"bytes"
	"fmt"
	"testing"
)

// TestReadRecordReused_IndexedKinds is the full-stride half of the table:
// for every record kind × corpus schema × record, ReadRecordReused must
// produce exactly the ReadRecordWithWide reference maps, through the
// index-keyed methods whenever the record implements them.
func TestReadRecordReused_IndexedKinds(t *testing.T) {
	t.Parallel()

	corpus := indexedDecodeCorpus(t)
	for _, kind := range recordKinds() {
		t.Run(kind.name, func(t *testing.T) {
			for _, e := range corpus {
				for ri, raw := range e.records {
					label := fmt.Sprintf("%s/%s/rec=%d", kind.name, e.name, ri)

					refRR := NewRecordReader(bytes.NewReader(raw), e.schema)
					wantVals := make(map[string]float64)
					wantNulls := make(map[string]bool)
					wantWide := make(map[string]any)
					if err := refRR.ReadRecordWithWide(wantVals, wantNulls, wantWide); err != nil {
						t.Fatalf("%s: reference: %v", label, err)
					}

					rec, snap := kind.build(t, e.schema)
					rr := NewRecordReader(bytes.NewReader(raw), e.schema)
					if err := rr.ReadRecordReused(rec); err != nil {
						t.Fatalf("%s: ReadRecordReused: %v", label, err)
					}
					assertKindDecode(t, label, kind, snap, wantVals, wantNulls, wantWide)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// TestReadRecordReusedWithPlan_IndexedKinds is the plan half: every kind
// × schema × retained set × record. The schema position handed to an
// index-keyed write must stay correct across SkipBytes segments, so the
// expected output is the full reference restricted to the retained set.
func TestReadRecordReusedWithPlan_IndexedKinds(t *testing.T) {
	t.Parallel()

	corpus := indexedDecodeCorpus(t)
	for _, kind := range recordKinds() {
		t.Run(kind.name, func(t *testing.T) {
			for _, e := range corpus {
				for si, retained := range e.retainedSets() {
					plan, err := BuildDecodePlan(e.schema, retained)
					if err != nil {
						t.Fatalf("%s: BuildDecodePlan: %v", e.name, err)
					}
					for ri, raw := range e.records {
						label := fmt.Sprintf("%s/%s/subset=%d/rec=%d", kind.name, e.name, si, ri)
						want := restrictReusedRecord(decodeFullReused(t, e.schema, raw, label), retained)

						rec, snap := kind.build(t, e.schema)
						rr := NewRecordReader(bytes.NewReader(raw), e.schema)
						if err := rr.ReadRecordReusedWithPlan(rec, keepFromNames(retained), plan); err != nil {
							t.Fatalf("%s: ReadRecordReusedWithPlan: %v", label, err)
						}
						assertKindDecode(t, label, kind, snap, want.values, want.nulls, want.wide)
						if t.Failed() {
							return
						}
					}
				}
			}
		})
	}
}
