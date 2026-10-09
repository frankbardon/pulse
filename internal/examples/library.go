// Package examples embeds the catalogue of runnable Pulse request JSON
// files and exposes a search/get API over them.
//
// Every embedded JSON carries a top-level _meta block describing the
// example's name, category, taxonomy tags, the operators it uses, and a
// one-sentence description. Pulse's types.Request unmarshaler ignores
// unknown fields by default, so _meta is invisible at execution time —
// the examples remain runnable verbatim.
//
// Get returns the example with the _meta block stripped, so callers can
// hand the JSON straight to pulse_process / pulse_predict.
package examples

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed aggregations/*.json attributes/*.json crosstab/*.json facet/*.json features/*.json filterers/*.json groupers/*.json matrices/*.json overlays/*.json regression/*.json tests/*.json windows/*.json
var content embed.FS

// AllCategories returns every directory the library indexes, sorted
// alphabetically.
func AllCategories() []string {
	idx := loadIndex()
	out := make([]string, 0, len(idx.byCategory))
	for c := range idx.byCategory {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// AllTags returns every taxonomy tag the library currently uses, sorted
// alphabetically. Derived from the indexed examples — not the canonical
// allowlist — so callers see exactly what is tagged today.
func AllTags() []string {
	idx := loadIndex()
	seen := make(map[string]struct{}, len(idx.byTag))
	for t := range idx.byTag {
		seen[t] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Intents returns every example's optional _meta.intents, keyed by
// example name; an example without the key is absent. Values are
// validated against the intent taxonomy by internal/descriptor
// (TestExamples_IntentsFromTaxonomy), which this package cannot import.
func Intents() map[string][]string {
	idx := loadIndex()
	out := make(map[string][]string, len(idx.intents))
	for n, ids := range idx.intents {
		out[n] = append([]string(nil), ids...)
	}
	return out
}

// Capabilities returns every example's optional _meta.capabilities, keyed
// by example name; an example without the key is absent. Values are
// feature-profile spellings of non-operator features
// (`capability:stream`), validated by internal/descriptor
// (TestExamples_CapabilitiesFromFeatures), which this package cannot
// import. The key exists for capabilities a request body carries no
// structural signal for; internal/descriptor's ontology builder turns
// each value into an `example requires_capability <feature>` edge.
func Capabilities() map[string][]string {
	idx := loadIndex()
	out := make(map[string][]string, len(idx.capabilities))
	for n, caps := range idx.capabilities {
		out[n] = append([]string(nil), caps...)
	}
	return out
}

// Count returns the number of embedded examples.
func Count() int { return len(loadIndex().byName) }

// ExampleSummary is the lightweight projection used by Search results.
type ExampleSummary struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Operators   []string `json:"operators"`
	Description string   `json:"description"`
	// Intents is the example's _meta.intents (intent-taxonomy IDs);
	// absent on the wire when the example declares none.
	Intents []string `json:"intents,omitempty"`
}

// Example is the full record returned by Get. Body carries the request
// JSON with the _meta block stripped, so it can be handed verbatim to
// pulse_process / pulse_predict.
type Example struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Operators   []string `json:"operators"`
	Description string   `json:"description"`
	// Intents is the example's _meta.intents (intent-taxonomy IDs);
	// absent on the wire when the example declares none.
	Intents []string        `json:"intents,omitempty"`
	Body    json.RawMessage `json:"body"`
}

// Meta mirrors the _meta block every example JSON carries — built-in
// and embedder (pulse.Extensions.Examples) alike.
type Meta struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Operators   []string `json:"operators"`
	Description string   `json:"description"`
	// Intents is optional; every value must be an intent-taxonomy ID
	// (validated by internal/descriptor's TestExamples_IntentsFromTaxonomy,
	// since this package cannot import the taxonomy).
	Intents []string `json:"intents,omitempty"`
	// Capabilities is optional; every value must be a non-operator
	// feature name (validated by internal/descriptor's
	// TestExamples_CapabilitiesFromFeatures).
	Capabilities []string `json:"capabilities,omitempty"`
}

// indexed is the package-private in-memory index built on first access.
type indexed struct {
	byName     map[string]*Example
	byCategory map[string][]string // category -> example names
	byTag      map[string][]string // tag -> example names
	intents    map[string][]string // example name -> _meta.intents
	// capabilities: example name -> _meta.capabilities
	capabilities map[string][]string
	all          []string // every name, alphabetical
}

var (
	indexOnce sync.Once
	indexVal  *indexed
)

// loadIndex parses every embedded JSON file once on first call. Panics
// on corrupt or unannotated content — that means a CI gate is missing.
func loadIndex() *indexed {
	indexOnce.Do(func() {
		idx := &indexed{
			byName:       make(map[string]*Example),
			byCategory:   make(map[string][]string),
			byTag:        make(map[string][]string),
			intents:      make(map[string][]string),
			capabilities: make(map[string][]string),
		}
		err := fs.WalkDir(content, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			data, readErr := content.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			ex, m, err := Parse(data, false)
			if err != nil {
				return fmt.Errorf("examples: %s: %w", p, err)
			}
			if m.Name == "" {
				return fmt.Errorf("examples: %s: _meta.name is empty", p)
			}
			if _, dup := idx.byName[m.Name]; dup {
				return fmt.Errorf("examples: duplicate _meta.name %q in %s", m.Name, p)
			}
			idx.byName[m.Name] = ex
			if len(m.Intents) > 0 {
				idx.intents[m.Name] = append([]string(nil), m.Intents...)
			}
			if len(m.Capabilities) > 0 {
				idx.capabilities[m.Name] = append([]string(nil), m.Capabilities...)
			}
			idx.byCategory[m.Category] = append(idx.byCategory[m.Category], m.Name)
			for _, t := range m.Tags {
				idx.byTag[t] = append(idx.byTag[t], m.Name)
			}
			return nil
		})
		if err != nil {
			panic("examples: " + err.Error())
		}
		for c := range idx.byCategory {
			sort.Strings(idx.byCategory[c])
		}
		for t := range idx.byTag {
			sort.Strings(idx.byTag[t])
		}
		all := make([]string, 0, len(idx.byName))
		for n := range idx.byName {
			all = append(all, n)
		}
		sort.Strings(all)
		idx.all = all
		indexVal = idx
	})
	return indexVal
}

// ErrNoMeta is Parse's error for a JSON object without a _meta block.
var ErrNoMeta = errors.New("missing _meta block")

// Parse reads one example file: a JSON object carrying a _meta block
// plus the request body. It returns the Example — Body is the object
// minus _meta, re-marshaled deterministically — and the parsed Meta.
// strict refuses an unknown _meta key (embedder examples); the embedded
// library parses leniently. Parse does not validate Meta's values.
func Parse(data []byte, strict bool) (*Example, Meta, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, Meta{}, fmt.Errorf("not a JSON object: %w", err)
	}
	rawMeta, ok := raw["_meta"]
	if !ok {
		return nil, Meta{}, ErrNoMeta
	}
	var m Meta
	dec := json.NewDecoder(bytes.NewReader(rawMeta))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&m); err != nil {
		return nil, Meta{}, fmt.Errorf("parsing _meta: %w", err)
	}
	delete(raw, "_meta")
	body, err := marshalDeterministic(raw)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("re-marshal body: %w", err)
	}
	return &Example{
		Name:        m.Name,
		Category:    m.Category,
		Tags:        append([]string(nil), m.Tags...),
		Operators:   append([]string(nil), m.Operators...),
		Description: m.Description,
		Intents:     slices.Clone(m.Intents),
		Body:        body,
	}, m, nil
}

// operatorRe captures `"type": "PREFIX_..."` in a JSON request body.
// Kept in sync with the regex used by cmd/annotate-examples.
var operatorRe = regexp.MustCompile(`"type"\s*:\s*"((?:AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|REG|MAT)_[A-Z0-9_]+)"`)

// DeriveOperators returns the sorted, distinct operator names a body
// carries as a `"type"` value — what `_meta.operators` must equal
// (TestExamples_OperatorsMatchBody; embedder examples at pulse.New).
func DeriveOperators(body []byte) []string {
	matches := operatorRe.FindAllStringSubmatch(string(body), -1)
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for op := range seen {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// marshalDeterministic re-marshals a raw map preserving lexical key order
// so the embedded body remains diff-stable. encoding/json marshals map
// keys alphabetically, which is the determinism we want.
func marshalDeterministic(raw map[string]json.RawMessage) ([]byte, error) {
	return json.Marshal(raw)
}

// Get returns the example with the given name. The returned Body is the
// request JSON minus the _meta field, runnable verbatim against
// pulse.Process. Returns false when no example carries the name.
func Get(name string) (*Example, bool) {
	idx := loadIndex()
	ex, ok := idx.byName[name]
	if !ok {
		return nil, false
	}
	out := *ex
	out.Tags = append([]string(nil), ex.Tags...)
	out.Operators = append([]string(nil), ex.Operators...)
	out.Intents = slices.Clone(ex.Intents)
	out.Body = append(json.RawMessage(nil), ex.Body...)
	return &out, true
}

// All returns a copy of every embedded example, sorted by name.
func All() []*Example {
	idx := loadIndex()
	out := make([]*Example, 0, len(idx.all))
	for _, n := range idx.all {
		ex, _ := Get(n)
		out = append(out, ex)
	}
	return out
}

// Search returns summaries matching all three filters. An empty filter
// is treated as "no constraint" for that dimension. It is SearchQuery
// over the embedded library with no synonym tables; SearchQuery
// documents the matching rules. Never nil.
func Search(query string, tags []string, category string) []ExampleSummary {
	return SearchLibrary(Query{Query: query, Tags: tags, Category: category}, Options{})
}

// SearchIn is Search over lib, which must be sorted by name (the
// no-query order and the score tie-break). An instance serving embedder
// examples searches the merged library through it.
func SearchIn(lib []*Example, query string, tags []string, category string) []ExampleSummary {
	return SearchQuery(lib, Query{Query: query, Tags: tags, Category: category}, Options{})
}

// exampleHasAllTags returns true when ex carries every tag in want.
// Empty want means no tag constraint.
func exampleHasAllTags(ex *Example, want []string) bool {
	if len(want) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(ex.Tags))
	for _, t := range ex.Tags {
		have[t] = struct{}{}
	}
	for _, t := range want {
		if _, ok := have[t]; !ok {
			return false
		}
	}
	return true
}

// CanonicalTags is the curated taxonomy every annotated example must
// draw from. Kept here (rather than embedded in a separate file) so the
// validation gate can compare directly without round-tripping through a
// disk asset.
var CanonicalTags = []string{
	// Domain / use case (16)
	"time-series", "cohort-analysis", "experiment-analysis", "correlation-analysis",
	"comparison", "before-after", "top-n", "distribution-shape", "cross-tabulation",
	"proportion-analysis", "trend-detection", "outlier-detection", "cardinality-analysis",
	"data-quality", "financial", "feature-engineering",

	// Statistical method (11)
	"hypothesis-test", "t-test", "parametric", "nonparametric", "paired", "one-sample",
	"two-sample", "k-sample", "repeated-measures", "post-hoc",
	"normality-test", "homogeneity-test", "exact-test",

	// Regression / modeling (15)
	"regression", "ecological",
	"ols", "glm", "logistic", "bayesian",
	"regularization", "ridge", "lasso", "elasticnet",
	"polynomial", "resampling", "jackknife", "selection", "stepwise",

	// Pipeline machinery (8)
	"tier-1-test", "tier-2-test", "composed", "pre-filter", "feature-pipeline",
	"window-operator", "streaming-friendly", "buffered-pipeline",

	// Matrices (2)
	"matrix", "covariance",

	// Risk / edge (2)
	"leakage-risk", "small-sample",

	// Discovery / facet (1)
	"facet",

	// Cohort shape (2)
	"sharded", "anchor",

	// Result decoration (6)
	"overlay",
	"byte-equal-test",
	"compose",
	"crosstab",
	"welch",
	"welford-triple",
	"z",
}

// IsCanonicalTag reports whether tag belongs to the curated taxonomy.
func IsCanonicalTag(tag string) bool {
	return slices.Contains(CanonicalTags, tag)
}
