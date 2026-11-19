package encoding

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// DetailZoneField is the CodedError.Details key naming a cohort's
// declared default zone field ([Schema.ZoneField]) on every refusal
// [Schema.ValidateZoneField] raises.
const DetailZoneField = "zone_field"

// The ZONE section (tag 3, always REQUIRED) of the 0x02 schema
// extension block carries the cohort's default per-row zone field:
//
//	u16 name_length                 >= 1
//	name_length bytes of UTF-8      the field's name, exactly as in its descriptor
//
// It is written only when Schema.ZoneField is set, so a cohort that
// declares no default zone is byte-identical to one written before the
// section existed (a flat cohort stays 0x01). It is REQUIRED: a binary
// that predates it refuses the file rather than silently resolve every
// datetime operator in UTC instead of the cohort's zones.
const sectionTagZone uint16 = 3

// HasZoneField reports whether the schema declares a default per-row
// zone field (a ZONE section).
func (s *Schema) HasZoneField() bool { return s.ZoneField != "" }

// ValidateZoneField checks Schema.ZoneField against Schema.Fields: an
// undeclared default (empty) passes; a declared one must name a field,
// that field must be categorical_u8 / u16 / u32, and every entry of its
// dictionary must be a time zone Pulse accepts ("UTC" or an IANA
// Area/Location name in the embedded tz database — the rule of
// internal/temporal.ValidZoneName, the same one a request's
// time_zone_field is held to).
//
// A missing or non-categorical field is ENCODING_INVALID; a bad entry
// is the zone loader's own PULSE_TIMEZONE_UNKNOWN, its details extended
// with the field (DetailZoneField) and the entry's dictionary code
// (dictionary_index). Every refusal names the field under
// DetailZoneField. The writer runs it before a byte is written and the
// reader once every extension section is read, so neither can produce
// or accept a default that does not resolve.
func (s *Schema) ValidateZoneField() error { return s.validateZoneField(true) }

// validateZoneField is ValidateZoneField; without entries (a
// layout-only read, whose dictionaries were skipped) the dictionary
// entries are not checked — the structural rules still are.
func (s *Schema) validateZoneField(entries bool) error {
	if !s.HasZoneField() {
		return nil
	}
	f := s.Field(s.ZoneField)
	if f == nil {
		return zoneErr("names no field", map[string]any{DetailZoneField: s.ZoneField, "field_count": len(s.Fields)})
	}
	if !f.Type.IsCategorical() {
		return zoneErr("must be a categorical field (categorical_u8 / u16 / u32) whose values are IANA zone names",
			map[string]any{DetailZoneField: s.ZoneField, "type": f.Type.String()})
	}
	if !entries || f.Dictionary == nil {
		return nil
	}
	for i, name := range f.Dictionary.Values() {
		err := temporal.ValidZoneName(name)
		if err == nil {
			continue
		}
		ce, ok := err.(*errors.CodedError)
		if !ok {
			return err
		}
		details := make(map[string]any, len(ce.Details)+2)
		for k, v := range ce.Details {
			details[k] = v
		}
		details[DetailZoneField] = s.ZoneField
		details[temporal.DetailDictionaryIndex] = i
		return errors.NewCodedErrorWithDetails(ce.Code,
			fmt.Sprintf("cohort default zone field %q entry %d: %s", s.ZoneField, i, ce.Message), details)
	}
	return nil
}

func zoneErr(msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID, "cohort default zone field "+msg, details)
}

// writeZoneSection writes the ZONE section body. s must have passed
// ValidateZoneField, so the name is a field's and fits a u16 length.
func writeZoneSection(b *bytes.Buffer, s *Schema) {
	b.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(s.ZoneField))))
	b.WriteString(s.ZoneField)
}

// readZoneSection parses the ZONE section into s.ZoneField. An empty
// name is refused here; whether it names a categorical field of valid
// zones is ValidateZoneField, which the caller runs once every section
// is read.
func readZoneSection(x *extReader, s *Schema) error {
	n, err := x.u16("zone field name length")
	if err != nil {
		return err
	}
	if n == 0 {
		return zoneErr("names no field (empty name)", map[string]any{DetailZoneField: ""})
	}
	name, err := x.bytesN(uint64(n), "zone field name")
	if err != nil {
		return err
	}
	s.ZoneField = string(name)
	return nil
}
