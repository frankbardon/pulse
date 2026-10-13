package processing

import (
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
)

// The shared row → []float64 fill (U29 E2-S1).
//
// A resolved vector (internal/vectors.Resolved) lists its members in
// axis order; a member is a scalar column or one element of a native
// vector field (vec_f32 / vec_f64). memberFill is the ONE place a
// consumer turns a record into that vector's values, so a native vector
// and its scalar-column twin read through a virtual vector are
// indistinguishable downstream:
//
//   - a scalar member reads Record.NumericValue: absent / null → a null
//     member; a stored NaN is a value (the scalar rule, unchanged);
//   - a native element reads the vector field's storage in place (no
//     copy): a null vector → every element a null member; a NaN element
//     → a null member (the format carries no other per-element null).
//
// A null member is written NaN with present[i] = false, so a consumer
// that only needs values (expressions: a null member is a NaN element)
// ignores present, and one that counts (AGG_VEC_*: per-element n) reads
// it.

// memberFill is a compiled member-read plan for one resolved vector.
type memberFill struct {
	refs []vectors.MemberRef
}

// newMemberFill compiles r's member list.
func newMemberFill(r vectors.Resolved) memberFill {
	refs := make([]vectors.MemberRef, len(r.Members))
	for i := range refs {
		refs[i] = r.Ref(i)
	}
	return memberFill{refs: refs}
}

// memberFillOf compiles a slot's literal member-name list — the
// resolved members a vector-input slot is rewritten to before its
// factory runs (Vector cleared, Fields = Resolved.Members) — against
// schema: a name that is an element member "f[k]" of a native vector
// field of schema (vectors.ParseElementMember, the resolver's own
// reading) reads that element in place; every other name is a scalar
// column. A scalar member's read falls back to Record.memberValue, so a
// slot whose schema lost its vector metadata still reads an element
// member by name rather than seeing it null.
func memberFillOf(names []string, schema *encoding.Schema) memberFill {
	refs := make([]vectors.MemberRef, len(names))
	for i, n := range names {
		refs[i] = vectors.MemberRef{Field: n, Element: -1}
		if f, e, ok := vectors.ParseElementMember(n, schema); ok {
			refs[i] = vectors.MemberRef{Field: f, Element: e}
		}
	}
	return memberFill{refs: refs}
}

// Len is the member count.
func (f memberFill) Len() int { return len(f.refs) }

// fill writes the members of rec into dst (len ≥ Len): the value, or
// NaN for a null member; present (nil: not wanted, else len ≥ Len)
// receives each member's presence. It returns the present-member count.
func (f memberFill) fill(rec *Record, dst []float64, present []bool) int {
	n := 0
	var (
		curField string
		cur      []float64
		curOK    bool
	)
	for i, m := range f.refs {
		var x float64
		ok := false
		if m.Element < 0 {
			x, ok = rec.memberValue(m.Field)
		} else {
			if m.Field != curField {
				curField = m.Field
				cur, curOK = rec.vectorView(m.Field)
			}
			if curOK && m.Element < len(cur) {
				x = cur[m.Element]
				ok = !math.IsNaN(x)
			}
		}
		if !ok {
			x = math.NaN()
		} else {
			n++
		}
		dst[i] = x
		if present != nil {
			present[i] = ok
		}
	}
	return n
}

// complete fills dst like fill and reports whether every member is
// present and not NaN — the listwise question (a stored NaN in a scalar
// member is missing to every listwise consumer, as is a null member).
func (f memberFill) complete(rec *Record, dst []float64) bool {
	f.fill(rec, dst, nil)
	for _, v := range dst[:len(f.refs)] {
		if math.IsNaN(v) {
			return false
		}
	}
	return true
}

// vectorView returns the named vector field's elements on this record
// WITHOUT copying — valid only until the record is next written (a
// reused record is rewritten by the next decode) and never to be
// modified or retained; VectorValue is the copying accessor. (nil,
// false) when the field is null, missing, projected out or not a
// vector.
func (r *Record) vectorView(name string) ([]float64, bool) {
	if r.nullMarked(name) {
		return nil, false
	}
	if i := r.pos(name); i >= 0 && r.test(planeWide, i) {
		if r.layout.wideKind[i] != wideVector {
			return nil, false
		}
		return r.vecAt(i), true
	}
	v, ok := r.getWide(name)
	if !ok {
		return nil, false
	}
	vec, ok := v.([]float64)
	return vec, ok
}

// memberValue reads one resolved member by NAME from r: a scalar
// column's numeric value, or — when name is an element member
// "f[k]" of a vector field f on r (vectors.ElementMember) — that
// element, a NaN element or a null vector being a null member. It is
// the name-only form of memberFill for the sites that hold a slot's
// prepared member list but no plan (the aggregation floor's presence
// question).
func (r *Record) memberValue(name string) (float64, bool) {
	if x, ok := r.NumericValue(name); ok {
		return x, true
	}
	if !strings.HasSuffix(name, "]") {
		return 0, false
	}
	open := strings.LastIndexByte(name, '[')
	if open <= 0 {
		return 0, false
	}
	digits := name[open+1 : len(name)-1]
	if digits == "" || digits[0] == '0' {
		return 0, false // canonical base-10, 1..dim: no sign, no leading zero
	}
	k, err := strconv.Atoi(digits)
	if err != nil || k < 1 || digits[0] == '+' {
		return 0, false
	}
	vec, ok := r.vectorView(name[:open])
	if !ok || k > len(vec) || math.IsNaN(vec[k-1]) {
		return 0, false
	}
	return vec[k-1], true
}
