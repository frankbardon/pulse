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
// sha256 pipeline. The Overlays slot (E1-S1) is covered automatically
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
func (r *ComposedRequest) Hash() string {
	if r == nil {
		return CanonicalHash("composed", (*ComposedRequest)(nil))
	}
	return CanonicalHash("composed", r)
}

// Hash returns the canonical content hash of the FacetRequest.
func (r *FacetRequest) Hash() string {
	if r == nil {
		return CanonicalHash("facet", (*FacetRequest)(nil))
	}
	return CanonicalHash("facet", r)
}

// Hash returns the canonical content hash of the ChainRequest.
func (r *ChainRequest) Hash() string {
	if r == nil {
		return CanonicalHash("chain", (*ChainRequest)(nil))
	}
	return CanonicalHash("chain", r)
}
