package returnplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// DigestPrefix versions the digest scheme ("rp1:" + sha256 hex).
const DigestPrefix = "rp1:"

// PresetCustom is the Plan.Preset of a block that names no preset but
// selects (include) or carves (exclude) explicitly.
const PresetCustom = "custom"

// Plan is a resolved Return selection. Build one with New; the zero
// value selects nothing.
//
// A concrete response node is emitted iff no Exclude path matches it or
// an ancestor, and either an Include path matches it or an ancestor
// (the whole subtree), or it is an ancestor of something an Include
// path selects (emitted as a container holding only selected children),
// or a Keep path matches it exactly and its parent is emitted. Keep
// paths are the "retained unless excluded" nested `warnings` slots:
// kept wherever their parent object appears, never conjured on their
// own.
type Plan struct {
	// Preset is the preset name the block resolved from, PresetCustom
	// for an explicit include / exclude without one.
	Preset string
	// Include, Exclude and Keep are canonical: deduplicated, sorted by
	// spelling, with every path another one already covers removed, an
	// include an exclude covers removed, an exclude that touches no
	// include or keep path removed, and a keep path an include covers
	// removed.
	Include []Path
	Exclude []Path
	Keep    []Path
	// Companions are the CONDITIONAL selections a preset carries: each
	// one keeps a map key beside a kept sibling (Companion). Canonical
	// like Keep: one an include or exclude covers is removed. Answered by
	// VisitIn, never by Visit (which cannot see the parent map).
	Companions []Companion
	// Precision is the wire float significant-digit count, 0 unlimited.
	Precision int
	// Exact lists the REQUEST-DERIVED precision exemptions: nodes (and
	// their subtrees) whose floats carry integer semantics for this
	// request — a count aggregation's data column, a count crosstab
	// cell — written at full precision even when Precision is set. The
	// resolver fills it (internal/descriptor/return_resolve.go); the
	// response-type exemptions that hold for every request live beside
	// the encoder (types/return_shape.go, precisionExempt). Derived from
	// the request, never part of the selection, so outside the digest.
	Exact []Path
	// SelectsAll reports that Include covers every top-level key of the
	// root, so the root itself is selected whole.
	SelectsAll bool
	// Digest identifies the resolved selection (Include, Exclude, Keep,
	// Companions, Precision — not the preset name), so equivalent
	// spellings share it.
	Digest string
}

// Companion is a conditional selection over a map: the map key Path
// names (its last segment) is selected whole wherever its parent map
// also holds the key With AND the plan keeps that sibling. It is how a
// preset keeps a figure that only means something beside another one
// (the pairwise `auxiliary.n` beside an inference `auxiliary.p`) without
// emitting it everywhere. An exclude covering Path (or the sibling)
// wins, as everywhere.
type Companion struct {
	Path Path
	With string
}

// String renders the companion as `<path> (with <sibling>)`.
func (c Companion) String() string { return c.Path.String() + " (with " + c.With + ")" }

// New canonicalizes include / exclude / keep and computes the digest.
// rootKeys is every visible top-level key of the root object; an
// include set covering all of them selects the root whole.
func New(preset string, include, exclude, keep []Path, precision int, rootKeys []string) *Plan {
	return NewWithCompanions(preset, include, exclude, keep, nil, precision, rootKeys)
}

// NewWithCompanions is New plus the preset's conditional selections
// (Companion). A companion an exclude or an include covers is dropped
// (excluded, or already selected unconditionally).
func NewWithCompanions(preset string, include, exclude, keep []Path, companions []Companion, precision int, rootKeys []string) *Plan {
	exclude = reduce(exclude)
	include = reduce(include)
	include = dropCovered(include, exclude)
	keep = dropCovered(reduce(keep), exclude)
	keep = dropCovered(keep, include)
	companions = canonicalCompanions(companions, include, exclude)
	cpaths := make([]Path, len(companions))
	for i, c := range companions {
		cpaths[i] = c.Path
	}
	exclude = relevant(exclude, include, append(append([]Path(nil), keep...), cpaths...))
	p := &Plan{
		Preset:     preset,
		Include:    include,
		Exclude:    exclude,
		Keep:       keep,
		Companions: companions,
		Precision:  precision,
	}
	p.SelectsAll = len(rootKeys) > 0
	for _, k := range rootKeys {
		if !p.includesWhole([]Segment{Key(k)}) {
			p.SelectsAll = false
			break
		}
	}
	p.Digest = p.digest()
	return p
}

// Identity reports whether the plan changes nothing: everything
// selected, nothing excluded, no precision.
func (p *Plan) Identity() bool {
	return p == nil || (p.SelectsAll && len(p.Exclude) == 0 && p.Precision == 0)
}

// Strings renders a path list in canonical spelling (never nil).
func Strings(ps []Path) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// Verdict is the plan's answer for one concrete node.
type Verdict struct {
	// Keep is false when the node is absent on the wire.
	Keep bool
	// Whole means the node's whole subtree is emitted untouched: the
	// caller need not descend. Keep && !Whole means descend and ask for
	// each child.
	Whole bool
}

