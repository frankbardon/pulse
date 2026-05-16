package descriptor

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// streamableAlternative maps a non-streamable operator to its closest
// streamable substitute. Entries with an empty replacement signal that no
// streamable peer exists; predict still emits a suggestion in that case so
// the caller is not left guessing. Keys are typed-string-ish so we can
// share the same table across aggregator/attribute/group/window
// categories; values are operator names from the corresponding catalog.
//
// Keep this table in sync with types/streamability.go: every operator
// whose Streamable() returns false should have a row, even if the row is
// "no streamable peer".
type streamableAlt struct {
	// Replacement is the recommended operator name. Empty string when
	// none exists; the suggestion still fires with Proposed: nil.
	Replacement string
	// Category labels the destination operator's catalog. Used in the
	// suggestion's prose to disambiguate (e.g. AGG_ZSCORE has no
	// streaming aggregator peer; ATTR_ZSCORE is its two-pass attribute
	// substitute).
	Category string
}

var streamableAlternatives = map[string]streamableAlt{
	string(types.AGG_MEDIAN):     {Replacement: string(types.AGG_AVERAGE), Category: "aggregator"},
	string(types.AGG_PERCENTILE): {Replacement: string(types.AGG_AVERAGE), Category: "aggregator"},
	// AGG_ZSCORE → ATTR_ZSCORE: the streaming two-pass attribute
	// produces per-row standardized scores without materialising the
	// full row set.
	string(types.AGG_ZSCORE):       {Replacement: string(types.ATTR_ZSCORE), Category: "attribute"},
	string(types.ATTR_PERCENTILE):  {Replacement: "", Category: "attribute"},
	string(types.GROUP_QUANTILE): {Replacement: string(types.GROUP_RANGE), Category: "grouper"},
	string(types.GROUP_DATE):     {Replacement: "", Category: "grouper"},
}

// commonPercentiles lists the canonical percentile values predict
// suggests when AGG_PERCENTILE is missing its P parameter.
var commonPercentiles = []any{25.0, 50.0, 75.0, 90.0, 95.0, 99.0}

// numericFallback returns the schema fields suitable for replacing a
// missing-numeric-field reference (date or numeric scalar). Used by
// missing-OrderBy suggestions on WIN_LAG / WIN_LEAD and friends.
func numericFallbackFields(schema *encoding.Schema) []any {
	if schema == nil {
		return nil
	}
	out := make([]any, 0, len(schema.Fields))
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if isNumericType(f.Type) || f.Type == encoding.FieldTypeDate {
			out = append(out, f.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].(string) < out[j].(string) })
	return out
}

// computeSuggestions is the top-level entry point. It walks each
// suggestion source in deterministic order so output is stable, and
// returns a non-nil empty slice when nothing fires (so JSON serialises
// as []). Source order: typos → operator/type → date-misuse →
// missing-params → streamability.
func computeSuggestions(req *types.Request, schema *encoding.Schema, streamable bool) []Suggestion {
	out := []Suggestion{}
	if req == nil || schema == nil {
		return out
	}

	fieldNames := schemaFieldNames(schema)

	out = appendTypoSuggestions(out, req, schema, fieldNames)
	out = appendOperatorTypeSuggestions(out, req, schema)
	out = appendDateMisuseSuggestions(out, req, schema)
	out = appendMissingParamSuggestions(out, req, schema)
	if !streamable {
		out = appendStreamabilitySuggestions(out, req)
	}

	return out
}

// schemaFieldNames returns a sorted snapshot of the schema's field names
// so Levenshtein candidate lists are stable across runs.
func schemaFieldNames(schema *encoding.Schema) []string {
	names := make([]string, 0, len(schema.Fields))
	for i := range schema.Fields {
		names = append(names, schema.Fields[i].Name)
	}
	sort.Strings(names)
	return names
}

