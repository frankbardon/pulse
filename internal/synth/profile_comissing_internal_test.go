package synth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// blockFixture builds a cohort carrying, simultaneously:
//
//   - blk0..blk3: a four-field question block nulled on EXACTLY the same
//     rows (one row in four) — the thing to find;
//   - solo0, solo1: two INDEPENDENT fields sharing that block's null rate
//     to the last bit and overlapping only by chance — the thing that
//     must NOT be found, and the whole subtlety of this detector;
//   - near: the block's pattern plus a handful of extra nulls — reported
//     with its agreement, never emitted;
//   - rare0, rare1: independent, null on 1% of rows each and on no row
//     in common. They are the NEGATIVE CONTROL FOR THE NEAR-MISS
//     MEASURE, and they are rare on purpose: two independent fields at
//     a 1% null rate agree on 98% of ROWS, so any row-level agreement
//     threshold near 1 reports them as an almost-block. Measured over
//     the null SETS they score 0. Found while falsifying: with only the
//     25%-rate solo pair, swapping the measure for row-level agreement
//     left this control green, because the scale-dependence only bites
//     at low null rates.
//   - gone, gone2: null on every row. Their own finding, and — the half
//     a single always-null column cannot test — they share a null
//     PATTERN with each other perfectly, so nothing but the exclusion
//     keeps them out of a two-member block. Found while falsifying: with
//     one such column the exclusion could be deleted and every test
//     still passed, because a class of one is never emitted.
//   - present: declared nullable, never null — silently ignored;
//   - solid: not nullable at all.
//
// Every null decision is an exact integer function of the row index, so
// the fixture bytes are architecture-independent by construction rather
// than by a float barrier (synth/moments.go).
//
// solo0/solo1 are the load-bearing part. They are constructed to share
// the block's null COUNT exactly — n/4 rows each — while being null on
// different rows: solo0 on rows congruent to 1 mod 4 and solo1 on rows
// congruent to 2 mod 4, which is zero overlap with the block, with each
// other, and with nothing to distinguish them from the block by rate.
func blockFixtureSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "blk0", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "blk1", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "blk2", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "blk3", Type: encoding.FieldTypePackedBool, Nullable: true},
		{Name: "solo0", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "solo1", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "near", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "rare0", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "rare1", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "gone", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "gone2", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "present", Type: encoding.FieldTypeU4, Nullable: true},
		{Name: "solid", Type: encoding.FieldTypeU4},
	}}
}

// blockFixtureRows builds n rows (n must be a multiple of 4).
// extraNear is the number of rows on which `near` carries a null the
// block does not.
func blockFixtureRows(t *testing.T, n, extraNear int) ([]map[string]any, []map[string]bool) {
	t.Helper()
	if n%4 != 0 {
		t.Fatalf("blockFixtureRows: n = %d must be a multiple of 4", n)
	}
	rows := make([]map[string]any, 0, n)
	nulls := make([]map[string]bool, 0, n)
	for i := 0; i < n; i++ {
		row := map[string]any{
			"blk0": float64(i % 8), "blk1": float64(i % 2),
			"blk2": float64((i / 2) % 2), "blk3": float64((i / 3) % 2),
			"solo0": float64(i % 5), "solo1": float64(i % 6),
			"near": float64(i % 7), "gone": 0.0, "gone2": 0.0,
			"rare0": float64(i % 4), "rare1": float64(i % 5),
			"present": float64(i % 3), "solid": float64(i % 9),
		}
		rowNull := map[string]bool{"gone": true, "gone2": true}
		if i%4 == 0 {
			rowNull["blk0"], rowNull["blk1"] = true, true
			rowNull["blk2"], rowNull["blk3"] = true, true
			rowNull["near"] = true
		}
		// Same COUNT as the block (n/4), different ROWS.
		if i%4 == 1 {
			rowNull["solo0"] = true
		}
		if i%4 == 2 {
			rowNull["solo1"] = true
		}
		// `near` = the block's pattern plus extraNear stray nulls, taken
		// from the rows congruent to 3 mod 4 so they cannot collide with
		// the block's own or with either solo's.
		if extraNear > 0 && i%4 == 3 && i/4 < extraNear {
			rowNull["near"] = true
		}
		// 1% each, on disjoint rows: 0.98 row-level agreement, 0
		// null-set overlap.
		if i%100 == 7 {
			rowNull["rare0"] = true
		}
		if i%100 == 13 {
			rowNull["rare1"] = true
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	return rows, nulls
}

func blockFixtureProfile(t *testing.T, n, extraNear int) *Profile {
	t.Helper()
	schema := blockFixtureSchema(t)
	rows, nulls := blockFixtureRows(t, n, extraNear)
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

// blockCandidates returns the co-missing candidates only.
func blockCandidates(cands []RuleSpec) []RuleSpec {
	var out []RuleSpec
	for _, c := range cands {
		if c.Evidence != nil && c.Evidence.Detector == comissingDetectorName {
			out = append(out, c)
		}
	}
	return out
}

func blockMembers(cands []RuleSpec) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, strings.Join(c.NullTogether, "+"))
	}
	return out
}

