package synth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// depFixture builds a cohort carrying, simultaneously:
//
//   - score (u4, 0..10) and the THREE-BAND PARTITION derived from it —
//     promoter / passive / detractor — all four null on exactly the same
//     rows. The motivating case, and the one that must come back as ONE
//     candidate rather than three.
//   - band edges chosen as 9 / 7 so the test can assert they were
//     DISCOVERED; a detector that hardcodes the standard NPS definition
//     would agree with this fixture by accident, which is why
//     TestSuggestDeps_BandEdgesAreDiscoveredNotAssumed uses a SECOND
//     fixture with non-standard edges.
//   - near: promoter's mapping with a handful of contradicting rows.
//     Reported with its exception count, never emitted.
//   - loose: promoter's mapping with far more contradictions than
//     maxDepExceptions. Neither emitted NOR reported — it is not
//     "almost" anything.
//   - region (categorical) and west, a boolean membership dependency on
//     it, so the categorical arm is exercised beside the numeric one.
//   - unshared: an EXACT function of score whose null pattern differs
//     from score's. Reported, never emitted — a set_expr would un-null
//     it on the rows score is absent from.
//   - flat: a constant packed_bool, and fixednum: a constant u4.
//     Determined by every field and by none. BOTH are needed: a constant
//     BOOLEAN target is refused three ways over (the band guard, the
//     empty true-set and the empty false-set), so it cannot test the
//     guard at all — found while falsifying, when the guard could be
//     weakened with every test still green. A constant NUMERIC target
//     reaches the ternary renderer, which happily emits a bare literal
//     with no condition, and only the band guard stops it.
//   - noise: an ordinary field determined by nothing — the negative
//     control, and it is deliberately NOT threshold-independent: its
//     values are a function of the row index that repeats at a different
//     period from every source, so it contradicts each of them many
//     times over.
//
// Every value and every null decision is an exact integer function of
// the row index, so the fixture bytes are architecture-independent by
// construction rather than by a float barrier (synth/moments.go).
func depDict(values []string) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			panic(err)
		}
	}
	return d
}

func depFixtureSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "promoter", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "passive", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "detractor", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "near", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "loose", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true,
			Dictionary: depDict([]string{"east", "north", "south", "west"})},
		{Name: "west", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "unshared", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "flat", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "fixednum", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "noise", Type: encoding.FieldTypeU4, Nullable: true},
	}}
}

