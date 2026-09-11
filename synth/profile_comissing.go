package synth

import (
	"fmt"
	"math/bits"
	"sort"

	"github.com/frankbardon/pulse/encoding"
)

// This file is the profile-time CO-MISSING detector, the second
// candidate kind on the pipeline `profile create --suggest-rules` built
// (synth/profile_gating.go). Where that one finds a field whose LEVELS
// gate another field's null rate, this one finds fields that are simply
// nulled TOGETHER — a survey question block asked or skipped as a unit —
// and proposes them as `null_together` rules.
//
// It exists because E3-S1's detector cannot express the finding and said
// so: a gate whose only gated level is the null pseudo-level is the
// ABSENCE of a field rather than one of its values, which every member
// of an N-field block reports about the other N-1, so proposing them
// restates one finding N times. Those were COUNTED there (3,932 on the
// motivating cohort) and are resolved here into one candidate per block.
//
// # THE EQUAL-RATE TEST ALONE IS INSUFFICIENT, and that is the whole
// # subtlety of this detector
//
// Two fields that share a null_rate can have nothing whatever in common:
// on a 381,324-row cohort a pair of independent fields each null on
// 96,326 rows share their rate to sixteen digits and overlap on about a
// quarter of it. A detector that groups by rate proposes them as one
// question block, `null_together` then makes the claim true in generated
// output, and the cohort acquires a structural fact the source does not
// have — in a file whose whole purpose is to state facts the data obeys.
//
// So the rate is the RANKING signal and the per-row null PATTERN is the
// PROOF: a block is admitted only when its members are null on EXACTLY
// the same rows. Identity is an equivalence relation, so the members of
// an admitted block are indistinguishable from one another by
// measurement — which is what makes the emitted member order arbitrary
// (see blockCandidateNote) rather than a ranking someone has to get
// right.
//
// # How the pattern is accumulated: BOUNDED, and on the existing scan
//
// The obvious accumulator is a per-field hash of the null pattern: O(F)
// memory, O(k) per row, exact-identity for free. It is rejected because
// it answers ONLY the yes/no question. A near-block — a block plus one
// field with three extra nulls in 381,324 rows — is the single most
// informative thing this detector can report, and a hash can neither
// measure it nor distinguish it from an unrelated field. A hash also
// trades an exact answer for a collision probability, and a collision
// here is a fabricated question block with nothing anywhere saying so.
//
// What is accumulated instead is the exact pairwise null OVERLAP, in
// CHUNKS: each field gets a bitset over the rows of the current chunk
// (blockChunkRows), the chunk is folded into an F x F co-null matrix by
// popcount, and the bitsets are cleared. Memory is
// O(F x blockChunkRows / 8 + F^2) and does NOT grow with the cohort —
// under 2 MB at the field cap. The per-row cost is one bit write per
// null field; the fold is F^2/2 x rows/64 popcounts for the whole scan
// (44 million on the motivating cohort, tens of milliseconds). It rides
// the scan profile create already makes: no second pass and no extra
// byte, which TestSuggestBlocks_RidesTheExistingScan measures by
// counting bytes pulled.
//
// # What it reports rather than proposes
//
//   - A NEAR block (high overlap, not exact) is reported with its
//     agreement rather than emitted. E3-S1's thin candidates SHIP
//     because their relationship is exact and only its SUPPORT is thin;
//     a near block is the opposite — the support is ample and the
//     relationship is measurably false on some rows — and null_together
//     has no dial for "almost", so applying one silently rewrites the
//     rows that disagreed. Reported, never dropped.
//   - An ALWAYS-NULL column is its own finding, named with its type. It
//     has no gate and no block: it is not co-missing with anything, it
//     is simply absent, and today generation fabricates a distribution
//     for it off a marginal computed over zero observations. What
//     generation should DO about it is deliberately out of scope here.

