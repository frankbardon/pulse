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
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"
)

//go:embed aggregations/*.json attributes/*.json facet/*.json features/*.json filterers/*.json groupers/*.json regression/*.json tests/*.json windows/*.json
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

// Count returns the number of embedded examples.
func Count() int { return len(loadIndex().byName) }

// ExampleSummary is the lightweight projection used by Search results.
type ExampleSummary struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Operators   []string `json:"operators"`
	Description string   `json:"description"`
}

// Example is the full record returned by Get. Body carries the request
// JSON with the _meta block stripped, so it can be handed verbatim to
// pulse_process / pulse_predict.
type Example struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Tags        []string        `json:"tags"`
	Operators   []string        `json:"operators"`
	Description string          `json:"description"`
	Body        json.RawMessage `json:"body"`
}

// meta mirrors the _meta block embedded in every example JSON.
type meta struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
	Operators   []string `json:"operators"`
	Description string   `json:"description"`
}

// indexed is the package-private in-memory index built on first access.
type indexed struct {
	byName     map[string]*Example
	byCategory map[string][]string // category -> example names
	byTag      map[string][]string // tag -> example names
	all        []string            // every name, alphabetical
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
			byName:     make(map[string]*Example),
			byCategory: make(map[string][]string),
			byTag:      make(map[string][]string),
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
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				return fmt.Errorf("examples: %s: %w", p, err)
			}
			rawMeta, ok := raw["_meta"]
			if !ok {
				return fmt.Errorf("examples: %s: missing _meta block", p)
			}
			var m meta
			if err := json.Unmarshal(rawMeta, &m); err != nil {
				return fmt.Errorf("examples: %s: parsing _meta: %w", p, err)
			}
			if m.Name == "" {
				return fmt.Errorf("examples: %s: _meta.name is empty", p)
			}
			if _, dup := idx.byName[m.Name]; dup {
				return fmt.Errorf("examples: duplicate _meta.name %q in %s", m.Name, p)
			}
			delete(raw, "_meta")
			body, err := marshalDeterministic(raw)
			if err != nil {
				return fmt.Errorf("examples: %s: re-marshal body: %w", p, err)
			}
			ex := &Example{
				Name:        m.Name,
				Category:    m.Category,
				Tags:        append([]string(nil), m.Tags...),
				Operators:   append([]string(nil), m.Operators...),
				Description: m.Description,
				Body:        body,
			}
			idx.byName[m.Name] = ex
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
	out.Body = append(json.RawMessage(nil), ex.Body...)
	return &out, true
}

// Search returns summaries matching all three filters. An empty filter
// is treated as "no constraint" for that dimension.
//
//   - query (case-insensitive substring) matches against name, description,
//     and every operator. Score = number of distinct matches across the
//     three fields; ties break alphabetically by name.
//   - tags is ANDed: an example must carry every requested tag.
//   - category is an exact directory match.
//
// Returns a non-nil empty slice when nothing matches.
func Search(query string, tags []string, category string) []ExampleSummary {
	idx := loadIndex()
	q := strings.ToLower(strings.TrimSpace(query))

	type scored struct {
		ex    *Example
		score int
	}
	var hits []scored

	for _, name := range idx.all {
		ex := idx.byName[name]
		if category != "" && ex.Category != category {
			continue
		}
		if !exampleHasAllTags(ex, tags) {
			continue
		}
		score := 0
		if q != "" {
			score = scoreExample(ex, q)
			if score == 0 {
				continue
			}
		}
		hits = append(hits, scored{ex: ex, score: score})
	}

	if q != "" {
		sort.SliceStable(hits, func(i, j int) bool {
			if hits[i].score != hits[j].score {
				return hits[i].score > hits[j].score
			}
			return hits[i].ex.Name < hits[j].ex.Name
		})
	}

	out := make([]ExampleSummary, 0, len(hits))
	for _, h := range hits {
		out = append(out, ExampleSummary{
			Name:        h.ex.Name,
			Category:    h.ex.Category,
			Tags:        append([]string(nil), h.ex.Tags...),
			Operators:   append([]string(nil), h.ex.Operators...),
			Description: h.ex.Description,
		})
	}
	return out
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

// scoreExample tallies how many of the (name, description, operators)
// fields contain q. Higher = better.
func scoreExample(ex *Example, q string) int {
	score := 0
	if strings.Contains(strings.ToLower(ex.Name), q) {
		score++
	}
	if strings.Contains(strings.ToLower(ex.Description), q) {
		score++
	}
	for _, op := range ex.Operators {
		if strings.Contains(strings.ToLower(op), q) {
			score++
			break
		}
	}
	return score
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

	// Risk / edge (3)
	"leakage-safe", "leakage-risk", "small-sample",

	// Discovery / facet (1)
	"facet",
}

// IsCanonicalTag reports whether tag belongs to the curated taxonomy.
func IsCanonicalTag(tag string) bool {
	return slices.Contains(CanonicalTags, tag)
}
