package prompulse

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/obsprom"
)

// TestHelpAndBucketsMatchBuiltinExporter pins this adapter's HELP text
// and default buckets to Pulse's built-in exporter, so a metric scraped
// from `pulse mcp --metrics-addr` and from a host registry reads the
// same. (A test-only import of a package internal to the same module.)
func TestHelpAndBucketsMatchBuiltinExporter(t *testing.T) {
	t.Parallel()
	for name, h := range help {
		want, ok := obsprom.Help(name)
		if !ok {
			t.Errorf("%s is not a documented Pulse metric", name)
		}
		if h != want {
			t.Errorf("%s help\n got %q\nwant %q", name, h, want)
		}
	}
	if !slices.Equal(DefaultBuckets, obsprom.DefaultBuckets) {
		t.Errorf("DefaultBuckets %v, want %v", DefaultBuckets, obsprom.DefaultBuckets)
	}
}