const (
	// nearBlockAgreement is the overlap at or above which a pair of
	// fields that are NOT exactly co-missing is reported anyway.
	//
	// The measure is the overlap of the two null SETS — of the rows
	// where either field is null, the share where both are (the Jaccard
	// index over null sets) — and not row-level agreement, which is
	// scale-dependent in the worst possible direction: two INDEPENDENT
	// fields each null on 1% of rows agree on 98% of rows, so a
	// row-level threshold anywhere near 1 floods with pairs that have
	// nothing to do with each other. Over null sets the same pair scores
	// about 0.005, while a real block plus three stray nulls in 381,324
	// rows scores 0.99997.
	//
	// 0.95 is deliberately loose where the admission rule is exact. Its
	// job is not to decide anything — nothing is emitted on the strength
	// of it — but to make a near miss VISIBLE, and the near misses worth
	// seeing are exactly the ones a reader would otherwise have to
	// discover by comparing null rates by eye, which is how this
	// cohort's blocks were found in the first place. Measured on the
	// motivating cohort it surfaces the relationship between the
	// 0.2526 block and the 0.2649 block (0.954) — the pair E3-S1 could
	// explain only as "gated by the same field, with a further 1.2% of
	// missingness on the open side".
	nearBlockAgreement = 0.95

	// maxBlockFields caps the fields the pairwise accumulator carries.
	//
	// It is a CPU bound before it is a memory bound. The chunk fold is
	// F^2/2 x rows/64 popcounts, so doubling the field count quadruples
	// the fold; at 256 the whole detection stays within a small constant
	// factor of one pass over the cohort's own null bitmap, which is the
	// standard this rides the existing scan against. Memory at the cap
	// is 256 x blockChunkRows / 8 bytes of bitset plus 256^2 counters —
	// about 2.5 MB, and flat in the row count.
	//
	// Only NULLABLE fields count toward it: a field the schema cannot
	// record a null for can never be a block member, so excluding it is
	// exact rather than a heuristic.
	//
	// Over the cap the detector is ABANDONED, never truncated, for
	// exactly the reason maxGateLevels abandons: a truncated field set
	// still yields blocks that are EXACT among the fields it kept, so
	// every one of them would be a strict subset of a real block with
	// nothing saying so — and a null_together missing a member is
	// precisely the defect this detector exists to remove, now written
	// down as a rule.
	maxBlockFields = 256

	// blockChunkRows is the rows per bitset chunk. It is what makes the
	// accumulator's memory independent of the cohort's size: bits are
	// folded into the co-null matrix by popcount and cleared every
	// blockChunkRows rows, so a 400-row cohort and a 400-million-row
	// cohort hold the same buffers.
	//
	// 65,536 is one uint64 word per 64 rows x 1,024 words per field —
	// 8 KB per field, 2 MB at maxBlockFields. Smaller chunks fold more
	// often for no benefit; larger ones buy nothing, because the fold's
	// total cost over a scan is rows/64 popcounts per pair whatever the
	// chunk size. A partial final chunk folds only the words it used.
	blockChunkRows = 65536

	// maxBlockCandidates bounds the blocks written to the candidate
	// file, largest-first with a counted remainder.
	//
	// It deliberately shares maxRuleCandidates' value and is applied
	// PER DETECTOR rather than to the file as a whole. A shared budget
	// would let one detector's candidates crowd the other's out
	// entirely — twenty gating candidates and the analyst never learns
	// the cohort has question blocks — and the two are different KINDS
	// of finding rather than competing instances of one, so there is no
	// ranking between them to spend a shared budget on.
	maxBlockCandidates = maxRuleCandidates

	// maxNearBlockWarnings and maxAlwaysNullWarnings bound the two
	// report-only listings, worst-first with a counted remainder — the
	// shape maxThinLevelWarnings, maxResidualRecoveryPairs and
	// maxThinGateWarnings already set, for the same reason: the
	// truncated tail is by construction the least severe part of the
	// finding.
	maxNearBlockWarnings  = 20
	maxAlwaysNullWarnings = 20

	// comissingDetectorName is the RuleEvidence.Detector discriminant
	// for this detector, beside gatingDetectorName.
	comissingDetectorName = "co_missing"
)