func depFixtureRows(n int) ([]map[string]any, []map[string]bool) {
	regions := []string{"east", "north", "south", "west"}
	rows := make([]map[string]any, 0, n)
	nulls := make([]map[string]bool, 0, n)
	for i := 0; i < n; i++ {
		score := i % 11
		region := regions[i%4]
		b := func(v bool) float64 {
			if v {
				return 1
			}
			return 0
		}
		row := map[string]any{
			"score":     float64(score),
			"promoter":  b(score >= 9),
			"passive":   b(score == 7 || score == 8),
			"detractor": b(score <= 6),
			"near":      b(score >= 9),
			"loose":     b(score >= 9),
			"region":    region,
			"west":      b(region == "west"),
			"unshared":  b(score >= 9),
			"flat":      0.0,
			"fixednum":  3.0,
			"noise":     float64(i % 13),
		}
		rowNull := map[string]bool{}
		// The four-field NPS block: null together, one row in five.
		if i%5 == 0 {
			for _, f := range []string{"score", "promoter", "passive", "detractor", "near", "loose"} {
				rowNull[f] = true
			}
		}
		// `near` contradicts promoter on a handful of rows.
		if i%97 == 3 && i/97 < 4 && !rowNull["near"] {
			row["near"] = b(score < 9)
		}
		// `loose` contradicts it far more often than maxDepExceptions.
		if i%7 == 3 && !rowNull["loose"] {
			row["loose"] = b(score < 9)
		}
		// `unshared` is an exact function of score and is null on a
		// DIFFERENT set of rows.
		if i%5 == 1 {
			rowNull["unshared"] = true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	return rows, nulls
}

func depFixtureProfile(t *testing.T, n int) *Profile {
	t.Helper()
	schema := depFixtureSchema()
	rows, nulls := depFixtureRows(n)
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

// depCandidates returns the exact-dependency candidates only.
func depCandidates(cands []RuleSpec) []RuleSpec {
	var out []RuleSpec
	for _, c := range cands {
		if c.Evidence != nil && c.Evidence.Detector == dependencyDetectorName {
			out = append(out, c)
		}
	}
	return out
}

func depCandidateFor(cands []RuleSpec, source string) *RuleSpec {
	for i := range cands {
		if cands[i].Evidence != nil && cands[i].Evidence.SourceField == source {
			return &cands[i]
		}
	}
	return nil
}

// TestSuggestDeps_PartitionIsOneCandidateWithDiscoveredEdges is
// acceptance criteria 1 and 3 in ONE test, because each is trivially
// satisfiable by abandoning the other: a detector emitting one rule per
// (source, target) satisfies "the dependency is found" and fails the
// partition criterion, and a detector that groups everything into one
// rule regardless satisfies the partition criterion while proposing
// relationships that do not exist.
//
// It is the named falsification target of this story. Remove the
// group-by-source assembly in candidateFor and the partition comes back
// as three rules that must be kept consistent by hand.
func TestSuggestDeps_PartitionIsOneCandidateWithDiscoveredEdges(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	cands := depCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatalf("no dependency candidates; warnings = %v", prof.Warnings)
	}

	c := depCandidateFor(cands, "score")
	if c == nil {
		t.Fatalf("no candidate sourced on score; sources = %v", depSources(cands))
	}

	// ONE candidate covering all three members, not three.
	want := []string{"detractor", "passive", "promoter"}
	got := sortedKeys(c.SetExpr)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidate set_expr targets = %v, want %v (the partition must be ONE rule)", got, want)
	}
	for _, other := range cands {
		if other.Evidence.SourceField == "score" {
			continue
		}
		for _, name := range sortedKeys(other.SetExpr) {
			if name == "promoter" || name == "passive" || name == "detractor" {
				t.Errorf("partition member %q is also written by a second candidate sourced on %q",
					name, other.Evidence.SourceField)
			}
		}
	}

	// THE EDGES ARE DISCOVERED. The fixture's own generating rule is
	// score >= 9 / 7..8 / <= 6, and these are the expressions read off
	// the data.
	for field, want := range map[string]string{
		"promoter":  "round(score) >= 9",
		"passive":   "round(score) >= 7 && round(score) <= 8",
		"detractor": "round(score) <= 6",
	} {
		if got := c.SetExpr[field]; got != want {
			t.Errorf("set_expr[%q] = %q, want %q", field, got, want)
		}
	}

	// The three E2-S4 remedies, each measured wrong before it was
	// measured right.
	for field, src := range c.SetExpr {
		if !strings.Contains(src, "round(") {
			t.Errorf("set_expr[%q] = %q does not go through round(); a bare comparison reads the "+
				"pre-rounding float and not the value the file holds", field, src)
		}
	}
	if c.When != "" {
		t.Errorf("dependency candidate carries when = %q; an unconditional rule is what makes the "+
			"targets pre-claim-eligible", c.When)
	}
	if len(c.NullTogether) == 0 {
		t.Fatalf("the candidate carries no null_together; a set_expr clears the null mask, so the block " +
			"must ride THIS rule where it is the last write")
	}
	if c.NullTogether[0] != "score" {
		t.Errorf("null_together[0] = %q, want the SOURCE first — applyNullTogether copies the first "+
			"member's decision", c.NullTogether[0])
	}
	if strings.Join(c.NullTogether, ",") != "score,promoter,passive,detractor" {
		t.Errorf("null_together = %v, want the source then the targets in schema order", c.NullTogether)
	}
}

func depSources(cands []RuleSpec) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Evidence.SourceField)
	}
	sort.Strings(out)
	return out
}

// TestSuggestDeps_BandEdgesAreDiscoveredNotAssumed is the half the
// motivating fixture cannot test, because its edges ARE the standard NPS
// definition and a hardcoded detector would agree with it by accident.
// Here the same shape carries edges at 3 and 6.
func TestSuggestDeps_BandEdgesAreDiscoveredNotAssumed(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeU4},
		{Name: "hi", Type: encoding.FieldTypePackedBool},
		{Name: "mid", Type: encoding.FieldTypePackedBool},
	}}
	var rows []map[string]any
	for i := 0; i < 220; i++ {
		s := i % 11
		b := func(v bool) float64 {
			if v {
				return 1
			}
			return 0
		}
		rows = append(rows, map[string]any{
			"score": float64(s), "hi": b(s >= 6), "mid": b(s >= 3 && s <= 5),
		})
	}
	data := encodeModelRows(t, schema, rows, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	c := depCandidateFor(depCandidates(prof.RuleCandidates), "score")
	if c == nil {
		t.Fatalf("no candidate sourced on score; warnings = %v", prof.Warnings)
	}
	if got, want := c.SetExpr["hi"], "round(score) >= 6"; got != want {
		t.Errorf("set_expr[hi] = %q, want %q — the edge must come from the data, not from the standard "+
			"NPS definition", got, want)
	}
	if got, want := c.SetExpr["mid"], "round(score) >= 3 && round(score) <= 5"; got != want {
		t.Errorf("set_expr[mid] = %q, want %q", got, want)
	}
	// Nothing is ever null here, so the identical-null-pattern admission
	// holds vacuously and no null_together is emitted: a block over two
	// never-null fields would be noise that copies a decision nobody
	// made.
	if len(c.NullTogether) != 0 {
		t.Errorf("never-null fields got a null_together %v; there is no null decision to share", c.NullTogether)
	}
}

