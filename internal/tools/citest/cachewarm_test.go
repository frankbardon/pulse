package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// cache-warm.yml seeds, on main, the setup-go caches ci.yml's jobs
// restore. setup-go restores by exact key, and the key is a function of
// the runner (runs-on), the resolved Go version and the hash of
// cache-dependency-path (default go.sum), so a warm job only helps when
// those three inputs match a ci.yml job's. The checks below read both
// workflows with the line parser release_test.go uses (parseJobs).

const cacheWarmWorkflow = ".github/workflows/cache-warm.yml"

// cacheWarmExempt lists the ci.yml jobs that keep a setup-go cache but are
// deliberately not warmed, with the reason. A new cached ci.yml job must
// get a cache-warm.yml job or an entry here.
var cacheWarmExempt = map[string]string{
	"fmt-vet":     "lint cache keyed on go.sum + Makefile; cheap, rebuilt per branch",
	"staticcheck": "lint cache keyed on go.sum + Makefile; cheap, rebuilt per branch",
	"build":       "ubuntu `**/go.sum` key; make build + smoke is cheap cold",
}

// setupGoKey is the part of setup-go's cache key a workflow controls.
type setupGoKey struct {
	runsOn, goVersion, depPath string
}

func (k setupGoKey) String() string {
	return "runs-on " + k.runsOn + ", go-version " + k.goVersion + ", cache-dependency-path " + k.depPath
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

// setupGoCacheKey returns the job's setup-go cache key, and false when the
// job has no setup-go step or sets `cache: false`.
func setupGoCacheKey(j wfJob) (setupGoKey, bool) {
	runsOn, _ := j.key("runs-on")
	k := setupGoKey{runsOn: runsOn, depPath: "go.sum"}
	found := false
	for i := 0; i < len(j.lines); i++ {
		at := strings.Index(j.lines[i], "uses: actions/setup-go@")
		if at < 0 {
			continue
		}
		found = true
		// `with:` sits at the `uses:` key's column; its keys are deeper.
		for i++; i < len(j.lines) && indentOf(j.lines[i]) >= at; i++ {
			if strings.TrimSpace(j.lines[i]) != "with:" {
				continue
			}
			wi := indentOf(j.lines[i])
			for i++; i < len(j.lines) && indentOf(j.lines[i]) > wi; i++ {
				name, val, _ := strings.Cut(strings.TrimSpace(j.lines[i]), ":")
				val = strings.Trim(strings.TrimSpace(val), `"'`)
				if val == "|" { // block scalar: the deeper lines
					ki := indentOf(j.lines[i])
					var parts []string
					for i+1 < len(j.lines) && indentOf(j.lines[i+1]) > ki {
						i++
						parts = append(parts, strings.TrimSpace(j.lines[i]))
					}
					val = strings.Join(parts, " ")
				}
				switch name {
				case "go-version":
					k.goVersion = resolveMatrix(j, val)
				case "cache":
					if val == "false" {
						return k, false
					}
				case "cache-dependency-path":
					k.depPath = val
				}
			}
			break
		}
		break
	}
	return k, found
}

// resolveMatrix maps a `${{ matrix.<axis> }}` value to the job's matrix
// list when that list has one entry (`go-version: ["1.26"]`).
func resolveMatrix(j wfJob, val string) string {
	axis, ok := strings.CutPrefix(val, "${{ matrix.")
	if !ok {
		return val
	}
	axis = strings.TrimSuffix(axis, " }}")
	for _, l := range j.lines {
		v, ok := strings.CutPrefix(strings.TrimSpace(l), axis+": [")
		if !ok {
			continue
		}
		items := strings.Split(strings.TrimSuffix(v, "]"), ",")
		if len(items) == 1 {
			return strings.Trim(strings.TrimSpace(items[0]), `"'`)
		}
	}
	return val
}

// cacheWarmProblems: every cached ci.yml job outside cacheWarmExempt has a
// cache-warm.yml job with its key, every warm job's key is read by some
// ci.yml job, and every exemption names a cached ci.yml job.
func cacheWarmProblems(ciSrc, warmSrc string) []string {
	var probs []string
	readers := map[setupGoKey][]string{}
	cached := map[string]bool{}
	for _, j := range parseJobs(ciSrc) {
		if k, ok := setupGoCacheKey(j); ok {
			readers[k] = append(readers[k], j.name)
			cached[j.name] = true
		}
	}
	warmed := map[setupGoKey]bool{}
	for _, j := range parseJobs(warmSrc) {
		k, ok := setupGoCacheKey(j)
		if !ok {
			continue
		}
		warmed[k] = true
		if len(readers[k]) == 0 {
			probs = append(probs, cacheWarmWorkflow+": job "+j.name+" warms a key no ci.yml job restores ("+k.String()+")")
		}
	}
	for k, jobs := range readers {
		if warmed[k] {
			continue
		}
		for _, name := range jobs {
			if _, ok := cacheWarmExempt[name]; !ok {
				probs = append(probs, "ci.yml job "+name+" restores a setup-go key no cache-warm.yml job warms ("+k.String()+"); add a warm job or a cacheWarmExempt entry")
			}
		}
	}
	for name := range cacheWarmExempt {
		if !cached[name] {
			probs = append(probs, "cacheWarmExempt names "+name+", which is not a ci.yml job with a setup-go cache")
		}
	}
	slices.Sort(probs)
	return probs
}

const cacheWarmCIFixture = `jobs:
  fmt-vet:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache-dependency-path: |
            go.sum
            Makefile
  staticcheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache-dependency-path: |
            go.sum
            Makefile
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache-dependency-path: "**/go.sum"
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ["1.26"]
    steps:
      - uses: actions/checkout@v7
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: ${{ matrix.go-version }}
  test-windows:
    runs-on: windows-latest
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache: false # no warm needed
  smoke-macos:
    runs-on: macos-latest
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache-dependency-path: "**/go.sum"
`

const cacheWarmFixture = `jobs:
  drop-stale:
    runs-on: ubuntu-latest
    steps:
      - run: gh cache list
  warm:
    needs: drop-stale
    runs-on: ubuntu-latest
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: "1.26"
  warm-smoke-macos:
    runs-on: macos-latest
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version: "1.26"
          cache-dependency-path: "**/go.sum"
`

func TestSetupGoCacheKey(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	for _, j := range parseJobs(cacheWarmCIFixture) {
		if k, ok := setupGoCacheKey(j); ok {
			got[j.name] = k.String()
		} else {
			got[j.name] = "none"
		}
	}
	want := map[string]string{
		"fmt-vet":      "runs-on ubuntu-latest, go-version 1.26, cache-dependency-path go.sum Makefile",
		"staticcheck":  "runs-on ubuntu-latest, go-version 1.26, cache-dependency-path go.sum Makefile",
		"build":        "runs-on ubuntu-latest, go-version 1.26, cache-dependency-path **/go.sum",
		"test":         "runs-on ubuntu-latest, go-version 1.26, cache-dependency-path go.sum",
		"test-windows": "none",
		"smoke-macos":  "runs-on macos-latest, go-version 1.26, cache-dependency-path **/go.sum",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: key %q, want %q", name, got[name], w)
		}
	}
}