// blockDetector is the per-row co-missing accumulator. It is nil unless
// ProfileOptions.SuggestRules asked for detection AND the cohort
// declares at least two nullable fields, so the per-row cost for every
// existing caller is exactly zero.
type blockDetector struct {
	// fields are the participants, in SCHEMA order — every ordering this
	// detector emits derives from it, never from a map walk, so a
	// candidate file is byte-reproducible.
	fields []blockField
	idx    map[string]int
	n      int

	// bits is the chunk bitset: n x words uint64s, field i occupying
	// [i*words, (i+1)*words). Bit b of word w is row (w*64 + b) of the
	// CURRENT chunk.
	bits  []uint64
	words int
	// nz marks the fields that set a bit in the current chunk, so the
	// fold skips the pairs a sparse chunk cannot contribute to.
	nz        []bool
	chunkRows int

	// nullN is each participant's null count and coNull[i*n+j] (i < j)
	// the rows on which both were null. Everything below is derived
	// from those two exactly: the overlap of i and j is
	// coNull / (nullN_i + nullN_j - coNull), which is 1 if and only if
	// the two null sets are identical.
	nullN  []int
	coNull []int

	rows int

	// over marks the detector abandoned for exceeding maxBlockFields.
	over      bool
	overCount int
}

// blockField is one participant.
type blockField struct {
	name string
	typ  string
}

// newBlockDetector admits every NULLABLE schema field. Nullability is an
// exact filter rather than a heuristic — a non-nullable field cannot
// contribute a null to any bitmap, so it can never be a block member —
// and it is knowable before the first row, which is what keeps the
// accumulator's width fixed for the whole scan.
func newBlockDetector(schema *encoding.Schema) *blockDetector {
	var fields []blockField
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if !f.Nullable {
			continue
		}
		fields = append(fields, blockField{name: f.Name, typ: f.Type.String()})
	}
	if len(fields) < 2 {
		return nil
	}
	if len(fields) > maxBlockFields {
		return &blockDetector{over: true, overCount: len(fields)}
	}
	n := len(fields)
	d := &blockDetector{
		fields: fields,
		idx:    make(map[string]int, n),
		n:      n,
		words:  blockChunkRows / 64,
		nz:     make([]bool, n),
		nullN:  make([]int, n),
		coNull: make([]int, n*n),
	}
	for i, f := range fields {
		d.idx[f.name] = i
	}
	d.bits = make([]uint64, n*d.words)
	return d
}

// observe folds one already-decoded row into the accumulator. It reads
// the `nulls` map profileRecords has just filled — which holds ONLY the
// row's null fields — and pulls no bytes of its own.
func (d *blockDetector) observe(nulls map[string]bool) {
	if d.over {
		return
	}
	row := d.chunkRows
	w, mask := row>>6, uint64(1)<<uint(row&63)
	for name := range nulls {
		i, ok := d.idx[name]
		if !ok {
			continue
		}
		d.nullN[i]++
		d.bits[i*d.words+w] |= mask
		d.nz[i] = true
	}
	d.chunkRows++
	d.rows++
	if d.chunkRows == blockChunkRows {
		d.foldChunk()
	}
}

// foldChunk accumulates the current chunk's pairwise overlaps into
// coNull by popcount and clears the bitsets.
//
// Only the words the chunk actually used are folded and cleared, so a
// 400-row cohort pays for 7 words per field rather than 1,024 — the
// buffers are sized for the cap, the work is sized for the data.
func (d *blockDetector) foldChunk() {
	if d.chunkRows == 0 {
		return
	}
	w := (d.chunkRows + 63) / 64
	for i := 0; i < d.n; i++ {
		if !d.nz[i] {
			continue
		}
		ai := d.bits[i*d.words : i*d.words+w]
		for j := i + 1; j < d.n; j++ {
			if !d.nz[j] {
				continue
			}
			aj := d.bits[j*d.words : j*d.words+w]
			c := 0
			for k := range ai {
				c += bits.OnesCount64(ai[k] & aj[k])
			}
			d.coNull[i*d.n+j] += c
		}
	}
	for i := 0; i < d.n; i++ {
		if !d.nz[i] {
			continue
		}
		clear(d.bits[i*d.words : i*d.words+w])
		d.nz[i] = false
	}
	d.chunkRows = 0
}

// co returns the rows on which both i and j were null.
//
// The diagonal is answered from nullN rather than from the matrix: the
// fold only writes the strict upper triangle (i < j), so co(i, i) read
// off the matrix would be 0 and a field's overlap with itself would come
// back as "shares nothing", which is how a block member's own agreement
// silently rendered as 0 in the evidence.
func (d *blockDetector) co(i, j int) int {
	if i == j {
		return d.nullN[i]
	}
	if i > j {
		i, j = j, i
	}
	return d.coNull[i*d.n+j]
}

