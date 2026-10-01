package encoding

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// gidxRecorder records the SetGroupIndices calls the grouped reuse
// decoder makes.
type gidxRecorder struct {
	src   *encoding.Schema
	idx   []uint32
	calls int
}

func (s *gidxRecorder) SetGroupIndices(schema *encoding.Schema, idx []uint32) {
	s.calls++
	s.src = schema
	s.idx = append(s.idx[:0], idx...)
	if idx == nil {
		s.idx = nil
	}
}

// plainGroupIndexSink is an index-keyed record with no run-skip, so the
// decoder's ClearForRow path runs; runSkipGroupIndexSink exercises the
// kept-row path.
type plainGroupIndexSink struct {
	*dualRecord
	gidxRecorder
}

type runSkipGroupIndexSink struct {
	*runSkipTestRecord
	gidxRecorder
}

// TestGroupedDecode_ReportsGroupIndices (E5-S1): after every grouped
// reuse decode — full and plan, plain sink and run-skip sink — a
// GroupIndexRecord is handed the row's entry per group, addressed by the
// GROUPED schema (whose dictionaries the entries index, not the
// reader's Logical view); a plan that decodes nothing hands it nil.
func TestGroupedDecode_ReportsGroupIndices(t *testing.T) {
	for _, fx := range groupFixtures(t) {
		rows := scatterRows(fx.rows, 0x1D)
		_, gs, _, phys := twinRegions(t, fx, rows)
		want := groupIndexStream(gs, phys)
		plans := map[string][]string{"full": nil, "none": {}}
		for i, sub := range fx.subsets {
			plans[fmt.Sprint("subset", i)] = sub
		}
		for pname, retained := range plans {
			for _, runSkip := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/runskip=%v", fx.name, pname, runSkip), func(t *testing.T) {
					rr := NewRecordReader(bytes.NewReader(phys), gs)
					var rec ReusableRecord
					var gi *gidxRecorder
					if runSkip {
						r := &runSkipGroupIndexSink{runSkipTestRecord: newRunSkipTestRecord(gs)}
						rec, gi = r, &r.gidxRecorder
					} else {
						r := &plainGroupIndexSink{dualRecord: &dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}}
						rec, gi = r, &r.gidxRecorder
					}
					if _, ok := rec.(RunSkipRecord); ok != runSkip {
						t.Fatalf("sink run-skip = %v, want %v", ok, runSkip)
					}
					if _, ok := rec.(GroupIndexRecord); !ok {
						t.Fatal("test sink does not implement GroupIndexRecord")
					}
					var plan *DecodePlan
					var keep FieldFilter
					if retained != nil {
						p, err := BuildDecodePlan(gs, retained)
						if err != nil {
							t.Fatal(err)
						}
						set := map[string]bool{}
						for _, n := range retained {
							set[n] = true
						}
						plan, keep = p, func(n string) bool { return set[n] }
					}
					for r := 0; r <= len(rows); r++ {
						var err error
						if plan != nil {
							err = rr.ReadRecordReusedWithPlan(rec, keep, plan)
						} else {
							err = rr.ReadRecordReused(rec)
						}
						if err == io.EOF || (pname == "none" && r == len(rows)) {
							// (a plan that decodes nothing seeks, and a
							// seek past the end is not an EOF)
							if r != len(rows) {
								t.Fatalf("decoded %d rows, want %d", r, len(rows))
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						if gi.calls != r+1 || gi.src != gs {
							t.Fatalf("row %d: %d SetGroupIndices calls, schema %p (grouped %p)", r, gi.calls, gi.src, gs)
						}
						if pname == "none" {
							if gi.idx != nil {
								t.Fatalf("row %d: a plan that decodes nothing reported indices %v", r, gi.idx)
							}
							continue
						}
						for g := range gs.Groups {
							if gi.idx[g] != want[g][r] {
								t.Fatalf("row %d group %d: reported entry %d, want %d", r, g, gi.idx[g], want[g][r])
							}
						}
					}
				})
			}
		}
	}
}

// TestGroupEntryDecoder_MatchesDecodeGroupEntry: the compiled per-entry
// decoder writes, for the members it was built over, exactly what
// DecodeGroupEntry writes for them — on every entry of every group,
// nulls, decimals and sets included — and nothing else.
func TestGroupEntryDecoder_MatchesDecodeGroupEntry(t *testing.T) {
	for _, fx := range groupFixtures(t) {
		_, gs, _, _ := twinRegions(t, fx, fx.rows)
		for g := range gs.Groups {
			var fields []int
			for k, m := range gs.Groups[g].Members {
				if k%2 == 0 || gs.Fields[m.Field].Nullable {
					fields = append(fields, m.Field)
				}
			}
			dec, err := NewGroupEntryDecoder(gs, g, fields)
			if err != nil {
				t.Fatal(err)
			}
			for e := range gs.GroupEntryCount(g) {
				full, sub := newIndexedTestRecord(gs), newIndexedTestRecord(gs)
				if err := DecodeGroupEntry(gs, g, e, full); err != nil {
					t.Fatal(err)
				}
				if err := dec.Decode(e, sub); err != nil {
					t.Fatal(err)
				}
				for _, fi := range fields {
					n := gs.Fields[fi].Name
					if full.values[n] != sub.values[n] || full.nulls[n] != sub.nulls[n] || !reflect.DeepEqual(full.wide[n], sub.wide[n]) {
						t.Fatalf("%s group %d entry %d field %s: decoder %v/%v/%v, DecodeGroupEntry %v/%v/%v",
							fx.name, g, e, n, sub.values[n], sub.nulls[n], sub.wide[n], full.values[n], full.nulls[n], full.wide[n])
					}
				}
				if len(sub.values) != len(fields) {
					t.Fatalf("decoder wrote %d fields, want %d", len(sub.values), len(fields))
				}
			}
			if err := dec.Decode(gs.GroupEntryCount(g), newIndexedTestRecord(gs)); err == nil {
				t.Fatal("out-of-range entry decoded")
			}
		}
		if _, err := NewGroupEntryDecoder(gs, 0, []int{-1}); err == nil {
			t.Fatal("a non-member field compiled")
		}
	}
}
