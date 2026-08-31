package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// This file holds the two ADMISSION-RULE contracts that the numeric
// coverage in crosstab_fused_margin_agg_test.go and
// crosstab_buffered_margin_agg_test.go pins the answers to but does not
// itself defend:
//
//  1. the CONTRAST — every record the auxiliary rule excludes is still
//     present in the cell aggregator's own margin counts, read off
//     Response.Components.Crosstab on BOTH paths, so "the auxiliary
//     narrows the base" is a measured difference rather than a claim;
//  2. the NON-VACUITY CONTROL — the fixture genuinely DISCRIMINATES
//     between cell-admission and the wrong rules, so the shipped
//     assertions cannot go quietly vacuous if the fixture is edited.
//
// WHY A DEDICATED FILE AND WHY THE SEMANTIC NAMES.
//
// The admission rule ("a record contributes to an auxiliary margin only
// if it contributed to a CELL") is not an implementation detail that
// happens to have been written one way. It is the mechanism delivering
// two PRODUCT decisions, and each of the three exclusions in the shared
// fixture belongs to one of them:
//
//   - METRIC-SCOPING — a respondent with no value for the measured
//     metric is not in that metric's base. Fixture record: respondent 3
//     (alpha / f, metric null).
//
//   - THE INCLUDED-UNIVERSE RULE — a respondent who reaches no cell,
//     because their only records sit on axis keys the request excluded,
//     is not in the base either. Fixture records: respondent 2 (row key
//     `beta`, removed by the grouper Include) and respondent 4 (column
//     key null, so no column resolves).
//
// If either half is dropped — if an auxiliary is admitted on the slot's
// OWN axis alone, the way the cell aggregator's own margins are —
// NOTHING THROWS, NOTHING WARNS, AND EVERY NUMBER STILL RENDERS. The
// reported base merely becomes cohort-wide and metric-agnostic. No
// downstream test can see that: the metric fields this slot was built
// for are non-nullable in the live data, so the two rules agree there
// and only a fixture built to disagree can tell them apart. That is
// this file's whole job.
//
// A future reader looking at admitRecords (buffered) or
// updateAuxMargins (fused) and seeing needless complexity should read
// the two decisions above before simplifying either.
//
// MUTATION REGISTER. A control that cannot fail is worse than no control
// at all, so every assertion here was proved to redden against a
// deliberate breakage. Re-run these after editing this file or the
// shared fixture; each names the test that caught it.
//
//	1. buffered admitRecords: drop the cell-field null test
//	   → ...StayInTheCellsOwnMargins/buffered_auxiliary (base 2→3)
//	2. buffered admitRecords: drop both axis-membership tests
//	   → ...StayInTheCellsOwnMargins/buffered_auxiliary (grand base 2→4)
//	3. fused updateAuxMargins: drop the !cellValuePresent term
//	   → ...StayInTheCellsOwnMargins/fused_auxiliary (base 2→3)
//	4. fused updateAuxMargins: drop the colIdxBuf-empty term
//	   → ...StayInTheCellsOwnMargins/fused_auxiliary (row base 2→3)
//	5. fixture: give respondent 3 a metric value
//	   → ...FixtureDiscriminates... drift guard (cell-admission 2→3)
//	6. fixture: resolve every axis key (respondent 4 gains a gender,
//	   the Include is dropped)
//	   → ...FixtureDiscriminates... drift guard (cell-admission 2→4)
//	7. mutation 5 PLUS "helpfully" updating the pinned bases to match,
//	   which is what a careless maintainer does when a test reddens
//	   → ...FixtureDiscriminates.../no_metric-scoping reports zero
//	     discriminating slots. This is the arm the story's non-vacuity
//	     criterion is actually about, and mutations 5 and 6 do NOT
//	     reach it — they stop at the drift guard.
//	8. RunCrosstab: narrow the CELL's own grand margin to the admitted
//	   set, conflating the two behaviours
//	   → ...StayInTheCellsOwnMargins/buffered_cell_margins fires all
//	     three of its assertions, including the strictly-below contrast
//	     guard that stops this file going vacuous.

// auxAdmissionSlots names the three margin slots the fixture
// discriminates on, together with the figures each surface reports.
//
// The `want` numbers are MEASURED against the shared fixture, not
// derived: auxBase is what both paths' auxiliary accumulators hold (and
// what the E2-S2 / E2-S3 tests pin independently), while marginCount and
// marginN / marginNNull are what the cell aggregator's own margins have
// always reported and which this effort deliberately did not touch.
//
// The contrast is the point. At every slot auxBase is strictly smaller
// than BOTH the raw routed count and the universal floor's own n, so a
// rule that quietly widened to either would fail here.
type auxAdmissionSlot struct {
	name string
	// which semantic decision each excluded record belongs to
	excluded string
	// auxiliary distinct_count over `respondent`, admission-scoped
	auxBase float64
	// the cell aggregator's own margin, which is NOT admission-scoped
	marginCount int
	marginN     int
	marginNNull int
}

