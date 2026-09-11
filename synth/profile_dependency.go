package synth

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/encoding"
)

// This file is the profile-time EXACT-DEPENDENCY detector, the third
// candidate kind on the pipeline `profile create --suggest-rules` built
// (synth/profile_gating.go, synth/profile_comissing.go). The first two
// read NULL STATE only — which field's levels gate another's absence,
// which fields are absent together — and E3-S1 said so in as many words.
// This one reads VALUES: a field that is an exact function of ONE other
// field on every row where both are present, proposed as a `set_expr`.
//
// It exists because `promoter` is `nps >= 9`. Generation samples it from
// its own marginal and gives it a captured linear model, so the
// synthetic cohort contains promoters with a score of 3 — and the
// profile document has been carrying the proof the whole time
// (`promoter + passive + detractor = 1.0000`, exactly) with nothing
// reading it.
//
// # THE SEARCH IS NARROW, DELIBERATELY, AND THE BOUNDS ARE PUBLISHED
//
// A general functional-dependency search over 122 fields is quadratic in
// the field count, mostly finds noise, and would have to be believed on
// the strength of a claim nobody can check. What is searched is stated
// here, in every candidate's `_evidence.note`, on stderr and in the
// docs, so a reader knows what was NOT looked for:
//
//   - TARGETS are `packed_bool` and `u4` ONLY — a boolean flag or a
//     small integer. A categorical target is excluded even though
//     `set_expr` could write one: a derived CATEGORY is a different
//     authoring shape (it needs the declared domain, and a wrong arm
//     silently grows the dictionary) and it is not the motivating case.
//   - SOURCES are the low-cardinality types `gateCandidateFields`
//     already admits — `categorical_*`, `packed_bool`, `u4` — with at
//     most maxDepLevels OBSERVED levels.
//   - The dependency is on exactly ONE source. A target determined
//     JOINTLY by two fields is not looked for; that search is the
//     product of two level sets per pair and the rendering has no
//     readable form.
//   - Wider numerics (`u8`+, `f32`/`f64`, `decimal128`), `date` and
//     `set_*` are neither source nor target.
//
// Claiming generality the implementation does not have would be worse
// than the bound: an analyst who believes a field was checked and
// cleared stops looking.
//
// # DETECTION IS NOT EXPRESSION, and the boundaries are DISCOVERED
//
// What is measured is a LOOKUP — source level to target value. Rendering
// it as `round(nps) >= 9` rather than a nine-way disjunction is a
// separate judgement, made in depRender below. The threshold form is
// preferred wherever the mapping's value regions are CONTIGUOUS in the
// source's own order, and the band edges are read off the data: the NPS
// 9-10 / 7-8 / 0-6 definition is the detector's OUTPUT on this cohort,
// never an input. A detector that hardcodes the standard definition is
// not a detector.
//
// The threshold form also earns its place on a property the enumeration
// does not have: it is a TOTAL function of the source. A source value
// generation produces that the cohort never carried falls into the
// nearest band instead of falling off the end of an enumeration, which
// matters because a numeric marginal reconstructed from min/max can
// produce an interior value the source never had.
//
// # It rides the existing scan
//
// The per-row hook is one level lookup per participant plus one array
// compare per LIVE pair; no second pass and no extra byte, which
// TestSuggestDeps_RidesTheExistingScan measures by counting bytes pulled
// exactly as the model capture's own gate does.
//
// The pair count starts at F^2 and that is affordable only because a
// pair DIES the moment it is contradicted: two rows sharing a source
// level and disagreeing on the target. On real data the overwhelming
// majority die within the first few dozen rows, so the live list is a
// handful long for the remaining 99.99% of the scan. Pairs are held in a
// per-source list that is filtered in place, so compaction is free and
// a row on which the source is NULL skips that source's whole list.
//
// # What it reports rather than proposes
//
//   - An ALMOST-determined pair (at least one and at most
//     maxDepExceptions contradicting rows) is REPORTED with its
//     exception count, never emitted. It follows E3-S2's near-block
//     precedent and for the same reason: the relationship is measurably
//     FALSE on some rows, `set_expr` has no dial for "almost", and
//     applying one silently rewrites exactly the rows that disagreed.
//     Unlike a gating candidate — whose `when` an analyst corrects —
//     there is nothing in it to fix.
//   - A pair that IS exactly determined but whose two fields do not
//     share a null pattern is reported and not emitted, because the rule
//     would be unfaithful in the other direction: a `set_expr` CLEARS
//     the target's null mask, so it would un-null the target on every
//     row the source is absent from.
//   - A CONSTANT column is its own finding. It is determined by every
//     other field and by none of them, so it is excluded from both roles
//     rather than proposed 117 times.
const (
	// maxDepLevels is the SOURCE CARDINALITY CEILING, and the bound on
	// every level array in this file.
	//
	// It deliberately shares maxGateLevels' value rather than getting
	// its own, and the reasoning is the one minGateLevelSupport =
	// MinPairObservations uses: this is the SAME question that constant
	// already answers — how many arms can a branch have before it stops
	// being a branch — asked of a `set_expr` instead of a `when`. A
	// 40-arm disjunction is not a derived field; it is a lookup table
	// someone would have to check by hand. 16 is also the full range of
	// a u4, so every u4 in a cohort is considered whole.
	//
	// Cardinality is only known as the scan runs, so a field is
	// ABANDONED the moment it exceeds the cap — exact rather than
	// order-dependent, since observed cardinality is monotone. Abandoned
	// rather than truncated for maxGateLevels' reason: a partial level
	// map yields a mapping that is exact over an arbitrary subset of the
	// cohort, which looks like a measurement and is not one.
	//
	// As a bound on the TARGET side it is structurally unreachable — a
	// packed_bool has 2 values and a u4 has 16 — and is kept so the
	// fixed-width arrays cannot overflow if the admitted target set ever
	// widens.
	maxDepLevels = maxGateLevels

	// maxDepFields caps the participants, because the pair count is
	// F x (F-1) and the accumulator is allocated for all of them.
	//
	// It shares maxBlockFields' value for the same reason that one
	// exists: a CPU bound before a memory bound. At the cap the pair
	// table is 256 x 255 mappings of maxDepLevels int32s — about 6 MB,
	// flat in the row count — and the first rows of the scan pay 65,024
	// array compares each before the contradiction pass thins them.
	// Over the cap the detector is ABANDONED, never truncated: a
	// truncated field set yields dependencies that are exact among the
	// fields it kept while silently missing every relationship involving
	// the rest, and a reader has no way to tell which.
	maxDepFields = maxBlockFields

	// maxDepExceptions is the largest number of contradicting rows a
	// pair may carry and still be REPORTED as an almost-dependency. Over
	// it the pair is dropped entirely and is not reported, because it is
	// not "almost" anything.
	//
	// It is small on purpose. The story this reports is "a handful of
	// rows disagree" — a coding fault, a partial re-ask, a merged wave —
	// which is exactly the shape E3-S2's near-block measure exists to
	// surface (a block plus three stray nulls in 381,324 rows). A loose
	// bound would instead report ordinary ASSOCIATION: on a cohort of
	// rare binary flags, two independent flags each true on 50 rows
	// contradict each other on only 50, and a threshold above that turns
	// every such pair into a "near dependency" with nothing to do about
	// it.
	//
	// It is also what keeps the live pair list short, since a pair is
	// dropped the moment it exceeds this. That is a consequence, not the
	// reason: the reason is that 200 contradicting rows is a different
	// finding from 3 and must not share a word with it.
	maxDepExceptions = 8

	// maxDepCandidates bounds the dependency candidates written to the
	// file, strongest-first with a counted remainder. It shares
	// maxRuleCandidates' value and is applied PER DETECTOR, exactly as
	// maxBlockCandidates is and for the same reason: a shared budget
	// would let one detector's candidates crowd another's out entirely,
	// and the three are different KINDS of finding rather than competing
	// instances of one.
	maxDepCandidates = maxRuleCandidates

	// maxNearDepWarnings bounds each report-only listing, worst-first
	// with a counted remainder — the shape maxThinLevelWarnings,
	// maxNearBlockWarnings and maxThinGateWarnings already set.
	maxNearDepWarnings = 20

	// maxConstantFieldWarnings bounds the constant-column listing.
	maxConstantFieldWarnings = 20

	// dependencyDetectorName is the RuleEvidence.Detector discriminant
	// for this detector, beside gatingDetectorName and
	// comissingDetectorName.
	dependencyDetectorName = "dependency"

	// The closed vocabulary of RuleEvidenceDependency.Form — how the
	// measured lookup was RENDERED, which is the judgement a reader most
	// needs to check and the one thing the expression itself does not
	// say.
	depFormThreshold      = "threshold"       // contiguous bands of a numeric source, boolean target
	depFormThresholdChain = "threshold_chain" // contiguous bands of a numeric source, numeric target
	depFormMembership     = "membership"      // an equality set over a categorical source, boolean target
	depFormComplement     = "membership_complement"
	depFormEnumeration    = "enumeration_chain" // an equality set over a categorical source, numeric target
)

