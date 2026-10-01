package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Grouped reuse decode (group_decode.go): the reuse decoders read a
// 0x02 physical row directly — no expansion — skip a group whose index
// did not change, and populate a changed group from a per-entry cache.
// These tests pin (1) that the output is the 0x01 twin's under every
// cache mode, run-skip backoff state and decode shape, and (2) the WORK:
// deterministic counts of member populations, never wall clock.

// setCacheBudget overrides the entry-cache budget for one test.
func setCacheBudget(t *testing.T, b int) {
	t.Helper()
	old := groupEntryCacheBudget
	groupEntryCacheBudget = b
	t.Cleanup(func() { groupEntryCacheBudget = old })
}

// scatterRows returns rows in a deterministic permutation: parents no
// longer run, so nearly every row changes every group's index.
func scatterRows(rows [][]byte, seed int64) [][]byte {
	out := make([][]byte, len(rows))
	for i, p := range rand.New(rand.NewSource(seed)).Perm(len(rows)) {
		out[i] = rows[p]
	}
	return out
}

// twinRegions builds the flat and grouped twins of rows.
func twinRegions(t *testing.T, fx groupFixture, rows [][]byte) (fs, gs *encoding.Schema, flatRegion, physRegion []byte) {
	t.Helper()
	flat := flatCohort(t, fx.schema, rows)
	grouped, gs := groupedTwin(t, flat, fx.specs)
	fs, flatRegion = recordRegion(t, flat)
	_, physRegion = recordRegion(t, grouped)
	return fs, gs, flatRegion, physRegion
}

// groupIndexStream returns, per group, the entry index of every physical
// row (always 0 for a constant group).
func groupIndexStream(gs *encoding.Schema, phys []byte) [][]uint32 {
	stride := gs.RecordByteSize()
	out := make([][]uint32, len(gs.Groups))
	for g := range gs.Groups {
		off := gs.GroupIndexOffset(g)
		for r := 0; r+stride <= len(phys); r += stride {
			var e uint32
			if off >= 0 {
				e = binary.LittleEndian.Uint32(phys[r+off:])
			}
			out[g] = append(out[g], e)
		}
	}
	return out
}

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

// memberWriteSink wraps a run-skip record and counts writes that land on
// group MEMBER fields, per row.
type memberWriteSink struct {
	*runSkipTestRecord
	member []bool
	row    int
}

func (m *memberWriteSink) note(idx int) {
	if m.member[idx] {
		m.row++
	}
}
func (m *memberWriteSink) SetNumericAt(idx int, v float64) {
	m.note(idx)
	m.runSkipTestRecord.SetNumericAt(idx, v)
}
func (m *memberWriteSink) SetNullFieldAt(idx int) {
	m.note(idx)
	m.runSkipTestRecord.SetNullFieldAt(idx)
}
func (m *memberWriteSink) SetWideFieldAt(idx int, v any) {
	m.note(idx)
	m.runSkipTestRecord.SetWideFieldAt(idx, v)
}
func (m *memberWriteSink) ClearNullAt(idx int) { m.note(idx); m.runSkipTestRecord.ClearNullAt(idx) }