// TestSuggestBlocks_PatternNotMerelyRateYieldsTheBlock is acceptance
// criteria 1 and 2 in ONE test, because each is trivially satisfiable by
// abandoning the other: a detector that groups by null rate alone finds
// the block (and two fields that are not one), and a detector that finds
// nothing never proposes an unrelated pair.
//
// It is the named falsification target of this story. Drop the pattern
// term from blockDetector.classify and keep the rate term, and the
// second half below fails with solo0 and solo1 folded into the block.
func TestSuggestBlocks_PatternNotMerelyRateYieldsTheBlock(t *testing.T) {
	prof := blockFixtureProfile(t, 400, 5)
	cands := blockCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatalf("no co-missing candidates; warnings = %v", prof.Warnings)
	}

	var block *RuleSpec
	for i := range cands {
		if len(cands[i].NullTogether) > 0 && cands[i].NullTogether[0] == "blk0" {
			block = &cands[i]
		}
	}
	if block == nil {
		t.Fatalf("the four-field block was not proposed; candidates = %v", blockMembers(cands))
	}
	want := "blk0,blk1,blk2,blk3"
	if got := strings.Join(block.NullTogether, ","); got != want {
		t.Errorf("block members = %q, want %q", got, want)
	}
	// A block candidate carries NO `when`: an absent `when` means every
	// row, which is the true statement — the members are null together
	// on the rows where the block is null AND present together on the
	// rows where it is not.
	if block.When != "" {
		t.Errorf("block candidate carries when = %q; it must apply to every row", block.When)
	}
	if len(block.SetNull) != 0 || len(block.Set) != 0 || len(block.SetExpr) != 0 {
		t.Errorf("block candidate declares a non-null_together action: %+v", *block)
	}

	// THE DISCRIMINATING HALF. solo0 and solo1 share the block's null
	// rate to the last bit and are null on entirely different rows.
	// Neither may appear in ANY block, with the block or with each
	// other.
	nullN := map[string]float64{}
	for _, fp := range prof.Fields {
		nullN[fp.Name] = fp.NullRate
	}
	if nullN["solo0"] != nullN["blk0"] || nullN["solo1"] != nullN["blk0"] {
		t.Fatalf("fixture broken: solo rates %v / %v must equal the block's %v for this test to discriminate",
			nullN["solo0"], nullN["solo1"], nullN["blk0"])
	}
	for _, c := range cands {
		for _, m := range c.NullTogether {
			if m == "solo0" || m == "solo1" {
				t.Errorf("independent field %q was emitted in a block (%v) — it shares the block's null RATE "+
					"and none of its null ROWS; the equal-rate test alone is insufficient",
					m, c.NullTogether)
			}
		}
	}
}

