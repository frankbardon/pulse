package temporal

import (
	perr "github.com/frankbardon/pulse/errors"
)

// DetailDictionaryIndex is the CodedError.Details key carrying the
// dictionary code of a zone-field entry that is not a valid zone name
// (LoadZones, and the request-time zone-field check that mirrors it).
const DetailDictionaryIndex = "dictionary_index"

// ValidZoneName reports whether LoadZone would accept name, without
// building its transition table: "UTC", or a well-formed IANA
// Area/Location name with an entry in the embedded tz database. A
// refusal is LoadZone's own PULSE_TIMEZONE_UNKNOWN error, message and
// details included. It is the cheap check a validator runs over every
// dictionary entry of a per-row zone field; the operator that then
// executes builds the zones with LoadZones.
func ValidZoneName(name string) error {
	if name == "UTC" {
		return nil
	}
	if !wellFormedZoneName(name) {
		return unknownZone(name, "it is not \"UTC\" or an IANA Area/Location name")
	}
	idx, err := embeddedZones()
	if err != nil || idx[name] == nil {
		return unknownZone(name, "it is not in the tz database")
	}
	return nil
}

// ZoneTable is a per-row zone lookup: the *Zone of every entry of a
// categorical zone field's dictionary, indexed by dictionary code. An
// operator builds one at construction (LoadZones), forks it per worker
// (Fork), and reads a row's zone as At(code) — no allocation per row.
// A shard dictionary is a prefix of the canonical one, so one table
// built from the canonical dictionary serves every shard.
//
// A nil *ZoneTable is an empty table.
type ZoneTable struct {
	zones  []*Zone
	allUTC bool
}

// LoadZones builds the table for names (names[i] is dictionary code
// i). load resolves one name (nil: LoadZone; a long-lived owner passes
// a Cache's Load); it is called once per distinct name, and a repeated
// name shares one *Zone. An unloadable entry is refused with the
// loader's PULSE_TIMEZONE_UNKNOWN error, its details extended with the
// entry's code under DetailDictionaryIndex.
func LoadZones(names []string, load func(string) (*Zone, error)) (*ZoneTable, error) {
	if load == nil {
		load = LoadZone
	}
	t := &ZoneTable{zones: make([]*Zone, len(names)), allUTC: true}
	byName := make(map[string]*Zone, len(names))
	for i, n := range names {
		z, ok := byName[n]
		if !ok {
			var err error
			if z, err = load(n); err != nil {
				return nil, withDictionaryIndex(err, i)
			}
			byName[n] = z
		}
		t.zones[i] = z
		if !z.IsUTC() {
			t.allUTC = false
		}
	}
	return t, nil
}

// withDictionaryIndex copies a coded error with the entry's code added
// to its details; any other error is returned as is.
func withDictionaryIndex(err error, code int) error {
	ce, ok := err.(*perr.CodedError)
	if !ok {
		return err
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details[DetailDictionaryIndex] = code
	return perr.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

// Len returns the number of dictionary codes the table covers.
func (t *ZoneTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.zones)
}

// At returns the zone of dictionary code code, or nil when the code is
// outside the table (a caller treats that like a null zone cell).
func (t *ZoneTable) At(code uint32) *Zone {
	if t == nil || uint64(code) >= uint64(len(t.zones)) {
		return nil
	}
	return t.zones[code]
}

// AllUTC reports whether every entry is UTC-equivalent (Zone.IsUTC), so
// a caller may take its zone-free fast path.
func (t *ZoneTable) AllUTC() bool { return t == nil || t.allUTC }

// Fork returns a table with the same answers whose non-UTC zones are
// forks (Zone.Fork): each worker holds its own lookup caches. Sharing
// between repeated names is kept; one allocation per distinct zone.
func (t *ZoneTable) Fork() *ZoneTable {
	if t == nil {
		return nil
	}
	f := &ZoneTable{zones: make([]*Zone, len(t.zones)), allUTC: t.allUTC}
	forks := make(map[*Zone]*Zone, len(t.zones))
	for i, z := range t.zones {
		fz, ok := forks[z]
		if !ok {
			fz = z.Fork()
			forks[z] = fz
		}
		f.zones[i] = fz
	}
	return f
}