// appendTypoSuggestions walks every field reference in the request and
// proposes Levenshtein-near matches (distance ≤ 2) when the reference
// does not resolve in the schema. Confidence: 0.9 at distance 1, 0.7 at
// distance 2.
func appendTypoSuggestions(out []Suggestion, req *types.Request, schema *encoding.Schema, fieldNames []string) []Suggestion {
	emit := func(path []string, supplied string) {
		if supplied == "" {
			return
		}
		if schema.Field(supplied) != nil {
			return // resolves; nothing to suggest
		}
		candidates, distance := nearestFields(supplied, fieldNames, 2)
		if len(candidates) == 0 {
			return
		}
		confidence := 0.7
		if distance == 1 {
			confidence = 0.9
		}
		proposed := make([]any, len(candidates))
		for i, c := range candidates {
			proposed[i] = c
		}
		out = append(out, Suggestion{
			Path:       path,
			Reason:     "field " + supplied + " is not in the schema; closest names by edit distance",
			Current:    supplied,
			Proposed:   proposed,
			Confidence: confidence,
		})
	}

	for i, agg := range req.Aggregations {
		emit([]string{"Aggregations", strconv.Itoa(i), "Field"}, agg.Field)
	}
	for i, fil := range req.Filterers {
		if fil.Type == types.FILTER_EXPRESSION {
			continue
		}
		emit([]string{"Filterers", strconv.Itoa(i), "Field"}, fil.Field)
	}
	for i, grp := range req.Groups {
		emit([]string{"Groups", strconv.Itoa(i), "Field"}, grp.Field)
	}
	for i, attr := range req.Attributes {
		emit([]string{"Attributes", strconv.Itoa(i), "Field"}, attr.Field)
	}
	for i, w := range req.Windows {
		emit([]string{"Windows", strconv.Itoa(i), "Field"}, w.Field)
		for j, ob := range w.OrderBy {
			emit([]string{"Windows", strconv.Itoa(i), "OrderBy", strconv.Itoa(j), "Field"}, ob.Field)
		}
		for j, p := range w.PartitionBy {
			emit([]string{"Windows", strconv.Itoa(i), "PartitionBy", strconv.Itoa(j)}, p)
		}
	}
	for i, feat := range req.Features {
		emit([]string{"Features", strconv.Itoa(i), "Field"}, feat.Field)
	}

	return out
}

// appendOperatorTypeSuggestions proposes alternative operators when the
// supplied operator is incompatible with the field's schema type:
//
//   - Numeric aggregation on a categorical field → AGG_MODE, AGG_FREQUENCY,
//     AGG_DISTINCT_COUNT (alphabetical order; confidence 0.6 multi-candidate).
//   - Aggregation on a decimal field that is not on the decimal-supported
//     list → the supported aggregations for that field type
//     (confidence 0.6).
// Reuses the fixup hint from errors.MetadataFor when one is registered
// for the underlying code so prose stays in lockstep with the fixup
// table.
func appendOperatorTypeSuggestions(out []Suggestion, req *types.Request, schema *encoding.Schema) []Suggestion {
	for i, agg := range req.Aggregations {
		f := schema.Field(agg.Field)
		if f == nil {
			continue
		}
		path := []string{"Aggregations", strconv.Itoa(i), "Type"}

		switch {
		case f.Type.IsCategorical() && numericAggregations[agg.Type]:
			proposed := []any{
				string(types.AGG_DISTINCT_COUNT),
				string(types.AGG_FREQUENCY),
				string(types.AGG_MODE),
			}
			out = append(out, Suggestion{
				Path:       path,
				Reason:     reasonFromCode(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL, "numeric aggregation does not apply to a categorical field; use a categorical-friendly aggregator"),
				Current:    string(agg.Type),
				Proposed:   proposed,
				Confidence: 0.6,
			})

		case f.Type.IsDecimal() && !decimalSupportedAggregations[agg.Type]:
			proposed := []any{
				string(types.AGG_AVERAGE),
				string(types.AGG_COUNT),
				string(types.AGG_DISTINCT_COUNT),
				string(types.AGG_MAX),
				string(types.AGG_MIN),
				string(types.AGG_STDDEV),
				string(types.AGG_SUM),
				string(types.AGG_VARIANCE),
			}
			out = append(out, Suggestion{
				Path:       path,
				Reason:     reasonFromCode(errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL, "aggregation has no decimal128 implementation; pick from the decimal-supported aggregators"),
				Current:    string(agg.Type),
				Proposed:   proposed,
				Confidence: 0.6,
			})

		}
	}

	return out
}