// TestSuggestBlocks_AlwaysNullIsItsOwnFinding is acceptance criterion 5.
// A column with no observations has no gate and no block; it is named
// with its type and kept out of both.
func TestSuggestBlocks_AlwaysNullIsItsOwnFinding(t *testing.T) {
	prof := blockFixtureProfile(t, 400, 5)
	var line string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, `always-null column "gone"`) {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("the always-null column was not reported; warnings = %v", prof.Warnings)
	}
	if !strings.Contains(line, "(u4)") {
		t.Errorf("the always-null finding does not name the field's type: %q", line)
	}
	if !strings.Contains(line, "400") {
		t.Errorf("the always-null finding does not name the rows profiled: %q", line)
	}
	kind, attention := classifyWarning(line)
	if kind != "always-null column" || !attention {
		t.Errorf("always-null classified as %q / attention=%v, want its own attention kind", kind, attention)
	}
	if !strings.Contains(strings.Join(prof.Warnings, "\n"), `always-null column "gone2"`) {
		t.Errorf("the second always-null column was not reported; warnings = %v", prof.Warnings)
	}
	for _, c := range prof.RuleCandidates {
		for _, m := range c.NullTogether {
			if m == "gone" || m == "gone2" {
				t.Errorf("an always-null column was forced into a block: %v — two of them share a null "+
					"pattern perfectly, so only the exclusion keeps them out", c.NullTogether)
			}
		}
		for _, m := range c.SetNull {
			if m == "gone" {
				t.Errorf("an always-null column was forced into a gating candidate: %v", c.SetNull)
			}
		}
		if c.Evidence != nil && c.Evidence.GateField == "gone" {
			t.Errorf("an always-null column was proposed as a gate")
		}
	}

	// A field declared nullable that carried NO null is not a finding of
	// THIS detector at all: it has no pattern to match and nothing went
	// wrong.
	//
	// NARROWED at E3-S3, not relaxed. The assertion was "no warning
	// mentions `present`", which held only while the two null-state
	// detectors were the sole writers to this channel. The
	// exact-dependency detector reads VALUES and legitimately names a
	// never-null field when it is a function of something (this
	// fixture's columns are all row-index functions, so several are), so
	// the assertion now excludes that detector's own lines by their
	// wording rather than excluding the field.
	for _, w := range prof.Warnings {
		if strings.Contains(w, "exact function") {
			continue
		}
		if strings.Contains(w, `"present"`) {
			t.Errorf("a never-null field was reported by a null-state detector: %q", w)
		}
	}
}

