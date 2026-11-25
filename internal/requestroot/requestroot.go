// Package requestroot is the one rule that names the request root a JSON
// request document is, read off its top-level keys: a ChainRequest, a
// ComposedRequest, a FacetRequest or a single Request. The request read
// maps (internal/descriptor), the docs snippet check (internal/doccheck),
// the extension-example validator and the example library's strict-decode
// test all call it, so the four cannot drift. It is a stdlib-only leaf
// (TestRequestRoot_ImportBoundary).
package requestroot

// Kind names a request root.
type Kind string

// The request roots, spelled as descriptor.ReadsRoot spells them.
const (
	Request  Kind = "request"
	Composed Kind = "composed"
	Chain    Kind = "chain"
	Facet    Kind = "facet"
)

// ComposeKeys are the top-level keys that make a document a
// ComposedRequest (when it has no `stages`).
var ComposeKeys = []string{"requests", "sweep", "banner", "fits", "optimize", "base"}

// Detect names the root of a document whose top-level keys has reports:
// `stages` makes a chain, else any of ComposeKeys a compose, else `fields`
// a facet, else a single request. It never fails: a document that is no
// request at all reads as Request, so a caller that must tell the two
// apart decodes it.
func Detect(has func(key string) bool) Kind {
	switch {
	case has("stages"):
		return Chain
	case hasAny(has, ComposeKeys):
		return Composed
	case has("fields"):
		return Facet
	}
	return Request
}

// Of is Detect over a decoded top-level object; a key is present even
// when its value is null.
func Of[V any](top map[string]V) Kind {
	return Detect(func(k string) bool { _, ok := top[k]; return ok })
}

func hasAny(has func(string) bool, keys []string) bool {
	for _, k := range keys {
		if has(k) {
			return true
		}
	}
	return false
}