func TestCacheWarmProblems(t *testing.T) {
	t.Parallel()
	if p := cacheWarmProblems(cacheWarmCIFixture, cacheWarmFixture); len(p) != 0 {
		t.Fatalf("in-sync fixture: %v", p)
	}
	cases := map[string]struct {
		ci, warm, want string
	}{
		"missing warm job": {
			ci:   cacheWarmCIFixture,
			warm: strings.Split(cacheWarmFixture, "  warm-smoke-macos:")[0],
			want: "ci.yml job smoke-macos restores a setup-go key no cache-warm.yml job warms",
		},
		"warm job on the wrong dependency path": {
			ci:   cacheWarmCIFixture,
			warm: strings.Replace(cacheWarmFixture, `"**/go.sum"`, `go.sum`, 1),
			want: ".github/workflows/cache-warm.yml: job warm-smoke-macos warms a key no ci.yml job restores",
		},
		"warm job on the wrong runner": {
			ci:   cacheWarmCIFixture,
			warm: strings.Replace(cacheWarmFixture, "runs-on: macos-latest", "runs-on: macos-13", 1),
			want: ".github/workflows/cache-warm.yml: job warm-smoke-macos warms a key no ci.yml job restores",
		},
		"warm job on another go-version": {
			ci:   cacheWarmCIFixture,
			warm: strings.Replace(cacheWarmFixture, `go-version: "1.26"`, `go-version: "1.25"`, 1),
			want: "ci.yml job test restores a setup-go key no cache-warm.yml job warms",
		},
		"stale exemption": {
			ci:   strings.Replace(cacheWarmCIFixture, "  build:", "  build-all:", 1),
			warm: cacheWarmFixture,
			want: "cacheWarmExempt names build, which is not a ci.yml job with a setup-go cache",
		},
	}
	for name, c := range cases {
		probs := cacheWarmProblems(c.ci, c.warm)
		if !slices.ContainsFunc(probs, func(p string) bool { return strings.HasPrefix(p, c.want) }) {
			t.Errorf("%s: problems %v, want one starting %q", name, probs, c.want)
		}
	}
}

// TestRepoCacheWarm_MatchesCachedJobs runs the check on the real
// workflows: a new cached ci.yml job (or a changed setup-go input on one)
// must come with its cache-warm.yml job.
func TestRepoCacheWarm_MatchesCachedJobs(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	warm, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cacheWarmWorkflow)))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cacheWarmProblems(string(ci), string(warm)) {
		t.Error(p)
	}
}
