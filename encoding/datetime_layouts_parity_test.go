package encoding

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/temporal"
)

// TestDateTimeFormats_MatchTemporalLayouts pins the import edge's
// source-zone parser (temporal.ParseLocal) to the layout list
// ParseDateTime walks. temporal cannot import encoding (encoding forwards
// into it), so it carries its own copy; a drift would let a literal parse
// as datetime under one path and fail, or resolve differently, under the
// other.
func TestDateTimeFormats_MatchTemporalLayouts(t *testing.T) {
	if got := temporal.DateTimeLayouts(); !slices.Equal(got, DateTimeFormats) {
		t.Fatalf("temporal.DateTimeLayouts() = %q, encoding.DateTimeFormats = %q", got, DateTimeFormats)
	}
}