// overlap is the share of the rows where EITHER field is null on which
// BOTH are — 1 exactly when the two null sets are identical, and about
// p/2 for two independent fields each null at rate p.
func (d *blockDetector) overlap(i, j int) float64 {
	c := d.co(i, j)
	union := d.nullN[i] + d.nullN[j] - c
	if union == 0 {
		return 0
	}
	return float64(c) / float64(union)
}

// blockClass is one set of fields sharing a null count, partitioned
// further by whether their null PATTERNS are identical.
type blockClass struct {
	members []int
	nullN   int
}

// finish turns the accumulator into candidate rules. It runs after the
// scan for the same reason the model fits and the gating candidates do:
// the classification is a property of the whole cohort and does not
// exist until every row has been seen.
func (d *blockDetector) finish(warnings *[]string) []RuleSpec {
	if d == nil {
		return nil
	}
	if d.over {
		*warnings = append(*warnings, blockAbandonedWarning(d.overCount))
		return nil
	}
	if d.rows == 0 {
		return nil
	}
	d.foldChunk()

	// Participants, in schema order. Two exclusions, and they are
	// different findings rather than one filter:
	//
	//   - never null: a field declared nullable that carried no null has
	//     no pattern to match. Ordinary, and silent.
	//   - ALWAYS null: it is not co-missing with anything, it is absent.
	//     Reported as its own finding, named with its type, and kept out
	//     of every block — forcing it into one would propose a rule
	//     nulling a column that is already entirely null.
	var parts []int
	var alwaysNull []int
	for i := range d.fields {
		switch d.nullN[i] {
		case 0:
			continue
		case d.rows:
			alwaysNull = append(alwaysNull, i)
		default:
			parts = append(parts, i)
		}
	}
	d.appendAlwaysNullWarnings(warnings, alwaysNull)
	if len(parts) < 2 {
		return nil
	}

	classes := d.classify(parts)

	// Ranking: the biggest block first, then the rows it covers, then
	// the first member's name. "Cleanest" is not a further tiebreak and
	// cannot be — every admitted block is exact, so its members' overlap
	// is 1 by the admission rule and there is no cleanliness to rank.
	emitted := make([]blockClass, 0, len(classes))
	for _, c := range classes {
		if len(c.members) >= 2 {
			emitted = append(emitted, c)
		}
	}
	sort.SliceStable(emitted, func(i, j int) bool {
		a, b := emitted[i], emitted[j]
		if len(a.members) != len(b.members) {
			return len(a.members) > len(b.members)
		}
		if a.nullN != b.nullN {
			return a.nullN > b.nullN
		}
		return d.fields[a.members[0]].name < d.fields[b.members[0]].name
	})

	kept := emitted
	if len(kept) > maxBlockCandidates {
		kept = kept[:maxBlockCandidates]
	}
	out := make([]RuleSpec, 0, len(kept))
	for _, c := range kept {
		spec := d.buildBlockCandidate(c)
		out = append(out, spec)
		// A thin block SHIPS with its support attached, exactly as a
		// thin gate candidate does and for the same reason: the analyst
		// is better placed than the threshold to judge whether eight
		// rows are a question block. The listing needs no cap of its own
		// — it is already bounded by maxBlockCandidates.
		if w := blockThinWarning(spec.NullTogether[0], len(c.members), spec.Evidence.MinLevelSupport); w != "" {
			*warnings = append(*warnings, w)
		}
	}
	if rest := len(emitted) - len(kept); rest > 0 {
		*warnings = append(*warnings, blockTruncationWarning(rest, len(kept)))
	}

	d.appendNearBlockWarnings(warnings, classes)
	return out
}

// classify partitions the participants by null COUNT and then by null
// PATTERN, in schema order.
//
// The two tests are written as two terms of one condition on purpose,
// because dropping the second is the whole failure mode this detector
// exists to avoid and the falsification is exactly that edit: keep
// `d.nullN[rep] == d.nullN[i]` alone and two independent fields that
// merely share a rate become one question block.
//
// Greedy first-match assignment is exact rather than order-dependent:
// "null on exactly the same rows" is an equivalence relation, so a field
// matching any member of a class matches every member of it.
func (d *blockDetector) classify(parts []int) []blockClass {
	var classes []blockClass
	for _, i := range parts {
		placed := false
		for ci := range classes {
			rep := classes[ci].members[0]
			if d.nullN[rep] == d.nullN[i] && d.co(rep, i) == d.nullN[i] {
				classes[ci].members = append(classes[ci].members, i)
				placed = true
				break
			}
		}
		if !placed {
			classes = append(classes, blockClass{members: []int{i}, nullN: d.nullN[i]})
		}
	}
	return classes
}

