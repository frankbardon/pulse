package encoding

import (
	"time"

	"github.com/frankbardon/pulse/errors"
)

// DateFormats enumerates the date literal layouts ParseDate accepts, in
// priority order — the first layout that parses a given literal wins.
// This is the authoritative list for turning a date literal into the
// persisted on-wire epoch-day representation; io/import.go's column-type
// inference (io/infer.go's own, independently-maintained dateFormats)
// is a lighter, best-effort "does this column look date-shaped" probe
// and intentionally stays separate from this conversion authority.
var DateFormats = []string{
	"2006-01-02",
	"01/02/2006",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006/01/02",
	"02-Jan-2006",
}

// ParseDate parses raw against DateFormats (first match wins) and
// returns the on-wire representation the .pulse `date` field type
// carries: whole days since the Unix epoch, narrowed to uint32 — exactly
// the conversion io/import.go's row importer performs when converting a
// date column cell (`t.Unix() / 86400`). This function is the single
// source of truth both the importer (io/import.go's convertValue) and
// the point-lookup key resolver (processing.ResolveLookupKeyBytes) call,
// so a date literal parsed at import time and the same literal parsed at
// lookup time always resolve to the identical on-wire uint32 — no
// separate epoch-math implementation to drift out of sync.
//
// Returns an ENCODING_INVALID coded error when raw matches none of
// DateFormats.
func ParseDate(raw string) (uint32, error) {
	for _, layout := range DateFormats {
		t, err := time.Parse(layout, raw)
		if err == nil {
			days := t.Unix() / 86400
			return uint32(days), nil
		}
	}
	return 0, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		"cannot parse date literal against any known layout",
		map[string]any{"value": raw})
}
