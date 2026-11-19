package processing

import (
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// attribute_previous_state_sorted_test.go pins ATTR_PREVIOUS_STATE's
// sorted-input path (sortedRuns + computeRuns) against the bucket-and-
// sort path (computeBucketed): the check's verdict per input order, and
// a property test that both paths — and every arrangement of the same
// rows — give bitwise the same outputs, tallies and warning.

// psSortedSchema: three subject columns (u32 sid, u64 uid, decimal128
// did), order d (date), state st (categorical {A, B, C}); all nullable.
func psSortedSchema(t testing.TB) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, l := range []string{"A", "B", "C"} {
		if _, err := dict.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "sid", Type: encoding.FieldTypeU32, Nullable: true},
		{Name: "uid", Type: encoding.FieldTypeU64, Nullable: true},
		{Name: "did", Type: encoding.FieldTypeDecimal128, Precision: 38, Scale: 0, Nullable: true},
		{Name: "d", Type: encoding.FieldTypeDate, Nullable: true},
		{Name: "st", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Nullable: true},
	}}
}

// psRow is one panel row: subject, order and state, -1 for null.
type psRow struct{ subj, order, state int }

// psSubjectBase puts the u64 and decimal128 subjects past 2^53, where
// neighbouring values share one float64 echo: subjects 0 and 1 are one
// echo, so only the exact word / mantissa keeps them apart.
const psSubjectBase = 1 << 53

// psSortedRecord builds row r's record; every subject column carries the
// same subject (sid as itself, uid and did offset by psSubjectBase).
func psSortedRecord(t testing.TB, s *encoding.Schema, r psRow) *Record {
	t.Helper()
	vals, nulls := map[string]float64{}, map[string]bool{}
	set := func(name string, v int) {
		if v < 0 {
			nulls[name] = true
			return
		}
		vals[name] = float64(v)
	}
	set("d", r.order)
	set("st", r.state)
	set("sid", r.subj)
	if r.subj < 0 {
		nulls["uid"], nulls["did"] = true, true
		return NewRecordWithNulls(s, vals, nulls)
	}
	word := uint64(psSubjectBase + r.subj)
	vals["uid"] = float64(word)
	d, _, err := encoding.ParseDecimal128(strconv.FormatUint(word, 10))
	if err != nil {
		t.Fatal(err)
	}
	vals["did"] = d.Float64(0)
	rec := NewRecordWithWide(s, vals, nulls, map[string]any{"did": d})
	rec.SetExactU64("uid", word)
	return rec
}

// psAttr builds the slot over subject column subj.
func psAttr(t testing.TB, s *encoding.Schema, subj string) *previousStateAttribute {
	t.Helper()
	comp, err := newPreviousStateAttribute(&types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: "st", SubjectField: subj, OrderField: "d", Label: "prev"}, s)
	if err != nil {
		t.Fatal(err)
	}
	return comp.(*previousStateAttribute)
}

// TestPreviousState_SortedCheck: the check's verdict. A subject's keyed
// records must be contiguous and non-decreasing in order (ties are
// fine); a record with a null subject or order breaks no run.
func TestPreviousState_SortedCheck(t *testing.T) {
	t.Parallel()

	s := psSortedSchema(t)
	for _, c := range []struct {
		name   string
		rows   []psRow
		sorted bool
		runs   int
	}{
		{"empty", nil, true, 0},
		{"one_subject_in_order", []psRow{{1, 1, 0}, {1, 2, 1}, {1, 3, 2}}, true, 1},
		{"ties_in_a_run", []psRow{{1, 1, 0}, {1, 1, 1}, {1, 2, 2}}, true, 1},
		{"subjects_in_any_key_order", []psRow{{3, 1, 0}, {3, 2, 1}, {1, 1, 0}, {2, 5, 1}}, true, 3},
		{"null_subject_inside_a_run", []psRow{{1, 1, 0}, {-1, 0, 1}, {1, 2, 2}}, true, 1},
		{"null_order_inside_a_run", []psRow{{1, 2, 0}, {1, -1, 1}, {1, 3, 2}}, true, 1},
		{"null_order_out_of_place_is_skipped", []psRow{{1, 5, 0}, {1, -1, 1}, {1, 6, 2}, {2, 1, 0}}, true, 2},
		{"order_decreases", []psRow{{1, 2, 0}, {1, 1, 1}}, false, 0},
		{"subject_comes_back", []psRow{{1, 1, 0}, {2, 1, 1}, {1, 2, 2}}, false, 0},
		{"subject_comes_back_after_nulls", []psRow{{1, 1, 0}, {-1, 1, 1}, {2, 1, 1}, {1, -1, 2}, {1, 2, 2}}, false, 0},
		// u64 / decimal128 subjects 0 and 1 share a float64 echo: two runs,
		// never one run of two subjects.
		{"shared_float_echo_is_two_subjects", []psRow{{0, 1, 0}, {0, 2, 1}, {1, 1, 2}}, true, 2},
		{"shared_float_echo_comes_back", []psRow{{0, 1, 0}, {1, 1, 1}, {0, 2, 2}}, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			recs := make([]*Record, len(c.rows))
			for i, r := range c.rows {
				recs[i] = psSortedRecord(t, s, r)
			}
			for _, subj := range []string{"sid", "uid", "did"} {
				_, starts, ok := psAttr(t, s, subj).sortedRuns(recs)
				if ok != c.sorted || len(starts) != c.runs {
					t.Errorf("%s: sorted %v with %d runs, want %v with %d", subj, ok, len(starts), c.sorted, c.runs)
				}
			}
		})
	}
}