// TestSuggestDeps_AlmostDeterminedIsReportedNotEmitted is acceptance
// criterion 2, with BOTH sides of the maxDepExceptions bound in one
// test: `near` contradicts on a handful of rows and is reported with its
// count; `loose` contradicts far more often and is neither emitted nor
// reported, because it is not "almost" anything.
func TestSuggestDeps_AlmostDeterminedIsReportedNotEmitted(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	for _, c := range depCandidates(prof.RuleCandidates) {
		for _, name := range sortedKeys(c.SetExpr) {
			if name == "near" {
				t.Errorf("an ALMOST-determined target was emitted as exact: %q from %q",
					name, c.Evidence.SourceField)
			}
			if name == "loose" {
				t.Errorf("a target contradicting its source on many rows was emitted: %q", name)
			}
		}
	}
	joined := strings.Join(prof.Warnings, "\n")
	if !strings.Contains(joined, `"near" is an exact function of "score" on all but `) {
		t.Errorf("the almost-determined pair was not reported with its exception count; warnings = %v",
			prof.Warnings)
	}
	if strings.Contains(joined, `"loose" is an exact function`) {
		t.Errorf("a pair contradicting on more than %d rows was reported as almost-determined; "+
			"over the bound it is not almost anything", maxDepExceptions)
	}
	for _, w := range prof.Warnings {
		if strings.Contains(w, `"near" is an exact function`) {
			kind, attention := classifyWarning(w)
			if kind != "rule candidate not considered" || !attention {
				t.Errorf("the almost-determined report classified as %q / attention=%v", kind, attention)
			}
		}
	}
}

// TestSuggestDeps_NullShapeMismatchIsReportedNotEmitted pins the second
// refusal: `unshared` IS an exact function of score and still cannot be
// written, because a set_expr clears the null mask and the two are not
// absent on the same rows.
func TestSuggestDeps_NullShapeMismatchIsReportedNotEmitted(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	for _, c := range depCandidates(prof.RuleCandidates) {
		if _, ok := c.SetExpr["unshared"]; ok {
			t.Errorf("a target whose null pattern differs from its source was emitted: set_expr = %v "+
				"null_together = %v", c.SetExpr, c.NullTogether)
		}
	}
	joined := strings.Join(prof.Warnings, "\n")
	if !strings.Contains(joined, `"unshared" is an exact function of "score"`) ||
		!strings.Contains(joined, "do not share a null pattern") {
		t.Errorf("the unexpressible exact dependency was not reported; warnings = %v", prof.Warnings)
	}
}

// TestSuggestDeps_CategoricalSourceYieldsMembership exercises the
// categorical arm: a boolean determined by a categorical is an equality
// set, not a threshold.
func TestSuggestDeps_CategoricalSourceYieldsMembership(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	c := depCandidateFor(depCandidates(prof.RuleCandidates), "region")
	if c == nil {
		t.Fatalf("no candidate sourced on region; sources = %v warnings = %v",
			depSources(depCandidates(prof.RuleCandidates)), prof.Warnings)
	}
	if got, want := c.SetExpr["west"], `region == "west"`; got != want {
		t.Errorf("set_expr[west] = %q, want %q", got, want)
	}
	var form string
	for _, d := range c.Evidence.Dependency {
		if d.Field == "west" {
			form = d.Form
		}
	}
	if form != depFormMembership {
		t.Errorf("form = %q, want %q", form, depFormMembership)
	}
}

