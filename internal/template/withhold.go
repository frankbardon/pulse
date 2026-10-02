package template

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/types"
)

// RenderOptions tunes RenderWith. The zero value is Render.
type RenderOptions struct {
	// WithheldSlots, when non-nil, reports the top-level JSON keys a
	// request root does NOT offer on the caller's engine. root is a
	// pointer to one of the request roots (*types.Request,
	// *types.ComposedRequest, *types.ChainRequest, *types.FacetRequest,
	// *types.SampleRequest); the result is a list of json keys of that
	// root. A withheld key is decoded exactly as a key the target type
	// does not declare: the strict decode refuses it with the
	// unknown-field PULSE_TEMPLATE_RENDER_INVALID, at the same point in
	// the document an unknown key would have been reached.
	//
	// It is consulted for the target root and, for the composed and
	// chain targets, for each nested request (requests[i],
	// stages[i].request). The package cannot see the engine, so the
	// facade supplies it.
	WithheldSlots func(root any) []string
}

// withheldMarker is appended to a withheld key before the strict decode
// so the decoder reports it as unknown, in stream order, alongside any
// genuinely unknown key. No request struct declares a key carrying it.
const withheldMarker = "\x00"

// withholdSlots rewrites raw so every withheld key at a gated position
// is renamed key+withheldMarker. Bytes are returned unchanged when no
// key is withheld. A non-object where an object is expected is left
// alone — the strict decode reports it as a structural mismatch.
func withholdSlots(target Target, raw json.RawMessage, withheld func(any) []string) (json.RawMessage, error) {
	requestKeys := func() []string { return withheld(new(types.Request)) }
	nestedRequest := func(v json.RawMessage) (json.RawMessage, error) {
		return rewriteObject(v, requestKeys(), nil)
	}
	switch target {
	case TargetRequest:
		return rewriteObject(raw, requestKeys(), nil)
	case TargetComposed:
		return rewriteObject(raw, withheld(new(types.ComposedRequest)), map[string]func(json.RawMessage) (json.RawMessage, error){
			"requests": func(v json.RawMessage) (json.RawMessage, error) { return rewriteArray(v, nestedRequest) },
		})
	case TargetChain:
		stage := func(v json.RawMessage) (json.RawMessage, error) {
			return rewriteObject(v, nil, map[string]func(json.RawMessage) (json.RawMessage, error){
				"request": nestedRequest,
			})
		}
		return rewriteObject(raw, withheld(new(types.ChainRequest)), map[string]func(json.RawMessage) (json.RawMessage, error){
			"stages": func(v json.RawMessage) (json.RawMessage, error) { return rewriteArray(v, stage) },
		})
	case TargetFacet:
		return rewriteObject(raw, withheld(new(types.FacetRequest)), nil)
	case TargetSample:
		return rewriteObject(raw, withheld(new(types.SampleRequest)), nil)
	}
	return raw, nil
}

// rewriteObject walks one JSON object's members in document order,
// renaming members whose key matches a withheld key (case-insensitively,
// as encoding/json matches field names) and recursing into members named
// in descend. It returns raw itself when nothing changed.
func rewriteObject(raw json.RawMessage, withheld []string, descend map[string]func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	if len(withheld) == 0 && len(descend) == 0 {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return raw, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	changed := false
	for i := 0; dec.More(); i++ {
		tok, err := dec.Token()
		if err != nil {
			return raw, nil
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return raw, nil
		}
		outKey := key
		if matchKey(key, withheld) {
			outKey = key + withheldMarker
			changed = true
		} else {
			for name, fn := range descend {
				if !strings.EqualFold(key, name) {
					continue
				}
				nv, err := fn(val)
				if err != nil {
					return nil, err
				}
				if !bytes.Equal(nv, val) {
					val = nv
					changed = true
				}
			}
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(outKey)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(val)
	}
	if !changed {
		return raw, nil
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// rewriteArray applies elem to each element of a JSON array, returning
// raw itself when nothing changed (or when raw is not an array).
func rewriteArray(raw json.RawMessage, elem func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return raw, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	changed := false
	for i := 0; dec.More(); i++ {
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return raw, nil
		}
		nv, err := elem(val)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(nv, val) {
			changed = true
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(nv)
	}
	if !changed {
		return raw, nil
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

func matchKey(key string, keys []string) bool {
	for _, k := range keys {
		if strings.EqualFold(key, k) {
			return true
		}
	}
	return false
}

// unwithheldCause maps a decoder unknown-field fault on a renamed key
// back to the fault the decoder raises for an unknown key of the
// original spelling, so the error — message, details AND cause — is the
// one an unknown key produces.
func unwithheldCause(cause error) error {
	field, ok := unknownFieldName(cause)
	if !ok || !strings.HasSuffix(field, withheldMarker) {
		return cause
	}
	return stderrors.New(unknownFieldPrefix + strconv.Quote(strings.TrimSuffix(field, withheldMarker)))
}
