package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// CanonicalHash returns a deterministic 32-character hex hash of v's
// canonical JSON form. The hash is stable across processes and Pulse
// versions where the request's semantic meaning is unchanged.
//
// The algorithm: encoding/json.Marshal(v) → walk the resulting JSON
// tree to drop unset entries (omitempty already handled by the struct
// tags, so this step is mostly a no-op for the request types) and
// normalize numeric edge cases (negative zero collapses to zero) →
// re-emit with sorted map keys and no whitespace → SHA-256 of the
// canonical bytes prefixed with a domain tag → first 16 bytes hex.
//
// tag namespaces hashes so a Request and a ComposedRequest with the
// same wire bytes do not collide. Callers can use "" for an anonymous
// hash but the concrete Request.Hash / ComposedRequest.Hash / etc.
// methods always pass a non-empty tag.
func CanonicalHash(tag string, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	canon, err := canonicalizeJSON(b)
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(tag))
	h.Write([]byte{0})
	h.Write(canon)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func canonicalizeJSON(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	v = normalizeJSON(v)
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// normalizeJSON walks the decoded tree and collapses representational
// variants of the same value (negative zero, redundant exponent forms)
// to a single canonical spelling. The walk is destructive on maps and
// slices but never replaces the container itself.
func normalizeJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			x[k] = normalizeJSON(vv)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = normalizeJSON(vv)
		}
		return x
	case json.Number:
		return normalizeNumber(x)
	default:
		return v
	}
}

