package integration_test

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// installDocPath is the Getting Started page that names the release assets;
// releaseDistScript is the one script that builds them (`make dist` and
// release.yml both call it).
const (
	installDocPath    = "docs/src/getting-started/installation.md"
	releaseDistScript = "scripts/release-dist.sh"
)

var (
	// distTargetsRe reads the script's six OS/ARCH targets.
	distTargetsRe = regexp.MustCompile(`(?m)^ALL_TARGETS="([^"]+)"`)
	// distNameRe reads the archive_name lines: one per archive extension.
	distNameRe = regexp.MustCompile(`echo "pulse_\$\{VERSION\}_\$\{1%/\*\}_\$\{1#\*/\}\.([a-z.]+)"`)
	// docAssetRe is an asset name written with the page's placeholder.
	docAssetRe = regexp.MustCompile(`pulse_<version>_([a-z0-9]+)_([a-z0-9]+)\.(tar\.gz|zip)`)
)

// TestInstallDocAssetNamesMatchRelease holds the installation page's
// download table to the archives scripts/release-dist.sh builds: every
// target's archive is named, nothing else is, and checksums.txt is named.
// A change to the targets or the naming scheme fails here until the page
// follows.
func TestInstallDocAssetNamesMatchRelease(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(releaseDistScript)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(installDocPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range installAssetProblems(string(script), string(doc)) {
		t.Error(p)
	}
}

// TestInstallDocAssetNamesMatchReleaseRefuses pins the comparison on
// fixtures: a missing target, an extra asset, a missing checksums file and
// an unreadable naming scheme are each reported.
func TestInstallDocAssetNamesMatchReleaseRefuses(t *testing.T) {
	t.Parallel()

	script := `ALL_TARGETS="linux/amd64 windows/arm64"
archive_name() {
	if [ "${1%/*}" = "windows" ]; then
		echo "pulse_${VERSION}_${1%/*}_${1#*/}.zip"
	else
		echo "pulse_${VERSION}_${1%/*}_${1#*/}.tar.gz"
	fi
}
`
	good := "`pulse_<version>_linux_amd64.tar.gz` `pulse_<version>_windows_arm64.zip` checksums.txt"
	if p := installAssetProblems(script, good); len(p) != 0 {
		t.Fatalf("clean fixture reported %q", p)
	}
	cases := map[string]struct{ script, doc, want string }{
		"missing target": {script, "`pulse_<version>_linux_amd64.tar.gz` checksums.txt", "windows_arm64.zip"},
		"extra asset":    {script, good + " `pulse_<version>_darwin_arm64.tar.gz`", "darwin_arm64"},
		"wrong archive":  {script, good + " `pulse_<version>_linux_amd64.zip`", "linux_amd64.zip"},
		"no checksums":   {script, strings.TrimSuffix(good, " checksums.txt"), "checksums.txt"},
		"scheme changed": {strings.ReplaceAll(script, "pulse_${VERSION}", "pulse-${VERSION}"), good, "naming scheme"},
	}
	for name, c := range cases {
		p := installAssetProblems(c.script, c.doc)
		if len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: problems %q, want one naming %q", name, p, c.want)
		}
	}
}

// installAssetProblems compares the asset names the release script builds
// with the ones the installation page names.
func installAssetProblems(script, doc string) []string {
	m := distTargetsRe.FindStringSubmatch(script)
	if m == nil {
		return []string{releaseDistScript + ": no ALL_TARGETS line"}
	}
	var exts []string
	for _, e := range distNameRe.FindAllStringSubmatch(script, -1) {
		exts = append(exts, e[1])
	}
	sort.Strings(exts)
	if strings.Join(exts, " ") != "tar.gz zip" {
		return []string{fmt.Sprintf("%s: archive naming scheme changed (read extensions %q): update %s and this test", releaseDistScript, exts, installDocPath)}
	}
	want := map[string]bool{}
	for _, target := range strings.Fields(m[1]) {
		goos, goarch, _ := strings.Cut(target, "/")
		ext := "tar.gz"
		if goos == "windows" {
			ext = "zip"
		}
		want[fmt.Sprintf("pulse_<version>_%s_%s.%s", goos, goarch, ext)] = true
	}
	have := map[string]bool{}
	for _, a := range docAssetRe.FindAllString(doc, -1) {
		have[a] = true
	}
	var problems []string
	for a := range want {
		if !have[a] {
			problems = append(problems, fmt.Sprintf("%s does not name the release asset %s", installDocPath, a))
		}
	}
	for a := range have {
		if !want[a] {
			problems = append(problems, fmt.Sprintf("%s names %s, which %s does not build", installDocPath, a, releaseDistScript))
		}
	}
	if !strings.Contains(doc, "checksums.txt") {
		problems = append(problems, installDocPath+" does not name checksums.txt")
	}
	sort.Strings(problems)
	return problems
}