// depField is one participant: a low-cardinality field, with the level
// table discovered online.
type depField struct {
	name        string
	typ         string
	categorical bool
	dict        *encoding.Dictionary
	// source and target record which ROLES the field's type admits it
	// to. A categorical is a source only; packed_bool and u4 are both.
	source bool
	target bool

	// levels is the observed level table: index -> spelling. For a
	// numeric field the spelling is the decimal integer, matching the
	// gating detector's own convention so one document's level names
	// mean one thing.
	levels   []string
	levelIdx map[string]int32
	levelN   []int

	// over marks the field abandoned for exceeding maxDepLevels.
	over bool

	nullN int
}

// numericLevel parses a numeric field's level spelling back to the
// integer it was formatted from.
func (f *depField) numericLevel(idx int32) int64 {
	v, _ := strconv.ParseInt(f.levels[idx], 10, 64)
	return v
}

// depPair is the accumulator for one ORDERED pair (source, target).
//
// Per SOURCE level it holds the two most recently distinct target values
// seen with it and how many rows carried each, so `exceptions` is the
// sum over levels of the SMALLER count: the rows that would have to be
// rewritten for the pair to be a function.
//
// TWO SLOTS RATHER THAN ONE, AND THAT IS NOT AN OPTIMISATION. A single
// first-observation slot answers the EXACT question perfectly — a second
// value at any level means "not a function" whichever order the rows
// arrived in — and gets the NEAR question badly wrong, in the direction
// that loses the finding: if the minority value happens to arrive FIRST,
// the count comes back as the majority's. Measured on this story's own
// fixture, a target agreeing with its source on all but three of 880
// rows scored 80 exceptions and was dropped as unrelated, because the
// first row carrying that source level was one of the three. The near
// report exists precisely to surface a handful of contradicting rows, so
// an order-sensitive count is not a caveat on it; it is its failure.
//
// Both counts saturate at 255. That is exact for every question asked of
// them: only the SMALLER count is ever read, and a pair whose smaller
// count could reach 255 exceeded maxDepExceptions long ago and is no
// longer alive.
//
// A source level showing a THIRD distinct target value kills the pair
// outright rather than growing a slot. Two values at a level is the
// shape a coding fault takes (a flag flipped on a handful of rows); a
// third is a lookup, not a near miss, and the near listing is a report
// whose job is to be short.
type depPair struct {
	v0, v1     [maxDepLevels]int8
	n0, n1     [maxDepLevels]uint8
	coPresent  int
	exceptions int
}

// observe folds one co-present row into the pair and reports whether it
// is still worth carrying.
//
// exceptions is maintained incrementally as the sum over levels of
// min(n0, n1): incrementing one count raises that level's minimum by
// exactly one when the incremented count is the smaller of the two
// afterwards, and by nothing otherwise.
func (p *depPair) observe(l int, v int8) bool {
	p.coPresent++
	switch {
	case p.v0[l] < 0:
		p.v0[l], p.n0[l] = v, 1
	case p.v0[l] == v:
		if p.n0[l] < 255 {
			p.n0[l]++
		}
		if p.v1[l] >= 0 && p.n0[l] <= p.n1[l] {
			p.exceptions++
		}
	case p.v1[l] < 0:
		p.v1[l], p.n1[l] = v, 1
		p.exceptions++
	case p.v1[l] == v:
		if p.n1[l] < 255 {
			p.n1[l]++
		}
		if p.n1[l] <= p.n0[l] {
			p.exceptions++
		}
	default:
		p.exceptions = maxDepExceptions + 1
	}
	return p.exceptions <= maxDepExceptions
}

// value returns the target value a source level maps to — the majority
// of the two tracked, which for every EMITTED candidate is the only one
// there is (exceptions == 0 means no level ever saw a second value).
func (p *depPair) value(l int) int32 {
	if p.v1[l] >= 0 && p.n1[l] > p.n0[l] {
		return int32(p.v1[l])
	}
	return int32(p.v0[l])
}

func (p *depPair) seen(l int) bool { return p.v0[l] >= 0 }