// TestSuggestDeps_ConstantColumnIsItsOwnFinding: a single-valued column
// is determined by every other field and by none of them. It is excluded
// from both roles and named once rather than proposed 117 times.
func TestSuggestDeps_ConstantColumnIsItsOwnFinding(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	for _, c := range depCandidates(prof.RuleCandidates) {
		for _, name := range []string{"flat", "fixednum"} {
			if src, ok := c.SetExpr[name]; ok {
				t.Errorf("the constant column %q was proposed as a derived field (source %q, expr %q)",
					name, c.Evidence.SourceField, src)
			}
			if c.Evidence.SourceField == name {
				t.Errorf("the constant column %q was proposed as a dependency SOURCE", name)
			}
		}
	}
	joined := strings.Join(prof.Warnings, "\n")
	for _, name := range []string{"flat", "fixednum"} {
		if !strings.Contains(joined, `column "`+name+`" carries a single value on all 1100 row(s)`) {
			t.Errorf("the constant column %q was not reported; warnings = %v", name, prof.Warnings)
		}
		// ONCE, as a field — never once per source as a dependency it is
		// not. A constant column is trivially "an exact function of"
		// every other field, so describing it that way is true, useless
		// and O(sources) lines long. This is what pins render's band
		// guard AHEAD of the null-shape check in candidateFor; the two
		// orders differ only here, and the wrong one produced twelve
		// such lines on this thirteen-field fixture.
		for _, w := range prof.Warnings {
			if strings.Contains(w, `"`+name+`" is an exact function of`) {
				t.Errorf("the constant column %q was described as a dependency: %q", name, w)
			}
		}
	}
}

// TestSuggestDeps_UnrelatedFieldIsNotProposed is the negative control,
// and it is deliberately NOT threshold-independent: `noise` repeats at
// period 13 against score's 11 and region's 4, so it contradicts each of
// them dozens of times. Loosening maxDepExceptions far enough admits it,
// which is what makes this test capable of failing.
func TestSuggestDeps_UnrelatedFieldIsNotProposed(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	for _, c := range depCandidates(prof.RuleCandidates) {
		if _, ok := c.SetExpr["noise"]; ok {
			t.Errorf("an unrelated field was proposed as derived: set_expr = %v from %q",
				c.SetExpr, c.Evidence.SourceField)
		}
		if c.Evidence.SourceField == "noise" {
			t.Errorf("an unrelated field was proposed as a dependency source: %v", c.SetExpr)
		}
	}
}

// TestSuggestDeps_EvidenceCarriesTheMeasuredLookup pins the evidence
// shape: the mapping the expression was read off, its support, and the
// zero exception count that is the detector's central claim.
func TestSuggestDeps_EvidenceCarriesTheMeasuredLookup(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	c := depCandidateFor(depCandidates(prof.RuleCandidates), "score")
	if c == nil {
		t.Fatalf("no candidate sourced on score")
	}
	ev := c.Evidence
	if ev.SourceField != "score" || ev.SourceLevels != 11 {
		t.Errorf("source_field/source_levels = %q/%d, want score/11", ev.SourceField, ev.SourceLevels)
	}
	if len(ev.Dependency) != 3 {
		t.Fatalf("dependency has %d entries, want 3", len(ev.Dependency))
	}
	for _, d := range ev.Dependency {
		if d.Exceptions != 0 {
			t.Errorf("target %q emitted with %d exception(s)", d.Field, d.Exceptions)
		}
		if len(d.Mapping) != 11 {
			t.Errorf("target %q mapping has %d arm(s), want one per observed source level (11)",
				d.Field, len(d.Mapping))
		}
		if d.Type != "packed_bool" {
			t.Errorf("target %q type = %q", d.Field, d.Type)
		}
		total := 0
		for _, m := range d.Mapping {
			total += m.N
			if _, ok := m.Value.(bool); !ok {
				t.Errorf("target %q level %q value = %T, want a bool for a packed_bool target",
					d.Field, m.Level, m.Value)
			}
		}
		if total != ev.RowsAffected {
			t.Errorf("target %q mapping support sums to %d, rows_affected = %d", d.Field, total, ev.RowsAffected)
		}
	}
	if ev.RowsObserved != 1100 {
		t.Errorf("rows_observed = %d, want 1100", ev.RowsObserved)
	}
	// The block's own null pattern is identical by the admission rule,
	// so nothing E1-S5 warns about can be discarded by the
	// null_together this candidate carries.
	if ev.MaxNullRateDeviation != 0 {
		t.Errorf("max_null_rate_deviation = %v, want 0 — identical null patterns cannot diverge",
			ev.MaxNullRateDeviation)
	}
	// The bounds are stated IN THE FILE, not only on stderr.
	for _, want := range []string{
		"packed_bool and u4 ONLY", "at most 16 observed levels", "ONE source",
		"was not cleared", "TOTAL function", "RULE-DETERMINED", "null_together",
	} {
		if !strings.Contains(ev.Note, want) {
			t.Errorf("candidate note does not state %q:\n%s", want, ev.Note)
		}
	}
}