func auxAdmissionSlots() []auxAdmissionSlot {
	return []auxAdmissionSlot{
		{
			name: "grand",
			excluded: "respondent 2 (included-universe: row key excluded), " +
				"respondent 3 (metric-scoping: cell field null), " +
				"respondent 4 (included-universe: column key null)",
			auxBase: 2, marginCount: 5, marginN: 4, marginNNull: 1,
		},
		{
			name: "row[alpha]",
			excluded: "respondent 3 (metric-scoping: cell field null), " +
				"respondent 4 (included-universe: column key null)",
			auxBase: 2, marginCount: 4, marginN: 3, marginNNull: 1,
		},
		{
			name: "column[f]",
			excluded: "respondent 2 (included-universe: row key excluded by Include), " +
				"respondent 3 (metric-scoping: cell field null)",
			auxBase: 1, marginCount: 3, marginN: 2, marginNNull: 1,
		},
	}
}

// TestCrosstabAuxMargin_ExcludedRecordsStayInTheCellsOwnMargins is the
// contrast the story asks for, on both paths and at the surface a caller
// actually reads.
//
// Each of the three excluded records must be ABSENT from the auxiliary
// figure and STILL PRESENT in the cell aggregator's own margin — and the
// two must be demonstrably different numbers, or the auxiliary assertion
// proves nothing. Being present in the existing margin takes two forms
// and both are checked: the Include-excluded and null-column-key records
// ride the universal floor's `n`, while the null-cell-field record rides
// its `n_null`.
//
// It reads the existing margins off Response.Components.Crosstab rather
// than off either path's internal carrier, which is deliberate on two
// counts. It is the surface a caller sees, so this is the difference
// they would actually observe; and reading it through RunCrosstab /
// RunCrosstabFused end to end is what proves this effort left the cell
// aggregator's own margins alone on BOTH arms — the regression that would
// otherwise be indistinguishable from the auxiliary working.
func TestCrosstabAuxMargin_ExcludedRecordsStayInTheCellsOwnMargins(t *testing.T) {
	slots := auxAdmissionSlots()

	// The cell aggregator's own margins, read off the response. Asserted
	// per path so a divergence names which arm moved.
	for _, path := range []string{"buffered", "fused"} {
		t.Run(path+" cell margins are not admission-scoped", func(t *testing.T) {
			schema := auxMarginSchema(t)
			records := auxMarginRecords(schema)
			req := &types.Request{Crosstab: auxMarginSpec()}
			p := NewProcessor(schema)

			var resp *types.Response
			var err error
			if path == "buffered" {
				resp, err = p.RunCrosstab(t.Context(), req, records)
			} else {
				resp, err = p.RunCrosstabFused(t.Context(), req, NewSliceIterator(records))
			}
			if err != nil {
				t.Fatalf("%s crosstab: %v", path, err)
			}
			ct := resp.Components.Crosstab
			if ct == nil {
				t.Fatalf("%s crosstab emitted no components; the contrast cannot be read", path)
			}

			// Fixture guard: the response's axis order is what indexes
			// the margin vectors below.
			if got := resp.Crosstab.Matrix.RowKeys; len(got) != 1 {
				t.Fatalf("fixture drift: %d row keys, want 1 (alpha); got %v", len(got), got)
			}
			if got := resp.Crosstab.Matrix.ColumnKeys; len(got) != 2 {
				t.Fatalf("fixture drift: %d column keys, want 2 (f, m); got %v", len(got), got)
			}

			read := func(slot string) (count int, comps map[string]any) {
				switch slot {
				case "grand":
					return ct.GrandTotalCount, ct.GrandTotalComponents
				case "row[alpha]":
					return ct.RowMarginCounts[0], ct.RowMarginComponents[0]
				default:
					return ct.ColumnMarginCounts[0], ct.ColumnMarginComponents[0]
				}
			}

			for _, s := range slots {
				count, comps := read(s.name)
				if count != s.marginCount {
					t.Errorf("%s %s cell margin count = %d, want %d — this effort must not "+
						"narrow the cell aggregator's own margins; excluded here: %s",
						path, s.name, count, s.marginCount, s.excluded)
				}
				n := int(coerceFloat64(comps["n"]))
				nNull := int(coerceFloat64(comps["n_null"]))
				if n != s.marginN || nNull != s.marginNNull {
					t.Errorf("%s %s cell margin floor = {n:%d n_null:%d}, want {n:%d n_null:%d}",
						path, s.name, n, nNull, s.marginN, s.marginNNull)
				}
				// The contrast itself. Without this the auxiliary could
				// be reporting the floor and nobody would notice.
				if s.auxBase >= float64(n) {
					t.Errorf("%s %s: auxiliary base %v is not strictly below the cell margin's "+
						"own n=%d, so this slot no longer demonstrates that the auxiliary is "+
						"admission-scoped", path, s.name, s.auxBase, n)
				}
			}
		})
	}

	// The auxiliary side of the same three slots, read off each path's
	// own carrier because nothing is on the wire yet (E2-S5 widens
	// populateCrosstabComponents).
	t.Run("fused auxiliary is admission-scoped", func(t *testing.T) {
		state := driveAuxMargin(t, auxMarginSpec())
		got := map[string]float64{}
		v, _, _ := auxFinal(t, state.grandMarginAux, 0)
		got["grand"] = v
		v, _, _ = auxFinal(t, state.rowMarginAux[0], 0)
		got["row[alpha]"] = v
		colIdx, ok := state.colIndex["f"]
		if !ok {
			t.Fatalf("column key f not interned; columns = %v", state.colKeys)
		}
		v, _, _ = auxFinal(t, state.colMarginAux[colIdx], 0)
		got["column[f]"] = v
		assertAuxBases(t, "fused", slots, got)
	})

	t.Run("buffered auxiliary is admission-scoped", func(t *testing.T) {
		aux := driveBufferedAuxMargin(t, auxMarginSpec())
		got := map[string]float64{}
		v, _, _ := bufferedAuxFinal(t, aux.Grand, 0)
		got["grand"] = v
		v, _, _ = bufferedAuxFinal(t, aux.Rows["alpha"], 0)
		got["row[alpha]"] = v
		v, _, _ = bufferedAuxFinal(t, aux.Cols["f"], 0)
		got["column[f]"] = v
		assertAuxBases(t, "buffered", slots, got)
	})
}

