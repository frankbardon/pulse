package descriptor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/spf13/afero"
)

// profileGoldenFixtures are the fixture feature profiles under
// testdata/profiles/ (a subdirectory, so TestGoldensNotHandEdited does
// not demand a hash trailer on the hand-written inputs). Their goldens
// live at the top of testdata/ — manifest.<fixture>.json and
// payload-schema.<fixture>.json — where the hand-edit gate covers them.
var profileGoldenFixtures = []string{"minimal", "survey-crosstab", "empty"}

// newProfilePulse builds an instance under the named fixture profile.
// pulse_version is pinned by the caller: the manifest carries it and
// the default digest depends on it.
func newProfilePulse(t *testing.T, fixture string) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{
		FS:                 afero.NewReadOnlyFs(afero.NewOsFs()),
		FeatureProfileFile: "testdata/profiles/" + fixture + ".json",
	})
	if err != nil {
		t.Fatalf("New with fixture %s: %v", fixture, err)
	}
	return p
}

// TestProfileManifestGolden pins each fixture instance's manifest
// envelope. Regenerate with go test ./descriptor/ -run 'Test.*Golden' -update.
func TestProfileManifestGolden(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	for _, fixture := range profileGoldenFixtures {
		t.Run(fixture, func(t *testing.T) {
			m := newProfilePulse(t, fixture).Manifest(context.Background())
			data, err := json.MarshalIndent(descriptor.NewEnvelope(m), "", "  ")
			if err != nil {
				t.Fatalf("json.MarshalIndent: %v", err)
			}
			compareGolden(t, "manifest."+fixture+".json", data)
		})
	}
}

// TestProfilePayloadSchemaGolden pins each fixture instance's payload
// JSON Schema, byte for byte as Pulse.PayloadSchema serves it.
func TestProfilePayloadSchemaGolden(t *testing.T) {
	defer buildinfo.SetForTest("v0.0.0-test")()
	for _, fixture := range profileGoldenFixtures {
		t.Run(fixture, func(t *testing.T) {
			data, err := newProfilePulse(t, fixture).PayloadSchema()
			if err != nil {
				t.Fatalf("PayloadSchema: %v", err)
			}
			compareGolden(t, "payload-schema."+fixture+".json", data)
		})
	}
}