// appendDateMisuseSuggestions proposes GROUP_DATE when GROUP_CATEGORY is
// applied to a date-typed field. The category grouper still works on
// dates (each distinct epoch value becomes a key), but it is almost
// never what the caller wants.
func appendDateMisuseSuggestions(out []Suggestion, req *types.Request, schema *encoding.Schema) []Suggestion {
	for i, grp := range req.Groups {
		if grp.Type != types.GROUP_CATEGORY {
			continue
		}
		f := schema.Field(grp.Field)
		if f == nil || f.Type != encoding.FieldTypeDate {
			continue
		}
		out = append(out, Suggestion{
			Path:       []string{"Groups", strconv.Itoa(i), "Type"},
			Reason:     "GROUP_CATEGORY on a date field bucketizes by exact epoch value; use GROUP_DATE with a calendar component (year, quarter, month, ...) for a useful grouping",
			Current:    string(grp.Type),
			Proposed:   []any{string(types.GROUP_DATE)},
			Confidence: 0.9,
		})
	}
	return out
}

// appendMissingParamSuggestions proposes values for required parameters
// that the caller omitted:
//
//   - WIN_LAG / WIN_LEAD / WIN_RUNNING_* / WIN_MOVING_AVG / WIN_EWMA /
//     WIN_PCT_CHANGE / WIN_RANK / WIN_DENSE_RANK / WIN_ROW_NUMBER missing
//     OrderBy → propose every date or numeric field (confidence 0.5).
//   - AGG_PERCENTILE missing percentile → propose the common quantiles
//     (confidence 0.5).
func appendMissingParamSuggestions(out []Suggestion, req *types.Request, schema *encoding.Schema) []Suggestion {
	// Window OrderBy.
	for i, w := range req.Windows {
		if len(w.OrderBy) != 0 {
			continue
		}
		proposed := numericFallbackFields(schema)
		if len(proposed) == 0 {
			continue
		}
		out = append(out, Suggestion{
			Path:       []string{"Windows", strconv.Itoa(i), "OrderBy"},
			Reason:     "window " + string(w.Type) + " requires at least one OrderBy key; pick a date or numeric field",
			Current:    nil,
			Proposed:   proposed,
			Confidence: 0.5,
		})
	}

	// AGG_PERCENTILE percentile.
	for i, agg := range req.Aggregations {
		if agg.Type != types.AGG_PERCENTILE {
			continue
		}
		if hasPercentileParam(agg.Params) {
			continue
		}
		out = append(out, Suggestion{
			Path:       []string{"Aggregations", strconv.Itoa(i), "Params", "percentile"},
			Reason:     "AGG_PERCENTILE requires a percentile parameter in [0, 100]; common choices follow",
			Current:    nil,
			Proposed:   append([]any(nil), commonPercentiles...),
			Confidence: 0.5,
		})
	}

	return out
}

// hasPercentileParam reports whether the raw-JSON Params block carries
// a percentile (or "p") key. Returns true when the JSON is absent or
// malformed — predict's other validators will surface that as an error;
// suggestions should not double-up.
func hasPercentileParam(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Malformed JSON — let the regular validator handle it; do not
		// emit a missing-param suggestion that contradicts the real
		// failure.
		return true
	}
	if _, ok := probe["percentile"]; ok {
		return true
	}
	if _, ok := probe["p"]; ok {
		return true
	}
	return false
}

