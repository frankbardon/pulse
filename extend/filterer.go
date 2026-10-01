package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// FilterFunc reports whether one row passes the filter. It runs on the
// streaming and buffered paths alike, once per row, and must give the
// same answer on both. The Record is valid only for the call.
type FilterFunc func(rec Record) (bool, error)

// FiltererBuilder compiles one filter slot of one request into a
// FilterFunc. Build is called once per request with the slot's spec and
// the cohort schema.
type FiltererBuilder interface {
	Build(spec *types.Filterer, schema *encoding.Schema) (FilterFunc, error)
}

// FiltererFactory returns a fresh FiltererBuilder. It takes no
// arguments; during probe-validation at pulse.New it is called once and
// must return a non-nil builder.
type FiltererFactory func() FiltererBuilder
