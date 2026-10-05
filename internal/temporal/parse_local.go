package temporal

import (
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	perr "github.com/frankbardon/pulse/errors"
)

// Local wall-clock parsing: the import edge's counterpart of the
// instant-to-local primitives in local.go. A naive datetime literal
// ("2026-03-29 02:30") names a wall-clock reading in some zone, not an
// instant; resolving it to the instant Pulse stores (UTC epoch seconds)
// means inverting the zone's offset function — which, across a DST
// transition, has two answers (a fall-back OVERLAP) or none (a
// spring-forward GAP). Ambiguity is the caller's policy for those.

// Ambiguity is the policy for a wall-clock reading that a zone's DST
// transition makes ambiguous (an overlap: it occurs twice) or
// nonexistent (a gap: the clock skips it).
type Ambiguity uint8

const (
	// AmbiguityError refuses both cases: ResolveLocal / ParseLocal
	// return ErrLocalAmbiguous or ErrLocalNonexistent. The default.
	AmbiguityError Ambiguity = iota
	// AmbiguityEarlier resolves with the PRE-transition offset: an
	// overlap takes its first occurrence; a gap reads the skipped wall
	// clock as if the transition had not happened yet (Europe/Berlin
	// 2026-03-29 02:30 → 01:30Z).
	AmbiguityEarlier
	// AmbiguityLater resolves with the POST-transition offset: an
	// overlap takes its second occurrence; a gap reads the skipped
	// wall clock as if the transition had already happened
	// (Europe/Berlin 2026-03-29 02:30 → 00:30Z).
	AmbiguityLater
)

// String returns the policy's spelling: "error", "earlier" or "later".
func (a Ambiguity) String() string {
	switch a {
	case AmbiguityEarlier:
		return "earlier"
	case AmbiguityLater:
		return "later"
	}
	return "error"
}

// ParseAmbiguity parses a policy spelling. The empty string is the
// default, AmbiguityError; anything but "", "error", "earlier" and
// "later" (exact, lower case) reports ok=false.
func ParseAmbiguity(s string) (Ambiguity, bool) {
	switch s {
	case "", "error":
		return AmbiguityError, true
	case "earlier":
		return AmbiguityEarlier, true
	case "later":
		return AmbiguityLater, true
	}
	return AmbiguityError, false
}

// LocalKind tells the caller how a literal resolved.
type LocalKind uint8

const (
	// LocalExact: a naive wall clock the zone reads exactly once.
	LocalExact LocalKind = iota
	// LocalOffset: the literal carried its own `Z` or ±HH:MM offset and
	// named that instant; the zone was not consulted.
	LocalOffset
	// LocalAmbiguous: a naive wall clock the zone reads twice (a DST
	// overlap). Resolved by the policy, or refused under AmbiguityError.
	LocalAmbiguous
	// LocalNonexistent: a naive wall clock the zone never reads (a DST
	// gap). Resolved by the policy, or refused under AmbiguityError.
	LocalNonexistent
)

// ErrLocalAmbiguous and ErrLocalNonexistent are what ResolveLocal and
// ParseLocal return under AmbiguityError for an overlap or a gap. They
// carry no row or column; the caller wraps them with its own context.
var (
	ErrLocalAmbiguous   = stderrors.New("local time is ambiguous: a DST transition repeats it")
	ErrLocalNonexistent = stderrors.New("local time does not exist: a DST transition skips it")
)

