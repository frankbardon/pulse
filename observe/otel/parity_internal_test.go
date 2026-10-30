package otelpulse

import (
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/obsprom"
)

// TestDescriptionsAndBucketsMatchBuiltinExporter pins the instrument
// descriptions and advised buckets to Pulse's built-in exporter. (A
// test-only import of a package internal to the same module.)
func TestDescriptionsAndBucketsMatchBuiltinExporter(t *testing.T) {
	t.Parallel()
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

// TestScopeNameIsImportPath pins the instrumentation scope to the
// package's own import path, so a moved package cannot keep reporting a
// stale scope.
func TestScopeNameIsImportPath(t *testing.T) {
	t.Parallel()
	if got := reflect.TypeOf(tracer{}).PkgPath(); ScopeName != got {
		t.Errorf("ScopeName %q, want the import path %q", ScopeName, got)
	}
	if ScopeName != "github.com/frankbardon/pulse/observe/otel" {
		t.Errorf("ScopeName %q, want github.com/frankbardon/pulse/observe/otel", ScopeName)
	}
}
