package processing

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
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
// to disambiguate. The rule itself lives in
// internal/encoding.JoinedSchema, which predict calls too.
func JoinedSchema(left, right *encoding.Schema, spec *types.JoinSpec) (*encoding.Schema, error) {
	as := ""
	if spec != nil {
		as = spec.As
	}
	return encx.JoinedSchema(left, right, as)
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

	// leftSchema / rightSchema are the schemas joined was derived from;
	// a side record built over one of them copies into the joined
	// record by position.
	leftSchema  *encoding.Schema
	rightSchema *encoding.Schema
	cursor      int
	leftRec     *Record
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
	// Kind and OnPairs: the one join-key rule predict and the
	// validators call too (internal/encoding.JoinKeysRefusals).
	if err := encx.JoinKeysRefusal(leftSchema, rightSchema, spec); err != nil {
		return nil, nil, err
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
		left:        left,
		joined:      joinedSchema,
		leftSchema:  leftSchema,
		rightSchema: rightSchema,
		spec:        spec,
		rightHash:   rightHash,
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
	out := newPositionalRecord(h.joined)
	// JoinedSchema lays the left fields down first, then the (renamed)
	// right fields, so a side record built over the schema the join was
	// bound with copies by position; anything else — and every
	// off-schema overflow entry — resolves by name.
	h.leftRec.copyStateInto(out, joinIdentity, 0, h.leftRec.schema == h.leftSchema)
	h.copyRight(rightRec, out)
	return out
}

func joinIdentity(name string) string { return name }

// copyRight copies a matched right record into the joined record under
// the right-side rename.
func (h *HashJoinIterator) copyRight(rightRec, out *Record) {
	rename := func(name string) string { return joinRenameRight(h.spec, name) }
	rightRec.copyStateInto(out, rename, len(h.leftSchema.Fields), rightRec.schema == h.rightSchema)
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
// sides. Categorical fields resolve to dict strings; decimal128
// fields stringify their EXACT 128-bit mantissa bytes; other numeric
// fields stringify via canonical float formatting.
//
// decimal128 gets the exact-bytes treatment for the same reason
// KeyFieldOnWireBytes gives it one (internal/processing/index_key.go): the
// value in Record.values is only the Float64(scale) echo, so two
// decimals whose difference falls below float64's mantissa spacing
// would share a key and join as equal. Nothing is lost by
// special-casing it — internal/encoding.JoinKeyTypesCompatible admits
// decimal128 only against decimal128, so a decimal
// key is never normalised against another family's float formatting.
//
// Set columns never reach here: the join-key rule
// (internal/encoding.JoinKeysRefusals) rejects them at
// NewHashJoinIterator. See internal/encoding.JoinKeySetRejection.
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
		if rec.nullMarked(field) {
			return "", false
		}
		if f.Type.IsCategorical() && f.Dictionary != nil {
			v, ok := rec.rawValue(field)
			if !ok {
				return "", false
			}
			parts = append(parts, f.Dictionary.Resolve(uint32(v)))
			continue
		}
		if f.Type == encoding.FieldTypeDecimal128 {
			wv, ok := rec.WideValue(field)
			if !ok {
				return "", false
			}
			d, ok := wv.(encoding.Decimal128)
			if !ok {
				return "", false
			}
			enc := encoding.EncodeDecimal128(d)
			parts = append(parts, string(enc[:]))
			continue
		}
		v, ok := rec.rawValue(field)
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

// FormatJoinKindError surfaces a unified "unsupported kind" message
// for callers that want to print the rejection reason themselves.
// Unused today; reserved for the upcoming outer/left/anti landings.
func FormatJoinKindError(kind string) string {
	return fmt.Sprintf("join kind %q is reserved", kind)
}