// TestGroupedDecode_MemberWriteCounts is the deterministic work gate for
// width-independent grouped decode (counts, not wall clock):
//
//   - run-skip record: a group is populated exactly on the rows where its
//     index changed, so member value writes == Σ_g changes_g × members_g.
//     On the SORTED fixture that is a small fraction of rows × members
//     (≈ members / fanout per row); on the SCATTERED one it is at most
//     changes × members. A row whose indices all repeat takes NO write
//     on any member field — no byte compare, no expansion.
//   - fresh record per row (buffered): every row writes every member,
//     but each dictionary entry is DECODED once (cache fills == distinct
//     entries touched), never once per row.
func TestGroupedDecode_MemberWriteCounts(t *testing.T) {
	setBackoff(t, 32, 1024) // production backoff: groups must ignore it
	fx := groupFixtures(t)[0]
	for _, order := range []string{"sorted", "scattered"} {
		t.Run(order, func(t *testing.T) {
			rows := fx.rows
			if order == "scattered" {
				rows = scatterRows(rows, 0x5CA7)
			}
			_, gs, _, phys := twinRegions(t, fx, rows)
			idx := groupIndexStream(gs, phys)
			member := make([]bool, len(gs.Fields))
			totalMembers := 0
			wantWrites, changedRows := int64(0), make([]bool, len(rows))
			distinct := 0
			for g := range gs.Groups {
				for _, m := range gs.Groups[g].Members {
					member[m.Field] = true
				}
				n := len(gs.Groups[g].Members)
				totalMembers += n
				seen := map[uint32]bool{}
				for r, e := range idx[g] {
					seen[e] = true
					if r == 0 || e != idx[g][r-1] {
						wantWrites += int64(n)
						changedRows[r] = true
					}
				}
				distinct += len(seen)
			}

			// Run-skip record through one reader.
			rec := &memberWriteSink{runSkipTestRecord: newRunSkipTestRecord(gs), member: member}
			rr := NewRecordReader(bytes.NewReader(phys), gs)
			for r := range rows {
				rec.row = 0
				if err := rr.ReadRecordReused(rec); err != nil {
					t.Fatal(err)
				}
				if !changedRows[r] && rec.row != 0 {
					t.Fatalf("row %d: every group index repeats, yet %d member-field writes", r, rec.row)
				}
			}
			st := rr.gd.stats
			if st.memberWrites != wantWrites {
				t.Fatalf("member value writes = %d, want Σ changes × members = %d", st.memberWrites, wantWrites)
			}
			perRow := float64(st.memberWrites) / float64(len(rows))
			t.Logf("%s: %d rows x %d members; member writes %d (%.2f/row), group skips %d, cache fills %d",
				order, len(rows), totalMembers, st.memberWrites, perRow, st.groupSkips, st.cacheFills)
			if order == "sorted" && perRow > float64(totalMembers)/4 {
				t.Fatalf("sorted: %.2f member writes per row, want <= %d/4: unchanged groups are being repopulated", perRow, totalMembers)
			}
			if st.memberWrites > int64(len(rows)*totalMembers) {
				t.Fatalf("member writes %d exceed rows x members %d", st.memberWrites, len(rows)*totalMembers)
			}
			if st.cacheFills > int64(distinct) {
				t.Fatalf("cache fills %d exceed the %d distinct entries: an entry was decoded twice", st.cacheFills, distinct)
			}

			// Fresh record per row: every member written every row, each
			// entry decoded once.
			rr = NewRecordReader(bytes.NewReader(phys), gs)
			for range rows {
				if err := rr.ReadRecordReused(&dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}); err != nil {
					t.Fatal(err)
				}
			}
			st = rr.gd.stats
			if st.memberWrites != int64(len(rows)*totalMembers) {
				t.Fatalf("fresh records: member writes %d, want rows x members %d", st.memberWrites, len(rows)*totalMembers)
			}
			if st.cacheFills != int64(distinct) || st.decimalDecodes != 0 {
				t.Fatalf("fresh records: %d cache fills and %d uncached decimal decodes, want %d fills (one per distinct entry) and 0",
					st.cacheFills, st.decimalDecodes, distinct)
			}
		})
	}
}

// TestGroupedDecode_ProjectionSkipsUnretainedGroups: a plan that retains
// no member of a group never populates it (no member write, no cache
// fill); retaining one member populates only its group.
func TestGroupedDecode_ProjectionSkipsUnretainedGroups(t *testing.T) {
	fx := groupFixtures(t)[0]
	fs, gs, flatRegion, phys := twinRegions(t, fx, fx.rows)
	for _, tc := range []struct {
		retained        []string
		wantPopulations int // populated groups per changed row
	}{
		{[]string{"u8_b", "u16_c"}, 0},   // row fields only
		{[]string{"amount", "u32_b"}, 1}, // one member of group 0
	} {
		gplan, _ := BuildDecodePlan(gs, tc.retained)
		fplan, _ := BuildDecodePlan(fs, tc.retained)
		keep := keepFromNames(tc.retained)
		rr := NewRecordReader(bytes.NewReader(phys), gs)
		rf := NewRecordReader(bytes.NewReader(flatRegion), fs)
		rec := newRunSkipTestRecord(gs)
		want := newRunSkipTestRecord(fs)
		for k := range fx.rows {
			if err := rr.ReadRecordReusedWithPlan(rec, keep, gplan); err != nil {
				t.Fatal(err)
			}
			if err := rf.ReadRecordReusedWithPlan(want, keep, fplan); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]any{want.values, want.nulls, want.wide}, []any{rec.values, rec.nulls, rec.wide}) {
				t.Fatalf("%v row %d differs from the flat twin", tc.retained, k)
			}
		}
		st := rr.gd.stats
		switch tc.wantPopulations {
		case 0:
			if st.populations != 0 || st.memberWrites != 0 || st.cacheFills != 0 {
				t.Fatalf("%v: unretained groups touched: %+v", tc.retained, st)
			}
		default:
			if st.memberWrites != st.populations || st.populations == 0 || st.populations > int64(gs.GroupEntryCount(0))*2 {
				t.Fatalf("%v: want only group 0's one member populated per change, got %+v", tc.retained, st)
			}
		}
	}

	// An unretained group's index is still bounds-checked, exactly as the
	// row expansion always checked it.
	s := &encoding.Schema{
		Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8}, {Name: "c", Type: encoding.FieldTypeU8}},
		Groups: []encoding.Group{{Members: []encoding.GroupMember{{Field: 0}}, Entries: []byte{7}}},
	}
	plan, _ := BuildDecodePlan(s, []string{"c"})
	rr := NewRecordReader(bytes.NewReader([]byte{1, 0, 0, 0, 3}), s)
	err := rr.ReadRecordReusedWithPlan(&dualRecord{indexedTestRecord: newIndexedTestRecord(s)}, keepFromNames([]string{"c"}), plan)
	if !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("projected read of an out-of-range index: err = %v, want ENCODING_INVALID", err)
	}
}