// TestSuggestBlocks_PartialOverlapIsReportedWithItsAgreement is
// acceptance criterion 4: a near block is neither emitted silently nor
// dropped silently.
func TestSuggestBlocks_PartialOverlapIsReportedWithItsAgreement(t *testing.T) {
	// 400 rows: the block covers 100, `near` covers those 100 plus 5.
	prof := blockFixtureProfile(t, 400, 5)

	for _, c := range blockCandidates(prof.RuleCandidates) {
		members := strings.Join(c.NullTogether, ",")
		if strings.Contains(members, "near") && strings.Contains(members, "blk0") {
			t.Errorf("a near block was emitted as an exact one: %v", c.NullTogether)
		}
	}

	var line string
	for _, w := range prof.Warnings {
		if strings.Contains(w, "null together on") && strings.Contains(w, `"near"`) {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("the near block was dropped silently; warnings = %v", prof.Warnings)
	}
	// 100 shared, 105 union -> 0.9524, 5 rows disagreeing.
	if !strings.Contains(line, "0.9524") {
		t.Errorf("the near-block report does not carry its agreement rate: %q", line)
	}
	if !strings.Contains(line, "5 row(s) disagree") {
		t.Errorf("the near-block report does not carry the disagreeing row count: %q", line)
	}
	// It names the block SIDE by size, so a reader knows the miss is
	// against four fields rather than against one.
	if !strings.Contains(line, "a block of 4") {
		t.Errorf("the near-block report does not name the block it missed: %q", line)
	}
	kind, attention := classifyWarning(line)
	if kind != "rule candidate not considered" || !attention {
		t.Errorf("near-block classified as %q / attention=%v", kind, attention)
	}
	// FU-10: the block side must carry a handle that actually finds the
	// candidate. The handle is the candidate's FIRST null_together entry
	// — content-derived, so an analyst deleting other candidates does
	// not invalidate it, which is exactly what a candidate INDEX would
	// do. Asserted as a real lookup rather than as a substring: the
	// handle is checked by going and finding the candidate with it.
	if !strings.Contains(line, "null_together beginning") {
		t.Errorf("the near-block report gives no handle back to the candidate: %q", line)
	}
	handle := nearBlockHandleFromLine(t, line)
	var carrier []string
	for _, c := range blockCandidates(prof.RuleCandidates) {
		if len(c.NullTogether) > 0 && c.NullTogether[0] == handle {
			carrier = c.NullTogether
		}
	}
	if carrier == nil {
		t.Fatalf("the handle %q names no emitted candidate; candidates = %v",
			handle, blockMembers(blockCandidates(prof.RuleCandidates)))
	}
	if len(carrier) != 4 {
		t.Errorf("the handle resolves to a block of %d, but the line says 4: %v", len(carrier), carrier)
	}

	// And the exact block still ships without it — a near miss costs the
	// near field its membership, not the block its candidacy.
	var found bool
	for _, c := range blockCandidates(prof.RuleCandidates) {
		if strings.Join(c.NullTogether, ",") == "blk0,blk1,blk2,blk3" {
			found = true
		}
	}
	if !found {
		t.Errorf("the exact block was lost to its near miss; candidates = %v",
			blockMembers(blockCandidates(prof.RuleCandidates)))
	}
}

// TestSuggestBlocks_IndependentPairsAreNotEvenReported keeps the
// near-block threshold honest from the other side: two fields sharing a
// rate and nothing else must not surface as a near miss either, or the
// report becomes noise a reader learns to skip.
func TestSuggestBlocks_IndependentPairsAreNotEvenReported(t *testing.T) {
	prof := blockFixtureProfile(t, 400, 5)
	for _, w := range prof.Warnings {
		if !strings.Contains(w, "null together on") {
			continue
		}
		for _, f := range []string{`"solo0"`, `"solo1"`, `"rare0"`, `"rare1"`} {
			if strings.Contains(w, f) {
				t.Errorf("an independent field was reported as a near block: %q", w)
			}
		}
	}
}

// TestSuggestBlocks_EvidenceCarriesThePatternClaim is the checkability
// property: a candidate a reader cannot verify is one they accept
// blindly.
func TestSuggestBlocks_EvidenceCarriesThePatternClaim(t *testing.T) {
	prof := blockFixtureProfile(t, 400, 5)
	cands := blockCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatal("no block candidates")
	}
	ev := cands[0].Evidence
	if ev.Detector != comissingDetectorName {
		t.Errorf("detector = %q, want %q", ev.Detector, comissingDetectorName)
	}
	if ev.RowsObserved != 400 || ev.RowsAffected != 100 {
		t.Errorf("rows = %d observed / %d affected, want 400 / 100", ev.RowsObserved, ev.RowsAffected)
	}
	if ev.GatedShare != 0.25 {
		t.Errorf("gated_share = %v, want 0.25", ev.GatedShare)
	}
	if len(ev.Block) != 4 {
		t.Fatalf("block members = %d, want 4", len(ev.Block))
	}
	types := map[string]string{"blk0": "u4", "blk1": "packed_bool", "blk2": "packed_bool", "blk3": "packed_bool"}
	for _, m := range ev.Block {
		if m.NullCount != 100 || m.NullRate != 0.25 {
			t.Errorf("member %q = %d / %v, want 100 / 0.25", m.Field, m.NullCount, m.NullRate)
		}
		if m.Agreement != 1 {
			t.Errorf("member %q agreement = %v; an emitted block is EXACT", m.Field, m.Agreement)
		}
		if m.Type != types[m.Field] {
			t.Errorf("member %q type = %q, want %q", m.Field, m.Type, types[m.Field])
		}
	}
	// The gating detector's own slots stay empty and, crucially, off the
	// wire — see TestSuggestBlocks_EvidenceKeepsTheGatingWireShape.
	if ev.GateField != "" || len(ev.Levels) != 0 || len(ev.Targets) != 0 {
		t.Errorf("a co-missing candidate populated the gating detector's slots: %+v", ev)
	}
	// The deviation is E1-S5's own divergence measure over the rates
	// null_together is about to discard, and it is 0 by construction: an
	// exact block cannot trip nullRateDivergenceThreshold.
	if ev.MaxNullRateDeviation != 0 {
		t.Errorf("max_null_rate_deviation = %v, want 0 for an exact block", ev.MaxNullRateDeviation)
	}
	if ev.MaxNullRateDeviation >= nullRateDivergenceThreshold {
		t.Errorf("an emitted block would trip E1-S5's divergence warning at %v", ev.MaxNullRateDeviation)
	}
	if ev.MinLevelSupport != 100 || ev.ThinSupport {
		t.Errorf("support = %d thin=%v, want 100 / false", ev.MinLevelSupport, ev.ThinSupport)
	}
	for _, want := range []string{"PATTERN", "null_rate", "AFTER"} {
		if !strings.Contains(ev.Note, want) {
			t.Errorf("note does not mention %q: %q", want, ev.Note)
		}
	}
}

