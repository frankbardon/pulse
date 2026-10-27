package regresample

import (
	"bytes"
	"encoding/json"
	"sort"
)

// CanonicalJSON is the ONE canonical form a raw JSON slot takes before
// an rng_seed 0 derivation hashes it (DeriveSeed's spec bytes): object
// keys sorted (byte order of the decoded key, as encoding/json sorts a
// map), recursively, and whitespace dropped. Array order is kept, and
// every number and string keeps its text exactly as written — `0.10`
// stays `0.10`, a 20-digit integer keeps every digit (no float
// round-trip) — so Pulse's own markers (`{"$var": "x"}`, `{{axis}}`)
// pass through untouched. Two bodies that differ only in key order or
// whitespace therefore derive the same seed; any value change still
// moves it. A body already in sorted key order (no duplicate or
// escaped keys) canonicalises to its compact form — exactly what
// json.Marshal of the raw slot wrote before — so its seed does not
// move.
//
// Empty or malformed input is returned as given: the strict decoder
// refuses it later with its own error, and nil stays nil so an
// omitempty slot stays absent.
func CanonicalJSON(raw []byte) []byte {
	if len(raw) == 0 || !json.Valid(raw) {
		return raw
	}
	var buf bytes.Buffer
	if err := canonicalValue(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

func canonicalValue(buf *bytes.Buffer, raw []byte) error {
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
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
			if err := canonicalValue(buf, obj[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return err
		}
		buf.WriteByte('[')
		for i, v := range arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalValue(buf, v); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		// A scalar: its text as written (a string's escapes, a number's
		// digits), whitespace already trimmed.
		buf.Write(raw)
	}
	return nil
}
