package processing

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// MemberSet is a read-only set of field values used by the cohort
// filter include-from path to test record membership. Three concrete
// impls back this interface, picked by field type at load time so the
// per-row lookup runs on the fastest path available:
//
//   - *BitsetSet — categorical fields. One bit per dictionary entry.
//     Lookup is a single word load + bit test, no hashing, no compare.
//   - *Uint64Set — integer / date / bit-packed integer fields. Backed
//     by map[uint64]struct{}; avoids the per-row string allocation a
//     string-keyed map would force.
//   - *StringSet — fallback (decimal128, any non-categorical string
//     surface). Backed by map[string]struct{}.
//
// Float fields (f32/f64) are rejected at load time. Exact-equality
// membership on floats is a footgun; the caller is expected to use the
// expression filter for numeric ranges.
//
// The interface itself carries only metadata; the per-row hot path
// goes through the concrete type via the predicate built by
// BuildMemberSetPredicate, which type-switches once at construction and
// returns a closure with no interface dispatch in the loop.
type MemberSet interface {
	Len() int
	Kind() string
}

// BitsetSet stores membership as a packed bit per dictionary id.
//
// Memory: dictSize bits, rounded up to a uint64 word. A categorical_u32
// dict at its 4 294 967 295 cap would be ~512 MiB, but in practice
// categorical dicts run much smaller — a 65 536-entry categorical_u16
// is 8 KiB.
type BitsetSet struct {
	bits []uint64
	n    int
}

func (b *BitsetSet) Len() int     { return b.n }
func (b *BitsetSet) Kind() string { return "bitset" }

// Contains reports whether id is set. Out-of-range ids return false
// without panicking — the loader can build a bitset sized to the
// dictionary, but records read against a later, larger dictionary on a
// shard archive could in principle present an id beyond the bitset.
func (b *BitsetSet) Contains(id uint32) bool {
	w := int(id) >> 6
	if w >= len(b.bits) {
		return false
	}
	return b.bits[w]&(uint64(1)<<(uint(id)&63)) != 0
}

// Add marks id as present. Idempotent.
func (b *BitsetSet) Add(id uint32) {
	w := int(id) >> 6
	if w >= len(b.bits) {
		return
	}
	bit := uint64(1) << (uint(id) & 63)
	if b.bits[w]&bit == 0 {
		b.bits[w] |= bit
		b.n++
	}
}

func newBitsetSet(dictSize int) *BitsetSet {
	if dictSize < 0 {
		dictSize = 0
	}
	return &BitsetSet{bits: make([]uint64, (dictSize+63)>>6)}
}

// Uint64Set stores integer membership.
type Uint64Set struct {
	m map[uint64]struct{}
}

func (s *Uint64Set) Len() int               { return len(s.m) }
func (s *Uint64Set) Kind() string           { return "uint64" }
func (s *Uint64Set) Contains(v uint64) bool { _, ok := s.m[v]; return ok }
func (s *Uint64Set) Add(v uint64)           { s.m[v] = struct{}{} }

func newUint64Set(hint int) *Uint64Set {
	if hint < 0 {
		hint = 0
	}
	return &Uint64Set{m: make(map[uint64]struct{}, hint)}
}

// StringSet stores string membership (decimal128 fields, fallback path).
type StringSet struct {
	m map[string]struct{}
}

func (s *StringSet) Len() int     { return len(s.m) }
func (s *StringSet) Kind() string { return "string" }
func (s *StringSet) Contains(v string) bool {
	_, ok := s.m[v]
	return ok
}
func (s *StringSet) Add(v string) { s.m[v] = struct{}{} }

func newStringSet(hint int) *StringSet {
	if hint < 0 {
		hint = 0
	}
	return &StringSet{m: make(map[string]struct{}, hint)}
}

// LoadMemberSetResult is the per-load report returned by
// LoadMemberSetFromReader. Lines is total non-blank lines processed;
// NotInDictionary counts categorical lookups whose value was absent
// from the field's dictionary (those keys can never match a record so
// they're dropped); Invalid counts numeric lines that failed to parse
// on the integer path. Callers can decide whether to surface each
// counter as a warning or hard error.
type LoadMemberSetResult struct {
	Set             MemberSet
	Lines           int
	NotInDictionary int
	Invalid         int
}