// buildBlockCandidate assembles one null_together proposal and its
// evidence.
//
// The rule carries NO `when`. An absent `when` means EVERY ROW (see
// RuleSpec.When), which is the correct statement here: the block's
// members are null together on every row of the cohort, both the rows
// where the block is null and the rows where it is present, and a
// null_together whose gate is the first member's own decision needs no
// predicate to say that.
func (d *blockDetector) buildBlockCandidate(c blockClass) RuleSpec {
	members := make([]string, 0, len(c.members))
	ev := &RuleEvidence{
		Detector:        comissingDetectorName,
		Note:            blockCandidateNote(len(c.members), c.nullN),
		RowsObserved:    d.rows,
		RowsAffected:    c.nullN,
		GatedShare:      float64(c.nullN) / float64(d.rows),
		MinLevelSupport: min(c.nullN, d.rows-c.nullN),
	}
	ev.ThinSupport = ev.MinLevelSupport < minGateLevelSupport
	for _, i := range c.members {
		members = append(members, d.fields[i].name)
		ev.Block = append(ev.Block, RuleEvidenceBlockMember{
			Field:     d.fields[i].name,
			Type:      d.fields[i].typ,
			NullCount: d.nullN[i],
			NullRate:  float64(d.nullN[i]) / float64(d.rows),
			Agreement: d.overlap(c.members[0], i),
		})
	}
	// Exactly E1-S5's own divergence measure, over the members whose
	// null_rate null_together is about to discard. It is 0 by
	// construction here — identical patterns have identical counts — and
	// that is the point: it is the checkable form of "no declared rate
	// is being thrown away", the one cost E1-S5 documents.
	maxDev := 0.0
	for _, m := range ev.Block {
		if dev := m.NullRate - ev.GatedShare; dev > maxDev {
			maxDev = dev
		} else if -dev > maxDev {
			maxDev = -dev
		}
	}
	ev.MaxNullRateDeviation = maxDev
	return RuleSpec{NullTogether: members, Evidence: ev}
}

func blockCandidateNote(members, rows int) string {
	return fmt.Sprintf(
		"measured, not asserted: these %d field(s) are null on exactly the same %d row(s) — identical null PATTERN, "+
			"not merely an identical null rate, which two unrelated fields can share by coincidence. "+
			"null_together copies the FIRST member's null decision onto the rest and IGNORES every other member's own null_rate; "+
			"every member here carries the same rate (max_null_rate_deviation 0), so no declared rate is discarded and the member order is arbitrary by construction. "+
			"Declaration order is applied order: keep this rule AFTER any gating rule naming the same fields, because the block copies whatever the first member holds when it runs.",
		members, rows)
}

func blockThinWarning(first string, members, n int) string {
	return thinSupportWarning("co-missing block", first, fmt.Sprintf("%d field(s)", members), n,
		minGateLevelSupport,
		"the candidate still ships and carries its support; judge it on the null counts in its `_evidence`")
}

func blockAbandonedWarning(n int) string {
	return fmt.Sprintf(
		"rule suggestion: co-missing block detection was not run — the cohort declares %d nullable field(s), "+
			"more than the %d the pairwise accumulator carries; a truncated field set yields blocks that are exact among "+
			"the fields it kept and silently missing the rest, which is the defect this detector exists to remove",
		n, maxBlockFields)
}

func blockTruncationWarning(remaining, kept int) string {
	return fmt.Sprintf(
		"rule suggestion: +%d further co-missing block(s) not written; the file carries the %d largest by member count then rows covered",
		remaining, kept)
}

