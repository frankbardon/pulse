package pulse

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// exampleFeatureProfilesFS holds the published example feature profiles.
// They are frozen: a published file is never edited in place (see
// examples/profiles/README.md).
//
//go:embed examples/profiles/*.json
var exampleFeatureProfilesFS embed.FS

const exampleFeatureProfilesDir = "examples/profiles"

// featureProfileReasonUnknownExample is the "reason" detail of the
// PULSE_FEATURE_PROFILE_INVALID error ExampleFeatureProfile returns for a
// name that is not a published example.
const featureProfileReasonUnknownExample = "unknown_example"

// ExampleFeatureProfiles returns the names of the published example
// feature profiles, sorted. A name is the example's file name without the
// ".json" extension (for example "minimal"); pass it to
// ExampleFeatureProfile.
func ExampleFeatureProfiles() []string {
	entries, err := exampleFeatureProfilesFS.ReadDir(exampleFeatureProfilesDir)
	if err != nil {
		return []string{}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(names)
	return names
}

// ExampleFeatureProfile returns the published example feature profile
// called name, decoded with the same strict decode as
// ParseFeatureProfile. Each call returns a fresh value the caller owns.
//
// Examples are frozen: each lists exact feature names as of the release
// in its WrittenWith, and Pulse never edits a published example in place.
// Every example passes pulse.New validation when handed over as
// Options.FeatureProfile.
//
// An unknown name is refused with PULSE_FEATURE_PROFILE_INVALID, reason
// "unknown_example", naming the available examples under "examples".
func ExampleFeatureProfile(name string) (*FeatureProfile, error) {
	known := ExampleFeatureProfiles()
	i := sort.SearchStrings(known, name)
	if i == len(known) || known[i] != name {
		return nil, featureProfileInvalid(featureProfileReasonUnknownExample,
			fmt.Sprintf("feature profile: no example profile named %q (available: %s)", name, strings.Join(known, ", ")),
			map[string]any{"name": name, "examples": known})
	}
	raw, err := exampleFeatureProfilesFS.ReadFile(path.Join(exampleFeatureProfilesDir, name+".json"))
	if err != nil {
		return nil, featureProfileInvalid(featureProfileReasonFileUnreadable,
			fmt.Sprintf("feature profile: example %q: %v", name, err),
			map[string]any{"name": name})
	}
	return decodeFeatureProfile(raw, path.Join(exampleFeatureProfilesDir, name+".json"))
}