// appendStreamabilitySuggestions emits one suggestion per operator that
// forced Streamable=false, naming the closest streamable substitute.
// Operators with no streamable peer still get a row (Proposed: nil) so
// the caller is not left in silence. Confidence: 0.8 when a replacement
// is available, 0.6 when none exists (the suggestion is advisory only).
func appendStreamabilitySuggestions(out []Suggestion, req *types.Request) []Suggestion {
	emit := func(path []string, current string) {
		alt, ok := streamableAlternatives[current]
		if !ok {
			// Operator not in the table (e.g. WIN_* — every window
			// operator is buffered).
			out = append(out, Suggestion{
				Path:       path,
				Reason:     "operator " + current + " forces the buffered path; no streamable peer is available",
				Current:    current,
				Proposed:   nil,
				Confidence: 0.6,
			})
			return
		}
		if alt.Replacement == "" {
			out = append(out, Suggestion{
				Path:       path,
				Reason:     "operator " + current + " forces the buffered path; no streamable peer is available",
				Current:    current,
				Proposed:   nil,
				Confidence: 0.6,
			})
			return
		}
		out = append(out, Suggestion{
			Path:       path,
			Reason:     "operator " + current + " forces the buffered path; closest streamable substitute is the " + alt.Category + " " + alt.Replacement,
			Current:    current,
			Proposed:   []any{alt.Replacement},
			Confidence: 0.8,
		})
	}

	for i, agg := range req.Aggregations {
		if agg.Type.Streamable() {
			continue
		}
		emit([]string{"Aggregations", strconv.Itoa(i), "Type"}, string(agg.Type))
	}
	for i, attr := range req.Attributes {
		if attr.Type.Streamable() {
			continue
		}
		emit([]string{"Attributes", strconv.Itoa(i), "Type"}, string(attr.Type))
	}
	for i, grp := range req.Groups {
		if grp.Type.Streamable() {
			continue
		}
		emit([]string{"Groups", strconv.Itoa(i), "Type"}, string(grp.Type))
	}
	for i, w := range req.Windows {
		// Every WIN_* is non-streamable today.
		emit([]string{"Windows", strconv.Itoa(i), "Type"}, string(w.Type))
	}

	return out
}

// reasonFromCode prefers the canonical Hint from the fixup metadata
// table when available; falls back to the supplied prose otherwise.
// Opportunistic reuse: stable prose without inventing wording per call
// site.
func reasonFromCode(c errors.Code, fallback string) string {
	meta, ok := errors.MetadataFor(c)
	if !ok || meta.FixupNotApplicable || len(meta.Fixups) == 0 {
		return fallback
	}
	if meta.Fixups[0].Hint != "" {
		return meta.Fixups[0].Hint
	}
	return fallback
}

// nearestFields returns schema field names within `maxDistance` of the
// supplied name, sorted by distance ascending then lexically. The
// second return value is the minimum distance observed (zero when the
// result is empty). Stable: ties resolve in alphabetical order so the
// suggestion list is deterministic across runs.
func nearestFields(name string, fields []string, maxDistance int) ([]string, int) {
	type match struct {
		name     string
		distance int
	}
	var matches []match
	for _, f := range fields {
		d := levenshtein(name, f)
		if d <= maxDistance && d > 0 {
			matches = append(matches, match{name: f, distance: d})
		}
	}
	if len(matches) == 0 {
		return nil, 0
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distance != matches[j].distance {
			return matches[i].distance < matches[j].distance
		}
		return matches[i].name < matches[j].name
	})
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.name
	}
	return out, matches[0].distance
}

// levenshtein computes the edit distance between two strings using
// classic DP with two rolling rows. O(len(a) * len(b)) time, O(min(len))
// space. Pure Go, no external deps — predict cannot import packages
// outside of types/, encoding/, errors/.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra := []rune(a)
	rb := []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	// Ensure rb is the shorter row so the buffer stays minimal.
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}
