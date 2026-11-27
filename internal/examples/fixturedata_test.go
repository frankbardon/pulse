package examples

import (
	"bytes"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// TestFixtureCohorts_MatchBuildScript: the embedded fixture set (what
// `pulse examples data` writes) is exactly the cohort loop of
// fixtures/build.sh, and every embedded CSV and schema is the
// checked-in file byte for byte.
func TestFixtureCohorts_MatchBuildScript(t *testing.T) {
	t.Parallel()
	osfs := afero.NewOsFs()
	script, err := afero.ReadFile(osfs, filepath.Join("fixtures", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^for cohort in ([^;]+); do`).FindSubmatch(script)
	if m == nil {
		t.Fatal("build.sh: no `for cohort in …; do` loop")
	}
	loop := strings.Fields(string(m[1]))
	slices.Sort(loop)
	got := FixtureCohorts()
	if !slices.Equal(got, loop) {
		t.Fatalf("embedded fixture cohorts %v\nbuild.sh loop            %v", got, loop)
	}
	for _, stem := range got {
		for _, pair := range []struct {
			rel  string
			read func(string) ([]byte, error)
		}{
			{filepath.Join("fixtures", stem+".csv"), FixtureCSV},
			{filepath.Join("fixtures", "schemas", stem+".json"), FixtureSchema},
		} {
			want, err := afero.ReadFile(osfs, pair.rel)
			if err != nil {
				t.Fatal(err)
			}
			b, err := pair.read(stem)
			if err != nil || !bytes.Equal(b, want) {
				t.Errorf("%s: embedded copy differs (%v)", pair.rel, err)
			}
		}
	}
	if _, err := FixtureCSV("no_such_cohort"); err == nil {
		t.Error("FixtureCSV of an unknown stem succeeded")
	}
}
