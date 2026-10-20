package pulse

import (
	"encoding/json"
)

// probeParams synthesizes the params object the registration-time probe
// hands a factory (on the spec's Params, and on extend.MatrixInput.Params
// for a matrix operator), so a factory that validates its own declared
// contract passes the probe. Since U53 E1-S3 a request can only reach a
// factory carrying every Required param and exactly one member of every
// ExactlyOneOf group (predict and runtime refuse the rest), so the probe
// hands the factory an object that satisfies the same declaration:
//
//   - every Required param, and the first-declared member of every
//     ExactlyOneOf group, gets its ParamMeta.Example when the
//     registration declares one (pulse.New has already checked it is a
//     value of the JSONType), else a placeholder of its JSONType;
//   - every other param stays absent (a Default is the factory's to
//     apply, exactly as on a request that omits it).
//
// The placeholder per JSONType: number 1, boolean false, object {}, and —
// when the probe builds against named fields (a regression's synthetic
// predictors, a matrix operator's synthetic members; fields) — string
// the first of them and array all of them, else "" and []. Every
// ParamMeta.JSONType is in the closed set validateParams admits, so a
// placeholder can always be built; a factory that accepts only certain
// values declares an Example the probe hands it instead. A factory
// that refuses its placeholder (or Example) is
// PULSE_EXTENSION_FACTORY_PANIC like any other probe factory error,
// with the object it was handed on details.probe_params.
//
// Nil when no param needs a value: a registration with no Required
// param and no ExactlyOneOf group is probed with absent params, byte for
// byte as before.
func probeParams(params []ParamMeta, fields []string) json.RawMessage {
	var obj map[string]any
	seenGroup := map[string]bool{}
	for _, p := range params {
		if !p.Required {
			if p.ExactlyOneOf == "" || seenGroup[p.ExactlyOneOf] {
				continue
			}
			seenGroup[p.ExactlyOneOf] = true
		}
		if obj == nil {
			obj = map[string]any{}
		}
		if p.Example != nil {
			obj[p.Name] = p.Example
			continue
		}
		obj[p.Name] = probeParamValue(p.JSONType, fields)
	}
	if obj == nil {
		return nil
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		panic(err) // unreachable: placeholders are plain JSON values, and validateParams refused an Example that does not marshal
	}
	return raw
}

// probeParamValue is the placeholder of one declared JSONType.
func probeParamValue(jsonType string, fields []string) any {
	switch jsonType {
	case "number":
		return 1
	case "boolean":
		return false
	case "string":
		if len(fields) > 0 {
			return fields[0]
		}
		return ""
	case "array":
		return append([]string{}, fields...)
	default: // "object" — validateParams admits no other type
		return map[string]any{}
	}
}

// withProbeParams adds details.probe_params (the synthesized params
// object, decoded) to a probe factory failure's details when the probe
// handed the factory one.
func withProbeParams(details map[string]any, raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return details
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		details["probe_params"] = m
	}
	return details
}
