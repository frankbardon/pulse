package processing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// JoinedSchema synthesises the schema produced by a single inner
// hash-join attached to a Request. Left fields appear unchanged;
// right fields are prefixed with spec.As (when set) and validated
// for collisions against the left side. The returned schema has no
// on-wire byte layout — like ChainOutputSchema, it exists to
// satisfy downstream operator lookups against an in-memory
// SliceIterator.
//
// Returns PULSE_JOIN_FIELD_COLLISION when a non-prefixed right
// field shares a name with a left field. Callers can set spec.As
// to disambiguate.
func JoinedSchema(left, right *encoding.Schema, spec *types.JoinSpec) (*encoding.Schema, error) {
	if left == nil || right == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"joined schema requires non-nil left and right schemas")
	}
	fields := make([]encoding.Field, 0, len(left.Fields)+len(right.Fields))
	seen := make(map[string]struct{}, len(left.Fields)+len(right.Fields))
	for _, f := range left.Fields {
		// Copy without the byte-offset / Dictionary reuse beyond the
		// in-memory record use. The joined records own their own
		// values map; categorical-dict lookups still work because
		// every Field references the original schema's Dictionary
		// via pointer (left side's view).
		seen[f.Name] = struct{}{}
		fields = append(fields, f)
	}
	for _, f := range right.Fields {
		name := f.Name
		if spec.As != "" {
			name = spec.As + f.Name
		}
		if _, dup := seen[name]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_COLLISION,
				"joined schema field name collides between left and right",
				map[string]any{"field": name, "as": spec.As})
		}
		seen[name] = struct{}{}
		copied := f
		copied.Name = name
		fields = append(fields, copied)
	}
	return &encoding.Schema{Fields: fields}, nil
}

// HashJoinIterator wraps a left-side iterator and yields joined
// records on each Next() call. The right side is materialised into a
// hashmap keyed by the composite (LeftField → string) tuple. Inner
// join only — non-matching left rows are dropped silently.
//
// Memory: O(right_rows × per_record_state). The orchestrator picks
// the smaller side as the build side; in v1 this is always the
// caller-provided "right" path. A future iteration adds a
// CountRecords pre-pass to swap sides automatically.
type HashJoinIterator struct {
	left      RecordIterator
	joined    *encoding.Schema
	spec      *types.JoinSpec
	rightHash map[string][]*Record
	matches   []*Record
	cursor    int
	leftRec   *Record
}

// NewHashJoinIterator builds the right-side hash table eagerly from
// the supplied right-iterator, then returns an iterator that walks
// the left side. Each left row's composite key is looked up; matched
// pairs are emitted via Next() in (left, right[0]), (left, right[1])
// order.
func NewHashJoinIterator(left RecordIterator, right []*Record, leftSchema, rightSchema *encoding.Schema, spec *types.JoinSpec) (*HashJoinIterator, *encoding.Schema, error) {
	if spec == nil {
		return nil, nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "join iterator requires a JoinSpec")
	}
	if len(spec.On) == 0 {
		return nil, nil, errors.NewCodedError(errors.PULSE_JOIN_KEYS_EMPTY,
			"join spec requires at least one OnPair")
	}
	kind := spec.Kind
	if kind == "" {
		kind = "inner"
	}
	if kind != "inner" {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED,
			"only inner join is implemented in v1",
			map[string]any{"kind": kind})
	}
	for _, pair := range spec.On {
		if pair.LeftField == "" || pair.RightField == "" {
			return nil, nil, errors.NewCodedError(errors.PULSE_JOIN_KEYS_EMPTY,
				"OnPair requires both LeftField and RightField")
		}
		if leftSchema.Field(pair.LeftField) == nil {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_UNKNOWN,
				"OnPair.LeftField not found in left schema",
				map[string]any{"field": pair.LeftField})
		}
		if rightSchema.Field(pair.RightField) == nil {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_FIELD_UNKNOWN,
				"OnPair.RightField not found in right schema",
				map[string]any{"field": pair.RightField})
		}
		lf := leftSchema.Field(pair.LeftField)
		rf := rightSchema.Field(pair.RightField)
		if !typesCompatibleForJoin(lf.Type, rf.Type) {
			return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_TYPE_MISMATCH,
				"join key types are not compatible",
				map[string]any{
					"left_field":  pair.LeftField,
					"left_type":   lf.Type.String(),
					"right_field": pair.RightField,
					"right_type":  rf.Type.String(),
				})
		}
	}

	joinedSchema, err := JoinedSchema(leftSchema, rightSchema, spec)
	if err != nil {
		return nil, nil, err
	}

	// Build the right-side hash. Composite key is the pipe-joined
	// stringified per-OnPair value. Null on any key field skips that
	// row (inner join semantics).
	rightHash := make(map[string][]*Record, len(right))
	for _, r := range right {
		key, ok := joinKeyOf(r, rightSchema, spec, true)
		if !ok {
			continue
		}
		rightHash[key] = append(rightHash[key], r)
	}

	return &HashJoinIterator{
		left:      left,
		joined:    joinedSchema,
		spec:      spec,
		rightHash: rightHash,
	}, joinedSchema, nil
}

