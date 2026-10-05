package temporal

import "time"

// canonicalUTCLayout is encoding.CanonicalDateTimeLayout, restated here
// because temporal must not import the encoding package that forwards
// into it. internal/io's TestExportZone_UTCArmMatchesCanonical pins the two together.
const canonicalUTCLayout = "2006-01-02T15:04:05Z"

// localWallLayout is the wall-clock half of FormatLocal's output; the
// numeric offset is appended by hand.
const localWallLayout = "2006-01-02T15:04:05"

// FormatLocal renders instant sec (epoch seconds) as an RFC 3339 / ISO
// 8601 literal on zone z's wall clock with its numeric UTC offset —
// 2026-03-29T03:30:00+02:00. The literal names exactly the instant sec:
// re-parsing it (encoding.ParseDateTime honours the offset) yields sec
// again, so an export rendered through FormatLocal re-imports losslessly.
//
// A nil or UTC-equivalent zone (Zone.IsUTC) renders the canonical
// `…Z` form, byte-identical to encoding.FormatDateTime. So does an
// instant whose offset is not a whole number of minutes — a pre-1900
// local-mean-time offset such as Europe/Berlin's +00:53:28: RFC 3339
// has no spelling for a seconds offset, and truncating it to +00:53
// would name a different instant, so the UTC form (still exact) is the
// only honest literal. One string allocation per call.
func FormatLocal(sec int64, z *Zone) string {
	if z == nil || z.IsUTC() {
		return time.Unix(sec, 0).UTC().Format(canonicalUTCLayout)
	}
	off := z.Offset(sec)
	if off%60 != 0 {
		return time.Unix(sec, 0).UTC().Format(canonicalUTCLayout)
	}
	var buf [32]byte
	b := time.Unix(sec+int64(off), 0).UTC().AppendFormat(buf[:0], localWallLayout)
	sign := byte('+')
	if off < 0 {
		sign = '-'
		off = -off
	}
	mins := off / 60
	h, m := mins/60, mins%60
	b = append(b, sign, byte('0'+h/10), byte('0'+h%10), ':', byte('0'+m/10), byte('0'+m%10))
	return string(b)
}
