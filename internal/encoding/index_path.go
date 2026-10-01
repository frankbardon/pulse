package encoding

import "fmt"

// SidecarIndexPath derives the deterministic on-disk path for a
// point-lookup sidecar index built against cohortPath's key column
// set: "<cohortPath>.<keyhash>.idx". keyhash is the 16-hex-digit
// FNV-1a digest (HashKey) of the ordered key column names, joined
// with a NUL byte separator that cannot appear inside a schema field
// name — so ["ab", "c"] and ["a", "bc"] hash to distinct paths rather
// than colliding on naive concatenation.
//
// Key column order is significant: SidecarIndexPath(p, []string{"a",
// "b"}) and SidecarIndexPath(p, []string{"b", "a"}) derive different,
// stable paths, matching the composite-key contract ("key column order
// is significant") the format's Keys spec carries. Single-key builds
// (this story) pass a one-element slice; the derivation is already
// N-column-general so E2-S1's composite-key work and E4-S1's CLI /
// E1-S4's lookup service need no path-derivation change.
//
// Deterministic and pure — no filesystem access, no ordering beyond
// what the caller supplies in keyFields. Callers own key-order
// normalization (or intentional non-normalization) before calling.
func SidecarIndexPath(cohortPath string, keyFields []string) string {
	h := HashKey([]byte(joinWithNUL(keyFields)))
	return fmt.Sprintf("%s.%016x.idx", cohortPath, h)
}

// joinWithNUL concatenates parts with a NUL-byte separator. Schema
// field names cannot contain NUL bytes, so this avoids the
// concatenation-collision hazard of a plain strings.Join with no
// separator (or a separator that could itself appear in a field name).
func joinWithNUL(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "\x00" + p
	}
	return out
}