// normalizeNumber maps JSON numeric literals to a canonical spelling:
//   - negative zero -> "0"
//   - integer-valued numbers keep their integer form
//   - other floats parse + re-emit via strconv.FormatFloat 'g', -1
func normalizeNumber(n json.Number) json.Number {
	s := string(n)
	// Cheap integer path: no '.' / no 'e' / no 'E'.
	if !strings.ContainsAny(s, ".eE") {
		// Strip leading minus on zero.
		if strings.TrimLeft(s, "-0") == "" {
			return "0"
		}
		return n
	}
	f, err := n.Float64()
	if err != nil {
		return n
	}
	if f == 0 {
		return "0"
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
}

func writeCanonicalJSON(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(string(x))
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, vv := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalJSON(buf, vv); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonicalJSON(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	}
	return nil
}

// Hash returns the canonical content hash of the Request. Same logical
// request → same hash, across processes and Pulse versions where the
// request's semantic meaning is unchanged. See CanonicalHash for the
// algorithm.
//
// Slot coverage is data-driven: every JSON-tagged field on Request
// participates in the hash via the json.Marshal → canonicalize →
// sha256 pipeline. The Overlays slot is covered automatically
// — each OverlaySpec contributes its Name/Kind/Scope/Ref to the
// canonical bytes in declared spec order (slices preserve order), and
// the discriminated OverlayRef union hashes only the populated arm
// because every family pointer carries `json:",omitempty"`. An
// overlay-free Request (nil or empty Overlays slice) omits the
// `overlays` key entirely via the slot's own `omitempty` tag, so its
// hash is byte-identical to the pre-Overlays canonical form — see
// TestCanonicalHash_OverlayFreeByteIdentity.
func (r *Request) Hash() string {
	if r == nil {
		return CanonicalHash("request", (*Request)(nil))
	}
	return CanonicalHash("request", r)
}

// Hash returns the canonical content hash of the ComposedRequest.
//
// Slot coverage is data-driven: every JSON-tagged field on
// ComposedRequest participates in the hash via the json.Marshal →
// canonicalize → sha256 pipeline. The per-Request Label slot folds in
// through the json walk of each *Request — empty Labels are
// `omitempty` and stay byte-identical to the pre-Label form. Before
// hashing, the helper applies the auto-default rule that
// service/compose_label.go uses at validate time: every slot whose
// Label arrives empty is rewritten to `request_<index+1>` (1-based)
// against a shallow clone, so two ComposedRequests differing only in
// empty-vs-auto-defaulted Labels hash identically. Callers that
// already filled the Label explicitly are unaffected — the auto-
// default only fires when Label is empty. This mirrors the validate-
// time normalizer in service/compose_label.go so hash equality lines
// up with execution-time slot identity.
//
// The Compose-level Overlays slot is covered automatically —
// each ComposeOverlaySpec contributes its Name / Kind / Scope /
// Reference / Targets / Level / Within / Params to the canonical
// bytes in declared spec order (slices preserve order). An overlay-
// free ComposedRequest (nil or empty Overlays slice) omits the
// `overlays` key entirely via the slot's own `omitempty` tag, so its
// hash is byte-identical to the pre-Overlays canonical form — see
// TestComposedRequest_OverlayFreeByteIdentity.
func (r *ComposedRequest) Hash() string {
	if r == nil {
		return CanonicalHash("composed", (*ComposedRequest)(nil))
	}
	return CanonicalHash("composed", normalizeComposedRequestForHash(r))
}

// normalizeComposedRequestForHash returns a shallow-clone projection of
// the ComposedRequest whose per-slot Requests carry the
// service/compose_label.go auto-defaulted Label values. The clone is
// hash-only: the caller's *ComposedRequest pointer is never mutated,
// and every other slot (Cohort, Aggregations, Overlays, etc.) shares
// the underlying slices because canonical-hash only needs to walk the
// JSON projection. Mirrors service/compose_label.go's
// applyComposeLabelDefaults — the two normalizers are intentionally
// kept in lockstep so hash equality matches validate-time slot
// identity. We deliberately do NOT import the service package from
// types (types stays at the bottom of the import graph); the helper
// duplicates the one-line auto-default rule and the unit tests assert
// the rule stays in lockstep.
func normalizeComposedRequestForHash(r *ComposedRequest) *ComposedRequest {
	if r == nil {
		return nil
	}
	if len(r.Requests) == 0 {
		// Nothing to normalize — the slot list is the only carrier of
		// Label. Returning r directly keeps the pre-Overlays byte form
		// for callers that authored an empty Requests slice.
		return r
	}
	clone := *r
	requests := make([]*Request, len(r.Requests))
	for i, src := range r.Requests {
		if src == nil {
			requests[i] = nil
			continue
		}
		if src.Label != "" {
			// Caller-supplied Label passes through verbatim — no
			// clone, no allocation. Matches the validate-time
			// normalizer's "explicit value wins" rule.
			requests[i] = src
			continue
		}
		// Shallow-clone the Request so the synthesised Label does
		// not leak into the caller's pointer. Every other slot on
		// the *Request shares the underlying slice / pointer with
		// the caller's value — the hash walker is read-only over
		// the JSON projection.
		clone := *src
		clone.Label = composeLabelDefault(i)
		requests[i] = &clone
	}
	clone.Requests = requests
	return &clone
}

// composeLabelDefault is the canonical compose auto-default rule:
// every slot whose Label arrives empty resolves to `request_<index+1>`
// (1-based). Kept as a separate helper so the rule has one source of
// truth across the canonical-hash normalizer (above) and any future
// types-layer consumer. The service-layer validator
// (service/compose_label.go) carries the same one-liner — the two
// must stay in lockstep so hash equality lines up with execution-time
// slot identity. We deliberately do NOT use fmt.Sprintf here: the
// canonical-hash codepath bans fmt-built strings (the structural-
// defense rationale in CLAUDE.md "Output Format Contract" applies to
// JSON envelope construction, but we keep the discipline here too
// because the helper is called per Compose slot during dedup-cache
// lookups and the byte-buffer pattern is cheaper).
func composeLabelDefault(i int) string {
	var buf [32]byte
	out := append(buf[:0], "request_"...)
	out = strconv.AppendInt(out, int64(i+1), 10)
	return string(out)
}

// Hash returns the canonical content hash of the FacetRequest.
func (r *FacetRequest) Hash() string {
	if r == nil {
		return CanonicalHash("facet", (*FacetRequest)(nil))
	}
	return CanonicalHash("facet", r)
}

// Hash returns the canonical content hash of the ChainRequest.
//
// Slot coverage is data-driven: every JSON-tagged field on
// ChainRequest participates via the json.Marshal → canonicalize →
// sha256 pipeline. The whole-chain Overlays slot is covered
// automatically — each ChainOverlaySpec contributes its Name / Kind /
// Ref{Index,Name} / Target{Index,Name} / Scope / Params to the
// canonical bytes in declared spec order (slices preserve order),
// and the StageRef discriminated reference hashes only the populated
// arm because every field carries `json:",omitempty"`. An
// overlay-free ChainRequest (nil or empty Overlays slice) omits the
// `overlays` key entirely via the slot's own `omitempty` tag, so its
// hash is byte-identical to the pre-Overlays canonical form — see
// TestChainCanonicalHash_OverlayFreeByteIdentity.
func (r *ChainRequest) Hash() string {
	if r == nil {
		return CanonicalHash("chain", (*ChainRequest)(nil))
	}
	return CanonicalHash("chain", r)
}

// Hash returns the canonical content hash of the LookupRequest.
func (r *LookupRequest) Hash() string {
	if r == nil {
		return CanonicalHash("lookup", (*LookupRequest)(nil))
	}
	return CanonicalHash("lookup", r)
}
