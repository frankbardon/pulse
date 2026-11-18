package io

import (
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// The cohort DEFAULT zone field an import or convert declares
// (ImportJob.ZoneField / ConvertJob.ZoneField): the writer half of the
// 0x02 ZONE section. The rule is encoding.Schema.ValidateZoneField's,
// never restated here; this file only decides WHEN it runs and how an
// option refusal is coded.

// optionZoneField is the DetailOption value naming the zone-field
// option on every refusal it draws.
const optionZoneField = encx.ZoneFieldOption

// withZoneField returns schema declaring name as its default zone field:
// schema itself when name is empty (nothing is declared, so the write is
// byte-identical), else a shallow copy, so a caller's schema object —
// ConvertJob hands its own to the KeepPulseAt intermediate, and its
// report carries it — is never mutated.
func withZoneField(schema *encoding.Schema, name string) *encoding.Schema {
	if name == "" || schema == nil {
		return schema
	}
	cp := *schema
	cp.ZoneField = name
	return &cp
}

// checkZoneField judges a declared zone field against the resolved
// schema before the row pass: the field must exist and be
// categorical_*, and any dictionary entry the schema already holds (an
// authoritative or explicit dictionary, an inference seed) must be a
// zone. The entries the row pass adds are judged again at write time.
func checkZoneField(schema *encoding.Schema, name string) error {
	if name == "" {
		return nil
	}
	return zoneFieldRefusal(withZoneField(schema, name).ValidateZoneField())
}

// zoneFieldRefusal re-codes the encoding layer's structural refusal of a
// declared zone field as the OPTION refusal it is at an import surface;
// the one rule is internal/encoding.ZoneFieldOptionRefusal (the set
// writer, Pulse.SetCohortZoneField, shares it).
func zoneFieldRefusal(err error) error { return encx.ZoneFieldOptionRefusal(err) }

// zoneFieldNoCohort refuses a ConvertJob.ZoneField without KeepPulseAt:
// a convert's target is a tabular file with no schema to carry the
// default, so the declaration would otherwise be silently dropped.
func zoneFieldNoCohort(name string) error {
	return zoneRefusal(optionZoneField,
		"a default zone field is declared on a .pulse cohort; a convert writes one only with KeepPulseAt (CLI --keep-pulse) — set it, or import the source instead",
		map[string]any{encoding.DetailZoneField: name, "reason": "no_cohort"})
}

// zoneFieldProbe interns a convert's zone-field cells into a private
// dictionary exactly as ConvertJob.Run's row loop interns them into the
// schema's, so ConvertJob.Predict reaches Run's dictionary verdict (same
// entries, same order, same dictionary_index) without touching the
// schema it reports. Nil when no zone field is declared.
type zoneFieldProbe struct {
	idx  int
	dict *encoding.Dictionary
}

func newZoneFieldProbe(schema *encoding.Schema, name string) *zoneFieldProbe {
	if name == "" {
		return nil
	}
	for i := range schema.Fields {
		if schema.Fields[i].Name != name {
			continue
		}
		d := encoding.NewDictionary()
		if src := schema.Fields[i].Dictionary; src != nil {
			for _, v := range src.Values() {
				_, _ = d.Add(v)
			}
		}
		return &zoneFieldProbe{idx: i, dict: d}
	}
	return nil
}

// observe interns field i's raw cell when i is the zone field and the
// cell is present (Run skips a null token).
func (p *zoneFieldProbe) observe(i int, raw string) {
	if p == nil || i != p.idx || isNullToken(raw) {
		return
	}
	_, _ = p.dict.Add(raw)
}

// verdict runs the write-time rule over the probed dictionary.
func (p *zoneFieldProbe) verdict(schema *encoding.Schema, name string) error {
	if p == nil {
		return nil
	}
	probe := withZoneField(schema, name)
	probe.Fields = append([]encoding.Field(nil), schema.Fields...)
	probe.Fields[p.idx].Dictionary = p.dict
	return zoneFieldRefusal(probe.ValidateZoneField())
}