// TestSuggestDeps_RidesTheExistingScan asserts the detector reads NO
// additional byte, counted the way TestProfileModels_RidesTheExistingScan
// counts them.
func TestSuggestDeps_RidesTheExistingScan(t *testing.T) {
	schema := depFixtureSchema()
	rows, nulls := depFixtureRows(400)
	data := encodeModelRows(t, schema, rows, nulls)

	read := func(suggest bool) int64 {
		c := &countingReader{r: bytes.NewReader(data)}
		if _, err := profileRecords(schema, c, ProfileOptions{SuggestRules: suggest}); err != nil {
			t.Fatalf("profileRecords(suggest=%v): %v", suggest, err)
		}
		return c.n
	}
	with, without := read(true), read(false)
	if with != without {
		t.Errorf("detection pulled %d bytes against %d without it; it must ride the existing scan",
			with, without)
	}
}

// TestSuggestDeps_HighCardinalitySourceIsAbandoned pins BOTH sides of
// maxDepLevels: a source with exactly the cap is considered, one over it
// is abandoned (never truncated) and counted.
//
// The fixture is parameterised BY the constant under test, which means
// raising maxDepLevels leaves it green — the same limitation E3-S1
// recorded for its own cardinality fixture. The property it does pin is
// the ABANDON, and the boundary from both sides.
func TestSuggestDeps_HighCardinalitySourceIsAbandoned(t *testing.T) {
	build := func(levels int) *Profile {
		t.Helper()
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "wide", Type: encoding.FieldTypeU8},
			{Name: "flag", Type: encoding.FieldTypePackedBool},
		}}
		// A u8 is not an admissible source type, so the wide field has
		// to be a categorical to reach the level cap at all.
		values := make([]string, levels)
		for i := range values {
			values[i] = fmt.Sprintf("v%02d", i)
		}
		schema.Fields[0] = encoding.Field{
			Name: "wide", Type: encoding.FieldTypeCategoricalU16,
			Dictionary: depDict(values),
		}
		var rows []map[string]any
		for i := 0; i < levels*20; i++ {
			lv := i % levels
			rows = append(rows, map[string]any{
				"wide": values[lv],
				"flag": float64(lv % 2),
			})
		}
		data := encodeModelRows(t, schema, rows, nil)
		prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
		if err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		return prof
	}

	atCap := build(maxDepLevels)
	if depCandidateFor(depCandidates(atCap.RuleCandidates), "wide") == nil {
		t.Errorf("a source with exactly %d levels was not considered; warnings = %v",
			maxDepLevels, atCap.Warnings)
	}
	over := build(maxDepLevels + 1)
	if c := depCandidateFor(depCandidates(over.RuleCandidates), "wide"); c != nil {
		t.Errorf("a source with %d levels was proposed: %v", maxDepLevels+1, c.SetExpr)
	}
	if !strings.Contains(strings.Join(over.Warnings, "\n"),
		"were not considered for exact dependency") {
		t.Errorf("the abandoned source was not counted; warnings = %v", over.Warnings)
	}
}

// TestSuggestDeps_ThinSupportShipsWithItsSupport: a candidate resting on
// a thin source level is not suppressed — it ships flagged, with its
// support, because the analyst is better placed than the threshold to
// judge a six-row arm.
func TestSuggestDeps_ThinSupportShipsWithItsSupport(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "grade", Type: encoding.FieldTypeU4},
		{Name: "top", Type: encoding.FieldTypePackedBool},
	}}
	var rows []map[string]any
	for i := 0; i < 200; i++ {
		g := 1
		// Level 2 rests on far fewer rows than minGateLevelSupport.
		if i%40 == 0 {
			g = 2
		}
		rows = append(rows, map[string]any{"grade": float64(g), "top": float64(g - 1)})
	}
	data := encodeModelRows(t, schema, rows, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	c := depCandidateFor(depCandidates(prof.RuleCandidates), "grade")
	if c == nil {
		t.Fatalf("the thin candidate was suppressed instead of shipped; warnings = %v", prof.Warnings)
	}
	if !c.Evidence.ThinSupport {
		t.Errorf("thin_support = false at min_level_support %d (below %d)",
			c.Evidence.MinLevelSupport, minGateLevelSupport)
	}
	if c.Evidence.MinLevelSupport != 5 {
		t.Errorf("min_level_support = %d, want 5", c.Evidence.MinLevelSupport)
	}
	var line string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, "thin dependency level ") {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("the thin candidate shipped without a warning; warnings = %v", prof.Warnings)
	}
	if kind, attention := classifyWarning(line); kind != "thin dependency level" || attention {
		t.Errorf("thin dependency classified as %q / attention=%v, want its own expected-outcome kind",
			kind, attention)
	}
}