// Visit answers for the concrete node at path c (Key / Elem segments;
// the empty path is the root). The caller only asks for a child of a
// node it emits.
func (p *Plan) Visit(c []Segment) Verdict {
	if p == nil {
		return Verdict{Keep: true, Whole: true}
	}
	if len(c) == 0 {
		if p.SelectsAll && len(p.Exclude) == 0 {
			return Verdict{Keep: true, Whole: true}
		}
		return Verdict{Keep: true}
	}
	for _, e := range p.Exclude {
		if matchPrefix(e, c) {
			return Verdict{}
		}
	}
	excludeBelow := false
	for _, e := range p.Exclude {
		if isAncestor(c, e) {
			excludeBelow = true
			break
		}
	}
	if p.includesWhole(c) {
		return Verdict{Keep: true, Whole: !excludeBelow}
	}
	for _, k := range p.Keep {
		if len(k.Segments) == len(c) && matchPrefix(k, c) {
			return Verdict{Keep: true, Whole: !excludeBelow}
		}
	}
	for _, in := range p.Include {
		if isAncestor(c, in) {
			return Verdict{Keep: true}
		}
	}
	// A keep path never makes an ancestor appear on its own.
	return Verdict{}
}

// VisitIn is Visit for the map key at c (a Key segment last) whose
// parent map holds the keys has reports: on top of Visit's verdict it
// answers the plan's Companions — a companion key is kept whole beside
// a kept With sibling the parent holds. Exclude wins. Use it wherever a
// walker iterates a map's keys; Visit alone never keeps a companion.
func (p *Plan) VisitIn(c []Segment, has func(key string) bool) Verdict {
	v := p.Visit(c)
	if v.Keep || p == nil || len(p.Companions) == 0 || len(c) == 0 {
		return v
	}
	for _, e := range p.Exclude {
		if matchPrefix(e, c) {
			return v
		}
	}
	for _, cp := range p.Companions {
		if !cp.Path.Matches(c) || !has(cp.With) {
			continue
		}
		sib := append(append(make([]Segment, 0, len(c)), c[:len(c)-1]...), Key(cp.With))
		if !p.Visit(sib).Keep {
			continue
		}
		whole := true
		for _, e := range p.Exclude {
			if isAncestor(c, e) {
				whole = false
				break
			}
		}
		return Verdict{Keep: true, Whole: whole}
	}
	return v
}

// canonicalCompanions drops the companions an exclude or an include
// covers, deduplicates and sorts them.
func canonicalCompanions(cs []Companion, include, exclude []Path) []Companion {
	if len(cs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []Companion
	for _, c := range cs {
		if seen[c.String()] || len(dropCovered([]Path{c.Path}, exclude)) == 0 || len(dropCovered([]Path{c.Path}, include)) == 0 {
			continue
		}
		seen[c.String()] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// CompanionStrings renders the companions in canonical spelling.
func CompanionStrings(cs []Companion) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

// includesWhole reports whether some include path matches c or one of
// its ancestors.
func (p *Plan) includesWhole(c []Segment) bool {
	for _, in := range p.Include {
		if matchPrefix(in, c) {
			return true
		}
	}
	return false
}

func (p *Plan) digest() string {
	// Companions are omitempty: a plan without one digests exactly as
	// before they existed.
	var companions []string
	if len(p.Companions) > 0 {
		companions = CompanionStrings(p.Companions)
	}
	body, _ := json.Marshal(struct {
		Include    []string `json:"include"`
		Exclude    []string `json:"exclude"`
		Keep       []string `json:"keep"`
		Companions []string `json:"companions,omitempty"`
		Precision  int      `json:"precision"`
	}{Strings(p.Include), Strings(p.Exclude), Strings(p.Keep), companions, p.Precision})
	sum := sha256.Sum256(body)
	return DigestPrefix + hex.EncodeToString(sum[:])
}

// reduce deduplicates by spelling, drops every path another one covers,
// and sorts by spelling.
func reduce(ps []Path) []Path {
	seen := make(map[string]bool, len(ps))
	uniq := make([]Path, 0, len(ps))
	for _, p := range ps {
		s := p.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		uniq = append(uniq, p)
	}
	out := uniq[:0:0]
	for i, p := range uniq {
		covered := false
		for j, q := range uniq {
			if i != j && covers(q, p) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// dropCovered removes every path of ps some path of by covers.
func dropCovered(ps, by []Path) []Path {
	out := make([]Path, 0, len(ps))
	for _, p := range ps {
		covered := false
		for _, q := range by {
			if covers(q, p) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	return out
}

// relevant keeps the excludes that overlap some include or keep path —
// an exclude outside every selected subtree changes nothing.
func relevant(exclude, include, keep []Path) []Path {
	out := make([]Path, 0, len(exclude))
	for _, e := range exclude {
		hit := false
		for _, set := range [][]Path{include, keep} {
			for _, in := range set {
				if overlaps(e, in) {
					hit = true
					break
				}
			}
		}
		if hit {
			out = append(out, e)
		}
	}
	return out
}
