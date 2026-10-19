package pulse

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"math"
	"slices"
	"sort"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// The filter-to-file dedup identity of what a predicate reads beyond
// the request itself (U34 E3-S6). A predicate naming a registered table
// reads that table's CONTENT, which two instances may register under the
// same name with different rows; a predicate running embedder Go code
// (an extension filterer, an ExprFunction, a function-driven lookup
// table) reads behaviour no content digest can name. The first keys the
// predicate hash on a content digest of every table the predicate
// references; the second disables reuse, so two instances never share an
// output their registrations could make differ.

// Table kinds a predicate can reference by name.
const (
	tableKindLookup    = "lookup"    // Extensions.LookupTables via lookup()
	tableKindReference = "reference" // Extensions.ReferenceVectors via ref()
	tableKindRange     = "range"     // Extensions.RangeTables via FILTER_DATE_RANGES params.table
)

// tableIdentity is one referenced table's dedup identity: its kind, name
// and the SHA-256 of its content.
type tableIdentity struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// predicateDependencies returns the content identities of the
// registered tables the predicate references — expr's lookup() and ref()
// table names and every direct FILTER_DATE_RANGES params.table — sorted
// by (kind, name), nil when it references none; and opaque, true when
// the predicate runs embedder code whose identity no digest names: a
// direct extension filterer, a call of a registered ExprFunction, or a
// lookup() reaching a function-driven (Lookup) table. A call whose table
// argument is not a string literal may read any table of its kind, so
// every registered one of that kind joins. A name nothing registers
// contributes nothing (the run refuses it). Only referenced tables
// join, so registering another table never moves the hash.
func predicateDependencies(reg *processing.ExtensionRegistry, expr string, direct []*types.Filterer) (tables []tableIdentity, opaque bool) {
	if reg == nil {
		return nil, false
	}
	for _, f := range direct {
		if f == nil {
			continue
		}
		if _, ext := reg.Filterers[f.Type]; ext {
			opaque = true
		}
		if f.Type == types.FILTER_DATE_RANGES && len(f.Params) > 0 {
			var params struct {
				Table string `json:"table"`
			}
			if json.Unmarshal(f.Params, &params) == nil && params.Table != "" {
				if t, ok := reg.RangeTables[params.Table]; ok {
					tables = append(tables, tableIdentity{Kind: tableKindRange, Name: params.Table, SHA256: rangeTableDigest(t)})
				}
			}
		}
	}
	if expr != "" {
		callees, _ := exprident.Callees(expr)
		for _, fn := range reg.ExprFunctions {
			if slices.Contains(callees, fn.Name) {
				opaque = true
			}
		}
		if slices.Contains(callees, exprenv.LookupFunc) {
			for _, name := range referencedNames(expr, exprenv.LookupFunc, mapKeys(reg.LookupTables)) {
				t := reg.LookupTables[name]
				if t.Lookup != nil {
					opaque = true
					continue
				}
				tables = append(tables, tableIdentity{Kind: tableKindLookup, Name: name, SHA256: lookupTableDigest(t)})
			}
		}
		if slices.Contains(callees, exprenv.RefFunc) {
			for _, name := range referencedNames(expr, exprenv.RefFunc, mapKeys(reg.ReferenceVectors)) {
				tables = append(tables, tableIdentity{Kind: tableKindReference, Name: name, SHA256: referenceTableDigest(reg.ReferenceVectors[name])})
			}
		}
	}
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Kind != tables[j].Kind {
			return tables[i].Kind < tables[j].Kind
		}
		return tables[i].Name < tables[j].Name
	})
	tables = slices.CompactFunc(tables, func(a, b tableIdentity) bool { return a.Kind == b.Kind && a.Name == b.Name })
	if len(tables) == 0 {
		tables = nil
	}
	return tables, opaque
}

// referencedNames is the registered table names expr's calls of fn name
// in their first argument: the literal ones registered, or every
// registered name when a call's table argument is not a literal.
func referencedNames(expr, fn string, registered []string) []string {
	lits, dynamic, _ := exprident.StringArgs(expr, fn, 0)
	if dynamic {
		return registered
	}
	out := make([]string, 0, len(lits))
	for _, n := range lits {
		if slices.Contains(registered, n) {
			out = append(out, n)
		}
	}
	return out
}

// mapKeys is m's keys, sorted.
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// digestWriter writes length-prefixed, type-tagged values into a hash so
// two different contents can never write the same bytes.
type digestWriter struct{ h hash.Hash }

func newDigest(tag string) digestWriter {
	d := digestWriter{h: sha256.New()}
	d.str(tag)
	return d
}

func (d digestWriter) u64(v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	d.h.Write(b[:])
}

func (d digestWriter) str(s string) {
	d.u64(uint64(len(s)))
	d.h.Write([]byte(s))
}

func (d digestWriter) f64(v float64) { d.u64(math.Float64bits(v)) }

func (d digestWriter) optStr(s *string) {
	if s == nil {
		d.u64(0)
		return
	}
	d.u64(1)
	d.str(*s)
}

func (d digestWriter) optF64(v *float64) {
	if v == nil {
		d.u64(0)
		return
	}
	d.u64(1)
	d.f64(*v)
}

func (d digestWriter) sum() string { return hex.EncodeToString(d.h.Sum(nil)) }

// lookupTableDigest digests a Rows-backed lookup table: its rows in key
// order, each value's float64 bits.
func lookupTableDigest(t processing.LookupTable) string {
	d := newDigest("lookup_table")
	keys := mapKeys(t.Rows)
	d.u64(uint64(len(keys)))
	for _, k := range keys {
		d.str(k)
		d.f64(t.Rows[k])
	}
	return d.sum()
}

// referenceTableDigest digests a reference-vector table: its labels in
// order, then each profile in key order.
func referenceTableDigest(t vectors.ReferenceTable) string {
	d := newDigest("reference_table")
	d.u64(uint64(len(t.Labels)))
	for _, l := range t.Labels {
		d.str(l)
	}
	keys := mapKeys(t.Profiles)
	d.u64(uint64(len(keys)))
	for _, k := range keys {
		d.str(k)
		vs := t.Profiles[k]
		d.u64(uint64(len(vs)))
		for _, v := range vs {
			d.f64(v)
		}
	}
	return d.sum()
}

// rangeTableDigest digests a range table: its date ranges, then its
// numeric ranges, each in table order (order is part of the table).
func rangeTableDigest(t processing.RangeTable) string {
	d := newDigest("range_table")
	d.u64(uint64(len(t.Ranges)))
	for _, r := range t.Ranges {
		d.str(r.Label)
		d.optStr(r.Start)
		d.optStr(r.End)
	}
	d.u64(uint64(len(t.NumericRanges)))
	for _, r := range t.NumericRanges {
		d.str(r.Label)
		d.optF64(r.Start)
		d.optF64(r.End)
	}
	return d.sum()
}
