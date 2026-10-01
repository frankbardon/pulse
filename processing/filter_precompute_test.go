package processing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// Filter precompute over parent-group dictionary entries (E5-S1).
//
// The fixture is a synthetic parent/child cohort and its deduped 0x02
// twin: an indexed group G0 (a key and one member of every filterable
// type — categorical, f64, date, packed_bool, set — the nullable ones
// null for every fifth parent), an indexed group G1 whose only two
// members are both nullable and BOTH null on one entry (a null parent
// tuple), a constant group G2, and two child row fields. Rows are
// scattered so no entry arrives as a run.

const precomputeParents = 40

func precomputeDict(vals ...string) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, v := range vals {
		_, _ = d.Add(v)
	}
	return d
}

func precomputeFlatSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "p_key", Type: encoding.FieldTypeU32},
		{Name: "p_cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: precomputeDict("a", "b", "c", "d"), Nullable: true},
		{Name: "p_num", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "p_date", Type: encoding.FieldTypeDate},
		{Name: "p_flag", Type: encoding.FieldTypePackedBool},
		{Name: "p_set", Type: encoding.FieldTypeSetU8, Dictionary: precomputeDict("x", "y", "z", "w"), Nullable: true},
		{Name: "q_cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: precomputeDict("m", "n", "o"), Nullable: true},
		{Name: "q_num", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "c_num", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "c_cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: precomputeDict("r", "s", "t")},
		{Name: "k_const", Type: encoding.FieldTypeCategoricalU8, Dictionary: precomputeDict("only")},
	}}
}

// precomputeTwins returns the flat cohort and its grouped twin, rows
// scattered by one fixed permutation, plus the row count.
func precomputeTwins(t testing.TB) (flat, grouped []byte, rows int) {
	t.Helper()
	s := precomputeFlatSchema()
	type row struct {
		vals  []uint64
		nulls []int
	}
	var all []row
	for p := range precomputeParents {
		for c := range 10 + p%5 {
			r := row{vals: make([]uint64, len(s.Fields))}
			null := func(i int) { r.nulls = append(r.nulls, i) }
			r.vals[0] = uint64(1000 + p)
			if p%5 == 0 {
				null(1)
				null(2)
				null(5)
			} else {
				r.vals[1] = uint64(p % 4)
				r.vals[2] = math.Float64bits(float64(p%9) + 0.5)
				r.vals[5] = uint64((p * 7) % 16)
			}
			r.vals[3] = uint64(18262 + (p*23)%366) // 2020-01-01 onward
			r.vals[4] = uint64(p % 2)
			if q := p % 7; q == 0 {
				null(6) // the null parent tuple: every G1 member null
				null(7)
			} else {
				r.vals[6] = uint64(q % 3)
				r.vals[7] = math.Float64bits(float64(q) * 1.5)
			}
			if (p+c)%6 == 0 {
				null(8)
			} else {
				r.vals[8] = math.Float64bits(float64((p*13+c*5)%11) + 0.25)
			}
			r.vals[9] = uint64((p + c) % 3)
			all = append(all, r)
		}
	}
	perm := rand.New(rand.NewPCG(0xF117E4, 0x5CA7)).Perm(len(all))
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, s); err != nil {
		t.Fatal(err)
	}
	for _, i := range perm {
		r := all[i]
		for fi := range s.Fields {
			if s.Fields[fi].Type.IsBitPacked() {
				buf.WriteByte(byte(r.vals[fi]))
				continue
			}
			if err := encoding.WriteFieldValue(&buf, s.Fields[fi].Type, r.vals[fi]); err != nil {
				t.Fatal(err)
			}
		}
		bm := make([]byte, s.BitmapByteSize())
		for _, fi := range r.nulls {
			encoding.BitmapSetNull(bm, fi)
		}
		buf.Write(bm)
	}
	flat = buf.Bytes()
	var out bytes.Buffer
	gs, n, err := encx.DedupCohort(&out, bytes.NewReader(flat), []encx.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: []string{"p_key", "p_cat", "p_num", "p_date", "p_flag", "p_set"}, Key: []string{"p_key"}},
		{Kind: encoding.GroupKindIndexed, Members: []string{"q_cat", "q_num"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"k_const"}},
	})
	if err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	if int(n) != len(all) || gs.GroupEntryCount(0) != precomputeParents || gs.GroupEntryCount(1) != 7 || gs.GroupEntryCount(2) != 1 {
		t.Fatalf("fixture: %d rows, entries %d/%d/%d", n, gs.GroupEntryCount(0), gs.GroupEntryCount(1), gs.GroupEntryCount(2))
	}
	return flat, out.Bytes(), len(all)
}

