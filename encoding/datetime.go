package encoding

import (
	"time"

	"github.com/frankbardon/pulse/errors"
)

// CanonicalDateTimeLayout is the single text form FormatDateTime emits
// for a `datetime` field: an ISO-8601 / RFC 3339 calendar-date +
// wall-clock-time literal with a literal `Z` suffix, second resolution,
// no fractional part. It is the canonical round-trip form — a value
// exported through it re-parses under DateTimeFormats[0] to the exact
// same on-wire uint64, which is what makes the text-oriented adapters
// (CSV / TSV / NDJSON / JSON) lossless for `datetime` without any
// format-native datetime column type.
const CanonicalDateTimeLayout = "2006-01-02T15:04:05Z"

// DateTimeFormats enumerates the datetime literal layouts ParseDateTime
// accepts, in priority order — the first layout that parses a given
// literal wins. Same shape and same first-match-wins contract as
// DateFormats, but a deliberately stricter list:
//
//   - The slash forms DateFormats tolerates ("01/02/2006") are NOT
//     accepted. `03/04/2024` is 4 March in most of the world and
//     3 April in the US; a datetime column silently swapping day and
//     month is exactly the quiet degradation this type must not do.
//     An ambiguous literal fails loudly with ENCODING_INVALID instead.
//   - A literal carrying no time-of-day is NOT accepted. That is a
//     `date`, and io/infer.go's column classification depends on this
//     rejection to keep a date-only column typed FieldTypeDate rather
//     than widening it to FieldTypeDateTime.
//
// Go's time.Parse accepts a fractional-second field immediately after
// the seconds field even when the layout omits one, so
// "2024-03-04T10:11:12.5Z" parses under the first layout with no
// dedicated entry. Sub-second precision is then truncated by the
// seconds-resolution on-wire representation (see ParseDateTime).
var DateTimeFormats = []string{
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// ParseDateTime parses raw against DateTimeFormats (first match wins)
// and returns the on-wire representation the .pulse `datetime` field
// type carries: whole SECONDS since the Unix epoch, as uint64.
//
// Note the contrast with ParseDate, which returns epoch DAYS as a
// uint32. The two are not interchangeable and must never be swapped:
// a `date` cell holds days, a `datetime` cell holds seconds.
//
// Timezone policy is naive UTC — the format stores an instant and no
// zone. A layout with no offset is read as UTC (time.Parse's own
// default), and a layout carrying an explicit offset is normalised to
// the same instant, with the offset itself discarded. Round-tripping
// "2024-03-04T10:11:12+02:00" therefore yields "2024-03-04T08:11:12Z":
// the instant is preserved exactly, the local-wall-clock presentation
// is not.
//
// Precision is seconds; any fractional-second component in raw is
// truncated toward the epoch by time.Time.Unix.
//
// Pre-1970 instants are representable: the negative int64 second count
// is reinterpreted as uint64 two's-complement, which FormatDateTime
// reverses exactly, so the round trip is lossless across the whole
// int64 second range.
//
// Returns an ENCODING_INVALID coded error when raw matches none of
// DateTimeFormats.
func ParseDateTime(raw string) (uint64, error) {
	for _, layout := range DateTimeFormats {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return uint64(t.Unix()), nil
		}
	}
	return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		"cannot parse datetime literal against any known layout",
		map[string]any{"value": raw})
}

// FormatDateTime renders an on-wire `datetime` value (epoch seconds as
// uint64) back into CanonicalDateTimeLayout. It is the exact inverse of
// ParseDateTime: FormatDateTime(ParseDateTime(s)) re-parses to the same
// uint64 for every s that ParseDateTime accepts.
//
// The int64 conversion is deliberate and reverses the uint64 widening
// ParseDateTime performs, so pre-1970 instants render as their real
// calendar time rather than as a year-292-billion overflow.
func FormatDateTime(raw uint64) string {
	return time.Unix(int64(raw), 0).UTC().Format(CanonicalDateTimeLayout)
}