// LoadMemberSetFromReader reads newline-delimited values from r and
// builds the best MemberSet impl for the named field on schema. Lines
// are trimmed of surrounding whitespace (covers CRLF). A UTF-8 BOM is
// stripped from the very first line. Blank lines are skipped silently
// — they don't count against Lines.
//
// Dispatch by field type:
//
//   - categorical_u{8,16,32}: parse line as string, resolve via
//     dict.IDFor → BitsetSet. Lines not in the dictionary are counted
//     in NotInDictionary and dropped — they can never match a record.
//   - u8/u16/u32/u64, nullable_u4/u8/u16, nullable_bool, packed_bool,
//     date: parse as uint64 → Uint64Set. ParseUint failures are
//     counted in Invalid and dropped.
//   - decimal128 / nullable_decimal128: parse as string → StringSet.
//     The cohort filter compares the literal text against the
//     decimal128 wide value's String() form; exact match required.
//
// Float fields are rejected with SERVICE_VALIDATION. Unknown field
// names return SERVICE_VALIDATION.
func LoadMemberSetFromReader(r io.Reader, schema *encoding.Schema, fieldName string) (LoadMemberSetResult, error) {
	f := schema.Field(fieldName)
	if f == nil {
		return LoadMemberSetResult{}, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			fmt.Sprintf("include field %q not present in cohort schema", fieldName),
			map[string]any{"field": fieldName})
	}
	if f.Type == encoding.FieldTypeF32 || f.Type == encoding.FieldTypeF64 {
		return LoadMemberSetResult{}, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			fmt.Sprintf("include-set membership on float field %q is unsupported; use --filter for numeric ranges", fieldName),
			map[string]any{"field": fieldName, "type": f.Type.String()})
	}

	var (
		bitset *BitsetSet
		uset   *Uint64Set
		sset   *StringSet
	)
	switch {
	case f.Type.IsCategorical() && f.Dictionary != nil:
		bitset = newBitsetSet(f.Dictionary.Count())
	case f.Type == encoding.FieldTypeU8, f.Type == encoding.FieldTypeU16,
		f.Type == encoding.FieldTypeU32, f.Type == encoding.FieldTypeU64,
		f.Type == encoding.FieldTypeNullableU4, f.Type == encoding.FieldTypeNullableU8,
		f.Type == encoding.FieldTypeNullableU16, f.Type == encoding.FieldTypeNullableBool,
		f.Type == encoding.FieldTypePackedBool, f.Type == encoding.FieldTypeDate:
		uset = newUint64Set(1024)
	default:
		sset = newStringSet(1024)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var (
		lines     int
		notInDict int
		invalid   int
		first     = true
	)
	for sc.Scan() {
		line := sc.Bytes()
		if first {
			first = false
			if len(line) >= 3 && line[0] == 0xEF && line[1] == 0xBB && line[2] == 0xBF {
				line = line[3:]
			}
		}
		s := strings.TrimSpace(string(line))
		if s == "" {
			continue
		}
		lines++
		switch {
		case bitset != nil:
			id, ok := f.Dictionary.IDFor(s)
			if !ok {
				notInDict++
				continue
			}
			bitset.Add(id)
		case uset != nil:
			v, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				invalid++
				continue
			}
			uset.Add(v)
		default:
			sset.Add(s)
		}
	}
	if err := sc.Err(); err != nil {
		return LoadMemberSetResult{}, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"reading include-from values")
	}

	res := LoadMemberSetResult{Lines: lines, NotInDictionary: notInDict, Invalid: invalid}
	switch {
	case bitset != nil:
		res.Set = bitset
	case uset != nil:
		res.Set = uset
	default:
		res.Set = sset
	}
	return res, nil
}

// BuildMemberSetPredicate returns a per-row FilterFunc that reports
// whether the record's value for fieldName is a member of set. The
// concrete predicate is selected once at construction by type-switching
// on the set + schema field type — the returned closure carries no
// interface dispatch on the hot path.
//
// Float fields are rejected, matching LoadMemberSetFromReader. Unknown
// field names return SERVICE_VALIDATION.
func BuildMemberSetPredicate(set MemberSet, schema *encoding.Schema, fieldName string) (FilterFunc, error) {
	if set == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"BuildMemberSetPredicate requires a non-nil set")
	}
	f := schema.Field(fieldName)
	if f == nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			fmt.Sprintf("include field %q not present in cohort schema", fieldName),
			map[string]any{"field": fieldName})
	}
	if f.Type == encoding.FieldTypeF32 || f.Type == encoding.FieldTypeF64 {
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			fmt.Sprintf("include-set membership on float field %q is unsupported", fieldName),
			map[string]any{"field": fieldName, "type": f.Type.String()})
	}

	switch s := set.(type) {
	case *BitsetSet:
		if !f.Type.IsCategorical() {
			return nil, errors.NewCodedErrorWithDetails(
				errors.SERVICE_VALIDATION,
				fmt.Sprintf("BitsetSet predicate requires a categorical field; %q is %s", fieldName, f.Type),
				map[string]any{"field": fieldName, "type": f.Type.String()})
		}
		field := fieldName
		return func(rec *Record) (bool, error) {
			v, ok := rec.NumericValue(field)
			if !ok {
				return false, nil
			}
			return s.Contains(uint32(v)), nil
		}, nil
	case *Uint64Set:
		field := fieldName
		isCategorical := f.Type.IsCategorical()
		return func(rec *Record) (bool, error) {
			v, ok := rec.NumericValue(field)
			if !ok {
				return false, nil
			}
			if isCategorical {
				// Categorical-but-loaded-as-uint64 means caller built
				// the set against raw dict ids. Treat the float as the
				// id directly.
				return s.Contains(uint64(uint32(v))), nil
			}
			return s.Contains(uint64(v)), nil
		}, nil
	case *StringSet:
		field := fieldName
		if f.Type.IsCategorical() {
			return func(rec *Record) (bool, error) {
				sv, ok := rec.StringValue(field)
				if !ok {
					return false, nil
				}
				return s.Contains(sv), nil
			}, nil
		}
		if f.Type.IsDecimal() {
			scale := f.Scale
			return func(rec *Record) (bool, error) {
				wv, ok := rec.WideValue(field)
				if !ok {
					return false, nil
				}
				dec, isDec := wv.(encoding.Decimal128)
				if !isDec {
					return false, nil
				}
				return s.Contains(dec.String(scale)), nil
			}, nil
		}
		return func(rec *Record) (bool, error) {
			v, ok := rec.NumericValue(field)
			if !ok {
				return false, nil
			}
			return s.Contains(strconv.FormatUint(uint64(v), 10)), nil
		}, nil
	default:
		return nil, errors.NewCodedErrorWithDetails(
			errors.SERVICE_VALIDATION,
			fmt.Sprintf("unsupported MemberSet impl: %T", set),
			map[string]any{"kind": "unknown"})
	}
}
