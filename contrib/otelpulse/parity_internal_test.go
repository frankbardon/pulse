package otelpulse

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/obsprom"
)

// TestDescriptionsAndBucketsMatchBuiltinExporter pins the instrument
// descriptions and advised buckets to Pulse's built-in exporter. (A
// test-only import: the module path sits under the Pulse module's tree,
// so its internal packages are visible to it.)
func TestDescriptionsAndBucketsMatchBuiltinExporter(t *testing.T) {
	for name, d := range descriptions {
		want, ok := obsprom.Help(name)
		if !ok {
			t.Errorf("%s is not a documented Pulse metric", name)
		}
		if d != want {
			t.Errorf("%s description\n got %q\nwant %q", name, d, want)
		}
	}
	if !slices.Equal(DefaultBuckets, obsprom.DefaultBuckets) {
		t.Errorf("DefaultBuckets %v, want %v", DefaultBuckets, obsprom.DefaultBuckets)
	}
}