// decodeArm decodes every row of data through one reuse-decoder arm and
// calls fn with the record the arm yields.
type decodeArm struct {
	name string
	run  func(t *testing.T, data []byte, fn func(*Record))
}

func precomputeArms(retained []string) []decodeArm {
	open := func(t *testing.T, data []byte) (*encoding.Schema, *encx.RecordReader) {
		t.Helper()
		r := bytes.NewReader(data)
		s, _, err := encx.ReadPreamble(r)
		if err != nil {
			t.Fatal(err)
		}
		return s, encx.NewRecordReader(r, s)
	}
	loop := func(t *testing.T, read func() error) {
		t.Helper()
		for {
			err := read()
			if err == io.EOF {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return []decodeArm{
		{"reuse", func(t *testing.T, data []byte, fn func(*Record)) {
			s, rr := open(t, data)
			rec := NewReusableRecord(s)
			loop(t, func() error {
				if err := rr.ReadRecordReused(rec); err != nil {
					return err
				}
				fn(rec)
				return nil
			})
		}},
		{"buffered-plan", func(t *testing.T, data []byte, fn func(*Record)) {
			s, rr := open(t, data)
			keepSet := map[string]bool{}
			for _, n := range retained {
				keepSet[n] = true
			}
			keep := encx.FieldFilter(func(n string) bool { return keepSet[n] })
			plan, err := encx.BuildDecodePlan(s, retained)
			if err != nil {
				t.Fatal(err)
			}
			b := BindRecords(s, keep)
			loop(t, func() error {
				rec := b.NewRecord()
				if err := rr.ReadRecordReusedWithPlan(rec, keep, plan); err != nil {
					return err
				}
				fn(rec)
				return nil
			})
		}},
	}
}

func dateRangesParams(t testing.TB) json.RawMessage {
	t.Helper()
	s1, e1, s2, e2 := "2020-02-01", "2020-05-31", "2020-09-01", "2020-10-15"
	raw, err := json.Marshal(map[string]any{"ranges": []DateRangeSpec{
		{Label: "a", Start: &s1, End: &e1}, {Label: "b", Start: &s2, End: &e2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// precomputeFilterCase is one filterer and whether it must precompute.
type precomputeFilterCase struct {
	f        *types.Filterer
	eligible bool
	group    int // the group whose entries bound the evaluations
}

func precomputeCases(t testing.TB) []precomputeFilterCase {
	fl := func(ft types.FiltererType, field string, vals ...string) *types.Filterer {
		return &types.Filterer{Type: ft, Field: field, Values: vals}
	}
	ex := func(src string) *types.Filterer {
		return &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: src}
	}
	dr := fl(types.FILTER_DATE_RANGES, "p_date")
	dr.Params = dateRangesParams(t)
	return []precomputeFilterCase{
		{fl(types.FILTER_INCLUDE, "p_cat", "a", "c"), true, 0},
		{fl(types.FILTER_EXCLUDE, "p_cat", "b"), true, 0},
		{fl(types.FILTER_RANGE, "p_num", "2", "6"), true, 0},
		{dr, true, 0},
		{fl(types.FILTER_NULL, "p_num", "is_null"), true, 0},
		{fl(types.FILTER_NULL, "p_set", "is_not_null"), true, 0},
		{fl(types.FILTER_TRUE, "p_flag"), true, 0},
		{fl(types.FILTER_FALSE, "p_flag"), true, 0},
		{fl(types.FILTER_TRUE, "p_num", "truthy"), true, 0},
		{fl(types.FILTER_SET_CONTAINS_ANY, "p_set", "x"), true, 0},
		{fl(types.FILTER_SET_CONTAINS_ALL, "p_set", "x", "y"), true, 0},
		{fl(types.FILTER_SET_CONTAINS_NONE, "p_set", "z"), true, 0},
		{fl(types.FILTER_SET_EQUALS, "p_set", "y", "z"), true, 0},
		{ex(`p_key > 1011 && p_date < 18500`), true, 0},
		{ex(`p_key > 1030 || p_date > 18400`), true, 0},
		// the null parent tuple (G1 entry with every member null)
		{fl(types.FILTER_NULL, "q_num", "is_null"), true, 1},
		{fl(types.FILTER_EXCLUDE, "q_cat", "n"), true, 1},
		// a constant group: one entry decides the whole cohort
		{fl(types.FILTER_INCLUDE, "k_const", "only"), true, 2},
		// ineligible: a row field, a group member mixed with a row
		// field, two groups, an impure call
		{fl(types.FILTER_INCLUDE, "c_cat", "r"), false, -1},
		{ex(`p_key > 1010 && c_cat == "r"`), false, -1},
		{ex(`p_key > 1010 && k_const == "only"`), false, -1},
		{ex(`p_key > 1010 && now() != nil`), false, -1},
	}
}

// precomputeRetained is the projection a request filtering on chain and
// summing c_num decodes.
func precomputeRetained(chain []*types.Filterer, s *encoding.Schema) []string {
	needed := NeededFields(&types.Request{Filterers: chain}, s, nil)
	needed.Add("c_num")
	var out []string
	for i := range s.Fields {
		if needed.Has(s.Fields[i].Name) {
			out = append(out, s.Fields[i].Name)
		}
	}
	return out
}

func precomputeArm(name string, retained []string) decodeArm {
	for _, a := range precomputeArms(retained) {
		if a.name == name {
			return a
		}
	}
	panic(name)
}

// TestFilterPrecompute_ParityEveryFilterer: on every built-in filterer,
// over both reuse-decoder arms, the precomputed verdicts and the
// {n_in, n_out, n_null_input} counters are identical to per-row
// evaluation on the grouped cohort AND on its 0x01 twin; an eligible
// filter evaluates its predicate once per DISTINCT entry reached, an
// ineligible one once per row.
func TestFilterPrecompute_ParityEveryFilterer(t *testing.T) {
	flat, grouped, rows := precomputeTwins(t)
	gs, _, err := encx.ReadPreamble(bytes.NewReader(grouped))
	if err != nil {
		t.Fatal(err)
	}
	fs, _, err := encx.ReadPreamble(bytes.NewReader(flat))
	if err != nil {
		t.Fatal(err)
	}
	for _, armName := range []string{"reuse", "buffered-plan"} {
		for i, tc := range precomputeCases(t) {
			t.Run(fmt.Sprintf("%s/%d_%s", armName, i, tc.f.Type), func(t *testing.T) {
				chain := []*types.Filterer{tc.f}
				arm := precomputeArm(armName, precomputeRetained(chain, gs))
				// per-row reference on the flat twin
				ref, err := BuildFilters(chain, fs, nil)
				if err != nil {
					t.Fatal(err)
				}
				var want []bool
				wantC := NewFilterPassCounters(chain)
				arm.run(t, flat, func(r *Record) {
					ok, err := ApplyFilterPass(r, chain, ref, wantC)
					if err != nil {
						t.Fatal(err)
					}
					want = append(want, ok)
				})

				// precomputed on the grouped cohort, counting the
				// predicate evaluations behind the wrapper
				prev := SetFilterPrecompute(false)
				perRow, err := BuildFilters(chain, gs, nil)
				SetFilterPrecompute(prev)
				if err != nil {
					t.Fatal(err)
				}
				evals := 0
				rawInner := perRow[0]
				counted := FilterFunc(func(r *Record) (bool, error) { evals++; return rawInner(r) })
				wrapped := []FilterFunc{wrapFilterPrecompute(counted, tc.f, gs, nil)}
				before := FilterPrecomputeStats()
				var got []bool
				gotC := NewFilterPassCounters(chain)
				entries := map[uint32]bool{}
				arm.run(t, grouped, func(r *Record) {
					if tc.group >= 0 {
						e, ok := r.GroupIndex(tc.group)
						if !ok {
							t.Fatal("grouped reuse decode left the record without a group index")
						}
						entries[e] = true
					}
					ok, err := ApplyFilterPass(r, chain, wrapped, gotC)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, ok)
				})
				after := FilterPrecomputeStats()

				// per-row on the grouped cohort, precompute off
				var gotRow []bool
				rowC := NewFilterPassCounters(chain)
				arm.run(t, grouped, func(r *Record) {
					ok, err := ApplyFilterPass(r, chain, perRow, rowC)
					if err != nil {
						t.Fatal(err)
					}
					gotRow = append(gotRow, ok)
				})

				if len(want) != rows || len(got) != rows || len(gotRow) != rows {
					t.Fatalf("row counts %d/%d/%d, want %d", len(want), len(got), len(gotRow), rows)
				}
				for r := range want {
					if got[r] != want[r] || gotRow[r] != want[r] {
						t.Fatalf("row %d: precomputed %v, per-row grouped %v, flat %v", r, got[r], gotRow[r], want[r])
					}
				}
				wc := BuildFiltererComponents(chain, wantC)
				if gc := BuildFiltererComponents(chain, gotC); gc[0] != wc[0] {
					t.Fatalf("components: precomputed %+v, flat %+v", gc[0], wc[0])
				}
				if rc := BuildFiltererComponents(chain, rowC); rc[0] != wc[0] {
					t.Fatalf("components: per-row grouped %+v, flat %+v", rc[0], wc[0])
				}
				if tc.group != 2 && (wc[0].NOut == 0 || wc[0].NOut == rows) {
					t.Fatalf("filter selects %d of %d rows; the case proves nothing", wc[0].NOut, rows)
				}

				if tc.eligible {
					if evals != len(entries) {
						t.Fatalf("predicate evaluated %d times, want once per distinct entry (%d); %d rows", evals, len(entries), rows)
					}
					if d := after.EntryEvaluations - before.EntryEvaluations; d != int64(evals) {
						t.Fatalf("EntryEvaluations delta %d, want %d", d, evals)
					}
					// The ratio gate: at this fixture's 12x fanout a
					// parent-member filter does at least 10x less work.
					if tc.group == 0 && evals*10 > rows {
						t.Fatalf("%d evaluations over %d rows: less than a 10x reduction", evals, rows)
					}
				} else if evals != rows {
					t.Fatalf("ineligible filter evaluated %d times, want once per row (%d)", evals, rows)
				}
			})
		}
	}
}

// groupedRecords decodes every row of the grouped twin through the reuse
// decoder into fresh records (the buffered shape), for tests that need
// to hold rows.
func groupedRecords(t *testing.T) (*encoding.Schema, []*Record) {
	t.Helper()
	_, grouped, _ := precomputeTwins(t)
	r := bytes.NewReader(grouped)
	s, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	rr := encx.NewRecordReader(r, s)
	var out []*Record
	for {
		rec := NewReusableRecord(s)
		err := rr.ReadRecordReused(rec)
		if err == io.EOF {
			return s, out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
}

// TestFilterPrecompute_UngroupedUnaffected: over a schema with no groups
// the wrapper is not installed at all — BuildFilters returns the
// builder's FilterFunc itself and builds no table.
func TestFilterPrecompute_UngroupedUnaffected(t *testing.T) {
	flat, _, _ := precomputeTwins(t)
	fs, _, err := encx.ReadPreamble(bytes.NewReader(flat))
	if err != nil {
		t.Fatal(err)
	}
	f := &types.Filterer{Type: types.FILTER_INCLUDE, Field: "p_cat", Values: []string{"a"}}
	fn, err := (&includeFilterer{}).Build(f, fs)
	if err != nil {
		t.Fatal(err)
	}
	if got := wrapFilterPrecompute(fn, f, fs, nil); reflect.ValueOf(got).Pointer() != reflect.ValueOf(fn).Pointer() {
		t.Fatal("an ungrouped schema got the precompute wrapper")
	}
	before := FilterPrecomputeStats()
	fns, err := BuildFilters([]*types.Filterer{f}, fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewRecord(fs, map[string]float64{"p_cat": 0})
	if ok, _ := fns[0](rec); !ok {
		t.Fatal("verdict")
	}
	if FilterPrecomputeStats() != before {
		t.Fatal("an ungrouped schema touched the precompute counters")
	}
}

// TestFilterPrecompute_StaleIndexFallsBack: a record whose group member
// is rewritten after decode (a feature or attribute writing the member's
// name) stops reporting its entry, so the filter reads the rewritten
// value per row instead of the entry's verdict. So does a record handed
// another schema's indices.
func TestFilterPrecompute_StaleIndexFallsBack(t *testing.T) {
	s, recs := groupedRecords(t)
	f := &types.Filterer{Type: types.FILTER_INCLUDE, Field: "p_cat", Values: []string{"a"}}
	fns, err := BuildFilters([]*types.Filterer{f}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rec *Record
	for _, r := range recs {
		if v, ok := r.NumericValue("p_cat"); ok && v == 0 {
			rec = r
			break
		}
	}
	if rec == nil {
		t.Fatal("fixture: no row with p_cat = a")
	}
	if _, ok := rec.GroupIndex(0); !ok {
		t.Fatal("decoded record carries no group index")
	}
	if ok, _ := fns[0](rec); !ok {
		t.Fatal("row with p_cat = a rejected")
	}
	rec.Set("p_cat", 1) // "b"
	if _, ok := rec.GroupIndex(0); ok {
		t.Fatal("group index survived a member rewrite")
	}
	if ok, _ := fns[0](rec); ok {
		t.Fatal("filter answered the stale entry's verdict for a rewritten member")
	}

	other := *s
	r2 := NewReusableRecord(s)
	r2.SetGroupIndices(&other, []uint32{0, 0, 0})
	if _, ok := r2.GroupIndex(0); ok {
		t.Fatal("indices addressing another schema's dictionaries were accepted")
	}
	r2.SetGroupIndices(s, []uint32{3, 1, 0})
	if e, ok := r2.GroupIndex(0); !ok || e != 3 {
		t.Fatalf("GroupIndex(0) = %d, %v; want 3, true", e, ok)
	}
	r2.ClearForRow()
	if _, ok := r2.GroupIndex(0); ok {
		t.Fatal("group index survived ClearForRow")
	}
}

// TestFilterPrecompute_ErrorEntries: an entry whose evaluation errors is
// re-evaluated per row, so the error surfaces on exactly the row the
// per-row path raises it on — and, because entries are evaluated lazily,
// not at all when an earlier filter keeps every such row away.
func TestFilterPrecompute_ErrorEntries(t *testing.T) {
	s, recs := groupedRecords(t)
	// The expression returns a non-bool — a PROCESSING_RUNTIME error —
	// on every entry whose p_num is at most 3. (A null p_num no longer
	// errors: it binds nil and the filter drops the row, E5-S5.)
	bad := &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: `p_num > 3 ? true : "not a bool"`}
	firstErr := func(fns []FilterFunc) (int, []bool) {
		var verdicts []bool
		for i, r := range recs {
			ok, err := fns[0](r)
			if err != nil {
				return i, verdicts
			}
			verdicts = append(verdicts, ok)
		}
		return -1, verdicts
	}
	pre, err := BuildFilters([]*types.Filterer{bad}, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := SetFilterPrecompute(false)
	row, err := BuildFilters([]*types.Filterer{bad}, s, nil)
	SetFilterPrecompute(prev)
	if err != nil {
		t.Fatal(err)
	}
	pi, pv := firstErr(pre)
	ri, rv := firstErr(row)
	if ri < 0 || pi != ri || fmt.Sprint(pv) != fmt.Sprint(rv) {
		t.Fatalf("precomputed first error at row %d (%v), per-row at %d (%v)", pi, pv, ri, rv)
	}

	expr := &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "p_num > 3"}
	chain := []*types.Filterer{{Type: types.FILTER_NULL, Field: "p_num", Values: []string{"is_not_null"}}, expr}
	fns, err := BuildFilters(chain, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewFilterPassCounters(chain)
	reached := map[uint32]bool{} // entries with a non-null p_num
	all := map[uint32]bool{}
	before := FilterPrecomputeStats().EntryEvaluations
	for _, r := range recs {
		e, _ := r.GroupIndex(0)
		all[e] = true
		if !r.IsNull("p_num") {
			reached[e] = true
		}
		if _, err := ApplyFilterPass(r, chain, fns, c); err != nil {
			t.Fatalf("a guarded expression raised %v", err)
		}
	}
	// Lazy: slot 1 evaluates every entry, slot 2 only the entries that
	// get past slot 1 — an eager table would evaluate all of them twice.
	if got, want := FilterPrecomputeStats().EntryEvaluations-before, int64(len(all)+len(reached)); got != want || len(reached) == len(all) {
		t.Fatalf("entry evaluations %d, want %d (%d entries, %d reach slot 2)", got, want, len(all), len(reached))
	}
}

// TestExprPureInputs pins FILTER_EXPRESSION eligibility: identifiers
// are returned for a precompute; an impure or unknown call refuses it.
func TestExprPureInputs(t *testing.T) {
	rows := &ExtensionRegistry{LookupTables: map[string]LookupTable{"t": {Rows: map[string]float64{"a": 1}}}}
	callback := &ExtensionRegistry{LookupTables: map[string]LookupTable{"t": {Lookup: func(...string) (float64, bool, error) { return 0, false, nil }}}}
	fnReg := &ExtensionRegistry{ExprFunctions: []ExprFunction{{Name: "score", Fn: func(float64) float64 { return 0 }}, {Name: "abs", Fn: func(float64) float64 { return 0 }}}}
	for _, tc := range []struct {
		src  string
		exts *ExtensionRegistry
		want string // "" = ineligible
	}{
		{`a > 1 && b == "x"`, nil, "[a b]"},
		{`abs(a) > 1 || len(b) > 2`, nil, "[a b]"},
		{`has_any(s, ["x"])`, nil, "[s]"},
		{`all(s, {# > 1})`, nil, "[s]"},
		{`lookup("t", a) > 0`, rows, "[a]"},
		{`lookup("t", a) > 0`, callback, ""},
		{`lookup("t", a) > 0`, nil, ""},
		{`now() != nil && a > 1`, nil, ""},
		{`score(a) > 1`, fnReg, ""},
		{`abs(a) > 1`, fnReg, ""}, // an embedder function shadowing a built-in
		{`a.b() > 1`, nil, ""},
		{`true`, nil, ""},
		{`(`, nil, ""},
		{`let x = a; x > 1`, nil, "[a x]"}, // x resolves to no group member → per row
	} {
		got, ok := exprPureInputs(tc.src, tc.exts)
		g := ""
		if ok {
			g = fmt.Sprint(got)
		}
		if g != tc.want {
			t.Errorf("exprPureInputs(%q) = %q, want %q", tc.src, g, tc.want)
		}
	}
}

// TestRecord_GroupIndexWithdrawnByEveryMutator: every schema-field write
// that is not a reuse-decoder write withdraws the record's group index,
// exactly as it withdraws the record from run-skip (resetRun) — so a
// feature or attribute rewriting a group member can never be answered
// with the entry's precomputed verdict. Off-schema writes (an attribute
// label, a feature output column) keep it.
func TestRecord_GroupIndexWithdrawnByEveryMutator(t *testing.T) {
	s, recs := groupedRecords(t)
	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
		keeps  bool
	}{
		{"putValue", func(r *Record) { r.putValue("p_num", 3) }, false},
		{"dropValue", func(r *Record) { r.dropValue("p_num") }, false},
		{"markNull", func(r *Record) { r.markNull("p_cat") }, false},
		{"unmarkNull", func(r *Record) { r.unmarkNull("p_cat") }, false},
		{"putWide", func(r *Record) { r.putWide("p_set", uint64(1)) }, false},
		{"Set", func(r *Record) { r.Set("p_key", 1) }, false},
		{"SetNull", func(r *Record) { r.SetNull("p_date") }, false},
		{"SetWide", func(r *Record) { r.SetWide("p_set", uint64(2)) }, false},
		{"injectValue", func(r *Record) { r.injectValue("p_flag", 1) }, false},
		{"SetNumeric", func(r *Record) { r.SetNumeric("q_num", 1) }, false},
		{"SetNullField", func(r *Record) { r.SetNullField("q_cat") }, false},
		{"ClearForRow", func(r *Record) { r.ClearForRow() }, false},
		{"copyStateInto target", func(r *Record) {
			NewReusableRecord(s).copyStateInto(r, func(n string) string { return n }, 0, true)
		}, false},
		{"off-schema Set", func(r *Record) { r.Set("derived_label", 1) }, true},
		{"off-schema injectValue", func(r *Record) { r.injectValue("attr_label", 2) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := recs[len(recs)/2]
			// re-decode a pristine copy: records are shared across cases
			fresh := NewReusableRecord(s)
			idx := make([]uint32, len(s.Groups))
			for g := range idx {
				e, ok := rec.GroupIndex(g)
				if !ok {
					t.Fatal("decoded record carries no group index")
				}
				idx[g] = e
			}
			fresh.SetGroupIndices(s, idx)
			tc.mutate(fresh)
			if _, ok := fresh.GroupIndex(0); ok != tc.keeps {
				t.Fatalf("GroupIndex ok = %v after %s, want %v", ok, tc.name, tc.keeps)
			}
		})
	}
}
