package pulse

import (
	"encoding/json"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// extensions_probe_dictionary.go is the pulse.New probe of an attribute
// declaring InheritsDictionary (U67 E3-S2): the attribute is computed
// over a synthetic three-entry categorical source carrying each code
// once, and a value that is no code of it is refused with the run's own
// PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH (reason
// dictionary_code_out_of_range, details.stage "probe").
//
// The probe judges the returned codes only: a factory that refuses the
// synthetic spec, a Compute error, a panic or a short result skips it
// (the factory probe proper — safeBuildAttribute — already judged
// construction), so a registration that works on real data is never
// refused for the synthetic rows' sake.

// probeDictionaryField names the synthetic source field.
const probeDictionaryField = "probe_source"

// probeDictionaryLabels are the synthetic source's dictionary entries.
var probeDictionaryLabels = []string{"a", "b", "c"}

// probeAttributeCodes runs the probe for reg; nil when reg declares no
// InheritsDictionary or every value is a code.
func probeAttributeCodes(reg AttributeRegistration) error {
	d := reg.InheritsDictionary
	if d == nil {
		return nil
	}
	dict := encoding.NewDictionary()
	for _, l := range probeDictionaryLabels {
		if _, err := dict.Add(l); err != nil {
			return nil
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{{
		Name: probeDictionaryField, Type: encoding.FieldTypeCategoricalU8, Dictionary: dict,
		Description: "Synthetic categorical source of the dictionary probe",
	}}}
	spec := &types.Attribute{Type: reg.Name, Params: probeParams(reg.Params, nil)}
	if d.Param == "" {
		spec.Field = probeDictionaryField
	} else {
		spec.Params = withProbeSource(spec.Params, d.Param)
	}
	records := make([]*processing.Record, len(probeDictionaryLabels))
	for i := range records {
		records[i] = processing.NewRecord(schema, map[string]float64{probeDictionaryField: float64(i)})
	}
	out, nullable, ok := computeProbeCodes(reg, spec, schema, records)
	if !ok {
		return nil
	}
	n := len(probeDictionaryLabels)
	for _, v := range out {
		if (math.IsNaN(v) && !nullable) || prevstate.CodeFault(v, n) {
			return dictionaryCodeFault(reg.Name, v, n, map[string]any{"stage": "probe"})
		}
	}
	return nil
}

// withProbeSource is params raw with key set to the synthetic source
// field's name.
func withProbeSource(raw json.RawMessage, key string) json.RawMessage {
	m := map[string]any{}
	if len(raw) > 0 && json.Unmarshal(raw, &m) != nil {
		m = map[string]any{}
	}
	m[key] = probeDictionaryField
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// computeProbeCodes builds reg over schema for spec and computes it over
// records, reporting whether the value is a nullable attribute and
// whether the probe ran (ok=false on a factory or Compute error, a nil
// instance, a panic or a result not one value per row).
func computeProbeCodes(reg AttributeRegistration, spec *types.Attribute, schema *encoding.Schema, records []*processing.Record) (out []float64, nullable, ok bool) {
	defer func() {
		if recover() != nil {
			out, nullable, ok = nil, false, false
		}
	}()
	instance, err := reg.Factory(spec, schema)
	if err != nil || instance == nil {
		return nil, false, false
	}
	core := attrCore{inner: instance}
	out, err = core.Compute(records, spec.Field)
	if err != nil || len(out) != len(records) {
		return nil, false, false
	}
	return out, core.EmitsNull(), true
}