// Next advances to the next joined record. Returns false when the
// left side is exhausted and the per-left-row match buffer is empty.
func (h *HashJoinIterator) Next() bool {
	for {
		if h.cursor < len(h.matches) {
			h.cursor++
			return true
		}
		if !h.left.Next() {
			return false
		}
		h.leftRec = h.left.Record()
		key, ok := joinKeyOf(h.leftRec, h.leftRec.schema, h.spec, false)
		if !ok {
			continue
		}
		matches := h.rightHash[key]
		if len(matches) == 0 {
			continue
		}
		h.matches = matches
		h.cursor = 0
	}
}

// Record returns the current joined record. Combines the left
// iterator's current record with the matched right record indexed by
// cursor-1 (Next() already advanced past it).
func (h *HashJoinIterator) Record() *Record {
	rightRec := h.matches[h.cursor-1]
	values := make(map[string]float64, len(h.joined.Fields))
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	for k, v := range h.leftRec.values {
		values[k] = v
	}
	for k, v := range h.leftRec.nulls {
		nulls[k] = v
	}
	for k, v := range h.leftRec.wide {
		wide[k] = v
	}
	for k, v := range rightRec.values {
		values[joinRenameRight(h.spec, k)] = v
	}
	for k, v := range rightRec.nulls {
		nulls[joinRenameRight(h.spec, k)] = v
	}
	for k, v := range rightRec.wide {
		wide[joinRenameRight(h.spec, k)] = v
	}
	return NewRecordWithWide(h.joined, values, nulls, wide)
}

// Reset rewinds the left iterator and clears per-row state. Right-
// hash retention is intentional: build-once + scan-many.
func (h *HashJoinIterator) Reset() {
	h.left.Reset()
	h.matches = nil
	h.cursor = 0
	h.leftRec = nil
}

// joinKeyOf produces the composite hash key for one record. Returns
// ok=false when any key field is null — inner join drops these rows.
// The build flag is informational; semantics are identical for both
// sides. Categorical fields resolve to dict strings; numeric fields
// stringify via canonical float formatting.
func joinKeyOf(rec *Record, schema *encoding.Schema, spec *types.JoinSpec, build bool) (string, bool) {
	var parts []string
	for _, pair := range spec.On {
		field := pair.LeftField
		if build {
			field = pair.RightField
		}
		f := schema.Field(field)
		if f == nil {
			return "", false
		}
		if rec.nulls[field] {
			return "", false
		}
		if f.Type.IsCategorical() && f.Dictionary != nil {
			v, ok := rec.values[field]
			if !ok {
				return "", false
			}
			parts = append(parts, f.Dictionary.Resolve(uint32(v)))
			continue
		}
		v, ok := rec.values[field]
		if !ok {
			return "", false
		}
		parts = append(parts, strconv.FormatFloat(v, 'g', -1, 64))
	}
	return strings.Join(parts, "|"), true
}

func joinRenameRight(spec *types.JoinSpec, name string) string {
	if spec.As == "" {
		return name
	}
	return spec.As + name
}

// typesCompatibleForJoin reports whether two schema types can be
// compared as equi-join keys after normalisation. The conservative
// v1 rule: identical types match; categorical types of any width
// match each other (dict strings normalise to text); numeric types
// of the same broad family (unsigned int family vs float family) all
// match within their family. Decimal128 keys reject across the type
// boundary (precision differences matter).
func typesCompatibleForJoin(a, b encoding.FieldType) bool {
	if a == b {
		return true
	}
	if a.IsCategorical() && b.IsCategorical() {
		return true
	}
	if joinNumericFamily(a) && joinNumericFamily(b) {
		return true
	}
	return false
}

func joinNumericFamily(t encoding.FieldType) bool {
	switch t {
	case encoding.FieldTypeU4,
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate:
		return true
	}
	return false
}

// FormatJoinKindError surfaces a unified "unsupported kind" message
// for callers that want to print the rejection reason themselves.
// Unused today; reserved for the upcoming outer/left/anti landings.
func FormatJoinKindError(kind string) string {
	return fmt.Sprintf("join kind %q is reserved", kind)
}