// dateTimeLayouts is the datetime literal list, in priority order. It
// MUST equal encoding.DateTimeFormats (pinned by a parity test there):
// encoding forwards into this package, so the list cannot be imported
// from it. A layout carrying "Z07:00" accepts only an offset-bearing
// literal (`Z` or ±HH:MM); every other layout accepts only a naive one —
// which is how ParseLocal tells the two apart, something time.Parse's
// result alone cannot do (a naive literal parses to a UTC time too).
var dateTimeLayouts = []string{
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// DateTimeLayouts returns a copy of the datetime literal layouts
// ParseLocal walks, in priority order.
func DateTimeLayouts() []string {
	return append([]string(nil), dateTimeLayouts...)
}

// ParseLocal parses a datetime literal and returns the instant it names
// as epoch seconds. It accepts exactly the literals encoding.ParseDateTime
// accepts, first matching layout wins, and:
//
//   - an offset-bearing literal (`Z`, `+02:00`) names that instant; z is
//     ignored (kind LocalOffset) — the answer ParseDateTime gives;
//   - a naive literal is a wall-clock reading in z, resolved by
//     ResolveLocal under policy a. Under UTC (or any UTC-equivalent
//     zone) that is the answer ParseDateTime gives.
//
// Sub-second digits are floored toward the past, as ParseDateTime does.
// A literal matching no layout returns the same ENCODING_INVALID coded
// error ParseDateTime returns.
func ParseLocal(s string, z *Zone, a Ambiguity) (int64, LocalKind, error) {
	for _, layout := range dateTimeLayouts {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		if strings.Contains(layout, "Z07:00") {
			return t.Unix(), LocalOffset, nil
		}
		return ResolveLocal(t.Unix(), z, a)
	}
	return 0, LocalExact, perr.NewCodedErrorWithDetails(perr.ENCODING_INVALID,
		"cannot parse datetime literal against any known layout",
		map[string]any{"value": s})
}

// ResolveLocal returns the instant (epoch seconds) at which a clock in z
// reads wall, a wall-clock reading counted as seconds since
// 1970-01-01T00:00:00 on that clock.
//
// A reading z shows exactly once resolves to that instant (LocalExact).
// One a fall-back shows twice is LocalAmbiguous: AmbiguityEarlier takes
// the first occurrence (the pre-transition offset), AmbiguityLater the
// second. One a spring-forward skips is LocalNonexistent:
// AmbiguityEarlier reads it at the pre-transition offset, AmbiguityLater
// at the post-transition offset. Under AmbiguityError both return
// ErrLocalAmbiguous / ErrLocalNonexistent and an instant of 0.
//
// UTC and UTC-equivalent zones return wall unchanged.
func ResolveLocal(wall int64, z *Zone, a Ambiguity) (int64, LocalKind, error) {
	if z.IsUTC() {
		return wall, LocalExact, nil
	}
	// Every tz offset is well under a day, so each instant reading wall
	// lies in (wall-1d, wall+1d); the spans of [wall-2d, wall+2d) are
	// visited in time order (exactly as LocalMidnightUTC walks them).
	// Within one span [s0, s1) of offset off the wall clock is s+off, so
	// the span holds an instant reading wall iff wall-off lies in it.
	lo, hi := wall-2*SecondsPerDay, wall+2*SecondsPerDay
	var (
		first, last int64
		found       int
		gapPre      int64 // wall at the offset before the skipping transition
		gapPost     int64 // wall at the offset after it
		gap         bool
		prevOff     int64
	)
	for s0 := lo; ; {
		s1 := z.nextChange(s0, hi)
		off := int64(z.Offset(s0))
		cand := wall - off
		switch {
		case cand >= s0 && cand < s1:
			if found == 0 {
				first = cand
			}
			last = cand
			found++
		case s0 > lo && !gap && wall-prevOff >= s0 && cand < s0:
			// The previous span ends before wall is read at its offset
			// and this span starts after wall would have been read at
			// its own: the transition at s0 skips wall.
			gap, gapPre, gapPost = true, wall-prevOff, cand
		}
		if s1 >= hi {
			break
		}
		prevOff, s0 = off, s1
	}
	switch {
	case found == 1:
		return first, LocalExact, nil
	case found > 1:
		switch a {
		case AmbiguityEarlier:
			return first, LocalAmbiguous, nil
		case AmbiguityLater:
			return last, LocalAmbiguous, nil
		}
		return 0, LocalAmbiguous, ErrLocalAmbiguous
	case gap:
		switch a {
		case AmbiguityEarlier:
			return gapPre, LocalNonexistent, nil
		case AmbiguityLater:
			return gapPost, LocalNonexistent, nil
		}
		return 0, LocalNonexistent, ErrLocalNonexistent
	}
	// Unreachable for a real zone (a reading neither held nor skipped);
	// refuse rather than guess.
	return 0, LocalNonexistent, ErrLocalNonexistent
}

// LoadImportZone resolves a SOURCE zone for the import edge. It accepts
// everything LoadZone accepts and, in addition, a fixed offset spelled
// exactly "+HH:MM" or "-HH:MM" (|offset| <= 18:00). The fixed form is
// for imports only — a request-time zone stays IANA-only (LoadZone
// refuses it). A fixed-offset zone has no transitions, so every naive
// reading in it is LocalExact; "+00:00" / "-00:00" is UTC-equivalent.
// Anything else is the PULSE_TIMEZONE_UNKNOWN refusal.
func LoadImportZone(name string) (*Zone, error) {
	if off, ok := parseFixedOffset(name); ok {
		return fixedZone(name, off), nil
	}
	if len(name) > 0 && (name[0] == '+' || name[0] == '-') {
		return nil, unknownImportZone(name, "a fixed offset must be spelled ±HH:MM, at most 18:00")
	}
	if name == "UTC" || wellFormedZoneName(name) {
		return LoadZone(name)
	}
	return nil, unknownImportZone(name, "it is not \"UTC\", an IANA Area/Location name or a ±HH:MM offset")
}

// parseFixedOffset parses "+HH:MM" / "-HH:MM" into seconds east of UTC.
func parseFixedOffset(s string) (int32, bool) {
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return 0, false
	}
	digit := func(c byte) (int32, bool) { return int32(c - '0'), c >= '0' && c <= '9' }
	var v [4]int32
	for k, i := range []int{1, 2, 4, 5} {
		d, ok := digit(s[i])
		if !ok {
			return 0, false
		}
		v[k] = d
	}
	h, m := v[0]*10+v[1], v[2]*10+v[3]
	if m > 59 || h > 18 || h == 18 && m != 0 {
		return 0, false
	}
	off := h*SecondsPerHour + m*60
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

// fixedZone builds a transition-free Zone at offset off.
func fixedZone(name string, off int32) *Zone {
	return &Zone{
		name:      name,
		loc:       time.FixedZone(name, int(off)),
		zeroFixed: off == 0,
		starts:    []int64{windowStart},
		offs:      []int32{off},
	}
}

func unknownImportZone(name, why string) error {
	return perr.NewCodedErrorWithDetails(
		perr.PULSE_TIMEZONE_UNKNOWN,
		fmt.Sprintf("unknown source time zone %q: %s; use \"UTC\", an IANA Area/Location name such as \"Europe/Berlin\", or a fixed offset such as \"+05:30\"", name, why),
		map[string]any{perr.DetailTimeZone: name},
	)
}