// assertAuxBases compares one path's auxiliary bases against the slot
// table, quoting the excluded records so a failure says which semantic
// decision was lost rather than only which number moved.
func assertAuxBases(t *testing.T, path string, slots []auxAdmissionSlot, got map[string]float64) {
	t.Helper()
	for _, s := range slots {
		if got[s.name] != s.auxBase {
			t.Errorf("%s %s auxiliary base = %v, want %v — a LARGER figure means the rule "+
				"admitted a record it must exclude: %s",
				path, s.name, got[s.name], s.auxBase, s.excluded)
		}
	}
}

// TestCrosstabAuxMargin_FixtureDiscriminatesTheAdmissionRule is the
// non-vacuity control, and it guards the FIXTURE rather than the
// implementation.
//
// Every admission assertion in this package is a claim about a number
// the shared fixture produces. That claim is only worth something while
// the fixture is built so the RIGHT rule and the WRONG rules give
// DIFFERENT answers. A fixture on which they agree — which is exactly
// what the live Visa data looks like, since the metric fields are
// non-nullable there — proves nothing at all, and it would still pass
// every existing test.
//
// So this test applies each rule to the fixture ITSELF and asserts they
// disagree. It deliberately does NOT call the auxiliary implementation:
// it re-derives each record's three admission facts from the real axis
// partitions and the real null probe, then counts what each rule would
// admit. That keeps it an independent statement about the fixture, so it
// cannot be satisfied by the very code it is protecting.
//
// The wrong rules are the three that would arise from a plausible
// "simplification":
//
//   - AXIS-ONLY — admit whatever the slot's own axis routed, which is
//     precisely how the cell aggregator's own margins behave and
//     therefore the most likely wrong turn. Loses BOTH decisions.
//   - NO METRIC-SCOPING — keep the axis cross-check, drop the cell-field
//     null test.
//   - NO INCLUDED-UNIVERSE — keep the cell-field null test, drop the
//     other axis's membership cross-check.
//
// Each is required to differ from cell-admission on at least one slot,
// and the two single-decision rules are checked separately so the
// fixture cannot lose one half of its discriminating power while the
// other half masks it.
func TestCrosstabAuxMargin_FixtureDiscriminatesTheAdmissionRule(t *testing.T) {
	spec := auxMarginSpec()
	schema := auxMarginSchema(t)
	records := auxMarginRecords(schema)

	p := NewProcessor(schema)
	rowPart, err := p.PartitionByAxis(spec.Rows, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(rows): %v", err)
	}
	colPart, err := p.PartitionByAxis(spec.Columns, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(columns): %v", err)
	}

	rowMember := axisMembership(rowPart)
	colMember := axisMembership(colPart)
	cellField := spec.Cell.Field

	// Each record's three admission facts, read from the real
	// partitioning and the real null probe.
	rowOK := func(r *Record) bool { _, ok := rowMember[r]; return ok }
	colOK := func(r *Record) bool { _, ok := colMember[r]; return ok }
	cellOK := func(r *Record) bool { _, ok := r.NumericValue(cellField); return ok }

	// The auxiliary in slot 0 is AGG_DISTINCT_COUNT over `respondent`,
	// so its figure is the count of DISTINCT respondents admitted. The
	// fixture gives every record its own respondent id, which is what
	// makes a plain record count the right arithmetic here — asserted
	// rather than assumed, since an edit that reused an id would make
	// every count below silently wrong.
	seen := map[float64]bool{}
	for _, r := range records {
		id, ok := r.NumericValue("respondent")
		if !ok {
			t.Fatalf("fixture drift: a record carries no respondent id")
		}
		if seen[id] {
			t.Fatalf("fixture drift: respondent id %v appears twice; the counts in this "+
				"control assume one record per respondent", id)
		}
		seen[id] = true
	}

	// bucket returns the records a slot's own axis routed to it, which
	// is the set every rule starts from and narrows differently.
	type slotDef struct {
		name string
		// records routed to this slot by its own axis
		bucket []*Record
		// the OTHER axis's membership test this slot cross-checks
		other func(*Record) bool
	}
	slots := []slotDef{
		{name: "grand", bucket: records, other: nil},
		{name: "row[alpha]", bucket: rowPart.Records["alpha"], other: colOK},
		{name: "column[f]", bucket: colPart.Records["f"], other: rowOK},
	}
	for _, s := range slots {
		if len(s.bucket) == 0 {
			t.Fatalf("fixture drift: slot %s routes no record at all", s.name)
		}
	}

	// The grand slot cross-checks BOTH axes; the per-axis slots walk
	// their own axis and cross-check the other one.
	otherAxes := func(s slotDef) func(*Record) bool {
		if s.name == "grand" {
			return func(r *Record) bool { return rowOK(r) && colOK(r) }
		}
		return s.other
	}

	count := func(s slotDef, admit func(*Record) bool) int {
		n := 0
		for _, r := range s.bucket {
			if admit(r) {
				n++
			}
		}
		return n
	}

	rules := []struct {
		name string
		// what a reader loses if the implementation drifts to this rule
		loses string
		admit func(s slotDef) func(*Record) bool
	}{
		{
			name:  "axis-only admission",
			loses: "BOTH metric-scoping and the included-universe rule; the base becomes cohort-wide",
			admit: func(slotDef) func(*Record) bool {
				return func(*Record) bool { return true }
			},
		},
		{
			name:  "no metric-scoping",
			loses: "metric-scoping; respondents with no value for the measured metric join the base",
			admit: func(s slotDef) func(*Record) bool {
				other := otherAxes(s)
				return func(r *Record) bool { return other == nil || other(r) }
			},
		},
		{
			name:  "no included-universe",
			loses: "the included-universe rule; respondents who reach no cell join the base",
			admit: func(slotDef) func(*Record) bool {
				return cellOK
			},
		},
	}

	// Cell-admission, the shipped rule, derived the same way.
	cellAdmission := func(s slotDef) func(*Record) bool {
		other := otherAxes(s)
		return func(r *Record) bool {
			if other != nil && !other(r) {
				return false
			}
			return cellOK(r)
		}
	}

	// The control is only meaningful if this independent derivation
	// reproduces the figures the shipped tests pin. If it does not, the
	// fixture moved and every count below describes something else.
	want := map[string]int{"grand": 2, "row[alpha]": 2, "column[f]": 1}
	for _, s := range slots {
		if got := count(s, cellAdmission(s)); got != want[s.name] {
			t.Fatalf("fixture drift: cell-admission over %s admits %d records, but the "+
				"auxiliary tests pin its base at %d; this control no longer describes the fixture",
				s.name, got, want[s.name])
		}
	}

	for _, rule := range rules {
		t.Run(rule.name, func(t *testing.T) {
			differs := []string{}
			for _, s := range slots {
				right := count(s, cellAdmission(s))
				wrong := count(s, rule.admit(s))
				if wrong != right {
					differs = append(differs, s.name)
					t.Logf("%s: cell-admission %d, %s %d", s.name, right, rule.name, wrong)
				}
			}
			if len(differs) == 0 {
				t.Errorf("the fixture does NOT distinguish cell-admission from %q on any "+
					"margin slot, so every admission assertion in this package is vacuous "+
					"against that rule. Implementing it would lose %s, and no test would "+
					"fail. Restore a record that discriminates.", rule.name, rule.loses)
			}
		})
	}
}