// TestSuggestBlocks_RidesTheExistingScan is the "no second pass" gate,
// the same direct measurement TestProfileModels_RidesTheExistingScan and
// TestSuggestRules_RidesTheExistingScan make: profileRecords consumes a
// one-shot io.Reader, so a second pass is unrepresentable without
// re-reading bytes.
func TestSuggestBlocks_RidesTheExistingScan(t *testing.T) {
	schema := blockFixtureSchema(t)
	rows, nulls := blockFixtureRows(t, 400, 5)
	data := encodeModelRows(t, schema, rows, nulls)

	baseline := &countingReader{r: bytes.NewReader(data)}
	if _, err := profileRecords(schema, baseline, ProfileOptions{}); err != nil {
		t.Fatalf("baseline profileRecords: %v", err)
	}
	detected := &countingReader{r: bytes.NewReader(data)}
	prof, err := profileRecords(schema, detected, ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("detecting profileRecords: %v", err)
	}
	if len(blockCandidates(prof.RuleCandidates)) == 0 {
		t.Fatal("fixture must produce a block for this test to mean anything")
	}
	if detected.n != baseline.n {
		t.Errorf("detection read %d bytes, baseline read %d — it must ride the existing scan",
			detected.n, baseline.n)
	}
}

// TestSuggestBlocks_MemoryIsBoundedByTheChunkNotTheCohort is the other
// half of acceptance criterion 3. The accumulator's buffers are sized
// from the FIELD count and blockChunkRows and never from the row count,
// so a cohort ten times longer allocates exactly the same thing.
//
// Asserted on the detector's own state rather than by a heap
// measurement: a runtime.MemStats delta over a profile scan is dominated
// by every other accumulator in profileRecords and would be a test of
// the allocator's noise floor.
func TestSuggestBlocks_MemoryIsBoundedByTheChunkNotTheCohort(t *testing.T) {
	schema := blockFixtureSchema(t)
	small := newBlockDetector(schema)
	if small == nil {
		t.Fatal("fixture schema must admit participants")
	}
	wantBits := small.n * small.words
	wantCo := small.n * small.n

	rows, nulls := blockFixtureRows(t, 400, 5)
	for i := range rows {
		small.observe(nulls[i])
	}
	big := newBlockDetector(schema)
	bigRows, bigNulls := blockFixtureRows(t, 400*40, 5)
	for i := range bigRows {
		big.observe(bigNulls[i])
	}
	if big.rows != 16000 {
		t.Fatalf("big detector saw %d rows", big.rows)
	}
	if len(big.bits) != wantBits || len(small.bits) != wantBits {
		t.Errorf("bitset grew with the cohort: %d vs %d words", len(big.bits), len(small.bits))
	}
	if len(big.coNull) != wantCo || len(small.coNull) != wantCo {
		t.Errorf("co-null matrix grew with the cohort: %d vs %d", len(big.coNull), len(small.coNull))
	}
	// And the chunk fold is exact across the boundary: 16,000 rows is
	// under one chunk, so drive a cohort past blockChunkRows too.
	huge := newBlockDetector(schema)
	hugeRows, hugeNulls := blockFixtureRows(t, blockChunkRows+400, 5)
	for i := range hugeRows {
		huge.observe(hugeNulls[i])
	}
	huge.foldChunk()
	i0, i1 := huge.idx["blk0"], huge.idx["blk1"]
	if got, want := huge.co(i0, i1), (blockChunkRows+400)/4; got != want {
		t.Errorf("co-null across the chunk boundary = %d, want %d", got, want)
	}
	if huge.overlap(i0, huge.idx["solo0"]) != 0 {
		t.Errorf("independent fields overlapped across the chunk boundary")
	}
	if len(huge.bits) != wantBits {
		t.Errorf("bitset grew past one chunk: %d words", len(huge.bits))
	}
}