// depDetector is the per-row accumulator. It is nil unless
// ProfileOptions.SuggestRules asked for detection AND the cohort carries
// at least one admissible source and one admissible target, so the
// per-row cost for every existing caller is exactly zero.
type depDetector struct {
	fields []depField
	n      int

	// pairs is the flat n x n table, addressed source*n + target. Only
	// the (source, target) cells whose ROLES admit them are ever
	// touched; the rest are dead weight in exchange for an index
	// calculation with no indirection.
	pairs []depPair
	// live[s] is the still-possible targets of source s, filtered in
	// place every row so compaction costs nothing and a NULL source
	// skips its whole list.
	live [][]int32

	// lvl is the per-row scratch of level indices, reused across rows so
	// the hook allocates nothing. -1 means "no usable level": null, an
	// unresolvable dictionary id, or an abandoned field.
	lvl []int32

	rows int

	// over marks the detector abandoned for exceeding maxDepFields.
	over      bool
	overCount int
}

// depCandidateFields returns the participants in SCHEMA order, each
// flagged with the roles its type admits. Every ordering this detector
// emits derives from this one, never from a map walk, so a candidate
// file is byte-reproducible.
func depCandidateFields(schema *encoding.Schema) []depField {
	var out []depField
	for i := range schema.Fields {
		f := &schema.Fields[i]
		switch {
		case f.Type.IsCategorical():
			if f.Dictionary == nil {
				continue
			}
			out = append(out, depField{
				name: f.Name, typ: f.Type.String(), categorical: true,
				dict: f.Dictionary, source: true,
			})
		case f.Type == encoding.FieldTypePackedBool, f.Type == encoding.FieldTypeU4:
			out = append(out, depField{
				name: f.Name, typ: f.Type.String(), source: true, target: true,
			})
		}
	}
	return out
}

func newDepDetector(schema *encoding.Schema) *depDetector {
	fields := depCandidateFields(schema)
	// An ordered (source, target) pair needs two distinct participants,
	// at least one of which the target roles admit. Everything narrower
	// is nil, so the per-row cost for a cohort with no low-cardinality
	// fields is exactly zero.
	targets := 0
	for i := range fields {
		if fields[i].target {
			targets++
		}
	}
	if targets == 0 || len(fields) < 2 {
		return nil
	}
	if len(fields) > maxDepFields {
		return &depDetector{over: true, overCount: len(fields)}
	}
	n := len(fields)
	d := &depDetector{fields: fields, n: n}
	for i := range d.fields {
		d.fields[i].levelIdx = make(map[string]int32, maxDepLevels+1)
	}
	d.pairs = make([]depPair, n*n)
	d.lvl = make([]int32, n)
	d.live = make([][]int32, n)
	any := false
	for s := 0; s < n; s++ {
		if !d.fields[s].source {
			continue
		}
		var targets []int32
		for t := 0; t < n; t++ {
			if t == s || !d.fields[t].target {
				continue
			}
			targets = append(targets, int32(t))
			p := &d.pairs[s*n+t]
			for l := range p.v0 {
				p.v0[l], p.v1[l] = -1, -1
			}
		}
		if len(targets) > 0 {
			any = true
		}
		d.live[s] = targets
	}
	if !any {
		return nil
	}
	return d
}

// observe folds one already-decoded row into the accumulators. It reads
// the same two maps profileRecords has just filled and pulls no bytes of
// its own.
func (d *depDetector) observe(values map[string]float64, nulls map[string]bool) {
	if d.over {
		return
	}
	d.rows++

	for i := range d.fields {
		f := &d.fields[i]
		if nulls[f.name] {
			f.nullN++
			d.lvl[i] = -1
			continue
		}
		if f.over {
			d.lvl[i] = -1
			continue
		}
		var key string
		if f.categorical {
			// An id the dictionary cannot resolve is treated as absent,
			// exactly as catAcc and the gating detector treat it, so the
			// sections of one document cannot disagree about which rows
			// a category was observed on.
			key = f.dict.Resolve(uint32(values[f.name]))
			if key == "" {
				d.lvl[i] = -1
				continue
			}
		} else {
			key = strconv.FormatInt(int64(values[f.name]), 10)
		}
		idx, ok := f.levelIdx[key]
		if !ok {
			if len(f.levels) >= maxDepLevels {
				f.over = true
				d.lvl[i] = -1
				continue
			}
			idx = int32(len(f.levels))
			f.levels = append(f.levels, key)
			f.levelN = append(f.levelN, 0)
			f.levelIdx[key] = idx
		}
		f.levelN[idx]++
		d.lvl[i] = idx
	}

	base := 0
	for s := 0; s < d.n; s++ {
		targets := d.live[s]
		if len(targets) == 0 {
			base += d.n
			continue
		}
		ls := d.lvl[s]
		if ls < 0 {
			// The source is absent (or abandoned) on this row: nothing
			// about any of its pairs can be learned, and skipping the
			// whole list is what makes a null-heavy source free.
			if d.fields[s].over {
				d.live[s] = nil
			}
			base += d.n
			continue
		}
		w := 0
		for _, t := range targets {
			lt := d.lvl[t]
			if lt < 0 {
				if d.fields[t].over {
					continue
				}
				targets[w] = t
				w++
				continue
			}
			if !d.pairs[base+int(t)].observe(int(ls), int8(lt)) {
				// Dropped, not merely flagged: over the bound it is not
				// an almost-dependency and keeping it alive would only
				// cost the rest of the scan.
				continue
			}
			targets[w] = t
			w++
		}
		d.live[s] = targets[:w]
		base += d.n
	}
}

// depGroup is one proposal before it becomes a RuleSpec: every target a
// single source determines, in one rule.
//
// Grouping by SOURCE is what satisfies the partition case without a
// special case for it. promoter / passive / detractor are three separate
// measurements against `nps`, and three separate rules would have to be
// kept consistent by hand — delete one and the remaining two silently
// stop partitioning. One rule per source is also the form E2-S4's
// canonical snippet already takes, so the emitted file and the
// hand-written documentation agree.
type depGroup struct {
	src        int
	targets    []int
	exprs      map[string]string
	deps       []RuleEvidenceDependency
	coPresent  int
	minSupport int
	// block is the null_together member list when the source and every
	// target share one non-empty null pattern; nil when nothing is ever
	// null — a block over two never-null fields would copy a decision
	// nobody made.
	block []string
}