// TestGroupedDecode_InterleavedPaths: the map decoder (logical stream)
// and the grouped reuse decoder share one reader and one position, a
// plain record between two run-skip rows does not disturb the run-skip
// baseline, and GroupIndex reports the row whichever path decoded it.
func TestGroupedDecode_InterleavedPaths(t *testing.T) {
	fx := groupFixtures(t)[0]
	fs, gs, _, phys := twinRegions(t, fx, fx.rows)
	idx := groupIndexStream(gs, phys)
	rr := NewRecordReader(bytes.NewReader(phys), gs)
	rs := newRunSkipTestRecord(gs)
	for k, row := range fx.rows {
		switch k % 3 {
		case 0:
			if err := rr.ReadRecordReused(rs); err != nil {
				t.Fatal(err)
			}
			assertSameState(t, fmt.Sprintf("run-skip row %d", k), rs, referenceDecode(t, fs, row, nil, nil))
		case 1:
			v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
			if err := rr.ReadRecordWithWide(v, n, w); err != nil {
				t.Fatal(err)
			}
			ref := referenceDecode(t, fs, row, nil, nil)
			if !reflect.DeepEqual([]any{ref.values, ref.nulls, ref.wide}, []any{v, n, w}) {
				t.Fatalf("map row %d differs", k)
			}
		case 2:
			plain := &dualRecord{indexedTestRecord: newIndexedTestRecord(gs)}
			if err := rr.ReadRecordReused(plain); err != nil {
				t.Fatal(err)
			}
			ref := referenceDecode(t, fs, row, nil, nil)
			if !reflect.DeepEqual([]any{ref.values, ref.nulls, ref.wide}, []any{plain.values, plain.nulls, plain.wide}) {
				t.Fatalf("plain row %d differs", k)
			}
		}
		for g := range gs.Groups {
			if e, ok := rr.GroupIndex(g); !ok || e != idx[g][k] {
				t.Fatalf("row %d: GroupIndex(%d) = %d,%v, want %d", k, g, e, ok, idx[g][k])
			}
		}
	}
	if rs.keptRows == 0 {
		t.Fatal("the run-skip record never kept a row across the interleaved reads")
	}
}

// TestGroupedDecode_RunSkipRowFields: E2 run-skip still governs the ROW
// fields of a grouped cohort. Every row is stored twice in a row; with
// comparison on, the repeat takes no record write at all (row fields
// byte-compared, groups index-compared), and the record still equals a
// full decode.
func TestGroupedDecode_RunSkipRowFields(t *testing.T) {
	setBackoff(t, 32, 0)
	for _, fx := range groupFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			var rows [][]byte
			for _, r := range fx.rows {
				rows = append(rows, r, r)
			}
			fs, gs, _, phys := twinRegions(t, fx, rows)
			rec := newRunSkipTestRecord(gs)
			rr := NewRecordReader(bytes.NewReader(phys), gs)
			for k, row := range rows {
				before := rec.writes()
				if err := rr.ReadRecordReused(rec); err != nil {
					t.Fatal(err)
				}
				if k%2 == 1 && rec.writes() != before {
					t.Fatalf("row %d repeats row %d byte for byte, yet took %d writes", k, k-1, rec.writes()-before)
				}
				assertSameState(t, fmt.Sprintf("row %d", k), rec, referenceDecode(t, fs, row, nil, nil))
			}
		})
	}
}