// TestSuggestBlocks_RankedLargestFirstAndBounded is acceptance criterion
// 6, driven against the constant so retuning it moves the boundary
// rather than stranding a literal.
//
// maxBlockCandidates + 3 blocks with DISJOINT null patterns: one of five
// members and the rest of two, so the cap, the counted remainder and the
// largest-first order are all exercised by one fixture. Distinct sizes
// for every block are not available — maxBlockCandidates + 3 distinct
// sizes would need more nullable fields than maxBlockFields admits, and
// the detector would abandon instead. Found by this test.
func TestSuggestBlocks_RankedLargestFirstAndBounded(t *testing.T) {
	const blocks = maxBlockCandidates + 3
	size := func(b int) int {
		if b == 0 {
			return 5
		}
		return 2
	}
	var fields []encoding.Field
	for b := 0; b < blocks; b++ {
		for m := 0; m < size(b); m++ {
			fields = append(fields, encoding.Field{
				Name: fmt.Sprintf("b%02d_%02d", b, m), Type: encoding.FieldTypeU4, Nullable: true})
		}
	}
	schema := &encoding.Schema{Fields: fields}
	const n = blocks * 8
	rows := make([]map[string]any, 0, n)
	nulls := make([]map[string]bool, 0, n)
	for i := 0; i < n; i++ {
		row := map[string]any{}
		rowNull := map[string]bool{}
		for b := 0; b < blocks; b++ {
			null := i%blocks == b
			for m := 0; m < size(b); m++ {
				name := fmt.Sprintf("b%02d_%02d", b, m)
				row[name] = float64((i + m) % 8)
				if null {
					rowNull[name] = true
				}
			}
		}
		rows = append(rows, row)
		nulls = append(nulls, rowNull)
	}
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	cands := blockCandidates(prof.RuleCandidates)
	if len(cands) != maxBlockCandidates {
		t.Fatalf("emitted %d blocks, want the cap of %d; warnings = %v",
			len(cands), maxBlockCandidates, prof.Warnings)
	}
	for i := 1; i < len(cands); i++ {
		if len(cands[i-1].NullTogether) < len(cands[i].NullTogether) {
			t.Errorf("blocks are not largest-first: %d then %d",
				len(cands[i-1].NullTogether), len(cands[i].NullTogether))
		}
	}
	if got := len(cands[0].NullTogether); got != 5 {
		t.Errorf("first block has %d members, want the largest (5)", got)
	}
	var rollup string
	for _, w := range prof.Warnings {
		if strings.Contains(w, "further co-missing block(s) not written") {
			rollup = w
		}
	}
	if rollup == "" {
		t.Fatalf("the truncated blocks were dropped silently; warnings = %v", prof.Warnings)
	}
	if !strings.HasPrefix(rollup, "rule suggestion: +3 ") {
		t.Errorf("roll-up = %q, want a counted remainder of 3", rollup)
	}
	kind, attention := classifyWarning(rollup)
	if kind != "rule candidate not considered" || !attention {
		t.Errorf("roll-up classified as %q / attention=%v", kind, attention)
	}
}