// finish turns the accumulators into candidate rules. It runs after the
// scan for the same reason the model fits and the other two detectors
// do: the classification is a property of the whole cohort and does not
// exist until every row has been seen.
//
// blocks is E3-S2's co-missing accumulator, READ rather than duplicated.
// The admission rule below needs "are these two fields null on exactly
// the same rows", which is the exact question that detector's pairwise
// co-null matrix already answers; a second accumulator computing the
// same number is the shape E1-S6 exists because of. It may be nil or
// abandoned, in which case only the never-null case can be admitted —
// stated rather than guessed.
func (d *depDetector) finish(warnings *[]string, blocks *blockDetector) []RuleSpec {
	if d == nil {
		return nil
	}
	if d.over {
		*warnings = append(*warnings, depAbandonedWarning(d.overCount))
		return nil
	}
	if d.rows == 0 {
		return nil
	}

	d.reportExcludedFields(warnings)
	check := d.exprChecker()

	var groups []depGroup
	var near []depNearPair
	var unexpressible []depNearPair
	for s := 0; s < d.n; s++ {
		g, n, u := d.candidateFor(s, blocks, check)
		if g != nil {
			groups = append(groups, *g)
		}
		near = append(near, n...)
		unexpressible = append(unexpressible, u...)
	}

	// Ranking. Target COUNT leads, then the co-present rows the mapping
	// rests on — the same shape the gating detector ranks by, and for
	// the same reason: the analyst reviews these by hand, and the
	// biggest structural claim is the one they most need to get right.
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if len(a.targets) != len(b.targets) {
			return len(a.targets) > len(b.targets)
		}
		if a.coPresent != b.coPresent {
			return a.coPresent > b.coPresent
		}
		return d.fields[a.src].name < d.fields[b.src].name
	})

	// ONE RULE PER TARGET. A field determined by two different sources
	// would otherwise be written twice, by two rules, of which only the
	// later survives under last-write-wins — the earlier rule is then a
	// candidate that validates, compiles, fires and has no effect, which
	// is the silent-inertness class this whole effort exists to remove.
	// The strongest group keeps the target; the others lose it, and a
	// group that loses every target disappears.
	claimed := make(map[int]int, len(groups))
	kept := groups[:0]
	contested := 0
	for gi := range groups {
		g := groups[gi]
		var survivors []int
		for _, t := range g.targets {
			if _, taken := claimed[t]; taken {
				contested++
				continue
			}
			claimed[t] = g.src
			survivors = append(survivors, t)
		}
		if len(survivors) == 0 {
			continue
		}
		kept = append(kept, d.restrict(g, survivors))
	}
	groups = kept
	if contested > 0 {
		*warnings = append(*warnings, depContestedWarning(contested))
	}

	d.appendMutualWarnings(warnings, groups)

	out := make([]RuleSpec, 0, min(len(groups), maxDepCandidates))
	var thin []depGroup
	for _, g := range groups {
		if len(out) >= maxDepCandidates {
			break
		}
		spec := d.buildCandidate(g)
		out = append(out, spec)
		if spec.Evidence.ThinSupport {
			thin = append(thin, g)
		}
	}
	if rest := len(groups) - len(out); rest > 0 {
		*warnings = append(*warnings, depTruncationWarning(rest, len(out)))
	}
	d.appendThinWarnings(warnings, thin)
	d.appendNearWarnings(warnings, near)
	d.appendUnexpressibleWarnings(warnings, unexpressible)
	return out
}

// depNearPair is one reported-but-not-emitted pair.
type depNearPair struct {
	src, tgt   int
	coPresent  int
	exceptions int
	// sourceNull and targetNull explain an EXACT pair that cannot be
	// expressed because the two are not absent on the same rows.
	//
	// exceptions carries a SENTINEL -1 on this list, and only here: the
	// pair is exact (so a real count would be 0) and was dropped because
	// its rendered expression does not compile, which is a different
	// finding with a different line. The two share a list because both
	// answer "this IS a dependency and it is still not proposed", which
	// is the question a reader is asking.
	sourceNull int
	targetNull int
}

// candidateFor classifies every live target of one source and assembles
// at most one group from the survivors.
func (d *depDetector) candidateFor(s int, blocks *blockDetector, check func(string, string) error) (*depGroup, []depNearPair, []depNearPair) {
	sf := &d.fields[s]
	if sf.over || !sf.source || len(sf.levels) < 2 {
		// An abandoned or CONSTANT source determines nothing usefully: a
		// single observed level splits nothing, and a level map that
		// stopped growing is a mapping over an arbitrary subset.
		return nil, nil, nil
	}

	var near, unexpressible []depNearPair
	g := depGroup{src: s, exprs: map[string]string{}, minSupport: 1 << 62}
	blockMembers := []string{sf.name}
	needBlock := false
	base := s * d.n
	for _, t32 := range d.live[s] {
		t := int(t32)
		tf := &d.fields[t]
		if tf.over {
			continue
		}
		p := &d.pairs[base+t]
		if p.coPresent == 0 {
			continue
		}
		levels := d.observedLevels(p, sf)
		if len(levels) < 2 {
			// The source took a single level on the co-present rows, so
			// it splits nothing there whatever its global cardinality.
			continue
		}
		if p.exceptions > 0 {
			near = append(near, depNearPair{src: s, tgt: t, coPresent: p.coPresent, exceptions: p.exceptions})
			continue
		}

		// RENDER BEFORE THE NULL-SHAPE CHECK, and the order is not
		// cosmetic. render's band guard is the ONE place a CONSTANT
		// target is refused (see render), and a constant target is not a
		// dependency at all — so it must be dropped before anything
		// describes it as one. Run the other way round, every constant
		// column in the cohort is reported once per source as "an exact
		// function of X, not proposed because the null patterns differ",
		// which is true, useless, and O(sources) lines long. Found by
		// falsifying the excluded-field reporting, which surfaced twelve
		// such lines on a thirteen-field fixture.
		src, form, dep, ok := d.render(sf, tf, p, levels)
		if !ok {
			continue
		}

		same, known := d.sameNullPattern(s, t, blocks)
		if !known || !same {
			unexpressible = append(unexpressible, depNearPair{
				src: s, tgt: t, coPresent: p.coPresent,
				sourceNull: sf.nullN, targetNull: tf.nullN,
			})
			continue
		}
		if err := check(tf.name, src); err != nil {
			// Compiled against the rule layer's OWN environment before
			// it is written, never a hand-rolled identifier check, so a
			// candidate the loader would refuse is dropped here instead.
			// E3-S1 found this the hard way: a field legally named `in`
			// yields valid JSON that the loader rejects.
			unexpressible = append(unexpressible, depNearPair{src: s, tgt: t, coPresent: p.coPresent, exceptions: -1})
			continue
		}
		dep.Form = form
		g.targets = append(g.targets, t)
		g.exprs[tf.name] = src
		g.deps = append(g.deps, dep)
		g.coPresent = p.coPresent
		blockMembers = append(blockMembers, tf.name)
		if sf.nullN > 0 {
			needBlock = true
		}
	}
	if len(g.targets) == 0 {
		return nil, near, unexpressible
	}
	for _, l := range d.observedLevels(&d.pairs[base+g.targets[0]], sf) {
		if n := sf.levelN[l]; n < g.minSupport {
			g.minSupport = n
		}
	}
	if needBlock {
		g.block = blockMembers
	}
	return &g, near, unexpressible
}

