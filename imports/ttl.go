package imports

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseTTL converts a human-friendly TTL string into a time.Duration.
// Accepted forms:
//
//   - Go duration strings: "1h", "30m", "3600s", "1h30m", "500ms"
//   - Day suffix: "7d" => 7*24h, "1d" => 24h. Combine via Go-duration
//     additivity is not supported ("1d6h" is rejected) — keep parsing
//     surface narrow; callers wanting hybrid units should use Go form.
//   - "0" or "" => 0 (caller decides what zero means: in Open it falls
//     back to DefaultTTL; the empty-string case is rejected by callers
//     that require an explicit TTL).
//   - "pin", "none", "-1": parse to negative => pinned (no expiry).
//
// Returns an error wrapping the original input on any parse failure;
// the error is intentionally bare (no error code) — callers that want
// a CodedError wrap this at their boundary.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	switch strings.ToLower(s) {
	case "pin", "none", "-1":
		return -1 * time.Second, nil
	}

	// Day suffix is the only extension beyond Go's native duration
	// grammar. Accept a leading positive integer followed by "d".
	if body, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(body)
		if err != nil {
			return 0, fmt.Errorf("imports: invalid TTL %q: %w", s, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("imports: TTL %q must be non-negative (use \"pin\" for never-expire)", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("imports: invalid TTL %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("imports: TTL %q must be non-negative (use \"pin\" for never-expire)", s)
	}
	return d, nil
}

// FormatTTL renders a Duration back into the shortest unambiguous
// representation. Used in CLI list output and sidecar diagnostics.
// Negative durations render as "pin"; zero renders as "0s" for
// compactness across humans + parsers.
func FormatTTL(d time.Duration) string {
	if d < 0 {
		return "pin"
	}
	if d == 0 {
		return "0s"
	}
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	}
	return d.String()
}