// TestSuggestBlocks_WideCohortIsAbandonedNotTruncated pins the field cap
// from both sides, the way TestSuggestRules_HighCardinalityFieldIsNotAGate
// pins maxGateLevels: a truncated field set would yield blocks that are
// exact among the fields it kept and silently missing the rest.
//
// The fixture is expressed RELATIVE to the constant, so it keeps
// exercising the cap when the constant moves; the cost is that the
// numeric VALUE is not pinned here, which is correct — it is a tuning
// documented at its declaration.
func TestSuggestBlocks_WideCohortIsAbandonedNotTruncated(t *testing.T) {
	build := func(nullable int) *Profile {
		t.Helper()
		var fields []encoding.Field
		for i := 0; i < nullable; i++ {
			fields = append(fields, encoding.Field{
				Name: fmt.Sprintf("f%04d", i), Type: encoding.FieldTypeU4, Nullable: true})
		}
		const n = 40
		rows := make([]map[string]any, 0, n)
		nulls := make([]map[string]bool, 0, n)
		for i := 0; i < n; i++ {
			row := map[string]any{}
			rowNull := map[string]bool{}
			for j := 0; j < nullable; j++ {
				name := fmt.Sprintf("f%04d", j)
				row[name] = float64((i + j) % 8)
				if i%4 == 0 {
					rowNull[name] = true
				}
			}
			rows = append(rows, row)
			nulls = append(nulls, rowNull)
		}
		schema := &encoding.Schema{Fields: fields}
		data := encodeModelRows(t, schema, rows, nulls)
		prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{SuggestRules: true})
		if err != nil {
			t.Fatalf("profileRecords: %v", err)
		}
		return prof
	}

	over := build(maxBlockFields + 1)
	if got := len(blockCandidates(over.RuleCandidates)); got != 0 {
		t.Errorf("a %d-field cohort produced %d blocks; over the cap the detector must abandon",
			maxBlockFields+1, got)
	}
	var reported bool
	for _, w := range over.Warnings {
		if strings.Contains(w, "co-missing block detection was not run") {
			reported = true
			kind, attention := classifyWarning(w)
			if kind != "rule candidate not considered" || !attention {
				t.Errorf("abandonment classified as %q / attention=%v", kind, attention)
			}
		}
	}
	if !reported {
		t.Errorf("the abandonment was not reported; warnings = %v", over.Warnings)
	}

	atCap := build(maxBlockFields)
	if got := len(blockCandidates(atCap.RuleCandidates)); got != 1 {
		t.Errorf("a cohort with exactly %d nullable fields produced %d blocks, want 1 — "+
			"the cap must admit its own boundary", maxBlockFields, got)
	}
}

// TestSuggestBlocks_EvidenceKeepsTheGatingWireShape pins the one
// backward-compatibility risk in widening RuleEvidence: the five gating
// slots gained `omitempty` so a co-missing candidate does not write five
// empty measurements, and a GATING candidate must still write all five.
func TestSuggestBlocks_EvidenceKeepsTheGatingWireShape(t *testing.T) {
	prof := gateFixtureProfile(t, 400, 4)
	gate, ok := candidateByGate(prof.RuleCandidates, "aware")
	if !ok {
		t.Fatal("gating fixture produced no candidate")
	}
	raw := mustMarshalRule(t, gate)
	for _, key := range []string{
		`"gate_field"`, `"gated_levels"`, `"open_levels"`, `"levels"`, `"targets"`,
		`"rows_observed"`, `"rows_affected"`, `"gated_share"`, `"max_null_rate_deviation"`,
		`"min_level_support"`,
	} {
		if !strings.Contains(raw, key) {
			t.Errorf("gating candidate lost %s from its evidence: %s", key, raw)
		}
	}
	if strings.Contains(raw, `"block"`) {
		t.Errorf("gating candidate carries the co-missing block slot: %s", raw)
	}

	blocks := blockCandidates(blockFixtureProfile(t, 400, 5).RuleCandidates)
	if len(blocks) == 0 {
		t.Fatal("block fixture produced no candidate")
	}
	braw := mustMarshalRule(t, blocks[0])
	for _, key := range []string{`"gate_field"`, `"gated_levels"`, `"open_levels"`, `"levels"`, `"targets"`} {
		if strings.Contains(braw, key) {
			t.Errorf("co-missing candidate writes the gating slot %s: %s", key, braw)
		}
	}
	if !strings.Contains(braw, `"block"`) {
		t.Errorf("co-missing candidate carries no block evidence: %s", braw)
	}
	// max_null_rate_deviation is SHARED and must keep writing its real
	// 0 on both detectors — an omitempty there would turn the best
	// possible measurement into an absent one.
	if !strings.Contains(braw, `"max_null_rate_deviation":0`) {
		t.Errorf("co-missing candidate dropped its zero deviation: %s", braw)
	}
}

func mustMarshalRule(t *testing.T, r RuleSpec) string {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	return string(raw)
}

