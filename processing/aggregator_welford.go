package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_WELFORD emits the streaming Welford-Pébaÿ triple — running mean,
// running sample variance (n-1 denominator), and observed count — over
// a numeric field. The aggregator satisfies the scalar Aggregator
// contract (Value() returns the running mean for legacy / debug
// consumers) and the RichAggregator contract (Rich() returns the typed
// WelfordTriple).
//
// Field-type policy mirrors TEST_WELCH (see processing/test_t.go's
// IsCategorical check): only the strict scalar numeric family is
// admitted — u8 / u16 / u32 / u64 / f32 / f64. decimal128, date, and
// the bit-packed integer encodings are rejected at factory time with
// PROCESSING_CONFIG so the per-record hot path stays branch-free.
//
// Variance denominator is the sample form M2 / (n - 1) — the same
// denominator TEST_WELCH consumes — so AGG_WELFORD output is byte-equal
// to a TEST_WELCH per-group variance for the same input stream. Empty
// or single-row inputs yield variance = 0 (mirrors welfordBucket's
// zero-on-N<2 behaviour); the legacy scalar path returns NaN when no
// rows arrived (no defensible mean exists) and Rich() returns
// (nil, nil) under the same "no rows" condition, matching the set-
// aggregator precedent.

// WelfordTriple is the Rich payload returned by AGG_WELFORD. Mean is
// the running mean, Variance is the unbiased sample variance
// (M2 / (n - 1); zero when N < 2), and N is the number of non-null
// rows that contributed.
//
// Returned by value (not via a pointer) so downstream consumers — the
// crosstab MatrixCell builder, overlay handlers — can pass it through
// the `any`-typed Value slot without pointer-aliasing surprises across
// copies.
type WelfordTriple struct {
	Mean     float64 `json:"mean"`
	Variance float64 `json:"variance"`
	N        uint64  `json:"n"`
}

type welfordAggregator struct {
	field  string
	bucket welfordBucket
}

func newWelfordAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	if agg.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"AGG_WELFORD requires field")
	}
	// Schema may be nil at registry-streamability probe time; runtime
	// callers always pass a schema and the field-type check below catches
	// type errors before Aggregate ever runs.
	if schema != nil {
		f := schema.Field(agg.Field)
		if f == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"AGG_WELFORD: unknown field "+agg.Field)
		}
		switch f.Type {
		case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
			encoding.FieldTypeF32, encoding.FieldTypeF64:
			// accepted
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("AGG_WELFORD field %q has non-numeric type %s", agg.Field, f.Type.String()))
		}
	}
	return &welfordAggregator{field: agg.Field}, nil
}

// Aggregate folds every non-null value of field through the Welford
// recurrence and returns the running mean. Empty input returns NaN so
// callers can distinguish "no rows" from "rows that happened to mean
// zero"; Rich() applies the same discrimination by returning (nil, nil)
// in the empty-input case so the orchestrator falls through to the
// scalar path.
func (a *welfordAggregator) Aggregate(records []*Record, field string) (float64, error) {
	a.bucket = welfordBucket{}
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		a.bucket.add(v)
	}
	if a.bucket.n == 0 {
		return math.NaN(), nil
	}
	return a.bucket.mean, nil
}

// UpdateRow / Finalize implement OnlineAggregator so the streaming
// orchestrator can drive AGG_WELFORD over the iterator without
// materialising the record set. Finalize emits the running mean (the
// scalar fallback) and leaves the bucket intact so Rich() can read the
// frozen triple immediately afterward — the streaming dispatch contract
// invokes Finalize before Rich.
func (a *welfordAggregator) UpdateRow(r *Record, field string) error {
	v, ok := r.NumericValue(field)
	if !ok {
		return nil
	}
	a.bucket.add(v)
	return nil
}

func (a *welfordAggregator) Finalize() (float64, error) {
	if a.bucket.n == 0 {
		return math.NaN(), nil
	}
	return a.bucket.mean, nil
}

// MergeOnline applies the Chan-Welford parallel reduction so the
// per-shard parallel reducer in service/shard_reduce.go and the
// per-segment parallel decode in service/parallel_decode.go can fold
// independent partials byte-equal to the single-pass result on
// well-conditioned inputs.
func (a *welfordAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*welfordAggregator)
	if !ok {
		return mergeTypeMismatch("AGG_WELFORD")
	}
	mergeWelford(&a.bucket.n, &a.bucket.mean, &a.bucket.m2, b.bucket.n, b.bucket.mean, b.bucket.m2)
	return nil
}

// Rich returns the frozen Welford triple after Aggregate / Finalize.
// Returns (nil, nil) when no rows contributed so the orchestrator's
// rich-or-scalar dispatch falls through to the Value() path.
func (a *welfordAggregator) Rich() (any, error) {
	if a.bucket.n == 0 {
		return nil, nil
	}
	return WelfordTriple{
		Mean:     a.bucket.mean,
		Variance: a.bucket.sampleVariance(),
		N:        uint64(a.bucket.n),
	}, nil
}

// Components returns the running Welford moments after Aggregate /
// Finalize. Reads the bucket directly — neither path resets the state
// before Components is called (Finalize is documented to leave the
// bucket intact so Rich() reads the frozen triple). Numerically
// identical to the WelfordTriple emitted via Rich(): {mean, m2,
// variance, stddev} where variance = m2/(n-1) for n>=2 and 0 for n<2,
// stddev = sqrt(variance). Empty-input case returns (nil, nil) so the
// orchestrator's universal floor (n=0, n_null=...) is the entire
// payload, matching the Rich() contract.
//
// Numerical-stability contract: emitted values are bit-identical to
// the WelfordTriple consumed by the stat-test parity overlays
// (OVERLAY_T_CELL / OVERLAY_Z_CELL); both surfaces share the
// welfordBucket recurrence so any divergence at the bit level is a
// regression.
func (a *welfordAggregator) Components() (map[string]any, error) {
	if a.bucket.n == 0 {
		return nil, nil
	}
	variance := a.bucket.sampleVariance()
	return map[string]any{
		"mean":     a.bucket.mean,
		"m2":       a.bucket.m2,
		"variance": variance,
		"stddev":   math.Sqrt(variance),
	}, nil
}

// Compile-time interface lock — keeps the MetaAggregator wiring
// grep-discoverable and surfaces interface drift at build time.
var _ MetaAggregator = (*welfordAggregator)(nil)