// TestSuggestDeps_OneRuleWritesEachTarget: a target determined by two
// sources is written once, by the strongest candidate. Two rules writing
// one field leaves the earlier one firing with no effect, which is the
// silent-inertness class the whole effort exists to remove.
func TestSuggestDeps_OneRuleWritesEachTarget(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU4},
		{Name: "b", Type: encoding.FieldTypeU4},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "other", Type: encoding.FieldTypePackedBool},
	}}
	var rows []map[string]any
	for i := 0; i < 400; i++ {
		v := i % 4
		rows = append(rows, map[string]any{
			"a": float64(v), "b": float64(v),
			"flag": float64(v / 2), "other": float64(v % 2),
		})
	}
	data := encodeModelRows(t, schema, rows, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	writers := map[string][]string{}
	for _, c := range depCandidates(prof.RuleCandidates) {
		for _, name := range sortedKeys(c.SetExpr) {
			writers[name] = append(writers[name], c.Evidence.SourceField)
		}
	}
	for name, srcs := range writers {
		if len(srcs) > 1 {
			t.Errorf("field %q is written by %d candidates (%v); only the strongest may keep it",
				name, len(srcs), srcs)
		}
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"),
		"determined by more than one source") {
		t.Errorf("the contested targets were not counted; warnings = %v", prof.Warnings)
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"), "determine each OTHER") {
		t.Errorf("the mutually-determining pair a/b was not reported; warnings = %v", prof.Warnings)
	}
}

// TestSuggestDeps_BoundedListingRollsUp: over maxDepCandidates the file
// carries the strongest and the remainder is COUNTED, never dropped.
func TestSuggestDeps_BoundedListingRollsUp(t *testing.T) {
	n := maxDepCandidates + 5
	fields := []encoding.Field{}
	for i := 0; i < n; i++ {
		fields = append(fields,
			encoding.Field{Name: fmt.Sprintf("s%02d", i), Type: encoding.FieldTypeU4},
			encoding.Field{Name: fmt.Sprintf("t%02d", i), Type: encoding.FieldTypePackedBool})
	}
	schema := &encoding.Schema{Fields: fields}
	var rows []map[string]any
	for r := 0; r < 200; r++ {
		row := map[string]any{}
		for i := 0; i < n; i++ {
			// Each source gets its own period so no two of them agree,
			// and each target is a function of its own source alone.
			v := (r / (i + 1)) % 3
			row[fmt.Sprintf("s%02d", i)] = float64(v)
			row[fmt.Sprintf("t%02d", i)] = float64(v / 2)
		}
		rows = append(rows, row)
	}
	data := encodeModelRows(t, schema, rows, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	cands := depCandidates(prof.RuleCandidates)
	if len(cands) > maxDepCandidates {
		t.Errorf("%d dependency candidates written against a cap of %d", len(cands), maxDepCandidates)
	}
	if len(cands) != maxDepCandidates {
		t.Fatalf("fixture produced %d candidates; it must exceed the cap of %d to test the roll-up",
			len(cands), maxDepCandidates)
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"),
		"further exact-dependency candidate(s) not written") {
		t.Errorf("the truncated remainder was not counted; warnings = %v", prof.Warnings)
	}
}

// TestSuggestDeps_EmittedExpressionSurvivesTheRuleCompiler: every
// proposed expression compiles against the rule layer's OWN environment
// and passes the SAME static coercion check validateRules applies, so a
// candidate the loader would refuse is never written.
func TestSuggestDeps_EmittedExpressionSurvivesTheRuleCompiler(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	cands := depCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatal("no dependency candidates")
	}
	spec := &Spec{RowCount: 10, Rules: cands}
	for _, f := range depFixtureSchema().Fields {
		spec.Fields = append(spec.Fields, FieldSpec{
			Name: f.Name, Type: f.Type.String(), Nullable: f.Nullable,
			Distribution: DistConstant, Params: map[string]any{"value": 0.0},
		})
	}
	// The categorical needs a value its own domain accepts.
	for i := range spec.Fields {
		if spec.Fields[i].Name == "region" {
			spec.Fields[i].Params = map[string]any{"value": "east"}
		}
	}
	if err := validateRules(spec); err != nil {
		t.Fatalf("a written candidate was refused by the rule validator: %v", err)
	}
}