// sameNullPattern answers whether the source and target are null on
// exactly the same rows.
//
// The never-null case is answered from this detector's own counts, so a
// cohort with no nullable fields at all needs no co-null matrix. Every
// other case defers to E3-S2's accumulator, and an unavailable one
// (absent, or abandoned over maxBlockFields) returns known=false rather
// than a guess — the candidate is then reported, not emitted.
func (d *depDetector) sameNullPattern(s, t int, blocks *blockDetector) (same, known bool) {
	if d.fields[s].nullN == 0 && d.fields[t].nullN == 0 {
		return true, true
	}
	return blocks.sameNullPattern(d.fields[s].name, d.fields[t].name)
}

// observedLevels returns the source level indices carrying a mapping, in
// the source's own order: numerically for a numeric source, lexically
// for a categorical one. Never map order.
func (d *depDetector) observedLevels(p *depPair, sf *depField) []int32 {
	var out []int32
	for l := 0; l < len(sf.levels); l++ {
		if p.seen(l) {
			out = append(out, int32(l))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !sf.categorical {
			av, bv := sf.numericLevel(a), sf.numericLevel(b)
			if av != bv {
				return av < bv
			}
		}
		return sf.levels[a] < sf.levels[b]
	})
	return out
}

// exprChecker returns a checker that compiles a proposed `set_expr`
// against the environment validateRules compiles one against, and then
// asks the SAME static coercion matrix (ruleExprStaticFault) whether the
// inferred return type could ever be written to the target.
//
// Sharing rowExprEnv / rowExprOptions / ruleExprStaticFault is the
// point: a second hand-rolled environment is exactly the drift that
// produced issue #258, and a second coercion table is the drift E1-S6
// exists because of.
//
// The COMPILE half is live and is not theoretical: E3-S1 found, while
// falsifying, that a field legally named `in` (expr's membership
// operator) yields valid JSON that looks like every other candidate and
// is refused by the loader it is fed to.
//
// The COERCION half CANNOT FIRE for the target set as bounded today, and
// that is recorded here rather than hidden: packed_bool and u4 are both
// SCALAR targets, and the renderer emits only bools and integer literals
// for them, every one of which the matrix accepts. Falsification
// confirmed it — deleting the call broke no test. It is kept as the
// parity guarantee for the day the admitted target set widens (a
// categorical target would be rendered as a string, and a string on a
// scalar target is exactly what the matrix refuses), not as coverage
// anyone should claim today.
func (d *depDetector) exprChecker() func(target, src string) error {
	specs := make([]FieldSpec, 0, len(d.fields))
	byName := make(map[string]FieldSpec, len(d.fields))
	for i := range d.fields {
		fs := FieldSpec{Name: d.fields[i].name, Type: d.fields[i].typ}
		specs = append(specs, fs)
		byName[fs.Name] = fs
	}
	env, names := rowExprEnv(specs)
	probe := &compiledConstraints{fields: names}
	opts := rowExprOptions(env, names, probe.isnullBuiltin)
	return func(target, src string) error {
		prog, err := expr.Compile(src, opts...)
		if err != nil {
			return err
		}
		f := byName[target]
		ft, ok := fieldTypeFromName(f.Type)
		if !ok {
			return nil
		}
		node := prog.Node()
		if node == nil {
			return nil
		}
		return ruleExprStaticFault(target, f, ft, node.Type())
	}
}

// restrict narrows a group to a subset of its targets, rebuilding EVERY
// slot that names one.
//
// Filtering `targets` alone is not enough and the failure is silent: the
// emitted rule's `set_expr` comes from `exprs` and its `null_together`
// from `block`, so a group that lost a target to a stronger candidate
// would still write it, and the arbitration this exists to perform would
// have had no effect on the file at all.
func (d *depDetector) restrict(g depGroup, targets []int) depGroup {
	keep := make(map[string]bool, len(targets))
	out := depGroup{
		src: g.src, targets: targets, exprs: make(map[string]string, len(targets)),
		coPresent: g.coPresent, minSupport: g.minSupport,
	}
	for _, t := range targets {
		name := d.fields[t].name
		keep[name] = true
		out.exprs[name] = g.exprs[name]
	}
	for _, dep := range g.deps {
		if keep[dep.Field] {
			out.deps = append(out.deps, dep)
		}
	}
	for _, m := range g.block {
		if m == d.fields[g.src].name || keep[m] {
			out.block = append(out.block, m)
		}
	}
	// A block needs at least two distinct fields (PULSE_SYNTH_RULE_BLOCK_INVALID);
	// a source left alone with one target still has two.
	if len(out.block) < 2 {
		out.block = nil
	}
	return out
}

// buildCandidate assembles one proposal and its evidence.
//
// The rule carries NO `when`, which is three decisions at once:
//
//   - It is the TRUE statement. The target is a function of the source
//     on every co-present row, and the admission rule above has already
//     established that "co-present" is every row either is present on.
//   - It makes the targets PRE-CLAIM-ELIGIBLE (E2-S2): an unconditional
//     `set_expr` that does not read its own target claims the field at
//     priority 0, so the captured linear model, conditional pairs and
//     residual correlations naming it are RETIRED instead of being
//     computed and then overwritten. Detection therefore removes the
//     wasted model as a side effect, which is the single most valuable
//     consequence of accepting one of these candidates and is said so in
//     the note.
//   - The null mask is restored by `null_together` IN THIS SAME RULE,
//     where it is the last write by construction. That is E2-S4's
//     measured finding and it is not negotiable: a `set_expr` CLEARS the
//     target's null mask, so declaring the block in a SEPARATE, EARLIER
//     rule lets the derivation run last and un-null every member —
//     33,056 of 40,000 rows carrying a classification flag with no
//     score. The source is named FIRST because applyNullTogether copies
//     the FIRST member's decision, and the source is the field whose
//     absence the targets inherit.
func (d *depDetector) buildCandidate(g depGroup) RuleSpec {
	sf := &d.fields[g.src]
	ev := &RuleEvidence{
		Detector:        dependencyDetectorName,
		Note:            depCandidateNote(sf.name, len(g.targets), g.coPresent, g.block != nil),
		SourceField:     sf.name,
		SourceLevels:    len(sf.levels),
		Dependency:      g.deps,
		RowsObserved:    d.rows,
		RowsAffected:    g.coPresent,
		GatedShare:      float64(g.coPresent) / float64(d.rows),
		MinLevelSupport: g.minSupport,
	}
	ev.ThinSupport = ev.MinLevelSupport < minGateLevelSupport
	// 0 by construction: the admission rule is an IDENTICAL null
	// pattern, so every member of the block this rule carries has the
	// same null count and the same rate. It is the checkable form of
	// "no declared null_rate is being discarded", which is the one cost
	// E1-S5 documents for null_together — exactly the role it plays on a
	// co-missing candidate.
	ev.MaxNullRateDeviation = 0

	spec := RuleSpec{SetExpr: g.exprs, Evidence: ev}
	if g.block != nil {
		spec.NullTogether = g.block
	}
	return spec
}