// TestSuggestBlocks_ThinBlockShipsWithItsSupport is the co-missing
// counterpart of TestSuggestRules_ThinSupportShipsWithItsSupport: a
// block whose thinner arm rests on too few rows is FLAGGED, not
// suppressed, because the analyst is better placed than the threshold to
// judge whether three rows are a question block.
//
// Added after an HONEST NEGATIVE while falsifying: deleting the
// thin-block warning outright broke no test, so the warning existed and
// nothing asserted it.
func TestSuggestBlocks_ThinBlockShipsWithItsSupport(t *testing.T) {
	// Twelve rows: the block is null on three of them.
	prof := blockFixtureProfile(t, 12, 0)
	cands := blockCandidates(prof.RuleCandidates)
	if len(cands) == 0 {
		t.Fatalf("a thin block was SUPPRESSED rather than flagged; warnings = %v", prof.Warnings)
	}
	ev := cands[0].Evidence
	if ev.MinLevelSupport != 3 {
		t.Errorf("min_level_support = %d, want 3 (the null arm)", ev.MinLevelSupport)
	}
	if !ev.ThinSupport {
		t.Errorf("thin_support = false at support %d (threshold %d)", ev.MinLevelSupport, minGateLevelSupport)
	}
	var thin []string
	for _, w := range prof.Warnings {
		if strings.HasPrefix(w, "thin co-missing block ") {
			thin = append(thin, w)
		}
	}
	if len(thin) == 0 {
		t.Fatalf("a thin block shipped with no warning; warnings = %v", prof.Warnings)
	}
	for _, w := range thin {
		if !strings.Contains(w, "still ships") {
			t.Errorf("thin warning does not say the candidate still ships: %q", w)
		}
		kind, attention := classifyWarning(w)
		if kind != "thin co-missing block" || attention {
			t.Errorf("thin block warning classified as %q / attention=%v, want an expected outcome",
				kind, attention)
		}
	}
	// The listing needs no cap of its own: it is bounded by
	// maxBlockCandidates, one line per emitted block at most.
	if len(thin) > maxBlockCandidates {
		t.Errorf("%d thin-block lines for at most %d blocks", len(thin), maxBlockCandidates)
	}
}

// nearBlockHandleFromLine extracts the candidate handle a near-miss line
// advertises — the field name it says the candidate's null_together
// begins with.
func nearBlockHandleFromLine(t *testing.T, line string) string {
	t.Helper()
	const marker = `null_together beginning "`
	i := strings.Index(line, marker)
	if i < 0 {
		t.Fatalf("no handle in %q", line)
	}
	rest := line[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated handle in %q", line)
	}
	return rest[:j]
}

// TestNearBlockSideLabel_HandleOnlyWhenThereIsACandidate pins the three
// shapes a near-miss side can take, because the middle one is the whole
// of FU-10 and the third is the way to get it wrong.
//
// A SINGLETON is not a block, so it carries no handle — there is no
// candidate to find. A proposed class carries the content-derived handle
// (the candidate whose null_together begins with the representative). A
// class that did NOT reach the file — beyond maxBlockCandidates — must
// say so rather than advertise a handle that resolves to nothing, which
// is the failure a per-side flag exists to prevent.
func TestNearBlockSideLabel_HandleOnlyWhenThereIsACandidate(t *testing.T) {
	if got := blockSideLabel("solo", 1, false); got != `"solo"` {
		t.Errorf("singleton label = %q, want the bare name", got)
	}
	if got := blockSideLabel("solo", 1, true); got != `"solo"` {
		t.Errorf("a singleton must never advertise a block handle: %q", got)
	}
	proposed := blockSideLabel("regard", 50, true)
	if !strings.Contains(proposed, "a block of 50") ||
		!strings.Contains(proposed, `null_together beginning "regard"`) {
		t.Errorf("a proposed block gives no handle: %q", proposed)
	}
	dropped := blockSideLabel("regard", 50, false)
	if strings.Contains(dropped, "null_together beginning") {
		t.Errorf("a block that never reached the file advertises a handle to nothing: %q", dropped)
	}
	if !strings.Contains(dropped, "not proposed") {
		t.Errorf("a block that never reached the file does not say so: %q", dropped)
	}
}
