package pulse

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
)

// TestPathMessages_WindowsPathSingleBackslash: a Windows-style path in
// an error message reads with single backslashes (pathq.Quote), never
// the doubled `C:\\cfg\\…` of %q, while details.path carries it raw. On
// a non-Windows host a backslash is an ordinary name byte, so the memory
// filesystem stores the path verbatim and each loader reaches its
// message.
func TestPathMessages_WindowsPathSingleBackslash(t *testing.T) {
	t.Parallel()

	const path = `C:\cfg\tables`
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		load func(*Options) error
		set  func(*Options)
	}{
		{"populations", loadPopulationsFromDir, func(o *Options) { o.PopulationsDir = path }},
		{"label tables", loadLabelTablesFromDir, func(o *Options) { o.LabelTablesDir = path }},
		{"lookup tables", loadLookupTablesFromDir, func(o *Options) { o.LookupTablesDir = path }},
		{"range tables", loadRangeTablesFromDir, func(o *Options) { o.RangeTablesDir = path }},
	} {
		opts := Options{FS: fsys}
		tc.set(&opts)
		err := tc.load(&opts)
		ce, ok := err.(*errors.CodedError)
		if !ok {
			t.Fatalf("%s: %v, want a coded error", tc.name, err)
		}
		if !strings.Contains(ce.Message, `"C:\cfg\tables" is not a directory`) || strings.Contains(ce.Message, `\\`) {
			t.Errorf("%s: message %q, want the path with single backslashes", tc.name, ce.Message)
		}
		if ce.Details["path"] != path {
			t.Errorf("%s: details.path %v, want the raw path", tc.name, ce.Details["path"])
		}
	}

	_, err := loadFeatureProfileFile(fsys, `C:\cfg\missing.json`)
	ce, ok := err.(*errors.CodedError)
	if !ok || !strings.Contains(ce.Message, `cannot read "C:\cfg\missing.json"`) || ce.Details["path"] != `C:\cfg\missing.json` {
		t.Errorf("feature profile: %v, want the path with single backslashes and a raw details.path", err)
	}
}