// render turns one measured lookup into an expression, and this is the
// separate judgement the story names: the mapping is a table, and
// `round(nps) >= 9` is a reading of it.
//
// Threshold forms are preferred wherever the target's value regions are
// CONTIGUOUS in the source's own numeric order, because a threshold is a
// TOTAL function of the source — a value generation produces that the
// cohort never carried lands in the nearest band instead of falling off
// the end of an enumeration. The BAND EDGES ARE READ OFF THE DATA. On
// the motivating cohort that reproduces the standard 9-10 / 7-8 / 0-6
// NPS definition, which is the detector's output and was never its
// input.
func (d *depDetector) render(sf, tf *depField, p *depPair, levels []int32) (string, string, RuleEvidenceDependency, bool) {
	dep := RuleEvidenceDependency{Field: tf.name, Type: tf.typ, Exceptions: p.exceptions}
	for _, l := range levels {
		dep.Mapping = append(dep.Mapping, RuleEvidenceMapValue{
			Level: sf.levels[l],
			Value: tf.levelValue(p.value(int(l))),
			N:     sf.levelN[l],
		})
	}

	// FEWER THAN TWO BANDS MEANS THE TARGET IS CONSTANT over the rows
	// this pair observed, and that is the ONE place it is refused.
	//
	// A constant target is a function of EVERY field in the cohort and of
	// NONE of them — 117 identical candidates naming one uninformative
	// column — so it must not be emitted. It is reported ONCE, as a
	// field, by reportExcludedFields.
	//
	// An earlier, separate "did the target take more than one value"
	// check stood in candidateFor and was deleted at E3-S3 after
	// falsification showed it could be removed with no test failing:
	// bands are built from the SAME per-level values, so one band and
	// one value are the same statement, and two guards spelling it twice
	// is a guard nothing tests. Do not re-add it beside this one.
	bands := depBands(sf, p, levels)
	if len(bands) < 2 {
		return "", "", dep, false
	}
	boolean := tf.typ == encoding.FieldTypePackedBool.String()

	if !sf.categorical {
		lo, hi := sf.numericLevel(levels[0]), sf.numericLevel(levels[len(levels)-1])
		if boolean {
			src, ok := depBoolThreshold(sf, tf, bands, lo, hi)
			return src, depFormThreshold, dep, ok
		}
		src, ok := depNumericThreshold(sf, tf, bands)
		return src, depFormThresholdChain, dep, ok
	}
	if boolean {
		src, form, ok := depBoolMembership(sf, tf, bands)
		return src, form, dep, ok
	}
	src, ok := depNumericEnumeration(sf, tf, bands)
	return src, depFormEnumeration, dep, ok
}

// levelValue renders one of a TARGET field's level indices as the JSON
// value the evidence carries: a bool for a packed_bool, a number for
// everything else. The evidence deliberately does NOT carry the rendered
// expression — it is already the rule's own `set_expr`, and a copy of it
// would go stale the first time the analyst edits the one that executes.
func (f *depField) levelValue(idx int32) any {
	v, _ := strconv.ParseInt(f.levels[idx], 10, 64)
	if f.typ == encoding.FieldTypePackedBool.String() {
		return v != 0
	}
	return v
}

// depBand is one run of source levels sharing a target value.
//
// For a NUMERIC source a band is contiguous in the source's own value
// order and carries its edges. For a CATEGORICAL source there is no
// order to be contiguous in, so a band is simply the set of levels
// mapping to one value.
type depBand struct {
	levels []int32
	value  int32
	lo, hi int64
	n      int
}

func depBands(sf *depField, p *depPair, levels []int32) []depBand {
	var out []depBand
	if sf.categorical {
		byValue := map[int32]int{}
		for _, l := range levels {
			v := p.value(int(l))
			bi, ok := byValue[v]
			if !ok {
				bi = len(out)
				byValue[v] = bi
				out = append(out, depBand{value: v})
			}
			out[bi].levels = append(out[bi].levels, l)
			out[bi].n += sf.levelN[l]
		}
		// Deterministic: by the band's first (already sorted) level.
		sort.SliceStable(out, func(i, j int) bool {
			return sf.levels[out[i].levels[0]] < sf.levels[out[j].levels[0]]
		})
		return out
	}
	for _, l := range levels {
		v := p.value(int(l))
		nv := sf.numericLevel(l)
		if len(out) > 0 && out[len(out)-1].value == v {
			b := &out[len(out)-1]
			b.levels = append(b.levels, l)
			b.hi = nv
			b.n += sf.levelN[l]
			continue
		}
		out = append(out, depBand{levels: []int32{l}, value: v, lo: nv, hi: nv, n: sf.levelN[l]})
	}
	return out
}

// depNumericTerm renders one band of a numeric source as a predicate.
//
// Every numeric comparison goes through round(), uniformly, for the
// reason the gating detector emits its gates that way: the generated row
// holds the sampler's FLOAT and the file holds the stored integer, so a
// bare `nps >= 9` reads a value that is not the one the file shows.
// Measured by E2-S4 at 1,029 band disagreements on 40,000 rows. The
// uniformity is the point — a file where one term is bare and another is
// not teaches a reader that bare is sometimes fine, and nothing says
// which.
//
// An open-ended form is used only where the band touches the observed
// extreme, which is what makes the expression total: a value above the
// largest level the cohort carried still lands in the top band.
func depNumericTerm(sf *depField, b depBand, lo, hi int64) string {
	name := "round(" + sf.name + ")"
	switch {
	case b.lo == lo && b.hi == hi:
		return ""
	case b.lo == b.hi:
		return name + " == " + strconv.FormatInt(b.lo, 10)
	case b.lo == lo:
		return name + " <= " + strconv.FormatInt(b.hi, 10)
	case b.hi == hi:
		return name + " >= " + strconv.FormatInt(b.lo, 10)
	}
	return name + " >= " + strconv.FormatInt(b.lo, 10) + " && " + name + " <= " + strconv.FormatInt(b.hi, 10)
}

func depBoolThreshold(sf, tf *depField, bands []depBand, lo, hi int64) (string, bool) {
	var terms []string
	for _, b := range bands {
		if !tf.truthy(b.value) {
			continue
		}
		term := depNumericTerm(sf, b, lo, hi)
		if term == "" {
			return "", false
		}
		terms = append(terms, term)
	}
	if len(terms) == 0 {
		return "", false
	}
	if len(terms) == 1 {
		return terms[0], true
	}
	for i, t := range terms {
		if strings.Contains(t, "&&") {
			terms[i] = "(" + t + ")"
		}
	}
	return strings.Join(terms, " || "), true
}

