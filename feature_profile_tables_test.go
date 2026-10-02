package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/spf13/afero"
)

// TestManifest_NamedTablesFollowTheirCapability: on a profiled instance
// extensions.label_tables lists the registered label tables only when
// capability:labels is enabled, and extensions.range_tables the range
// tables only when capability:range_tables is enabled. A hidden one is
// the empty list ([] on the wire), exactly as an instance with no tables
// registered; a profile-free instance lists both.
func TestManifest_NamedTablesFollowTheirCapability(t *testing.T) {
	cases := []struct {
		name      string
		features  []string // nil = no profile
		wantLabel bool
		wantRange bool
	}{
		{"no profile", nil, true, true},
		{"both hidden", []string{"capability:process"}, false, false},
		{"labels only", []string{"capability:process", "capability:labels"}, true, false},
		{"range tables only", []string{"capability:process", "capability:range_tables"}, false, true},
		{"both enabled", []string{"capability:process", "capability:labels", "capability:range_tables"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{FS: afero.NewMemMapFs(), Extensions: sweepNamedTables()}
			if tc.features != nil {
				opts.FeatureProfile = &FeatureProfile{Features: tc.features}
			}
			p, err := New(opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			m := p.Manifest(context.Background())

			var labels, ranges []string
			for _, lt := range m.Extensions.LabelTables {
				labels = append(labels, lt.Name)
			}
			for _, rt := range m.Extensions.RangeTables {
				ranges = append(ranges, rt.Name)
			}
			if got := slices.Equal(labels, []string{sweepLabelTable}); got != tc.wantLabel {
				t.Errorf("label_tables = %v, want listed=%v", labels, tc.wantLabel)
			}
			if got := slices.Equal(ranges, []string{sweepRangeTable}); got != tc.wantRange {
				t.Errorf("range_tables = %v, want listed=%v", ranges, tc.wantRange)
			}

			// Wire shape: a hidden list is [] (never null, never absent),
			// matching the never-registered instance.
			raw, err := json.Marshal(m.Extensions)
			if err != nil {
				t.Fatal(err)
			}
			var ext map[string]json.RawMessage
			if err := json.Unmarshal(raw, &ext); err != nil {
				t.Fatal(err)
			}
			if !tc.wantLabel && string(ext["label_tables"]) != "[]" {
				t.Errorf("hidden label_tables wire = %s, want []", ext["label_tables"])
			}
			if !tc.wantRange && string(ext["range_tables"]) != "[]" {
				t.Errorf("hidden range_tables wire = %s, want []", ext["range_tables"])
			}
		})
	}
}
