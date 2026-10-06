package descriptor

import (
	"sort"
	"strings"
)

// operatorCategoryOrder is the category order operator features are
// listed in: the order the feature table groups them in.
var operatorCategoryOrder = []string{
	"AGG", "ATTR", "FILTER", "GROUP", "WIN", "FEAT", "TEST", "REG", "MAT", "OVERLAY",
}

// FeatureKindOfName classifies any feature spelling, built-in or not:
// the table row's kind when name is a built-in, otherwise the kind its
// prefix names (`capability:`, `io_format:`, `mcp_extra:`), and an
// operator for a bare name (an extension operator).
func FeatureKindOfName(name string) FeatureKind {
	if k, ok := FeatureKindOf(name); ok {
		return k
	}
	for _, k := range AllFeatureKinds() {
		if k != FeatureKindOperator && strings.HasPrefix(name, string(k)+":") {
			return k
		}
	}
	return FeatureKindOperator
}

// FeatureCategoryOf returns the category of an operator feature — the
// name's leading SCREAMING_SNAKE segment (`AGG`, `GROUP`, `OVERLAY` …).
// Non-operator features have no category and return "".
func FeatureCategoryOf(name string) string {
	if FeatureKindOfName(name) != FeatureKindOperator {
		return ""
	}
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

// SortFeatureNames returns names in the canonical profile order: by kind
// (AllFeatureKinds order), then by operator category (the table's
// category order; an unlisted category such as SYNTH follows, by name),
// then by feature-table position. Names absent from the table (extension
// operators, unknown spellings) follow the built-ins of their kind and
// category, by name. The input is not modified.
//
// This is the order the published example profiles are written in and
// the order a generated profile lists its features.
func SortFeatureNames(names []string) []string {
	kindRank := map[FeatureKind]int{}
	for i, k := range AllFeatureKinds() {
		kindRank[k] = i
	}
	catRank := map[string]int{}
	for i, c := range operatorCategoryOrder {
		catRank[c] = i
	}
	tablePos := map[string]int{}
	for i, n := range FeatureNames() {
		if _, dup := tablePos[n]; !dup {
			tablePos[n] = i
		}
	}

	type key struct {
		kind    int
		catRank int
		cat     string
		builtin bool
		pos     int
		name    string
	}
	keyOf := func(n string) key {
		c := FeatureCategoryOf(n)
		cr, ok := catRank[c]
		if !ok {
			cr = len(operatorCategoryOrder)
		}
		p, builtin := tablePos[n]
		return key{kind: kindRank[FeatureKindOfName(n)], catRank: cr, cat: c, builtin: builtin, pos: p, name: n}
	}

	out := append([]string(nil), names...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := keyOf(out[i]), keyOf(out[j])
		switch {
		case a.kind != b.kind:
			return a.kind < b.kind
		case a.catRank != b.catRank:
			return a.catRank < b.catRank
		case a.cat != b.cat:
			return a.cat < b.cat
		case a.builtin != b.builtin:
			return a.builtin
		case a.builtin:
			return a.pos < b.pos
		default:
			return a.name < b.name
		}
	})
	return out
}
