package temporal

import "sync"

// Cache memoises LoadZone by name. LoadZone builds a transition table on
// every call, so a long-lived owner (one per pulse.Pulse instance) keeps
// one Cache and resolves every request-time zone through it. Only
// successful loads are cached: a refused name is re-validated (and
// re-refused) on every call, so a cache can never mask an error.
//
// The zero value is ready to use and safe for concurrent use.
type Cache struct {
	zones sync.Map // name -> *Zone
}

// Load returns the Zone for name, loading and caching it on first use.
// It returns exactly what LoadZone returns for an unknown name (a
// PULSE_TIMEZONE_UNKNOWN *errors.CodedError); "UTC" is the UTC sentinel.
// Concurrent first loads of one name may each build a table, but every
// caller observes the single stored pointer.
func (c *Cache) Load(name string) (*Zone, error) {
	if v, ok := c.zones.Load(name); ok {
		return v.(*Zone), nil
	}
	z, err := LoadZone(name)
	if err != nil {
		return nil, err
	}
	actual, _ := c.zones.LoadOrStore(name, z)
	return actual.(*Zone), nil
}