// psFixture draws a random long panel: subjects with several records
// each, orders over a narrow range (so ties are common, some with
// differing states), null states, and records with a null subject or
// order. It returns the rows sorted by (subject, order) — ties in draw
// order — which is the sorted arrangement.
func psFixture(rng *rand.Rand) []psRow {
	var rows []psRow
	nSubj := 1 + rng.Intn(12)
	for s := 0; s < nSubj; s++ {
		for k := rng.Intn(8); k > 0; k-- {
			r := psRow{subj: s, order: rng.Intn(5), state: rng.Intn(3)}
			switch rng.Intn(10) {
			case 0:
				r.state = -1
			case 1:
				r.order = -1
			case 2:
				r.subj = -1
			}
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].subj != rows[j].subj {
			return rows[i].subj < rows[j].subj
		}
		return rows[i].order < rows[j].order
	})
	return rows
}

// psOutcome is one Compute's whole observable answer.
type psOutcome struct {
	bits       []uint64 // outputs by original row, as bits
	components map[string]any
	warnings   []*types.ResponseWarning
}

// psRun computes rows (arranged by perm: position i holds row perm[i])
// on the path asked and returns the answer by original row.
func psRun(t *testing.T, s *encoding.Schema, subj string, rows []psRow, perm []int, sortedPath bool) (psOutcome, bool) {
	t.Helper()
	recs := make([]*Record, len(rows))
	for i, p := range perm {
		recs[i] = psSortedRecord(t, s, rows[p])
	}
	a := psAttr(t, s, subj)
	var out []float64
	tookSorted := false
	if sortedPath {
		var err error
		if _, _, tookSorted = a.sortedRuns(recs); !tookSorted {
			return psOutcome{}, false
		}
		if out, err = a.Compute(recs, "st"); err != nil {
			t.Fatal(err)
		}
		if !a.sortedPath {
			t.Fatal("Compute took the bucket-and-sort path on sorted input")
		}
	} else {
		out = a.reset(len(recs))
		a.computeBucketed(recs, out)
	}
	comps, err := a.Components()
	if err != nil {
		t.Fatal(err)
	}
	o := psOutcome{bits: make([]uint64, len(rows)), components: comps, warnings: a.attributeWarnings()}
	for i, p := range perm {
		o.bits[p] = math.Float64bits(out[i])
	}
	return o, tookSorted
}

// TestPreviousState_SortedPathMatchesSlowPath is the property test: over
// random panels (ties with differing and identical states, null states,
// null subjects and orders) and every subject type (u32, u64 and
// decimal128 past 2^53), Compute on the sorted arrangement takes the
// sorted path and answers — outputs bit for bit by original row,
// Components tallies and the AMBIGUOUS_TIE warning — exactly as the
// bucket-and-sort path does on the sorted arrangement, on a shuffled
// one, and on one with the null-keyed records moved to the front.
func TestPreviousState_SortedPathMatchesSlowPath(t *testing.T) {
	t.Parallel()

	s := psSortedSchema(t)
	rng := rand.New(rand.NewSource(485))
	var ambiguous, shuffledSlow int
	for iter := 0; iter < 200; iter++ {
		rows := psFixture(rng)
		identity := make([]int, len(rows))
		for i := range identity {
			identity[i] = i
		}
		shuffled := rng.Perm(len(rows))
		nullsFirst := make([]int, 0, len(rows))
		for _, keyed := range []bool{false, true} {
			for i, r := range rows {
				if (r.subj >= 0 && r.order >= 0) == keyed {
					nullsFirst = append(nullsFirst, i)
				}
			}
		}
		for _, subj := range []string{"sid", "uid", "did"} {
			want, took := psRun(t, s, subj, rows, identity, true)
			if !took {
				t.Fatalf("iter %d %s: the sorted arrangement did not take the sorted path: %v", iter, subj, rows)
			}
			if want.components["n_ambiguous"].(int64) > 0 {
				ambiguous++
			}
			for name, perm := range map[string][]int{"sorted": identity, "shuffled": shuffled, "nulls_first": nullsFirst} {
				got, _ := psRun(t, s, subj, rows, perm, false)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("iter %d %s %s: bucketed path\n%+v\nsorted path\n%+v\nrows %v", iter, subj, name, got, want, rows)
				}
				if name != "shuffled" {
					continue
				}
				recs := make([]*Record, len(rows))
				for i, p := range perm {
					recs[i] = psSortedRecord(t, s, rows[p])
				}
				_, _, ok := psAttr(t, s, subj).sortedRuns(recs)
				if !ok {
					shuffledSlow++
				}
				// Compute itself, on whichever path the check picks.
				auto := psAttr(t, s, subj)
				out, err := auto.Compute(recs, "st")
				if err != nil {
					t.Fatal(err)
				}
				if auto.sortedPath != ok {
					t.Fatalf("iter %d %s shuffled: Compute sorted path %v, check %v", iter, subj, auto.sortedPath, ok)
				}
				for i, p := range perm {
					if math.Float64bits(out[i]) != want.bits[p] {
						t.Fatalf("iter %d %s shuffled Compute: row %d = %v", iter, subj, p, out[i])
					}
				}
			}
		}
	}
	// The fixtures exercise what they claim to.
	if ambiguous < 200 || shuffledSlow < 450 {
		t.Fatalf("weak fixtures: %d ambiguous-tie runs, %d shuffled runs on the slow path", ambiguous, shuffledSlow)
	}
}
