package fieldcast

import (
	"math"
	"math/big"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Bucket is one materialize output column's bucketing
// (types.MaterializeBucket): From is the field read; exactly one of
// Numeric (a numeric From — the shared half-open numrange pass) or Dates
// (a date / datetime From — the shared inclusive daterange pass) is set.
// Zone is the zone a datetime is read in (its local day); nil is UTC.
type Bucket struct {
	From    encoding.Field
	Numeric *numrange.Set
	Dates   *daterange.Set
	Zone    *temporal.Zone
}

// exactFloat is the largest magnitude below which every integer is an
// exact float64.
const exactFloat = 1 << 53

// Apply returns the label of the range holding the stored value v (the
// CohortRow type of b.From). A nil value stays nil. A value outside every
// range fails "no_range", a NaN "not_finite"; Value renders it exactly (a
// date as its ISO day, a datetime as its UTC instant). Numeric values are
// compared exactly — an integer past 2^53 or a decimal never rounds
// across a bound.
func (b *Bucket) Apply(v any) (any, *Failure) {
	if v == nil {
		return nil, nil
	}
	if b.Dates != nil {
		var day int64
		var rendered string
		switch x := v.(type) {
		case int32:
			day, rendered = int64(x), temporal.FormatDay(int64(x))
		case int64:
			zone := b.Zone
			if zone == nil {
				zone = temporal.UTC
			}
			day, rendered = temporal.LocalDay(x, zone), temporal.FormatUTC(x)
		default:
			return nil, unexpected(v)
		}
		if label, ok := b.Dates.Match(day); ok {
			return label, nil
		}
		return nil, &Failure{Reason: "no_range", Value: rendered}
	}
	n, ok := lift(v, b.From)
	if !ok {
		return nil, unexpected(v)
	}
	var (
		label   string
		matched bool
	)
	switch {
	case n.kind == 'f':
		if math.IsNaN(n.f) {
			return nil, &Failure{Reason: "not_finite", Value: n.String()}
		}
		label, matched = b.Numeric.Match(n.f)
	case n.kind == 'u' && n.u <= exactFloat:
		label, matched = b.Numeric.Match(float64(n.u))
	case n.kind == 'i' && n.i <= exactFloat && n.i >= -exactFloat:
		label, matched = b.Numeric.Match(float64(n.i))
	default:
		label, matched = b.Numeric.MatchRat(exactRat(n))
	}
	if !matched {
		return nil, &Failure{Reason: "no_range", Value: n.String()}
	}
	return label, nil
}

// exactRat is n as an exact rational (an integer or a decimal).
func exactRat(n number) *big.Rat {
	switch n.kind {
	case 'u':
		return new(big.Rat).SetUint64(n.u)
	case 'i':
		return new(big.Rat).SetInt64(n.i)
	}
	return new(big.Rat).SetFrac(n.m, pow10(n.scale))
}
