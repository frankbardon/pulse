//go:build heavy

package encoding

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// TestGroupedDecode_CacheModesMatchFlat: in every cache mode (the whole
// dictionary cached, decimals only and direct-mapped, no cache at all),
// sorted and scattered, full and projected, the grouped reuse decode —
// a fresh record per row, and one run-skip record across the stream with
// the backoff both off and tripping every few rows — leaves exactly the
// state the 0x01 twin's decode leaves.
func TestGroupedDecode_CacheModesMatchFlat(t *testing.T) {
	for _, budget := range []struct {
		name string
		b    int
	}{{"whole", 8 << 20}, {"decimals_direct_mapped", 400}, {"none", 0}} {
		for _, fx := range groupFixtures(t) {
			for _, order := range []string{"sorted", "scattered"} {
				rows := fx.rows
				if order == "scattered" {
					rows = scatterRows(rows, 0x5CA7)
				}
				t.Run(fmt.Sprintf("%s/%s/%s", budget.name, fx.name, order), func(t *testing.T) {
					setCacheBudget(t, budget.b)
					fs, gs, flatRegion, physRegion := twinRegions(t, fx, rows)
					shapes := append([][]string{nil}, fx.subsets...)
					for si, retained := range shapes {
						var keep FieldFilter
						var fplan, gplan *DecodePlan
						if retained != nil {
							keep = keepFromNames(retained)
							fplan, _ = BuildDecodePlan(fs, retained)
							gplan, _ = BuildDecodePlan(gs, retained)
						}
						label := fmt.Sprintf("shape %d %v", si, retained)

						// Fresh record per row (the buffered path).
						rf := NewRecordReader(bytes.NewReader(flatRegion), fs)
						rg := NewRecordReader(bytes.NewReader(physRegion), gs)
						for k := range rows {
							want := &dualRecord{indexedTestRecord: newIndexedTestRecord(fs)}
							got := &dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}
							if err := rf.ReadRecordReusedWithPlan(want, keep, fplan); err != nil {
								t.Fatal(err)
							}
							if err := rg.ReadRecordReusedWithPlan(got, keep, gplan); err != nil {
								t.Fatalf("%s fresh row %d: %v", label, k, err)
							}
							if !reflect.DeepEqual(
								[]any{want.values, want.nulls, want.wide},
								[]any{got.values, got.nulls, got.wide}) {
								t.Fatalf("%s fresh row %d:\n flat   =%v %v %v\n grouped=%v %v %v", label, k,
									want.values, want.nulls, want.wide, got.values, got.nulls, got.wide)
							}
						}
						if err := rg.ReadRecordReusedWithPlan(&dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}, keep, gplan); err != io.EOF {
							t.Fatalf("%s: after the last row err = %v, want io.EOF", label, err)
						}
						if fx.name == "big_mixed" && retained == nil {
							// The mode each budget is meant to reach, for group 0
							// (nine members, one decimal) — else the arm is vacuous.
							c := rg.gd.full.groups[0].cache
							ok := map[string]bool{
								"whole":                  c.all && c.slots == gs.GroupEntryCount(0),
								"decimals_direct_mapped": !c.all && c.slots > 0 && c.slots < gs.GroupEntryCount(0),
								"none":                   c.slots == 0,
							}[budget.name]
							if !ok {
								t.Fatalf("budget %s: group 0 cache all=%v slots=%d of %d entries", budget.name, c.all, c.slots, gs.GroupEntryCount(0))
							}
						}

						// One run-skip record across the stream.
						for _, bo := range []struct {
							name           string
							probe, backoff int
						}{{"compare", 32, 0}, {"backoff", 1, 3}} {
							setBackoff(t, bo.probe, bo.backoff)
							rec := newRunSkipTestRecord(gs)
							rg := NewRecordReader(bytes.NewReader(physRegion), gs)
							for k, row := range rows {
								if err := rg.ReadRecordReusedWithPlan(rec, keep, gplan); err != nil {
									t.Fatalf("%s %s row %d: %v", label, bo.name, k, err)
								}
								assertSameState(t, fmt.Sprintf("%s %s row %d", label, bo.name, k), rec, referenceDecode(t, fs, row, keep, fplan))
							}
							if rec.keptRows != len(rows)-1 {
								t.Fatalf("%s %s: kept %d rows, want %d (groups ignore the backoff)", label, bo.name, rec.keptRows, len(rows)-1)
							}
						}
					}
				})
			}
		}
	}
}