// TestSuggestDeps_DocumentMovesOnlyItsWarnings: the profile DOCUMENT is
// untouched by detection. An unreviewed structural claim must not ride
// inside the document that drives generation.
func TestSuggestDeps_DocumentMovesOnlyItsWarnings(t *testing.T) {
	schema := depFixtureSchema()
	rows, nulls := depFixtureRows(400)
	data := encodeModelRows(t, schema, rows, nulls)

	marshal := func(suggest bool) map[string]json.RawMessage {
		prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: suggest})
		if err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		raw, err := json.Marshal(prof)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}
	with, without := marshal(true), marshal(false)
	for k, v := range with {
		if k == "warnings" {
			continue
		}
		if !bytes.Equal(v, without[k]) {
			t.Errorf("profile key %q moved under --suggest-rules", k)
		}
	}
	for k := range without {
		if _, ok := with[k]; !ok {
			t.Errorf("profile key %q disappeared under --suggest-rules", k)
		}
	}
	if _, ok := with["rule_candidates"]; ok {
		t.Error("the candidates rode inside the profile document")
	}
}

// TestSuggestDeps_CandidatesArePreClaimEligible pins the consequence the
// candidate's own note advertises, and pins it against the REAL
// arbitration rather than by inspecting the emitted string: every target
// a dependency candidate writes is claimed at priority 0 by
// resolveConflicts' own ruleClaims, so its captured linear model,
// conditional pairs and residual correlations are RETIRED instead of
// being computed and then overwritten.
//
// The check must be ruleClaims and not a substring test on the
// expression. E2-S2 established that self-reference is detected on the
// PARSED expression, and this detector emits `region == "west"` for a
// target named `west` — a string that CONTAINS its own target's name
// inside a quoted literal and does not READ it. A substring check calls
// that a self-reference and reports the opposite of the truth.
func TestSuggestDeps_CandidatesArePreClaimEligible(t *testing.T) {
	prof := depFixtureProfile(t, 1100)
	cands := depCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatalf("no dependency candidates; warnings = %v", prof.Warnings)
	}
	claimed := map[string]bool{}
	for _, rc := range ruleClaims(cands) {
		claimed[rc.target.field] = true
	}
	for _, c := range cands {
		if c.When != "" {
			t.Errorf("candidate carries when = %q, which makes its targets ineligible for the pre-claim "+
				"its note advertises", c.When)
		}
		for _, name := range sortedKeys(c.SetExpr) {
			if !claimed[name] {
				t.Errorf("target %q is not pre-claimed; accepting the candidate would leave its captured "+
					"model computed and then overwritten", name)
			}
		}
		// The SOURCE is deliberately NOT claimed: nothing writes it, and
		// a null_together supplies no value.
		if claimed[c.Evidence.SourceField] {
			t.Errorf("the source %q was pre-claimed; a null_together member supplies no value and the "+
				"source keeps its own generation", c.Evidence.SourceField)
		}
	}
}

// TestSuggestDeps_ExceptionCountIsOrderIndependent is the test that
// found the accumulator's first design wrong.
//
// A single first-observation slot per source level answers the EXACT
// question correctly whatever order the rows arrive in, and answers the
// NEAR question backwards when the minority value happens to arrive
// first: the count comes back as the majority's, which pushes the pair
// over maxDepExceptions and loses the finding entirely. Both orders are
// driven here and must report the same three exceptions.
func TestSuggestDeps_ExceptionCountIsOrderIndependent(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "src", Type: encoding.FieldTypeU4},
		{Name: "tgt", Type: encoding.FieldTypePackedBool},
	}}
	// flipEarly puts the three contradicting rows FIRST at their level;
	// flipLate puts them last. The relationship is identical.
	build := func(flipEarly bool) []string {
		var rows []map[string]any
		for i := 0; i < 300; i++ {
			v := i % 3
			flag := 0.0
			if v >= 2 {
				flag = 1
			}
			flip := false
			if flipEarly {
				flip = i < 9 && v == 1
			} else {
				flip = i >= 291 && v == 1
			}
			if flip {
				flag = 1
			}
			rows = append(rows, map[string]any{"src": float64(v), "tgt": flag})
		}
		data := encodeModelRows(t, schema, rows, nil)
		prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
		if err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		var lines []string
		for _, w := range prof.Warnings {
			if strings.Contains(w, "is an exact function of") {
				lines = append(lines, w)
			}
		}
		return lines
	}
	early, late := build(true), build(false)
	if len(early) != 1 || len(late) != 1 {
		t.Fatalf("expected one almost-determined report each, got %d early / %d late\nearly=%v\nlate=%v",
			len(early), len(late), early, late)
	}
	if early[0] != late[0] {
		t.Errorf("the exception count depends on row order:\n  minority first: %s\n  minority last:  %s",
			early[0], late[0])
	}
	if !strings.Contains(early[0], "on all but 3 of 300") {
		t.Errorf("exception count = %q, want 3 of 300", early[0])
	}
}