// appendAlwaysNullWarnings reports the columns that are null on every
// row — their own finding, named with their type.
//
// They are NOT proposed as a rule. A rule is available (a `set_null`
// with no `when` nulls a field on every row) and is deliberately not
// emitted: what generation should do about a column with no observations
// at all is a separate decision from detecting it, and this file's whole
// contract is that a candidate is a claim a human reviews. The column is
// named so the analyst can see that generation is today fabricating a
// distribution for it off a marginal computed over zero rows.
func (d *blockDetector) appendAlwaysNullWarnings(warnings *[]string, idx []int) {
	if len(idx) == 0 {
		return
	}
	shown := idx
	if len(shown) > maxAlwaysNullWarnings {
		shown = shown[:maxAlwaysNullWarnings]
	}
	for _, i := range shown {
		*warnings = append(*warnings, alwaysNullWarning(d.fields[i].name, d.fields[i].typ, d.rows))
	}
	if rest := len(idx) - len(shown); rest > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"always-null column: +%d further column(s) null on all %d rows profiled", rest, d.rows))
	}
}

func alwaysNullWarning(field, typ string, rows int) string {
	return fmt.Sprintf(
		"always-null column %q (%s): null on all %d row(s) profiled, so its marginal is summarised over zero "+
			"observations and generation fabricates a distribution for it; it has no gate and no block, and is not proposed as a rule",
		field, typ, rows)
}

// nearBlockPair is one class pair reported for near — but not exact —
// co-missingness.
type nearBlockPair struct {
	a, b      int
	sizeA     int
	sizeB     int
	agreement float64
	disagree  int
}

// appendNearBlockWarnings reports the pairs that ALMOST form a block.
//
// The comparison is between class REPRESENTATIVES rather than between
// every pair of fields, and that is a correctness property as well as a
// volume one: within a class the null patterns are identical, so every
// cross-class field pair has the SAME overlap as its representatives'
// and listing them individually would restate one finding
// |A| x |B| times — 650 lines for the motivating cohort's 50-field and
// 13-field blocks alone, which is the shape E3-S1's co-missing count
// exists to avoid.
//
// Nothing is emitted on the strength of these. null_together has no
// dial for "almost": applying it to a pair that disagrees on 4,700 rows
// silently rewrites those rows, and unlike a gating candidate — where
// the analyst corrects the `when` — there is nothing here to correct.
// The finding is the two field names and the number, which is enough to
// go and look.
func (d *blockDetector) appendNearBlockWarnings(warnings *[]string, classes []blockClass) {
	var pairs []nearBlockPair
	for i := 0; i < len(classes); i++ {
		for j := i + 1; j < len(classes); j++ {
			a, b := classes[i].members[0], classes[j].members[0]
			ag := d.overlap(a, b)
			if ag < nearBlockAgreement {
				continue
			}
			pairs = append(pairs, nearBlockPair{
				a: a, b: b,
				sizeA:     len(classes[i].members),
				sizeB:     len(classes[j].members),
				agreement: ag,
				disagree:  d.nullN[a] + d.nullN[b] - 2*d.co(a, b),
			})
		}
	}
	if len(pairs) == 0 {
		return
	}
	// Nearest first: the closest miss is the one most likely to be a
	// real block with a coding fault in it.
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].agreement > pairs[j].agreement })
	shown := pairs
	if len(shown) > maxNearBlockWarnings {
		shown = shown[:maxNearBlockWarnings]
	}
	for _, p := range shown {
		*warnings = append(*warnings, nearBlockWarning(
			d.fields[p.a].name, p.sizeA, d.fields[p.b].name, p.sizeB, p.agreement, p.disagree))
	}
	if rest := len(pairs) - len(shown); rest > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"rule suggestion: +%d further near co-missing pair(s) above %.2f agreement not listed; "+
				"the listing carries the %d nearest", rest, nearBlockAgreement, len(shown)))
	}
}

func nearBlockWarning(a string, sizeA int, b string, sizeB int, agreement float64, disagree int) string {
	return fmt.Sprintf(
		"rule suggestion: %s and %s are null together on %.4f of the rows where either is null (%d row(s) disagree) "+
			"but not on exactly the same rows, so they are NOT proposed as one block — null_together would rewrite "+
			"the disagreeing rows and has no way to say \"almost\"",
		blockSideLabel(a, sizeA), blockSideLabel(b, sizeB), agreement, disagree)
}

func blockSideLabel(field string, size int) string {
	if size > 1 {
		return fmt.Sprintf("%q (a block of %d)", field, size)
	}
	return fmt.Sprintf("%q", field)
}
