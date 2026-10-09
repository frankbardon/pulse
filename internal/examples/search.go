package examples

import (
	"slices"
	"sort"
	"strings"
)

// Query is one example search: every filter optional, all ANDed.
//
//   - Query is matched against an example's name, description,
//     operators, _meta.intents and tags (see SearchQuery for the match
//     tiers).
//   - Tags is ANDed: an example must carry every requested tag.
//   - Category is an exact directory match.
//   - Intent is an intent-taxonomy ID the example's _meta.intents must
//     carry. This package does not validate it (it cannot import the
//     taxonomy); internal/descriptor refuses an unknown or hidden one
//     before searching.
type Query struct {
	Query    string   `json:"query,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Category string   `json:"category,omitempty"`
	Intent   string   `json:"intent,omitempty"`
}

// Options carries an instance's view into a search: the synonym
// tables of the synonym match tier and the visibility filters. Every
// table key is in internal/descriptor's FoldAlias form (lower-cased,
// whitespace collapsed, punctuation kept). The zero value searches the
// whole library with the synonym tier disabled.
type Options struct {
	// Aliases maps a Purpose.KnownAs alias to the operator declaring it.
	Aliases map[string]string
	// Sounds maps an intent Sound to its intent ID.
	Sounds map[string]string
	// KeepIntent, when set, drops every _meta.intents value it refuses
	// from matching and from the returned summaries (an intent the
	// instance hides). Nil keeps all.
	KeepIntent func(id string) bool
	// KeepExample, when set, drops every example it refuses before any
	// tier runs, so a hidden example never decides which tier answers.
	KeepExample func(name string) bool
}

// Matched-field bits: the score of a hit is the number of distinct
// fields matched.
const (
	fieldName = 1 << iota
	fieldDescription
	fieldOperators
	fieldIntents
	fieldTags
)

// searchDoc is one example's searchable view: its fields tokenized once
// per search.
type searchDoc struct {
	ex        *Example
	intents   []string
	fields    [5][]string // token lists indexed by field bit position
	tokenized bool
}

// matchTier is one ordered match strategy. Tiers run in order and the
// first tier that matches any example decides the result set; a later
// tier is a fallback, never a merge, so adding one (U31's fuzzy tier
// goes last) cannot reorder what an earlier tier already answers.
// match returns the matched-field bits for doc (0 = no match).
type matchTier interface {
	match(doc *searchDoc) int
}

// SearchQuery returns the summaries of lib matching q, ranked. lib must
// be sorted by name (the no-query order and the score tie-break).
//
// Matching runs as ordered tiers; the first tier with any hit wins:
//
//  1. exact — a query that is a single plain word (letters and digits
//     only) keeps the legacy rule: a case-insensitive substring of the
//     name, the description or any operator, so a one-word query answers
//     exactly as before. Any other query is tokenized (lower-cased,
//     split on non-alphanumerics, no stemming, common stop words
//     dropped) and an example matches when every query token is an
//     exact token of at least one of its fields (name, description,
//     operators, intents, tags); a field counts when it holds any
//     query token.
//  2. synonym — a query phrase that is a Purpose.KnownAs alias expands
//     the query to that operator, and a query phrase found inside an
//     intent's Sound expands it to that intent. An example matches when
//     its operators or intents carry an expansion; the fields matched by
//     the expansions and by any query token count.
//
// Score = distinct matched fields; ties break alphabetically by name.
// No query lists every example passing the filters, alphabetically.
// Returns a non-nil empty slice when nothing matches.
func SearchQuery(lib []*Example, q Query, syn Options) []ExampleSummary {
	var docs []*searchDoc
	for _, ex := range lib {
		if syn.KeepExample != nil && !syn.KeepExample(ex.Name) {
			continue
		}
		if q.Category != "" && ex.Category != q.Category {
			continue
		}
		if !exampleHasAllTags(ex, q.Tags) {
			continue
		}
		intents := ex.Intents
		if syn.KeepIntent != nil {
			intents = slices.DeleteFunc(slices.Clone(intents), func(id string) bool { return !syn.KeepIntent(id) })
		}
		if q.Intent != "" && !slices.Contains(intents, q.Intent) {
			continue
		}
		docs = append(docs, &searchDoc{ex: ex, intents: intents})
	}

	type scored struct {
		doc   *searchDoc
		score int
	}
	var hits []scored
	query := strings.TrimSpace(q.Query)
	if query == "" {
		for _, d := range docs {
			hits = append(hits, scored{doc: d})
		}
	} else {
		for _, d := range docs {
			d.tokenize()
		}
		for _, tier := range tiersFor(query, syn) {
			for _, d := range docs {
				if bits := tier.match(d); bits != 0 {
					hits = append(hits, scored{doc: d, score: popcount(bits)})
				}
			}
			if len(hits) > 0 {
				break
			}
		}
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].score != hits[j].score {
				return hits[i].score > hits[j].score
			}
			return hits[i].doc.ex.Name < hits[j].doc.ex.Name
		})
	}

	out := make([]ExampleSummary, 0, len(hits))
	for _, h := range hits {
		ex := h.doc.ex
		out = append(out, ExampleSummary{
			Name:        ex.Name,
			Category:    ex.Category,
			Tags:        append([]string(nil), ex.Tags...),
			Operators:   append([]string(nil), ex.Operators...),
			Description: ex.Description,
			Intents:     slices.Clone(h.doc.intents),
		})
	}
	return out
}

// SearchLibrary is SearchQuery over the embedded library.
func SearchLibrary(q Query, opts Options) []ExampleSummary {
	idx := loadIndex()
	lib := make([]*Example, 0, len(idx.all))
	for _, n := range idx.all {
		lib = append(lib, idx.byName[n])
	}
	return SearchQuery(lib, q, opts)
}

// tiersFor builds the ordered tiers for a non-empty query.
func tiersFor(query string, syn Options) []matchTier {
	toks := Tokenize(query)
	if len(toks) == 1 && toks[0] == strings.ToLower(query) {
		return []matchTier{substringTier{q: toks[0]}, newSynonymTier(query, toks, syn)}
	}
	return []matchTier{newTokenTier(toks), newSynonymTier(query, toks, syn)}
}

// tokenize fills d.fields once.
func (d *searchDoc) tokenize() {
	if d.tokenized {
		return
	}
	d.tokenized = true
	d.fields[0] = Tokenize(d.ex.Name)
	d.fields[1] = Tokenize(d.ex.Description)
	d.fields[2] = tokenizeAll(d.ex.Operators)
	d.fields[3] = tokenizeAll(d.intents)
	d.fields[4] = tokenizeAll(d.ex.Tags)
}

func tokenizeAll(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, Tokenize(v)...)
	}
	return out
}

// Tokenize lower-cases s and splits it on every character that is not
// an ASCII letter or digit. No stemming; empty tokens are dropped.
func Tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

// stopWords never have to match in the token tier and never anchor a
// Sound match: they carry no topic.
var stopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "by": {}, "do": {}, "does": {},
	"for": {}, "from": {}, "how": {}, "in": {}, "is": {}, "it": {}, "my": {}, "of": {},
	"on": {}, "or": {}, "the": {}, "this": {}, "to": {}, "vs": {}, "what": {}, "which": {},
	"with": {},
}

// placeholders are the variable letters intent Sounds use ("Do X and Y
// move together?"); like stop words they never anchor a Sound match.
var placeholders = map[string]struct{}{"x": {}, "y": {}, "a": {}, "b": {}}

func isContent(tok string) bool {
	_, stop := stopWords[tok]
	_, ph := placeholders[tok]
	return !stop && !ph
}

func popcount(bits int) int {
	n := 0
	for ; bits != 0; bits &= bits - 1 {
		n++
	}
	return n
}

// substringTier is the legacy single-word rule: a substring of name,
// description or any operator.
type substringTier struct{ q string }

func (t substringTier) match(d *searchDoc) int {
	bits := 0
	if strings.Contains(strings.ToLower(d.ex.Name), t.q) {
		bits |= fieldName
	}
	if strings.Contains(strings.ToLower(d.ex.Description), t.q) {
		bits |= fieldDescription
	}
	for _, op := range d.ex.Operators {
		if strings.Contains(strings.ToLower(op), t.q) {
			bits |= fieldOperators
			break
		}
	}
	return bits
}

// tokenTier requires every content token of the query as an exact token
// of some field.
type tokenTier struct{ toks []string }

func newTokenTier(toks []string) tokenTier {
	var content []string
	for _, t := range toks {
		if _, stop := stopWords[t]; !stop {
			content = append(content, t)
		}
	}
	return tokenTier{toks: content}
}

func (t tokenTier) match(d *searchDoc) int {
	if len(t.toks) == 0 {
		return 0
	}
	bits := 0
	for _, tok := range t.toks {
		found := false
		for i, f := range d.fields {
			if slices.Contains(f, tok) {
				bits |= 1 << i
				found = true
			}
		}
		if !found {
			return 0
		}
	}
	return bits
}

// synonymTier matches the operators and intents a query phrase expands
// to.
type synonymTier struct {
	ops     map[string]struct{}
	intents map[string]struct{}
	content []string
}

func newSynonymTier(query string, toks []string, syn Options) synonymTier {
	t := synonymTier{ops: map[string]struct{}{}, intents: map[string]struct{}{}}
	for _, tok := range toks {
		if isContent(tok) {
			t.content = append(t.content, tok)
		}
	}
	if len(syn.Aliases) > 0 {
		// Alias index in two spellings: the folded phrase as declared
		// (punctuation kept) and its token sequence, so "kruskal.test"
		// and "kruskal test" both reach the alias.
		byTokens := map[string][]string{}
		for alias, op := range syn.Aliases {
			k := strings.Join(Tokenize(alias), " ")
			byTokens[k] = append(byTokens[k], op)
		}
		words := strings.Fields(strings.ToLower(query))
		for i := range words {
			for j := i + 1; j <= len(words); j++ {
				phrase := strings.Join(words[i:j], " ")
				for _, p := range []string{phrase, strings.Trim(phrase, `?!.,;:"()[]`)} {
					if op, ok := syn.Aliases[p]; ok {
						t.ops[op] = struct{}{}
					}
				}
			}
		}
		for i := range toks {
			for j := i + 1; j <= len(toks); j++ {
				for _, op := range byTokens[strings.Join(toks[i:j], " ")] {
					t.ops[op] = struct{}{}
				}
			}
		}
	}
	// A Sound matches when a query phrase of two or more tokens, at
	// least one of them content, sits contiguously inside the Sound's
	// tokens (or the whole Sound sits inside the query).
	for sound, intent := range syn.Sounds {
		st := strings.Join(Tokenize(sound), " ")
		for i := range toks {
			for j := i + 2; j <= len(toks); j++ {
				gram := toks[i:j]
				if !slices.ContainsFunc(gram, isContent) {
					continue
				}
				if containsPhrase(st, strings.Join(gram, " ")) {
					t.intents[intent] = struct{}{}
				}
			}
		}
		if st != "" && containsPhrase(strings.Join(toks, " "), st) {
			t.intents[intent] = struct{}{}
		}
	}
	return t
}

// containsPhrase reports whether phrase occurs in s on token
// boundaries (both space-joined token lists).
func containsPhrase(s, phrase string) bool {
	return strings.Contains(" "+s+" ", " "+phrase+" ")
}

func (t synonymTier) match(d *searchDoc) int {
	bits := 0
	for _, op := range d.ex.Operators {
		if _, ok := t.ops[op]; ok {
			bits |= fieldOperators
		}
	}
	for _, in := range d.intents {
		if _, ok := t.intents[in]; ok {
			bits |= fieldIntents
		}
	}
	if bits == 0 {
		return 0
	}
	for _, tok := range t.content {
		for i, f := range d.fields {
			if slices.Contains(f, tok) {
				bits |= 1 << i
			}
		}
	}
	return bits
}