// depNumericThreshold renders a multi-valued numeric target as a
// right-nested ternary chain over the bands' upper edges. The LAST band
// is the default arm, which is also the arm an unobserved value above
// the top edge lands in.
func depNumericThreshold(sf, tf *depField, bands []depBand) (string, bool) {
	name := "round(" + sf.name + ")"
	src := tf.literal(bands[len(bands)-1].value)
	for i := len(bands) - 2; i >= 0; i-- {
		cond := name + " <= " + strconv.FormatInt(bands[i].hi, 10)
		src = cond + " ? " + tf.literal(bands[i].value) + " : (" + src + ")"
	}
	return src, true
}

// depBoolMembership renders a boolean target of a CATEGORICAL source as
// an equality set. The SMALLER side is written, negated when that is the
// false side, because a 10-of-12 disjunction is a list a reader has to
// check against the dictionary while its 2-term complement is not.
func depBoolMembership(sf, tf *depField, bands []depBand) (string, string, bool) {
	var trueLevels, falseLevels []int32
	for _, b := range bands {
		if tf.truthy(b.value) {
			trueLevels = append(trueLevels, b.levels...)
		} else {
			falseLevels = append(falseLevels, b.levels...)
		}
	}
	if len(trueLevels) == 0 || len(falseLevels) == 0 {
		return "", "", false
	}
	if len(trueLevels) <= len(falseLevels) {
		return depEqualitySet(sf, trueLevels), depFormMembership, true
	}
	return "!(" + depEqualitySet(sf, falseLevels) + ")", depFormComplement, true
}

// depNumericEnumeration renders a multi-valued numeric target of a
// categorical source as a ternary chain. The LARGEST band is the default
// arm — the fewest terms to write, and the least fabrication for a
// category generation produces that the cohort never carried.
func depNumericEnumeration(sf, tf *depField, bands []depBand) (string, bool) {
	def := 0
	for i := range bands {
		if bands[i].n > bands[def].n {
			def = i
		}
	}
	src := tf.literal(bands[def].value)
	for i := len(bands) - 1; i >= 0; i-- {
		if i == def {
			continue
		}
		src = depEqualitySet(sf, bands[i].levels) + " ? " + tf.literal(bands[i].value) + " : (" + src + ")"
	}
	return src, true
}

func depEqualitySet(sf *depField, levels []int32) string {
	terms := make([]string, 0, len(levels))
	for _, l := range levels {
		terms = append(terms, sf.name+" == "+strconv.Quote(sf.levels[l]))
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "(" + strings.Join(terms, " || ") + ")"
}

// truthy reports whether one of a packed_bool target's level indices is
// the true one.
func (f *depField) truthy(idx int32) bool {
	v, _ := strconv.ParseInt(f.levels[idx], 10, 64)
	return v != 0
}

// literal renders one of a target's level indices as an expression
// literal: `true` / `false` for a packed_bool (the coercion matrix reads
// a bool on a scalar target as 1/0, which is how an author writes it),
// the integer otherwise.
func (f *depField) literal(idx int32) string {
	v, _ := strconv.ParseInt(f.levels[idx], 10, 64)
	if f.typ == encoding.FieldTypePackedBool.String() {
		if v != 0 {
			return "true"
		}
		return "false"
	}
	return strconv.FormatInt(v, 10)
}

// reportExcludedFields names the fields that could not take part, each
// with the reason, so a reader knows what was not searched rather than
// inferring it from an absence.
func (d *depDetector) reportExcludedFields(warnings *[]string) {
	var abandoned, constant []string
	for i := range d.fields {
		f := &d.fields[i]
		switch {
		case f.over:
			abandoned = append(abandoned, f.name)
		case len(f.levels) == 1:
			constant = append(constant, f.name)
		}
	}
	if len(abandoned) > 0 {
		*warnings = append(*warnings, depAbandonedFieldsWarning(abandoned))
	}
	shown := constant
	if len(shown) > maxConstantFieldWarnings {
		shown = shown[:maxConstantFieldWarnings]
	}
	for _, name := range shown {
		*warnings = append(*warnings, depConstantWarning(name, d.rows))
	}
	if rest := len(constant) - len(shown); rest > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"rule suggestion: +%d further single-valued column(s) not considered for dependency detection", rest))
	}
}

// appendMutualWarnings reports pairs that determine EACH OTHER. Both
// directions are proposed and both are consistent — the second rule
// reads what the first wrote — but either alone is sufficient, and a
// pair of mutually-determining columns is usually one column under two
// names, which is worth knowing.
func (d *depDetector) appendMutualWarnings(warnings *[]string, groups []depGroup) {
	owner := make(map[[2]int]bool, len(groups))
	for _, g := range groups {
		for _, t := range g.targets {
			owner[[2]int{g.src, t}] = true
		}
	}
	var lines []string
	for _, g := range groups {
		for _, t := range g.targets {
			if g.src < t && owner[[2]int{t, g.src}] {
				lines = append(lines, depMutualWarning(d.fields[g.src].name, d.fields[t].name, g.coPresent))
			}
		}
	}
	appendBoundedWarnings(warnings, lines, maxNearDepWarnings,
		"rule suggestion: +%d further mutually-determining pair(s) not listed")
}

func (d *depDetector) appendThinWarnings(warnings *[]string, thin []depGroup) {
	if len(thin) == 0 {
		return
	}
	sort.SliceStable(thin, func(i, j int) bool { return thin[i].minSupport < thin[j].minSupport })
	shown := thin
	if len(shown) > maxNearDepWarnings {
		shown = shown[:maxNearDepWarnings]
	}
	for _, g := range shown {
		*warnings = append(*warnings, depThinWarning(d.fields[g.src].name, len(g.targets), g.minSupport))
	}
	if rest := len(thin) - len(shown); rest > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"+%d further thin dependency level(s) below %d supporting observation(s); "+
				"every candidate still ships and carries its own support", rest, minGateLevelSupport))
	}
}

// appendNearWarnings reports the ALMOST-determined pairs, nearest first.
func (d *depDetector) appendNearWarnings(warnings *[]string, near []depNearPair) {
	if len(near) == 0 {
		return
	}
	sort.SliceStable(near, func(i, j int) bool {
		if near[i].exceptions != near[j].exceptions {
			return near[i].exceptions < near[j].exceptions
		}
		return near[i].coPresent > near[j].coPresent
	})
	lines := make([]string, 0, len(near))
	for _, p := range near {
		lines = append(lines, depNearWarning(
			d.fields[p.src].name, d.fields[p.tgt].name, p.coPresent, p.exceptions))
	}
	appendBoundedWarnings(warnings, lines, maxNearDepWarnings,
		"rule suggestion: +%d further almost-determined pair(s) not listed; the listing carries the nearest")
}

