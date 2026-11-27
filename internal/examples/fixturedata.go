package examples

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// The fixture cohorts' sources: one CSV per cohort and the explicit
// schema it imports under. Only these inputs are embedded — never a
// built .pulse file (the binary writes those, Pulse.WriteExampleData)
// and never anything under internal/eval.
//
//go:embed fixtures/*.csv fixtures/schemas/*.json
var fixtureFiles embed.FS

// FixtureCohorts returns the stem of every embedded fixture cohort
// (experiment, transactions, …), sorted: the cohorts
// internal/examples/fixtures/build.sh imports. A stem has both a CSV
// and a schema; the walk of the embedded schemas is the list.
func FixtureCohorts() []string {
	entries, err := fs.ReadDir(fixtureFiles, "fixtures/schemas")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if stem, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			out = append(out, stem)
		}
	}
	sort.Strings(out)
	return out
}

// FixtureCSV returns the embedded CSV source of a fixture cohort.
func FixtureCSV(stem string) ([]byte, error) {
	return readFixture(path.Join("fixtures", stem+".csv"))
}

// FixtureSchema returns the embedded schema JSON of a fixture cohort
// (the file build.sh passes as --schema).
func FixtureSchema(stem string) ([]byte, error) {
	return readFixture(path.Join("fixtures", "schemas", stem+".json"))
}

func readFixture(name string) ([]byte, error) {
	b, err := fixtureFiles.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("examples: no embedded fixture %s", name)
	}
	return b, nil
}
