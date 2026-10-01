package facadebridge_test

import (
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/spf13/afero"
)

func TestExtensionsSnapshotHookInstalled(t *testing.T) {
	if facadebridge.ExtensionsSnapshot == nil {
		t.Fatal("root package did not install the ExtensionsSnapshot hook")
	}
	bare, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if facadebridge.ExtensionsSnapshot(bare) != nil {
		t.Fatal("ExtensionsSnapshot of an extension-free Pulse should be nil")
	}
	start, end := "2024-01-01", "2024-03-31"
	p, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{RangeTables: map[string]pulse.RangeTable{
			"quarters": {Ranges: []pulse.DateRangeSpec{{Label: "Q1", Start: &start, End: &end}}},
		}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	snap := facadebridge.ExtensionsSnapshot(p)
	if snap == nil || len(snap.RangeTables) != 1 || snap.RangeTables[0].Name != "quarters" {
		t.Fatalf("ExtensionsSnapshot(*Pulse) = %+v, want the registered range table", snap)
	}
	if facadebridge.ExtensionsSnapshot("not a pulse") != nil {
		t.Fatal("ExtensionsSnapshot(non-Pulse) should return nil")
	}
}
