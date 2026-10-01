package processing

import "reflect"

// funcPointerImpl returns the underlying function pointer of an
// AggregatorFactory. Used by extension tests to assert overlay
// identity; lives outside the _test.go file so reflect is excluded
// from non-test builds via a single touchpoint.
func funcPointerImpl(f AggregatorFactory) uintptr {
	return reflect.ValueOf(f).Pointer()
}