// appendUnexpressibleWarnings reports pairs that ARE exactly determined
// and still cannot be written as a faithful rule.
func (d *depDetector) appendUnexpressibleWarnings(warnings *[]string, pairs []depNearPair) {
	if len(pairs) == 0 {
		return
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].coPresent > pairs[j].coPresent })
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.exceptions < 0 { // the uncompilable sentinel; see depNearPair
			lines = append(lines, depUncompilableWarning(d.fields[p.src].name, d.fields[p.tgt].name))
			continue
		}
		lines = append(lines, depNullShapeWarning(
			d.fields[p.src].name, d.fields[p.tgt].name, p.coPresent, p.sourceNull, p.targetNull))
	}
	appendBoundedWarnings(warnings, lines, maxNearDepWarnings,
		"rule suggestion: +%d further exact dependency(ies) not proposed for the same reason")
}

// appendBoundedWarnings is the bounded-listing shape every report-only
// listing in this detector family uses: a capped prefix plus one counted
// remainder worded so classifyWarning's generic roll-up folds it into
// the same group as the lines it summarises.
func appendBoundedWarnings(warnings *[]string, lines []string, cap int, remainder string) {
	if len(lines) == 0 {
		return
	}
	shown := lines
	if len(shown) > cap {
		shown = shown[:cap]
	}
	*warnings = append(*warnings, shown...)
	if rest := len(lines) - len(shown); rest > 0 {
		*warnings = append(*warnings, fmt.Sprintf(remainder, rest))
	}
}

func depCandidateNote(source string, targets, coPresent int, block bool) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"measured, not asserted: each of these %d target(s) takes exactly one value at each level of %q, "+
			"on all %d row(s) where they are present together — zero exceptions. ",
		targets, source, coPresent)
	b.WriteString(
		"THE SEARCH IS NARROW BY DESIGN: targets are packed_bool and u4 ONLY, sources are categorical_*/packed_bool/u4 " +
			"with at most 16 observed levels, and the dependency is on ONE source. Wider numerics, date, set_*, " +
			"categorical-valued targets and joint two-field dependencies were NOT looked for, so a field missing from " +
			"this file was not cleared — it was not examined. ")
	b.WriteString(
		"The band edges below are READ OFF THE DATA, never assumed, and each expression is a TOTAL function of the " +
			"source: a level generation produces that the cohort never carried falls into the nearest band rather " +
			"than raising, so check the edges. ")
	b.WriteString(
		"This rule carries no `when`, so accepting it makes these target(s) RULE-DETERMINED: their captured linear " +
			"models, conditional pairs and residual correlations are retired at priority 0 rather than computed and " +
			"then overwritten.")
	if block {
		b.WriteString(
			" `null_together` names the source FIRST and rides THIS SAME rule, where it is the last write: the " +
				"`set_expr` clears each target's null mask and the block then restores the source's own null decision. " +
				"Splitting them into two rules lets the derivation run last and un-null every member.")
	}
	return b.String()
}

func depAbandonedWarning(n int) string {
	return fmt.Sprintf(
		"rule suggestion: exact-dependency detection was not run — the cohort carries %d low-cardinality field(s), "+
			"more than the %d the pairwise accumulator carries; a truncated field set yields dependencies that are "+
			"exact among the fields it kept while silently missing every relationship involving the rest",
		n, maxDepFields)
}

func depAbandonedFieldsWarning(fields []string) string {
	sort.Strings(fields)
	shown := fields
	suffix := ""
	if len(shown) > 5 {
		shown = shown[:5]
		suffix = fmt.Sprintf(" (+%d more)", len(fields)-5)
	}
	return fmt.Sprintf(
		"rule suggestion: %d field(s) were not considered for exact dependency — more than %d observed levels; "+
			"a derived field with more arms than that is a lookup table, not an expression: %s%s",
		len(fields), maxDepLevels, strings.Join(shown, ", "), suffix)
}

func depConstantWarning(field string, rows int) string {
	return fmt.Sprintf(
		"rule suggestion: column %q carries a single value on all %d row(s) profiled, so it is not considered for "+
			"exact dependency in either role — a constant column is determined by every other field and by none of "+
			"them, and generation reconstructs it from a marginal with no variation",
		field, rows)
}

func depNearWarning(source, target string, coPresent, exceptions int) string {
	return fmt.Sprintf(
		"rule suggestion: %q is an exact function of %q on all but %d of %d co-present row(s), so it is NOT proposed "+
			"— `set_expr` has no way to say \"almost\" and would rewrite exactly the rows that disagreed. The count is "+
			"against the value first seen at each source level, so it is a report and never an emission criterion",
		target, source, exceptions, coPresent)
}

func depNullShapeWarning(source, target string, coPresent, sourceNull, targetNull int) string {
	return fmt.Sprintf(
		"rule suggestion: %q is an exact function of %q on all %d co-present row(s) but is NOT proposed — the two do "+
			"not share a null pattern (%q null on %d row(s), %q on %d), and a `set_expr` CLEARS the target's null "+
			"mask, so the rule would un-null %q on the rows where %q is absent",
		target, source, coPresent, source, sourceNull, target, targetNull, target, source)
}

func depUncompilableWarning(source, target string) string {
	return fmt.Sprintf(
		"rule suggestion: the exact dependency of %q on %q was dropped — the proposed expression does not compile "+
			"against the row environment (a field name that is an expression keyword, or a target type the coercion "+
			"matrix refuses)", target, source)
}

func depMutualWarning(a, b string, coPresent int) string {
	return fmt.Sprintf(
		"rule suggestion: %q and %q determine each OTHER on all %d co-present row(s) — both directions are proposed "+
			"and both are consistent, but either alone is sufficient and a mutually-determining pair is usually one "+
			"column under two names", a, b, coPresent)
}

func depContestedWarning(n int) string {
	return fmt.Sprintf(
		"rule suggestion: %d target(s) are determined by more than one source; each is written once, by the "+
			"strongest candidate, because two rules writing one field leaves the earlier one firing with no effect",
		n)
}

func depTruncationWarning(remaining, kept int) string {
	return fmt.Sprintf(
		"rule suggestion: +%d further exact-dependency candidate(s) not written; the file carries the %d strongest "+
			"by target count then co-present rows", remaining, kept)
}

func depThinWarning(source string, targets, n int) string {
	return thinSupportWarning("dependency level", source, fmt.Sprintf("%d target(s)", targets), n,
		minGateLevelSupport,
		"the candidate still ships and carries its support; judge it on the mapping in its `_evidence`")
}
