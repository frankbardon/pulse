package service

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/processing"
)

// TestGroupedDecode_PositionalRecordParity drives the PRODUCTION record
// (processing.Record: run-skip ownership, typed decimal / set storage,
// projected bindings) through the grouped reuse decoder — one run-skip
// record across the stream, and a fresh bound record per row — and
// checks every accessor against the 0x01 twin decoded with run-skip off,
// on the parent-width fixture (decimal, nullable and set members; the
// whole parent block one group), sorted and scattered, full and
// projected.
func TestGroupedDecode_PositionalRecordParity(t *testing.T) {
	for _, w := range []int{10, 40} {
		fs, gs, regions := groupWidthTwins(t, w, 60)
		rows := joinShapeRows(60)
		for _, order := range []string{"sorted", "scattered"} {
			for _, shape := range []string{"full", "proj4"} {
				t.Run(fmt.Sprintf("w%d/%s/%s", w, order, shape), func(t *testing.T) {
					var keep encx.FieldFilter
					var fplan, gplan *encx.DecodePlan
					if shape == "proj4" {
						keep = groupWidthKeep4
						fplan = baselinePlan(t, fs, keep)
						gplan = baselinePlan(t, gs, keep)
					}
					rf := encx.NewRecordReader(bytes.NewReader(regions["v1/"+order]), fs)
					rg := encx.NewRecordReader(bytes.NewReader(regions["v2/"+order]), gs)
					rb := encx.NewRecordReader(bytes.NewReader(regions["v2/"+order]), gs)
					binding := recordBindingFor(gs, gplan, keep)
					want := newNoSkipRecord(fs)
					got := processing.NewReusableRecord(gs)
					for k := range rows {
						if err := want.read(rf, keep, fplan); err != nil {
							t.Fatal(err)
						}
						if err := rg.ReadRecordReusedWithPlan(got, keep, gplan); err != nil {
							t.Fatalf("row %d: %v", k, err)
						}
						assertSameRecord(t, fmt.Sprintf("run-skip row %d", k), fs, got, want.record())
						fresh := binding.NewRecord()
						if err := rb.ReadRecordReusedWithPlan(fresh, keep, gplan); err != nil {
							t.Fatalf("buffered row %d: %v", k, err)
						}
						if shape == "full" {
							assertSameRecord(t, fmt.Sprintf("buffered row %d", k), fs, fresh, want.record())
						} else {
							for _, f := range fs.Fields {
								if !keep(f.Name) {
									continue
								}
								gv, gok := fresh.NumericValue(f.Name)
								wv, wok := want.record().NumericValue(f.Name)
								gw, _ := fresh.WideValue(f.Name)
								ww, _ := want.record().WideValue(f.Name)
								if gv != wv || gok != wok || fresh.IsNull(f.Name) != want.record().IsNull(f.Name) || !reflect.DeepEqual(gw, ww) {
									t.Fatalf("buffered row %d field %s: (%v,%v,%v,%v) want (%v,%v,%v,%v)", k, f.Name,
										gv, gok, fresh.IsNull(f.Name), gw, wv, wok, want.record().IsNull(f.Name), ww)
								}
							}
						}
					}
				})
			}
		}
	}
}