// TestSuggestDeps_UncompilablePredicateIsDroppedNotWritten is E3-S1's
// own falsification finding, asked of this detector: a field legally
// named `in` is expr's membership operator, so the rendered expression
// is valid JSON that looks like every other candidate and is refused by
// the loader it is fed to.
//
// The candidate is dropped HERE, against the rule layer's own
// environment, and the drop is counted rather than silent.
func TestSuggestDeps_UncompilablePredicateIsDroppedNotWritten(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "in", Type: encoding.FieldTypeU4},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
	}}
	var rows []map[string]any
	for i := 0; i < 200; i++ {
		v := i % 4
		rows = append(rows, map[string]any{"in": float64(v), "flag": float64(v / 2)})
	}
	data := encodeModelRows(t, schema, rows, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	for _, c := range depCandidates(prof.RuleCandidates) {
		t.Errorf("a candidate was written whose expression does not compile: %v", c.SetExpr)
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"), "does not compile against the row environment") {
		t.Errorf("the dropped candidate was not counted; warnings = %v", prof.Warnings)
	}
}

// TestSuggestDeps_UnavailableCoNullMatrixRefusesRatherThanGuesses covers
// the one shape that genuinely reaches the "unknown" answer, and it took
// falsification to find that it is NOT the obvious one.
//
// The admission test asks E3-S2's co-null accumulator whether two fields
// share a null pattern. That accumulator is ABSENT when the cohort
// declares fewer than two nullable fields — but in that case at most one
// of any pair can ever be null, so their patterns demonstrably differ
// and "unknown" and "not the same" agree. The only shape where the
// answer is genuinely unknown is an ABANDONED accumulator: more nullable
// fields than maxBlockFields, where the matrix was never built and two
// fields really might share a pattern.
//
// Here they DO share one, exactly, and the candidate must still be
// reported rather than emitted — because emitting it on a guess is how a
// `set_expr` ends up un-nulling its target on every row its source is
// absent from.
func TestSuggestDeps_UnavailableCoNullMatrixRefusesRatherThanGuesses(t *testing.T) {
	fields := []encoding.Field{
		{Name: "src", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "tgt", Type: encoding.FieldTypePackedBool, Nullable: true},
	}
	// Enough nullable padding to abandon the co-null accumulator.
	for i := 0; len(fields) <= maxBlockFields; i++ {
		fields = append(fields, encoding.Field{
			Name: fmt.Sprintf("pad%03d", i), Type: encoding.FieldTypeU8, Nullable: true,
		})
	}
	schema := &encoding.Schema{Fields: fields}
	var rows []map[string]any
	var nulls []map[string]bool
	for i := 0; i < 120; i++ {
		v := i % 4
		row := map[string]any{"src": float64(v), "tgt": float64(v / 2)}
		for _, f := range fields[2:] {
			row[f.Name] = float64(i % 7)
		}
		rowNull := map[string]bool{}
		if i%10 == 0 {
			// src and tgt null on EXACTLY the same rows.
			rowNull["src"], rowNull["tgt"] = true, true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	joined := strings.Join(prof.Warnings, "\n")
	if !strings.Contains(joined, "co-missing block detection was not run") {
		t.Fatalf("the fixture did not abandon the co-null accumulator, so it cannot reach the unknown "+
			"answer; warnings = %v", prof.Warnings)
	}
	for _, c := range depCandidates(prof.RuleCandidates) {
		if _, ok := c.SetExpr["tgt"]; ok {
			t.Errorf("a candidate was emitted on a GUESS about the null pattern: %v / %v",
				c.SetExpr, c.NullTogether)
		}
	}
	if !strings.Contains(joined, `"tgt" is an exact function of "src"`) {
		t.Errorf("the unexpressible dependency was not reported; warnings = %v", prof.Warnings)
	}
}
