package processing

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_FREQUENCY counts the non-null rows whose field equals ONE chosen
// value (params.value) and returns that count as a scalar float64 — per
// group under a grouper, 0 when no row matches. It is the one-call form
// of FILTER_INCLUDE{value} + AGG_COUNT, and matches values by the same
// rule (matchValueKey): a categorical value is a dictionary LABEL, every
// other type parses as a number (date in epoch days, datetime in epoch
// seconds, packed_bool as 1 / 0). Where FILTER_INCLUDE refuses a label
// the dictionary does not hold, AGG_FREQUENCY counts zero — no row can
// hold it — so a value absent from one shard or slice is not an error.
//
// The fold is two integer counters, so the online partials merge
// exactly (shards, decode segments, ProcessChain) and the buffered,
// streaming and merged paths agree bit for bit. Components add
// match_count (= the scalar) and share (match_count / n, omitted when
// n is 0 — a share of nothing is undefined, not zero).

// frequencyParams holds AGG_FREQUENCY's one required param. Value is
// raw so a JSON number (`"value": 3`) is accepted beside a string.
type frequencyParams struct {
	Value json.RawMessage `json:"value"`
}

type frequencyAggregator struct {
	// key is the float64 Record.NumericValue yields for a matching row;
	// matchable is false when the value names no dictionary entry, so
	// no row can match.
	key       float64
	matchable bool

	n       int
	matches int

	frozenFinalized bool
	frozenN         int
	frozenMatches   int
}

// remedyFrequencyModeCount completes the missing-value refusal: the
// modal count AGG_FREQUENCY once meant is AGG_MODE_COUNT.
func remedyFrequencyModeCount(offered func(string) bool) string {
	if !isOffered(offered, string(types.AGG_MODE_COUNT)) {
		return ""
	}
	return " The count of the field's most common value is " + string(types.AGG_MODE_COUNT) + "."
}

// frequencyValueMissingMessage is the refusal for an AGG_FREQUENCY slot
// without params.value. Predict mirrors it verbatim.
func frequencyValueMissingMessage() string {
	return string(types.AGG_FREQUENCY) + " requires params.value: it returns the count of rows equal to that value." +
		remedyFrequencyModeCount(nil)
}

// frequencyValueString decodes params.value: a JSON string is used as
// written, a JSON number by its literal text. Anything else (absent,
// null, empty string, bool, object, array) reads as missing.
func frequencyValueString(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, s != ""
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), true
	}
	return "", false
}

func newFrequencyAggregator(agg *types.Aggregation, schema *encoding.Schema) (Aggregator, error) {
	if err := rejectSetFieldForNumericAggregator(agg, schema); err != nil {
		return nil, err
	}
	var params frequencyParams
	if len(agg.Params) > 0 {
		if err := json.Unmarshal(agg.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"invalid AGG_FREQUENCY params: "+err.Error())
		}
	}
	value, ok := frequencyValueString(params.Value)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			frequencyValueMissingMessage(),
			map[string]any{"aggregator": string(types.AGG_FREQUENCY), "param": "value"})
	}
	var field *encoding.Field
	if schema != nil {
		field = schema.Field(agg.Field)
	}
	key, found, err := matchValueKey(field, value)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing AGG_FREQUENCY value "+strconv.Quote(value))
	}
	if w := slotWeight(agg); w != nil {
		return newWeightedFrequencyAggregator(key, found, w), nil
	}
	return &frequencyAggregator{key: key, matchable: found}, nil
}

func (a *frequencyAggregator) Aggregate(records []*Record, field string) (float64, error) {
	return a.aggregateValues(collectValues(records, field))
}

func (a *frequencyAggregator) aggregateValues(vals []float64) (float64, error) {
	a.n, a.matches = 0, 0
	for _, v := range vals {
		a.observe(v)
	}
	return a.Finalize()
}

func (a *frequencyAggregator) observe(v float64) {
	a.n++
	if a.matchable && v == a.key {
		a.matches++
	}
}

func (a *frequencyAggregator) UpdateRow(r *Record, field string) error {
	if v, ok := r.NumericValue(field); ok {
		a.observe(v)
	}
	return nil
}

func (a *frequencyAggregator) Finalize() (float64, error) {
	a.frozenFinalized = true
	a.frozenN, a.frozenMatches = a.n, a.matches
	a.n, a.matches = 0, 0
	return float64(a.frozenMatches), nil
}

func (a *frequencyAggregator) MergeOnline(other OnlineAggregator) error {
	b, ok := other.(*frequencyAggregator)
	if !ok {
		return mergeTypeMismatch(string(types.AGG_FREQUENCY))
	}
	a.n += b.n
	a.matches += b.matches
	return nil
}

// Components returns {match_count, share}: the scalar as an int and its
// share of the non-null rows. A run over no non-null row emits
// match_count 0 and omits share (0/0 is undefined); a never-run
// aggregator emits nothing, leaving the universal floor.
func (a *frequencyAggregator) Components() (map[string]any, error) {
	if !a.frozenFinalized {
		return nil, nil
	}
	out := map[string]any{"match_count": a.frozenMatches}
	if a.frozenN > 0 {
		out["share"] = float64(a.frozenMatches) / float64(a.frozenN)
	}
	return out, nil
}

var (
	_ MetaAggregator      = (*frequencyAggregator)(nil)
	_ MergeableAggregator = (*frequencyAggregator)(nil)
	_ valueAggregator     = (*frequencyAggregator)(nil)
)
